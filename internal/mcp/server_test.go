package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/storage"
)

type stubBackend struct {
	doneCh        chan struct{}
	errToReturn   error
	lastWritePath string
}

func (s *stubBackend) Authenticate(r *http.Request) (auth.Principal, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch tok {
	case "agent-a":
		return auth.Principal{
			User:    auth.PublicUser{Username: "alice", Role: "user"},
			IsAgent: true,
			AgentKey: &auth.PublicAgentKey{
				ID:     "agk_a",
				Name:   "AgentA",
				Scopes: []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare},
			},
		}, nil
	case "agent-b":
		return auth.Principal{
			User:    auth.PublicUser{Username: "bob", Role: "user"},
			IsAgent: true,
			AgentKey: &auth.PublicAgentKey{
				ID:     "agk_b",
				Name:   "AgentB",
				Scopes: []string{auth.ScopeRead},
			},
		}, nil
	case "limited":
		return auth.Principal{}, auth.ErrRateLimited
	default:
		return auth.Principal{}, auth.ErrUnauthenticated
	}
}

func (s *stubBackend) EventsDone() <-chan struct{} { return s.doneCh }

func (s *stubBackend) WhoAmI(ctx context.Context, p auth.Principal) (WhoAmIResult, error) {
	return WhoAmIResult{Username: p.User.Username, IsAgent: p.IsAgent, ActorLabel: p.ActorLabel()}, nil
}

func (s *stubBackend) ListUsers(ctx context.Context, p auth.Principal) ([]SelectableUser, error) {
	return []SelectableUser{{Username: "alice", Role: "user"}}, nil
}

func (s *stubBackend) ListTree(ctx context.Context, p auth.Principal, in ListTreeInput) (ListTreeResult, error) {
	return ListTreeResult{}, nil
}

func (s *stubBackend) SearchFiles(ctx context.Context, p auth.Principal, in SearchFilesInput) (SearchFilesResult, error) {
	return SearchFilesResult{}, nil
}

func (s *stubBackend) ReadFile(ctx context.Context, p auth.Principal, in ReadFileInput) (ReadFileResult, error) {
	return ReadFileResult{FileID: "f1", Path: in.Path, Version: 1, Content: "hello"}, nil
}

func (s *stubBackend) WriteFile(ctx context.Context, r *http.Request, p auth.Principal, in WriteFileInput) (FileSummary, error) {
	s.lastWritePath = in.Path
	if s.errToReturn != nil {
		return FileSummary{}, s.errToReturn
	}
	return FileSummary{ID: "f1", Path: in.Path, Version: 2}, nil
}

func (s *stubBackend) PatchFile(ctx context.Context, r *http.Request, p auth.Principal, in PatchFileInput) (FileSummary, error) {
	if s.errToReturn != nil {
		return FileSummary{}, s.errToReturn
	}
	return FileSummary{ID: "f1", Path: in.Path, Version: 3}, nil
}

func (s *stubBackend) BatchCommit(ctx context.Context, r *http.Request, p auth.Principal, in BatchCommitInput) (storage.BatchCommitResult, error) {
	return storage.BatchCommitResult{BatchID: "bat_1"}, nil
}

func (s *stubBackend) DeleteFile(ctx context.Context, r *http.Request, p auth.Principal, in DeleteFileInput) (DeleteFileResult, error) {
	return DeleteFileResult{OK: true, FileID: "f1", Path: in.Path}, nil
}

func (s *stubBackend) SetFileVisibility(ctx context.Context, r *http.Request, p auth.Principal, in SetVisibilityInput) (FileSummary, error) {
	return FileSummary{ID: "f1", Path: in.Path, AllowedUsers: in.AllowedUsers}, nil
}

func (s *stubBackend) AcquireLease(ctx context.Context, p auth.Principal, in AcquireLeaseInput) (storage.FileLease, error) {
	return storage.FileLease{FileID: "f1", HolderKeyID: "agk_a", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *stubBackend) ReleaseLease(ctx context.Context, p auth.Principal, in ReleaseLeaseInput) error {
	return nil
}

func (s *stubBackend) GetFileVersions(ctx context.Context, p auth.Principal, in FileLocatorInput) (FileVersionsResult, error) {
	return FileVersionsResult{FileID: "f1", CurrentVersion: 1}, nil
}

func (s *stubBackend) RestoreFileVersion(ctx context.Context, r *http.Request, p auth.Principal, in RestoreVersionInput) (FileSummary, error) {
	return FileSummary{ID: "f1", Version: 2}, nil
}

func (s *stubBackend) WaitForChanges(ctx context.Context, p auth.Principal, in WaitChangesInput) (WaitChangesResult, error) {
	return WaitChangesResult{CurrentVersion: 10, TimedOut: true}, nil
}

func (s *stubBackend) IssueTransferTicket(ctx context.Context, r *http.Request, p auth.Principal, in IssueTicketInput) (IssueTicketResult, error) {
	return IssueTicketResult{
		Action:         in.Action,
		NodeID:         "node-1",
		NodeName:       "Node1",
		EntryURL:       "http://127.0.0.1/s/ticket?p=a&s=b",
		RecommendedURL: "http://127.0.0.1/s/ticket?p=a&s=b",
		SingleUse:      in.Action == "up",
		Method:         http.MethodPut,
	}, nil
}

func (s *stubBackend) TriggerWorkspaceVerify(ctx context.Context, p auth.Principal, in TriggerVerifyInput) (bridge.VerifyResult, error) {
	return bridge.VerifyResult{OK: true, MountID: in.MountID, PresetID: in.PresetID}, nil
}

func postMCP(t *testing.T, tsURL, token, sessionID string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, tsURL+"/mcp", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do /mcp: %v", err)
	}
	return resp
}

