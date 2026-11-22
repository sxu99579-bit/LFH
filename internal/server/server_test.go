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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/clipboard"
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

func registerAndLoginTestUser(t *testing.T, adminClient *http.Client, baseURL, username, password string) *http.Client {
	t.Helper()
	invRes, err := adminClient.Post(baseURL+"/api/admin/invites", "application/json", strings.NewReader(`{"max_uses":1,"expire_hours":24}`))
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	var invOut struct {
		Invite auth.InviteCode `json:"invite"`
	}
	_ = json.NewDecoder(invRes.Body).Decode(&invOut)
	invRes.Body.Close()

	client := newCookieClient(t)
	regBody, _ := json.Marshal(map[string]string{
		"username":    username,
		"password":    password,
		"invite_code": invOut.Invite.Code,
	})
	regRes, err := client.Post(baseURL+"/api/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	regRes.Body.Close()
	if regRes.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 register %s, got %d", username, regRes.StatusCode)
	}
	return client
}

func TestAdminGrantAndRevokeRole(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)
	aliceClient := registerAndLoginTestUser(t, adminClient, ts.URL, "alice", "pass1234")

	// Initially alice is non-admin: /api/admin/users returns 403.
	uRes0, _ := aliceClient.Get(ts.URL + "/api/admin/users")
	uRes0.Body.Close()
	if uRes0.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d", uRes0.StatusCode)
	}

	// Admin promotes alice -> 200 and alice immediately gets admin access.
	promRes, err := adminClient.Post(ts.URL+"/api/admin/users/alice/role", "application/json", strings.NewReader(`{"role":"admin"}`))
	if err != nil {
		t.Fatalf("promote alice: %v", err)
	}
	promRes.Body.Close()
	if promRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 promote, got %d", promRes.StatusCode)
	}

	uRes1, _ := aliceClient.Get(ts.URL + "/api/admin/users")
	uRes1.Body.Close()
	if uRes1.StatusCode != http.StatusOK {
		t.Fatalf("expected promoted alice to get 200 on /api/admin/users, got %d", uRes1.StatusCode)
	}

	// Selectable users lists active users cleanly.
	selRes, err := aliceClient.Get(ts.URL + "/api/users/selectable")
	if err != nil {
		t.Fatalf("GET /api/users/selectable: %v", err)
	}
	var selOut struct {
		Users []auth.PublicUser `json:"users"`
	}
	_ = json.NewDecoder(selRes.Body).Decode(&selOut)
	selRes.Body.Close()
	if selRes.StatusCode != http.StatusOK || len(selOut.Users) < 2 {
		t.Fatalf("unexpected selectable users: status=%d out=%+v", selRes.StatusCode, selOut)
	}

	// Admin revokes alice's admin role -> 200 and alice gets 403 again.
	demRes, err := adminClient.Post(ts.URL+"/api/admin/users/alice/role", "application/json", strings.NewReader(`{"role":"user"}`))
	if err != nil {
		t.Fatalf("demote alice: %v", err)
	}
	demRes.Body.Close()
	if demRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 demote, got %d", demRes.StatusCode)
	}

	uRes2, _ := aliceClient.Get(ts.URL + "/api/admin/users")
	uRes2.Body.Close()
	if uRes2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected demoted alice to get 403 on /api/admin/users, got %d", uRes2.StatusCode)
	}
}

func TestFileVisibilityAndDeletePermissions(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)
	aliceClient := registerAndLoginTestUser(t, adminClient, ts.URL, "alice", "pass1234")
	bobClient := registerAndLoginTestUser(t, adminClient, ts.URL, "bob", "pass1234")
	carolClient := registerAndLoginTestUser(t, adminClient, ts.URL, "carol", "pass1234")

	// Alice uploads a file visible only to ["bob"].
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("allowed_users", `["bob"]`)
	fw, _ := mw.CreateFormFile("files", "secret-for-bob.txt")
	_, _ = io.WriteString(fw, "confidential-bob-only")
	_ = mw.Close()

	upRes, err := aliceClient.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("alice upload: %v", err)
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated || len(upOut.Uploaded) != 1 {
		t.Fatalf("expected 201 upload, got %d out=%+v", upRes.StatusCode, upOut)
	}
	if upOut.Uploaded[0].StorageDir != "" {
		t.Fatalf("expected non-admin upload response to omit storage_dir, got %q", upOut.Uploaded[0].StorageDir)
	}
	fileID := upOut.Uploaded[0].ID

	// Bob sees it in /api/files and /api/summary, can download & preview, CANNOT delete (403) or change access (403), and does NOT see storage paths.
	bobFilesRes, _ := bobClient.Get(ts.URL + "/api/files")
	var bobFiles struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(bobFilesRes.Body).Decode(&bobFiles)
	bobFilesRes.Body.Close()
	if len(bobFiles.Files) != 1 || bobFiles.Files[0].ID != fileID || bobFiles.Files[0].StorageDir != "" {
		t.Fatalf("unexpected bob /api/files: %+v", bobFiles.Files)
	}

	bobDlRes, _ := bobClient.Get(ts.URL + "/api/files/" + fileID + "/download")
	bobDlBytes, _ := io.ReadAll(bobDlRes.Body)
	bobDlRes.Body.Close()
	if bobDlRes.StatusCode != http.StatusOK || string(bobDlBytes) != "confidential-bob-only" {
		t.Fatalf("bob download failed: status=%d body=%q", bobDlRes.StatusCode, string(bobDlBytes))
	}

	bobMetaRes, _ := bobClient.Get(ts.URL + "/api/files/" + fileID + "/meta")
	var bobMeta map[string]any
	_ = json.NewDecoder(bobMetaRes.Body).Decode(&bobMeta)
	bobMetaRes.Body.Close()
	if bobMetaRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 bob meta, got %d", bobMetaRes.StatusCode)
	}
	if _, hasDisk := bobMeta["disk_path"]; hasDisk {
		t.Fatalf("non-admin meta must NOT include disk_path: %+v", bobMeta)
	}

	bobDelReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/files/"+fileID, nil)
	bobDelRes, _ := bobClient.Do(bobDelReq)
	bobDelRes.Body.Close()
	if bobDelRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when non-uploader bob deletes file, got %d", bobDelRes.StatusCode)
	}

	bobAclRes, _ := bobClient.Post(ts.URL+"/api/files/"+fileID+"/access", "application/json", strings.NewReader(`{"allowed_users":[]}`))
	bobAclRes.Body.Close()
	if bobAclRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when non-uploader bob edits access, got %d", bobAclRes.StatusCode)
	}

	// Non-admin /api/cluster must hide storage_dir and drives.
	bobClRes, _ := bobClient.Get(ts.URL + "/api/cluster")
	var bobCl struct {
		Pool cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(bobClRes.Body).Decode(&bobCl)
	bobClRes.Body.Close()
	if len(bobCl.Pool.Nodes) == 0 || bobCl.Pool.Nodes[0].StorageDir != "" || len(bobCl.Pool.Nodes[0].Drives) != 0 {
		t.Fatalf("non-admin /api/cluster must hide storage_dir & drives: %+v", bobCl.Pool.Nodes)
	}

	// Carol is NOT authorized: hidden in /api/files & /api/summary, and gets 404 everywhere.
	carolFilesRes, _ := carolClient.Get(ts.URL + "/api/files")
	var carolFiles struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(carolFilesRes.Body).Decode(&carolFiles)
	carolFilesRes.Body.Close()
	if len(carolFiles.Files) != 0 {
		t.Fatalf("expected carol to see 0 hidden files, got %+v", carolFiles.Files)
	}

	carolSumRes, _ := carolClient.Get(ts.URL + "/api/summary")
	var carolSum storage.SummaryResponse
	_ = json.NewDecoder(carolSumRes.Body).Decode(&carolSum)
	carolSumRes.Body.Close()
	if carolSum.TotalCount != 0 {
		t.Fatalf("expected carol summary count=0, got %+v", carolSum)
	}

	for _, endpoint := range []string{
		"/api/files/" + fileID + "/download",
		"/api/files/" + fileID + "/raw",
		"/api/files/" + fileID + "/preview",
		"/api/files/" + fileID + "/meta",
		"/api/files/" + fileID + "/archive-entry?path=a",
	} {
		r404, _ := carolClient.Get(ts.URL + endpoint)
		r404.Body.Close()
		if r404.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 for unauthorized carol on %s, got %d", endpoint, r404.StatusCode)
		}
	}

	// Admin CAN see the file AND gets physical disk_path in /meta AND can inspect storage_dir in /api/cluster.
	admMetaRes, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/meta")
	var admMeta map[string]any
	_ = json.NewDecoder(admMetaRes.Body).Decode(&admMeta)
	admMetaRes.Body.Close()
	if admMetaRes.StatusCode != http.StatusOK || admMeta["disk_path"] == "" || admMeta["storage_dir"] == "" {
		t.Fatalf("expected admin meta to include disk_path & storage_dir, got status=%d out=%+v", admMetaRes.StatusCode, admMeta)
	}

	// Uploader Alice grants Carol access too -> Carol now sees it.
	grantRes, _ := aliceClient.Post(ts.URL+"/api/files/"+fileID+"/access", "application/json", strings.NewReader(`{"allowed_users":["bob","carol"]}`))
	grantRes.Body.Close()
	if grantRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when uploader updates access, got %d", grantRes.StatusCode)
	}
	carolAfterRes, _ := carolClient.Get(ts.URL + "/api/files")
	var carolAfter struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(carolAfterRes.Body).Decode(&carolAfter)
	carolAfterRes.Body.Close()
	if len(carolAfter.Files) != 1 || carolAfter.Files[0].ID != fileID {
		t.Fatalf("expected carol to see file after grant, got %+v", carolAfter.Files)
	}

	// Admin deletes file -> succeeds (200).
	admDelReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/files/"+fileID, nil)
	admDelRes, _ := adminClient.Do(admDelReq)
	admDelRes.Body.Close()
	if admDelRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when admin deletes file, got %d", admDelRes.StatusCode)
	}
}

