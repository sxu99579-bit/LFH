package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
)

const (
	maxOAuthClients          = 256
	oauthRegisterRatePerIP   = 20
	oauthPendingAuthTTL      = 5 * time.Minute
	oauthAuthCodeTTL         = 60 * time.Second
)

type oauthClient struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

type oauthPendingAuth struct {
	CSRFToken           string
	Username            string
	ClientID            string
	ClientName          string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	RequestedScopes     []string
	ExpiresAt           time.Time
}

type oauthAuthCode struct {
	Code          string
	PlainToken    string
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Scopes        []string
	AgentName     string
	ExpiresAt     time.Time
}

type mcpOAuthManager struct {
	mu      sync.Mutex
	clients map[string]*oauthClient
	pending map[string]*oauthPendingAuth
	codes   map[string]*oauthAuthCode
	ipHits  map[string][]time.Time
}

func newMCPOAuthManager() *mcpOAuthManager {
	return &mcpOAuthManager{
		clients: make(map[string]*oauthClient),
		pending: make(map[string]*oauthPendingAuth),
		codes:   make(map[string]*oauthAuthCode),
		ipHits:  make(map[string][]time.Time),
	}
}

func (s *Server) getMCPOAuth() *mcpOAuthManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpOAuth == nil {
		s.mcpOAuth = newMCPOAuthManager()
	}
	return s.mcpOAuth
}

func randomHexToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requestRemoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func isAllowedOAuthRedirectURI(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "cursor", "vscode", "vscode-insiders", "antigravity", "claude", "windsurf":
		return true
	case "https":
		return strings.TrimSpace(u.Host) != ""
	case "http":
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if host == "" {
			return false
		}
		if host == "localhost" {
			return true
		}
		return bridge.IsPrivateOrTailscaleAddr(net.JoinHostPort(host, "80"))
	default:
		return false
	}
}

func (m *mcpOAuthManager) checkRegisterRateLocked(ip string, now time.Time) bool {
	if ip == "" {
		ip = "unknown"
	}
	cutoff := now.Add(-1 * time.Minute)
	hits := m.ipHits[ip]
	kept := hits[:0]
	for _, ts := range hits {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= oauthRegisterRatePerIP {
		m.ipHits[ip] = kept
		return false
	}
	m.ipHits[ip] = append(kept, now)
	return true
}

func (m *mcpOAuthManager) pruneLocked(now time.Time) {
	for k, p := range m.pending {
		if now.After(p.ExpiresAt) {
			delete(m.pending, k)
		}
	}
	for k, c := range m.codes {
		if now.After(c.ExpiresAt) {
			c.PlainToken = ""
			delete(m.codes, k)
		}
	}
	if len(m.clients) > maxOAuthClients {
		var oldestID string
		var oldest time.Time
		for id, c := range m.clients {
			if oldestID == "" || c.LastUsedAt.Before(oldest) {
				oldestID = id
				oldest = c.LastUsedAt
			}
		}
		if oldestID != "" {
			delete(m.clients, oldestID)
		}
	}
}

func oauthBaseURL(s *Server, r *http.Request) string {
	selfURL := ""
	if s.cluster != nil {
		selfURL = s.cluster.SelfStatus().HTTPURL
	}
	return requestBaseURL(r, selfURL, s.port)
}

func (s *Server) handleOAuthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := oauthBaseURL(s, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare, auth.ScopeVerify},
	})
}

func (s *Server) handleOAuthAuthServerMeta(w http.ResponseWriter, r *http.Request) {
	base := oauthBaseURL(s, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"scopes_supported":                      []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare, auth.ScopeVerify},
	})
}

func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	if len(in.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "redirect_uris required"})
		return
	}
	cleanRedirects := make([]string, 0, len(in.RedirectURIs))
	for _, uri := range in.RedirectURIs {
		trimmed := strings.TrimSpace(uri)
		if !isAllowedOAuthRedirectURI(trimmed) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "disallowed redirect_uri scheme or host"})
			return
		}
		cleanRedirects = append(cleanRedirects, trimmed)
	}

	now := time.Now()
	ip := requestRemoteIP(r)
	om := s.getMCPOAuth()
	om.mu.Lock()
	om.pruneLocked(now)
	if !om.checkRegisterRateLocked(ip, now) {
		om.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded"})
		return
	}
	cid := "mcp_cli_" + randomHexToken(12)
	cname := sanitizePairingText(in.ClientName, 64)
	if cname == "" {
		cname = "MCP OAuth Client"
	}
	om.clients[cid] = &oauthClient{
		ClientID:     cid,
		ClientName:   cname,
		RedirectURIs: cleanRedirects,
		CreatedAt:    now,
		LastUsedAt:   now,
	}
	om.pruneLocked(now)
	om.mu.Unlock()

	authMethod := strings.TrimSpace(in.TokenEndpointAuthMethod)
	if authMethod == "" {
		authMethod = "none"
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  cid,
		"client_name":                cname,
		"redirect_uris":              cleanRedirects,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": authMethod,
	})
}

