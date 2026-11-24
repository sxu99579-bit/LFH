package server

import (
	"net/http"
	"strconv"
	"strings"
)

func sanitizeWorkerRunner(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto", "agy", "claude", "custom", "notify":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "auto"
	}
}

func sanitizeWorkerMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "poll", "interval":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "poll"
	}
}

func sanitizeWorkerInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

const workerPS1Template = `# LFH (LAN FileHub) Zero-Token Agent Task Watcher (Windows PowerShell 5.1+ / 7+)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$baseUrl = '__LFH_BASE_URL__'
$serverName = '__LFH_SERVER_NAME__'
$runnerMode = '__LFH_RUNNER__'
$pollMode = '__LFH_WORKER_MODE__'
$intervalMin = __LFH_INTERVAL_MIN__
$cooldownSec = __LFH_COOLDOWN_SEC__

if ($env:LFH_SERVER_NAME) { $serverName = $env:LFH_SERVER_NAME }
if ($env:LFH_RUNNER) { $runnerMode = $env:LFH_RUNNER }
if ($env:LFH_WORKER_MODE) { $pollMode = $env:LFH_WORKER_MODE }
if ($env:LFH_INTERVAL_MIN) {
  $parsed = 0
  if ([int]::TryParse($env:LFH_INTERVAL_MIN, [ref]$parsed) -and $parsed -ge 1 -and $parsed -le 1440) { $intervalMin = $parsed }
}
if ($env:LFH_COOLDOWN_SEC) {
  $parsedCd = 0
  if ([int]::TryParse($env:LFH_COOLDOWN_SEC, [ref]$parsedCd) -and $parsedCd -ge 5 -and $parsedCd -le 600) { $cooldownSec = $parsedCd }
}
if (-not $serverName) { $serverName = 'lfh' }

function Resolve-LfhTokenFromConfigs {
  param([string]$ServerKey, [string]$BaseUrl)
  if ($env:LFH_TOKEN -and $env:LFH_TOKEN.StartsWith('lfh_ag_')) {
    return $env:LFH_TOKEN.Trim()
  }
  $candidates = New-Object System.Collections.Generic.List[string]
  [void]$candidates.Add((Join-Path $HOME '.gemini\config\mcp_config.json'))
  [void]$candidates.Add((Join-Path $HOME '.gemini\antigravity\mcp_config.json'))
  [void]$candidates.Add((Join-Path $HOME '.cursor\mcp.json'))
  if ($env:APPDATA) {
    [void]$candidates.Add((Join-Path $env:APPDATA 'Claude\claude_desktop_config.json'))
  }
  foreach ($cfgPath in $candidates) {
    if (-not (Test-Path -LiteralPath $cfgPath)) { continue }
    try {
      $raw = [System.IO.File]::ReadAllText($cfgPath, [System.Text.Encoding]::UTF8)
      $obj = $raw | ConvertFrom-Json
      if ($null -eq $obj -or -not ($obj.PSObject.Properties.Name -contains 'mcpServers') -or $null -eq $obj.mcpServers) { continue }
      $servers = $obj.mcpServers
      $orderedNames = New-Object System.Collections.Generic.List[string]
      if ($servers.PSObject.Properties.Name -contains $ServerKey) {
        [void]$orderedNames.Add($ServerKey)
      }
      foreach ($p in $servers.PSObject.Properties) {
        if ($p.Name -ne $ServerKey) { [void]$orderedNames.Add($p.Name) }
      }
      foreach ($name in $orderedNames) {
        $entry = $servers.$name
        if ($null -eq $entry -or -not ($entry.PSObject.Properties.Name -contains 'headers')) { continue }
        $authVal = [string]$entry.headers.Authorization
        if ($authVal -match 'Bearer\s+(lfh_ag_[A-Za-z0-9_]+)') {
          $tok = $Matches[1]
          $entryUrl = [string]$entry.url
          if (-not $entryUrl) { $entryUrl = [string]$entry.serverUrl }
          if ($name -eq $ServerKey -or ($entryUrl -and $entryUrl.StartsWith($BaseUrl))) {
            return $tok
          }
        }
      }
    } catch {}
  }
  return ''
}

$token = Resolve-LfhTokenFromConfigs -ServerKey $serverName -BaseUrl $baseUrl
if (-not $token) {
  Write-Host ''
  Write-Host '[LFH Worker] No local lfh_ag_ token found in MCP configs or LFH_TOKEN.' -ForegroundColor Red
  Write-Host ("Run pairing first: irm """ + $baseUrl + "/agent/connect.ps1"" | iex") -ForegroundColor Yellow
  exit 1
}

# Check proxy/NO_PROXY bypass for private LAN/Tailscale hosts
try {
  $targetHost = ([uri]$baseUrl).Host
  $hasProxy = [bool]($env:HTTP_PROXY -or $env:HTTPS_PROXY -or $env:http_proxy -or $env:https_proxy)
  $isPriv = $targetHost -match '^(192\.168\.|10\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.|127\.|localhost)$'
  if ($hasProxy -and $isPriv) {
    $npAll = @($env:NO_PROXY, $env:no_proxy) -join ','
    $hasBadQuotes = ($env:NO_PROXY -match '^"|"$') -or ($env:no_proxy -match '^"|"$')
    $covered = ($npAll -match [regex]::Escape($targetHost)) -or ($targetHost.StartsWith('192.168.') -and $npAll -match '192\.168\.') -or ($targetHost.StartsWith('100.') -and $npAll -match '100\.64\.')
    if ($hasBadQuotes -or -not $covered) {
      Write-Host ''
      Write-Host '[LFH Proxy Warning] HTTP_PROXY is active without bypassing LAN/Tailscale in NO_PROXY.' -ForegroundColor Yellow
      Write-Host 'Fix and restart IDE/terminal:' -ForegroundColor Yellow
      Write-Host ('$noProxy = "localhost,127.0.0.1,::1,' + $targetHost + ',10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10"')
      Write-Host '[Environment]::SetEnvironmentVariable("NO_PROXY", $noProxy, "User")'
      Write-Host '[Environment]::SetEnvironmentVariable("no_proxy", $noProxy, "User")'
    }
  }
} catch {}

function Resolve-WorkerRunner {
  param([string]$Pref)
  if ($Pref -eq 'custom') {
    if ($env:LFH_WORKER_CMD) { return 'custom' }
    return 'notify'
  }
  if ($Pref -ne 'auto') { return $Pref }
  if ($env:LFH_WORKER_CMD) { return 'custom' }
  if (Get-Command claude -ErrorAction SilentlyContinue) { return 'claude' }
  if (Get-Command agy -ErrorAction SilentlyContinue) { return 'agy' }
  return 'notify'
}

$activeRunner = Resolve-WorkerRunner -Pref $runnerMode
Write-Host ''
Write-Host '=== LFH Zero-Token Agent Task Watcher ===' -ForegroundColor Cyan
Write-Host ('Server  : ' + $baseUrl)
Write-Host ('Mode    : ' + $pollMode + ' (interval=' + $intervalMin + 'm, cooldown=' + $cooldownSec + 's)')
Write-Host ('Runner  : ' + $activeRunner)
Write-Host 'Idle polling consumes 0 LLM tokens. Press Ctrl+C to stop.' -ForegroundColor DarkGray

$headers = @{ 'Authorization' = ('Bearer ' + $token) }
$waitQuery = if ($pollMode -eq 'interval') { '0' } else { '25' }

while ($true) {
  try {
    $pollUrl = $baseUrl + '/api/agent-tasks/poll?wait_sec=' + $waitQuery
    $res = Invoke-RestMethod -Method Get -Uri $pollUrl -Headers $headers -TimeoutSec 40
    if ($res -and $res.has_work) {
      $lines = New-Object System.Collections.Generic.List[string]
      foreach ($t in $res.tasks) {
        [void]$lines.Add(('- [' + $t.id + '] ' + $t.title + ' (status=' + $t.status + ')'))
      }
      $nl = [Environment]::NewLine
      $prompt = "You are connected to LFH (" + $baseUrl + "). Pending/mentioned tasks detected:" + $nl + ($lines -join $nl) + $nl + $nl + "Use LFH MCP tools: call list_tasks to inspect details, call claim_task on an unlocked ready task/subtask (respecting @Agent locks), call split_task if it needs multi-Agent decomposition, report progress via report_agent_status and post_task_comment, and call complete_task with outputs when finished."
      Write-Host ('[' + (Get-Date -Format 'HH:mm:ss') + '] Work detected: claimable=' + $res.claimable_count + ' mentioned=' + $res.mentioned_count) -ForegroundColor Green

      switch ($activeRunner) {
        'custom' {
          $env:LFH_TASK_PROMPT = $prompt
          & cmd.exe /c $env:LFH_WORKER_CMD
          Remove-Item Env:LFH_TASK_PROMPT -ErrorAction SilentlyContinue
        }
        'claude' {
          & claude -p $prompt
        }
        'agy' {
          & agy -p $prompt
        }
        default {
          Write-Host $prompt -ForegroundColor Cyan
        }
      }
      Start-Sleep -Seconds $cooldownSec
      continue
    }
  } catch {
    Write-Host ('[' + (Get-Date -Format 'HH:mm:ss') + '] Poll retry in 5s: ' + $_.Exception.Message) -ForegroundColor Yellow
    Start-Sleep -Seconds 5
    continue
  }
  if ($pollMode -eq 'interval') {
    Start-Sleep -Seconds ($intervalMin * 60)
  }
}
`

