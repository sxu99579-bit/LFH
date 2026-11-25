use crate::config::{AppConfig, SavedTopology};
use crate::discovery;
use serde::{Deserialize, Serialize};
use std::collections::BTreeSet;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Serialize)]
pub struct ProbeResult {
    pub ok: bool,
    pub url: String,
    pub app_name: String,
    pub version: String,
    pub role: String,
    pub cluster_id: String,
    pub node_id: String,
    pub node_name: String,
    pub entry_url: String,
    pub entry_urls: Vec<String>,
    pub epoch: u64,
    pub failover_active: bool,
    pub online_nodes: usize,
    pub total_nodes: usize,
    pub latency_ms: u64,
    pub error: String,
}

#[derive(Debug, Deserialize)]
struct ApiInfoPool {
    #[serde(default)]
    online_nodes: usize,
    #[serde(default)]
    nodes: Vec<serde_json::Value>,
}

#[derive(Debug, Deserialize)]
struct ApiInfoPayload {
    #[serde(default)]
    app_name: String,
    #[serde(default)]
    version: String,
    #[serde(default)]
    role: String,
    #[serde(default)]
    cluster_id: String,
    #[serde(default)]
    node_id: String,
    #[serde(default)]
    node_name: String,
    #[serde(default)]
    entry_url: String,
    #[serde(default)]
    entry_urls: Vec<String>,
    #[serde(default)]
    epoch: u64,
    #[serde(default)]
    failover_active: bool,
    #[serde(default)]
    pool: Option<ApiInfoPool>,
}

fn empty_fail_probe(clean_base: &str, latency_ms: u64, error: String) -> ProbeResult {
    ProbeResult {
        ok: false,
        url: clean_base.to_string(),
        app_name: String::new(),
        version: String::new(),
        role: String::new(),
        cluster_id: String::new(),
        node_id: String::new(),
        node_name: String::new(),
        entry_url: String::new(),
        entry_urls: Vec::new(),
        epoch: 0,
        failover_active: false,
        online_nodes: 0,
        total_nodes: 0,
        latency_ms,
        error,
    }
}

pub fn probe_server(base_url: &str, timeout_secs: u64) -> ProbeResult {
    let clean_base = base_url.trim_end_matches('/');
    let info_url = format!("{clean_base}/api/info");
    let start = Instant::now();

    let agent_cfg = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(timeout_secs.max(1))))
        .build();
    let agent: ureq::Agent = agent_cfg.into();

    let mut response = match agent.get(&info_url).call() {
        Ok(resp) => resp,
        Err(err) => {
            return empty_fail_probe(
                clean_base,
                start.elapsed().as_millis() as u64,
                friendly_ureq_error(&err.to_string()),
            );
        }
    };

    let status = response.status().as_u16();
    if status < 200 || status >= 300 {
        return empty_fail_probe(
            clean_base,
            start.elapsed().as_millis() as u64,
            format!("服务端返回异常状态码 HTTP {status}"),
        );
    }

    let body = match response.body_mut().read_to_string() {
        Ok(s) => s,
        Err(err) => {
            return empty_fail_probe(
                clean_base,
                start.elapsed().as_millis() as u64,
                format!("读取服务端响应失败: {err}"),
            );
        }
    };

    let parsed: ApiInfoPayload = match serde_json::from_str(&body) {
        Ok(v) => v,
        Err(_) => {
            return empty_fail_probe(
                clean_base,
                start.elapsed().as_millis() as u64,
                "目标服务返回的不是 QuailNest / LFH 的 /api/info JSON 数据，请检查 IP 和端口".to_string(),
            );
        }
    };

    if parsed.version.trim().is_empty() && parsed.app_name.trim().is_empty() {
        return empty_fail_probe(
            clean_base,
            start.elapsed().as_millis() as u64,
            "未识别为有效的 QuailNest 服务端（缺少版本或应用标识）".to_string(),
        );
    }

    let (online_nodes, total_nodes) = match parsed.pool {
        Some(p) => (p.online_nodes, p.nodes.len()),
        None => (1, 1),
    };

    ProbeResult {
        ok: true,
        url: clean_base.to_string(),
        app_name: if parsed.app_name.is_empty() {
            "QuailNest".to_string()
        } else {
            parsed.app_name
        },
        version: parsed.version,
        role: parsed.role,
        cluster_id: parsed.cluster_id,
        node_id: parsed.node_id,
        node_name: parsed.node_name,
        entry_url: parsed.entry_url,
        entry_urls: parsed.entry_urls,
        epoch: parsed.epoch,
        failover_active: parsed.failover_active,
        online_nodes,
        total_nodes,
        latency_ms: start.elapsed().as_millis() as u64,
        error: String::new(),
    }
}

