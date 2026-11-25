use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

const MAX_HISTORY: usize = 10;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ServerProfile {
    pub scheme: String,
    pub host: String,
    pub port: u16,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub last_used_at: u64,
}

impl ServerProfile {
    pub fn base_url(&self) -> String {
        let host = if self.host.contains(':') && !self.host.starts_with('[') {
            format!("[{}]", self.host)
        } else {
            self.host.clone()
        };
        format!("{}://{}:{}", self.scheme, host, self.port)
    }

    pub fn entry_url(&self, view_mode: &str) -> String {
        let view = if view_mode.eq_ignore_ascii_case("mobile") {
            "mobile"
        } else {
            "desktop"
        };
        format!("{}/?view={}", self.base_url(), view)
    }

    pub fn display_title(&self) -> String {
        if self.label.trim().is_empty() {
            format!("{}:{}", self.host, self.port)
        } else {
            format!("{} ({}:{})", self.label.trim(), self.host, self.port)
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct TopologyNode {
    pub node_id: String,
    pub node_name: String,
    pub role: String,
    #[serde(default)]
    pub is_home_primary: bool,
    #[serde(default)]
    pub online: bool,
    #[serde(default)]
    pub urls: Vec<String>,
    #[serde(default)]
    pub score: f64,
    #[serde(default)]
    pub version: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
pub struct SavedTopology {
    #[serde(default)]
    pub cluster_id: String,
    #[serde(default)]
    pub epoch: u64,
    #[serde(default)]
    pub home_primary_node_id: String,
    #[serde(default)]
    pub home_primary_urls: Vec<String>,
    #[serde(default)]
    pub active_entry_node_id: String,
    #[serde(default)]
    pub active_entry_urls: Vec<String>,
    #[serde(default)]
    pub failover_active: bool,
    #[serde(default)]
    pub standby_order: Vec<String>,
    #[serde(default)]
    pub nodes: Vec<TopologyNode>,
    #[serde(default)]
    pub updated_at: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AppConfig {
    #[serde(default)]
    pub current: Option<ServerProfile>,
    #[serde(default = "default_auto_connect")]
    pub auto_connect: bool,
    #[serde(default = "default_view_mode")]
    pub view_mode: String,
    #[serde(default)]
    pub history: Vec<ServerProfile>,
    #[serde(default)]
    pub topology: Option<SavedTopology>,
    #[serde(default)]
    pub mcp_proxy_enabled: bool,
    #[serde(default = "default_mcp_proxy_port")]
    pub mcp_proxy_port: u16,
}

fn default_auto_connect() -> bool {
    true
}

fn default_view_mode() -> String {
    "desktop".to_string()
}

fn default_mcp_proxy_port() -> u16 {
    18080
}

impl Default for AppConfig {
    fn default() -> Self {
        Self {
            current: None,
            auto_connect: true,
            view_mode: default_view_mode(),
            history: Vec::new(),
            topology: None,
            mcp_proxy_enabled: false,
            mcp_proxy_port: default_mcp_proxy_port(),
        }
    }
}

pub fn now_unix_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

pub fn config_dir() -> PathBuf {
    #[cfg(target_os = "windows")]
    {
        if let Ok(appdata) = std::env::var("APPDATA") {
            if !appdata.trim().is_empty() {
                return PathBuf::from(appdata).join("QuailNestDesktop");
            }
        }
    }
    #[cfg(target_os = "macos")]
    {
        if let Ok(home) = std::env::var("HOME") {
            if !home.trim().is_empty() {
                return PathBuf::from(home)
                    .join("Library")
                    .join("Application Support")
                    .join("QuailNestDesktop");
            }
        }
    }
    if let Ok(xdg) = std::env::var("XDG_CONFIG_HOME") {
        if !xdg.trim().is_empty() {
            return PathBuf::from(xdg).join("QuailNestDesktop");
        }
    }
    if let Ok(home) = std::env::var("HOME").or_else(|_| std::env::var("USERPROFILE")) {
        return PathBuf::from(home).join(".config").join("QuailNestDesktop");
    }
    PathBuf::from(".").join("QuailNestDesktop")
}

pub fn config_file_path() -> PathBuf {
    config_dir().join("config.json")
}

pub fn webview_data_dir() -> PathBuf {
    #[cfg(target_os = "windows")]
    {
        if let Ok(local_app) = std::env::var("LOCALAPPDATA") {
            if !local_app.trim().is_empty() {
                return PathBuf::from(local_app)
                    .join("QuailNestDesktop")
                    .join("webview2");
            }
        }
    }
    config_dir().join("webview")
}

pub fn load_config() -> AppConfig {
    load_config_from(&config_file_path())
}

pub fn load_config_from(path: &Path) -> AppConfig {
    let Ok(raw) = fs::read_to_string(path) else {
        return AppConfig::default();
    };
    let mut cfg: AppConfig = serde_json::from_str(&raw).unwrap_or_default();
    if cfg.view_mode != "desktop" && cfg.view_mode != "mobile" {
        cfg.view_mode = default_view_mode();
    }
    if cfg.mcp_proxy_port == 0 {
        cfg.mcp_proxy_port = default_mcp_proxy_port();
    }
    cfg
}

pub fn update_topology(cfg: &mut AppConfig, topo: SavedTopology) {
    cfg.topology = Some(topo);
}

pub fn save_config(cfg: &AppConfig) -> Result<(), String> {
    save_config_to(&config_file_path(), cfg)
}

pub fn save_config_to(path: &Path, cfg: &AppConfig) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| format!("创建配置目录失败: {e}"))?;
    }
    let body = serde_json::to_string_pretty(cfg).map_err(|e| format!("序列化配置失败: {e}"))?;
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, body).map_err(|e| format!("写入临时配置失败: {e}"))?;
    if path.exists() {
        let _ = fs::remove_file(path);
    }
    fs::rename(&tmp, path).map_err(|e| format!("保存配置文件失败: {e}"))?;
    Ok(())
}

pub fn normalize_profile(
    scheme_input: &str,
    host_input: &str,
    port_input: &str,
    label_input: &str,
) -> Result<ServerProfile, String> {
    let mut scheme = match scheme_input.trim().to_ascii_lowercase().as_str() {
        "https" | "https://" => "https".to_string(),
        _ => "http".to_string(),
    };

    let mut raw = host_input.trim().to_string();
    if raw.is_empty() {
        return Err("请输入服务器 IP 地址或域名".to_string());
    }

    if let Some(rest) = raw.strip_prefix("http://").or_else(|| {
        if raw.to_ascii_lowercase().starts_with("http://") {
            Some(&raw[7..])
        } else {
            None
        }
    }) {
        scheme = "http".to_string();
        raw = rest.to_string();
    } else if let Some(rest) = raw.strip_prefix("https://").or_else(|| {
        if raw.to_ascii_lowercase().starts_with("https://") {
            Some(&raw[8..])
        } else {
            None
        }
    }) {
        scheme = "https".to_string();
        raw = rest.to_string();
    }

    // Strip path / query / fragment if user pasted full URL
    if let Some(idx) = raw.find(&['/', '?', '#'][..]) {
        raw.truncate(idx);
    }
    let raw = raw.trim();
    if raw.is_empty() {
        return Err("服务器地址格式无效".to_string());
    }
    if raw.chars().any(char::is_whitespace) {
        return Err("服务器地址不能包含空格".to_string());
    }

    let mut parsed_port: Option<u16> = None;
    let clean_host: String;

    if raw.starts_with('[') {
        // Bracketed IPv6 e.g. [::1]:8080 or [::1]
        let Some(end_bracket) = raw.find(']') else {
            return Err("IPv6 地址缺少右括号 ']'".to_string());
        };
        let inside = &raw[1..end_bracket];
        if inside.is_empty() {
            return Err("IPv6 地址不能为空".to_string());
        }
        clean_host = inside.to_string();
        let after = &raw[end_bracket + 1..];
        if let Some(p_str) = after.strip_prefix(':') {
            if !p_str.is_empty() {
                let p = p_str
                    .parse::<u16>()
                    .map_err(|_| "粘贴地址中的端口无效（需为 1-65535）".to_string())?;
                if p == 0 {
                    return Err("端口必须在 1 到 65535 之间".to_string());
                }
                parsed_port = Some(p);
            }
        } else if !after.is_empty() {
            return Err("服务器地址格式无效".to_string());
        }
    } else if let Some((h, p_str)) = raw.rsplit_once(':') {
        // Check if single host:port (not bare IPv6 with multiple colons)
        if !h.contains(':') {
            if h.trim().is_empty() {
                return Err("主机名或 IP 地址不能为空".to_string());
            }
            clean_host = h.trim().to_string();
            if !p_str.trim().is_empty() {
                let p = p_str
                    .trim()
                    .parse::<u16>()
                    .map_err(|_| "粘贴地址中的端口无效（需为 1-65535）".to_string())?;
                if p == 0 {
                    return Err("端口必须在 1 到 65535 之间".to_string());
                }
                parsed_port = Some(p);
            }
        } else {
            // Bare IPv6 address without brackets
            clean_host = raw.to_string();
        }
    } else {
        clean_host = raw.to_string();
    }

    let port_trimmed = port_input.trim();
    let final_port = if let Some(p) = parsed_port {
        p
    } else if !port_trimmed.is_empty() {
        let p = port_trimmed
            .parse::<u16>()
            .map_err(|_| "端口必须为 1 到 65535 之间的数字".to_string())?;
        if p == 0 {
            return Err("端口必须在 1 到 65535 之间".to_string());
        }
        p
    } else if scheme == "https" {
        443
    } else {
        8080
    };

    Ok(ServerProfile {
        scheme,
        host: clean_host,
        port: final_port,
        label: label_input.trim().chars().take(48).collect(),
        version: String::new(),
        last_used_at: now_unix_secs(),
    })
}

pub fn upsert_server(cfg: &mut AppConfig, mut profile: ServerProfile) {
    if profile.last_used_at == 0 {
        profile.last_used_at = now_unix_secs();
    }
    let key = profile.base_url();
    if profile.version.is_empty() {
        if let Some(prev) = cfg.history.iter().find(|h| h.base_url() == key) {
            profile.version = prev.version.clone();
            if profile.label.is_empty() {
                profile.label = prev.label.clone();
            }
        }
    }
    cfg.current = Some(profile.clone());
    cfg.history.retain(|item| item.base_url() != key);
    cfg.history.insert(0, profile);
    if cfg.history.len() > MAX_HISTORY {
        cfg.history.truncate(MAX_HISTORY);
    }
}

pub fn remove_history(cfg: &mut AppConfig, base_url: &str) {
    cfg.history.retain(|item| item.base_url() != base_url);
    if let Some(cur) = &cfg.current {
        if cur.base_url() == base_url {
            cfg.current = cfg.history.first().cloned();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_handles_smart_url_and_plain_host() {
        let p1 = normalize_profile("http", "http://192.168.1.88:9090/files?x=1", "8080", "Office").unwrap();
        assert_eq!(p1.scheme, "http");
        assert_eq!(p1.host, "192.168.1.88");
        assert_eq!(p1.port, 9090);
        assert_eq!(p1.base_url(), "http://192.168.1.88:9090");
        assert_eq!(p1.entry_url("desktop"), "http://192.168.1.88:9090/?view=desktop");

        let p2 = normalize_profile("https", "files.local", "", "").unwrap();
        assert_eq!(p2.scheme, "https");
        assert_eq!(p2.host, "files.local");
        assert_eq!(p2.port, 443);

        let p3 = normalize_profile("http", "[::1]:8081", "", "").unwrap();
        assert_eq!(p3.host, "::1");
        assert_eq!(p3.port, 8081);
        assert_eq!(p3.base_url(), "http://[::1]:8081");
    }

    #[test]
    fn normalize_rejects_invalid_inputs() {
        assert!(normalize_profile("http", "", "8080", "").is_err());
        assert!(normalize_profile("http", "192.168.1.1", "0", "").is_err());
        assert!(normalize_profile("http", "192.168.1.1", "99999", "").is_err());
        assert!(normalize_profile("http", "192.168.1.1 host", "8080", "").is_err());
    }

    #[test]
    fn history_dedup_cap_and_persistence() {
        let tmp = std::env::temp_dir().join(format!("qn-desktop-test-{}.json", now_unix_secs()));
        let mut cfg = AppConfig::default();
        for i in 1..=12u16 {
            let p = ServerProfile {
                scheme: "http".to_string(),
                host: format!("192.168.1.{i}"),
                port: 8080,
                label: format!("Node {i}"),
                version: "V1.11.25.1".to_string(),
                last_used_at: i as u64,
            };
            upsert_server(&mut cfg, p);
        }
        assert_eq!(cfg.history.len(), MAX_HISTORY);
        assert_eq!(cfg.current.as_ref().unwrap().host, "192.168.1.12");

        save_config_to(&tmp, &cfg).expect("save config");
        let loaded = load_config_from(&tmp);
        assert_eq!(loaded.history.len(), MAX_HISTORY);
        assert_eq!(loaded.current.as_ref().unwrap().host, "192.168.1.12");

        let mut mod_cfg = loaded;
        update_topology(
            &mut mod_cfg,
            SavedTopology {
                cluster_id: "cid-1".to_string(),
                epoch: 3,
                home_primary_node_id: "node-a".to_string(),
                home_primary_urls: vec!["http://192.168.1.10:8080".to_string()],
                active_entry_node_id: "node-a".to_string(),
                active_entry_urls: vec!["http://192.168.1.10:8080".to_string()],
                failover_active: false,
                standby_order: vec!["node-b".to_string()],
                nodes: vec![TopologyNode {
                    node_id: "node-b".to_string(),
                    node_name: "Node-B".to_string(),
                    role: "worker".to_string(),
                    is_home_primary: false,
                    online: true,
                    urls: vec!["http://192.168.1.11:8080".to_string()],
                    score: 88.5,
                    version: "V1.11.25.2".to_string(),
                }],
                updated_at: "2026-11-25T00:00:00Z".to_string(),
            },
        );
        save_config_to(&tmp, &mod_cfg).expect("save with topology");
        let reloaded_topo = load_config_from(&tmp);
        assert_eq!(reloaded_topo.topology.as_ref().unwrap().epoch, 3);
        assert_eq!(reloaded_topo.mcp_proxy_port, 18080);

        remove_history(&mut mod_cfg, "http://192.168.1.12:8080");
        assert_eq!(mod_cfg.current.as_ref().unwrap().host, "192.168.1.11");
        let _ = fs::remove_file(&tmp);
    }
}