func TestAdminStorageDirSwitchAndMigration(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)
	userClient := registerAndLoginTestUser(t, adminClient, ts.URL, "worker_u", "pass1234")

	// Upload one file first.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "move-me.txt")
	_, _ = io.WriteString(fw, "migrated-payload")
	_ = mw.Close()
	upRes, _ := adminClient.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &buf)
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	fileID := upOut.Uploaded[0].ID

	newDriveDir := filepath.Join(t.TempDir(), "D-LFH-Storage")
	bodyBytes, _ := json.Marshal(map[string]any{
		"node_id":          srv.cluster.NodeID(),
		"storage_dir":      newDriveDir,
		"migrate_existing": true,
	})

	// Non-admin gets 403.
	forbRes, _ := userClient.Post(ts.URL+"/api/admin/cluster/storage-dir", "application/json", bytes.NewReader(bodyBytes))
	forbRes.Body.Close()
	if forbRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin storage-dir change, got %d", forbRes.StatusCode)
	}

	// Admin switches storage dir and migrates existing file.
	swRes, err := adminClient.Post(ts.URL+"/api/admin/cluster/storage-dir", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /api/admin/cluster/storage-dir: %v", err)
	}
	var swOut struct {
		Node       cluster.NodeStatus  `json:"node"`
		MovedCount int                 `json:"moved_count"`
		Pool       cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(swRes.Body).Decode(&swOut)
	swRes.Body.Close()
	if swRes.StatusCode != http.StatusOK || swOut.MovedCount != 1 || swOut.Node.StorageDir != newDriveDir {
		t.Fatalf("unexpected storage-dir response: status=%d out=%+v", swRes.StatusCode, swOut)
	}

	// Verify /meta reports new physical disk_path inside newDriveDir and download still serves exact payload.
	metaRes, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/meta")
	var metaOut struct {
		DiskPath   string `json:"disk_path"`
		StorageDir string `json:"storage_dir"`
	}
	_ = json.NewDecoder(metaRes.Body).Decode(&metaOut)
	metaRes.Body.Close()
	if metaOut.StorageDir != newDriveDir || filepath.Dir(metaOut.DiskPath) != newDriveDir {
		t.Fatalf("expected migrated disk_path under %q, got %+v", newDriveDir, metaOut)
	}
	if _, err := os.Stat(metaOut.DiskPath); err != nil {
		t.Fatalf("migrated file missing on disk at %q: %v", metaOut.DiskPath, err)
	}

	dlRes, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/download")
	dlBytes, _ := io.ReadAll(dlRes.Body)
	dlRes.Body.Close()
	if dlRes.StatusCode != http.StatusOK || string(dlBytes) != "migrated-payload" {
		t.Fatalf("download after migration failed: status=%d body=%q", dlRes.StatusCode, string(dlBytes))
	}
}

