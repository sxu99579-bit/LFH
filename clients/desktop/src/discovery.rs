use crate::probe;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeSet, HashMap};
use std::net::{IpAddr, Ipv4Addr, SocketAddr, TcpStream, UdpSocket};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

const UDP_DISCOVERY_PORT: u16 = 53319;
const DEFAULT_HTTP_PORT: u16 = 8080;

#[derive(Debug, Clone, Serialize)]
pub struct DiscoveredCluster {
    pub cluster_id: String,
    pub name: String,
    pub entry_url: String,
    pub entry_urls: Vec<String>,
    pub version: String,
    pub online_nodes: usize,
    pub total_nodes: usize,
    pub latency_ms: u64,
    pub source: String,
}

#[derive(Debug, Deserialize)]
struct UdpBeaconPacket {
    #[serde(default)]
    cluster_hash: String,
    #[serde(default)]
    node_name: String,
    #[serde(default)]
    http_url: String,
    #[serde(default)]
    entry_url: String,
}

pub fn is_scannable_private_ipv4(ip: Ipv4Addr) -> bool {
    if ip.is_loopback() || ip.is_link_local() || ip.is_unspecified() || ip.is_multicast() {
        return false;
    }
    let oct = ip.octets();
    // Skip Clash/Mihomo TUN 198.18.0.0/15
    if oct[0] == 198 && (oct[1] == 18 || oct[1] == 19) {
        return false;
    }
    // 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
    oct[0] == 10
        || (oct[0] == 172 && (16..=31).contains(&oct[1]))
        || (oct[0] == 192 && oct[1] == 168)
}

pub fn extract_local_private_ipv4s() -> Vec<Ipv4Addr> {
    let mut ips = BTreeSet::new();
    // Routing probe finds the preferred outbound LAN IP cleanly cross-platform
    for target in ["192.168.1.1:80", "10.0.0.1:80", "8.8.8.8:80"] {
        if let Ok(sock) = UdpSocket::bind("0.0.0.0:0") {
            if sock.connect(target).is_ok() {
                if let Ok(SocketAddr::V4(v4)) = sock.local_addr() {
                    if is_scannable_private_ipv4(*v4.ip()) {
                        ips.insert(*v4.ip());
                    }
                }
            }
        }
    }

    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        use std::process::Command;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        if let Ok(out) = Command::new("ipconfig")
            .creation_flags(CREATE_NO_WINDOW)
            .output()
        {
            let text = String::from_utf8_lossy(&out.stdout);
            for line in text.lines() {
                if !line.contains("IPv4") && !line.contains("192.168.") && !line.contains("10.") && !line.contains("172.") {
                    continue;
                }
                if let Some((_, right)) = line.rsplit_once(':') {
                    let candidate = right.trim().trim_end_matches("(Preferred)").trim();
                    if let Ok(ip) = candidate.parse::<Ipv4Addr>() {
                        if is_scannable_private_ipv4(ip) {
                            ips.insert(ip);
                        }
                    }
                }
            }
        }
    }

    ips.into_iter().collect()
}

fn collect_scan_candidates() -> Vec<SocketAddr> {
    let mut addrs = Vec::new();
    let mut seen_ips = BTreeSet::new();

    // Always probe localhost 8080 first for single-machine setups
    let loopback = IpAddr::V4(Ipv4Addr::LOCALHOST);
    seen_ips.insert(loopback);
    addrs.push(SocketAddr::new(loopback, DEFAULT_HTTP_PORT));

    let local_ips = extract_local_private_ipv4s();
    let mut prefixes: BTreeSet<[u8; 3]> = BTreeSet::new();
    for ip in &local_ips {
        let addr = IpAddr::V4(*ip);
        if seen_ips.insert(addr) {
            addrs.push(SocketAddr::new(addr, DEFAULT_HTTP_PORT));
        }
        let o = ip.octets();
        prefixes.insert([o[0], o[1], o[2]]);
        if prefixes.len() >= 3 {
            break;
        }
    }

    for p in prefixes {
        for host_byte in 1..=254u8 {
            let ip = IpAddr::V4(Ipv4Addr::new(p[0], p[1], p[2], host_byte));
            if seen_ips.insert(ip) {
                addrs.push(SocketAddr::new(ip, DEFAULT_HTTP_PORT));
            }
        }
    }
    addrs
}