var oauthLoginTpl = template.Must(template.New("oauthLogin").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录 QuailNest 审批接入 — {{.ClientName}}</title>
<style>
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f172a;color:#e2e8f0;display:flex;min-height:100vh;align-items:center;justify-content:center;padding:16px}
.card{width:100%;max-width:400px;background:#1e293b;border:1px solid #334155;border-radius:14px;padding:24px;box-sizing:border-box}
h1{font-size:18px;margin:0 0 6px}
p{font-size:13px;color:#94a3b8;margin:0 0 18px;line-height:1.5}
label{display:block;font-size:12px;margin:12px 0 4px;color:#cbd5e1}
input{width:100%;box-sizing:border-box;padding:9px 11px;border-radius:8px;border:1px solid #475569;background:#0f172a;color:#f8fafc;font-size:14px}
button{width:100%;margin-top:18px;padding:10px;border:0;border-radius:8px;background:#3b82f6;color:#fff;font-size:14px;font-weight:600;cursor:pointer}
.err{margin-top:10px;color:#f87171;font-size:12px;min-height:16px}
</style></head><body><div class="card">
<h1>登录 QuailNest 以审批 Agent 接入</h1>
<p>客户端 <strong>{{.ClientName}}</strong> 请求通过 MCP 协议接入你的工作区。请先验证身份。</p>
<form id="f">
<label>用户名</label><input id="u" required autocomplete="username">
<label>密码</label><input id="p" type="password" required autocomplete="current-password">
<button type="submit">登录并继续审批</button>
<div class="err" id="e"></div>
</form></div>
<script>
document.getElementById('f').addEventListener('submit', async function(ev){
  ev.preventDefault();
  var e=document.getElementById('e'); e.textContent='';
  try {
    var res = await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:document.getElementById('u').value.trim(),password:document.getElementById('p').value})});
    var d = await res.json().catch(function(){return {};});
    if(!res.ok){ e.textContent=d.error||'登录失败'; return; }
    window.location.reload();
  } catch(err){ e.textContent='网络异常，请重试'; }
});
</script></body></html>`))

var oauthApproveTpl = template.Must(template.New("oauthApprove").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>授权 AI Agent 接入 — QuailNest</title>
<style>
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f172a;color:#e2e8f0;display:flex;min-height:100vh;align-items:center;justify-content:center;padding:16px}
.card{width:100%;max-width:520px;background:#1e293b;border:1px solid #334155;border-radius:14px;padding:24px;box-sizing:border-box}
h1{font-size:18px;margin:0 0 6px}
.sub{font-size:13px;color:#94a3b8;margin:0 0 16px;line-height:1.5}
.meta{background:#0f172a;border:1px solid #334155;border-radius:8px;padding:10px 12px;font-size:12px;color:#cbd5e1;margin-bottom:16px}
label.lbl{display:block;font-size:12px;font-weight:600;margin:12px 0 6px}
input[type=text]{width:100%;box-sizing:border-box;padding:8px 10px;border-radius:8px;border:1px solid #475569;background:#0f172a;color:#f8fafc;font-size:13px}
.scopes{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}
.scope{display:flex;align-items:center;gap:6px;background:#0f172a;border:1px solid #334155;border-radius:8px;padding:8px;font-size:12px;cursor:pointer}
.actions{display:flex;gap:10px;margin-top:20px}
button{flex:1;padding:10px;border-radius:8px;border:0;font-size:14px;font-weight:600;cursor:pointer}
.ok{background:#10b981;color:#06281e}
.no{background:#334155;color:#e2e8f0}
</style></head><body><div class="card">
<h1>允许 AI Agent 接入 QuailNest 吗？</h1>
<p class="sub">批准后将为账号 <strong>{{.Username}}</strong> 签发专属受限密钥（仅回传给本地客户端，不会放宽管理员隔离边界）。</p>
<div class="meta">客户端：<strong>{{.ClientName}}</strong><br>回调目标：<code>{{.RedirectHost}}</code></div>
<form method="POST" action="/oauth/authorize">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label class="lbl">Agent 名称</label>
<input type="text" name="name" value="{{.DefaultAgentName}}" required>
<label class="lbl">授予权限（Scopes）</label>
<div class="scopes">
<label class="scope"><input type="checkbox" name="scopes" value="read" checked> 读 (read)</label>
<label class="scope"><input type="checkbox" name="scopes" value="write" checked> 写与补丁 (write)</label>
<label class="scope"><input type="checkbox" name="scopes" value="share"> 调整可见成员 (share)</label>
<label class="scope"><input type="checkbox" name="scopes" value="verify"> 工作区构建校验 (verify)</label>
<label class="scope"><input type="checkbox" name="scopes" value="delete"> 删除文件/版本 (高危)</label>
</div>
<label class="lbl">可读目录白名单（逗号分隔，* 为全部）</label>
<input type="text" name="read_folders" value="*">
<label class="lbl">可写目录白名单（逗号分隔，留空则只读）</label>
<input type="text" name="write_folders" value="">
<div class="actions">
<button type="submit" name="decision" value="deny" class="no">拒绝接入</button>
<button type="submit" name="decision" value="approve" class="ok">同意并授权接入</button>
</div></form></div></body></html>`))

func (m *mcpOAuthManager) validateAuthorizeParamsLocked(clientID, redirectURI, responseType, challenge, method string) (*oauthClient, string) {
	c, ok := m.clients[strings.TrimSpace(clientID)]
	if !ok {
		return nil, "未知的 client_id，请先调用 /oauth/register 注册"
	}
	if strings.TrimSpace(responseType) != "code" {
		return nil, "response_type 必须为 code"
	}
	matchedURI := false
	for _, allowed := range c.RedirectURIs {
		if allowed == redirectURI {
			matchedURI = true
			break
		}
	}
	if !matchedURI {
		return nil, "redirect_uri 与客户端注册记录不匹配"
	}
	if strings.TrimSpace(method) != "S256" || len(strings.TrimSpace(challenge)) < 32 {
		return nil, "必须启用 PKCE S256 (code_challenge_method=S256)"
	}
	c.LastUsedAt = time.Now()
	return c, ""
}

func (s *Server) handleOAuthAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := strings.TrimSpace(q.Get("client_id"))
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	responseType := strings.TrimSpace(q.Get("response_type"))
	state := q.Get("state")
	challenge := strings.TrimSpace(q.Get("code_challenge"))
	method := strings.TrimSpace(q.Get("code_challenge_method"))
	scopeRaw := strings.Fields(q.Get("scope"))

	now := time.Now()
	om := s.getMCPOAuth()
	om.mu.Lock()
	om.pruneLocked(now)
	client, errMsg := om.validateAuthorizeParamsLocked(clientID, redirectURI, responseType, challenge, method)
	var clientName string
	if client != nil {
		clientName = client.ClientName
	}
	om.mu.Unlock()
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}

	u, loggedIn := s.auth.CurrentUser(r)
	if !loggedIn {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = oauthLoginTpl.Execute(w, map[string]any{
			"ClientName": clientName,
		})
		return
	}

	csrf := "csrf_" + randomHexToken(16)
	om.mu.Lock()
	om.pending[csrf] = &oauthPendingAuth{
		CSRFToken:           csrf,
		Username:            u.Username,
		ClientID:            clientID,
		ClientName:          clientName,
		RedirectURI:         redirectURI,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		RequestedScopes:     scopeRaw,
		ExpiresAt:           now.Add(oauthPendingAuthTTL),
	}
	om.mu.Unlock()

	parsedRedir, _ := url.Parse(redirectURI)
	redirHost := redirectURI
	if parsedRedir != nil && parsedRedir.Host != "" {
		redirHost = parsedRedir.Scheme + "://" + parsedRedir.Host
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = oauthApproveTpl.Execute(w, map[string]any{
		"Username":         u.Username,
		"ClientName":       clientName,
		"RedirectHost":     redirHost,
		"CSRFToken":        csrf,
		"DefaultAgentName": clientName,
	})
}