func TestOfflinePeerFilesHiddenAndWebAdoptJoin(t *testing.T) {
	// Entry A and Entry B start as standalone Entry servers with distinct secrets.
	entrySrvA, entryMgrA, _, entryTSA := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Node-A",
		QuotaGB:  10,
		QuotaSet: true,
	})
	entrySrvB, entryMgrB, storeB, entryTSB := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Node-B",
		QuotaGB:  20,
		QuotaSet: true,
	})
	// Keep one file on Node-B before Node-B joins Node-A.
	if _, err := storeB.SaveUpload("node-b-file.txt", "admin_b", storage.DeviceInfo{Name: "PC-B"}, strings.NewReader("from-node-b")); err != nil {
		t.Fatalf("SaveUpload on B: %v", err)
	}

	adminClientA := loginTestAdmin(t, entrySrvA, entryTSA.URL)
	userB, passB, _ := entrySrvB.auth.InitialAdminCredentials()

	// 1. Manual probe discovers Node-B cleanly.
	probeBody, _ := json.Marshal(map[string]any{
		"mode":    "manual",
		"address": entryTSB.URL,
	})
	probeRes, err := adminClientA.Post(entryTSA.URL+"/api/admin/cluster/discover", "application/json", bytes.NewReader(probeBody))
	if err != nil {
		t.Fatalf("POST discover manual: %v", err)
	}
	var probeOut struct {
		Node cluster.DiscoveredNode `json:"node"`
	}
	_ = json.NewDecoder(probeRes.Body).Decode(&probeOut)
	probeRes.Body.Close()
	if probeRes.StatusCode != http.StatusOK || probeOut.Node.NodeID != entryMgrB.NodeID() || probeOut.Node.AlreadyInPool {
		t.Fatalf("unexpected probe result: status=%d out=%+v", probeRes.StatusCode, probeOut)
	}

	// 2. Adopt with wrong credentials fails and keeps Node-B standalone.
	badAdopt, _ := json.Marshal(map[string]any{
		"address":           entryTSB.URL,
		"target_admin_user": userB,
		"target_admin_pass": "wrong-pass",
	})
	badRes, _ := adminClientA.Post(entryTSA.URL+"/api/admin/cluster/adopt-peer", "application/json", bytes.NewReader(badAdopt))
	badRes.Body.Close()
	if badRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 adopt with wrong password, got %d", badRes.StatusCode)
	}
	if entryMgrB.Role() != cluster.RoleEntry {
		t.Fatalf("expected Node-B to remain entry on failed adopt")
	}

	// 3. Adopt with Node-B admin credentials succeeds -> Node-B joins live and its file shows on Node-A.
	goodAdopt, _ := json.Marshal(map[string]any{
		"address":           entryTSB.URL,
		"target_admin_user": userB,
		"target_admin_pass": passB,
		"quota_text":        "25GB",
	})
	goodRes, err := adminClientA.Post(entryTSA.URL+"/api/admin/cluster/adopt-peer", "application/json", bytes.NewReader(goodAdopt))
	if err != nil {
		t.Fatalf("adopt-peer: %v", err)
	}
	goodRes.Body.Close()
	if goodRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 adopt-peer, got %d", goodRes.StatusCode)
	}
	if entryMgrB.Role() != cluster.RoleWorker {
		t.Fatalf("expected Node-B switched to worker, got %s", entryMgrB.Role())
	}

	filesRes1, _ := adminClientA.Get(entryTSA.URL + "/api/files")
	var fOut1 struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(filesRes1.Body).Decode(&fOut1)
	filesRes1.Body.Close()
	if len(fOut1.Files) != 1 || fOut1.Files[0].Name != "node-b-file.txt" {
		t.Fatalf("expected Node-B file visible on Node-A while online, got %+v", fOut1.Files)
	}

	// 4. Mark Node-B offline -> Node-B still appears in /api/cluster (online=false), but its file is hidden!
	entryMgrA.SetPeerLastSeenForTest(entryMgrB.NodeID(), time.Now().Add(-30*time.Second))

	clResOff, _ := adminClientA.Get(entryTSA.URL + "/api/cluster")
	var clOff struct {
		Pool cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(clResOff.Body).Decode(&clOff)
	clResOff.Body.Close()
	if len(clOff.Pool.Nodes) != 2 || clOff.Pool.Nodes[1].Online {
		t.Fatalf("expected Node-B listed offline in pool, got %+v", clOff.Pool.Nodes)
	}

	filesResOff, _ := adminClientA.Get(entryTSA.URL + "/api/files")
	var fOutOff struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(filesResOff.Body).Decode(&fOutOff)
	filesResOff.Body.Close()
	if len(fOutOff.Files) != 0 {
		t.Fatalf("expected Node-B files hidden while offline, got %+v", fOutOff.Files)
	}

	sumResOff, _ := adminClientA.Get(entryTSA.URL + "/api/summary")
	var sumOff storage.SummaryResponse
	_ = json.NewDecoder(sumResOff.Body).Decode(&sumOff)
	sumResOff.Body.Close()
	if sumOff.TotalCount != 0 {
		t.Fatalf("expected summary TotalCount=0 while Node-B offline, got %+v", sumOff)
	}

	// 5. Node-B sends heartbeat -> file reappears automatically!
	if err := entryMgrB.SendHeartbeatNow(); err != nil {
		t.Fatalf("SendHeartbeatNow recovery: %v", err)
	}
	filesResBack, _ := adminClientA.Get(entryTSA.URL + "/api/files")
	var fOutBack struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(filesResBack.Body).Decode(&fOutBack)
	filesResBack.Body.Close()
	if len(fOutBack.Files) != 1 || fOutBack.Files[0].Name != "node-b-file.txt" {
		t.Fatalf("expected Node-B file visible again once back online, got %+v", fOutBack.Files)
	}

	// 6. Test Push join from standalone Node-C -> wrong secret fails without changing role; valid secret joins cleanly.
	entrySrvC, entryMgrC, _, entryTSC := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Node-C",
		QuotaGB:  10,
		QuotaSet: true,
	})
	adminClientC := loginTestAdmin(t, entrySrvC, entryTSC.URL)

	badJoin, _ := json.Marshal(map[string]any{
		"entry_url":      entryTSA.URL,
		"cluster_secret": "wrong-secret",
	})
	badJoinRes, _ := adminClientC.Post(entryTSC.URL+"/api/admin/cluster/join", "application/json", bytes.NewReader(badJoin))
	badJoinRes.Body.Close()
	if badJoinRes.StatusCode != http.StatusBadRequest || entryMgrC.Role() != cluster.RoleEntry {
		t.Fatalf("expected bad join to fail 400 without mutating C, status=%d role=%s", badJoinRes.StatusCode, entryMgrC.Role())
	}

	goodJoin, _ := json.Marshal(map[string]any{
		"entry_url":      entryTSA.URL,
		"cluster_secret": entryMgrA.Secret(),
		"quota_text":     "15GB",
	})
	goodJoinRes, _ := adminClientC.Post(entryTSC.URL+"/api/admin/cluster/join", "application/json", bytes.NewReader(goodJoin))
	goodJoinRes.Body.Close()
	if goodJoinRes.StatusCode != http.StatusOK || entryMgrC.Role() != cluster.RoleWorker {
		t.Fatalf("expected valid join 200 and C->worker, status=%d role=%s", goodJoinRes.StatusCode, entryMgrC.Role())
	}
}

