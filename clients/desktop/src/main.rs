#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod config;
mod discovery;
mod mcp_proxy;
mod probe;
mod updater;

use config::{AppConfig, SavedTopology, ServerProfile};
use discovery::DiscoveredCluster;
use http::Response;
use mcp_proxy::LocalMcpProxy;
use muda::{Menu, MenuEvent, MenuItem, PredefinedMenuItem, Submenu};
use serde::Deserialize;
use std::borrow::Cow;
use std::process::Command;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tao::dpi::LogicalSize;
use tao::event::{Event, WindowEvent};
use tao::event_loop::{ControlFlow, EventLoopBuilder};
use tao::window::{Icon, WindowBuilder};
use updater::ClientUpdateInfo;
use wry::{NewWindowResponse, WebContext, WebView, WebViewBuilder};

const CLIENT_VERSION: &str = "V1.11.25.2";
const SETUP_HTML: &str = include_str!("../ui/setup.html");
const LOGO_PNG: &[u8] = include_bytes!("../assets/logo.png");

#[cfg(target_os = "windows")]
const SETUP_URL: &str = "http://quailnest.localhost/";
#[cfg(not(target_os = "windows"))]
const SETUP_URL: &str = "quailnest://localhost/";

#[cfg(target_os = "windows")]
const SETUP_ORIGIN_PREFIX: &str = "http://quailnest.localhost";
#[cfg(not(target_os = "windows"))]
const SETUP_ORIGIN_PREFIX: &str = "quailnest://localhost";

#[derive(Debug)]
enum UserEvent {
    IpcMessage {
        origin_url: String,
        body: String,
    },
    ProbeDone(probe::ProbeResult),
    LanScanDone {
        clusters: Vec<DiscoveredCluster>,
        auto_enter_single: bool,
    },
    ConnectResolved {
        url: String,
        probe: probe::ProbeResult,
        topology: Option<SavedTopology>,
        reason: String,
    },
    StartupUnreachable {
        url: String,
        reason: String,
    },
    TopologySynced(SavedTopology),
    ServerSwitched {
        url: String,
        version: String,
        topology: Option<SavedTopology>,
        reason: String,
    },
    UpdateCheckDone {
        ok: bool,
        info: Option<ClientUpdateInfo>,
        error: String,
    },
    UpdateProgress {
        pct: u8,
        msg: String,
        error: String,
    },
    UpdateInstalledExit,
    MenuAction(String),
}

#[derive(Debug, Deserialize)]
struct IpcCmd {
    cmd: String,
    #[serde(default)]
    scheme: String,
    #[serde(default)]
    host: String,
    #[serde(default)]
    port: String,
    #[serde(default)]
    label: String,
    #[serde(default)]
    view_mode: String,
    #[serde(default)]
    auto_connect: Option<bool>,
    #[serde(default)]
    enabled: Option<bool>,
    #[serde(default)]
    auto_enter_single: Option<bool>,
    #[serde(default)]
    base_url: String,
    #[serde(default)]
    download_url: String,
    #[serde(default)]
    sha256: String,
    #[serde(default)]
    event: Option<serde_json::Value>,
}

fn load_window_icon() -> Option<Icon> {
    let decoded = image::load_from_memory(LOGO_PNG).ok()?.into_rgba8();
    let (w, h) = decoded.dimensions();
    Icon::from_rgba(decoded.into_raw(), w, h).ok()
}

fn open_in_system_browser(url: &str) {
    let clean = url.trim();
    if !clean.starts_with("http://") && !clean.starts_with("https://") {
        return;
    }
    #[cfg(target_os = "windows")]
    {
        let _ = Command::new("cmd").args(["/C", "start", "", clean]).spawn();
    }
    #[cfg(target_os = "macos")]
    {
        let _ = Command::new("open").arg(clean).spawn();
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        let _ = Command::new("xdg-open").arg(clean).spawn();
    }
}

fn push_setup_state(webview: &WebView, cfg: &AppConfig) {
    let payload = serde_json::json!({
        "client_version": CLIENT_VERSION,
        "config": cfg,
        "config_path": config::config_file_path().to_string_lossy(),
    });
    if let Ok(json_str) = serde_json::to_string(&payload) {
        let _ = webview.evaluate_script(&format!(
            "if (window.__qnHydrate) {{ window.__qnHydrate({json_str}); }}"
        ));
    }
}

