use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::cmp::Ordering;
use std::fs::{self, File};
use std::io::{Read, Write};
use std::path::Path;
use std::process::Command;
use std::time::Duration;

const GITHUB_LATEST_API: &str = "https://api.github.com/repos/sxu99579-bit/QuailNest/releases/latest";

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ClientUpdateInfo {
    #[serde(default)]
    pub platform: String,
    #[serde(default)]
    pub current_version: String,
    #[serde(default)]
    pub latest_version: String,
    #[serde(default)]
    pub has_update: bool,
    #[serde(default)]
    pub release_url: String,
    #[serde(default)]
    pub release_notes: String,
    #[serde(default)]
    pub published_at: String,
    #[serde(default)]
    pub asset_name: String,
    #[serde(default)]
    pub asset_size: i64,
    #[serde(default)]
    pub asset_url: String,
    #[serde(default)]
    pub sha256: String,
    #[serde(default)]
    pub download_url: String,
    #[serde(default)]
    pub via_server: bool,
}

#[derive(Debug, Deserialize)]
struct GithubAsset {
    name: String,
    size: i64,
    browser_download_url: String,
}

#[derive(Debug, Deserialize)]
struct GithubRelease {
    tag_name: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    body: String,
    #[serde(default)]
    html_url: String,
    #[serde(default)]
    published_at: String,
    #[serde(default)]
    assets: Vec<GithubAsset>,
}

fn parse_ver_nums(raw: &str) -> Vec<u64> {
    let trimmed = raw.trim().trim_start_matches(['v', 'V']);
    let main_part = trimmed.split(&['-', '+'][..]).next().unwrap_or(trimmed);
    main_part
        .split('.')
        .map(|seg| {
            let digits: String = seg.chars().take_while(|c| c.is_ascii_digit()).collect();
            digits.parse::<u64>().unwrap_or(0)
        })
        .collect()
}

pub fn compare_versions(a: &str, b: &str) -> Ordering {
    let va = parse_ver_nums(a);
    let vb = parse_ver_nums(b);
    let max_len = va.len().max(vb.len());
    for i in 0..max_len {
        let pa = *va.get(i).unwrap_or(&0);
        let pb = *vb.get(i).unwrap_or(&0);
        match pa.cmp(&pb) {
            Ordering::Equal => continue,
            non_eq => return non_eq,
        }
    }
    Ordering::Equal
}

pub fn cleanup_old_binary() {
    let Ok(exe) = std::env::current_exe() else {
        return;
    };
    let old = Path::new(&format!("{}.old", exe.display())).to_path_buf();
    if old.exists() {
        for _ in 0..5 {
            if fs::remove_file(&old).is_ok() {
                break;
            }
            std::thread::sleep(Duration::from_millis(200));
        }
    }
}

pub fn check_client_update(active_base_url: Option<&str>, current_version: &str) -> Result<ClientUpdateInfo, String> {
    if let Some(base) = active_base_url {
        let clean = base.trim().trim_end_matches('/');
        if !clean.is_empty() {
            if let Ok(mut info) = check_via_server(clean, current_version) {
                info.via_server = true;
                if info.download_url.starts_with('/') {
                    info.download_url = format!("{clean}{}", info.download_url);
                }
                return Ok(info);
            }
        }
    }
    check_via_github(current_version)
}

fn check_via_server(clean_base: &str, current_version: &str) -> Result<ClientUpdateInfo, String> {
    let url = format!(
        "{clean_base}/api/client/update/check?platform=windows-desktop&current_version={current_version}"
    );
    let agent_cfg = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(12)))
        .build();
    let agent: ureq::Agent = agent_cfg.into();
    let mut resp = agent.get(&url).call().map_err(|e| format!("{e}"))?;
    if resp.status().as_u16() >= 300 {
        return Err(format!("HTTP {}", resp.status().as_u16()));
    }
    let body = resp.body_mut().read_to_string().map_err(|e| format!("{e}"))?;
    serde_json::from_str::<ClientUpdateInfo>(&body).map_err(|e| format!("解析服务器更新数据失败: {e}"))
}

