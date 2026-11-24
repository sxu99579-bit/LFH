package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"lfh/internal/auth"
)

func TestAgentPairingRequestApproveClaimOneTimeAndReject(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// 1. External Agent self-requests access (including dangerous delete scope).
	reqBody, _ := json.Marshal(AgentPairingRequestInput{
		Name:                  "B-PC Cursor Agent",
		ClientType:            "cursor",
		ClientVersion:         "0.49.1",
		Hostname:              "DEV-B",
		OS:                    "windows",
		RequestedScopes:       []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeDelete},
		RequestedReadFolders:  []string{"*"},
		RequestedWriteFolders: []string{"shopapp"},
		Note:                  "Need workspace collaboration",
	})
	resp, err := http.Post(ts.URL+"/api/agent-pairing/request", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/agent-pairing/request: %v", err)
	}
	var createOut struct {
		OK          bool               `json:"ok"`
		ID          string             `json:"id"`
		ConfirmCode string             `json:"confirm_code"`
		PollSecret  string             `json:"poll_secret"`
		Status      string             `json:"status"`
		Request     PublicAgentPairing `json:"request"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&createOut)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !createOut.OK || createOut.ID == "" || createOut.PollSecret == "" || createOut.ConfirmCode == "" {
		t.Fatalf("unexpected pairing create response: status=%d out=%+v", resp.StatusCode, createOut)
	}

	// Wrong poll secret rejected (403); valid poll secret returns pending.
	badPoll, err := http.Get(ts.URL + "/api/agent-pairing/" + createOut.ID + "?secret=wrong")
	if err != nil {
		t.Fatalf("bad poll: %v", err)
	}
	badPoll.Body.Close()
	if badPoll.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong poll secret, got %d", badPoll.StatusCode)
	}

	pollReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agent-pairing/"+createOut.ID, nil)
	pollReq.Header.Set("X-Pairing-Secret", createOut.PollSecret)
	pollResp, _ := http.DefaultClient.Do(pollReq)
	var pollOut map[string]any
	_ = json.NewDecoder(pollResp.Body).Decode(&pollOut)
	pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK || pollOut["status"] != "pending" {
		t.Fatalf("expected pending status, got %+v", pollOut)
	}

	// 2. Logged-in human lists pairings (must not leak poll_secret) and approves with safe default (no delete scope!).
	listResp, err := adminClient.Get(ts.URL + "/api/agent-pairing")
	if err != nil {
		t.Fatalf("GET /api/agent-pairing: %v", err)
	}
	var listRaw bytes.Buffer
	_, _ = listRaw.ReadFrom(listResp.Body)
	listResp.Body.Close()
	if strings.Contains(listRaw.String(), createOut.PollSecret) {
		t.Fatalf("poll_secret leaked in list endpoint")
	}

	approveBody, _ := json.Marshal(map[string]any{
		"name":          "Approved Cursor",
		"read_folders":  []string{"*"},
		"write_folders": []string{"shopapp"},
	})
	appResp, err := adminClient.Post(ts.URL+"/api/agent-pairing/"+createOut.ID+"/approve", "application/json", bytes.NewReader(approveBody))
	if err != nil {
		t.Fatalf("POST approve: %v", err)
	}
	var appOut struct {
		OK  bool                `json:"ok"`
		Key auth.PublicAgentKey `json:"key"`
	}
	_ = json.NewDecoder(appResp.Body).Decode(&appOut)
	appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK || !appOut.OK {
		t.Fatalf("approve failed: status=%d out=%+v", appResp.StatusCode, appOut)
	}
	for _, sc := range appOut.Key.Scopes {
		if sc == auth.ScopeDelete {
			t.Fatalf("delete scope must NOT be auto-approved from requester proposal, got scopes=%v", appOut.Key.Scopes)
		}
	}

	// 3. First poll retrieves plainToken once; second poll gets status="claimed" with no token.
	claimReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agent-pairing/"+createOut.ID, nil)
	claimReq.Header.Set("X-Pairing-Secret", createOut.PollSecret)
	claimResp, _ := http.DefaultClient.Do(claimReq)
	var claimOut struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Token  string `json:"token"`
		MCPURL string `json:"mcp_url"`
	}
	_ = json.NewDecoder(claimResp.Body).Decode(&claimOut)
	claimResp.Body.Close()
	if !claimOut.OK || claimOut.Status != "approved" || !strings.HasPrefix(claimOut.Token, "lfh_ag_") || claimOut.MCPURL == "" {
		t.Fatalf("expected one-time token on first poll, got %+v", claimOut)
	}

	secondReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agent-pairing/"+createOut.ID, nil)
	secondReq.Header.Set("X-Pairing-Secret", createOut.PollSecret)
	secondResp, _ := http.DefaultClient.Do(secondReq)
	var secondOut struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	_ = json.NewDecoder(secondResp.Body).Decode(&secondOut)
	secondResp.Body.Close()
	if secondOut.Status != "claimed" || secondOut.Token != "" {
		t.Fatalf("expected claimed with empty token on second poll, got %+v", secondOut)
	}

	// Claimed token immediately works against /mcp.
	sid := initMCPSession(t, ts.URL, claimOut.Token)
	whoRes, _ := callMCPTool(t, ts.URL, claimOut.Token, sid, "whoami", map[string]any{})
	if whoRes.IsError {
		t.Fatalf("whoami with claimed token failed: %+v", whoRes)
	}

	// 4. Reject flow.
	req2Body, _ := json.Marshal(AgentPairingRequestInput{Name: "Unknown Script", ClientType: "custom"})
	r2, _ := http.Post(ts.URL+"/api/agent-pairing/request", "application/json", bytes.NewReader(req2Body))
	var out2 struct {
		ID         string `json:"id"`
		PollSecret string `json:"poll_secret"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&out2)
	r2.Body.Close()

	rejResp, err := adminClient.Post(ts.URL+"/api/agent-pairing/"+out2.ID+"/reject", "application/json", nil)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejResp.Body.Close()

	p2Req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agent-pairing/"+out2.ID, nil)
	p2Req.Header.Set("X-Pairing-Secret", out2.PollSecret)
	p2Resp, _ := http.DefaultClient.Do(p2Req)
	var p2Out struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	_ = json.NewDecoder(p2Resp.Body).Decode(&p2Out)
	p2Resp.Body.Close()
	if p2Out.Status != "rejected" || p2Out.Token != "" {
		t.Fatalf("expected rejected status, got %+v", p2Out)
	}
}

