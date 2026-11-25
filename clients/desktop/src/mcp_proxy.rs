use std::io::{BufRead, BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream, ToSocketAddrs};
use std::sync::atomic::{AtomicBool, AtomicU16, Ordering};
use std::sync::{Arc, RwLock};
use std::time::Duration;

#[derive(Clone)]
pub struct LocalMcpProxy {
    target_base_url: Arc<RwLock<String>>,
    enabled: Arc<AtomicBool>,
    listening_port: Arc<AtomicU16>,
    shutdown: Arc<AtomicBool>,
}

impl LocalMcpProxy {
    pub fn new(initial_target: &str, enabled: bool) -> Self {
        Self {
            target_base_url: Arc::new(RwLock::new(initial_target.trim_end_matches('/').to_string())),
            enabled: Arc::new(AtomicBool::new(enabled)),
            listening_port: Arc::new(AtomicU16::new(0)),
            shutdown: Arc::new(AtomicBool::new(false)),
        }
    }

    pub fn set_target(&self, base_url: &str) {
        if let Ok(mut g) = self.target_base_url.write() {
            *g = base_url.trim().trim_end_matches('/').to_string();
        }
    }

    pub fn set_enabled(&self, enabled: bool) {
        self.enabled.store(enabled, Ordering::SeqCst);
    }

    pub fn is_enabled(&self) -> bool {
        self.enabled.load(Ordering::SeqCst)
    }

    pub fn active_port(&self) -> u16 {
        self.listening_port.load(Ordering::SeqCst)
    }

    pub fn start(&self, port: u16) -> Result<u16, String> {
        let existing = self.listening_port.load(Ordering::SeqCst);
        if existing > 0 {
            return Ok(existing);
        }
        let listener = TcpListener::bind(("127.0.0.1", port))
            .map_err(|e| format!("绑定本地 MCP 代理端口 127.0.0.1:{port} 失败: {e}"))?;
        let bound_port = listener
            .local_addr()
            .map(|a| a.port())
            .unwrap_or(port);
        let _ = listener.set_nonblocking(true);
        self.listening_port.store(bound_port, Ordering::SeqCst);

        let target_ref = self.target_base_url.clone();
        let enabled_ref = self.enabled.clone();
        let shutdown_ref = self.shutdown.clone();

        std::thread::spawn(move || {
            while !shutdown_ref.load(Ordering::SeqCst) {
                match listener.accept() {
                    Ok((stream, _)) => {
                        let _ = stream.set_nonblocking(false);
                        if !enabled_ref.load(Ordering::SeqCst) {
                            let mut s = stream;
                            let _ = write_http_error(
                                &mut s,
                                503,
                                "Service Unavailable",
                                r#"{"error":"本地 MCP 代理已关闭，请在 QuailNest 桌面端开启"}"#,
                            );
                            continue;
                        }
                        let target = target_ref
                            .read()
                            .ok()
                            .map(|g| g.clone())
                            .unwrap_or_default();
                        std::thread::spawn(move || {
                            handle_proxy_conn(stream, &target);
                        });
                    }
                    Err(ref e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                        std::thread::sleep(Duration::from_millis(80));
                    }
                    Err(_) => {
                        std::thread::sleep(Duration::from_millis(120));
                    }
                }
            }
        });

        Ok(bound_port)
    }
}

fn write_http_error(stream: &mut TcpStream, code: u16, status_text: &str, json_body: &str) -> std::io::Result<()> {
    let resp = format!(
        "HTTP/1.1 {code} {status_text}\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{json_body}",
        json_body.len()
    );
    stream.write_all(resp.as_bytes())?;
    stream.flush()
}

fn parse_http_host_port(base_url: &str) -> Option<(String, u16)> {
    let clean = base_url.trim();
    let rest = if let Some(s) = clean.strip_prefix("http://") {
        s
    } else if clean.starts_with("https://") {
        return None; // HTTPS upstream not supported in plain TCP forwarder
    } else {
        clean
    };
    let host_port = rest.split(&['/', '?', '#'][..]).next().unwrap_or("");
    if host_port.is_empty() {
        return None;
    }
    if host_port.starts_with('[') {
        let end = host_port.find(']')?;
        let host = host_port[1..end].to_string();
        let after = &host_port[end + 1..];
        let port = if let Some(p) = after.strip_prefix(':') {
            p.parse::<u16>().ok()?
        } else {
            80
        };
        return Some((host, port));
    }
    if let Some((h, p)) = host_port.rsplit_once(':') {
        let port = p.parse::<u16>().ok()?;
        Some((h.to_string(), port))
    } else {
        Some((host_port.to_string(), 80))
    }
}

