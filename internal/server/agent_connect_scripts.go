package server

import (
	"net/http"
	"net/url"
	"strings"
)

func sanitizeScriptClient(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cursor", "claude_code", "antigravity", "claude_desktop", "generic":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "auto"
	}
}

func sanitizeScriptOwner(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
			if b.Len() >= 64 {
				break
			}
		}
	}
	return b.String()
}

func sanitizeScriptServerName(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			if b.Len() >= 32 {
				break
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "lfh"
	}
	return out
}

func sanitizeScriptAgentName(raw string) string {
	clean := sanitizePairingText(raw, 64)
	if clean == "" {
		return ""
	}
	clean = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '"', '`', '$', '\\', ';', '|', '&', '(', ')', '<', '>', '{', '}':
			return -1
		default:
			return r
		}
	}, clean)
	return strings.TrimSpace(clean)
}

func validateScriptBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.ContainsAny(trimmed, " \t\r\n'\"`$\\;|&()<>") {
		return "http://127.0.0.1:8080"
	}
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(u.Host) == "" {
		return "http://127.0.0.1:8080"
	}
	return trimmed
}

func escapePSSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func escapePOSIXSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func resolveConnectScriptVars(s *Server, r *http.Request) (baseURL, client, owner, serverName, agentName string) {
	selfURL := ""
	if s.cluster != nil {
		selfURL = s.cluster.SelfStatus().HTTPURL
	}
	q := r.URL.Query()
	baseURL = validateScriptBaseURL(requestBaseURL(r, selfURL, s.port))
	client = sanitizeScriptClient(q.Get("client"))
	owner = sanitizeScriptOwner(q.Get("owner"))
	serverName = sanitizeScriptServerName(q.Get("server_name"))
	agentName = sanitizeScriptAgentName(q.Get("name"))
	return
}