func TestMCPProtocolSessionsBatchAndStructuredErrors(t *testing.T) {
	stub := &stubBackend{doneCh: make(chan struct{})}
	defer close(stub.doneCh)

	mux := http.NewServeMux()
	srv := NewServer(stub, "V0.11.24.0")
	mux.Handle("/mcp", srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Auth checks: missing token -> 401 with resource_metadata; rate limited -> 429.
	r401 := postMCP(t, ts.URL, "", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	r401.Body.Close()
	if r401.StatusCode != http.StatusUnauthorized || !strings.Contains(r401.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("expected 401 with WWW-Authenticate resource_metadata, got status=%d header=%q", r401.StatusCode, r401.Header.Get("WWW-Authenticate"))
	}
	r429 := postMCP(t, ts.URL, "limited", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	r429.Body.Close()
	if r429.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when rate limited, got %d", r429.StatusCode)
	}

	// 2. initialize -> returns Mcp-Session-Id header & protocolVersion negotiation.
	initResp := postMCP(t, ts.URL, "agent-a", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "test-client", "version": "1.0"},
		},
	})
	sid := initResp.Header.Get("Mcp-Session-Id")
	var initRPC JSONRPCResponse
	_ = json.NewDecoder(initResp.Body).Decode(&initRPC)
	initResp.Body.Close()
	if initResp.StatusCode != http.StatusOK || sid == "" || initRPC.Error != nil {
		t.Fatalf("initialize failed: status=%d sid=%q err=%#v", initResp.StatusCode, sid, initRPC.Error)
	}

	// Agent B cannot hijack Agent A's session ID -> 403 Forbidden.
	hijack := postMCP(t, ts.URL, "agent-b", sid, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"})
	hijack.Body.Close()
	if hijack.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when Agent B reuses Agent A session, got %d", hijack.StatusCode)
	}

	// Notification notifications/initialized -> 202 Accepted.
	notifResp := postMCP(t, ts.URL, "agent-a", sid, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	notifResp.Body.Close()
	if notifResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 on notification, got %d", notifResp.StatusCode)
	}

	// 3. Batch JSON-RPC request -> tools/list (all 15 tools) + ping.
	batchResp := postMCP(t, ts.URL, "agent-a", sid, []map[string]any{
		{"jsonrpc": "2.0", "id": 10, "method": "ping"},
		{"jsonrpc": "2.0", "id": 11, "method": "tools/list"},
	})
	var batchOut []JSONRPCResponse
	_ = json.NewDecoder(batchResp.Body).Decode(&batchOut)
	batchResp.Body.Close()
	if batchResp.StatusCode != http.StatusOK || len(batchOut) != 2 {
		t.Fatalf("unexpected batch response: status=%d len=%d", batchResp.StatusCode, len(batchOut))
	}
	resBytes, _ := json.Marshal(batchOut[1].Result)
	var listRes struct {
		Tools []Tool `json:"tools"`
	}
	_ = json.Unmarshal(resBytes, &listRes)
	if len(listRes.Tools) != 18 {
		t.Fatalf("expected 18 MCP tools, got %d", len(listRes.Tools))
	}

	// Report live status and confirm ListSessions captures client metadata and working state.
	repResp := postMCP(t, ts.URL, "agent-a", sid, map[string]any{
		"jsonrpc": "2.0",
		"id":      13,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "report_agent_status",
			"arguments": map[string]any{"state": "working", "task": "Implementing auth", "progress": "2/4"},
		},
	})
	repResp.Body.Close()
	if repResp.StatusCode != http.StatusOK {
		t.Fatalf("report_agent_status HTTP %d", repResp.StatusCode)
	}
	sessions := srv.ListSessions()
	var foundSess *SessionSnapshot
	for i := range sessions {
		if sessions[i].SessionID == sid {
			foundSess = &sessions[i]
			break
		}
	}
	if foundSess == nil || foundSess.ClientName != "test-client" || foundSess.ReportedTask != "Implementing auth" || foundSess.Status != "working" || foundSess.ToolCalls == 0 {
		t.Fatalf("unexpected live session snapshot: %+v", foundSess)
	}

	// Verify scope check on trigger_workspace_verify (agent-a lacks verify scope -> forbidden).
	verifyDeniedResp := postMCP(t, ts.URL, "agent-a", sid, map[string]any{
		"jsonrpc": "2.0",
		"id":      12,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "trigger_workspace_verify",
			"arguments": map[string]any{"mount_id": "mnt_1", "preset_id": "go-test"},
		},
	})
	var verifyDeniedRPC JSONRPCResponse
	_ = json.NewDecoder(verifyDeniedResp.Body).Decode(&verifyDeniedRPC)
	verifyDeniedResp.Body.Close()
	vrb, _ := json.Marshal(verifyDeniedRPC.Result)
	var verifyCallRes CallToolResult
	_ = json.Unmarshal(vrb, &verifyCallRes)
	if !verifyCallRes.IsError || len(verifyCallRes.Content) == 0 || !strings.Contains(verifyCallRes.Content[0].Text, `"code":"forbidden"`) {
		t.Fatalf("expected forbidden for trigger_workspace_verify without verify scope, got %+v", verifyCallRes)
	}

	// 4. Scope enforcement at tool dispatch: read-only Agent B cannot call write_file -> isError=true, code=forbidden.
	deniedResp := postMCP(t, ts.URL, "agent-b", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      20,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "shopapp/a.txt", "content": "hi"},
		},
	})
	var deniedRPC JSONRPCResponse
	_ = json.NewDecoder(deniedResp.Body).Decode(&deniedRPC)
	deniedResp.Body.Close()
	if deniedRPC.Error != nil {
		t.Fatalf("tool authorization error should return CallToolResult{IsError:true}, not JSON-RPC protocol error: %#v", deniedRPC.Error)
	}
	rb, _ := json.Marshal(deniedRPC.Result)
	var callRes CallToolResult
	_ = json.Unmarshal(rb, &callRes)
	if !callRes.IsError || !strings.Contains(callRes.Content[0].Text, `"code":"forbidden"`) {
		t.Fatalf("expected structured forbidden CallToolResult, got %+v", callRes)
	}

	// 5. Structured tool errors: ConflictError, LeaseLockedError, PatchError.
	checkStructuredErr := func(err error, wantCode string) {
		t.Helper()
		stub.errToReturn = err
		resp := postMCP(t, ts.URL, "agent-a", sid, map[string]any{
			"jsonrpc": "2.0",
			"id":      30,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "patch_file",
				"arguments": map[string]any{"path": "shopapp/main.go", "hunks": []map[string]any{{"old_text": "a", "new_text": "b"}}},
			},
		})
		var rpc JSONRPCResponse
		_ = json.NewDecoder(resp.Body).Decode(&rpc)
		resp.Body.Close()
		b, _ := json.Marshal(rpc.Result)
		var cr CallToolResult
		_ = json.Unmarshal(b, &cr)
		if !cr.IsError || len(cr.Content) == 0 || !strings.Contains(cr.Content[0].Text, `"code":"`+wantCode+`"`) {
			t.Fatalf("expected code=%q in %+v", wantCode, cr)
		}
	}
	checkStructuredErr(&storage.ConflictError{Path: "shopapp/main.go", ExpectedVersion: 1, CurrentVersion: 4}, "version_conflict")
	checkStructuredErr(&storage.LeaseLockedError{FileID: "f1", HolderKeyID: "agk_other", HolderName: "Agent · X", RemainingSec: 42}, "lease_locked")
	checkStructuredErr(&storage.PatchError{FileID: "f1", Reason: "not_found", HunkIndex: 0, Message: "missing"}, "patch_failed")
	stub.errToReturn = errors.New("boom")
	checkStructuredErr(stub.errToReturn, "tool_error")

	// 6. DELETE /mcp terminates session -> subsequent use returns 404.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/mcp", nil)
	delReq.Header.Set("Authorization", "Bearer agent-a")
	delReq.Header.Set("Mcp-Session-Id", sid)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /mcp: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 deleting session, got %d", delResp.StatusCode)
	}
	afterDel := postMCP(t, ts.URL, "agent-a", sid, map[string]any{"jsonrpc": "2.0", "id": 99, "method": "ping"})
	afterDel.Body.Close()
	if afterDel.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 using deleted session, got %d", afterDel.StatusCode)
	}

	// 7. DisconnectSession forcibly drops an active session.
	init2 := postMCP(t, ts.URL, "agent-a", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      100,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-03-26", "clientInfo": map[string]string{"name": "Cursor", "version": "0.49"}},
	})
	sid2 := init2.Header.Get("Mcp-Session-Id")
	init2.Body.Close()
	if sid2 == "" {
		t.Fatalf("expected second session id")
	}
	if _, ok := srv.DisconnectSession(sid2); !ok {
		t.Fatalf("expected DisconnectSession(%s) to succeed", sid2)
	}
	afterForce := postMCP(t, ts.URL, "agent-a", sid2, map[string]any{"jsonrpc": "2.0", "id": 101, "method": "ping"})
	afterForce.Body.Close()
	if afterForce.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after force disconnect, got %d", afterForce.StatusCode)
	}
}
