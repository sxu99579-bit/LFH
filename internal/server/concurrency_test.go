package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

func uploadMultipartWithConcurrency(
	t *testing.T,
	client *http.Client,
	baseURL, bearer, folder, name, content string,
	expectedVer string,
	ifMatch string,
) (int, []storage.FileRecord, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if folder != "" {
		_ = mw.WriteField("folder_path", folder)
	}
	if expectedVer != "" {
		_ = mw.WriteField("expected_version", expectedVer)
	}
	part, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte(content))
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	c := client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do upload: %v", err)
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
		t.Fatalf("decode upload: %v body=%s", err, string(raw))
	}
	return resp.StatusCode, out.Uploaded, raw
}

func TestServerWritableMountAndOptimisticConcurrencyHTTP(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	projDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projDir, "backend"), 0755)
	_ = os.MkdirAll(filepath.Join(projDir, "node_modules", "dep"), 0755)
	_ = os.WriteFile(filepath.Join(projDir, "backend", "main.go"), []byte("package main // v1"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, "node_modules", "dep", "index.js"), []byte("ignored"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, ".env"), []byte("SECRET=1"), 0644)

	// Create AgentKeys A & B scoped to shopapp/backend.
	makeAgent := func(name string) string {
		b, _ := json.Marshal(map[string]any{
			"name":          name,
			"scopes":        []string{"read", "write", "share", "delete"},
			"read_folders":  []string{"shopapp"},
			"write_folders": []string{"shopapp/backend"},
		})
		r, err := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		return out.Token
	}
	botA := makeAgent("agent-a")
	botB := makeAgent("agent-b")

	// 1. Add read-only mount first -> upload into it rejected 403.
	addBody, _ := json.Marshal(map[string]any{
		"dir_path": projDir,
		"alias":    "shopapp",
	})
	addRes, err := adminClient.Post(ts.URL+"/api/admin/mounts", "application/json", bytes.NewReader(addBody))
	if err != nil {
		t.Fatalf("add mount: %v", err)
	}
	var addOut struct {
		OK    bool               `json:"ok"`
		Mount storage.MountEntry `json:"mount"`
		Added int                `json:"added"`
	}
	_ = json.NewDecoder(addRes.Body).Decode(&addOut)
	addRes.Body.Close()
	if addRes.StatusCode != http.StatusOK || !addOut.OK || addOut.Mount.Writable {
		t.Fatalf("unexpected read-only mount result: status=%d out=%+v", addRes.StatusCode, addOut)
	}

	roCode, _, _ := uploadMultipartWithConcurrency(t, nil, ts.URL, botA, "shopapp/backend", "svc.go", "package backend", "0", "")
	if roCode != http.StatusForbidden {
		t.Fatalf("expected 403 uploading to read-only mount, got %d", roCode)
	}

	// 2. PATCH mount to writable=true, shadow_backup=true.
	patchMntBody, _ := json.Marshal(map[string]any{
		"writable":      true,
		"shadow_backup": true,
	})
	pmReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/admin/mounts/"+srv.cluster.NodeID()+"/"+addOut.Mount.ID, bytes.NewReader(patchMntBody))
	pmReq.Header.Set("Content-Type", "application/json")
	pmRes, err := adminClient.Do(pmReq)
	if err != nil {
		t.Fatalf("PATCH mount: %v", err)
	}
	var pmOut struct {
		OK    bool               `json:"ok"`
		Mount storage.MountEntry `json:"mount"`
	}
	_ = json.NewDecoder(pmRes.Body).Decode(&pmOut)
	pmRes.Body.Close()
	if pmRes.StatusCode != http.StatusOK || !pmOut.Mount.Writable || !pmOut.Mount.ShadowBackup {
		t.Fatalf("expected mount updated writable+shadow, got status=%d out=%+v", pmRes.StatusCode, pmOut)
	}

	// Check GET /api/files hides .env and node_modules.
	listResp := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files", botA, nil, "")
	var listOut struct {
		Files []storage.FileRecord `json:"files"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&listOut)
	listResp.Body.Close()
	if len(listOut.Files) != 1 || listOut.Files[0].Name != "main.go" {
		t.Fatalf("expected only main.go indexed in writable project mount, got %+v", listOut.Files)
	}
	mainID := listOut.Files[0].ID

	// 3. Create new service.go with expected_version=0 -> 201 and host disk written; duplicate 0 -> 409 Conflict.
	cCode, createdFiles, cRaw := uploadMultipartWithConcurrency(t, nil, ts.URL, botA, "shopapp/backend", "service.go", "package backend // svc1", "0", "")
	if cCode != http.StatusCreated || len(createdFiles) != 1 {
		t.Fatalf("expected 201 creating service.go, got %d body=%s", cCode, string(cRaw))
	}
	if raw, err := os.ReadFile(filepath.Join(projDir, "backend", "service.go")); err != nil || string(raw) != "package backend // svc1" {
		t.Fatalf("host service.go mismatch: err=%v data=%q", err, string(raw))
	}

	dupCode, _, dupRaw := uploadMultipartWithConcurrency(t, nil, ts.URL, botB, "shopapp/backend", "service.go", "overwrite", "0", "")
	if dupCode != http.StatusConflict || !strings.Contains(string(dupRaw), "version_conflict") {
		t.Fatalf("expected 409 version_conflict on duplicate expected_version=0, got %d body=%s", dupCode, string(dupRaw))
	}

	// 4. botA edits main.go using If-Match: "v1" -> v2; botB tries expected_version=1 -> 409 Conflict.
	editReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/files/"+mainID+"/edit", strings.NewReader(`{"content":"package main // v2","note":"botA edit"}`))
	editReq.Header.Set("Authorization", "Bearer "+botA)
	editReq.Header.Set("Content-Type", "application/json")
	editReq.Header.Set("If-Match", `"v1"`)
	editRes, err := http.DefaultClient.Do(editReq)
	if err != nil {
		t.Fatalf("edit If-Match v1: %v", err)
	}
	editRaw, _ := io.ReadAll(editRes.Body)
	editRes.Body.Close()
	if editRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 editing main.go v1->v2, got %d body=%s", editRes.StatusCode, string(editRaw))
	}

	staleRes := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+mainID+"/edit", botB, strings.NewReader(`{"content":"package main // stale","expected_version":1}`), "application/json")
	staleRaw, _ := io.ReadAll(staleRes.Body)
	staleRes.Body.Close()
	if staleRes.StatusCode != http.StatusConflict || !strings.Contains(string(staleRaw), "version_conflict") {
		t.Fatalf("expected 409 conflict for botB stale edit, got %d body=%s", staleRes.StatusCode, string(staleRaw))
	}

	// 5. Restore v1 with expected_version=2 -> writes v1 Shadow backup back to host disk!
	restRes := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+mainID+"/versions/1/restore", botA, strings.NewReader(`{"note":"revert","expected_version":2}`), "application/json")
	restRaw, _ := io.ReadAll(restRes.Body)
	restRes.Body.Close()
	if restRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 restoring v1 on mount, got %d body=%s", restRes.StatusCode, string(restRaw))
	}
	if diskMain, _ := os.ReadFile(filepath.Join(projDir, "backend", "main.go")); string(diskMain) != "package main // v1" {
		t.Fatalf("expected host main.go restored to v1, got %q", string(diskMain))
	}

	// 6. Sensitive files (.env, server.key) blocked with 403 Forbidden.
	for _, badName := range []string{".env", "server.key"} {
		badCode, _, _ := uploadMultipartWithConcurrency(t, nil, ts.URL, botA, "shopapp/backend", badName, "hack", "", "")
		if badCode != http.StatusForbidden {
			t.Fatalf("expected 403 writing %s into mount, got %d", badName, badCode)
		}
		if _, stErr := os.Stat(filepath.Join(projDir, "backend", badName)); !os.IsNotExist(stErr) {
			t.Fatalf("blocked file %s should not exist on host", badName)
		}
	}
}

func TestServerSoftLeaseAcquireEditReleaseAndAdminForceHTTP(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	makeAgent := func(name string) string {
		b, _ := json.Marshal(map[string]any{
			"name":          name,
			"scopes":        []string{"read", "write", "share", "delete"},
			"read_folders":  []string{"work"},
			"write_folders": []string{"work"},
		})
		r, _ := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(b))
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		return out.Token
	}
	botA := makeAgent("lease-bot-a")
	botB := makeAgent("lease-bot-b")

	code, uploaded, raw := uploadMultipartWithConcurrency(t, nil, ts.URL, botA, "work", "app.go", "package work // v1", "0", "")
	if code != http.StatusCreated || len(uploaded) != 1 {
		t.Fatalf("seed upload failed: %d body=%s", code, string(raw))
	}
	fileID := uploaded[0].ID

	// botA acquires soft lease.
	acqA := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/lease", botA, strings.NewReader(`{"ttl_seconds":60}`), "application/json")
	acqA.Body.Close()
	if acqA.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 acquiring lease, got %d", acqA.StatusCode)
	}

	getL := doBearerRequest(t, http.MethodGet, ts.URL+"/api/files/"+fileID+"/lease", botB, nil, "")
	var getOut struct {
		Locked bool               `json:"locked"`
		Lease  *storage.FileLease `json:"lease"`
	}
	_ = json.NewDecoder(getL.Body).Decode(&getOut)
	getL.Body.Close()
	if !getOut.Locked || getOut.Lease == nil || getOut.Lease.HolderName != "Agent · lease-bot-a" {
		t.Fatalf("unexpected GET lease response: %+v", getOut)
	}

	// botB blocked with 423 Locked on acquire, edit, delete, and release.
	acqB := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/lease", botB, strings.NewReader(`{"ttl_seconds":60}`), "application/json")
	acqB.Body.Close()
	if acqB.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked when botB tries to acquire, got %d", acqB.StatusCode)
	}

	editB := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/edit", botB, strings.NewReader(`{"content":"hijack"}`), "application/json")
	editB.Body.Close()
	if editB.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked when botB edits leased file, got %d", editB.StatusCode)
	}

	delB := doBearerRequest(t, http.MethodDelete, ts.URL+"/api/files/"+fileID, botB, nil, "")
	delB.Body.Close()
	if delB.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked when botB deletes leased file, got %d", delB.StatusCode)
	}

	relB := doBearerRequest(t, http.MethodDelete, ts.URL+"/api/files/"+fileID+"/lease", botB, nil, "")
	relB.Body.Close()
	if relB.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked when botB releases botA lease, got %d", relB.StatusCode)
	}

	// Holder botA edits successfully.
	editA := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/edit", botA, strings.NewReader(`{"content":"package work // v2 by A","expected_version":1}`), "application/json")
	editA.Body.Close()
	if editA.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 holder edit, got %d", editA.StatusCode)
	}

	// Admin human force_lease=true overrides lease; Agent attempting force=true is ignored -> still 423!
	forceAgentAttempt := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/lease", botB, strings.NewReader(`{"ttl_seconds":60,"force":true}`), "application/json")
	forceAgentAttempt.Body.Close()
	if forceAgentAttempt.StatusCode != http.StatusLocked {
		t.Fatalf("agent must not be allowed to force override lease, got %d", forceAgentAttempt.StatusCode)
	}

	adminForceEdit, err := adminClient.Post(ts.URL+"/api/files/"+fileID+"/edit", "application/json", strings.NewReader(`{"content":"package work // v3 admin override","expected_version":2,"force_lease":true}`))
	if err != nil {
		t.Fatalf("admin force edit: %v", err)
	}
	adminForceEdit.Body.Close()
	if adminForceEdit.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 admin force edit, got %d", adminForceEdit.StatusCode)
	}

	// Admin force release allows botB to acquire and edit.
	forceRelReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/files/"+fileID+"/lease?force=true", nil)
	forceRelRes, err := adminClient.Do(forceRelReq)
	if err != nil {
		t.Fatalf("admin force release: %v", err)
	}
	forceRelRes.Body.Close()
	if forceRelRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 admin force release, got %d", forceRelRes.StatusCode)
	}

	editBAfter := doBearerRequest(t, http.MethodPost, ts.URL+"/api/files/"+fileID+"/edit", botB, strings.NewReader(`{"content":"package work // v4 by B","expected_version":3}`), "application/json")
	editBAfter.Body.Close()
	if editBAfter.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 botB edit after lease released, got %d", editBAfter.StatusCode)
	}
}

func TestServerClusterRemoteWorkerConcurrencyPropagation(t *testing.T) {
	entrySrv, entryMgr, _, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Entry-Conc",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_, workerMgr, _, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:          cluster.RoleWorker,
		NodeName:      "Worker-Conc",
		ClusterSecret: entryMgr.Secret(),
		EntryHint:     entryTS.URL,
		QuotaGB:       10,
		QuotaSet:      true,
	})
	_ = workerTS
	if err := workerMgr.SendTestHeartbeat(entryTS.URL, entryMgr.Secret()); err != nil {
		t.Fatalf("worker heartbeat: %v", err)
	}

	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)

	// Upload a file forced to worker node by admin.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("folder_path", "shopapp/worker")
	_ = mw.WriteField("target_node", workerMgr.NodeID())
	part, _ := mw.CreateFormFile("files", "remote.go")
	_, _ = part.Write([]byte("package remote // v1"))
	_ = mw.Close()
	upReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/api/upload", &buf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRes, err := adminClient.Do(upReq)
	if err != nil {
		t.Fatalf("upload to worker: %v", err)
	}
	var upOut struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
	}
	_ = json.NewDecoder(upRes.Body).Decode(&upOut)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated || len(upOut.Uploaded) != 1 {
		t.Fatalf("expected 201 uploading to worker, got %d", upRes.StatusCode)
	}
	remoteID := upOut.Uploaded[0].ID

	// Create AgentKeys A & B.
	makeKey := func(name string) string {
		b, _ := json.Marshal(map[string]any{
			"name":          name,
			"scopes":        []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeDelete},
			"read_folders":  []string{"shopapp/worker"},
			"write_folders": []string{"shopapp/worker"},
		})
		r, _ := adminClient.Post(entryTS.URL+"/api/agent-keys", "application/json", bytes.NewReader(b))
		var o struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&o)
		r.Body.Close()
		return o.Token
	}
	botA := makeKey("remote-a")
	botB := makeKey("remote-b")

	// Remote lease on worker node blocks botB with 423.
	acqA := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteID+"/lease", botA, strings.NewReader(`{"ttl_seconds":60}`), "application/json")
	acqA.Body.Close()
	if acqA.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 remote lease acquire, got %d", acqA.StatusCode)
	}
	editB := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteID+"/edit", botB, strings.NewReader(`{"content":"blocked","expected_version":1}`), "application/json")
	editB.Body.Close()
	if editB.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 propagated from worker node, got %d", editB.StatusCode)
	}

	// Release lease, then test stale expected_version=0 / 99 -> 409 propagated from worker node!
	relA := doBearerRequest(t, http.MethodDelete, entryTS.URL+"/api/files/"+remoteID+"/lease", botA, nil, "")
	relA.Body.Close()
	if relA.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 remote lease release, got %d", relA.StatusCode)
	}
	conflictB := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteID+"/edit", botB, strings.NewReader(`{"content":"stale","expected_version":99}`), "application/json")
	conflictB.Body.Close()
	if conflictB.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 propagated from worker node, got %d", conflictB.StatusCode)
	}
}