func appendQueryToRedirect(baseURI string, params map[string]string) string {
	u, err := url.Parse(baseURI)
	if err != nil {
		return baseURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func parseOAuthFolderList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	return sanitizeProposedFolders(parts)
}

func (s *Server) handleOAuthAuthorizePost(w http.ResponseWriter, r *http.Request) {
	u, ok := s.auth.CurrentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	csrf := strings.TrimSpace(r.FormValue("csrf_token"))
	decision := strings.TrimSpace(r.FormValue("decision"))

	now := time.Now()
	om := s.getMCPOAuth()
	om.mu.Lock()
	om.pruneLocked(now)
	tx, exists := om.pending[csrf]
	if exists {
		delete(om.pending, csrf)
	}
	om.mu.Unlock()

	if !exists || now.After(tx.ExpiresAt) || tx.Username != u.Username {
		http.Error(w, "授权会话已失效，请重新发起授权", http.StatusBadRequest)
		return
	}

	if decision != "approve" {
		target := appendQueryToRedirect(tx.RedirectURI, map[string]string{
			"error": "access_denied",
			"state": tx.State,
		})
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	agentName := sanitizePairingText(r.FormValue("name"), 64)
	if agentName == "" {
		agentName = tx.ClientName
	}
	scopes := r.Form["scopes"]
	if len(scopes) == 0 {
		scopes = []string{auth.ScopeRead}
	}
	readFolders := parseOAuthFolderList(r.FormValue("read_folders"))
	writeFolders := parseOAuthFolderList(r.FormValue("write_folders"))
	if len(readFolders) == 0 && len(writeFolders) == 0 {
		readFolders = []string{"*"}
	}

	plainToken, pubKey, err := s.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:            agentName,
		OwnerUsername:   u.Username,
		Scopes:          scopes,
		ReadFolders:     readFolders,
		WriteFolders:    writeFolders,
		RateLimitPerMin: 240,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	code := "ac_" + randomHexToken(20)
	om.mu.Lock()
	om.codes[code] = &oauthAuthCode{
		Code:          code,
		PlainToken:    plainToken,
		ClientID:      tx.ClientID,
		RedirectURI:   tx.RedirectURI,
		CodeChallenge: tx.CodeChallenge,
		Scopes:        append([]string(nil), pubKey.Scopes...),
		AgentName:     pubKey.Name,
		ExpiresAt:     time.Now().Add(oauthAuthCodeTTL),
	}
	om.mu.Unlock()

	s.logAudit(r, u.Username, "mcp_oauth_approve", pubKey.Name, "ok", "key="+pubKey.ID+" client="+tx.ClientID)
	if s.events != nil {
		s.PublishEvent(FileChangeEvent{
			Kind:         "agent_pairing",
			Uploader:     u.Username,
			ActorName:    pubKey.Name,
			NodeName:     "oauth_approved",
			AllowedUsers: []string{u.Username},
		})
	}

	target := appendQueryToRedirect(tx.RedirectURI, map[string]string{
		"code":  code,
		"state": tx.State,
	})
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var grantType, code, redirectURI, clientID, codeVerifier string

	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			GrantType    string `json:"grant_type"`
			Code         string `json:"code"`
			RedirectURI  string `json:"redirect_uri"`
			ClientID     string `json:"client_id"`
			CodeVerifier string `json:"code_verifier"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		grantType, code, redirectURI, clientID, codeVerifier = body.GrantType, body.Code, body.RedirectURI, body.ClientID, body.CodeVerifier
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		grantType = r.FormValue("grant_type")
		code = r.FormValue("code")
		redirectURI = r.FormValue("redirect_uri")
		clientID = r.FormValue("client_id")
		codeVerifier = r.FormValue("code_verifier")
	}

	if strings.TrimSpace(grantType) != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	cleanCode := strings.TrimSpace(code)
	now := time.Now()
	om := s.getMCPOAuth()
	om.mu.Lock()
	om.pruneLocked(now)
	entry, ok := om.codes[cleanCode]
	if ok {
		delete(om.codes, cleanCode)
	}
	om.mu.Unlock()

	if !ok || now.After(entry.ExpiresAt) || entry.PlainToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	tok := entry.PlainToken
	entry.PlainToken = ""

	if strings.TrimSpace(clientID) != entry.ClientID || strings.TrimSpace(redirectURI) != entry.RedirectURI {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if strings.TrimSpace(codeVerifier) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(computed), []byte(entry.CodeChallenge)) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok,
		"token_type":   "Bearer",
		"scope":        strings.Join(entry.Scopes, " "),
	})
}
