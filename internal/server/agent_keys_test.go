package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/storage"
)

func mustRegisterAndLoginUser(t *testing.T, adminClient *http.Client, baseURL, username, password string) *http.Client {
	t.Helper()
	invResp, err := adminClient.Post(baseURL+"/api/admin/invites", "application/json", strings.NewReader(`{"note":"member","max_uses":5,"expire_hours":24}`))
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	defer invResp.Body.Close()
	if invResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 create invite, got %d", invResp.StatusCode)
	}
	var invOut struct {
		Invite auth.InviteCode `json:"invite"`
	}
	if err := json.NewDecoder(invResp.Body).Decode(&invOut); err != nil {
		t.Fatalf("decode invite: %v", err)
	}

	client := newCookieClient(t)
	body, _ := json.Marshal(map[string]string{
		"username":    username,
		"password":    password,
		"invite_code": invOut.Invite.Code,
	})
	regResp, err := client.Post(baseURL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 register %s, got %d", username, regResp.StatusCode)
	}
	return client
}

func doBearerRequest(t *testing.T, method, urlStr, token string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, urlStr, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, urlStr, err)
	}
	return resp
}

func uploadMultipartWithClient(t *testing.T, client *http.Client, baseURL, bearer, folder, name, content string, allowedUsers []string) (int, []storage.FileRecord, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if folder != "" {
		_ = mw.WriteField("folder_path", folder)
	}
	for _, au := range allowedUsers {
		_ = mw.WriteField("allowed_users", au)
	}
	part, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte(content))
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest upload: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	c := client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return resp.StatusCode, nil, raw
	}
	var out struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode upload response: %v body=%s", err, string(raw))
	}
	return resp.StatusCode, out.Uploaded, raw
}