fn show_in_app_toast(webview: &WebView, text: &str) {
    if let Ok(msg) = serde_json::to_string(text) {
        let script = format!(
            r#"(function(t){{
              try {{
                var el = document.getElementById('__qn_toast');
                if (!el) {{
                  el = document.createElement('div');
                  el.id = '__qn_toast';
                  el.style.cssText = 'position:fixed;top:14px;right:16px;z-index:999999;background:rgba(18,18,22,0.94);color:#fafafa;border:1px solid #3f3f46;border-radius:10px;padding:10px 14px;font-size:13px;box-shadow:0 10px 25px rgba(0,0,0,0.45);transition:opacity .25s ease;';
                  document.body.appendChild(el);
                }}
                el.textContent = t;
                el.style.opacity = '1';
                clearTimeout(window.__qnToastTimer);
                window.__qnToastTimer = setTimeout(function(){{ el.style.opacity = '0'; }}, 4200);
              }} catch (e) {{}}
            }})({msg});"#
        );
        let _ = webview.evaluate_script(&script);
    }
}

fn apply_connected_profile(
    cfg: &mut AppConfig,
    base_url: &str,
    version: &str,
    label: &str,
    topo: Option<SavedTopology>,
) -> Option<ServerProfile> {
    let parsed_scheme = if base_url.starts_with("https://") {
        "https"
    } else {
        "http"
    };
    let Ok(mut profile) = config::normalize_profile(parsed_scheme, base_url, "", label) else {
        return None;
    };
    if !version.trim().is_empty() {
        profile.version = version.trim().to_string();
    }
    config::upsert_server(cfg, profile.clone());
    if let Some(t) = topo {
        config::update_topology(cfg, t);
    }
    let _ = config::save_config(cfg);
    Some(profile)
}