fn check_via_github(current_version: &str) -> Result<ClientUpdateInfo, String> {
    let agent_cfg = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(12)))
        .build();
    let agent: ureq::Agent = agent_cfg.into();
    let mut resp = agent
        .get(GITHUB_LATEST_API)
        .header("Accept", "application/vnd.github+json")
        .header("User-Agent", &format!("QuailNestDesktop/{current_version}"))
        .call()
        .map_err(|e| format!("查询 GitHub 更新失败: {e}"))?;
    if resp.status().as_u16() >= 300 {
        return Err(format!("GitHub 返回异常状态码 HTTP {}", resp.status().as_u16()));
    }
    let body = resp.body_mut().read_to_string().map_err(|e| format!("{e}"))?;
    let rel: GithubRelease =
        serde_json::from_str(&body).map_err(|e| format!("解析 Release JSON 失败: {e}"))?;

    let latest = if !rel.tag_name.trim().is_empty() {
        rel.tag_name.trim().to_string()
    } else {
        rel.name.trim().to_string()
    };

    let mut chosen: Option<&GithubAsset> = None;
    let mut checksums_url = String::new();
    for a in &rel.assets {
        let low = a.name.to_ascii_lowercase();
        if low.ends_with("checksums.txt") {
            checksums_url = a.browser_download_url.clone();
            continue;
        }
        if low.contains("desktop") && low.contains("windows") && low.ends_with(".exe") {
            chosen = Some(a);
        }
    }

    let (asset_name, asset_size, asset_url) = match chosen {
        Some(a) => (a.name.clone(), a.size, a.browser_download_url.clone()),
        None => (String::new(), 0, String::new()),
    };

    let mut expected_sha = String::new();
    if !checksums_url.is_empty() && !asset_name.is_empty() {
        if let Ok(mut cs_resp) = agent.get(&checksums_url).call() {
            if let Ok(cs_body) = cs_resp.body_mut().read_to_string() {
                for line in cs_body.lines() {
                    let parts: Vec<&str> = line.split_whitespace().collect();
                    if parts.len() >= 2 && parts[1].trim_start_matches('*') == asset_name {
                        expected_sha = parts[0].to_ascii_lowercase();
                        break;
                    }
                }
            }
        }
    }

    let has_update = !latest.is_empty()
        && !asset_url.is_empty()
        && compare_versions(&latest, current_version) == Ordering::Greater;

    Ok(ClientUpdateInfo {
        platform: "windows-desktop".to_string(),
        current_version: current_version.to_string(),
        latest_version: latest,
        has_update,
        release_url: rel.html_url,
        release_notes: rel.body,
        published_at: rel.published_at,
        asset_name,
        asset_size,
        asset_url: asset_url.clone(),
        sha256: expected_sha,
        download_url: asset_url,
        via_server: false,
    })
}

pub fn download_and_apply_windows<F>(download_url: &str, expected_sha256: &str, on_progress: F) -> Result<(), String>
where
    F: Fn(u8),
{
    let exe_path = std::env::current_exe().map_err(|e| format!("无法获取当前程序路径: {e}"))?;
    let new_path = Path::new(&format!("{}.new", exe_path.display())).to_path_buf();
    let old_path = Path::new(&format!("{}.old", exe_path.display())).to_path_buf();
    let _ = fs::remove_file(&new_path);

    let agent_cfg = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(300)))
        .build();
    let agent: ureq::Agent = agent_cfg.into();
    let mut resp = agent
        .get(download_url)
        .header("User-Agent", "QuailNestDesktop")
        .call()
        .map_err(|e| format!("下载更新包失败: {e}"))?;
    if resp.status().as_u16() >= 300 {
        return Err(format!("下载返回 HTTP {}", resp.status().as_u16()));
    }

    let header_sha = resp
        .headers()
        .get("X-QuailNest-SHA256")
        .and_then(|v| v.to_str().ok())
        .map(|s| s.trim().to_ascii_lowercase())
        .unwrap_or_default();
    let total_bytes: u64 = resp
        .headers()
        .get("Content-Length")
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.parse::<u64>().ok())
        .unwrap_or(0);

    let target_sha = if !expected_sha256.trim().is_empty() {
        expected_sha256.trim().to_ascii_lowercase()
    } else {
        header_sha
    };

    let mut out = File::create(&new_path).map_err(|e| format!("创建临时更新文件失败: {e}"))?;
    let mut reader = resp.body_mut().as_reader();
    let mut hasher = Sha256::new();
    let mut buf = [0u8; 65536];
    let mut written: u64 = 0;
    let mut last_pct: u8 = 0;
    on_progress(5);

    loop {
        let n = reader
            .read(&mut buf)
            .map_err(|e| format!("读取下载流失败: {e}"))?;
        if n == 0 {
            break;
        }
        out.write_all(&buf[..n])
            .map_err(|e| format!("写入更新文件失败: {e}"))?;
        hasher.update(&buf[..n]);
        written += n as u64;
        if total_bytes > 0 {
            let pct = ((written * 85) / total_bytes).min(90) as u8;
            if pct >= last_pct.saturating_add(5) {
                last_pct = pct;
                on_progress(pct);
            }
        }
    }
    out.flush().map_err(|e| format!("刷新磁盘缓存失败: {e}"))?;
    drop(out);

    let actual_sha = format!("{:x}", hasher.finalize());
    if !target_sha.is_empty() && actual_sha != target_sha {
        let _ = fs::remove_file(&new_path);
        return Err(format!(
            "SHA-256 校验失败（期望 {target_sha}，实际 {actual_sha}）"
        ));
    }
    on_progress(95);

    if old_path.exists() {
        let _ = fs::remove_file(&old_path);
    }
    fs::rename(&exe_path, &old_path).map_err(|e| format!("备份旧程序失败: {e}"))?;
    if let Err(err) = fs::rename(&new_path, &exe_path) {
        let _ = fs::rename(&old_path, &exe_path);
        return Err(format!("替换新版可执行文件失败: {err}"));
    }
    on_progress(100);

    Command::new(&exe_path)
        .spawn()
        .map_err(|e| format!("新版本启动失败（可手动双击运行）: {e}"))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_comparison_handles_quailnest_tags() {
        assert_eq!(compare_versions("V1.11.25.2", "V1.11.25.1"), Ordering::Greater);
        assert_eq!(compare_versions("V1.12.0", "V1.11.25.2"), Ordering::Greater);
        assert_eq!(compare_versions("V1.11.25.2", "v1.11.25.2"), Ordering::Equal);
        assert_eq!(compare_versions("V1.11.25.1", "V1.11.25.2"), Ordering::Less);
    }
}