const workerSHTemplate = `#!/bin/sh
# LFH (LAN FileHub) Zero-Token Agent Task Watcher (Linux / macOS POSIX sh)
set -eu

BASE_URL='__LFH_BASE_URL__'
SERVER_NAME='__LFH_SERVER_NAME__'
RUNNER_MODE='__LFH_RUNNER__'
POLL_MODE='__LFH_WORKER_MODE__'
INTERVAL_MIN='__LFH_INTERVAL_MIN__'
COOLDOWN_SEC='__LFH_COOLDOWN_SEC__'

[ -n "${LFH_SERVER_NAME:-}" ] && SERVER_NAME="$LFH_SERVER_NAME"
[ -n "${LFH_RUNNER:-}" ] && RUNNER_MODE="$LFH_RUNNER"
[ -n "${LFH_WORKER_MODE:-}" ] && POLL_MODE="$LFH_WORKER_MODE"
[ -n "${LFH_INTERVAL_MIN:-}" ] && INTERVAL_MIN="$LFH_INTERVAL_MIN"
[ -n "${LFH_COOLDOWN_SEC:-}" ] && COOLDOWN_SEC="$LFH_COOLDOWN_SEC"
[ -z "$SERVER_NAME" ] && SERVER_NAME="lfh"

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required." >&2
  exit 1
fi

TARGET_HOST=$(printf '%s' "$BASE_URL" | sed -n 's#^[a-zA-Z]*://\([^/:]*\).*#\1#p')
IS_PRIV=0
case "$TARGET_HOST" in
  192.168.*|10.*|127.*|localhost|100.*|172.16.*|172.17.*|172.18.*|172.19.*|172.2[0-9].*|172.3[01].*)
    IS_PRIV=1
    ;;
esac

PROXY_ACTIVE="${HTTP_PROXY:-}${HTTPS_PROXY:-}${http_proxy:-}${https_proxy:-}"
NOPROXY_ALL="${NO_PROXY:-},${no_proxy:-}"
if [ -n "$PROXY_ACTIVE" ] && [ "$IS_PRIV" -eq 1 ]; then
  NEED_FIX=0
  case "${NO_PROXY:-}${no_proxy:-}" in
    *'"'*) NEED_FIX=1 ;;
  esac
  case "$NOPROXY_ALL" in
    *"$TARGET_HOST"*|*"192.168."*|*"100.64."*) ;;
    *) NEED_FIX=1 ;;
  esac
  if [ "$NEED_FIX" -eq 1 ]; then
    echo "[LFH Proxy Warning] Proxy env is active; ensure NO_PROXY bypasses $TARGET_HOST:" >&2
    echo "  export NO_PROXY=\"localhost,127.0.0.1,::1,${TARGET_HOST},10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10\"" >&2
  fi
fi

resolve_token() {
  if [ -n "${LFH_TOKEN:-}" ]; then
    printf '%s' "$LFH_TOKEN"
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    LFH_SERVER_KEY="$SERVER_NAME" LFH_BASE="$BASE_URL" python3 -c "
import json, os, re
paths = [
    os.path.expanduser('~/.gemini/config/mcp_config.json'),
    os.path.expanduser('~/.gemini/antigravity/mcp_config.json'),
    os.path.expanduser('~/.cursor/mcp.json'),
    os.path.expanduser('~/Library/Application Support/Claude/claude_desktop_config.json'),
    os.path.expanduser('~/.config/Claude/claude_desktop_config.json'),
]
srv_key = os.environ.get('LFH_SERVER_KEY', 'lfh')
base = os.environ.get('LFH_BASE', '')
for p in paths:
    if not os.path.exists(p):
        continue
    try:
        data = json.load(open(p, 'r', encoding='utf-8'))
        servers = data.get('mcpServers') or {}
        names = ([srv_key] if srv_key in servers else []) + [k for k in servers if k != srv_key]
        for k in names:
            ent = servers.get(k) or {}
            hdrs = ent.get('headers') or {}
            auth_val = str(hdrs.get('Authorization', ''))
            m = re.search(r'Bearer\s+(lfh_ag_[A-Za-z0-9_]+)', auth_val)
            url_val = str(ent.get('url') or ent.get('serverUrl') or '')
            if m and (k == srv_key or (base and url_val.startswith(base))):
                print(m.group(1))
                raise SystemExit(0)
    except SystemExit:
        raise
    except Exception:
        pass
"
    return
  fi
  for p in "$HOME/.gemini/config/mcp_config.json" "$HOME/.gemini/antigravity/mcp_config.json" "$HOME/.cursor/mcp.json"; do
    if [ -f "$p" ]; then
      TOK=$(grep -o 'lfh_ag_[A-Za-z0-9_]*' "$p" | head -n 1 || true)
      if [ -n "$TOK" ]; then
        printf '%s' "$TOK"
        return
      fi
    fi
  done
}

TOKEN=$(resolve_token || true)
if [ -z "$TOKEN" ]; then
  echo "Error: no local lfh_ag_ token found. Run pairing first: ${BASE_URL}/agent/connect.sh" >&2
  exit 1
fi

ACTIVE_RUNNER="$RUNNER_MODE"
if [ "$ACTIVE_RUNNER" = "custom" ] && [ -z "${LFH_WORKER_CMD:-}" ]; then
  ACTIVE_RUNNER="notify"
elif [ "$ACTIVE_RUNNER" = "auto" ]; then
  if [ -n "${LFH_WORKER_CMD:-}" ]; then
    ACTIVE_RUNNER="custom"
  elif command -v claude >/dev/null 2>&1; then
    ACTIVE_RUNNER="claude"
  elif command -v agy >/dev/null 2>&1; then
    ACTIVE_RUNNER="agy"
  else
    ACTIVE_RUNNER="notify"
  fi
fi

WAIT_QUERY="25"
[ "$POLL_MODE" = "interval" ] && WAIT_QUERY="0"

echo "=== LFH Zero-Token Agent Task Watcher ==="
echo "Server  : $BASE_URL"
echo "Mode    : $POLL_MODE (interval=${INTERVAL_MIN}m, cooldown=${COOLDOWN_SEC}s)"
echo "Runner  : $ACTIVE_RUNNER"
echo "Idle polling consumes 0 LLM tokens. Press Ctrl+C to stop."

build_task_summary() {
  RAW="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 -c "
import json, sys
d = json.loads(sys.stdin.read())
if not d.get('has_work'):
    sys.exit(1)
for t in d.get('tasks') or []:
    print(f\"- [{t.get('id')}] {t.get('title')} (status={t.get('status')})\")
" <<EOF
$RAW
EOF
    return $?
  fi
  printf '%s' "$RAW" | grep -q '"has_work":true' || return 1
  echo "- (Pending/mentioned tasks available; call list_tasks to inspect)"
}

while true; do
  if [ "$IS_PRIV" -eq 1 ] && [ -n "$TARGET_HOST" ]; then
    RES=$(curl -fsS --max-time 40 --noproxy "$TARGET_HOST" "${BASE_URL}/api/agent-tasks/poll?wait_sec=${WAIT_QUERY}" -H "Authorization: Bearer ${TOKEN}" || true)
  else
    RES=$(curl -fsS --max-time 40 "${BASE_URL}/api/agent-tasks/poll?wait_sec=${WAIT_QUERY}" -H "Authorization: Bearer ${TOKEN}" || true)
  fi

  if [ -z "$RES" ]; then
    sleep 5
    continue
  fi

  SUMMARY=$(build_task_summary "$RES" || true)
  if [ -n "$SUMMARY" ]; then
    TASK_PROMPT=$(cat <<EOF
You are connected to LFH (${BASE_URL}). Pending/mentioned tasks detected:
${SUMMARY}

Use LFH MCP tools: call list_tasks to inspect details, claim_task on an unlocked ready task/subtask (respecting @Agent locks), split_task if needed, report progress via report_agent_status and post_task_comment, and complete_task when finished.
EOF
)
    echo "[$(date +%H:%M:%S)] Work detected on LFH!"
    case "$ACTIVE_RUNNER" in
      custom)
        LFH_TASK_PROMPT="$TASK_PROMPT" sh -c "$LFH_WORKER_CMD" || true
        ;;
      claude)
        claude -p "$TASK_PROMPT" || true
        ;;
      agy)
        agy -p "$TASK_PROMPT" || true
        ;;
      *)
        printf '%s\n' "$TASK_PROMPT"
        ;;
    esac
    sleep "$COOLDOWN_SEC"
    continue
  fi

  if [ "$POLL_MODE" = "interval" ]; then
    sleep $((INTERVAL_MIN * 60))
  fi
done
`

