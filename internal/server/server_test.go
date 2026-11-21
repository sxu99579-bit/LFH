package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/storage"
	"lfh/internal/updater"
	"lfh/internal/version"
)

//go:embed server.go
var testEmbedFS embed.FS

func newTestServerWithCluster(t *testing.T, opts cluster.Options) (*Server, *cluster.Manager, *storage.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	authMgr, err := auth.NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager auth: %v", err)
	}
	store, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	opts.DiscoveryPort = 0
	opts.DiscoveryPortSet = true
	cm, err := cluster.NewManager(dir, store, opts)
	if err != nil {
		t.Fatalf("NewManager cluster: %v", err)
	}
	upMgr, err := updater.NewManager(dir, "")
	if err != nil {
		t.Fatalf("NewManager updater: %v", err)
	}
	srv := &Server{
		auth:    authMgr,
		store:   store,
		cluster: cm,
		events:  NewEventHub(),
		updater: upMgr,
		webFS:   testEmbedFS,
		port:    "8080",
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	cm.SetSelfURL(ts.URL)
	return srv, cm, store, ts
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv, _, _, ts := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		QuotaGB:  10,
		QuotaSet: true,
	})
	return srv, ts
}

func loginTestAdmin(t *testing.T, srv *Server, baseURL string) *http.Client {
	t.Helper()
	user, pass, _ := srv.auth.InitialAdminCredentials()
	client := newCookieClient(t)
	body, _ := json.Marshal(map[string]string{
		"username": user,
		"password": pass,
	})
	resp, err := client.Post(baseURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 admin login, got %d", resp.StatusCode)
	}
	return client
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func newNoRedirectClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestInfoInvitesAndUploadDeviceFingerprint(t *testing.T) {
	srv, ts := newTestServer(t)

	// 1. GET /api/info should succeed without login and include app_name/version.
	res, err := http.Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /api/info, got %d", res.StatusCode)
	}
	var infoResp struct {
		AppName        string              `json:"app_name"`
		Version        string              `json:"version"`
		EntryURL       string              `json:"entry_url"`
		LocalURL       string              `json:"local_url"`
		Role           string              `json:"role"`
		InviteRequired bool                `json:"invite_required"`
		Pool           cluster.PoolSummary `json:"pool"`
	}
	if err := json.NewDecoder(res.Body).Decode(&infoResp); err != nil {
		t.Fatalf("decode /api/info: %v", err)
	}
	if infoResp.AppName != version.AppName || infoResp.Version != version.Version || !infoResp.InviteRequired || infoResp.LocalURL != "http://localhost:8080" || infoResp.Role != "entry" || infoResp.Pool.OnlineNodes != 1 {
		t.Fatalf("unexpected /api/info: %+v", infoResp)
	}

	// 2. Register without invite must return 400.
	regNoInv, err := http.Post(ts.URL+"/api/register", "application/json", strings.NewReader(`{"username":"bob","password":"pass1234"}`))
	if err != nil {
		t.Fatalf("POST /api/register without invite: %v", err)
	}
	regNoInv.Body.Close()
	if regNoInv.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without invite, got %d", regNoInv.StatusCode)
	}

	// 3. Login as generated admin, create an invite code.
	adminClient := loginTestAdmin(t, srv, ts.URL)

	invResp, err := adminClient.Post(ts.URL+"/api/admin/invites", "application/json", strings.NewReader(`{"note":"test-device","max_uses":1,"expire_hours":24}`))
	if err != nil {
		t.Fatalf("POST /api/admin/invites: %v", err)
	}
	defer invResp.Body.Close()
	if invResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating invite, got %d", invResp.StatusCode)
	}
	var created struct {
		Invite auth.InviteCode `json:"invite"`
	}
	if err := json.NewDecoder(invResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created invite: %v", err)
	}
	if created.Invite.Code == "" {
		t.Fatalf("expected generated invite code")
	}

	// 4. Register new user with the invite code.
	userClient := newCookieClient(t)
	regBody, _ := json.Marshal(map[string]string{
		"username":    "bob",
		"password":    "pass1234",
		"invite_code": created.Invite.Code,
	})
	regOK, err := userClient.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("POST /api/register with invite: %v", err)
	}
	regOK.Body.Close()
	if regOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 register with invite, got %d", regOK.StatusCode)
	}

	// 5. Non-admin user must get 403 on admin invite routes.
	forbidResp, err := userClient.Post(ts.URL+"/api/admin/invites", "application/json", strings.NewReader(`{"max_uses":1}`))
	if err != nil {
		t.Fatalf("non-admin POST /api/admin/invites: %v", err)
	}
	forbidResp.Body.Close()
	if forbidResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d", forbidResp.StatusCode)
	}

	// 6. Upload with explicit device fields + upload without device fields (server fallback).
	uploadWithFields := func(withDevice bool) storage.FileRecord {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("files", "hello.txt")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = io.WriteString(fw, "hello world")
		if withDevice {
			_ = mw.WriteField("device_fp", "FP-CUSTOM1234")
			_ = mw.WriteField("device_name", "Windows · Edge")
			_ = mw.WriteField("device_detail", "1920x1080 · 8核")
		}
		_ = mw.Close()

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", &buf)
		if err != nil {
			t.Fatalf("NewRequest upload: %v", err)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Edg/157.0")
		resp, err := userClient.Do(req)
		if err != nil {
			t.Fatalf("Do upload: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 upload, got %d", resp.StatusCode)
		}
		var out struct {
			Uploaded []storage.FileRecord `json:"uploaded"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode upload: %v", err)
		}
		if len(out.Uploaded) != 1 {
			t.Fatalf("expected 1 uploaded record, got %d", len(out.Uploaded))
		}
		return out.Uploaded[0]
	}

	rExplicit := uploadWithFields(true)
	if rExplicit.DeviceFP != "FP-CUSTOM1234" || rExplicit.DeviceName != "Windows · Edge" || rExplicit.DeviceIP == "" {
		t.Fatalf("unexpected explicit device record: %+v", rExplicit)
	}

	rFallback := uploadWithFields(false)
	if !strings.HasPrefix(rFallback.DeviceFP, "FP-") || rFallback.DeviceName != "Windows · Edge" || rFallback.DeviceIP == "" {
		t.Fatalf("unexpected fallback device record: %+v", rFallback)
	}
}

func TestDistributedEntryAndWorkerCluster(t *testing.T) {
	sharedSecret := "cluster-shared-secret-999"

	// Entry has tiny quota (~1 KB) so a 4 KB upload routes automatically to Worker.
	entrySrv, _, entryStore, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:          cluster.RoleEntry,
		NodeName:      "PC-Entry",
		ClusterSecret: sharedSecret,
		QuotaGB:       0.000001, // ~1073 bytes
		QuotaSet:      true,
	})

	_, workerMgr, workerStore, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:          cluster.RoleWorker,
		NodeName:      "PC-Worker",
		ClusterSecret: sharedSecret,
		QuotaGB:       10,
		QuotaSet:      true,
		EntryHint:     entryTS.URL,
	})

	// 1. Worker sends heartbeat to Entry.
	if err := workerMgr.SendHeartbeatNow(); err != nil {
		t.Fatalf("SendHeartbeatNow: %v", err)
	}

	// Verify /api/info shows 2 online nodes.
	infoRes, err := http.Get(entryTS.URL + "/api/info")
	if err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	var info struct {
		Pool cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(infoRes.Body).Decode(&info)
	infoRes.Body.Close()
	if info.Pool.OnlineNodes != 2 {
		t.Fatalf("expected 2 online nodes, got %d", info.Pool.OnlineNodes)
	}

	// 2. Worker root URL must redirect 302 to Entry URL (single exposed entry).
	noRedir := newNoRedirectClient(t)
	redirRes, err := noRedir.Get(workerTS.URL + "/")
	if err != nil {
		t.Fatalf("GET worker root: %v", err)
	}
	redirRes.Body.Close()
	if redirRes.StatusCode != http.StatusFound || redirRes.Header.Get("Location") != entryTS.URL {
		t.Fatalf("expected 302 redirect to %s, got %d location=%q", entryTS.URL, redirRes.StatusCode, redirRes.Header.Get("Location"))
	}

	// 3. Admin logs in on Entry and uploads a 4 KB markdown file with target_node=auto.
	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)

	mdContent := "# Cluster Hello\n" + strings.Repeat("A", 4096)
	var upBuf bytes.Buffer
	mw := multipart.NewWriter(&upBuf)
	_ = mw.WriteField("target_node", "auto")
	_ = mw.WriteField("device_fp", "FP-TESTCLUSTER")
	_ = mw.WriteField("device_name", "Windows · Chrome")
	fw, _ := mw.CreateFormFile("files", "cluster.md")
	_, _ = io.WriteString(fw, mdContent)
	_ = mw.Close()

	upReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/api/upload", &upBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRes, err := adminClient.Do(upReq)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(upRes.Body)
		t.Fatalf("expected 201 upload, got %d: %s", upRes.StatusCode, string(body))
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	if len(upOut.Uploaded) != 1 {
		t.Fatalf("expected 1 uploaded file")
	}
	rec := upOut.Uploaded[0]
	if rec.NodeID != workerMgr.NodeID() || rec.NodeName != "PC-Worker" {
		t.Fatalf("expected file routed to worker %s, got node=%s (%s)", workerMgr.NodeID(), rec.NodeID, rec.NodeName)
	}

	// Verify stored physically on Worker, NOT Entry.
	if cnt, _ := entryStore.LocalStats(); cnt != 0 {
		t.Fatalf("expected 0 local files on entry, got %d", cnt)
	}
	if cnt, _ := workerStore.LocalStats(); cnt != 1 {
		t.Fatalf("expected 1 local file on worker, got %d", cnt)
	}

	// 4. GET /api/files shows the file tagged with Worker's node info.
	listRes, err := adminClient.Get(entryTS.URL + "/api/files")
	if err != nil {
		t.Fatalf("GET /api/files: %v", err)
	}
	var listOut struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(listRes.Body).Decode(&listOut)
	listRes.Body.Close()
	if len(listOut.Files) != 1 || listOut.Files[0].NodeID != workerMgr.NodeID() {
		t.Fatalf("unexpected /api/files: %+v", listOut.Files)
	}

	// 5. GET /api/files/{id}/raw with Range: bytes=0-5 returns 206 Partial Content proxied from Worker.
	rawReq, _ := http.NewRequest(http.MethodGet, entryTS.URL+"/api/files/"+rec.ID+"/raw", nil)
	rawReq.Header.Set("Range", "bytes=0-5")
	rawRes, err := adminClient.Do(rawReq)
	if err != nil {
		t.Fatalf("GET raw range: %v", err)
	}
	rawBytes, _ := io.ReadAll(rawRes.Body)
	rawRes.Body.Close()
	if rawRes.StatusCode != http.StatusPartialContent || string(rawBytes) != "# Clus" {
		t.Fatalf("expected 206 with '# Clus', got status=%d body=%q", rawRes.StatusCode, string(rawBytes))
	}

	// 6. GET /api/files/{id}/preview returns parsed markdown proxied from Worker.
	prevRes, err := adminClient.Get(entryTS.URL + "/api/files/" + rec.ID + "/preview")
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	var prevOut struct {
		IsMD    bool   `json:"is_md"`
		Content string `json:"content"`
	}
	_ = json.NewDecoder(prevRes.Body).Decode(&prevOut)
	prevRes.Body.Close()
	if prevRes.StatusCode != http.StatusOK || !prevOut.IsMD || !strings.HasPrefix(prevOut.Content, "# Cluster Hello") {
		t.Fatalf("unexpected preview: status=%d is_md=%v", prevRes.StatusCode, prevOut.IsMD)
	}

	// 7. POST /api/admin/cluster/quota with quota_text="500MB" updates Worker quota remotely.
	quotaBody, _ := json.Marshal(map[string]any{
		"node_id":    workerMgr.NodeID(),
		"quota_text": "500MB",
	})
	qRes, err := adminClient.Post(entryTS.URL+"/api/admin/cluster/quota", "application/json", bytes.NewReader(quotaBody))
	if err != nil {
		t.Fatalf("POST quota: %v", err)
	}
	qRes.Body.Close()
	if qRes.StatusCode != http.StatusOK || workerMgr.SelfStatus().QuotaBytes != 500*cluster.MB {
		t.Fatalf("expected worker quota updated to 500 MB, status=%d got=%d", qRes.StatusCode, workerMgr.SelfStatus().QuotaBytes)
	}

	// 8. DELETE /api/files/{id} removes file physically from Worker.
	delReq, _ := http.NewRequest(http.MethodDelete, entryTS.URL+"/api/files/"+rec.ID, nil)
	delRes, err := adminClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE file: %v", err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 delete, got %d", delRes.StatusCode)
	}
	if cnt, _ := workerStore.LocalStats(); cnt != 0 {
		t.Fatalf("expected 0 files on worker after delete, got %d", cnt)
	}
}

func TestAdminQuotaUnitsAndEndpointSwitch(t *testing.T) {
	srv, cm, _, ts := newTestServerWithCluster(t, cluster.Options{
		Role:       cluster.RoleEntry,
		QuotaBytes: 10 * cluster.GB,
		QuotaSet:   true,
	})

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// 1. Update local quota using quota_text="500MB" and quota_bytes=2TB.
	q1Res, err := adminClient.Post(ts.URL+"/api/admin/cluster/quota", "application/json", strings.NewReader(`{"quota_text":"500MB"}`))
	if err != nil {
		t.Fatalf("POST quota 500MB: %v", err)
	}
	q1Res.Body.Close()
	if q1Res.StatusCode != http.StatusOK || cm.SelfStatus().QuotaBytes != 500*cluster.MB {
		t.Fatalf("expected 500MB quota, status=%d got=%d", q1Res.StatusCode, cm.SelfStatus().QuotaBytes)
	}

	q2Body, _ := json.Marshal(map[string]any{
		"node_id":     cm.NodeID(),
		"quota_bytes": 2 * cluster.TB,
	})
	q2Res, err := adminClient.Post(ts.URL+"/api/admin/cluster/quota", "application/json", bytes.NewReader(q2Body))
	if err != nil {
		t.Fatalf("POST quota 2TB: %v", err)
	}
	q2Res.Body.Close()
	if q2Res.StatusCode != http.StatusOK || cm.SelfStatus().QuotaBytes != 2*cluster.TB {
		t.Fatalf("expected 2TB quota, status=%d got=%d", q2Res.StatusCode, cm.SelfStatus().QuotaBytes)
	}

	// 2. Endpoint port switch failure must leave old config untouched.
	srv.SetPortSwitcher(func(newPort string) error {
		return errors.New("port busy")
	})
	failRes, err := adminClient.Post(ts.URL+"/api/admin/cluster/endpoint", "application/json", strings.NewReader(`{"expose_ip":"100.120.53.119","port":"9090"}`))
	if err != nil {
		t.Fatalf("POST endpoint fail: %v", err)
	}
	failRes.Body.Close()
	if failRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on port switch failure, got %d", failRes.StatusCode)
	}
	if srv.Port() != "8080" || cm.ExposeIP() != "" {
		t.Fatalf("expected config unchanged after failed port switch, got port=%s expose=%q", srv.Port(), cm.ExposeIP())
	}

	// 3. Successful endpoint switch updates entry_url immediately and persists.
	var switchedTo string
	srv.SetPortSwitcher(func(newPort string) error {
		switchedTo = newPort
		return nil
	})
	okRes, err := adminClient.Post(ts.URL+"/api/admin/cluster/endpoint", "application/json", strings.NewReader(`{"expose_ip":"100.120.53.119","port":"9090"}`))
	if err != nil {
		t.Fatalf("POST endpoint ok: %v", err)
	}
	defer okRes.Body.Close()
	if okRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on endpoint update, got %d", okRes.StatusCode)
	}
	var epOut struct {
		EntryURL    string `json:"entry_url"`
		ExposeIP    string `json:"expose_ip"`
		Port        string `json:"port"`
		PortChanged bool   `json:"port_changed"`
	}
	if err := json.NewDecoder(okRes.Body).Decode(&epOut); err != nil {
		t.Fatalf("decode endpoint out: %v", err)
	}
	if switchedTo != "9090" || !epOut.PortChanged || epOut.EntryURL != "http://100.120.53.119:9090" || cm.EntryURL() != "http://100.120.53.119:9090" {
		t.Fatalf("unexpected endpoint response: %+v", epOut)
	}
}

func TestApprovalRegistrationBanAndDeleteUser(t *testing.T) {
	srv, ts := newTestServer(t)

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// 1. Admin turns off invite_required.
	setRes, err := adminClient.Post(ts.URL+"/api/admin/settings", "application/json", strings.NewReader(`{"invite_required":false}`))
	if err != nil {
		t.Fatalf("POST settings: %v", err)
	}
	setRes.Body.Close()
	if setRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 settings update, got %d", setRes.StatusCode)
	}

	infoRes, err := http.Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	var info struct {
		InviteRequired bool `json:"invite_required"`
	}
	_ = json.NewDecoder(infoRes.Body).Decode(&info)
	infoRes.Body.Close()
	if info.InviteRequired {
		t.Fatalf("expected invite_required=false on /api/info")
	}

	// 2. Unicode user registers without invite code -> 202 pending, no session cookie.
	userClient := newCookieClient(t)
	regBody, _ := json.Marshal(map[string]string{
		"username": "测试用户",
		"password": "password123",
	})
	regRes, err := userClient.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("POST register pending: %v", err)
	}
	var regOut struct {
		Pending bool `json:"pending"`
	}
	_ = json.NewDecoder(regRes.Body).Decode(&regOut)
	regRes.Body.Close()
	if regRes.StatusCode != http.StatusAccepted || !regOut.Pending {
		t.Fatalf("expected 202 pending=true, got status=%d pending=%v", regRes.StatusCode, regOut.Pending)
	}

	// 3. Pending user cannot login (403).
	pendLogin, err := userClient.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("pending login: %v", err)
	}
	pendLogin.Body.Close()
	if pendLogin.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 login while pending, got %d", pendLogin.StatusCode)
	}

	// 4. Admin approves user -> user logs in -> GET /api/me returns 200.
	escUser := url.PathEscape("测试用户")
	appRes, err := adminClient.Post(ts.URL+"/api/admin/users/"+escUser+"/approve", "application/json", nil)
	if err != nil {
		t.Fatalf("approve user: %v", err)
	}
	appRes.Body.Close()
	if appRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 approve, got %d", appRes.StatusCode)
	}

	okLogin, err := userClient.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("approved login: %v", err)
	}
	okLogin.Body.Close()
	if okLogin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 approved login, got %d", okLogin.StatusCode)
	}
	meRes, err := userClient.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	meRes.Body.Close()
	if meRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 /api/me before ban, got %d", meRes.StatusCode)
	}

	// 5. Admin bans user -> existing cookie invalidated (401) and login returns 403.
	banRes, err := adminClient.Post(ts.URL+"/api/admin/users/"+escUser+"/ban", "application/json", nil)
	if err != nil {
		t.Fatalf("ban user: %v", err)
	}
	banRes.Body.Close()
	if banRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 ban, got %d", banRes.StatusCode)
	}
	meAfterBan, err := userClient.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me after ban: %v", err)
	}
	meAfterBan.Body.Close()
	if meAfterBan.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 /api/me after ban, got %d", meAfterBan.StatusCode)
	}
	bannedLogin, err := userClient.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("banned login: %v", err)
	}
	bannedLogin.Body.Close()
	if bannedLogin.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 login while banned, got %d", bannedLogin.StatusCode)
	}

	// 6. Admin unbans user -> login succeeds again (200).
	unbanRes, err := adminClient.Post(ts.URL+"/api/admin/users/"+escUser+"/unban", "application/json", nil)
	if err != nil {
		t.Fatalf("unban user: %v", err)
	}
	unbanRes.Body.Close()
	if unbanRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 unban, got %d", unbanRes.StatusCode)
	}
	unbanLogin, err := userClient.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("unbanned login: %v", err)
	}
	unbanLogin.Body.Close()
	if unbanLogin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 login after unban, got %d", unbanLogin.StatusCode)
	}

	// 7. Admin deletes user -> login fails (401).
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/admin/users/"+escUser, nil)
	delRes, err := adminClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete user: %v", err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 delete user, got %d", delRes.StatusCode)
	}
	delLogin, err := userClient.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("deleted user login: %v", err)
	}
	delLogin.Body.Close()
	if delLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 login after user deletion, got %d", delLogin.StatusCode)
	}
}

func TestUploadPolicyFolderUploadAndArchivePreviewExtract(t *testing.T) {
	srv, ts := newTestServer(t)

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// 1. Set blocked_exts=["exe"] while keeping invite_required untouched.
	setRes, err := adminClient.Post(ts.URL+"/api/admin/settings", "application/json", strings.NewReader(`{"blocked_exts":["exe"]}`))
	if err != nil {
		t.Fatalf("POST settings: %v", err)
	}
	setRes.Body.Close()
	if setRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 settings, got %d", setRes.StatusCode)
	}

	// 2. Verify .exe upload is rejected (400).
	var exeBuf bytes.Buffer
	mwExe := multipart.NewWriter(&exeBuf)
	fwExe, _ := mwExe.CreateFormFile("files", "virus.exe")
	_, _ = io.WriteString(fwExe, "mz")
	mwExe.Close()
	exeReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", &exeBuf)
	exeReq.Header.Set("Content-Type", mwExe.FormDataContentType())
	exeResp, err := adminClient.Do(exeReq)
	if err != nil {
		t.Fatalf("upload exe: %v", err)
	}
	exeResp.Body.Close()
	if exeResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for blocked .exe, got %d", exeResp.StatusCode)
	}

	// 3. Folder upload with relative_paths preserves folder_path.
	var fldBuf bytes.Buffer
	mwFld := multipart.NewWriter(&fldBuf)
	_ = mwFld.WriteField("relative_paths", "proj/sub/a.txt")
	fwFld, _ := mwFld.CreateFormFile("files", "a.txt")
	_, _ = io.WriteString(fwFld, "folder content")
	mwFld.Close()
	fldReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", &fldBuf)
	fldReq.Header.Set("Content-Type", mwFld.FormDataContentType())
	fldResp, err := adminClient.Do(fldReq)
	if err != nil {
		t.Fatalf("upload folder: %v", err)
	}
	var fldOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(fldResp.Body).Decode(&fldOut)
	fldResp.Body.Close()
	if fldResp.StatusCode != http.StatusCreated || len(fldOut.Uploaded) != 1 || fldOut.Uploaded[0].FolderPath != "proj/sub" {
		t.Fatalf("unexpected folder upload: status=%d out=%+v", fldResp.StatusCode, fldOut)
	}

	// 4. Build and upload a real .zip containing docs/readme.md and bad/virus.exe.
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	w1, _ := zw.Create("docs/readme.md")
	_, _ = io.WriteString(w1, "# Archive Doc")
	w2, _ := zw.Create("bad/virus.exe")
	_, _ = io.WriteString(w2, "mz-bad")
	zw.Close()

	var upZip bytes.Buffer
	mwZip := multipart.NewWriter(&upZip)
	fwZip, _ := mwZip.CreateFormFile("files", "bundle.zip")
	_, _ = fwZip.Write(zipBytes.Bytes())
	mwZip.Close()
	zipReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", &upZip)
	zipReq.Header.Set("Content-Type", mwZip.FormDataContentType())
	zipResp, err := adminClient.Do(zipReq)
	if err != nil {
		t.Fatalf("upload zip: %v", err)
	}
	var zipOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(zipResp.Body).Decode(&zipOut)
	zipResp.Body.Close()
	if zipResp.StatusCode != http.StatusCreated || len(zipOut.Uploaded) != 1 {
		t.Fatalf("unexpected zip upload: status=%d", zipResp.StatusCode)
	}
	zipID := zipOut.Uploaded[0].ID

	// 5. GET /api/files/{id}/preview returns archive entries.
	prevRes, err := adminClient.Get(ts.URL + "/api/files/" + zipID + "/preview")
	if err != nil {
		t.Fatalf("preview zip: %v", err)
	}
	var archOut struct {
		Type       string `json:"type"`
		TotalFiles int    `json:"total_files"`
	}
	_ = json.NewDecoder(prevRes.Body).Decode(&archOut)
	prevRes.Body.Close()
	if prevRes.StatusCode != http.StatusOK || archOut.Type != "archive" || archOut.TotalFiles != 2 {
		t.Fatalf("unexpected archive preview: status=%d out=%+v", prevRes.StatusCode, archOut)
	}

	// 6. Preview & download individual entry inside zip.
	entPrev, err := adminClient.Get(ts.URL + "/api/files/" + zipID + "/archive-entry?path=" + url.QueryEscape("docs/readme.md") + "&mode=preview")
	if err != nil {
		t.Fatalf("archive entry preview: %v", err)
	}
	var mdPrev struct {
		IsMD    bool   `json:"is_md"`
		Content string `json:"content"`
	}
	_ = json.NewDecoder(entPrev.Body).Decode(&mdPrev)
	entPrev.Body.Close()
	if entPrev.StatusCode != http.StatusOK || !mdPrev.IsMD || mdPrev.Content != "# Archive Doc" {
		t.Fatalf("unexpected archive entry preview: status=%d out=%+v", entPrev.StatusCode, mdPrev)
	}

	entDl, err := adminClient.Get(ts.URL + "/api/files/" + zipID + "/archive-entry?path=" + url.QueryEscape("docs/readme.md") + "&mode=download")
	if err != nil {
		t.Fatalf("archive entry download: %v", err)
	}
	dlBody, _ := io.ReadAll(entDl.Body)
	entDl.Body.Close()
	if entDl.StatusCode != http.StatusOK || string(dlBody) != "# Archive Doc" {
		t.Fatalf("unexpected archive entry download: status=%d body=%q", entDl.StatusCode, string(dlBody))
	}

	// 7. POST /api/files/{id}/extract extracts docs/readme.md and skips bad/virus.exe due to blocked_exts.
	extRes, err := adminClient.Post(ts.URL+"/api/files/"+zipID+"/extract", "application/json", nil)
	if err != nil {
		t.Fatalf("extract archive: %v", err)
	}
	var extOut struct {
		Extracted []storage.FileRecord `json:"extracted"`
		Skipped   []string             `json:"skipped"`
	}
	_ = json.NewDecoder(extRes.Body).Decode(&extOut)
	extRes.Body.Close()
	if extRes.StatusCode != http.StatusCreated || len(extOut.Extracted) != 1 || len(extOut.Skipped) != 1 {
		t.Fatalf("unexpected extract result: status=%d out=%+v", extRes.StatusCode, extOut)
	}
	if extOut.Extracted[0].Name != "readme.md" || extOut.Extracted[0].FolderPath != "bundle/docs" {
		t.Fatalf("unexpected extracted record: %+v", extOut.Extracted[0])
	}
}

func TestScoreNetworkCandidates(t *testing.T) {
	candidates := []ifaceCandidate{
		{Iface: "Mihomo", IP: net.ParseIP("198.18.0.1")},
		{Iface: "Tailscale", IP: net.ParseIP("100.120.53.119")},
		{Iface: "以太网 3", IP: net.ParseIP("192.168.126.1")},
		{Iface: "VMware Network Adapter VMnet8", IP: net.ParseIP("192.168.174.1")},
		{Iface: "WLAN", IP: net.ParseIP("192.168.3.53")},
		{Iface: "vEthernet (Default Switch)", IP: net.ParseIP("172.31.96.1")},
	}
	addrs := scoreNetworkCandidates(candidates, "8080")
	if len(addrs) != 5 {
		t.Fatalf("expected 5 addresses (198.18.0.1 excluded), got %d: %+v", len(addrs), addrs)
	}
	if addrs[0].IP != "192.168.3.53" || !addrs[0].Primary || addrs[0].Tag != "推荐局域网" {
		t.Fatalf("expected WLAN 192.168.3.53 as primary LAN, got %+v", addrs[0])
	}
	if addrs[1].IP != "100.120.53.119" || addrs[1].Tag != "Tailscale" {
		t.Fatalf("expected Tailscale second, got %+v", addrs[1])
	}
}

func TestLiveEventsSSEOnUploadDeleteAndPolicy(t *testing.T) {
	srv, ts := newTestServer(t)

	sseClient := loginTestAdmin(t, srv, ts.URL)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	sseRes, err := sseClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer sseRes.Body.Close()

	if sseRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 SSE, got %d", sseRes.StatusCode)
	}
	if ct := sseRes.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	type sseFrame struct {
		event string
		data  string
	}
	frames := make(chan sseFrame, 16)
	readErr := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(sseRes.Body)
		var cur sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if cur.event != "" || cur.data != "" {
					frames <- cur
					cur = sseFrame{}
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			if val, ok := strings.CutPrefix(line, "event:"); ok {
				cur.event = strings.TrimSpace(val)
			} else if val, ok := strings.CutPrefix(line, "data:"); ok {
				cur.data = strings.TrimSpace(val)
			}
		}
		readErr <- scanner.Err()
	}()

	nextFrame := func(wantEvent string) sseFrame {
		t.Helper()
		select {
		case f := <-frames:
			if f.event != wantEvent {
				t.Fatalf("expected event %q, got %+v", wantEvent, f)
			}
			return f
		case err := <-readErr:
			t.Fatalf("SSE stream ended before %q: %v", wantEvent, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for SSE event %q", wantEvent)
		}
		return sseFrame{}
	}

	readyFrame := nextFrame("ready")
	var readyPayload struct {
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(readyFrame.data), &readyPayload); err != nil || readyPayload.Version == 0 {
		t.Fatalf("unexpected ready payload %q: %v", readyFrame.data, err)
	}

	// Upload a file via second client -> SSE receives kind=upload and /api/cluster version increases.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", "live-note.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = fw.Write([]byte("hello live sync"))
	_ = mw.Close()

	upRes, err := adminClient.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated || len(upOut.Uploaded) != 1 {
		t.Fatalf("unexpected upload response: status=%d out=%+v", upRes.StatusCode, upOut)
	}
	uploadedID := upOut.Uploaded[0].ID

	upFrame := nextFrame("change")
	var upEv FileChangeEvent
	if err := json.Unmarshal([]byte(upFrame.data), &upEv); err != nil {
		t.Fatalf("decode upload event: %v", err)
	}
	if upEv.Kind != "upload" || upEv.Count != 1 || upEv.Version <= readyPayload.Version {
		t.Fatalf("unexpected upload event: %+v", upEv)
	}

	clRes, err := adminClient.Get(ts.URL + "/api/cluster")
	if err != nil {
		t.Fatalf("GET /api/cluster: %v", err)
	}
	var clOut struct {
		EventsVersion uint64 `json:"events_version"`
	}
	_ = json.NewDecoder(clRes.Body).Decode(&clOut)
	clRes.Body.Close()
	if clRes.StatusCode != http.StatusOK || clOut.EventsVersion < upEv.Version {
		t.Fatalf("expected events_version >= %d, got status=%d ver=%d", upEv.Version, clRes.StatusCode, clOut.EventsVersion)
	}

	// Delete uploaded file -> SSE receives kind=delete.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/files/"+uploadedID, nil)
	delRes, err := adminClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE file: %v", err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", delRes.StatusCode)
	}

	delFrame := nextFrame("change")
	var delEv FileChangeEvent
	if err := json.Unmarshal([]byte(delFrame.data), &delEv); err != nil {
		t.Fatalf("decode delete event: %v", err)
	}
	if delEv.Kind != "delete" || delEv.Count != 1 || delEv.Version <= upEv.Version {
		t.Fatalf("unexpected delete event: %+v", delEv)
	}

	// Update settings -> SSE receives kind=policy.
	setRes, err := adminClient.Post(ts.URL+"/api/admin/settings", "application/json", strings.NewReader(`{"blocked_exts":["exe"]}`))
	if err != nil {
		t.Fatalf("POST settings: %v", err)
	}
	setRes.Body.Close()
	if setRes.StatusCode != http.StatusOK {
		t.Fatalf("settings status=%d", setRes.StatusCode)
	}

	polFrame := nextFrame("change")
	var polEv FileChangeEvent
	if err := json.Unmarshal([]byte(polFrame.data), &polEv); err != nil {
		t.Fatalf("decode policy event: %v", err)
	}
	if polEv.Kind != "policy" || polEv.Version <= delEv.Version {
		t.Fatalf("unexpected policy event: %+v", polEv)
	}

	cancel()
	sseRes.Body.Close()
}

func TestAdminUpdateConfigAndStatus(t *testing.T) {
	srv, ts := newTestServer(t)

	// Unauthenticated request to /api/admin/update must fail with 401.
	unauthRes, err := http.Get(ts.URL + "/api/admin/update")
	if err != nil {
		t.Fatalf("GET /api/admin/update unauth: %v", err)
	}
	unauthRes.Body.Close()
	if unauthRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauth, got %d", unauthRes.StatusCode)
	}

	adminClient := loginTestAdmin(t, srv, ts.URL)

	// GET /api/admin/update returns current version & default repo.
	stRes, err := adminClient.Get(ts.URL + "/api/admin/update")
	if err != nil {
		t.Fatalf("GET /api/admin/update: %v", err)
	}
	var stOut struct {
		Status updater.Status `json:"status"`
	}
	_ = json.NewDecoder(stRes.Body).Decode(&stOut)
	stRes.Body.Close()
	if stRes.StatusCode != http.StatusOK || stOut.Status.CurrentVersion != version.Version || stOut.Status.Repo != version.DefaultRepo {
		t.Fatalf("unexpected update status: status=%d out=%+v", stRes.StatusCode, stOut)
	}

	// POST /api/admin/update/config updates repo, proxy, and auto_check.
	cfgRes, err := adminClient.Post(
		ts.URL+"/api/admin/update/config",
		"application/json",
		strings.NewReader(`{"repo":"https://github.com/custom/LFH","proxy_url":"https://ghfast.top/","auto_check":false}`),
	)
	if err != nil {
		t.Fatalf("POST /api/admin/update/config: %v", err)
	}
	var cfgOut struct {
		Status updater.Status `json:"status"`
	}
	_ = json.NewDecoder(cfgRes.Body).Decode(&cfgOut)
	cfgRes.Body.Close()
	if cfgRes.StatusCode != http.StatusOK || cfgOut.Status.Repo != "custom/LFH" || cfgOut.Status.ProxyURL != "https://ghfast.top" || cfgOut.Status.AutoCheck {
		t.Fatalf("unexpected update config response: status=%d out=%+v", cfgRes.StatusCode, cfgOut)
	}
}

func TestFileMetaAndNonAdminAutoTarget(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	// Enable open registration so a regular user can be created/approved or use an invite.
	invRes, err := adminClient.Post(ts.URL+"/api/admin/invites", "application/json", strings.NewReader(`{"max_uses":2,"expire_hours":24}`))
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	var invOut struct {
		Invite auth.InviteCode `json:"invite"`
	}
	_ = json.NewDecoder(invRes.Body).Decode(&invOut)
	invRes.Body.Close()

	userClient := newCookieClient(t)
	regBody, _ := json.Marshal(map[string]string{
		"username":    "normal_u",
		"password":    "pass1234",
		"invite_code": invOut.Invite.Code,
	})
	regRes, err := userClient.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	regRes.Body.Close()
	if regRes.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 register, got %d", regRes.StatusCode)
	}

	// Regular user passes an invalid target_node, server must force "auto" and succeed.
	payload := []byte("# Detailed Props Test\nHello LFH metadata endpoint.")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("target_node", "non-existent-node-id")
	_ = mw.WriteField("relative_paths", "Docs/Sub/props.md")
	fw, _ := mw.CreateFormFile("files", "props.md")
	_, _ = fw.Write(payload)
	_ = mw.Close()

	upRes, err := userClient.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("non-admin upload: %v", err)
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated || len(upOut.Uploaded) != 1 {
		t.Fatalf("expected non-admin upload forced to auto to succeed, got status=%d out=%+v", upRes.StatusCode, upOut)
	}
	fileID := upOut.Uploaded[0].ID

	// GET /api/files/{id}/meta returns exact size, content_type, sha256, and can_preview.
	metaRes, err := userClient.Get(ts.URL + "/api/files/" + fileID + "/meta")
	if err != nil {
		t.Fatalf("GET /api/files/{id}/meta: %v", err)
	}
	var metaOut struct {
		File          storage.FileRecord `json:"file"`
		ContentType   string             `json:"content_type"`
		SHA256        string             `json:"sha256"`
		SHA256Skipped bool               `json:"sha256_skipped"`
		CanPreview    bool               `json:"can_preview"`
		IsArchive     bool               `json:"is_archive"`
	}
	_ = json.NewDecoder(metaRes.Body).Decode(&metaOut)
	metaRes.Body.Close()

	if metaRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 meta, got %d", metaRes.StatusCode)
	}
	if metaOut.File.Size != int64(len(payload)) || metaOut.File.FolderPath != "Docs/Sub" || !metaOut.CanPreview || metaOut.IsArchive {
		t.Fatalf("unexpected meta output: %+v", metaOut)
	}
	if metaOut.SHA256Skipped || len(metaOut.SHA256) != 64 {
		t.Fatalf("expected full 64-hex sha256, got skipped=%v sha=%q", metaOut.SHA256Skipped, metaOut.SHA256)
	}
	if !strings.Contains(metaOut.ContentType, "text/markdown") {
		t.Fatalf("expected markdown content_type, got %q", metaOut.ContentType)
	}
}