pub fn fetch_topology(base_url: &str, timeout_secs: u64) -> Option<SavedTopology> {
    let clean_base = base_url.trim_end_matches('/');
    if clean_base.is_empty() {
        return None;
    }
    let agent_cfg = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(timeout_secs.max(1))))
        .build();
    let agent: ureq::Agent = agent_cfg.into();
    let mut resp = agent.get(&format!("{clean_base}/api/client/topology")).call().ok()?;
    if resp.status().as_u16() < 200 || resp.status().as_u16() >= 300 {
        return None;
    }
    let body = resp.body_mut().read_to_string().ok()?;
    serde_json::from_str::<SavedTopology>(&body).ok()
}

pub fn pick_fastest_entry(urls: &[String], timeout_secs: u64) -> Option<(String, ProbeResult)> {
    let mut seen = BTreeSet::new();
    let mut clean_urls = Vec::new();
    for raw in urls {
        let u = raw.trim().trim_end_matches('/').to_string();
        if !u.is_empty() && seen.insert(u.clone()) {
            clean_urls.push(u);
        }
    }
    if clean_urls.is_empty() {
        return None;
    }

    let best: Arc<Mutex<Option<(String, ProbeResult)>>> = Arc::new(Mutex::new(None));
    let mut handles = Vec::with_capacity(clean_urls.len());
    for u in clean_urls {
        let best_c = best.clone();
        handles.push(std::thread::spawn(move || {
            let res = probe_server(&u, timeout_secs);
            if res.ok && (res.role.is_empty() || res.role == "entry") {
                if let Ok(mut g) = best_c.lock() {
                    let take = match &*g {
                        None => true,
                        Some((_, prev)) => res.latency_ms < prev.latency_ms,
                    };
                    if take {
                        *g = Some((u, res));
                    }
                }
            }
        }));
    }
    for h in handles {
        let _ = h.join();
    }
    best.lock().ok().and_then(|g| g.clone())
}

/// Executes 4-level connection & failover resolution:
/// 1) current saved URL -> 2) other saved NIC IPs of Entry -> 3) peer nodes in topology -> 4) LAN scan.
pub fn four_level_resolve(cfg: &AppConfig) -> Option<(String, ProbeResult, Option<SavedTopology>, &'static str)> {
    let mut level1 = Vec::new();
    if let Some(cur) = &cfg.current {
        level1.push(cur.base_url());
    }
    if let Some((u, r)) = pick_fastest_entry(&level1, 2) {
        let topo = fetch_topology(&u, 2);
        return Some((u, r, topo, "current"));
    }

    // Level 2: primary's other enabled NIC URLs
    if let Some(topo) = &cfg.topology {
        let mut nic_urls = Vec::new();
        nic_urls.extend(topo.active_entry_urls.clone());
        nic_urls.extend(topo.home_primary_urls.clone());
        if let Some((u, r)) = pick_fastest_entry(&nic_urls, 2) {
            let fresh_topo = fetch_topology(&u, 2);
            return Some((u, r, fresh_topo, "alternate_nic"));
        }

        // Level 3: cluster peer nodes in topology priority order
        let mut node_urls = Vec::new();
        for node in &topo.nodes {
            node_urls.extend(node.urls.clone());
        }
        if let Some((u, r)) = pick_fastest_entry(&node_urls, 2) {
            let fresh_topo = fetch_topology(&u, 2);
            return Some((u, r, fresh_topo, "failover_node"));
        }
    }

    // Level 4: LAN 8080 + UDP scan fallback matching saved cluster_id (or single match)
    let target_cid = cfg
        .topology
        .as_ref()
        .map(|t| t.cluster_id.clone())
        .unwrap_or_default();
    let discovered = discovery::scan_lan_clusters();
    if !target_cid.is_empty() {
        if let Some(hit) = discovered.iter().find(|c| c.cluster_id == target_cid) {
            if let Some((u, r)) = pick_fastest_entry(&hit.entry_urls, 2) {
                let fresh_topo = fetch_topology(&u, 2);
                return Some((u, r, fresh_topo, "lan_recovery"));
            }
        }
    }
    None
}

fn friendly_ureq_error(raw: &str) -> String {
    let lower = raw.to_ascii_lowercase();
    if lower.contains("timed out") || lower.contains("timeout") {
        "连接超时，请确认局域网设备在线且防火墙已放行对应端口".to_string()
    } else if lower.contains("refused") || lower.contains("10061") {
        "连接被拒绝，请检查服务端是否已启动以及端口号是否正确".to_string()
    } else if lower.contains("dns") || lower.contains("failed to lookup") || lower.contains("11001") {
        "无法解析主机地址，请检查输入的 IP 或域名".to_string()
    } else {
        format!("无法连接服务器: {raw}")
    }
}