func TestAgentKeysHTTPManagementAndAdminIsolation(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)
	aliceClient := mustRegisterAndLoginUser(t, adminClient, ts.URL, "alice", "pass1234")
	bobClient := mustRegisterAndLoginUser(t, adminClient, ts.URL, "bob", "pass1234")

	// Alice creates a key for herself; creating for bob must be 403.
	badForBob, _ := json.Marshal(map[string]any{
		"name":           "bad-cross",
		"owner_username": "bob",
		"write_folders":  []string{"*"},
	})
	respBad, err := aliceClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(badForBob))
	if err != nil {
		t.Fatalf("POST /api/agent-keys cross-owner: %v", err)
	}
	respBad.Body.Close()
	if respBad.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 non-admin creating key for bob, got %d", respBad.StatusCode)
	}

	aliceCreateBody, _ := json.Marshal(map[string]any{
		"name":          "alice-bot",
		"read_folders":  []string{"*"},
		"write_folders": []string{"alice-space"},
	})
	aliceCreateResp, err := aliceClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(aliceCreateBody))
	if err != nil {
		t.Fatalf("POST /api/agent-keys alice: %v", err)
	}
	var aliceCreated struct {
		Key   auth.PublicAgentKey `json:"key"`
		Token string              `json:"token"`
	}
	_ = json.NewDecoder(aliceCreateResp.Body).Decode(&aliceCreated)
	aliceCreateResp.Body.Close()
	if aliceCreateResp.StatusCode != http.StatusCreated || !strings.HasPrefix(aliceCreated.Token, auth.AgentTokenPrefix) {
		t.Fatalf("expected 201 alice key, got status=%d out=%+v", aliceCreateResp.StatusCode, aliceCreated)
	}

	// Admin creates a scoped key owned by admin (highest role in system).
	adminCreateBody, _ := json.Marshal(map[string]any{
		"name":          "admin-docs-bot",
		"scopes":        []string{"read", "write", "share", "verify"},
		"read_folders":  []string{"shopapp/docs"},
		"write_folders": []string{"shopapp/backend"},
	})
	adminCreateResp, err := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(adminCreateBody))
	if err != nil {
		t.Fatalf("POST /api/agent-keys admin: %v", err)
	}
	var adminCreated struct {
		Key   auth.PublicAgentKey `json:"key"`
		Token string              `json:"token"`
	}
	_ = json.NewDecoder(adminCreateResp.Body).Decode(&adminCreated)
	adminCreateResp.Body.Close()
	if adminCreateResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 admin key, got %d", adminCreateResp.StatusCode)
	}

	// Admin lists all keys (2), alice only sees her own (1), bob sees none (0).
	for _, tc := range []struct {
		client    *http.Client
		wantCount int
	}{
		{adminClient, 2},
		{aliceClient, 1},
		{bobClient, 0},
	} {
		lr, err := tc.client.Get(ts.URL + "/api/agent-keys")
		if err != nil {
			t.Fatalf("GET /api/agent-keys: %v", err)
		}
		var out struct {
			Keys []auth.PublicAgentKey `json:"keys"`
		}
		_ = json.NewDecoder(lr.Body).Decode(&out)
		lr.Body.Close()
		if len(out.Keys) != tc.wantCount {
			t.Fatalf("expected %d keys, got %d: %+v", tc.wantCount, len(out.Keys), out.Keys)
		}
	}

	// Bob cannot toggle, patch, or delete alice's key.
	reqTog, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/agent-keys/"+aliceCreated.Key.ID+"/toggle", nil)
	resTog, _ := bobClient.Do(reqTog)
	resTog.Body.Close()
	if resTog.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 bob toggling alice key, got %d", resTog.StatusCode)
	}

	// Admin-owned AgentKey MUST be rejected from ALL admin and human credential/cluster endpoints.
	blockedEndpoints := [][2]string{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodGet, "/api/admin/settings"},
		{http.MethodGet, "/api/admin/logs"},
		{http.MethodPost, "/api/password"},
		{http.MethodGet, "/api/token"},
		{http.MethodPost, "/api/token"},
		{http.MethodDelete, "/api/token"},
		{http.MethodGet, "/api/cluster"},
		{http.MethodGet, "/api/agent-keys"},
		{http.MethodPost, "/api/agent-keys"},
		{http.MethodGet, "/api/plugins/capabilities"},
		{http.MethodGet, "/api/plugins/jobs"},
	}
	for _, ep := range blockedEndpoints {
		r := doBearerRequest(t, ep[0], ts.URL+ep[1], adminCreated.Token, strings.NewReader(`{}`), "application/json")
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s with AgentKey expected 403 Forbidden, got %d body=%s", ep[0], ep[1], r.StatusCode, string(body))
		}
		if strings.Contains(string(body), srv.cluster.Secret()) {
			t.Fatalf("cluster secret leaked on %s %s", ep[0], ep[1])
		}
	}

	// GET /api/me must identify agent and omit admin-only fields.
	meResp := doBearerRequest(t, http.MethodGet, ts.URL+"/api/me", adminCreated.Token, nil, "")
	var meBody map[string]any
	_ = json.NewDecoder(meResp.Body).Decode(&meBody)
	meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK || meBody["is_agent"] != true {
		t.Fatalf("expected 200 with is_agent=true on /api/me, got status=%d body=%+v", meResp.StatusCode, meBody)
	}
	if _, hasPending := meBody["pending_users"]; hasPending {
		t.Fatalf("agent /api/me must not include admin pending_users")
	}

	// Toggle off & delete alice key works cleanly.
	toggleResp, _ := aliceClient.Post(ts.URL+"/api/agent-keys/"+aliceCreated.Key.ID+"/toggle", "application/json", nil)
	toggleResp.Body.Close()
	if toggleResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 toggling own key, got %d", toggleResp.StatusCode)
	}
	afterToggle := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files", aliceCreated.Token, nil, "")
	afterToggle.Body.Close()
	if afterToggle.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 when agent key disabled, got %d", afterToggle.StatusCode)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/agent-keys/"+aliceCreated.Key.ID, nil)
	delResp, _ := aliceClient.Do(delReq)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 deleting own key, got %d", delResp.StatusCode)
	}
}

