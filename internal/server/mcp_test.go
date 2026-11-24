package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/mcp"
)

func callMCPTool(t *testing.T, tsURL, token, sid, toolName string, args map[string]any) (mcp.CallToolResult, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, tsURL+"/mcp", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest /mcp: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do /mcp (%s): %v", toolName, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP tool %s HTTP %d: %s", toolName, resp.StatusCode, string(raw))
	}
	var rpc mcp.JSONRPCResponse
	if err := json.Unmarshal(raw, &rpc); err != nil {
		t.Fatalf("decode JSONRPCResponse (%s): %v body=%s", toolName, err, string(raw))
	}
	if rpc.Error != nil {
		t.Fatalf("MCP tool %s unexpected protocol error: %#v", toolName, rpc.Error)
	}
	rb, _ := json.Marshal(rpc.Result)
	var toolRes mcp.CallToolResult
	if err := json.Unmarshal(rb, &toolRes); err != nil {
		t.Fatalf("decode CallToolResult (%s): %v", toolName, err)
	}
	var decoded map[string]any
	if len(toolRes.Content) > 0 {
		_ = json.Unmarshal([]byte(toolRes.Content[0].Text), &decoded)
	}
	return toolRes, decoded
}

func initMCPSession(t *testing.T, tsURL, token string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"clientInfo":      map[string]string{"name": "agent-e2e", "version": "1.0"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, tsURL+"/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	resp.Body.Close()
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || sid == "" {
		t.Fatalf("initialize failed: status=%d sid=%q", resp.StatusCode, sid)
	}
	return sid
}

func TestMCPMultiAgentShopAppCollaborationE2E(t *testing.T) {
	srv, ts := newTestServer(t)
	adminClient := loginTestAdmin(t, srv, ts.URL)

	// Prepare local workspace directory representing Machine A's ShopApp repo.
	projDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projDir, "docs"), 0755)
	_ = os.MkdirAll(filepath.Join(projDir, "db"), 0755)
	_ = os.MkdirAll(filepath.Join(projDir, "backend"), 0755)
	_ = os.MkdirAll(filepath.Join(projDir, "node_modules", "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(projDir, "docs", "db-design.md"), []byte("# Schema Design\nusers table needs email\n"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, "backend", "user_handler.go"), []byte("package backend\n\nfunc HandleUser() string {\n\treturn \"v1\"\n}\n"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, ".env"), []byte("DB_PASS=secret123"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, "node_modules", "pkg", "index.js"), []byte("ignored"), 0644)

	// Admin mounts ShopApp as writable workspace.
	mntBody, _ := json.Marshal(map[string]any{
		"dir_path":      projDir,
		"alias":         "shopapp",
		"writable":      true,
		"shadow_backup": true,
	})
	mntResp, err := adminClient.Post(ts.URL+"/api/admin/mounts", "application/json", bytes.NewReader(mntBody))
	if err != nil {
		t.Fatalf("add mount: %v", err)
	}
	mntResp.Body.Close()
	if mntResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 mounting shopapp, got %d", mntResp.StatusCode)
	}

	// Admin also uploads a private file outside Agent scopes (for wait_for_changes isolation test).
	codePriv, _, rawPriv := uploadMultipartWithConcurrency(t, adminClient, ts.URL, "", "private", "secret_plan.txt", "top secret", "", "")
	if codePriv != http.StatusCreated {
		t.Fatalf("upload private file: %d %s", codePriv, string(rawPriv))
	}

	createAgentKey := func(name string, scopes, readFolders, writeFolders []string) string {
		b, _ := json.Marshal(map[string]any{
			"name":                 name,
			"scopes":               scopes,
			"read_folders":         readFolders,
			"write_folders":        writeFolders,
			"allow_set_visibility": true,
		})
		r, err := adminClient.Post(ts.URL+"/api/agent-keys", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("create agent key: %v", err)
		}
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		return out.Token
	}

	tokenB := createAgentKey(
		"B-backend-agent",
		[]string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare},
		[]string{"shopapp/docs", "shopapp/db", "shopapp/backend"},
		[]string{"shopapp/backend"},
	)
	tokenC := createAgentKey(
		"C-db-agent",
		[]string{auth.ScopeRead, auth.ScopeWrite},
		[]string{"shopapp/docs", "shopapp/db"},
		[]string{"shopapp/db"},
	)

	sidB := initMCPSession(t, ts.URL, tokenB)
	sidC := initMCPSession(t, ts.URL, tokenC)

	// 1. whoami omits real host dir_path.
	whoRes, whoMap := callMCPTool(t, ts.URL, tokenB, sidB, "whoami", nil)
	if whoRes.IsError || strings.Contains(whoRes.Content[0].Text, projDir) {
		t.Fatalf("whoami failed or leaked host dir path: %+v", whoRes)
	}
	if whoMap["agent_name"] != "B-backend-agent" {
		t.Fatalf("unexpected whoami payload: %+v", whoMap)
	}

	// 2. list_users and list_tree / search_files respect folder isolation.
	usersRes, usersMap := callMCPTool(t, ts.URL, tokenB, sidB, "list_users", nil)
	if usersRes.IsError || len(usersMap["users"].([]any)) == 0 {
		t.Fatalf("list_users failed: %+v", usersRes)
	}

	treeC, treeMapC := callMCPTool(t, ts.URL, tokenC, sidC, "list_tree", map[string]any{})
	if treeC.IsError || strings.Contains(treeC.Content[0].Text, "user_handler.go") || strings.Contains(treeC.Content[0].Text, "secret_plan.txt") {
		t.Fatalf("Agent C saw out-of-scope files: %+v map=%+v", treeC, treeMapC)
	}

	searchRes, searchMap := callMCPTool(t, ts.URL, tokenC, sidC, "search_files", map[string]any{"content_query": "email"})
	if searchRes.IsError || int(searchMap["total_matched"].(float64)) != 1 {
		t.Fatalf("Agent C search_files expected 1 match in docs, got %+v", searchMap)
	}

	// 3. Agent C performs atomic batch_commit creating DB migration + schema in shopapp/db while Agent B runs wait_for_changes!
	baseVer := srv.events.Version()
	var waitWG sync.WaitGroup
	var waitRes mcp.CallToolResult
	var waitMap map[string]any
	waitWG.Add(1)
	go func() {
		defer waitWG.Done()
		waitRes, waitMap = callMCPTool(t, ts.URL, tokenB, sidB, "wait_for_changes", map[string]any{
			"folder_prefix": "shopapp/db",
			"since_version": baseVer,
			"timeout_sec":   10,
		})
	}()

	time.Sleep(60 * time.Millisecond)

	// Upload an unrelated admin file into private/ first -> Agent B must NOT wake up for it!
	codePriv2, _, _ := uploadMultipartWithConcurrency(t, adminClient, ts.URL, "", "private", "noise.txt", "noise", "", "")
	if codePriv2 != http.StatusCreated {
		t.Fatalf("upload noise.txt: %d", codePriv2)
	}

	batchC, batchMapC := callMCPTool(t, ts.URL, tokenC, sidC, "batch_commit", map[string]any{
		"note": "create db migration and schema",
		"operations": []map[string]any{
			{"action": "write", "path": "shopapp/db/001_init.sql", "content": "CREATE TABLE users (id INT, email TEXT);\n", "expected_version": 0},
			{"action": "write", "path": "shopapp/db/schema.sql", "content": "-- current schema v1\nTABLE users(id, email);\n", "expected_version": 0},
		},
	})
	if batchC.IsError || batchMapC["batch_id"] == "" {
		t.Fatalf("Agent C batch_commit failed: %+v", batchC)
	}

	waitWG.Wait()
	if waitRes.IsError || strings.Contains(waitRes.Content[0].Text, "noise.txt") {
		t.Fatalf("wait_for_changes error or leaked private noise: %+v", waitRes)
	}
	evList, _ := waitMap["events"].([]any)
	if len(evList) == 0 {
		t.Fatalf("expected wait_for_changes to deliver shopapp/db batch event, got %+v", waitMap)
	}

	// 4. Agent B reads shopapp/backend/user_handler.go with line slicing + if_none_match.
	readB, readMapB := callMCPTool(t, ts.URL, tokenB, sidB, "read_file", map[string]any{
		"path":       "shopapp/backend/user_handler.go",
		"start_line": 3,
		"end_line":   5,
	})
	if readB.IsError || !strings.Contains(readMapB["content"].(string), `return "v1"`) || int(readMapB["version"].(float64)) != 1 {
		t.Fatalf("read_file slice unexpected: %+v", readMapB)
	}
	shaV1 := readMapB["sha256"].(string)

	skipRead, skipMap := callMCPTool(t, ts.URL, tokenB, sidB, "read_file", map[string]any{
		"path":          "shopapp/backend/user_handler.go",
		"if_none_match": shaV1,
	})
	if skipRead.IsError || skipMap["not_modified"] != true || skipMap["content"] != nil {
		t.Fatalf("expected if_none_match not_modified=true, got %+v", skipMap)
	}

	// 5. Agent B acquires lease, patches user_handler.go via search_replace + unified_diff, checks 409 on stale version, releases lease.
	acqRes, _ := callMCPTool(t, ts.URL, tokenB, sidB, "acquire_lease", map[string]any{
		"path":        "shopapp/backend/user_handler.go",
		"ttl_seconds": 60,
	})
	if acqRes.IsError {
		t.Fatalf("acquire_lease failed: %+v", acqRes)
	}

	patch1, pMap1 := callMCPTool(t, ts.URL, tokenB, sidB, "patch_file", map[string]any{
		"path":             "shopapp/backend/user_handler.go",
		"mode":             "search_replace",
		"hunks":            []map[string]any{{"old_text": `return "v1"`, "new_text": `return "v2"`}},
		"expected_version": 1,
		"note":             "use v2 handler",
	})
	if patch1.IsError || int(pMap1["version"].(float64)) != 2 {
		t.Fatalf("patch_file search_replace failed: %+v", patch1)
	}

	// Stale expected_version=1 returns version_conflict structured error.
	stalePatch, staleMap := callMCPTool(t, ts.URL, tokenB, sidB, "patch_file", map[string]any{
		"path":             "shopapp/backend/user_handler.go",
		"mode":             "search_replace",
		"hunks":            []map[string]any{{"old_text": `return "v2"`, "new_text": `return "v3"`}},
		"expected_version": 1,
	})
	if !stalePatch.IsError || staleMap["code"] != "version_conflict" {
		t.Fatalf("expected version_conflict, got %+v", stalePatch)
	}

	// unified_diff patch succeeds at expected_version=2 -> v3.
	patch2, pMap2 := callMCPTool(t, ts.URL, tokenB, sidB, "patch_file", map[string]any{
		"path":             "shopapp/backend/user_handler.go",
		"mode":             "unified_diff",
		"unified_diff":     "@@ -2,4 +2,4 @@\n \n func HandleUser() string {\n-\treturn \"v2\"\n+\treturn \"v3\"\n }\n",
		"expected_version": 2,
		"note":             "unified diff v3",
	})
	if patch2.IsError || int(pMap2["version"].(float64)) != 3 {
		t.Fatalf("patch_file unified_diff failed: %+v", patch2)
	}
	if diskB, _ := os.ReadFile(filepath.Join(projDir, "backend", "user_handler.go")); !strings.Contains(string(diskB), `"v3"`) {
		t.Fatalf("host file missing v3 after unified_diff: %s", string(diskB))
	}

	relRes, _ := callMCPTool(t, ts.URL, tokenB, sidB, "release_lease", map[string]any{
		"path": "shopapp/backend/user_handler.go",
	})
	if relRes.IsError {
		t.Fatalf("release_lease failed: %+v", relRes)
	}

	// 6. get_file_versions + restore_file_version reverts Machine A's host disk back to v1!
	versRes, versMap := callMCPTool(t, ts.URL, tokenB, sidB, "get_file_versions", map[string]any{
		"path": "shopapp/backend/user_handler.go",
	})
	if versRes.IsError || int(versMap["current_version"].(float64)) != 3 || len(versMap["versions"].([]any)) != 3 {
		t.Fatalf("get_file_versions unexpected: %+v", versMap)
	}
	restRes, restMap := callMCPTool(t, ts.URL, tokenB, sidB, "restore_file_version", map[string]any{
		"path":             "shopapp/backend/user_handler.go",
		"version":          1,
		"expected_version": 3,
		"note":             "revert to v1",
	})
	if restRes.IsError || int(restMap["version"].(float64)) != 4 {
		t.Fatalf("restore_file_version failed: %+v", restRes)
	}
	if diskRev, _ := os.ReadFile(filepath.Join(projDir, "backend", "user_handler.go")); !strings.Contains(string(diskRev), `"v1"`) {
		t.Fatalf("host file not reverted to v1: %s", string(diskRev))
	}

	// 7. write_file writes new file + respects allowed_users visibility + security blocks .env & cross-folder writes.
	adminUser, _, _ := srv.auth.InitialAdminCredentials()
	wRes, wMap := callMCPTool(t, ts.URL, tokenB, sidB, "write_file", map[string]any{
		"path":             "shopapp/backend/notes.md",
		"content":          "# Backend Notes\n",
		"expected_version": 0,
		"allowed_users":    []string{adminUser},
	})
	if wRes.IsError || int(wMap["version"].(float64)) != 1 {
		t.Fatalf("write_file failed: %+v", wRes)
	}
	visRes, visMap := callMCPTool(t, ts.URL, tokenB, sidB, "set_file_visibility", map[string]any{
		"path":          "shopapp/backend/notes.md",
		"allowed_users": []string{adminUser},
	})
	if visRes.IsError || len(visMap["allowed_users"].([]any)) == 0 {
		t.Fatalf("set_file_visibility failed: %+v", visRes)
	}

	// Out-of-scope write, .env write, and delete without ScopeDelete must all fail with forbidden.
	outScope, outMap := callMCPTool(t, ts.URL, tokenB, sidB, "write_file", map[string]any{
		"path":    "shopapp/db/evil.sql",
		"content": "DROP TABLE users;",
	})
	if !outScope.IsError || outMap["code"] != "forbidden" {
		t.Fatalf("expected forbidden for cross-folder write, got %+v", outScope)
	}
	envWrite, envMap := callMCPTool(t, ts.URL, tokenB, sidB, "write_file", map[string]any{
		"path":    "shopapp/backend/.env",
		"content": "HACK=1",
	})
	if !envWrite.IsError || envMap["code"] != "forbidden" {
		t.Fatalf("expected forbidden writing .env, got %+v", envWrite)
	}
	delDenied, delMap := callMCPTool(t, ts.URL, tokenB, sidB, "delete_file", map[string]any{
		"path": "shopapp/backend/notes.md",
	})
	if !delDenied.IsError || delMap["code"] != "forbidden" {
		t.Fatalf("expected forbidden deleting without ScopeDelete, got %+v", delDenied)
	}
}

