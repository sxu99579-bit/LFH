package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"lfh/internal/auth"
)

func loginUserClient(t *testing.T, baseURL, username, password string) (*http.Client, *http.Response, map[string]any) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	resp, err := client.Post(baseURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/login(%s): %v", username, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	return client, resp, parsed
}

func doJSONReq(t *testing.T, client *http.Client, method, targetURL string, payload any) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, targetURL, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, targetURL, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}

func TestUserGovernanceEndToEnd(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// Upload two files into different folders as admin for folder ACL filtering verification.
	docCreateRes, _ := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":        "public_doc.txt",
		"folder_path": "docs",
		"content":     "hello docs",
	})
	if docCreateRes.StatusCode != http.StatusCreated {
		t.Fatalf("create docs file status=%d", docCreateRes.StatusCode)
	}
	privCreateRes, _ := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":        "secret_root.txt",
		"folder_path": "",
		"content":     "root secret",
	})
	if privCreateRes.StatusCode != http.StatusCreated {
		t.Fatalf("create root file status=%d", privCreateRes.StatusCode)
	}

	// 1. Admin creates readonly user with allowed_folders=["docs"] and quota=1024.
	createRes, createRaw := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"username":        "reader1",
		"role":            "readonly",
		"display_name":    "Reader One",
		"note":            "Scoped readonly member",
		"quota_bytes":     1024,
		"allowed_folders": []string{"docs"},
	})
	if createRes.StatusCode != http.StatusCreated {
		t.Fatalf("create readonly user failed: %d %s", createRes.StatusCode, string(createRaw))
	}
	var createOut struct {
		OK                bool   `json:"ok"`
		GeneratedPassword string `json:"generated_password"`
	}
	_ = json.Unmarshal(createRaw, &createOut)
	if !createOut.OK || createOut.GeneratedPassword == "" {
		t.Fatalf("expected generated_password from admin create user, got %s", string(createRaw))
	}

	readerClient, loginRes, _ := loginUserClient(t, ts.URL, "reader1", createOut.GeneratedPassword)
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("readonly user login status=%d", loginRes.StatusCode)
	}

	// Sees only docs/ file.
	filesRes, filesRaw := doJSONReq(t, readerClient, http.MethodGet, ts.URL+"/api/files", nil)
	if filesRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/files as reader1 status=%d", filesRes.StatusCode)
	}
	var listOut struct {
		Files []struct {
			Name       string `json:"name"`
			FolderPath string `json:"folder_path"`
		} `json:"files"`
	}
	_ = json.Unmarshal(filesRaw, &listOut)
	if len(listOut.Files) != 1 || listOut.Files[0].Name != "public_doc.txt" {
		t.Fatalf("expected only docs file visible to reader1, got %+v", listOut.Files)
	}

	// Cannot create txt/upload, create folder, or create Agent key.
	roTxtRes, _ := doJSONReq(t, readerClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":        "nope.txt",
		"folder_path": "docs",
		"content":     "blocked",
	})
	if roTxtRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 create-txt for readonly user, got %d", roTxtRes.StatusCode)
	}
	roFolderRes, _ := doJSONReq(t, readerClient, http.MethodPost, ts.URL+"/api/folders", map[string]any{
		"path": "docs/newsub",
	})
	if roFolderRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 POST /api/folders for readonly user, got %d", roFolderRes.StatusCode)
	}
	roAgentRes, _ := doJSONReq(t, readerClient, http.MethodPost, ts.URL+"/api/agent-keys", map[string]any{
		"name":         "ro-agent",
		"scopes":       []string{"read"},
		"read_folders": []string{"*"},
	})
	if roAgentRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 POST /api/agent-keys for readonly user, got %d", roAgentRes.StatusCode)
	}

	// 2. Promote reader1 to regular user with 100-byte quota + cleared folder restriction -> 50B OK, 200B -> 507.
	updRes, updRaw := doJSONReq(t, adminClient, http.MethodPatch, ts.URL+"/api/admin/users/reader1", map[string]any{
		"role":            "user",
		"quota_bytes":     100,
		"allowed_folders": []string{},
		"can_use_agents":  true,
	})
	if updRes.StatusCode != http.StatusOK {
		t.Fatalf("PATCH user failed: %d %s", updRes.StatusCode, string(updRaw))
	}
	smallRes, smallRaw := doJSONReq(t, readerClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":        "small.txt",
		"folder_path": "work",
		"content":     strings.Repeat("a", 50),
	})
	if smallRes.StatusCode != http.StatusCreated {
		t.Fatalf("expected 50-byte create-txt 201, got %d %s", smallRes.StatusCode, string(smallRaw))
	}
	overRes, _ := doJSONReq(t, readerClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":        "toolarge.txt",
		"folder_path": "work",
		"content":     strings.Repeat("b", 200),
	})
	if overRes.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("expected 507 quota exceeded on 200B upload, got %d", overRes.StatusCode)
	}

	// 3. Admin resets password with must_change_password=true -> old cookie immediately 401; login temp pwd records last_login & allows password change.
	resetRes, resetRaw := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users/reader1/reset-password", map[string]any{
		"must_change_password": true,
	})
	if resetRes.StatusCode != http.StatusOK {
		t.Fatalf("reset-password failed: %d %s", resetRes.StatusCode, string(resetRaw))
	}
	var resetOut struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	_ = json.Unmarshal(resetRaw, &resetOut)
	if resetOut.TemporaryPassword == "" {
		t.Fatalf("expected temporary_password in response")
	}

	staleMeRes, _ := doJSONReq(t, readerClient, http.MethodGet, ts.URL+"/api/me", nil)
	if staleMeRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected old cookie invalidated (401) after password reset, got %d", staleMeRes.StatusCode)
	}

	tempClient, tempLoginRes, tempLoginMap := loginUserClient(t, ts.URL, "reader1", resetOut.TemporaryPassword)
	if tempLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("temp password login failed: %d", tempLoginRes.StatusCode)
	}
	uMap, _ := tempLoginMap["user"].(map[string]any)
	if uMap["must_change_password"] != true || uMap["last_login_ip"] == "" || uMap["last_login_at"] == "" {
		t.Fatalf("expected must_change_password=true and last_login populated, got %+v", uMap)
	}

	chgRes, chgRaw := doJSONReq(t, tempClient, http.MethodPost, ts.URL+"/api/password", map[string]any{
		"old_password": resetOut.TemporaryPassword,
		"new_password": "FinalUserPass99",
	})
	if chgRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/password failed: %d %s", chgRes.StatusCode, string(chgRaw))
	}
	afterChgMeRes, afterChgRaw := doJSONReq(t, tempClient, http.MethodGet, ts.URL+"/api/me", nil)
	if afterChgMeRes.StatusCode != http.StatusOK {
		t.Fatalf("expected refreshed cookie valid after password change, got %d %s", afterChgMeRes.StatusCode, string(afterChgRaw))
	}
	var meOut struct {
		User auth.PublicUser `json:"user"`
	}
	_ = json.Unmarshal(afterChgRaw, &meOut)
	if meOut.User.MustChangePassword {
		t.Fatal("expected must_change_password cleared after password change")
	}

	// 4. Create an Agent key + personal token, test ForceLogout & FreezeAgents.
	tokRes, tokRaw := doJSONReq(t, tempClient, http.MethodPost, ts.URL+"/api/token", nil)
	if tokRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/token failed: %d", tokRes.StatusCode)
	}
	var tokOut struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tokRaw, &tokOut)

	agRes, agRaw := doJSONReq(t, tempClient, http.MethodPost, ts.URL+"/api/agent-keys", map[string]any{
		"name":         "reader-worker",
		"scopes":       []string{"read"},
		"read_folders": []string{"*"},
	})
	if agRes.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/agent-keys failed: %d %s", agRes.StatusCode, string(agRaw))
	}
	var agOut struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(agRaw, &agOut)

	forceRes, _ := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users/reader1/force-logout", nil)
	if forceRes.StatusCode != http.StatusOK {
		t.Fatalf("force-logout failed: %d", forceRes.StatusCode)
	}
	kickedMe, _ := doJSONReq(t, tempClient, http.MethodGet, ts.URL+"/api/me", nil)
	if kickedMe.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after force-logout, got %d", kickedMe.StatusCode)
	}
	reqTok, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/files", nil)
	reqTok.Header.Set("Authorization", "Bearer "+tokOut.Token)
	respTok, _ := http.DefaultClient.Do(reqTok)
	respTok.Body.Close()
	if respTok.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected personal API token invalidated (401) after force-logout, got %d", respTok.StatusCode)
	}

	freezeRes, freezeRaw := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users/reader1/agents/freeze", map[string]any{
		"disabled": true,
	})
	if freezeRes.StatusCode != http.StatusOK {
		t.Fatalf("freeze agents failed: %d %s", freezeRes.StatusCode, string(freezeRaw))
	}
	reqAg, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/files", nil)
	reqAg.Header.Set("Authorization", "Bearer "+agOut.Token)
	respAg, _ := http.DefaultClient.Do(reqAg)
	respAg.Body.Close()
	if respAg.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected Agent token rejected (401) while frozen, got %d", respAg.StatusCode)
	}

	// 5. Batch action + Detail drawer endpoint.
	batchRes, _ := doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users/batch", map[string]any{
		"usernames":   []string{"reader1"},
		"action":      "set_quota",
		"quota_bytes": 4096,
	})
	if batchRes.StatusCode != http.StatusOK {
		t.Fatalf("batch set_quota failed: %d", batchRes.StatusCode)
	}
	detRes, detRaw := doJSONReq(t, adminClient, http.MethodGet, ts.URL+"/api/admin/users/reader1/detail", nil)
	if detRes.StatusCode != http.StatusOK {
		t.Fatalf("GET detail failed: %d %s", detRes.StatusCode, string(detRaw))
	}
	var detOut struct {
		User struct {
			FileCount     int   `json:"file_count"`
			UsedBytes     int64 `json:"used_bytes"`
			QuotaBytes    int64 `json:"quota_bytes"`
			AgentKeyCount int   `json:"agent_key_count"`
		} `json:"user"`
		AgentKeys    []auth.PublicAgentKey `json:"agent_keys"`
		AuditEntries []any                 `json:"audit_entries"`
	}
	_ = json.Unmarshal(detRaw, &detOut)
	if detOut.User.FileCount != 1 || detOut.User.UsedBytes != 50 || detOut.User.QuotaBytes != 4096 || len(detOut.AgentKeys) != 1 || len(detOut.AuditEntries) == 0 {
		t.Fatalf("unexpected user detail payload: %+v entries=%d", detOut.User, len(detOut.AuditEntries))
	}

	// 6. Delete user with file_action=transfer reassigns small.txt to admin; create+delete with file_action=delete removes file.
	delTransferRes, delTransferRaw := doJSONReq(t, adminClient, http.MethodDelete, ts.URL+"/api/admin/users/reader1?file_action=transfer", nil)
	if delTransferRes.StatusCode != http.StatusOK {
		t.Fatalf("delete transfer failed: %d %s", delTransferRes.StatusCode, string(delTransferRaw))
	}
	foundTransferred := false
	for _, rec := range srv.store.AllRecords() {
		if rec.Name == "small.txt" && rec.Uploader != "reader1" {
			foundTransferred = true
			break
		}
	}
	if !foundTransferred {
		t.Fatal("expected small.txt reassigned away from reader1 to admin")
	}

	_, _ = doJSONReq(t, adminClient, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"username": "temp_del",
		"password": "temp_del_pass",
		"role":     "user",
	})
	tempDelClient, _, _ := loginUserClient(t, ts.URL, "temp_del", "temp_del_pass")
	_, _ = doJSONReq(t, tempDelClient, http.MethodPost, ts.URL+"/api/files/create-txt", map[string]any{
		"name":    "temp_owned.txt",
		"content": "to be deleted",
	})
	delPurgeRes, delPurgeRaw := doJSONReq(t, adminClient, http.MethodDelete, ts.URL+"/api/admin/users/temp_del?file_action=delete", nil)
	if delPurgeRes.StatusCode != http.StatusOK {
		t.Fatalf("delete purge failed: %d %s", delPurgeRes.StatusCode, string(delPurgeRaw))
	}
	for _, rec := range srv.store.AllRecords() {
		if rec.Name == "temp_owned.txt" {
			t.Fatal("expected temp_owned.txt purged on user delete file_action=delete")
		}
	}
}