fn handle_proxy_conn(mut client: TcpStream, target_base: &str) {
    let _ = client.set_read_timeout(Some(Duration::from_secs(30)));
    let mut reader = BufReader::new(match client.try_clone() {
        Ok(c) => c,
        Err(_) => return,
    });

    let mut request_line = String::new();
    if reader.read_line(&mut request_line).unwrap_or(0) == 0 {
        return;
    }
    if request_line.starts_with("GET /_quailnest/proxy-status ") {
        let payload = serde_json::json!({ "ok": true, "target": target_base }).to_string();
        let resp = format!(
            "HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{payload}",
            payload.len()
        );
        let _ = client.write_all(resp.as_bytes());
        return;
    }

    let mut headers = Vec::new();
    loop {
        let mut line = String::new();
        let n = reader.read_line(&mut line).unwrap_or(0);
        if n == 0 || line == "\r\n" || line == "\n" {
            break;
        }
        headers.push(line);
        if headers.len() > 256 {
            let _ = write_http_error(&mut client, 431, "Headers Too Large", r#"{"error":"headers too large"}"#);
            return;
        }
    }

    if target_base.trim().is_empty() {
        let _ = write_http_error(
            &mut client,
            503,
            "Service Unavailable",
            r#"{"error":"当前尚未连接或发现可用的 QuailNest 主服务器"}"#,
        );
        return;
    }

    let Some((host, port)) = parse_http_host_port(target_base) else {
        let _ = write_http_error(
            &mut client,
            502,
            "Bad Gateway",
            r#"{"error":"本地 MCP 代理暂仅支持局域网 HTTP 服务端转发"}"#,
        );
        return;
    };

    let Ok(mut addrs) = (host.as_str(), port).to_socket_addrs() else {
        let _ = write_http_error(&mut client, 502, "Bad Gateway", r#"{"error":"无法解析上游服务器地址"}"#);
        return;
    };
    let Some(sock_addr) = addrs.next() else {
        let _ = write_http_error(&mut client, 502, "Bad Gateway", r#"{"error":"找不到可用网络地址"}"#);
        return;
    };

    let Ok(mut upstream) = TcpStream::connect_timeout(&sock_addr, Duration::from_secs(4)) else {
        let _ = write_http_error(
            &mut client,
            502,
            "Bad Gateway",
            r#"{"error":"上游主服务器当前不可达，正在等待自动切换"}"#,
        );
        return;
    };

    // Disable read timeout so long-lived SSE (/mcp GET) stays open cleanly
    let _ = client.set_read_timeout(None);

    let host_header = if host.contains(':') {
        format!("Host: [{host}]:{port}\r\n")
    } else {
        format!("Host: {host}:{port}\r\n")
    };

    let mut head_buf = Vec::with_capacity(1024);
    head_buf.extend_from_slice(request_line.as_bytes());
    let mut saw_host = false;
    for h in &headers {
        let low = h.to_ascii_lowercase();
        if low.starts_with("host:") {
            head_buf.extend_from_slice(host_header.as_bytes());
            saw_host = true;
        } else if low.starts_with("connection:") {
            continue;
        } else {
            head_buf.extend_from_slice(h.as_bytes());
        }
    }
    if !saw_host {
        head_buf.extend_from_slice(host_header.as_bytes());
    }
    head_buf.extend_from_slice(b"Connection: close\r\n\r\n");

    if upstream.write_all(&head_buf).is_err() {
        return;
    }
    // Drain any buffered body bytes already inside BufReader
    let buffered = reader.buffer().to_vec();
    if !buffered.is_empty() && upstream.write_all(&buffered).is_err() {
        return;
    }

    let mut raw_client_in = reader.into_inner();
    let Ok(mut upstream_read) = upstream.try_clone() else {
        return;
    };
    let Ok(mut client_write) = client.try_clone() else {
        return;
    };

    let pipe_in = std::thread::spawn(move || {
        let _ = std::io::copy(&mut raw_client_in, &mut upstream);
        let _ = upstream.shutdown(Shutdown::Write);
    });

    let mut buf = [0u8; 16384];
    loop {
        match upstream_read.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => {
                if client_write.write_all(&buf[..n]).is_err() || client_write.flush().is_err() {
                    break;
                }
            }
            Err(_) => break,
        }
    }
    let _ = client_write.shutdown(Shutdown::Both);
    let _ = pipe_in.join();
}