func TestPrimarySwitchEvictOnlineNodeAndWorkerLeaveSelf(t *testing.T) {
	srvA, mgrA, storeA, tsA := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Primary-A",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_, mgrB, storeB, tsB := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Candidate-B",
		QuotaGB:  20,
		QuotaSet: true,
	})
	_, mgrC, storeC, tsC := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Worker-C",
		QuotaGB:  15,
		QuotaSet: true,
	})

	if err := srvA.auth.SetInviteRequired(false); err != nil {
		t.Fatalf("SetInviteRequired: %v", err)
	}
	if _, err := srvA.auth.Register("alice", "alicepass1", ""); err != nil {
		t.Fatalf("Register alice: %v", err)
	}
	if _, err := srvA.auth.ApproveUser("alice"); err != nil {
		t.Fatalf("ApproveUser alice: %v", err)
	}

	if _, err := storeA.SaveUpload("node-a.txt", "alice", storage.DeviceInfo{Name: "PC-A"}, strings.NewReader("payload-a")); err != nil {
		t.Fatalf("SaveUpload A: %v", err)
	}
	if _, err := storeB.SaveUpload("node-b.txt", "alice", storage.DeviceInfo{Name: "PC-B"}, strings.NewReader("payload-b")); err != nil {
		t.Fatalf("SaveUpload B: %v", err)
	}
	if _, err := storeC.SaveUpload("node-c.txt", "alice", storage.DeviceInfo{Name: "PC-C"}, strings.NewReader("payload-c")); err != nil {
		t.Fatalf("SaveUpload C: %v", err)
	}

	if err := mgrB.JoinEntryCluster(tsA.URL, mgrA.Secret(), 0); err != nil {
		t.Fatalf("B JoinEntryCluster: %v", err)
	}
	if err := mgrB.SendHeartbeatNow(); err != nil {
		t.Fatalf("B SendHeartbeatNow: %v", err)
	}
	if err := mgrC.JoinEntryCluster(tsA.URL, mgrA.Secret(), 0); err != nil {
		t.Fatalf("C JoinEntryCluster: %v", err)
	}
	if err := mgrC.SendHeartbeatNow(); err != nil {
		t.Fatalf("C SendHeartbeatNow: %v", err)
	}

	adminClientA := loginTestAdmin(t, srvA, tsA.URL)

	// 1. Promote B to become new cluster Entry.
	promoteReq, _ := json.Marshal(map[string]string{"target_node_id": mgrB.NodeID()})
	promoteRes, err := adminClientA.Post(tsA.URL+"/api/admin/cluster/promote", "application/json", bytes.NewReader(promoteReq))
	if err != nil {
		t.Fatalf("POST /api/admin/cluster/promote: %v", err)
	}
	var promoteOut struct {
		OK          bool   `json:"ok"`
		NewEntryURL string `json:"new_entry_url"`
	}
	_ = json.NewDecoder(promoteRes.Body).Decode(&promoteOut)
	promoteRes.Body.Close()
	if promoteRes.StatusCode != http.StatusOK || !promoteOut.OK || promoteOut.NewEntryURL != tsB.URL {
		t.Fatalf("unexpected promote response: status=%d out=%+v", promoteRes.StatusCode, promoteOut)
	}

	if mgrB.Role() != cluster.RoleEntry || mgrA.Role() != cluster.RoleWorker || mgrC.Role() != cluster.RoleWorker {
		t.Fatalf("unexpected roles after promote: A=%s B=%s C=%s", mgrA.Role(), mgrB.Role(), mgrC.Role())
	}
	if mgrA.EntryURL() != tsB.URL || mgrC.EntryURL() != tsB.URL {
		t.Fatalf("expected workers repointed to %s, got A=%s C=%s", tsB.URL, mgrA.EntryURL(), mgrC.EntryURL())
	}

	// 2. Non-admin user alice created on A can now log in on promoted Entry B and see all 3 files.
	aliceLoginBody, _ := json.Marshal(map[string]string{"username": "alice", "password": "alicepass1"})
	aliceLoginRes, err := tsB.Client().Post(tsB.URL+"/api/login", "application/json", bytes.NewReader(aliceLoginBody))
	if err != nil {
		t.Fatalf("alice login on B: %v", err)
	}
	aliceCookies := aliceLoginRes.Cookies()
	aliceLoginRes.Body.Close()
	if aliceLoginRes.StatusCode != http.StatusOK || len(aliceCookies) == 0 {
		t.Fatalf("expected alice login 200 with cookie on promoted B, got status=%d cookies=%d", aliceLoginRes.StatusCode, len(aliceCookies))
	}

	filesReq, _ := http.NewRequest(http.MethodGet, tsB.URL+"/api/files", nil)
	for _, c := range aliceCookies {
		filesReq.AddCookie(c)
	}
	filesRes, err := tsB.Client().Do(filesReq)
	if err != nil {
		t.Fatalf("GET /api/files on B: %v", err)
	}
	var filesOut struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(filesRes.Body).Decode(&filesOut)
	filesRes.Body.Close()
	if filesRes.StatusCode != http.StatusOK || len(filesOut.Files) != 3 {
		t.Fatalf("expected all 3 cluster files visible on promoted B, status=%d files=%+v", filesRes.StatusCode, filesOut.Files)
	}

	// 3. Old Entry A redirects "/" -> B, while "/worker" remains accessible directly.
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	rootResA, err := noRedirect.Get(tsA.URL + "/")
	if err != nil {
		t.Fatalf("GET / on old Entry A: %v", err)
	}
	rootResA.Body.Close()
	if rootResA.StatusCode != http.StatusFound || rootResA.Header.Get("Location") != tsB.URL {
		t.Fatalf("expected 302 -> %s on A, got %d loc=%s", tsB.URL, rootResA.StatusCode, rootResA.Header.Get("Location"))
	}

	workerPageRes, err := noRedirect.Get(tsA.URL + "/worker")
	if err != nil {
		t.Fatalf("GET /worker on A: %v", err)
	}
	workerBody, _ := io.ReadAll(workerPageRes.Body)
	workerPageRes.Body.Close()
	if workerPageRes.StatusCode != http.StatusOK || !strings.Contains(string(workerBody), "LFH 本机节点控制台") {
		t.Fatalf("expected /worker 200 HTML, got status=%d", workerPageRes.StatusCode)
	}

	// 4. Evict online Worker-C from promoted Entry B (must log in with migrated root admin creds on B).
	rootAdminUser, rootAdminPass, _ := srvA.auth.InitialAdminCredentials()
	bAdminLoginBody, _ := json.Marshal(map[string]string{"username": rootAdminUser, "password": rootAdminPass})
	bAdminLoginRes, err := tsB.Client().Post(tsB.URL+"/api/login", "application/json", bytes.NewReader(bAdminLoginBody))
	if err != nil {
		t.Fatalf("root admin login on B: %v", err)
	}
	bAdminCookies := bAdminLoginRes.Cookies()
	bAdminLoginRes.Body.Close()
	if bAdminLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("expected migrated root admin login 200 on B, got %d", bAdminLoginRes.StatusCode)
	}

	evictReq, _ := http.NewRequest(http.MethodDelete, tsB.URL+"/api/admin/cluster/nodes/"+mgrC.NodeID(), nil)
	for _, c := range bAdminCookies {
		evictReq.AddCookie(c)
	}
	evictRes, err := tsB.Client().Do(evictReq)
	if err != nil {
		t.Fatalf("DELETE node C: %v", err)
	}
	var evictOut struct {
		OK       bool                `json:"ok"`
		Notified bool                `json:"notified"`
		Pool     cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(evictRes.Body).Decode(&evictOut)
	evictRes.Body.Close()
	if evictRes.StatusCode != http.StatusOK || !evictOut.OK || !evictOut.Notified || len(evictOut.Pool.Nodes) != 2 {
		t.Fatalf("unexpected evict response: status=%d out=%+v", evictRes.StatusCode, evictOut)
	}
	if mgrC.Role() != cluster.RoleEntry || mgrC.Secret() == mgrB.Secret() {
		t.Fatalf("expected C returned to standalone entry with rotated secret, role=%s", mgrC.Role())
	}
	if len(storeC.List("", "", "", "", "")) != 1 {
		t.Fatalf("expected C local files preserved after eviction")
	}

	// 5. Worker A self-leaves via /api/cluster/leave-self.
	badLeave, _ := json.Marshal(map[string]string{"cluster_secret": "wrong-secret"})
	badLeaveRes, _ := tsA.Client().Post(tsA.URL+"/api/cluster/leave-self", "application/json", bytes.NewReader(badLeave))
	badLeaveRes.Body.Close()
	if badLeaveRes.StatusCode != http.StatusUnauthorized || mgrA.Role() != cluster.RoleWorker {
		t.Fatalf("expected 401 on wrong leave-self secret, got status=%d role=%s", badLeaveRes.StatusCode, mgrA.Role())
	}

	goodLeave, _ := json.Marshal(map[string]string{"cluster_secret": mgrA.Secret()})
	goodLeaveRes, err := tsA.Client().Post(tsA.URL+"/api/cluster/leave-self", "application/json", bytes.NewReader(goodLeave))
	if err != nil {
		t.Fatalf("POST leave-self on A: %v", err)
	}
	goodLeaveRes.Body.Close()
	if goodLeaveRes.StatusCode != http.StatusOK || mgrA.Role() != cluster.RoleEntry || mgrA.Secret() == mgrB.Secret() {
		t.Fatalf("expected A detached to standalone entry with rotated secret, status=%d role=%s", goodLeaveRes.StatusCode, mgrA.Role())
	}
	rootAfterLeaveA, _ := noRedirect.Get(tsA.URL + "/")
	rootAfterLeaveA.Body.Close()
	if rootAfterLeaveA.StatusCode != http.StatusOK {
		t.Fatalf("expected GET / 200 on detached A, got %d", rootAfterLeaveA.StatusCode)
	}

	// 6. Rejoin C and dissolve entire cluster from Entry B.
	oldSecretB := mgrB.Secret()
	if err := mgrC.JoinEntryCluster(tsB.URL, oldSecretB, 0); err != nil {
		t.Fatalf("rejoin C -> B: %v", err)
	}
	if err := mgrC.SendHeartbeatNow(); err != nil {
		t.Fatalf("C heartbeat to B: %v", err)
	}
	dissolveReq, _ := http.NewRequest(http.MethodPost, tsB.URL+"/api/admin/cluster/leave", nil)
	for _, c := range bAdminCookies {
		dissolveReq.AddCookie(c)
	}
	dissolveRes, err := tsB.Client().Do(dissolveReq)
	if err != nil {
		t.Fatalf("POST /api/admin/cluster/leave: %v", err)
	}
	var dissolveOut struct {
		OK            bool                `json:"ok"`
		Detached      int                 `json:"detached"`
		ClusterSecret string              `json:"cluster_secret"`
		Pool          cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(dissolveRes.Body).Decode(&dissolveOut)
	dissolveRes.Body.Close()
	if dissolveRes.StatusCode != http.StatusOK || !dissolveOut.OK || dissolveOut.Detached != 1 || mgrB.Secret() == oldSecretB || len(dissolveOut.Pool.Nodes) != 1 {
		t.Fatalf("unexpected dissolve output: status=%d out=%+v", dissolveRes.StatusCode, dissolveOut)
	}
	if mgrC.Role() != cluster.RoleEntry || mgrC.Secret() == oldSecretB {
		t.Fatalf("expected C detached after dissolve, role=%s", mgrC.Role())
	}
	_ = tsC
}

func TestFileVersionControlAndNodeHealthTelemetryAPI(t *testing.T) {
	srv, _, _, ts := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Telemetry-Entry",
		QuotaGB:  10,
		QuotaSet: true,
	})
	adminClient := loginTestAdmin(t, srv, ts.URL)

	uploadFile := func(url, fieldName, filename, note, content string) (*http.Response, []byte) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if note != "" {
			_ = mw.WriteField("note", note)
		}
		fw, err := mw.CreateFormFile(fieldName, filename)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = fw.Write([]byte(content))
		_ = mw.Close()

		res, err := adminClient.Post(url, mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatalf("POST %s: %v", url, err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, raw
	}

	// 1. Upload v1 of spec.txt
	res1, raw1 := uploadFile(ts.URL+"/api/upload", "files", "spec.txt", "", "spec-v1")
	if res1.StatusCode != http.StatusCreated {
		t.Fatalf("upload v1 failed: %d %s", res1.StatusCode, string(raw1))
	}
	var up1 struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.Unmarshal(raw1, &up1)
	if len(up1.Uploaded) != 1 || up1.Uploaded[0].Version != 1 || up1.Uploaded[0].UpdateMethod != storage.MethodInitialUpload {
		t.Fatalf("expected v1 uploaded with MethodInitialUpload: %+v", up1)
	}
	fileID := up1.Uploaded[0].ID

	// 2. Re-uploading spec.txt creates v2 automatically on the same record ID with MethodUploadOverwrite
	res2, raw2 := uploadFile(ts.URL+"/api/upload", "files", "spec.txt", "", "spec-v2")
	if res2.StatusCode != http.StatusCreated {
		t.Fatalf("upload v2 failed: %d %s", res2.StatusCode, string(raw2))
	}
	var up2 struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.Unmarshal(raw2, &up2)
	if len(up2.Uploaded) != 1 || up2.Uploaded[0].ID != fileID || up2.Uploaded[0].Version != 2 || up2.Uploaded[0].VersionCount != 2 || up2.Uploaded[0].UpdateMethod != storage.MethodUploadOverwrite {
		t.Fatalf("expected automatic v2 on same fileID with MethodUploadOverwrite: %+v", up2)
	}

	// 3. Explicit POST /api/files/{id}/versions creates v3 with note and MethodVersionUpload
	res3, raw3 := uploadFile(ts.URL+"/api/files/"+fileID+"/versions", "file", "spec.txt", "正式版定稿", "spec-v3")
	if res3.StatusCode != http.StatusCreated {
		t.Fatalf("upload v3 failed: %d %s", res3.StatusCode, string(raw3))
	}

	// 3b. POST /api/files/{id}/edit creates v4 with MethodOnlineEdit
	editBody, _ := json.Marshal(map[string]string{
		"content": "spec-v4-edited-online",
		"note":    "在线编辑修订",
	})
	editReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/files/"+fileID+"/edit", bytes.NewReader(editBody))
	editReq.Header.Set("Content-Type", "application/json")
	editReq.Header.Set("X-Device-Name", "Win11-Workstation")
	editReq.Header.Set("X-Device-FP", "FP-EDIT123456")
	editRes, err := adminClient.Do(editReq)
	if err != nil {
		t.Fatalf("POST /api/files/%s/edit: %v", fileID, err)
	}
	var editOut struct {
		OK   bool               `json:"ok"`
		File storage.FileRecord `json:"file"`
	}
	_ = json.NewDecoder(editRes.Body).Decode(&editOut)
	editRes.Body.Close()
	if editRes.StatusCode != http.StatusOK || !editOut.OK || editOut.File.Version != 4 || editOut.File.UpdateMethod != storage.MethodOnlineEdit || editOut.File.DeviceName != "Win11-Workstation" {
		t.Fatalf("unexpected edit response: status=%d out=%+v", editRes.StatusCode, editOut)
	}

	// 4. GET /api/files/{id}/versions lists all 4 versions with update_method, uploader, device_name, uploaded_at
	vListRes, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/versions")
	var vListOut struct {
		CurrentVersion int                   `json:"current_version"`
		VersionCount   int                   `json:"version_count"`
		Versions       []storage.FileVersion `json:"versions"`
	}
	_ = json.NewDecoder(vListRes.Body).Decode(&vListOut)
	vListRes.Body.Close()
	if vListRes.StatusCode != http.StatusOK || vListOut.CurrentVersion != 4 || vListOut.VersionCount != 4 || len(vListOut.Versions) != 4 {
		t.Fatalf("unexpected versions list: %+v", vListOut)
	}
	if vListOut.Versions[0].UpdateMethod != storage.MethodOnlineEdit ||
		vListOut.Versions[1].UpdateMethod != storage.MethodVersionUpload ||
		vListOut.Versions[2].UpdateMethod != storage.MethodUploadOverwrite ||
		vListOut.Versions[3].UpdateMethod != storage.MethodInitialUpload {
		t.Fatalf("unexpected version update_method chain: %+v", vListOut.Versions)
	}

	// 5. Download historical v1
	dl1Res, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/versions/1/download")
	dl1Bytes, _ := io.ReadAll(dl1Res.Body)
	dl1Res.Body.Close()
	if dl1Res.StatusCode != http.StatusOK || string(dl1Bytes) != "spec-v1" {
		t.Fatalf("download v1 mismatch: status=%d body=%q", dl1Res.StatusCode, string(dl1Bytes))
	}

	// 6. Restore v2 as active version (records MethodVersionRestore)
	restoreBody, _ := json.Marshal(map[string]string{"note": "切回v2"})
	restRes, _ := adminClient.Post(ts.URL+"/api/files/"+fileID+"/versions/2/restore", "application/json", bytes.NewReader(restoreBody))
	var restOut struct {
		OK   bool               `json:"ok"`
		File storage.FileRecord `json:"file"`
	}
	_ = json.NewDecoder(restRes.Body).Decode(&restOut)
	restRes.Body.Close()
	if restRes.StatusCode != http.StatusOK || restOut.File.UpdateMethod != storage.MethodVersionRestore {
		t.Fatalf("restore v2 failed: status=%d out=%+v", restRes.StatusCode, restOut)
	}
	dlCurRes, _ := adminClient.Get(ts.URL + "/api/files/" + fileID + "/download")
	dlCurBytes, _ := io.ReadAll(dlCurRes.Body)
	dlCurRes.Body.Close()
	if string(dlCurBytes) != "spec-v2" {
		t.Fatalf("expected active content 'spec-v2' after restore, got %q", string(dlCurBytes))
	}

	// 7. Delete historical v1
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/files/"+fileID+"/versions/1", nil)
	delRes, _ := adminClient.Do(delReq)
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("delete v1 failed: %d", delRes.StatusCode)
	}

	// 8. Verify Node Health telemetry fields in /api/cluster
	clRes, _ := adminClient.Get(ts.URL + "/api/cluster")
	var clOut struct {
		Pool cluster.PoolSummary `json:"pool"`
	}
	_ = json.NewDecoder(clRes.Body).Decode(&clOut)
	clRes.Body.Close()
	if len(clOut.Pool.Nodes) != 1 {
		t.Fatalf("expected 1 node in pool, got %d", len(clOut.Pool.Nodes))
	}
	n := clOut.Pool.Nodes[0]
	if n.Version == "" || n.DiskTotalBytes <= 0 || n.MemTotalBytes <= 0 || n.CPUCores <= 0 || n.LastSyncAt.IsZero() || n.TotalUploadBytes <= 0 || n.TotalDownloadBytes <= 0 {
		t.Fatalf("expected populated node telemetry fields, got %+v", n)
	}
}

