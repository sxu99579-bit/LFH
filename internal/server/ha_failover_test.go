package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

func startFixedAddrNode(t *testing.T, dir string, addr string, opts cluster.Options) (*Server, *cluster.Manager, *http.Server, context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	actualAddr := ln.Addr().String()
	_, portStr, _ := net.SplitHostPort(actualAddr)
	baseURL := "http://" + actualAddr

	authMgr, err := auth.NewManager(dir)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("auth.NewManager: %v", err)
	}
	store, err := storage.NewStore(dir)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("storage.NewStore: %v", err)
	}
	opts.DiscoveryPort = 0
	opts.DiscoveryPortSet = true
	opts.Port = portStr
	opts.PortSet = true
	opts.SelfURL = baseURL
	cm, err := cluster.NewManager(dir, store, opts)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("cluster.NewManager: %v", err)
	}
	cm.SetSelfURL(baseURL)
	cm.SetHATimingsForTest(300*time.Millisecond, 900*time.Millisecond, 150*time.Millisecond, 250*time.Millisecond, 150*time.Millisecond)

	srv, err := New(authMgr, store, cm, testEmbedFS, portStr)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cm.Start(ctx)

	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	t.Cleanup(func() {
		cancel()
		_ = httpSrv.Close()
		srv.CloseEventStreams()
	})
	return srv, cm, httpSrv, cancel
}

