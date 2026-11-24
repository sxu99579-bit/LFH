package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

func TestBridgeTicketsDirectToWorkerWebhooksAndVerifyE2E(t *testing.T) {
	entrySrv, entryMgr, entryStore, entryTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Entry-Bridge",
		QuotaGB:  10,
		QuotaSet: true,
	})
	defer func() { _ = entryStore.Close() }()
	defer entrySrv.CloseEventStreams()

	workerSrv, workerMgr, workerStore, workerTS := newTestServerWithCluster(t, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Worker-Bridge",
		QuotaGB:  10,
		QuotaSet: true,
	})
	defer func() { _ = workerStore.Close() }()
	defer workerSrv.CloseEventStreams()

	_ = workerMgr.SetAutoSyncWorkers(false)
	if err := workerMgr.JoinEntryCluster(entryTS.URL, entryMgr.Secret(), 0); err != nil {
		t.Fatalf("worker JoinEntryCluster: %v", err)
	}
	hbStatus := workerMgr.SelfStatus()
	hbStatus.HTTPURL = workerTS.URL
	hbPayload, _ := json.Marshal(cluster.HeartbeatPayload{Node: hbStatus})
	hbReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/internal/cluster/heartbeat", bytes.NewReader(hbPayload))
	hbReq.Header.Set("Content-Type", "application/json")
	cluster.SignRequest(hbReq, entryMgr.Secret(), hbStatus.NodeID)
	hbRes, err := http.DefaultClient.Do(hbReq)
	if err != nil {
		t.Fatalf("send worker heartbeat: %v", err)
	}
	hbRes.Body.Close()

	// Create admin cookie client + AgentKeys on Entry.
	adminUser, _, _ := entrySrv.auth.InitialAdminCredentials()
	adminClient := loginTestAdmin(t, entrySrv, entryTS.URL)

	// 1. Admin creates a Webhook rule on "dropzone" and tests synchronous SendTest.
	var whMu sync.Mutex
	var whEvents []bridge.WebhookEvent
	whNotify := make(chan string, 10)
	whSecret := "wh_secret_e2e"
	hookTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-LFH-Signature-256") != bridge.SignWebhookPayload(whSecret, body) {
			http.Error(w, "bad sig", http.StatusUnauthorized)
			return
		}
		var ev bridge.WebhookEvent
		_ = json.Unmarshal(body, &ev)
		whMu.Lock()
		whEvents = append(whEvents, ev)
		whMu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case whNotify <- ev.Event:
		default:
		}
	}))
	defer hookTS.Close()

	createWHBody, _ := json.Marshal(bridge.CreateWebhookInput{
		Name:                  "DropzoneCI",
		FolderPrefix:          "dropzone",
		Events:                []string{bridge.EventFileUploaded, bridge.EventVerifyCompleted},
		TargetURL:             hookTS.URL + "/ci",
		Secret:                whSecret,
		IncludeDownloadTicket: true,
	})
	whResp, err := adminClient.Post(entryTS.URL+"/api/admin/webhooks", "application/json", bytes.NewReader(createWHBody))
	if err != nil {
		t.Fatalf("POST /api/admin/webhooks: %v", err)
	}
	var createWHOut struct {
		OK      bool                     `json:"ok"`
		Webhook bridge.PublicWebhookRule `json:"webhook"`
	}
	_ = json.NewDecoder(whResp.Body).Decode(&createWHOut)
	whResp.Body.Close()
	if whResp.StatusCode != http.StatusCreated || !createWHOut.Webhook.HasSecret || createWHOut.Webhook.ID == "" {
		t.Fatalf("create webhook failed: status=%d out=%+v", whResp.StatusCode, createWHOut)
	}

	testWHResp, err := adminClient.Post(entryTS.URL+"/api/admin/webhooks/"+createWHOut.Webhook.ID+"/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST webhook test: %v", err)
	}
	var testWHOut struct {
		OK     bool                     `json:"ok"`
		Result bridge.WebhookTestResult `json:"result"`
	}
	_ = json.NewDecoder(testWHResp.Body).Decode(&testWHOut)
	testWHResp.Body.Close()
	if !testWHOut.OK || !testWHOut.Result.OK {
		t.Fatalf("webhook SendTest failed: %+v", testWHOut)
	}
	<-whNotify

	// 2. Create two AgentKeys: BridgeAgent (with verify) and NoVerifyAgent (without verify).
	plainBridgeTok, _, err := entrySrv.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:          "BridgeAgent",
		OwnerUsername: adminUser,
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeVerify},
		ReadFolders:   []string{"*"},
		WriteFolders:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAgentKey BridgeAgent: %v", err)
	}
	plainNoVerifyTok, _, err := entrySrv.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:          "NoVerifyAgent",
		OwnerUsername: adminUser,
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite},
		ReadFolders:   []string{"*"},
		WriteFolders:  []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAgentKey NoVerifyAgent: %v", err)
	}

	// 3. Issue a Direct-to-Worker upload ticket targeting workerMgr.NodeID().
	ticketReqBody, _ := json.Marshal(IssueTicketRequest{
		Action:     "up",
		Path:       "dropzone/artifact.bin",
		TargetNode: workerMgr.NodeID(),
		MaxBytes:   64,
		TTLSeconds: 120,
	})
	issueReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/api/tickets", bytes.NewReader(ticketReqBody))
	issueReq.Header.Set("Content-Type", "application/json")
	issueReq.Header.Set("Authorization", "Bearer "+plainBridgeTok)
	issueResp, err := http.DefaultClient.Do(issueReq)
	if err != nil {
		t.Fatalf("POST /api/tickets: %v", err)
	}
	var upTicket IssueTicketResponse
	_ = json.NewDecoder(issueResp.Body).Decode(&upTicket)
	issueResp.Body.Close()
	if issueResp.StatusCode != http.StatusOK || !upTicket.OK || !upTicket.SingleUse {
		t.Fatalf("unexpected upload ticket response: status=%d body=%+v", issueResp.StatusCode, upTicket)
	}
	if !strings.HasPrefix(upTicket.RecommendedURL, workerTS.URL+"/s/ticket?") {
		t.Fatalf("expected Direct-to-Worker recommended_url on %s, got %q (direct=%q entry=%q)", workerTS.URL, upTicket.RecommendedURL, upTicket.DirectURL, upTicket.EntryURL)
	}

	// 4. PUT directly to Worker /s/ticket -> 201 Created, replay PUT -> 409 Conflict, webhook receives file.uploaded.
	payload := []byte("direct-worker-binary-payload-v1")
	putReq, _ := http.NewRequest(http.MethodPut, upTicket.RecommendedURL, bytes.NewReader(payload))
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT direct worker /s/ticket: %v", err)
	}
	var putOut struct {
		OK   bool               `json:"ok"`
		File storage.FileRecord `json:"file"`
	}
	_ = json.NewDecoder(putResp.Body).Decode(&putOut)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated || !putOut.OK || putOut.File.NodeID != workerMgr.NodeID() {
		t.Fatalf("expected 201 direct upload onto worker, got status=%d out=%+v", putResp.StatusCode, putOut)
	}

	replayReq, _ := http.NewRequest(http.MethodPut, upTicket.RecommendedURL, bytes.NewReader(payload))
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("replay PUT: %v", err)
	}
	replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on single-use upload ticket replay, got %d", replayResp.StatusCode)
	}

	// Wait for webhook file.uploaded triggered via Worker -> Entry bridge-event forwarding.
	select {
	case evName := <-whNotify:
		if evName != bridge.EventFileUploaded {
			t.Fatalf("expected webhook file.uploaded, got %q", evName)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for Direct-to-Worker file.uploaded webhook")
	}

	// Oversized PUT with max_bytes=8 -> 413 Request Entity Too Large.
	smallReqBody, _ := json.Marshal(IssueTicketRequest{
		Action:     "up",
		Path:       "dropzone/too-big.bin",
		TargetNode: workerMgr.NodeID(),
		MaxBytes:   8,
	})
	sReq, _ := http.NewRequest(http.MethodPost, entryTS.URL+"/api/tickets", bytes.NewReader(smallReqBody))
	sReq.Header.Set("Content-Type", "application/json")
	sReq.Header.Set("Authorization", "Bearer "+plainBridgeTok)
	sResp, _ := http.DefaultClient.Do(sReq)
	var smallTicket IssueTicketResponse
	_ = json.NewDecoder(sResp.Body).Decode(&smallTicket)
	sResp.Body.Close()
	bigPutReq, _ := http.NewRequest(http.MethodPut, smallTicket.RecommendedURL, bytes.NewReader([]byte("0123456789abcdef")))
	bigPutResp, err := http.DefaultClient.Do(bigPutReq)
	if err != nil {
		t.Fatalf("big PUT: %v", err)
	}
	bigPutResp.Body.Close()
	if bigPutResp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Request Entity Too Large, got %d", bigPutResp.StatusCode)
	}

	// 5. Agent uses MCP issue_transfer_ticket (down) and fetches exact bytes directly from Worker.
	sidBridge := initMCPSession(t, entryTS.URL, plainBridgeTok)
	dlToolRes, dlMap := callMCPTool(t, entryTS.URL, plainBridgeTok, sidBridge, "issue_transfer_ticket", map[string]any{
		"action": "down",
		"path":   "dropzone/artifact.bin",
	})
	if dlToolRes.IsError {
		t.Fatalf("MCP issue_transfer_ticket(down) error: %+v", dlToolRes)
	}
	dlURL, _ := dlMap["recommended_url"].(string)
	if !strings.HasPrefix(dlURL, workerTS.URL+"/s/ticket?") {
		t.Fatalf("expected Direct-to-Worker download ticket on worker, got %q", dlURL)
	}
	getResp, err := http.Get(dlURL)
	if err != nil {
		t.Fatalf("GET worker /s/ticket: %v", err)
	}
	dlBytes, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK || string(dlBytes) != string(payload) {
		t.Fatalf("direct worker download mismatch: status=%d got=%q want=%q", getResp.StatusCode, string(dlBytes), string(payload))
	}

	// 6. Admin adds a writable Worker mount with a VerifyPreset ("go version"); verify via MCP trigger_workspace_verify.
	workerWorkDir := t.TempDir()
	addMountBody, _ := json.Marshal(map[string]any{
		"node_id":  workerMgr.NodeID(),
		"dir_path": workerWorkDir,
		"alias":    "dropzone/work",
		"writable": true,
		"verify_presets": []storage.VerifyPreset{
			{ID: "go-ver", Label: "Go Version", Command: "go", Args: []string{"version"}, TimeoutSec: 30},
		},
	})
	mntResp, err := adminClient.Post(entryTS.URL+"/api/admin/mounts", "application/json", bytes.NewReader(addMountBody))
	if err != nil {
		t.Fatalf("POST /api/admin/mounts: %v", err)
	}
	var mntOut struct {
		OK    bool               `json:"ok"`
		Mount storage.MountEntry `json:"mount"`
	}
	_ = json.NewDecoder(mntResp.Body).Decode(&mntOut)
	mntResp.Body.Close()
	if mntResp.StatusCode != http.StatusOK || !mntOut.OK || mntOut.Mount.ID == "" {
		t.Fatalf("create worker verify mount failed: status=%d out=%+v", mntResp.StatusCode, mntOut)
	}

	// Agent without verify scope must receive forbidden.
	sidNoVerify := initMCPSession(t, entryTS.URL, plainNoVerifyTok)
	deniedRes, _ := callMCPTool(t, entryTS.URL, plainNoVerifyTok, sidNoVerify, "trigger_workspace_verify", map[string]any{
		"mount_id":  mntOut.Mount.ID,
		"preset_id": "go-ver",
	})
	if !deniedRes.IsError || !strings.Contains(deniedRes.Content[0].Text, `"code":"forbidden"`) {
		t.Fatalf("expected forbidden without verify scope, got %+v", deniedRes)
	}

	// whoami shows verify_presets; BridgeAgent runs trigger_workspace_verify across Entry -> Worker RPC!
	_, whoMap := callMCPTool(t, entryTS.URL, plainBridgeTok, sidBridge, "whoami", map[string]any{})
	whoRaw, _ := json.Marshal(whoMap)
	if !strings.Contains(string(whoRaw), `"go-ver"`) {
		t.Fatalf("expected whoami to expose verify_presets go-ver, got %s", string(whoRaw))
	}

	verifyRes, verifyMap := callMCPTool(t, entryTS.URL, plainBridgeTok, sidBridge, "trigger_workspace_verify", map[string]any{
		"mount_id":  mntOut.Mount.ID,
		"preset_id": "go-ver",
	})
	if verifyRes.IsError || verifyMap["ok"] != true {
		t.Fatalf("MCP trigger_workspace_verify failed: res=%+v map=%+v", verifyRes, verifyMap)
	}
	if !strings.Contains(strings.ToLower(verifyMap["tail_output"].(string)), "go version") {
		t.Fatalf("expected 'go version' in tail_output, got %+v", verifyMap["tail_output"])
	}
	logRaw, err := os.ReadFile(filepath.Join(workerWorkDir, ".lfh-logs", "go-ver.log"))
	if err != nil || !strings.Contains(strings.ToLower(string(logRaw)), "go version") {
		t.Fatalf("expected log file written in worker workspace, err=%v", err)
	}

	// Confirm verify.completed webhook delivered too.
	select {
	case evName := <-whNotify:
		if evName != bridge.EventVerifyCompleted {
			t.Fatalf("expected webhook verify.completed, got %q", evName)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for verify.completed webhook")
	}
}