func TestClusterWorkerVersionManagementAndAutoSync(t *testing.T) {
	entrySrv, entryMgr, _, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Entry-Master",
		QuotaGB:  10,
		QuotaSet: true,
	})
	workerSrv, workerMgr, _, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Worker-Sub",
		QuotaGB:  10,
		QuotaSet: true,
	})

	// Configure updater on both Entry and Worker.
	entryUpd, err := updater.NewManager(t.TempDir(), "tester/LFH")
	if err != nil {
		t.Fatalf("entry NewManager: %v", err)
	}
	entrySrv.SetUpdater(entryUpd)

	workerUpdDir := t.TempDir()
	workerUpd, err := updater.NewManager(workerUpdDir, "tester/LFH")
	if err != nil {
		t.Fatalf("worker NewManager: %v", err)
	}
	workerSrv.SetUpdater(workerUpd)

	if err := workerMgr.JoinEntryCluster(entryTS.URL, entryMgr.Secret(), 0); err != nil {
		t.Fatalf("worker JoinEntryCluster: %v", err)
	}
	if err := workerMgr.SendHeartbeatNow(); err != nil {
		t.Fatalf("worker SendHeartbeatNow: %v", err)
	}

	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)

	// 1. Toggle auto_sync_workers off and back on via /api/admin/cluster/upgrade-nodes
	toggleOff := false
	offBody, _ := json.Marshal(map[string]any{"auto_sync_workers": toggleOff})
	offRes, err := adminClient.Post(entryTS.URL+"/api/admin/cluster/upgrade-nodes", "application/json", bytes.NewReader(offBody))
	if err != nil {
		t.Fatalf("POST upgrade-nodes toggle off: %v", err)
	}
	offRes.Body.Close()
	if offRes.StatusCode != http.StatusOK || entryMgr.AutoSyncWorkers() {
		t.Fatalf("expected AutoSyncWorkers=false after toggle off")
	}

	toggleOn := true
	onBody, _ := json.Marshal(map[string]any{"auto_sync_workers": toggleOn})
	onRes, _ := adminClient.Post(entryTS.URL+"/api/admin/cluster/upgrade-nodes", "application/json", bytes.NewReader(onBody))
	onRes.Body.Close()
	if !entryMgr.AutoSyncWorkers() {
		t.Fatalf("expected AutoSyncWorkers=true after toggle on")
	}

	// 2. Verify GET /api/cluster/download-binary serves a valid non-empty binary with SHA-256 header
	dlBinRes, err := adminClient.Get(entryTS.URL + "/api/cluster/download-binary")
	if err != nil {
		t.Fatalf("GET /api/cluster/download-binary: %v", err)
	}
	_, _ = io.Copy(io.Discard, dlBinRes.Body)
	dlBinRes.Body.Close()
	if dlBinRes.StatusCode != http.StatusOK || dlBinRes.Header.Get("X-LFH-Binary-SHA256") == "" {
		t.Fatalf("expected 200 and X-LFH-Binary-SHA256 header from download-binary, status=%d", dlBinRes.StatusCode)
	}
	_ = workerTS
}