func TestClusterHAFailoverHotStandbyAndPrimaryFailback(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	dirC := t.TempDir()

	srvA, mgrA, httpA, cancelA := startFixedAddrNode(t, dirA, "127.0.0.1:0", cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Primary-A",
		QuotaGB:  10,
		QuotaSet: true,
	})
	urlA := mgrA.EntryURL()

	srvB, mgrB, _, _ := startFixedAddrNode(t, dirB, "127.0.0.1:0", cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Standby-B",
		QuotaGB:  20,
		QuotaSet: true,
	})
	urlB := mgrB.EntryURL()

	_, mgrC, _, _ := startFixedAddrNode(t, dirC, "127.0.0.1:0", cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Standby-C",
		QuotaGB:  15,
		QuotaSet: true,
	})
	urlC := mgrC.EntryURL()

	// Join B & C into A's cluster
	if err := mgrB.JoinEntryCluster(urlA, mgrA.Secret(), 0); err != nil {
		t.Fatalf("B join: %v", err)
	}
	if err := mgrC.JoinEntryCluster(urlA, mgrA.Secret(), 0); err != nil {
		t.Fatalf("C join: %v", err)
	}

	// Admin on A pins B as preferred standby so B takes over first deterministically
	if err := mgrA.SetHAConfig(true, mgrB.NodeID()); err != nil {
		t.Fatalf("SetHAConfig: %v", err)
	}

	// Seed Governance user, Agent Key, and AI Task on Primary A before outage
	if _, _, err := srvA.auth.AdminCreateUser(auth.AdminCreateUserInput{
		Username: "ha_user",
		Password: "ha_password1",
		Role:     auth.RoleUser,
	}); err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}
	agentToken, _, err := srvA.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		OwnerUsername: "ha_user",
		Name:          "HA-Bot",
		Scopes:        []string{auth.ScopeRead, auth.ScopeWrite},
	})
	if err != nil {
		t.Fatalf("CreateAgentKey: %v", err)
	}
	taskA, err := srvA.getTasks().CreateTask(bridge.TaskActor{Username: "ha_user", Role: "user"}, bridge.CreateTaskInput{
		Title:    "Pre-Failover Task",
		Priority: "high",
	})
	if err != nil {
		t.Fatalf("CreateTask on A: %v", err)
	}

	// Force immediate heartbeats so B & C pull hot-standby auth + task snapshots and updated topology
	if err := mgrB.SendHeartbeatNow(); err != nil {
		t.Fatalf("B heartbeat: %v", err)
	}
	if err := mgrC.SendHeartbeatNow(); err != nil {
		t.Fatalf("C heartbeat: %v", err)
	}

	// Verify /api/client/topology exposes active entry, home primary, and node table
	topoResp, err := http.Get(urlA + "/api/client/topology")
	if err != nil {
		t.Fatalf("GET /api/client/topology: %v", err)
	}
	var topo cluster.ClientTopology
	_ = json.NewDecoder(topoResp.Body).Decode(&topo)
	_ = topoResp.Body.Close()
	if topo.ClusterID == "" || len(topo.ActiveEntryURLs) == 0 || len(topo.Nodes) != 3 || len(topo.StandbyOrder) < 1 || topo.StandbyOrder[0] != mgrB.NodeID() {
		t.Fatalf("unexpected topology: %+v", topo)
	}

	// Verify B already holds the hot-standby AgentKey & Task before A goes down
	if _, authErr := srvB.auth.AuthenticateAgentToken(agentToken, "127.0.0.1"); authErr != nil {
		t.Fatalf("expected Standby B to have hot-replicated agentToken before failover: %v", authErr)
	}
	if _, err := srvB.getTasks().GetTask(bridge.TaskActor{Username: "ha_user", Role: "user"}, taskA.ID); err != nil {
		t.Fatalf("expected Standby B to have replicated taskA: %v", err)
	}

	// Shut down Primary A cleanly to simulate power outage
	cancelA()
	_ = httpA.Close()
	srvA.CloseEventStreams()

	// Wait for Worker B to auto-promote to Temporary Failover Entry and Worker C to repoint to B
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if mgrB.Role() == cluster.RoleEntry && mgrB.FailoverActive() && mgrC.Role() == cluster.RoleWorker && strings.TrimRight(mgrC.EntryURL(), "/") == strings.TrimRight(urlB, "/") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if mgrB.Role() != cluster.RoleEntry || !mgrB.FailoverActive() {
		t.Fatalf("expected B auto-promoted to Failover Entry, got role=%s failover=%v", mgrB.Role(), mgrB.FailoverActive())
	}
	if mgrC.Role() != cluster.RoleWorker || strings.TrimRight(mgrC.EntryURL(), "/") != strings.TrimRight(urlB, "/") {
		t.Fatalf("expected C repointed to B (%s), got role=%s entry=%s", urlB, mgrC.Role(), mgrC.EntryURL())
	}

	// Subscribe to SSE on Failover B BEFORE A restarts to verify primary_restored broadcast!
	evtCh, unsub := srvB.events.Subscribe()
	defer unsub()

	// During outage, Agent creates a new task directly on Failover Entry B!
	taskBReq, _ := json.Marshal(map[string]any{
		"title":       "Task Created During Failover",
		"description": "must survive failback to Primary A",
		"priority":    "urgent",
	})
	_ = urlC
	// Authenticate via human cookie on B for task creation API
	cookieB := newCookieClient(t)
	loginBody, _ := json.Marshal(map[string]string{"username": "ha_user", "password": "ha_password1"})
	loginRes, err := cookieB.Post(urlB+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil || loginRes.StatusCode != http.StatusOK {
		t.Fatalf("login on Failover B failed: err=%v status=%v", err, loginRes)
	}
	_ = loginRes.Body.Close()

	taskCreateRes, err := cookieB.Post(urlB+"/api/agent-tasks", "application/json", bytes.NewReader(taskBReq))
	if err != nil || taskCreateRes.StatusCode >= 300 {
		t.Fatalf("create task on Failover B failed: err=%v res=%v", err, taskCreateRes)
	}
	_ = taskCreateRes.Body.Close()

	// Verify AgentKey works on Failover B /api/agent-tasks/poll
	pollReq, _ := http.NewRequest(http.MethodGet, urlB+"/api/agent-tasks/poll", nil)
	pollReq.Header.Set("Authorization", "Bearer "+agentToken)
	pollRes, err := http.DefaultClient.Do(pollReq)
	if err != nil || pollRes.StatusCode != http.StatusOK {
		t.Fatalf("agent poll on Failover B failed: err=%v status=%v", err, pollRes)
	}
	_ = pollRes.Body.Close()

	// Bring Primary A back online on the exact same address!
	addrA := strings.TrimPrefix(urlA, "http://")
	srvARestored, mgrARestored, _, _ := startFixedAddrNode(t, dirA, addrA, cluster.Options{
		Role:     cluster.RoleEntry,
		NodeName: "Primary-A",
		QuotaGB:  10,
		QuotaSet: true,
	})

	// Wait for Failover B to detect A's stable recovery, sync state back, broadcast primary_restored, and step down to Worker
	gotSwitchbackEvent := false
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-evtCh:
			if ev.Kind == "entry_switched" && ev.Reason == "primary_restored" && strings.TrimRight(ev.EntryURL, "/") == strings.TrimRight(urlA, "/") {
				gotSwitchbackEvent = true
			}
		default:
		}
		if gotSwitchbackEvent && mgrB.Role() == cluster.RoleWorker && mgrARestored.Role() == cluster.RoleEntry && !mgrARestored.FailoverActive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !gotSwitchbackEvent {
		t.Fatalf("expected SSE entry_switched reason=primary_restored pointing to %s", urlA)
	}
	if mgrB.Role() != cluster.RoleWorker || mgrB.FailoverActive() {
		t.Fatalf("expected B stepped back down to Worker after failback, got role=%s failover=%v", mgrB.Role(), mgrB.FailoverActive())
	}
	if mgrARestored.Role() != cluster.RoleEntry || mgrARestored.FailoverActive() {
		t.Fatalf("expected restored A back as Entry, got role=%s failover=%v", mgrARestored.Role(), mgrARestored.FailoverActive())
	}

	// Verify the task created on B during failover survived failback on restored Primary A!
	listAfter := srvARestored.getTasks().ListTasks(bridge.TaskActor{Username: "ha_user", Role: "user"}, bridge.TaskListFilter{})
	foundTaskB := false
	for _, tk := range listAfter.Tasks {
		if tk.Title == "Task Created During Failover" {
			foundTaskB = true
			break
		}
	}
	if !foundTaskB {
		t.Fatalf("expected task created during failover on B to be preserved on restored Primary A; got=%+v", listAfter.Tasks)
	}
}