func TestMCPOAuth21FullPKCEFlow(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	// 1. Unauthenticated /mcp advertises OAuth protected-resource metadata.
	unauthReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatalf("unauth /mcp: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized || !strings.Contains(unauthResp.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource") {
		t.Fatalf("expected 401 with resource_metadata header, got %d %q", unauthResp.StatusCode, unauthResp.Header.Get("WWW-Authenticate"))
	}

	// 2. Metadata discovery endpoints.
	prmResp, _ := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	var prm map[string]any
	_ = json.NewDecoder(prmResp.Body).Decode(&prm)
	prmResp.Body.Close()
	if prmResp.StatusCode != http.StatusOK || prm["resource"] != ts.URL+"/mcp" {
		t.Fatalf("unexpected protected-resource metadata: %+v", prm)
	}

	asmResp, _ := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	var asm map[string]any
	_ = json.NewDecoder(asmResp.Body).Decode(&asm)
	asmResp.Body.Close()
	if asmResp.StatusCode != http.StatusOK || asm["token_endpoint"] != ts.URL+"/oauth/token" {
		t.Fatalf("unexpected oauth-authorization-server metadata: %+v", asm)
	}

	// 3. Dynamic Client Registration: reject javascript: redirect_uri, accept localhost.
	badReg, _ := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Bad","redirect_uris":["javascript:alert(1)"]}`))
	badReg.Body.Close()
	if badReg.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting javascript redirect URI, got %d", badReg.StatusCode)
	}

	redirURI := "http://127.0.0.1:43210/callback"
	regBody, _ := json.Marshal(map[string]any{
		"client_name":   "Antigravity IDE",
		"redirect_uris": []string{redirURI},
	})
	regResp, err := http.Post(ts.URL+"/oauth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("POST /oauth/register: %v", err)
	}
	var regOut struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(regResp.Body).Decode(&regOut)
	regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated || regOut.ClientID == "" {
		t.Fatalf("OAuth register failed: status=%d out=%+v", regResp.StatusCode, regOut)
	}

	// 4. Authorize GET: anonymous receives login page; logged-in human receives approval page with csrf_token.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	authURL := ts.URL + "/oauth/authorize?response_type=code&client_id=" + url.QueryEscape(regOut.ClientID) +
		"&redirect_uri=" + url.QueryEscape(redirURI) +
		"&code_challenge=" + url.QueryEscape(challenge) +
		"&code_challenge_method=S256&state=xyz123"

	anonAuth, _ := http.Get(authURL)
	var anonHTML bytes.Buffer
	_, _ = anonHTML.ReadFrom(anonAuth.Body)
	anonAuth.Body.Close()
	if anonAuth.StatusCode != http.StatusOK || !strings.Contains(anonHTML.String(), "/api/login") {
		t.Fatalf("expected login page for unauthenticated GET /oauth/authorize, status=%d", anonAuth.StatusCode)
	}

	adminClient := loginTestAdmin(t, srv, ts.URL)
	adminClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	appGet, err := adminClient.Get(authURL)
	if err != nil {
		t.Fatalf("authenticated GET /oauth/authorize: %v", err)
	}
	var appHTML bytes.Buffer
	_, _ = appHTML.ReadFrom(appGet.Body)
	appGet.Body.Close()
	marker := `name="csrf_token" value="`
	idx := strings.Index(appHTML.String(), marker)
	if idx < 0 {
		t.Fatalf("missing csrf_token in approval page: %s", appHTML.String())
	}
	rest := appHTML.String()[idx+len(marker):]
	endIdx := strings.IndexByte(rest, '"')
	csrfToken := rest[:endIdx]

	// 5. Submit approval form -> 302 redirect with one-time code.
	form := url.Values{
		"csrf_token":    {csrfToken},
		"decision":      {"approve"},
		"name":          {"Antigravity OAuth Agent"},
		"scopes":        {"read", "write"},
		"read_folders":  {"*"},
		"write_folders": {"shopapp"},
	}
	postRes, err := adminClient.PostForm(ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatalf("POST /oauth/authorize: %v", err)
	}
	postRes.Body.Close()
	if postRes.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect on authorize approve, got %d", postRes.StatusCode)
	}
	locURL, _ := url.Parse(postRes.Header.Get("Location"))
	code := locURL.Query().Get("code")
	if code == "" || locURL.Query().Get("state") != "xyz123" {
		t.Fatalf("unexpected OAuth redirect location: %q", postRes.Header.Get("Location"))
	}

	// 6. Exchange with valid PKCE verifier (Wait: test wrong verifier only? Note wrong verifier consumes code! So test valid exchange first, then replay!).
	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirURI},
		"client_id":     {regOut.ClientID},
		"code_verifier": {verifier},
	}
	tokResp, err := http.PostForm(ts.URL+"/oauth/token", tokForm)
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	var tokOut struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	_ = json.NewDecoder(tokResp.Body).Decode(&tokOut)
	tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK || !strings.HasPrefix(tokOut.AccessToken, "lfh_ag_") || tokOut.TokenType != "Bearer" {
		t.Fatalf("token exchange failed: status=%d out=%+v", tokResp.StatusCode, tokOut)
	}

	// Replaying same auth code must fail.
	replayTok, _ := http.PostForm(ts.URL+"/oauth/token", tokForm)
	replayTok.Body.Close()
	if replayTok.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 replaying OAuth code, got %d", replayTok.StatusCode)
	}

	// Exchanged access_token initializes /mcp cleanly.
	sid := initMCPSession(t, ts.URL, tokOut.AccessToken)
	whoRes, _ := callMCPTool(t, ts.URL, tokOut.AccessToken, sid, "whoami", map[string]any{})
	if whoRes.IsError {
		t.Fatalf("whoami via OAuth access_token failed: %+v", whoRes)
	}
}