func TestMultiNICEndpointTogglesURLEncodedDeviceHeadersAndMobileDetection(t *testing.T) {
	srv, _, _, ts := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "NIC-Master",
		QuotaGB:  10,
		QuotaSet: true,
	})
	adminClient := loginTestAdmin(t, srv, ts.URL)

	// 1. Save multi-NIC endpoint settings (enable two NIC IPs, disable one virtual NIC IP)
	epPayload, _ := json.Marshal(map[string]any{
		"expose_ip":    "192.168.3.53",
		"expose_ips":   []string{"192.168.3.53", "100.120.53.119"},
		"disabled_ips": []string{"192.168.174.1"},
		"port":         "8080",
	})
	epRes, err := adminClient.Post(ts.URL+"/api/admin/cluster/endpoint", "application/json", bytes.NewReader(epPayload))
	if err != nil {
		t.Fatalf("POST /api/admin/cluster/endpoint: %v", err)
	}
	var epOut struct {
		EntryURL    string        `json:"entry_url"`
		ExposeIPs   []string      `json:"expose_ips"`
		DisabledIPs []string      `json:"disabled_ips"`
		EntryAddrs  []NetworkAddr `json:"entry_addrs"`
	}
	_ = json.NewDecoder(epRes.Body).Decode(&epOut)
	epRes.Body.Close()
	if epRes.StatusCode != http.StatusOK || epOut.EntryURL != "http://192.168.3.53:8080" || len(epOut.ExposeIPs) < 2 || len(epOut.EntryAddrs) < 2 {
		t.Fatalf("unexpected multi-NIC endpoint response: status=%d out=%+v", epRes.StatusCode, epOut)
	}

	// 2. Verify URL-encoded non-ASCII X-Device-Name / X-Device-Detail headers decode properly
	var upBuf bytes.Buffer
	mw := multipart.NewWriter(&upBuf)
	fw, _ := mw.CreateFormFile("files", "mobile-note.txt")
	_, _ = fw.Write([]byte("hello from mobile"))
	_ = mw.Close()

	upReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", &upBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.Header.Set("X-Device-FP", "FP-MOBILE999")
	upReq.Header.Set("X-Device-Name", url.QueryEscape("Windows · Edge (中文终端)"))
	upReq.Header.Set("X-Device-Detail", url.QueryEscape("1920x1080 · 16核 · Asia/Shanghai"))
	upRes, err := adminClient.Do(upReq)
	if err != nil {
		t.Fatalf("POST /api/upload with encoded headers: %v", err)
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated || len(upOut.Uploaded) != 1 || upOut.Uploaded[0].DeviceName != "Windows · Edge (中文终端)" {
		t.Fatalf("expected decoded non-ASCII DeviceName, got %+v", upOut.Uploaded)
	}

	// 3. Verify Mobile vs Desktop HTML automatic detection on GET /
	mobReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	mobReq.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148")
	mobRes, err := ts.Client().Do(mobReq)
	if err != nil {
		t.Fatalf("GET / mobile UA: %v", err)
	}
	mobBody, _ := io.ReadAll(mobRes.Body)
	mobRes.Body.Close()
	if mobRes.StatusCode != http.StatusOK || !strings.Contains(string(mobBody), "mobile.html") {
		t.Fatalf("expected mobile.html served for iPhone UA, got %q", string(mobBody))
	}

	pcReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	pcReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/125.0.0.0")
	pcRes, err := ts.Client().Do(pcReq)
	if err != nil {
		t.Fatalf("GET / desktop UA: %v", err)
	}
	pcBody, _ := io.ReadAll(pcRes.Body)
	pcRes.Body.Close()
	if pcRes.StatusCode != http.StatusOK || !strings.Contains(string(pcBody), "index.html") {
		t.Fatalf("expected index.html served for Windows Chrome UA, got %q", string(pcBody))
	}
}