fn listen_udp_beacons(duration: Duration) -> Vec<String> {
    let mut found = Vec::new();
    let Ok(sock) = UdpSocket::bind(("0.0.0.0", UDP_DISCOVERY_PORT)) else {
        return found;
    };
    let _ = sock.set_read_timeout(Some(Duration::from_millis(200)));
    let deadline = Instant::now() + duration;
    let mut buf = [0u8; 4096];
    let mut seen = BTreeSet::new();

    while Instant::now() < deadline {
        match sock.recv_from(&mut buf) {
            Ok((n, _src)) if n > 0 => {
                if let Ok(pkt) = serde_json::from_slice::<UdpBeaconPacket>(&buf[..n]) {
                    let candidate = if !pkt.entry_url.trim().is_empty() {
                        pkt.entry_url.trim().trim_end_matches('/').to_string()
                    } else {
                        pkt.http_url.trim().trim_end_matches('/').to_string()
                    };
                    if candidate.starts_with("http://") || candidate.starts_with("https://") {
                        if seen.insert(candidate.clone()) {
                            found.push(candidate);
                        }
                    }
                    let _ = (&pkt.cluster_hash, &pkt.node_name);
                }
            }
            _ => {}
        }
    }
    found
}

pub fn merge_discovered_results(entries: Vec<(probe::ProbeResult, String)>) -> Vec<DiscoveredCluster> {
    let mut by_key: HashMap<String, DiscoveredCluster> = HashMap::new();
    for (res, src) in entries {
        if !res.ok {
            continue;
        }
        let canonical_url = if !res.entry_url.trim().is_empty() {
            res.entry_url.trim_end_matches('/').to_string()
        } else {
            res.url.trim_end_matches('/').to_string()
        };
        let key = if !res.cluster_id.trim().is_empty() {
            res.cluster_id.clone()
        } else {
            canonical_url.clone()
        };

        let mut all_urls = Vec::new();
        if !canonical_url.is_empty() {
            all_urls.push(canonical_url.clone());
        }
        for u in &res.entry_urls {
            let cu = u.trim_end_matches('/').to_string();
            if !cu.is_empty() && !all_urls.contains(&cu) {
                all_urls.push(cu);
            }
        }
        let direct = res.url.trim_end_matches('/').to_string();
        if !direct.is_empty() && !all_urls.contains(&direct) {
            all_urls.push(direct.clone());
        }

        let name = if !res.node_name.trim().is_empty() {
            res.node_name.clone()
        } else if !res.app_name.trim().is_empty() {
            res.app_name.clone()
        } else {
            "QuailNest 集群".to_string()
        };

        match by_key.get_mut(&key) {
            Some(existing) => {
                for u in all_urls {
                    if !existing.entry_urls.contains(&u) {
                        existing.entry_urls.push(u);
                    }
                }
                if res.role == "entry" && !direct.is_empty() && res.latency_ms < existing.latency_ms {
                    existing.entry_url = direct;
                    existing.latency_ms = res.latency_ms;
                }
                if !existing.source.contains(&src) {
                    existing.source = format!("{}+{}", existing.source, src);
                }
            }
            None => {
                by_key.insert(
                    key.clone(),
                    DiscoveredCluster {
                        cluster_id: key,
                        name,
                        entry_url: if !canonical_url.is_empty() {
                            canonical_url
                        } else {
                            direct
                        },
                        entry_urls: all_urls,
                        version: res.version,
                        online_nodes: res.online_nodes.max(1),
                        total_nodes: res.total_nodes.max(1),
                        latency_ms: res.latency_ms,
                        source: src,
                    },
                );
            }
        }
    }

    let mut out: Vec<DiscoveredCluster> = by_key.into_values().collect();
    out.sort_by(|a, b| a.latency_ms.cmp(&b.latency_ms).then_with(|| a.entry_url.cmp(&b.entry_url)));
    out
}

