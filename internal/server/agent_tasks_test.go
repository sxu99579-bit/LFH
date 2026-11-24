package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
)

func callTaskToolURL(t *testing.T, baseURL, token, toolName string, args map[string]any) (bool, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest %s: %v", toolName, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do /mcp %s: %v", toolName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP %s HTTP %d", toolName, resp.StatusCode)
	}
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode MCP %s: %v", toolName, err)
	}
	if rpc.Error != nil {
		t.Fatalf("MCP %s protocol error: %+v", toolName, rpc.Error)
	}
	txt := ""
	if len(rpc.Result.Content) > 0 {
		txt = rpc.Result.Content[0].Text
	}
	return rpc.Result.IsError, txt
}

func TestAgentKeyAvatarAndTaskBoardEndToEnd(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)
	adminUser, _, _ := srv.auth.InitialAdminCredentials()

	tokA, keyA, err := srv.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:          "LAPTOP-antigravity",
		OwnerUsername: adminUser,
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite},
		ReadFolders:   []string{"*"},
		WriteFolders:  []string{"*"},
		AvatarID:      "shiba",
		DisplayAlias:  "小柴",
		DeviceAlias:   "主力笔记本",
		DeviceKind:    "laptop",
	})
	if err != nil {
		t.Fatalf("CreateAgentKey A: %v", err)
	}
	tokB, keyB, err := srv.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:          "DESKTOP-cursor",
		OwnerUsername: adminUser,
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite},
		ReadFolders:   []string{"*"},
		WriteFolders:  []string{"*"},
		AvatarID:      "bluecat",
		DisplayAlias:  "蓝猫",
		DeviceAlias:   "家里台式机",
		DeviceKind:    "desktop",
	})
	if err != nil {
		t.Fatalf("CreateAgentKey B: %v", err)
	}
	if keyA.AvatarID != "shiba" || keyA.DisplayAlias != "小柴" || keyB.DeviceKind != "desktop" {
		t.Fatalf("unexpected cartoon metadata: keyA=%+v keyB=%+v", keyA, keyB)
	}

	humanDo := func(method, target string, payload any) (int, []byte) {
		t.Helper()
		var rdr *bytes.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, ts.URL+target, rdr)
		if err != nil {
			t.Fatalf("NewRequest %s %s: %v", method, target, err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := adminClient.Do(req)
		if err != nil {
			t.Fatalf("Do %s %s: %v", method, target, err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.Bytes()
	}

	// 1. Update keyA cartoon badge via PATCH /api/agent-keys/{id}.
	upStatus, upBody := humanDo(http.MethodPatch, "/api/agent-keys/"+keyA.ID, map[string]any{
		"avatar_id":     "fox",
		"display_alias": "小柴",
	})
	if upStatus != http.StatusOK || !strings.Contains(string(upBody), `"avatar_id":"fox"`) {
		t.Fatalf("PATCH agent-key avatar failed: %d %s", upStatus, string(upBody))
	}

	// 2. Verify whoami exposes cartoon metadata + tools/list has 25 tools.
	isErr, whoTxt := callTaskToolURL(t, ts.URL, tokA, "whoami", map[string]any{})
	if isErr || !strings.Contains(whoTxt, `"avatar_id":"fox"`) || !strings.Contains(whoTxt, `"display_alias":"小柴"`) {
		t.Fatalf("whoami missing cartoon fields: err=%v body=%s", isErr, whoTxt)
	}

	listToolsBody, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/list"})
	ltReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(listToolsBody))
	ltReq.Header.Set("Authorization", "Bearer "+tokA)
	ltReq.Header.Set("Content-Type", "application/json")
	ltResp, err := http.DefaultClient.Do(ltReq)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var ltRPC struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.NewDecoder(ltResp.Body).Decode(&ltRPC)
	ltResp.Body.Close()
	if len(ltRPC.Result.Tools) != 25 {
		t.Fatalf("expected 25 MCP tools over /mcp, got %d", len(ltRPC.Result.Tools))
	}

	// 3. Create targeted task mentioning @小柴 in title without explicit assigned_agent -> auto-locks to keyA.
	createStatus, createBody := humanDo(http.MethodPost, "/api/agent-tasks", map[string]any{
		"title":       "@小柴 批量核对扫描件",
		"description": "请仅小柴执行",
		"priority":    "high",
	})
	if createStatus != http.StatusCreated {
		t.Fatalf("POST /api/agent-tasks status=%d body=%s", createStatus, string(createBody))
	}
	var createOut struct {
		Task bridge.AgentTask `json:"task"`
	}
	_ = json.Unmarshal(createBody, &createOut)
	if createOut.Task.AssignedAgent != keyA.ID {
		t.Fatalf("expected @小柴 auto-resolved to %s, got %q", keyA.ID, createOut.Task.AssignedAgent)
	}

	// Agent B attempting to claim @小柴's task -> rejected with assigned_to_other.
	isErr, denyTxt := callTaskToolURL(t, ts.URL, tokB, "claim_task", map[string]any{"task_id": createOut.Task.ID})
	if !isErr || !strings.Contains(denyTxt, `"code":"assigned_to_other"`) {
		t.Fatalf("expected assigned_to_other for Agent B, got err=%v txt=%s", isErr, denyTxt)
	}

	// 4. Create open collaborative task & wake long-poll watcher.
	pollDone := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agent-tasks/poll?wait_sec=5", nil)
		req.Header.Set("Authorization", "Bearer "+tokB)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			pollDone <- "ERR:" + err.Error()
			return
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		pollDone <- buf.String()
	}()
	time.Sleep(100 * time.Millisecond)

	openStatus, openBody := humanDo(http.MethodPost, "/api/agent-tasks", map[string]any{
		"title": "复杂周报协同任务",
	})
	if openStatus != http.StatusCreated {
		t.Fatalf("POST open task: %d %s", openStatus, string(openBody))
	}
	var openOut struct {
		Task bridge.AgentTask `json:"task"`
	}
	_ = json.Unmarshal(openBody, &openOut)

	select {
	case pollBody := <-pollDone:
		if !strings.Contains(pollBody, `"has_work":true`) || !strings.Contains(pollBody, openOut.Task.ID) {
			t.Fatalf("unexpected poll wake payload: %s", pollBody)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("long-poll did not wake up when new task created")
	}

	// 5. Agent A claims & splits the open task, pinging @蓝猫; Agent B claims sub_2 & both complete -> parent done.
	isErr, splitTxt := callTaskToolURL(t, ts.URL, tokA, "split_task", map[string]any{
		"task_id": openOut.Task.ID,
		"note":    "@蓝猫 请接手第 2 块，汇总等我们俩完工解锁",
		"subtasks": []map[string]any{
			{"title": "第一块"},
			{"title": "第二块"},
			{"title": "汇总校验", "depends_on": []int{1, 2}},
		},
	})
	if isErr || !strings.Contains(splitTxt, `"status":"in_progress"`) {
		t.Fatalf("split_task failed: err=%v txt=%s", isErr, splitTxt)
	}

	isErr, claim2Txt := callTaskToolURL(t, ts.URL, tokB, "claim_task", map[string]any{
		"task_id":    openOut.Task.ID,
		"subtask_id": "sub_2",
	})
	if isErr || !strings.Contains(claim2Txt, `"claimed_by_id":"`+keyB.ID+`"`) {
		t.Fatalf("claim sub_2 failed: err=%v txt=%s", isErr, claim2Txt)
	}

	if errBool, txt := callTaskToolURL(t, ts.URL, tokA, "complete_task", map[string]any{
		"task_id":      openOut.Task.ID,
		"subtask_id":   "sub_1",
		"output_files": []string{"part1.md"},
	}); errBool {
		t.Fatalf("complete sub_1 failed: %s", txt)
	}
	if errBool, txt := callTaskToolURL(t, ts.URL, tokB, "complete_task", map[string]any{
		"task_id":      openOut.Task.ID,
		"subtask_id":   "sub_2",
		"output_files": []string{"part2.md"},
	}); errBool {
		t.Fatalf("complete sub_2 failed: %s", txt)
	}
	if errBool, txt := callTaskToolURL(t, ts.URL, tokB, "claim_task", map[string]any{
		"task_id":    openOut.Task.ID,
		"subtask_id": "sub_3",
	}); errBool {
		t.Fatalf("claim sub_3 failed: %s", txt)
	}
	isErr, doneTxt := callTaskToolURL(t, ts.URL, tokB, "complete_task", map[string]any{
		"task_id":        openOut.Task.ID,
		"subtask_id":     "sub_3",
		"output_files":   []string{"final.md"},
		"result_summary": "协同汇总完成",
	})
	if isErr || !strings.Contains(doneTxt, `"status":"done"`) {
		t.Fatalf("expected parent task done after sub_3: err=%v txt=%s", isErr, doneTxt)
	}
}