func TestCreateTxtFileAndSuperClipboardControls(t *testing.T) {
	entrySrv, entryMgr, _, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Clip-Entry",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_, workerMgr, _, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Clip-Worker",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_ = workerTS

	if err := workerMgr.JoinEntryCluster(entryTS.URL, entryMgr.Secret(), 0); err != nil {
		t.Fatalf("worker JoinEntryCluster: %v", err)
	}
	if err := workerMgr.SendHeartbeatNow(); err != nil {
		t.Fatalf("worker SendHeartbeatNow: %v", err)
	}

	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)

	// 1. POST /api/files/create-txt rejects non-.txt extensions
	badBody, _ := json.Marshal(map[string]any{
		"name":    "script.sh",
		"content": "#!/bin/sh",
	})
	badRes, err := adminClient.Post(entryTS.URL+"/api/files/create-txt", "application/json", bytes.NewReader(badBody))
	if err != nil {
		t.Fatalf("POST bad create-txt: %v", err)
	}
	badRes.Body.Close()
	if badRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-.txt file, got %d", badRes.StatusCode)
	}

	// 2. POST /api/files/create-txt auto-appends .txt and saves content to cluster
	goodBody, _ := json.Marshal(map[string]any{
		"name":        "meeting-notes",
		"folder_path": "notes/2026",
		"content":     "第一条纪要\n第二条待办",
		"target_node": entryMgr.NodeID(),
	})
	goodRes, err := adminClient.Post(entryTS.URL+"/api/files/create-txt", "application/json", bytes.NewReader(goodBody))
	if err != nil {
		t.Fatalf("POST good create-txt: %v", err)
	}
	var goodOut struct {
		OK   bool               `json:"ok"`
		File storage.FileRecord `json:"file"`
	}
	_ = json.NewDecoder(goodRes.Body).Decode(&goodOut)
	goodRes.Body.Close()
	if goodRes.StatusCode != http.StatusCreated || !goodOut.OK || goodOut.File.Name != "meeting-notes.txt" || goodOut.File.FolderPath != "notes/2026" {
		t.Fatalf("unexpected create-txt response: status=%d out=%+v", goodRes.StatusCode, goodOut)
	}
	dlRes, _ := adminClient.Get(entryTS.URL + "/api/files/" + goodOut.File.ID + "/download")
	dlRaw, _ := io.ReadAll(dlRes.Body)
	dlRes.Body.Close()
	if string(dlRaw) != "第一条纪要\n第二条待办" {
		t.Fatalf("unexpected downloaded txt content: %q", string(dlRaw))
	}

	// 3. Entry admin toggles Super Clipboard on remote Worker node via /api/admin/cluster/clipboard
	if entryMgr.ClipboardEnabled() || workerMgr.ClipboardEnabled() {
		t.Fatalf("expected Super Clipboard disabled by default on both nodes")
	}
	toggleWorkerOn, _ := json.Marshal(map[string]any{
		"node_id": workerMgr.NodeID(),
		"enabled": true,
	})
	twRes, err := adminClient.Post(entryTS.URL+"/api/admin/cluster/clipboard", "application/json", bytes.NewReader(toggleWorkerOn))
	if err != nil {
		t.Fatalf("POST toggle worker clipboard on: %v", err)
	}
	var twOut struct {
		OK   bool               `json:"ok"`
		Node cluster.NodeStatus `json:"node"`
	}
	_ = json.NewDecoder(twRes.Body).Decode(&twOut)
	twRes.Body.Close()
	if twRes.StatusCode != http.StatusOK || !twOut.OK || !twOut.Node.ClipboardEnabled || !workerMgr.ClipboardEnabled() {
		t.Fatalf("expected worker Super Clipboard enabled remotely, status=%d out=%+v worker=%v", twRes.StatusCode, twOut, workerMgr.ClipboardEnabled())
	}

	// Turn Entry Super Clipboard on as well, then Worker off
	toggleEntryOn, _ := json.Marshal(map[string]any{
		"node_id": entryMgr.NodeID(),
		"enabled": true,
	})
	teRes, _ := adminClient.Post(entryTS.URL+"/api/admin/cluster/clipboard", "application/json", bytes.NewReader(toggleEntryOn))
	teRes.Body.Close()
	if teRes.StatusCode != http.StatusOK || !entryMgr.ClipboardEnabled() {
		t.Fatalf("expected entry Super Clipboard enabled, status=%d", teRes.StatusCode)
	}

	// 4. Local HandleCapturedClipboard ingests auto-named file under 超级剪贴板/YYYY-MM
	tsCapture := time.Date(2026, 11, 22, 9, 50, 1, 0, time.Local)
	entrySrv.HandleCapturedClipboard(clipboard.Item{
		Kind:      "text",
		Ext:       ".txt",
		Data:      []byte("copied from system clipboard"),
		CreatedAt: tsCapture,
	})
	files := entryMgr.AllClusterFiles()
	foundClip := false
	for _, f := range files {
		if f.Name == "clip_20261122_095001.txt" && f.FolderPath == "超级剪贴板/2026-11" {
			foundClip = true
			break
		}
	}
	if !foundClip {
		t.Fatalf("expected auto-uploaded clipboard file in cluster, got %+v", files)
	}
}