fn main() {
    updater::cleanup_old_binary();

    let event_loop = EventLoopBuilder::<UserEvent>::with_user_event().build();
    let proxy = event_loop.create_proxy();

    let mut window_builder = WindowBuilder::new()
        .with_title(&format!("QuailNest 鹌鹑窝 — 桌面客户端 ({CLIENT_VERSION})"))
        .with_inner_size(LogicalSize::new(1280.0, 820.0))
        .with_min_inner_size(LogicalSize::new(860.0, 600.0));

    if let Some(icon) = load_window_icon() {
        window_builder = window_builder.with_window_icon(Some(icon));
    }

    let window = window_builder.build(&event_loop).expect("failed to build desktop window");

    // Top Native Menu Bar
    let menu = Menu::new();
    let server_menu = Submenu::new("服务器", true);
    let item_edit_server = MenuItem::with_id("open_setup", "修改 IP / 切换或扫描服务器\tCtrl+,", true, None);
    let item_scan_lan = MenuItem::with_id("scan_lan", "立即扫描局域网服务器 (8080/UDP)", true, None);
    let item_reconnect = MenuItem::with_id("reconnect", "智能重连主服务器", true, None);
    let item_open_browser = MenuItem::with_id("open_browser", "在系统浏览器中打开当前工作区", true, None);
    let item_toggle_mcp = MenuItem::with_id("toggle_mcp", "开关本机固定 MCP 代理 (127.0.0.1:18080)", true, None);
    let item_quit = MenuItem::with_id("quit", "退出客户端", true, None);
    let _ = server_menu.append_items(&[
        &item_edit_server,
        &item_scan_lan,
        &item_reconnect,
        &item_open_browser,
        &PredefinedMenuItem::separator(),
        &item_toggle_mcp,
        &PredefinedMenuItem::separator(),
        &item_quit,
    ]);

    let view_menu = Submenu::new("视图", true);
    let item_reload = MenuItem::with_id("reload", "刷新页面\tF5", true, None);
    let item_back = MenuItem::with_id("go_back", "后退\tAlt+Left", true, None);
    let item_forward = MenuItem::with_id("go_forward", "前进\tAlt+Right", true, None);
    let item_mode_desktop = MenuItem::with_id("mode_desktop", "切换为桌面版视图", true, None);
    let item_mode_mobile = MenuItem::with_id("mode_mobile", "切换为移动版视图", true, None);
    let _ = view_menu.append_items(&[
        &item_reload,
        &item_back,
        &item_forward,
        &PredefinedMenuItem::separator(),
        &item_mode_desktop,
        &item_mode_mobile,
    ]);

    let help_menu = Submenu::new("帮助", true);
    let item_check_update = MenuItem::with_id("check_update", "检查桌面客户端更新", true, None);
    let item_show_info = MenuItem::with_id("show_info", "查看连接、拓扑缓存与 MCP 代理信息", true, None);
    let _ = help_menu.append_items(&[&item_check_update, &item_show_info]);

    let _ = menu.append_items(&[&server_menu, &view_menu, &help_menu]);

    #[cfg(target_os = "windows")]
    {
        use tao::platform::windows::WindowExtWindows;
        unsafe {
            let _ = menu.init_for_hwnd(window.hwnd() as _);
        }
    }
    #[cfg(target_os = "macos")]
    {
        menu.init_for_nsapp();
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        use tao::platform::unix::WindowExtUnix;
        let _ = menu.init_for_gtk_window(window.gtk_window(), window.default_vbox());
    }

    let menu_proxy = proxy.clone();
    MenuEvent::set_event_handler(Some(move |event: MenuEvent| {
        let _ = menu_proxy.send_event(UserEvent::MenuAction(event.id.0.clone()));
    }));

    let mut cfg = config::load_config();
    let initial_base = cfg
        .current
        .as_ref()
        .map(|c| c.base_url())
        .unwrap_or_default();

    let mcp_proxy = LocalMcpProxy::new(&initial_base, cfg.mcp_proxy_enabled);
    if cfg.mcp_proxy_enabled {
        let _ = mcp_proxy.start(cfg.mcp_proxy_port);
    }

    let shared_origin = Arc::new(Mutex::new(initial_base));
    let shared_origin_new_win = shared_origin.clone();
    let shared_cfg = Arc::new(Mutex::new(cfg.clone()));
    let in_workspace = Arc::new(AtomicBool::new(false));
    let switching_busy = Arc::new(AtomicBool::new(false));

    let mut web_context = WebContext::new(Some(config::webview_data_dir()));
    let ipc_proxy = proxy.clone();
    let webview = WebViewBuilder::new_with_web_context(&mut web_context)
        .with_url(SETUP_URL)
        .with_custom_protocol("quailnest".into(), |_id, request| {
            let path = request.uri().path();
            if path == "/logo.png" {
                Response::builder()
                    .status(200)
                    .header("Content-Type", "image/png")
                    .body(Cow::Borrowed(LOGO_PNG))
                    .unwrap()
            } else {
                Response::builder()
                    .status(200)
                    .header("Content-Type", "text/html; charset=utf-8")
                    .body(Cow::Borrowed(SETUP_HTML.as_bytes()))
                    .unwrap()
            }
        })
        .with_initialization_script(
            r#"
            (function () {
              if (window.__qnDesktopKeysInstalled) return;
              window.__qnDesktopKeysInstalled = true;
              window.addEventListener('keydown', function (e) {
                if ((e.ctrlKey || e.metaKey) && e.key === ',') {
                  e.preventDefault();
                  if (window.ipc && window.ipc.postMessage) {
                    window.ipc.postMessage(JSON.stringify({ cmd: 'open_setup' }));
                  }
                } else if (e.key === 'F5') {
                  e.preventDefault();
                  location.reload();
                }
              }, true);
            })();
            "#,
        )
        .with_ipc_handler(move |req| {
            let _ = ipc_proxy.send_event(UserEvent::IpcMessage {
                origin_url: req.uri().to_string(),
                body: req.body().to_string(),
            });
        })
        .with_new_window_req_handler(move |url, _features| {
            let allow_same = shared_origin_new_win
                .lock()
                .ok()
                .map(|g| !g.is_empty() && url.starts_with(g.as_str()))
                .unwrap_or(false);
            if allow_same {
                NewWindowResponse::Allow
            } else {
                open_in_system_browser(&url);
                NewWindowResponse::Deny
            }
        })
        .build(&window)
        .expect("failed to build WebView");

    // Startup: auto-scan LAN when no saved server, or 4-level resolve when auto_connect is enabled
    {
        let startup_proxy = proxy.clone();
        let startup_cfg = cfg.clone();
        std::thread::spawn(move || {
            if startup_cfg.current.is_none() {
                let clusters = discovery::scan_lan_clusters();
                if clusters.len() == 1 {
                    let c = &clusters[0];
                    if let Some((u, res)) = probe::pick_fastest_entry(&c.entry_urls, 2) {
                        let topo = probe::fetch_topology(&u, 2);
                        let _ = startup_proxy.send_event(UserEvent::ConnectResolved {
                            url: u,
                            probe: res,
                            topology: topo,
                            reason: "lan_auto".to_string(),
                        });
                        return;
                    }
                }
                let _ = startup_proxy.send_event(UserEvent::LanScanDone {
                    clusters,
                    auto_enter_single: true,
                });
                return;
            }

            if startup_cfg.auto_connect {
                if let Some((url, res, topo, stage)) = probe::four_level_resolve(&startup_cfg) {
                    let _ = startup_proxy.send_event(UserEvent::ConnectResolved {
                        url,
                        probe: res,
                        topology: topo,
                        reason: stage.to_string(),
                    });
                } else {
                    let saved_url = startup_cfg
                        .current
                        .as_ref()
                        .map(|c| c.base_url())
                        .unwrap_or_default();
                    let _ = startup_proxy.send_event(UserEvent::StartupUnreachable {
                        url: saved_url,
                        reason: "原地址、其余网卡及备选节点均暂不可达，已触发局域网扫描定位".to_string(),
                    });
                    let clusters = discovery::scan_lan_clusters();
                    let _ = startup_proxy.send_event(UserEvent::LanScanDone {
                        clusters,
                        auto_enter_single: false,
                    });
                }
            }
        });
    }

    // Background 10s Topology Sync + Silent Multi-NIC/Failover Monitor + Auto-Switchback to Primary
    {
        let mon_proxy = proxy.clone();
        let mon_origin = shared_origin.clone();
        let mon_cfg = shared_cfg.clone();
        let mon_in_ws = in_workspace.clone();
        let mon_busy = switching_busy.clone();
        std::thread::spawn(move || {
            let mut fail_streak = 0u32;
            loop {
                std::thread::sleep(Duration::from_secs(10));
                if !mon_in_ws.load(Ordering::SeqCst) || mon_busy.load(Ordering::SeqCst) {
                    fail_streak = 0;
                    continue;
                }
                let cur_base = mon_origin
                    .lock()
                    .ok()
                    .map(|g| g.clone())
                    .unwrap_or_default();
                if cur_base.is_empty() {
                    continue;
                }

                if let Some(topo) = probe::fetch_topology(&cur_base, 3) {
                    fail_streak = 0;
                    let prev_failover = mon_cfg
                        .lock()
                        .ok()
                        .and_then(|c| c.topology.as_ref().map(|t| t.failover_active))
                        .unwrap_or(false);

                    let active_contains_cur = topo
                        .active_entry_urls
                        .iter()
                        .any(|u| u.trim_end_matches('/').eq_ignore_ascii_case(&cur_base));

                    let _ = mon_proxy.send_event(UserEvent::TopologySynced(topo.clone()));

                    // Check if primary recovered (!failover_active after failover) or Entry moved off cur_base
                    if (prev_failover && !topo.failover_active && !topo.home_primary_urls.is_empty())
                        || (!active_contains_cur && !topo.active_entry_urls.is_empty())
                    {
                        let targets = if !topo.failover_active && !topo.home_primary_urls.is_empty() {
                            &topo.home_primary_urls
                        } else {
                            &topo.active_entry_urls
                        };
                        if let Some((best_url, p_res)) = probe::pick_fastest_entry(targets, 2) {
                            if !best_url.eq_ignore_ascii_case(&cur_base) {
                                let reason = if prev_failover && !topo.failover_active {
                                    "primary_restored".to_string()
                                } else {
                                    "entry_switched".to_string()
                                };
                                let _ = mon_proxy.send_event(UserEvent::ServerSwitched {
                                    url: best_url,
                                    version: p_res.version,
                                    topology: Some(topo),
                                    reason,
                                });
                            }
                        }
                    }
                } else {
                    fail_streak += 1;
                    if fail_streak >= 2 && mon_in_ws.load(Ordering::SeqCst) {
                        if let Some(cfg_snap) = mon_cfg.lock().ok().map(|c| c.clone()) {
                            if let Some((new_url, p_res, fresh_topo, stage)) = probe::four_level_resolve(&cfg_snap) {
                                fail_streak = 0;
                                if !new_url.eq_ignore_ascii_case(&cur_base) {
                                    let _ = mon_proxy.send_event(UserEvent::ServerSwitched {
                                        url: new_url,
                                        version: p_res.version,
                                        topology: fresh_topo,
                                        reason: stage.to_string(),
                                    });
                                }
                            }
                        }
                    }
                }
            }
        });
    }

    let mut auto_connect_cancelled_by_user = false;

    event_loop.run(move |event, _, control_flow| {
        *control_flow = ControlFlow::Wait;

        let switch_workspace_to = |cfg: &mut AppConfig, url: &str, ver: &str, label: &str, topo: Option<SavedTopology>, toast_msg: Option<&str>| {
            if let Some(prof) = apply_connected_profile(cfg, url, ver, label, topo) {
                if let Ok(mut g) = shared_cfg.lock() {
                    *g = cfg.clone();
                }
                if let Ok(mut g) = shared_origin.lock() {
                    *g = prof.base_url();
                }
                mcp_proxy.set_target(&prof.base_url());
                in_workspace.store(true, Ordering::SeqCst);
                let target = prof.entry_url(&cfg.view_mode);
                window.set_title(&format!("QuailNest 鹌鹑窝 ({CLIENT_VERSION}) — {}", prof.display_title()));
                let _ = webview.load_url(&target);
                if let Some(t) = toast_msg {
                    show_in_app_toast(&webview, t);
                }
            }
        };

        match event {
            Event::WindowEvent {
                event: WindowEvent::CloseRequested,
                ..
            } => {
                *control_flow = ControlFlow::Exit;
            }
            Event::UserEvent(user_event) => match user_event {
                UserEvent::ConnectResolved {
                    url,
                    probe: p_res,
                    topology,
                    reason,
                } => {
                    if auto_connect_cancelled_by_user {
                        return;
                    }
                    let notice = match reason.as_str() {
                        "lan_auto" => Some("已自动发现唯一局域网服务器并连接"),
                        "alternate_nic" => Some("原 IP 不可用，已自动切换至主节点另一张网卡入口"),
                        "failover_node" => Some("原主节点离线，已自动切换至高可用备用主节点"),
                        "lan_recovery" => Some("通过局域网重扫找回集群入口"),
                        _ => None,
                    };
                    let lbl = p_res.node_name.clone();
                    switch_workspace_to(&mut cfg, &url, &p_res.version, &lbl, topology, notice);
                }
                UserEvent::StartupUnreachable { url, reason } => {
                    if auto_connect_cancelled_by_user {
                        return;
                    }
                    push_setup_state(&webview, &cfg);
                    if let (Ok(u_json), Ok(e_json)) = (serde_json::to_string(&url), serde_json::to_string(&reason)) {
                        let _ = webview.evaluate_script(&format!(
                            "if (window.__qnSetOfflineBanner) {{ window.__qnSetOfflineBanner({u_json}, {e_json}); }}"
                        ));
                    }
                }
                UserEvent::LanScanDone {
                    clusters,
                    auto_enter_single,
                } => {
                    if auto_enter_single && clusters.len() == 1 && !auto_connect_cancelled_by_user {
                        let c = &clusters[0];
                        let ver = c.version.clone();
                        let name = c.name.clone();
                        let entry = c.entry_url.clone();
                        let urls = c.entry_urls.clone();
                        let sw_proxy = proxy.clone();
                        std::thread::spawn(move || {
                            if let Some((u, res)) = probe::pick_fastest_entry(&urls, 2) {
                                let topo = probe::fetch_topology(&u, 2);
                                let _ = sw_proxy.send_event(UserEvent::ConnectResolved {
                                    url: u,
                                    probe: res,
                                    topology: topo,
                                    reason: "lan_auto".to_string(),
                                });
                            } else {
                                let _ = sw_proxy.send_event(UserEvent::LanScanDone {
                                    clusters: vec![DiscoveredCluster {
                                        cluster_id: String::new(),
                                        name,
                                        entry_url: entry,
                                        entry_urls: urls,
                                        version: ver,
                                        online_nodes: 1,
                                        total_nodes: 1,
                                        latency_ms: 1,
                                        source: "lan".into(),
                                    }],
                                    auto_enter_single: false,
                                });
                            }
                        });
                        return;
                    }
                    if let Ok(json_str) = serde_json::to_string(&clusters) {
                        let _ = webview.evaluate_script(&format!(
                            "if (window.__qnScanResult) {{ window.__qnScanResult({json_str}); }}"
                        ));
                    }
                }
                UserEvent::TopologySynced(topo) => {
                    config::update_topology(&mut cfg, topo);
                    let _ = config::save_config(&cfg);
                    if let Ok(mut g) = shared_cfg.lock() {
                        *g = cfg.clone();
                    }
                }
                UserEvent::ServerSwitched {
                    url,
                    version,
                    topology,
                    reason,
                } => {
                    if !in_workspace.load(Ordering::SeqCst) {
                        return;
                    }
                    let msg = if reason == "primary_restored" {
                        "主节点已恢复上线，已自动切回主服务器"
                    } else if reason == "alternate_nic" {
                        "已切换至主节点另一可用网卡地址"
                    } else {
                        "主节点地址已变更，已自动切换至新入口"
                    };
                    switch_workspace_to(&mut cfg, &url, &version, "", topology, Some(msg));
                }
                UserEvent::ProbeDone(res) => {
                    if let Ok(json_str) = serde_json::to_string(&res) {
                        let _ = webview.evaluate_script(&format!(
                            "if (window.__qnProbeResult) {{ window.__qnProbeResult({json_str}); }}"
                        ));
                    }
                }
                UserEvent::UpdateCheckDone { ok, info, error } => {
                    let payload = serde_json::json!({
                        "ok": ok,
                        "info": info,
                        "error": error,
                    });
                    if let Ok(json_str) = serde_json::to_string(&payload) {
                        let _ = webview.evaluate_script(&format!(
                            "if (window.__qnUpdateResult) {{ window.__qnUpdateResult({json_str}); }}"
                        ));
                    }
                    if !in_workspace.load(Ordering::SeqCst) {
                        return;
                    }
                    if let Some(i) = info {
                        if i.has_update {
                            show_in_app_toast(
                                &webview,
                                &format!("发现新版本桌面端 {}，按 Ctrl+, 返回配置页即可一键升级", i.latest_version),
                            );
                        } else {
                            show_in_app_toast(&webview, &format!("桌面客户端已是最新 ({CLIENT_VERSION})"));
                        }
                    } else if !error.is_empty() {
                        show_in_app_toast(&webview, &format!("检查更新失败：{error}"));
                    }
                }
                UserEvent::UpdateProgress { pct, msg, error } => {
                    if let (Ok(m_json), Ok(e_json)) = (serde_json::to_string(&msg), serde_json::to_string(&error)) {
                        let _ = webview.evaluate_script(&format!(
                            "if (window.__qnUpdateProgress) {{ window.__qnUpdateProgress({pct}, {m_json}, {e_json}); }}"
                        ));
                    }
                }
                UserEvent::UpdateInstalledExit => {
                    *control_flow = ControlFlow::Exit;
                }
                UserEvent::IpcMessage { origin_url, body } => {
                    let Ok(parsed) = serde_json::from_str::<IpcCmd>(&body) else {
                        return;
                    };

                    if parsed.cmd == "open_setup" {
                        auto_connect_cancelled_by_user = true;
                        in_workspace.store(false, Ordering::SeqCst);
                        window.set_title(&format!("QuailNest 鹌鹑窝 — 连接配置 ({CLIENT_VERSION})"));
                        let _ = webview.load_url(SETUP_URL);
                        return;
                    }

                    // Instant SSE event bridge from connected QuailNest web app (failback / topology update)
                    if parsed.cmd == "server_event" {
                        if let Some(ev) = parsed.event {
                            let kind = ev.get("kind").and_then(|v| v.as_str()).unwrap_or("");
                            let reason = ev.get("reason").and_then(|v| v.as_str()).unwrap_or("");
                            if let Some(topo_val) = ev.get("topology") {
                                if let Ok(topo) = serde_json::from_value::<SavedTopology>(topo_val.clone()) {
                                    config::update_topology(&mut cfg, topo);
                                    let _ = config::save_config(&cfg);
                                    if let Ok(mut g) = shared_cfg.lock() {
                                        *g = cfg.clone();
                                    }
                                }
                            }
                            if kind == "entry_switched" {
                                let mut candidates = Vec::new();
                                if let Some(arr) = ev.get("entry_urls").and_then(|v| v.as_array()) {
                                    for item in arr {
                                        if let Some(s) = item.as_str() {
                                            candidates.push(s.to_string());
                                        }
                                    }
                                }
                                if let Some(single) = ev.get("entry_url").and_then(|v| v.as_str()) {
                                    if !single.is_empty() && !candidates.iter().any(|x| x == single) {
                                        candidates.insert(0, single.to_string());
                                    }
                                }
                                if !candidates.is_empty() && !switching_busy.swap(true, Ordering::SeqCst) {
                                    let sw_proxy = proxy.clone();
                                    let busy_flag = switching_busy.clone();
                                    let reason_str = reason.to_string();
                                    std::thread::spawn(move || {
                                        if let Some((best, p_res)) = probe::pick_fastest_entry(&candidates, 2) {
                                            let topo = probe::fetch_topology(&best, 2);
                                            let _ = sw_proxy.send_event(UserEvent::ServerSwitched {
                                                url: best,
                                                version: p_res.version,
                                                topology: topo,
                                                reason: reason_str,
                                            });
                                        }
                                        busy_flag.store(false, Ordering::SeqCst);
                                    });
                                }
                            }
                        }
                        return;
                    }

                    if !origin_url.starts_with(SETUP_ORIGIN_PREFIX) {
                        return;
                    }

                    match parsed.cmd.as_str() {
                        "get_state" => {
                            push_setup_state(&webview, &cfg);
                        }
                        "scan_lan" => {
                            let auto_single = parsed.auto_enter_single.unwrap_or(false);
                            let sc_proxy = proxy.clone();
                            std::thread::spawn(move || {
                                let clusters = discovery::scan_lan_clusters();
                                let _ = sc_proxy.send_event(UserEvent::LanScanDone {
                                    clusters,
                                    auto_enter_single: auto_single,
                                });
                            });
                        }
                        "set_mcp_proxy" => {
                            let en = parsed.enabled.unwrap_or(false);
                            cfg.mcp_proxy_enabled = en;
                            mcp_proxy.set_enabled(en);
                            if en {
                                if let Err(err) = mcp_proxy.start(cfg.mcp_proxy_port) {
                                    if let Ok(msg) = serde_json::to_string(&err) {
                                        let _ = webview.evaluate_script(&format!(
                                            "if (window.__qnSetError) {{ window.__qnSetError({msg}); }}"
                                        ));
                                    }
                                    return;
                                }
                            }
                            let _ = config::save_config(&cfg);
                            if let Ok(mut g) = shared_cfg.lock() {
                                *g = cfg.clone();
                            }
                        }
                        "test_connection" => {
                            auto_connect_cancelled_by_user = true;
                            match config::normalize_profile(&parsed.scheme, &parsed.host, &parsed.port, &parsed.label) {
                                Ok(profile) => {
                                    let p_proxy = proxy.clone();
                                    let base = profile.base_url();
                                    std::thread::spawn(move || {
                                        let res = probe::probe_server(&base, 4);
                                        let _ = p_proxy.send_event(UserEvent::ProbeDone(res));
                                    });
                                }
                                Err(err) => {
                                    if let Ok(msg) = serde_json::to_string(&err) {
                                        let _ = webview.evaluate_script(&format!(
                                            "if (window.__qnSetError) {{ window.__qnSetError({msg}); }}"
                                        ));
                                    }
                                }
                            }
                        }
                        "save_and_connect" => {
                            auto_connect_cancelled_by_user = true;
                            match config::normalize_profile(&parsed.scheme, &parsed.host, &parsed.port, &parsed.label) {
                                Ok(profile) => {
                                    if let Some(ac) = parsed.auto_connect {
                                        cfg.auto_connect = ac;
                                    }
                                    if parsed.view_mode == "mobile" || parsed.view_mode == "desktop" {
                                        cfg.view_mode = parsed.view_mode;
                                    }
                                    let base = profile.base_url();
                                    let quick_probe = probe::probe_server(&base, 2);
                                    let topo = if quick_probe.ok {
                                        probe::fetch_topology(&base, 2)
                                    } else {
                                        None
                                    };
                                    switch_workspace_to(&mut cfg, &base, &quick_probe.version, &profile.label, topo, None);
                                }
                                Err(err) => {
                                    if let Ok(msg) = serde_json::to_string(&err) {
                                        let _ = webview.evaluate_script(&format!(
                                            "if (window.__qnSetError) {{ window.__qnSetError({msg}); }}"
                                        ));
                                    }
                                }
                            }
                        }
                        "auto_failover_reconnect" => {
                            let cfg_snap = cfg.clone();
                            let r_proxy = proxy.clone();
                            std::thread::spawn(move || {
                                if let Some((url, res, topo, stage)) = probe::four_level_resolve(&cfg_snap) {
                                    let _ = r_proxy.send_event(UserEvent::ConnectResolved {
                                        url,
                                        probe: res,
                                        topology: topo,
                                        reason: stage.to_string(),
                                    });
                                } else if let Some(msg) = Some("所有已知主/备节点地址均暂不可达") {
                                    let _ = r_proxy.send_event(UserEvent::StartupUnreachable {
                                        url: cfg_snap.current.map(|c| c.base_url()).unwrap_or_default(),
                                        reason: msg.to_string(),
                                    });
                                }
                            });
                        }
                        "delete_history" => {
                            auto_connect_cancelled_by_user = true;
                            config::remove_history(&mut cfg, &parsed.base_url);
                            let _ = config::save_config(&cfg);
                            if let Ok(mut g) = shared_cfg.lock() {
                                *g = cfg.clone();
                            }
                            push_setup_state(&webview, &cfg);
                        }
                        "check_update" => {
                            let active = cfg.current.as_ref().map(|c| c.base_url());
                            let u_proxy = proxy.clone();
                            std::thread::spawn(move || {
                                match updater::check_client_update(active.as_deref(), CLIENT_VERSION) {
                                    Ok(info) => {
                                        let _ = u_proxy.send_event(UserEvent::UpdateCheckDone {
                                            ok: true,
                                            info: Some(info),
                                            error: String::new(),
                                        });
                                    }
                                    Err(err) => {
                                        let _ = u_proxy.send_event(UserEvent::UpdateCheckDone {
                                            ok: false,
                                            info: None,
                                            error: err,
                                        });
                                    }
                                }
                            });
                        }
                        "apply_update" => {
                            let dl_url = parsed.download_url.clone();
                            let sha = parsed.sha256.clone();
                            let u_proxy = proxy.clone();
                            std::thread::spawn(move || {
                                let p_cb = u_proxy.clone();
                                match updater::download_and_apply_windows(&dl_url, &sha, move |pct| {
                                    let _ = p_cb.send_event(UserEvent::UpdateProgress {
                                        pct,
                                        msg: format!("正在下载并校验桌面端更新 ({pct}%)..."),
                                        error: String::new(),
                                    });
                                }) {
                                    Ok(()) => {
                                        let _ = u_proxy.send_event(UserEvent::UpdateInstalledExit);
                                    }
                                    Err(err) => {
                                        let _ = u_proxy.send_event(UserEvent::UpdateProgress {
                                            pct: 0,
                                            msg: String::new(),
                                            error: err,
                                        });
                                    }
                                }
                            });
                        }
                        _ => {}
                    }
                }
                UserEvent::MenuAction(action) => match action.as_str() {
                    "open_setup" => {
                        auto_connect_cancelled_by_user = true;
                        in_workspace.store(false, Ordering::SeqCst);
                        window.set_title(&format!("QuailNest 鹌鹑窝 — 连接配置 ({CLIENT_VERSION})"));
                        let _ = webview.load_url(SETUP_URL);
                    }
                    "scan_lan" => {
                        auto_connect_cancelled_by_user = true;
                        in_workspace.store(false, Ordering::SeqCst);
                        let _ = webview.load_url(SETUP_URL);
                        let sc_proxy = proxy.clone();
                        let _ = webview.evaluate_script("if (window.__qnScanStarting) window.__qnScanStarting();");
                        std::thread::spawn(move || {
                            let clusters = discovery::scan_lan_clusters();
                            let _ = sc_proxy.send_event(UserEvent::LanScanDone {
                                clusters,
                                auto_enter_single: false,
                            });
                        });
                    }
                    "reconnect" => {
                        auto_connect_cancelled_by_user = false;
                        let cfg_snap = cfg.clone();
                        let r_proxy = proxy.clone();
                        std::thread::spawn(move || {
                            if let Some((url, res, topo, stage)) = probe::four_level_resolve(&cfg_snap) {
                                let _ = r_proxy.send_event(UserEvent::ConnectResolved {
                                    url,
                                    probe: res,
                                    topology: topo,
                                    reason: stage.to_string(),
                                });
                            }
                        });
                    }
                    "open_browser" => {
                        if let Some(cur) = &cfg.current {
                            open_in_system_browser(&cur.entry_url(&cfg.view_mode));
                        }
                    }
                    "toggle_mcp" => {
                        let next = !cfg.mcp_proxy_enabled;
                        cfg.mcp_proxy_enabled = next;
                        mcp_proxy.set_enabled(next);
                        if next {
                            let _ = mcp_proxy.start(cfg.mcp_proxy_port);
                        }
                        let _ = config::save_config(&cfg);
                        if in_workspace.load(Ordering::SeqCst) {
                            let port = cfg.mcp_proxy_port;
                            let msg = if next {
                                format!("本机 MCP 代理已开启：http://127.0.0.1:{port}/mcp")
                            } else {
                                "本机固定 MCP 代理已关闭".to_string()
                            };
                            show_in_app_toast(&webview, &msg);
                        } else {
                            push_setup_state(&webview, &cfg);
                        }
                    }
                    "check_update" => {
                        let active = cfg.current.as_ref().map(|c| c.base_url());
                        let u_proxy = proxy.clone();
                        std::thread::spawn(move || {
                            match updater::check_client_update(active.as_deref(), CLIENT_VERSION) {
                                Ok(info) => {
                                    let _ = u_proxy.send_event(UserEvent::UpdateCheckDone {
                                        ok: true,
                                        info: Some(info),
                                        error: String::new(),
                                    });
                                }
                                Err(err) => {
                                    let _ = u_proxy.send_event(UserEvent::UpdateCheckDone {
                                        ok: false,
                                        info: None,
                                        error: err,
                                    });
                                }
                            }
                        });
                    }
                    "reload" => {
                        let _ = webview.evaluate_script("location.reload();");
                    }
                    "go_back" => {
                        let _ = webview.evaluate_script("history.back();");
                    }
                    "go_forward" => {
                        let _ = webview.evaluate_script("history.forward();");
                    }
                    "mode_desktop" => {
                        cfg.view_mode = "desktop".to_string();
                        let _ = config::save_config(&cfg);
                        if let Some(cur) = cfg.current.clone() {
                            let _ = webview.load_url(&cur.entry_url("desktop"));
                        }
                    }
                    "mode_mobile" => {
                        cfg.view_mode = "mobile".to_string();
                        let _ = config::save_config(&cfg);
                        if let Some(cur) = cfg.current.clone() {
                            let _ = webview.load_url(&cur.entry_url("mobile"));
                        }
                    }
                    "show_info" => {
                        let server_desc = cfg
                            .current
                            .as_ref()
                            .map(|c| c.base_url())
                            .unwrap_or_else(|| "尚未配置".to_string());
                        let topo_nodes = cfg
                            .topology
                            .as_ref()
                            .map(|t| t.nodes.len())
                            .unwrap_or(0);
                        let mcp_state = if cfg.mcp_proxy_enabled {
                            format!("已启用 (http://127.0.0.1:{}/mcp)", cfg.mcp_proxy_port)
                        } else {
                            "未启用".to_string()
                        };
                        let info_text = format!(
                            "QuailNest Desktop {CLIENT_VERSION}\n当前连接：{server_desc}\n已缓存集群节点：{topo_nodes} 个\n本机固定 MCP 代理：{mcp_state}\n配置文件：{}\n快捷键 Ctrl+, 可随时修改或扫描局域网服务器。",
                            config::config_file_path().display()
                        );
                        if let Ok(msg) = serde_json::to_string(&info_text) {
                            let _ = webview.evaluate_script(&format!("alert({msg});"));
                        }
                    }
                    "quit" => {
                        *control_flow = ControlFlow::Exit;
                    }
                    _ => {}
                },
            },
            _ => {}
        }
    });
}