/// Scans both UDP discovery beacons (53319) and local private /24 subnets on default port 8080.
pub fn scan_lan_clusters() -> Vec<DiscoveredCluster> {
    let udp_handle = std::thread::spawn(|| listen_udp_beacons(Duration::from_millis(1200)));

    let candidates = collect_scan_candidates();
    let idx = Arc::new(Mutex::new(0usize));
    let hits: Arc<Mutex<Vec<(probe::ProbeResult, String)>>> = Arc::new(Mutex::new(Vec::new()));
    let worker_count = 48usize.min(candidates.len().max(1));

    let mut handles = Vec::with_capacity(worker_count);
    for _ in 0..worker_count {
        let cands = candidates.clone();
        let idx_c = idx.clone();
        let hits_c = hits.clone();
        handles.push(std::thread::spawn(move || loop {
            let next = {
                let mut g = match idx_c.lock() {
                    Ok(v) => v,
                    Err(_) => return,
                };
                if *g >= cands.len() {
                    return;
                }
                let pos = *g;
                *g += 1;
                cands[pos]
            };

            if TcpStream::connect_timeout(&next, Duration::from_millis(260)).is_ok() {
                let url = format!("http://{}:{}", next.ip(), next.port());
                let res = probe::probe_server(&url, 2);
                if res.ok {
                    if let Ok(mut list) = hits_c.lock() {
                        list.push((res, "port-8080".to_string()));
                    }
                }
            }
        }));
    }

    for h in handles {
        let _ = h.join();
    }
    let udp_urls = udp_handle.join().unwrap_or_default();

    let mut all_hits = match hits.lock() {
        Ok(g) => g.clone(),
        Err(_) => Vec::new(),
    };
    for u in udp_urls {
        let res = probe::probe_server(&u, 2);
        if res.ok {
            all_hits.push((res, "udp".to_string()));
        }
    }

    merge_discovered_results(all_hits)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn private_ipv4_filter_excludes_loopback_linklocal_and_tun() {
        assert!(!is_scannable_private_ipv4(Ipv4Addr::LOCALHOST));
        assert!(!is_scannable_private_ipv4(Ipv4Addr::new(169, 254, 1, 5)));
        assert!(!is_scannable_private_ipv4(Ipv4Addr::new(198, 18, 0, 1)));
        assert!(!is_scannable_private_ipv4(Ipv4Addr::new(8, 8, 8, 8)));
        assert!(is_scannable_private_ipv4(Ipv4Addr::new(192, 168, 3, 55)));
        assert!(is_scannable_private_ipv4(Ipv4Addr::new(10, 0, 0, 8)));
        assert!(is_scannable_private_ipv4(Ipv4Addr::new(172, 20, 1, 2)));
    }

    #[test]
    fn merge_deduplicates_clusters_by_cluster_id() {
        let r1 = probe::ProbeResult {
            ok: true,
            url: "http://192.168.1.10:8080".to_string(),
            app_name: "QuailNest".to_string(),
            version: "V1.11.25.2".to_string(),
            role: "entry".to_string(),
            cluster_id: "c-abc".to_string(),
            node_id: "n1".to_string(),
            node_name: "Home-NAS".to_string(),
            entry_url: "http://192.168.1.10:8080".to_string(),
            entry_urls: vec!["http://192.168.1.10:8080".to_string(), "http://10.0.0.10:8080".to_string()],
            epoch: 2,
            failover_active: false,
            online_nodes: 2,
            total_nodes: 2,
            latency_ms: 8,
            error: String::new(),
        };
        let r2 = probe::ProbeResult {
            url: "http://10.0.0.10:8080".to_string(),
            latency_ms: 4,
            ..r1.clone()
        };
        let merged = merge_discovered_results(vec![(r1, "port-8080".into()), (r2, "udp".into())]);
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].cluster_id, "c-abc");
        assert_eq!(merged[0].entry_urls.len(), 2);
    }
}