func TestRESTPatchBatchAndRemoteWorkerMountPropagation(t *testing.T) {
	entrySrv, entryMgr, _, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Entry-MCP",
		QuotaGB:  10,
		QuotaSet: true,
	})
	_, workerMgr, workerStore, _ := newTestServerWithCluster(t, cluster.Options{
		Role:          cluster.RoleWorker,
		NodeName:      "Worker-Mount",
		ClusterSecret: entryMgr.Secret(),
		EntryHint:     entryTS.URL,
		QuotaGB:       10,
		QuotaSet:      true,
	})
	if err := workerMgr.SendTestHeartbeat(entryTS.URL, entryMgr.Secret()); err != nil {
		t.Fatalf("worker heartbeat: %v", err)
	}

	// Mount a writable project folder on the REMOTE Worker node.
	workerProjDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(workerProjDir, "api"), 0755)
	_ = os.WriteFile(filepath.Join(workerProjDir, "api", "routes.go"), []byte("package api\n\nconst Ver = 1\n"), 0644)

	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)
	mntPayload, _ := json.Marshal(map[string]any{
		"node_id":       workerMgr.NodeID(),
		"dir_path":      workerProjDir,
		"alias":         "remoteproj",
		"writable":      true,
		"shadow_backup": true,
	})
	mntResp, err := adminClient.Post(entryTS.URL+"/api/admin/mounts", "application/json", bytes.NewReader(mntPayload))
	if err != nil {
		t.Fatalf("remote mount: %v", err)
	}
	mntResp.Body.Close()
	if mntResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 mounting remoteproj on worker, got %d", mntResp.StatusCode)
	}

	// Create AgentKeys X & Y scoped to remoteproj/api.
	makeAgent := func(name string) string {
		b, _ := json.Marshal(map[string]any{
			"name":          name,
			"scopes":        []string{auth.ScopeRead, auth.ScopeWrite},
			"read_folders":  []string{"remoteproj"},
			"write_folders": []string{"remoteproj/api"},
		})
		r, _ := adminClient.Post(entryTS.URL+"/api/agent-keys", "application/json", bytes.NewReader(b))
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		return out.Token
	}
	tokenX := makeAgent("worker-agent-x")
	tokenY := makeAgent("worker-agent-y")

	remoteRec, ok := workerStore.FindByLogicalPath("remoteproj/api", "routes.go")
	if !ok {
		t.Fatal("expected remoteproj/api/routes.go indexed on worker store")
	}

	// 1. REST POST /api/files/{id}/patch forwards to Worker's writable mount -> v2.
	patchBody := `{"mode":"search_replace","hunks":[{"old_text":"Ver = 1","new_text":"Ver = 2"}],"expected_version":1,"note":"rest remote patch"}`
	restPatch := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteRec.ID+"/patch", tokenX, strings.NewReader(patchBody), "application/json")
	rawRestPatch, _ := io.ReadAll(restPatch.Body)
	restPatch.Body.Close()
	if restPatch.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 REST remote patch, got %d body=%s", restPatch.StatusCode, string(rawRestPatch))
	}
	if b, _ := os.ReadFile(filepath.Join(workerProjDir, "api", "routes.go")); !strings.Contains(string(b), "Ver = 2") {
		t.Fatalf("remote worker host file not updated by REST patch: %s", string(b))
	}

	// Stale REST patch -> 409 propagated cleanly.
	staleRest := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteRec.ID+"/patch", tokenX, strings.NewReader(patchBody), "application/json")
	staleRest.Body.Close()
	if staleRest.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 from remote worker stale patch, got %d", staleRest.StatusCode)
	}

	// Bad hunk REST patch -> 422 Unprocessable Entity propagated cleanly.
	badPatchBody := `{"mode":"search_replace","hunks":[{"old_text":"NO_MATCH","new_text":"x"}],"expected_version":2}`
	badRest := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/"+remoteRec.ID+"/patch", tokenX, strings.NewReader(badPatchBody), "application/json")
	badRest.Body.Close()
	if badRest.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 from remote worker failed patch, got %d", badRest.StatusCode)
	}

	// 2. REST POST /api/files/batch-commit transparently commits onto the remote Worker mount!
	batchBody := `{"note":"remote batch","operations":[{"action":"patch","path":"remoteproj/api/routes.go","hunks":[{"old_text":"Ver = 2","new_text":"Ver = 3"}],"expected_version":2},{"action":"write","path":"remoteproj/api/client.go","content":"package api\nfunc Client() {}\n","expected_version":0}]}`
	restBatch := doBearerRequest(t, http.MethodPost, entryTS.URL+"/api/files/batch-commit", tokenX, strings.NewReader(batchBody), "application/json")
	rawRestBatch, _ := io.ReadAll(restBatch.Body)
	restBatch.Body.Close()
	if restBatch.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 REST remote batch-commit, got %d body=%s", restBatch.StatusCode, string(rawRestBatch))
	}
	if b, _ := os.ReadFile(filepath.Join(workerProjDir, "api", "client.go")); !strings.Contains(string(b), "func Client()") {
		t.Fatalf("remote worker mount missing client.go: %s", string(b))
	}

	// 3. MCP write_file, search_files, read_file, acquire_lease (blocks tokenX via 423) on remote Worker mount!
	wRemote, _ := callMCPTool(t, entryTS.URL, tokenX, "", "write_file", map[string]any{
		"path":             "remoteproj/api/mcp_new.go",
		"content":          "package api\n// marker_remote_mcp\n",
		"expected_version": 0,
	})
	if wRemote.IsError {
		t.Fatalf("MCP write_file to remote worker mount failed: %+v", wRemote)
	}
	if b, _ := os.ReadFile(filepath.Join(workerProjDir, "api", "mcp_new.go")); !strings.Contains(string(b), "marker_remote_mcp") {
		t.Fatalf("MCP write_file did not write through to remote worker disk: %s", string(b))
	}

	sRemote, sMap := callMCPTool(t, entryTS.URL, tokenX, "", "search_files", map[string]any{
		"folder_path":   "remoteproj/api",
		"content_query": "marker_remote_mcp",
	})
	if sRemote.IsError || int(sMap["total_matched"].(float64)) != 1 {
		t.Fatalf("MCP search_files on remote worker expected 1 match, got %+v", sMap)
	}

	rRemote, rMap := callMCPTool(t, entryTS.URL, tokenX, "", "read_file", map[string]any{
		"path": "remoteproj/api/mcp_new.go",
	})
	if rRemote.IsError || !strings.Contains(rMap["content"].(string), "marker_remote_mcp") {
		t.Fatalf("MCP read_file on remote worker failed: %+v", rRemote)
	}

	// Token Y locks routes.go remotely -> MCP patch_file by Token X receives lease_locked!
	acqY, _ := callMCPTool(t, entryTS.URL, tokenY, "", "acquire_lease", map[string]any{
		"path":        "remoteproj/api/routes.go",
		"ttl_seconds": 60,
	})
	if acqY.IsError {
		t.Fatalf("remote acquire_lease failed: %+v", acqY)
	}
	blockedX, blockedMap := callMCPTool(t, entryTS.URL, tokenX, "", "patch_file", map[string]any{
		"path":  "remoteproj/api/routes.go",
		"hunks": []map[string]any{{"old_text": "Ver = 3", "new_text": "Ver = 4"}},
	})
	if !blockedX.IsError || blockedMap["code"] != "lease_locked" {
		t.Fatalf("expected lease_locked propagated through MCP from worker, got %+v", blockedX)
	}
}