func TestAgentKeyFolderSandboxUploadReadEditDeleteAndVisibility(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)
	_ = mustRegisterAndLoginUser(t, adminClient, ts.URL, "alice", "pass1234")
	bobClient := mustRegisterAndLoginUser(t, adminClient, ts.URL, "bob", "pass1234")

	// Admin seeds files in docs, backend2, and private folders.
	st, docFiles, _ := uploadMultipartWithClient(t, adminClient, ts.URL, "", "shopapp/docs", "spec.md", "# API Spec", nil)
	if st != http.StatusCreated {
		t.Fatalf("seed spec.md failed: %d", st)
	}
	docID := docFiles[0].ID

	st, siblingFiles, _ := uploadMultipartWithClient(t, adminClient, ts.URL, "", "shopapp/backend2", "other.txt", "sibling", nil)
	if st != http.StatusCreated {
		t.Fatalf("seed other.txt failed: %d", st)
	}
	siblingID := siblingFiles[0].ID

	st, privFiles, _ := uploadMultipartWithClient(t, adminClient, ts.URL, "", "private", "secret.txt", "top-secret", nil)
	if st != http.StatusCreated {
		t.Fatalf("seed secret.txt failed: %d", st)
	}
	privID := privFiles[0].ID

	// Create AgentKey owned by admin, read shopapp/docs, write shopapp/backend, NO delete scope,
	// default allowed_users=["alice"], allow_set_visibility=false.
	createBody, _ := json.Marshal(map[string]any{
		"name":                  "backend-coder",
		"read_folders":          []string{"shopapp/docs"},
		"write_folders":         []string{"shopapp/backend"},
		"default_allowed_users": []string{"alice"},
		"allow_set_visibility":  false,
	})
	cResp, _ := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(createBody))
	var created struct {
		Key   auth.PublicAgentKey `json:"key"`
		Token string              `json:"token"`
	}
	_ = json.NewDecoder(cResp.Body).Decode(&created)
	cResp.Body.Close()

	// 1. Writing outside write_folders (docs, backend2, root) must fail 403.
	for _, forbiddenFolder := range []string{"shopapp/docs", "shopapp/backend2", ""} {
		code, _, body := uploadMultipartWithClient(t, nil, ts.URL, created.Token, forbiddenFolder, "bad.txt", "nope", nil)
		if code != http.StatusForbidden {
			t.Fatalf("upload to %q expected 403, got %d body=%s", forbiddenFolder, code, string(body))
		}
	}

	// 2. Trying to override allowed_users when allow_set_visibility=false must fail 403.
	code, _, body := uploadMultipartWithClient(t, nil, ts.URL, created.Token, "shopapp/backend", "handler.go", "package main", []string{"bob"})
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 overriding allowed_users, got %d body=%s", code, string(body))
	}

	// 3. Valid upload to shopapp/backend/sub succeeds, tags Agent DeviceName, strips StorageDir/SourcePath,
	//    and enforces default_allowed_users=["alice"] + key owner.
	code, uploaded, body := uploadMultipartWithClient(t, nil, ts.URL, created.Token, "shopapp/backend/api", "handler.go", "package api", nil)
	if code != http.StatusCreated || len(uploaded) != 1 {
		t.Fatalf("expected 201 valid upload, got %d body=%s", code, string(body))
	}
	backendID := uploaded[0].ID
	if uploaded[0].DeviceName != "Agent · backend-coder" {
		t.Fatalf("expected DeviceName 'Agent · backend-coder', got %q", uploaded[0].DeviceName)
	}
	if uploaded[0].StorageDir != "" || uploaded[0].SourcePath != "" {
		t.Fatalf("agent upload response must strip StorageDir/SourcePath")
	}

	// Bob must not see or access handler.go because default_allowed_users=["alice"] (+admin owner).
	bobDL, _ := bobClient.Get(ts.URL + "/api/files/" + backendID + "/download")
	bobDL.Body.Close()
	if bobDL.StatusCode == http.StatusOK {
		t.Fatalf("expected bob denied from file restricted to default_allowed_users=[alice]")
	}

	// 4. GET /api/files returns only docs + backend, hiding backend2 & private.
	listResp := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files", created.Token, nil, "")
	var listOut struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&listOut)
	listResp.Body.Close()
	if len(listOut.Files) != 2 {
		t.Fatalf("expected exactly 2 visible files for agent, got %d: %+v", len(listOut.Files), listOut.Files)
	}
	for _, f := range listOut.Files {
		if f.ID == siblingID || f.ID == privID {
			t.Fatalf("agent /api/files leaked unauthorized file: %+v", f)
		}
		if f.StorageDir != "" || f.SourcePath != "" {
			t.Fatalf("agent /api/files leaked internal storage path: %+v", f)
		}
	}

	// 5. File meta & download: allowed for docs, 403 for backend2 & private.
	metaOK := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files/"+docID+"/meta", created.Token, nil, "")
	var metaMap map[string]any
	_ = json.NewDecoder(metaOK.Body).Decode(&metaMap)
	metaOK.Body.Close()
	if metaOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on allowed doc meta, got %d", metaOK.StatusCode)
	}
	if _, hasDisk := metaMap["disk_path"]; hasDisk {
		t.Fatalf("admin-owned agent must not receive disk_path in /meta")
	}
	for _, deniedID := range []string{siblingID, privID} {
		r := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files/"+deniedID+"/meta", created.Token, nil, "")
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 accessing %s meta, got %d", deniedID, r.StatusCode)
		}
	}

	// 6. Edit allowed inside write_folders, forbidden inside read-only docs.
	editDenied := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+docID+"/edit", created.Token, strings.NewReader(`{"content":"hacked"}`), "application/json")
	editDenied.Body.Close()
	if editDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 editing read-only docs file, got %d", editDenied.StatusCode)
	}
	editOK := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+backendID+"/edit", created.Token, strings.NewReader(`{"content":"package api // v2","note":"fix handler"}`), "application/json")
	editOK.Body.Close()
	if editOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 editing backend file, got %d", editOK.StatusCode)
	}

	// 7. Delete without delete scope fails 403; granting delete still forbids deleting docs, allows deleting backend.
	delDenied := doBearerRequest(t, http.MethodDelete, ts.URL+"/api/files/"+backendID, created.Token, nil, "")
	delDenied.Body.Close()
	if delDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 deleting without delete scope, got %d", delDenied.StatusCode)
	}

	// Create shortcut upload test in allowed folder vs denied folder.
	scDenied := doBearerRequest(t, http.MethodPost, ts.URL+"/api/shortcut/upload?folder=shopapp/docs&name=note.txt", created.Token, strings.NewReader("nope"), "text/plain")
	scDenied.Body.Close()
	if scDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 shortcut upload to read-only folder, got %d", scDenied.StatusCode)
	}
	scOK := doBearerRequest(t, http.MethodPost, ts.URL+"/api/shortcut/upload?folder=shopapp/backend&name=snippet.txt", created.Token, strings.NewReader("ok snippet"), "text/plain")
	scOK.Body.Close()
	if scOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 shortcut upload to backend folder, got %d", scOK.StatusCode)
	}

	// Grant delete scope to verify delete folder boundary.
	patchBody, _ := json.Marshal(map[string]any{
		"scopes": []string{"read", "write", "share", "delete"},
	})
	pReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/agent-keys/"+created.Key.ID, bytes.NewReader(patchBody))
	pReq.Header.Set("Content-Type", "application/json")
	pResp, _ := adminClient.Do(pReq)
	pResp.Body.Close()
	if pResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 patching agent key, got %d", pResp.StatusCode)
	}

	delDoc := doBearerRequest(t, http.MethodDelete, ts.URL+"/api/files/"+docID, created.Token, nil, "")
	delDoc.Body.Close()
	if delDoc.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 deleting read-only doc even with delete scope, got %d", delDoc.StatusCode)
	}
	delBackend := doBearerRequest(t, http.MethodDelete, ts.URL+"/api/files/"+backendID, created.Token, nil, "")
	delBackend.Body.Close()
	if delBackend.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 deleting backend file with delete scope, got %d", delBackend.StatusCode)
	}

	// Dedicated fresh key with rate_limit_per_min=3 -> verify 3 succeed and 4th returns 429 Too Many Requests.
	rateBody, _ := json.Marshal(map[string]any{
		"name":               "rate-check-bot",
		"read_folders":       []string{"shopapp/docs"},
		"rate_limit_per_min": 3,
	})
	rcResp, _ := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(rateBody))
	var rcOut struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rcResp.Body).Decode(&rcOut)
	rcResp.Body.Close()
	for i := 0; i < 3; i++ {
		r := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files", rcOut.Token, nil, "")
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("rate-limited request %d/3 expected 200, got %d", i+1, r.StatusCode)
		}
	}
	r4 := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files", rcOut.Token, nil, "")
	r4.Body.Close()
	if r4.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on 4th request, got %d", r4.StatusCode)
	}
}