func TestZeroCopyLocalImportAndMountsClusterFlow(t *testing.T) {
	entrySrv, entryMgr, _, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "ZC-Entry",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_, workerMgr, _, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "ZC-Worker",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_ = workerTS

	if err := workerMgr.JoinEntryCluster(entryTS.URL, entryMgr.Secret(), 0); err != nil {
		t.Fatalf("worker JoinEntryCluster: %v", err)
	}
	if err := workerMgr.SendHeartbeatNow(); err != nil {
		t.Fatalf("worker SendHeartbeatNow: %v", err)
	}

	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)
	userClient := registerAndLoginTestUser(t, adminClient, entryTS.URL, "regular", "pass12345")

	// 1. Non-admin users must receive 403 on zero-copy admin routes.
	uImpRes, _ := userClient.Post(entryTS.URL+"/api/admin/files/import-local", "application/json", strings.NewReader(`{"source_path":"C:\\temp.txt"}`))
	uImpRes.Body.Close()
	if uImpRes.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin import-local, got %d", uImpRes.StatusCode)
	}
	uMntGet, _ := userClient.Get(entryTS.URL + "/api/admin/mounts")
	uMntGet.Body.Close()
	if uMntGet.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin GET mounts, got %d", uMntGet.StatusCode)
	}

	// 2. Local hardlink zero-copy import on Entry node survives source deletion & serves file + meta.
	srcDir := t.TempDir()
	localFile := filepath.Join(srcDir, "movie-notes.txt")
	if err := os.WriteFile(localFile, []byte("zero-copy hardlink payload"), 0644); err != nil {
		t.Fatal(err)
	}
	impBody, _ := json.Marshal(map[string]any{
		"node_id":     entryMgr.NodeID(),
		"source_path": localFile,
		"folder_path": "本地资料",
	})
	impRes, err := adminClient.Post(entryTS.URL+"/api/admin/files/import-local", "application/json", bytes.NewReader(impBody))
	if err != nil {
		t.Fatalf("POST admin import-local: %v", err)
	}
	var impOut struct {
		OK      bool                  `json:"ok"`
		Files   []storage.FileRecord  `json:"files"`
		Summary storage.ImportSummary `json:"summary"`
	}
	_ = json.NewDecoder(impRes.Body).Decode(&impOut)
	impRes.Body.Close()
	if impRes.StatusCode != http.StatusOK || !impOut.OK || len(impOut.Files) != 1 || impOut.Files[0].StorageMode != storage.StorageModeHardlink {
		t.Fatalf("unexpected import-local output: status=%d out=%+v", impRes.StatusCode, impOut)
	}

	// Delete source file -> LFH hardlink remains readable & downloadable.
	_ = os.Remove(localFile)
	dlRes, _ := adminClient.Get(entryTS.URL + "/api/files/" + impOut.Files[0].ID + "/download")
	dlRaw, _ := io.ReadAll(dlRes.Body)
	dlRes.Body.Close()
	if dlRes.StatusCode != http.StatusOK || string(dlRaw) != "zero-copy hardlink payload" {
		t.Fatalf("expected hardlinked file still readable, status=%d body=%q", dlRes.StatusCode, string(dlRaw))
	}
	metaRes, _ := adminClient.Get(entryTS.URL + "/api/files/" + impOut.Files[0].ID + "/meta")
	var metaOut struct {
		StorageMode string `json:"storage_mode"`
		SourcePath  string `json:"source_path"`
	}
	_ = json.NewDecoder(metaRes.Body).Decode(&metaOut)
	metaRes.Body.Close()
	if metaOut.StorageMode != storage.StorageModeHardlink || metaOut.SourcePath != localFile {
		t.Fatalf("unexpected file meta: %+v", metaOut)
	}

	// 3. Remote worker mount add -> list -> sync -> 410 Gone when source removed -> remove mount.
	workerMountDir := t.TempDir()
	mntFile1 := filepath.Join(workerMountDir, "track1.txt")
	if err := os.WriteFile(mntFile1, []byte("worker-mount-v1"), 0644); err != nil {
		t.Fatal(err)
	}
	addMntBody, _ := json.Marshal(map[string]any{
		"node_id":  workerMgr.NodeID(),
		"dir_path": workerMountDir,
		"alias":    "远程电影库",
	})
	addRes, err := adminClient.Post(entryTS.URL+"/api/admin/mounts", "application/json", bytes.NewReader(addMntBody))
	if err != nil {
		t.Fatalf("POST admin mounts: %v", err)
	}
	var addOut struct {
		OK     bool                       `json:"ok"`
		Mount  storage.MountEntry         `json:"mount"`
		Added  int                        `json:"added"`
		Mounts []cluster.ClusterMountView `json:"mounts"`
	}
	_ = json.NewDecoder(addRes.Body).Decode(&addOut)
	addRes.Body.Close()
	if addRes.StatusCode != http.StatusOK || !addOut.OK || addOut.Added != 1 || len(addOut.Mounts) != 1 {
		t.Fatalf("unexpected add mount output: status=%d out=%+v", addRes.StatusCode, addOut)
	}

	// Add a second file to worker mount directory and trigger sync.
	mntFile2 := filepath.Join(workerMountDir, "track2.txt")
	if err := os.WriteFile(mntFile2, []byte("worker-mount-v2"), 0644); err != nil {
		t.Fatal(err)
	}
	syncReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/api/admin/mounts/"+workerMgr.NodeID()+"/"+addOut.Mount.ID+"/sync", nil)
	syncRes, err := adminClient.Do(syncReq)
	if err != nil {
		t.Fatalf("POST sync mount: %v", err)
	}
	var syncOut struct {
		OK     bool                    `json:"ok"`
		Result storage.MountSyncResult `json:"result"`
	}
	_ = json.NewDecoder(syncRes.Body).Decode(&syncOut)
	syncRes.Body.Close()
	if syncRes.StatusCode != http.StatusOK || !syncOut.OK || syncOut.Result.Added != 1 || syncOut.Result.FileCount != 2 {
		t.Fatalf("unexpected mount sync result: status=%d out=%+v", syncRes.StatusCode, syncOut)
	}

	// Deleting track2.txt directly on disk while mounted returns 410 Gone on download.
	var track2ID string
	for _, f := range entryMgr.AllClusterFiles() {
		if f.Name == "track2.txt" && f.MountID == addOut.Mount.ID {
			track2ID = f.ID
			break
		}
	}
	if track2ID == "" {
		t.Fatalf("expected track2.txt synced in cluster file list")
	}
	_ = os.Remove(mntFile2)
	goneRes, _ := adminClient.Get(entryTS.URL + "/api/files/" + track2ID + "/download")
	goneRes.Body.Close()
	if goneRes.StatusCode != http.StatusGone {
		t.Fatalf("expected 410 Gone when mounted external file missing on disk, got %d", goneRes.StatusCode)
	}

	// Removing mount cleans index without touching track1.txt on disk.
	delReq, _ := http.NewRequest(http.MethodDelete, entryTS.URL+"/api/admin/mounts/"+workerMgr.NodeID()+"/"+addOut.Mount.ID, nil)
	delRes, err := adminClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE mount: %v", err)
	}
	var delOut struct {
		OK      bool `json:"ok"`
		Removed int  `json:"removed"`
	}
	_ = json.NewDecoder(delRes.Body).Decode(&delOut)
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK || !delOut.OK || delOut.Removed != 2 {
		t.Fatalf("unexpected delete mount result: status=%d out=%+v", delRes.StatusCode, delOut)
	}
	if _, statErr := os.Stat(mntFile1); statErr != nil {
		t.Fatalf("RemoveMount should never remove underlying file on disk: %v", statErr)
	}
}