func renderWorkerScript(tmpl string, s *Server, r *http.Request, escaper func(string) string) string {
	selfURL := ""
	if s.cluster != nil {
		selfURL = s.cluster.SelfStatus().HTTPURL
	}
	q := r.URL.Query()
	baseURL := validateScriptBaseURL(requestBaseURL(r, selfURL, s.port))
	serverName := sanitizeScriptServerName(q.Get("server_name"))
	runner := sanitizeWorkerRunner(q.Get("runner"))
	mode := sanitizeWorkerMode(q.Get("mode"))
	intervalMin := sanitizeWorkerInt(q.Get("interval_min"), 15, 1, 1440)
	cooldownSec := sanitizeWorkerInt(q.Get("cooldown_sec"), 15, 5, 600)

	replacer := strings.NewReplacer(
		"__LFH_BASE_URL__", escaper(baseURL),
		"__LFH_SERVER_NAME__", escaper(serverName),
		"__LFH_RUNNER__", escaper(runner),
		"__LFH_WORKER_MODE__", escaper(mode),
		"__LFH_INTERVAL_MIN__", strconv.Itoa(intervalMin),
		"__LFH_COOLDOWN_SEC__", strconv.Itoa(cooldownSec),
	)
	return replacer.Replace(tmpl)
}

func (s *Server) handleAgentWorkerPS1(w http.ResponseWriter, r *http.Request) {
	body := renderWorkerScript(workerPS1Template, s, r, escapePSSingleQuote)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleAgentWorkerSH(w http.ResponseWriter, r *http.Request) {
	body := renderWorkerScript(workerSHTemplate, s, r, escapePOSIXSingleQuote)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