func TestAgentKeyEventsStreamFolderIsolation(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	createBody, _ := json.Marshal(map[string]any{
		"name":          "sse-watcher",
		"read_folders":  []string{"shopapp/docs"},
		"write_folders": []string{"shopapp/backend"},
	})
	cResp, _ := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(createBody))
	var created struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(cResp.Body).Decode(&created)
	cResp.Body.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest /api/events: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+created.Token)
	sseClient := &http.Client{Timeout: 5 * time.Second}
	sseResp, err := sseClient.Do(req)
	if err != nil {
		t.Fatalf("connect /api/events: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /api/events, got %d", sseResp.StatusCode)
	}

	reader := bufio.NewReader(sseResp.Body)
	// Consume initial ready event.
	for i := 0; i < 5; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read ready line: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	// Publish unauthorized private event, then authorized backend event.
	uploadMultipartWithClient(t, adminClient, ts.URL, "", "private", "hidden.txt", "secret", nil)
	uploadMultipartWithClient(t, adminClient, ts.URL, "", "shopapp/backend", "visible.go", "package backend", nil)

	var eventPayload string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read sse event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			eventPayload = strings.TrimPrefix(strings.TrimSpace(line), "data: ")
			break
		}
	}
	if eventPayload == "" {
		t.Fatalf("expected authorized event on SSE stream")
	}
	if strings.Contains(eventPayload, "hidden.txt") {
		t.Fatalf("agent SSE stream leaked unauthorized private filename: %s", eventPayload)
	}
	if !strings.Contains(eventPayload, "visible.go") {
		t.Fatalf("expected visible.go in agent SSE payload, got: %s", eventPayload)
	}
}