func TestLiveAgentSessionsWorkingStatusReportAndForceDisconnect(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)
	adminUser, _, _ := srv.auth.InitialAdminCredentials()

	plainTok, pubKey, err := srv.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:          "LiveWorkerAgent",
		OwnerUsername: adminUser,
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite},
		ReadFolders:   []string{"*"},
		WriteFolders:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAgentKey: %v", err)
	}

	// Initialize with explicit clientInfo ("Cursor 0.49").
	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"clientInfo":      map[string]string{"name": "Cursor", "version": "0.49"},
		},
	})
	initReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Authorization", "Bearer "+plainTok)
	initResp, _ := http.DefaultClient.Do(initReq)
	sid := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()
	if sid == "" {
		t.Fatalf("expected session id")
	}

	// Report task status & acquire a lease via MCP.
	repRes, _ := callMCPTool(t, ts.URL, plainTok, sid, "report_agent_status", map[string]any{
		"state":    "working",
		"task":     "Refactor payment API",
		"progress": "3/5",
	})
	if repRes.IsError {
		t.Fatalf("report_agent_status failed: %+v", repRes)
	}

	// Query /api/agent-sessions.
	sessResp, err := adminClient.Get(ts.URL + "/api/agent-sessions")
	if err != nil {
		t.Fatalf("GET /api/agent-sessions: %v", err)
	}
	var sessOut struct {
		OK      bool                 `json:"ok"`
		Summary AgentPresenceSummary `json:"summary"`
		Agents  []AgentPresenceItem  `json:"agents"`
	}
	_ = json.NewDecoder(sessResp.Body).Decode(&sessOut)
	sessResp.Body.Close()
	if !sessOut.OK || sessOut.Summary.OnlineAgents != 1 || sessOut.Summary.WorkingAgents != 1 || len(sessOut.Agents) != 1 {
		t.Fatalf("unexpected agent-sessions summary: %+v", sessOut)
	}
	ag := sessOut.Agents[0]
	if !ag.Online || ag.Status != "working" || ag.ClientName != "Cursor" || ag.ReportedTask != "Refactor payment API" || ag.ReportedProgress != "3/5" || ag.ToolCalls == 0 {
		t.Fatalf("unexpected live agent presence item: %+v", ag)
	}

	// Force disconnect session via DELETE /api/agent-sessions/{sessionID}.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/agent-sessions/"+sid, nil)
	delResp, err := adminClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE session: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 deleting session, got %d", delResp.StatusCode)
	}

	// Subsequent /mcp request with old Mcp-Session-Id returns 404.
	pingBody := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	pReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", pingBody)
	pReq.Header.Set("Content-Type", "application/json")
	pReq.Header.Set("Authorization", "Bearer "+plainTok)
	pReq.Header.Set("Mcp-Session-Id", sid)
	pResp, _ := http.DefaultClient.Do(pReq)
	pResp.Body.Close()
	if pResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on disconnected session, got %d", pResp.StatusCode)
	}

	// Re-connect then disable key via toggle -> session must be kicked immediately.
	sid2 := initMCPSession(t, ts.URL, plainTok)
	togResp, _ := adminClient.Post(ts.URL+"/api/agent-keys/"+pubKey.ID+"/toggle", "application/json", strings.NewReader(`{"disabled":true}`))
	togResp.Body.Close()
	if len(srv.getMCPServer().ListSessions()) != 0 {
		t.Fatalf("expected disabling key %s to evict session %s", pubKey.ID, sid2)
	}
}