const connectPS1Template = `# LFH (LAN FileHub) One-Click Agent Pairing Script (Windows PowerShell 5.1+ / 7+)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$baseUrl = '__LFH_BASE_URL__'
$clientPref = '__LFH_CLIENT__'
$targetOwner = '__LFH_OWNER__'
$serverName = '__LFH_SERVER_NAME__'
$customName = '__LFH_AGENT_NAME__'

if ($env:LFH_CLIENT) { $clientPref = $env:LFH_CLIENT }
if ($env:LFH_OWNER) { $targetOwner = $env:LFH_OWNER }
if ($env:LFH_SERVER_NAME) { $serverName = $env:LFH_SERVER_NAME }
if ($env:LFH_AGENT_NAME) { $customName = $env:LFH_AGENT_NAME }

$hostName = $env:COMPUTERNAME
if (-not $hostName) { $hostName = [System.Net.Dns]::GetHostName() }
$hostName = ($hostName -replace '["\\$;\|\&\<\>]', '').Trim()
if (-not $serverName) { $serverName = 'lfh' }

$detected = New-Object System.Collections.Generic.List[string]
if ($clientPref -eq 'auto') {
  if ((Test-Path -LiteralPath (Join-Path $HOME '.cursor')) -or (Get-Command cursor -ErrorAction SilentlyContinue)) {
    [void]$detected.Add('cursor')
  }
  if ((Test-Path -LiteralPath (Join-Path $HOME '.gemini\config')) -or (Test-Path -LiteralPath (Join-Path $HOME '.gemini\antigravity'))) {
    [void]$detected.Add('antigravity')
  }
  if (Get-Command claude -ErrorAction SilentlyContinue) {
    [void]$detected.Add('claude_code')
  }
  if ($env:APPDATA -and (Test-Path -LiteralPath (Join-Path $env:APPDATA 'Claude'))) {
    [void]$detected.Add('claude_desktop')
  }
} else {
  [void]$detected.Add($clientPref)
}

$primaryClient = 'generic'
if ($detected.Count -gt 0) {
  $primaryClient = $detected[0]
}
$agentName = $customName
if (-not $agentName) {
  $agentName = "$hostName-$primaryClient"
}

$deviceKind = ''
try {
  $enclosure = Get-CimInstance -ClassName Win32_SystemEnclosure -ErrorAction Stop | Select-Object -First 1
  if ($enclosure -and $enclosure.ChassisTypes) {
    foreach ($ct in $enclosure.ChassisTypes) {
      if ($ct -in 8, 9, 10, 14, 31, 32) {
        $deviceKind = 'laptop'
        break
      }
    }
  }
} catch {}
if (-not $deviceKind) {
  $hl = $hostName.ToLowerInvariant()
  if ($hl -match 'laptop|notebook|thinkpad|surface|matebook|zenbook|book') {
    $deviceKind = 'laptop'
  } elseif ($hl -match 'nas|server|srv|node') {
    $deviceKind = 'server'
  } else {
    $deviceKind = 'desktop'
  }
}

Write-Host ""
Write-Host "=== LFH One-Click Agent Pairing ===" -ForegroundColor Cyan
Write-Host ("Server : " + $baseUrl)
Write-Host ("Device : " + $deviceKind + " (" + $hostName + ")")
if ($detected.Count -gt 0) {
  Write-Host ("Clients: " + ($detected -join ', '))
} else {
  Write-Host "Clients: none detected (will output ready-to-paste MCP JSON)"
}

$reqPayload = @{
  name = $agentName
  client_type = $primaryClient
  client_version = 'ps1-connect'
  hostname = $hostName
  os = 'windows'
  device_kind = $deviceKind
  target_owner = $targetOwner
  requested_scopes = @('read', 'write')
  requested_read_folders = @('*')
  note = 'Windows PowerShell one-click pairing'
} | ConvertTo-Json -Depth 5

$reqBytes = [System.Text.Encoding]::UTF8.GetBytes($reqPayload)
$createRes = Invoke-RestMethod -Method Post -Uri ($baseUrl + '/api/agent-pairing/request') -ContentType 'application/json; charset=utf-8' -Body $reqBytes
if (-not $createRes.id -or -not $createRes.poll_secret) {
  throw 'Failed to create agent pairing request on LFH.'
}

Write-Host ""
Write-Host "--------------------------------------------------" -ForegroundColor Yellow
Write-Host ("  Confirm Code : " + $createRes.confirm_code) -ForegroundColor Green
Write-Host ("  Agent Name   : " + $agentName)
Write-Host ("  Approve In   : " + $baseUrl + " -> Settings -> Agent Bridge")
Write-Host "--------------------------------------------------" -ForegroundColor Yellow
Write-Host "Waiting for approval in LFH web UI (timeout: 5 minutes)..."

$pollHeaders = @{ 'X-Pairing-Secret' = [string]$createRes.poll_secret }
$approved = $null
for ($attempt = 0; $attempt -lt 150; $attempt++) {
  Start-Sleep -Seconds 2
  $pollRes = Invoke-RestMethod -Method Get -Uri ($baseUrl + '/api/agent-pairing/' + $createRes.id) -Headers $pollHeaders
  if ($pollRes.status -eq 'approved') {
    $approved = $pollRes
    break
  }
  if ($pollRes.status -eq 'rejected') {
    throw 'Pairing request was rejected in LFH.'
  }
  if ($pollRes.status -eq 'expired' -or $pollRes.status -eq 'claimed') {
    throw ('Pairing request ended with status: ' + $pollRes.status)
  }
}
if ($null -eq $approved -or -not $approved.token) {
  throw 'Pairing request timed out after 5 minutes.'
}

$token = [string]$approved.token
$mcpUrl = [string]$approved.mcp_url
if (-not $mcpUrl) { $mcpUrl = $baseUrl + '/mcp' }

function Merge-LfhMcpConfig {
  param(
    [string]$FilePath,
    [string]$TargetServerName,
    [string]$TargetMcpUrl,
    [string]$BearerToken,
    [bool]$IncludeHttpType
  )
  $parentDir = Split-Path -Parent $FilePath
  if ($parentDir -and -not (Test-Path -LiteralPath $parentDir)) {
    New-Item -ItemType Directory -Path $parentDir -Force | Out-Null
  }
  $rootObj = $null
  if (Test-Path -LiteralPath $FilePath) {
    Copy-Item -LiteralPath $FilePath -Destination ($FilePath + '.bak') -Force
    $existingRaw = [System.IO.File]::ReadAllText($FilePath, [System.Text.Encoding]::UTF8)
    if (-not [string]::IsNullOrWhiteSpace($existingRaw)) {
      try { $rootObj = $existingRaw | ConvertFrom-Json } catch { $rootObj = $null }
    }
  }
  if ($null -eq $rootObj -or ($rootObj -isnot [System.Management.Automation.PSCustomObject])) {
    $rootObj = [pscustomobject]@{}
  }
  if (-not ($rootObj.PSObject.Properties.Name -contains 'mcpServers') -or $null -eq $rootObj.mcpServers) {
    $rootObj | Add-Member -NotePropertyName 'mcpServers' -NotePropertyValue ([pscustomobject]@{}) -Force
  }
  $headersObj = [pscustomobject]@{ 'Authorization' = ('Bearer ' + $BearerToken) }
  if ($IncludeHttpType) {
    $entryObj = [pscustomobject]@{
      'type' = 'http'
      'url' = $TargetMcpUrl
      'headers' = $headersObj
    }
  } else {
    $entryObj = [pscustomobject]@{
      'url' = $TargetMcpUrl
      'headers' = $headersObj
    }
  }
  $rootObj.mcpServers | Add-Member -NotePropertyName $TargetServerName -NotePropertyValue $entryObj -Force
  $jsonText = $rootObj | ConvertTo-Json -Depth 10
  [System.IO.File]::WriteAllText($FilePath, $jsonText + [Environment]::NewLine, (New-Object System.Text.UTF8Encoding($false)))
  Write-Host ("Updated config : " + $FilePath) -ForegroundColor Green
}

$configured = 0
foreach ($clientKind in $detected) {
  switch ($clientKind) {
    'cursor' {
      Merge-LfhMcpConfig -FilePath (Join-Path $HOME '.cursor\mcp.json') -TargetServerName $serverName -TargetMcpUrl $mcpUrl -BearerToken $token -IncludeHttpType $true
      $configured++
    }
    'antigravity' {
      Merge-LfhMcpConfig -FilePath (Join-Path $HOME '.gemini\config\mcp_config.json') -TargetServerName $serverName -TargetMcpUrl $mcpUrl -BearerToken $token -IncludeHttpType $false
      $agyAlt = Join-Path $HOME '.gemini\antigravity\mcp_config.json'
      if (Test-Path -LiteralPath $agyAlt) {
        Merge-LfhMcpConfig -FilePath $agyAlt -TargetServerName $serverName -TargetMcpUrl $mcpUrl -BearerToken $token -IncludeHttpType $false
      }
      $configured++
    }
    'claude_desktop' {
      if ($env:APPDATA) {
        Merge-LfhMcpConfig -FilePath (Join-Path $env:APPDATA 'Claude\claude_desktop_config.json') -TargetServerName $serverName -TargetMcpUrl $mcpUrl -BearerToken $token -IncludeHttpType $true
        $configured++
      }
    }
    'claude_code' {
      if (Get-Command claude -ErrorAction SilentlyContinue) {
        try {
          & claude mcp remove $serverName 2>$null | Out-Null
        } catch {}
        & claude mcp add --transport http $serverName $mcpUrl --header ('Authorization: Bearer ' + $token)
        if ($LASTEXITCODE -eq 0) {
          Write-Host ("Configured Claude Code CLI MCP server: " + $serverName) -ForegroundColor Green
          $configured++
        } else {
          Write-Warning "claude mcp add exited non-zero; please add manually if needed."
        }
      }
    }
  }
}

if ($configured -eq 0) {
  Write-Host ""
  Write-Host "Paste this snippet into your MCP client config:" -ForegroundColor Yellow
  $snippet = [pscustomobject]@{
    mcpServers = [pscustomobject]@{
      $serverName = [pscustomobject]@{
        type = 'http'
        url = $mcpUrl
        headers = [pscustomobject]@{
          Authorization = ('Bearer ' + $token)
        }
      }
    }
  } | ConvertTo-Json -Depth 10
  Write-Host $snippet
}

Write-Host ""
Write-Host "Running MCP self-check (initialize + whoami)..."
$mcpHeaders = @{ 'Authorization' = ('Bearer ' + $token) }
$initBody = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"lfh-connect","version":"1.0"}}}'
$initResp = Invoke-WebRequest -UseBasicParsing -Method Post -Uri $mcpUrl -Headers $mcpHeaders -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($initBody))
$sid = $initResp.Headers['Mcp-Session-Id']
$whoHeaders = @{ 'Authorization' = ('Bearer ' + $token) }
if ($sid) { $whoHeaders['Mcp-Session-Id'] = [string]$sid }
$whoBody = '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
$whoResp = Invoke-RestMethod -Method Post -Uri $mcpUrl -Headers $whoHeaders -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($whoBody))
if ($whoResp.result -and $whoResp.result.content -and $whoResp.result.content.Count -gt 0) {
  $whoData = $whoResp.result.content[0].text | ConvertFrom-Json
  Write-Host ("Connected as   : " + $whoData.actor_label + " (user=" + $whoData.username + ")") -ForegroundColor Green
  Write-Host ("Scopes         : " + ($whoData.scopes -join ', '))
  if (($whoData.scopes -contains 'write') -and (-not $whoData.write_folders -or $whoData.write_folders.Count -eq 0)) {
    Write-Host ""
    Write-Host "[LFH Tip] Key has 'write' scope, but write_folders is currently empty." -ForegroundColor Yellow
    Write-Host "         Configure write_folders (e.g. *) in LFH Web UI -> Settings -> Agent Bridge before writing files." -ForegroundColor Yellow
  }
}

try {
  $targetHost = ([uri]$baseUrl).Host
  $hasProxy = [bool]($env:HTTP_PROXY -or $env:HTTPS_PROXY -or $env:http_proxy -or $env:https_proxy)
  $isPriv = $targetHost -match '^(192\.168\.|10\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.|127\.|localhost)$'
  if ($hasProxy -and $isPriv) {
    $npAll = @($env:NO_PROXY, $env:no_proxy) -join ','
    $hasBadQuotes = ($env:NO_PROXY -match '^"|"$') -or ($env:no_proxy -match '^"|"$')
    $covered = ($npAll -match [regex]::Escape($targetHost)) -or ($targetHost.StartsWith('192.168.') -and $npAll -match '192\.168\.') -or ($targetHost.StartsWith('100.') -and $npAll -match '100\.64\.')
    if ($hasBadQuotes -or -not $covered) {
      Write-Host ""
      Write-Host "[LFH Proxy Warning] HTTP_PROXY/HTTPS_PROXY is active without bypassing LAN/Tailscale in NO_PROXY." -ForegroundColor Yellow
      Write-Host "IDE/Antigravity may time out connecting to LFH. Run these 3 lines and restart your IDE:" -ForegroundColor Yellow
      Write-Host ('$noProxy = "localhost,127.0.0.1,::1,' + $targetHost + ',10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10"')
      Write-Host '[Environment]::SetEnvironmentVariable("NO_PROXY", $noProxy, "User")'
      Write-Host '[Environment]::SetEnvironmentVariable("no_proxy", $noProxy, "User")'
    }
  }
} catch {}

Write-Host "Done! Restart or reload MCP servers in your IDE to start collaborating." -ForegroundColor Cyan
`

const connectSHTemplate = `#!/bin/sh
# LFH (LAN FileHub) One-Click Agent Pairing Script (Linux / macOS POSIX sh)
set -eu

BASE_URL='__LFH_BASE_URL__'
CLIENT_PREF='__LFH_CLIENT__'
TARGET_OWNER='__LFH_OWNER__'
SERVER_NAME='__LFH_SERVER_NAME__'
CUSTOM_NAME='__LFH_AGENT_NAME__'

[ -n "${LFH_CLIENT:-}" ] && CLIENT_PREF="$LFH_CLIENT"
[ -n "${LFH_OWNER:-}" ] && TARGET_OWNER="$LFH_OWNER"
[ -n "${LFH_SERVER_NAME:-}" ] && SERVER_NAME="$LFH_SERVER_NAME"
[ -n "${LFH_AGENT_NAME:-}" ] && CUSTOM_NAME="$LFH_AGENT_NAME"

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required." >&2
  exit 1
fi

HOST_NAME=$(uname -n 2>/dev/null || hostname 2>/dev/null || echo host)
HOST_NAME=$(printf '%s' "$HOST_NAME" | tr -d '"\\\r\n' | cut -c1-64)
UNAME_OS=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]' || echo linux)
OS_TAG="linux"
case "$UNAME_OS" in
  *darwin*) OS_TAG="darwin" ;;
  *linux*)  OS_TAG="linux" ;;
esac
[ -z "$SERVER_NAME" ] && SERVER_NAME="lfh"

DETECTED=""
add_client() {
  case " $DETECTED " in
    *" $1 "*) ;;
    *) DETECTED="${DETECTED:+$DETECTED }$1" ;;
  esac
}

if [ "$CLIENT_PREF" = "auto" ]; then
  if [ -d "$HOME/.cursor" ] || command -v cursor >/dev/null 2>&1; then
    add_client "cursor"
  fi
  if [ -d "$HOME/.gemini/config" ] || [ -d "$HOME/.gemini/antigravity" ]; then
    add_client "antigravity"
  fi
  if command -v claude >/dev/null 2>&1; then
    add_client "claude_code"
  fi
  if [ "$OS_TAG" = "darwin" ] && [ -d "$HOME/Library/Application Support/Claude" ]; then
    add_client "claude_desktop"
  elif [ -d "$HOME/.config/Claude" ]; then
    add_client "claude_desktop"
  fi
else
  add_client "$CLIENT_PREF"
fi

PRIMARY_CLIENT="generic"
for c in $DETECTED; do
  PRIMARY_CLIENT="$c"
  break
done

AGENT_NAME=$(printf '%s' "$CUSTOM_NAME" | tr -d '"\\\r\n' | cut -c1-64)
if [ -z "$AGENT_NAME" ]; then
  AGENT_NAME="${HOST_NAME}-${PRIMARY_CLIENT}"
fi

HOST_LOWER=$(printf '%s' "$HOST_NAME" | tr '[:upper:]' '[:lower:]')
DEVICE_KIND="desktop"
case "$OS_TAG" in
  darwin)
    case "$HOST_LOWER" in
      *macbook*|*laptop*) DEVICE_KIND="laptop" ;;
      *) DEVICE_KIND="mac" ;;
    esac
    ;;
  linux)
    case "$HOST_LOWER" in
      *laptop*|*notebook*|*thinkpad*|*book*) DEVICE_KIND="laptop" ;;
      *) DEVICE_KIND="server" ;;
    esac
    ;;
esac

TARGET_HOST=$(printf '%s' "$BASE_URL" | sed -n 's#^[a-zA-Z]*://\([^/:]*\).*#\1#p')
IS_PRIV=0
case "$TARGET_HOST" in
  192.168.*|10.*|127.*|localhost|100.*|172.16.*|172.17.*|172.18.*|172.19.*|172.2[0-9].*|172.3[01].*)
    IS_PRIV=1
    ;;
esac

lfh_curl() {
  if [ "$IS_PRIV" -eq 1 ] && [ -n "$TARGET_HOST" ]; then
    curl --noproxy "$TARGET_HOST" "$@"
  else
    curl "$@"
  fi
}

json_get() {
  JSON_KEY="$1"
  RAW_JSON="$2"
  if command -v python3 >/dev/null 2>&1; then
    LFH_JSON_KEY="$JSON_KEY" python3 -c "import json,os,sys; d=json.loads(sys.stdin.read()); v=d.get(os.environ['LFH_JSON_KEY'],''); print(v if isinstance(v,str) else '')" <<EOF
$RAW_JSON
EOF
    return
  fi
  if command -v node >/dev/null 2>&1; then
    LFH_JSON_KEY="$JSON_KEY" node -e "const fs=require('fs');const d=JSON.parse(fs.readFileSync(0,'utf8'));const v=d[process.env.LFH_JSON_KEY];process.stdout.write(typeof v==='string'?v:'');" <<EOF
$RAW_JSON
EOF
    return
  fi
  printf '%s' "$RAW_JSON" | tr ',' '\n' | sed -n "s/.*\"${JSON_KEY}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -n 1
}

echo ""
echo "=== LFH One-Click Agent Pairing ==="
echo "Server : $BASE_URL"
echo "Device : $DEVICE_KIND ($HOST_NAME)"
if [ -n "$DETECTED" ]; then
  echo "Clients: $DETECTED"
else
  echo "Clients: none detected (will output ready-to-paste MCP JSON)"
fi

SAFE_OWNER=$(printf '%s' "$TARGET_OWNER" | tr -d '"\\\r\n')
REQ_PAYLOAD=$(printf '{"name":"%s","client_type":"%s","client_version":"sh-connect","hostname":"%s","os":"%s","device_kind":"%s","target_owner":"%s","requested_scopes":["read","write"],"requested_read_folders":["*"],"note":"Shell one-click pairing"}' \
  "$AGENT_NAME" "$PRIMARY_CLIENT" "$HOST_NAME" "$OS_TAG" "$DEVICE_KIND" "$SAFE_OWNER")

CREATE_RES=$(lfh_curl -fsS -X POST "${BASE_URL}/api/agent-pairing/request" \
  -H "Content-Type: application/json" \
  -d "$REQ_PAYLOAD")

PAIR_ID=$(json_get "id" "$CREATE_RES")
CONFIRM_CODE=$(json_get "confirm_code" "$CREATE_RES")
POLL_SECRET=$(json_get "poll_secret" "$CREATE_RES")

if [ -z "$PAIR_ID" ] || [ -z "$POLL_SECRET" ]; then
  echo "Error: failed to parse pairing response from LFH." >&2
  exit 1
fi

echo ""
echo "--------------------------------------------------"
echo "  Confirm Code : $CONFIRM_CODE"
echo "  Agent Name   : $AGENT_NAME"
echo "  Approve In   : $BASE_URL -> Settings -> Agent Bridge"
echo "--------------------------------------------------"
echo "Waiting for approval in LFH web UI (timeout: 5 minutes)..."

TOKEN=""
MCP_URL=""
ATTEMPT=0
while [ "$ATTEMPT" -lt 150 ]; do
  sleep 2
  ATTEMPT=$((ATTEMPT + 1))
  POLL_RES=$(lfh_curl -fsS "${BASE_URL}/api/agent-pairing/${PAIR_ID}" -H "X-Pairing-Secret: ${POLL_SECRET}")
  STATUS=$(json_get "status" "$POLL_RES")
  case "$STATUS" in
    approved)
      TOKEN=$(json_get "token" "$POLL_RES")
      MCP_URL=$(json_get "mcp_url" "$POLL_RES")
      break
      ;;
    rejected)
      echo "Error: pairing request was rejected in LFH." >&2
      exit 1
      ;;
    expired|claimed)
      echo "Error: pairing request ended with status: $STATUS" >&2
      exit 1
      ;;
  esac
done

if [ -z "$TOKEN" ]; then
  echo "Error: pairing request timed out after 5 minutes." >&2
  exit 1
fi
[ -z "$MCP_URL" ] && MCP_URL="${BASE_URL}/mcp"

merge_json_config() {
  CFG_PATH="$1"
  CFG_INCLUDE_TYPE="$2"
  mkdir -p "$(dirname "$CFG_PATH")"
  if [ -f "$CFG_PATH" ]; then
    cp "$CFG_PATH" "${CFG_PATH}.bak"
  fi
  if command -v python3 >/dev/null 2>&1; then
    LFH_CFG_PATH="$CFG_PATH" LFH_CFG_SERVER="$SERVER_NAME" LFH_CFG_URL="$MCP_URL" LFH_CFG_TOKEN="$TOKEN" LFH_CFG_TYPE="$CFG_INCLUDE_TYPE" python3 -c "
import json, os, shutil
p = os.environ['LFH_CFG_PATH']
data = {}
if os.path.exists(p):
    try:
        with open(p, 'r', encoding='utf-8') as f:
            loaded = json.load(f)
            if isinstance(loaded, dict):
                data = loaded
    except Exception:
        data = {}
servers = data.get('mcpServers')
if not isinstance(servers, dict):
    servers = {}
entry = {'url': os.environ['LFH_CFG_URL'], 'headers': {'Authorization': 'Bearer ' + os.environ['LFH_CFG_TOKEN']}}
if os.environ['LFH_CFG_TYPE'] == '1':
    entry['type'] = 'http'
servers[os.environ['LFH_CFG_SERVER']] = entry
data['mcpServers'] = servers
with open(p, 'w', encoding='utf-8') as f:
    json.dump(data, f, indent=2, ensure_ascii=False)
    f.write('\n')
"
    echo "Updated config : $CFG_PATH"
    return 0
  fi
  if command -v node >/dev/null 2>&1; then
    LFH_CFG_PATH="$CFG_PATH" LFH_CFG_SERVER="$SERVER_NAME" LFH_CFG_URL="$MCP_URL" LFH_CFG_TOKEN="$TOKEN" LFH_CFG_TYPE="$CFG_INCLUDE_TYPE" node -e "
const fs = require('fs');
const p = process.env.LFH_CFG_PATH;
let data = {};
if (fs.existsSync(p)) {
  try {
    const parsed = JSON.parse(fs.readFileSync(p, 'utf8'));
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) data = parsed;
  } catch {}
}
if (!data.mcpServers || typeof data.mcpServers !== 'object' || Array.isArray(data.mcpServers)) data.mcpServers = {};
const entry = { url: process.env.LFH_CFG_URL, headers: { Authorization: 'Bearer ' + process.env.LFH_CFG_TOKEN } };
if (process.env.LFH_CFG_TYPE === '1') entry.type = 'http';
data.mcpServers[process.env.LFH_CFG_SERVER] = entry;
fs.writeFileSync(p, JSON.stringify(data, null, 2) + '\n', 'utf8');
"
    echo "Updated config : $CFG_PATH"
    return 0
  fi
  return 1
}

CONFIGURED=0
for C in $DETECTED; do
  case "$C" in
    cursor)
      if merge_json_config "$HOME/.cursor/mcp.json" "1"; then
        CONFIGURED=$((CONFIGURED + 1))
      fi
      ;;
    antigravity)
      if merge_json_config "$HOME/.gemini/config/mcp_config.json" "0"; then
        CONFIGURED=$((CONFIGURED + 1))
      fi
      if [ -f "$HOME/.gemini/antigravity/mcp_config.json" ]; then
        merge_json_config "$HOME/.gemini/antigravity/mcp_config.json" "0" || true
      fi
      ;;
    claude_desktop)
      if [ "$OS_TAG" = "darwin" ]; then
        if merge_json_config "$HOME/Library/Application Support/Claude/claude_desktop_config.json" "1"; then
          CONFIGURED=$((CONFIGURED + 1))
        fi
      else
        if merge_json_config "$HOME/.config/Claude/claude_desktop_config.json" "1"; then
          CONFIGURED=$((CONFIGURED + 1))
        fi
      fi
      ;;
    claude_code)
      if command -v claude >/dev/null 2>&1; then
        claude mcp remove "$SERVER_NAME" >/dev/null 2>&1 || true
        if claude mcp add --transport http "$SERVER_NAME" "$MCP_URL" --header "Authorization: Bearer ${TOKEN}"; then
          echo "Configured Claude Code CLI MCP server: $SERVER_NAME"
          CONFIGURED=$((CONFIGURED + 1))
        fi
      fi
      ;;
  esac
done

if [ "$CONFIGURED" -eq 0 ]; then
  echo ""
  echo "Paste this snippet into your MCP client config:"
  cat <<EOF
{
  "mcpServers": {
    "${SERVER_NAME}": {
      "type": "http",
      "url": "${MCP_URL}",
      "headers": {
        "Authorization": "Bearer ${TOKEN}"
      }
    }
  }
}
EOF
fi

echo ""
echo "Running MCP self-check (initialize + whoami)..."
HDR_FILE=$(mktemp)
trap 'rm -f "$HDR_FILE"' EXIT
lfh_curl -fsS -D "$HDR_FILE" -X POST "$MCP_URL" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"lfh-connect","version":"1.0"}}}' >/dev/null
SID=$(grep -i '^Mcp-Session-Id:' "$HDR_FILE" | head -n 1 | cut -d':' -f2- | tr -d ' \r\n' || true)
if [ -n "$SID" ]; then
  WHO_RES=$(lfh_curl -fsS -X POST "$MCP_URL" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Mcp-Session-Id: ${SID}" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}')
  if [ -n "$WHO_RES" ]; then
    echo "Self-check OK  : session=${SID}"
    if command -v python3 >/dev/null 2>&1; then
      printf '%s' "$WHO_RES" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    txt = d.get('result',{}).get('content',[{}])[0].get('text','{}')
    w = json.loads(txt)
    if 'write' in (w.get('scopes') or []) and not (w.get('write_folders') or []):
        print('[LFH Tip] Key has write scope, but write_folders is empty. Set write_folders (e.g. *) in LFH Web UI before writing files.')
except Exception:
    pass
" || true
    fi
  fi
fi

PROXY_ACTIVE="${HTTP_PROXY:-}${HTTPS_PROXY:-}${http_proxy:-}${https_proxy:-}"
NOPROXY_ALL="${NO_PROXY:-},${no_proxy:-}"
if [ -n "$PROXY_ACTIVE" ] && [ "$IS_PRIV" -eq 1 ] && [ -n "$TARGET_HOST" ]; then
  NEED_FIX=0
  case "${NO_PROXY:-}${no_proxy:-}" in
    *'"'*) NEED_FIX=1 ;;
  esac
  case "$NOPROXY_ALL" in
    *"$TARGET_HOST"*|*"192.168."*|*"100.64."*) ;;
    *) NEED_FIX=1 ;;
  esac
  if [ "$NEED_FIX" -eq 1 ]; then
    echo ""
    echo "[LFH Proxy Warning] HTTP_PROXY/HTTPS_PROXY is active without bypassing LAN/Tailscale in NO_PROXY." >&2
    echo "Ensure your shell/IDE exports NO_PROXY cleanly before restarting your IDE:" >&2
    echo "  export NO_PROXY=\"localhost,127.0.0.1,::1,${TARGET_HOST},10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10\"" >&2
  fi
fi

echo "Done! Restart or reload MCP servers in your IDE to start collaborating."
`

func renderConnectScript(tmpl string, s *Server, r *http.Request, escaper func(string) string) string {
	baseURL, client, owner, serverName, agentName := resolveConnectScriptVars(s, r)
	replacer := strings.NewReplacer(
		"__LFH_BASE_URL__", escaper(baseURL),
		"__LFH_CLIENT__", escaper(client),
		"__LFH_OWNER__", escaper(owner),
		"__LFH_SERVER_NAME__", escaper(serverName),
		"__LFH_AGENT_NAME__", escaper(agentName),
	)
	return replacer.Replace(tmpl)
}

func (s *Server) handleAgentConnectPS1(w http.ResponseWriter, r *http.Request) {
	body := renderConnectScript(connectPS1Template, s, r, escapePSSingleQuote)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleAgentConnectSH(w http.ResponseWriter, r *http.Request) {
	body := renderConnectScript(connectSHTemplate, s, r, escapePOSIXSingleQuote)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
