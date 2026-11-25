package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lfh/internal/plugins"
	"lfh/internal/storage"
)

func TestParseQuotaBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"500MB", 500 * MB},
		{"500 m", 500 * MB},
		{"2.5GB", int64(2.5 * float64(GB))},
		{"1TB", 1 * TB},
		{"10", 10 * GB},
		{"1024KB", 1024 * KB},
	}
	for _, tc := range cases {
		got, err := ParseQuotaBytes(tc.in)
		if err != nil {
			t.Fatalf("ParseQuotaBytes(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseQuotaBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	for _, bad := range []string{"", "0", "-5GB", "abc", "200TB"} {
		if _, err := ParseQuotaBytes(bad); err == nil {
			t.Fatalf("expected ParseQuotaBytes(%q) to fail", bad)
		}
	}
}

func TestSignAndVerifyRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/internal/cluster/heartbeat", nil)
	SignRequest(req, "super-secret", "node-abc")

	if !VerifyRequest(req, "super-secret") {
		t.Fatalf("expected valid signature to verify")
	}
	if VerifyRequest(req, "wrong-secret") {
		t.Fatalf("expected wrong secret to fail verification")
	}

	reqTampered := httptest.NewRequest(http.MethodDelete, "/internal/cluster/heartbeat", nil)
	reqTampered.Header = req.Header.Clone()
	if VerifyRequest(reqTampered, "super-secret") {
		t.Fatalf("expected method tampering to fail verification")
	}
}

func TestVerifyRequestAntiReplayNonce(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/internal/cluster/clipboard", nil)
	SignRequest(req, "cluster-sec", "node-1")
	if req.Header.Get(hdrNonce) == "" {
		t.Fatal("expected SignRequest to attach X-LFH-Nonce")
	}
	if !VerifyRequest(req, "cluster-sec") {
		t.Fatal("first verification with valid nonce should succeed")
	}
	if VerifyRequest(req, "cluster-sec") {
		t.Fatal("replayed request with identical nonce must be rejected")
	}

	// Legacy peer without X-LFH-Nonce remains accepted within time window.
	legacyReq := httptest.NewRequest(http.MethodGet, "/internal/cluster/mounts", nil)
	nowStr := strconv.FormatInt(time.Now().Unix(), 10)
	legacyReq.Header.Set(hdrNodeID, "legacy-node")
	legacyReq.Header.Set(hdrTimestamp, nowStr)
	legacyReq.Header.Set(hdrSignature, computeSignature("cluster-sec", http.MethodGet, "/internal/cluster/mounts", nowStr, "legacy-node", ""))
	if !VerifyRequest(legacyReq, "cluster-sec") {
		t.Fatal("legacy signed request without nonce should remain accepted")
	}
}

func TestSeedConfigDistinctNodeIDAndQuotaPersistence(t *testing.T) {
	seedDir := t.TempDir()
	seedPath := filepath.Join(seedDir, "cluster-config.json")
	seedJSON := `{
  "role": "worker",
  "cluster_secret": "shared-secret-1234",
  "quota_gb": 10,
  "entry_hint": "http://192.168.3.53:8080",
  "discovery_port": 0
}`
	if err := os.WriteFile(seedPath, []byte(seedJSON), 0600); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	d1 := t.TempDir()
	s1, err := storage.NewStore(d1)
	if err != nil {
		t.Fatalf("NewStore 1: %v", err)
	}
	m1, err := NewManager(d1, s1, Options{ConfigSeedPath: seedPath, SelfURL: "http://192.168.3.54:8080"})
	if err != nil {
		t.Fatalf("NewManager 1: %v", err)
	}

	d2 := t.TempDir()
	s2, err := storage.NewStore(d2)
	if err != nil {
		t.Fatalf("NewStore 2: %v", err)
	}
	m2, err := NewManager(d2, s2, Options{ConfigSeedPath: seedPath, SelfURL: "http://192.168.3.55:8080"})
	if err != nil {
		t.Fatalf("NewManager 2: %v", err)
	}

	if m1.NodeID() == "" || m2.NodeID() == "" || m1.NodeID() == m2.NodeID() {
		t.Fatalf("expected distinct generated node IDs, got %q and %q", m1.NodeID(), m2.NodeID())
	}
	if m1.Secret() != "shared-secret-1234" || m2.Secret() != "shared-secret-1234" {
		t.Fatalf("expected shared secret from seed")
	}
	if m1.Role() != RoleWorker || m1.EntryURL() != "http://192.168.3.53:8080" {
		t.Fatalf("unexpected role/entryURL: role=%s entry=%s", m1.Role(), m1.EntryURL())
	}

	// Test MB-level quota persistence and endpoint persistence.
	if _, err := m1.SetLocalQuotaBytes(500 * MB); err != nil {
		t.Fatalf("SetLocalQuotaBytes: %v", err)
	}
	if err := m1.SetEndpointConfig("100.120.53.119", "9090", "http://100.120.53.119:9090"); err != nil {
		t.Fatalf("SetEndpointConfig: %v", err)
	}

	m1Reloaded, err := NewManager(d1, s1, Options{SelfURL: "http://100.120.53.119:9090"})
	if err != nil {
		t.Fatalf("reload NewManager 1: %v", err)
	}
	if m1Reloaded.SelfStatus().QuotaBytes != 500*MB {
		t.Fatalf("expected persisted quota 500 MB (%d), got %d", 500*MB, m1Reloaded.SelfStatus().QuotaBytes)
	}
	if m1Reloaded.ExposeIP() != "100.120.53.119" || m1Reloaded.Port() != "9090" {
		t.Fatalf("expected persisted endpoint 100.120.53.119:9090, got %s:%s", m1Reloaded.ExposeIP(), m1Reloaded.Port())
	}
}

func TestLegacyQuotaGBMigration(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	legacy := `{
  "node_id": "node-legacy",
  "node_name": "OLD-PC",
  "role": "entry",
  "cluster_secret": "abc123",
  "quota_gb": 15,
  "discovery_port": 0
}`
	if err := os.WriteFile(filepath.Join(dir, "cluster.json"), []byte(legacy), 0600); err != nil {
		t.Fatalf("write legacy cluster.json: %v", err)
	}
	m, err := NewManager(dir, st, Options{SelfURL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("NewManager legacy: %v", err)
	}
	if m.SelfStatus().QuotaBytes != 15*GB || m.Port() != "8080" {
		t.Fatalf("unexpected migrated quota/port: bytes=%d port=%s", m.SelfStatus().QuotaBytes, m.Port())
	}
}

func TestSelectUploadTargetLargestFreeAndInsufficientQuota(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	mgr, err := NewManager(dir, st, Options{
		Role:             RoleEntry,
		QuotaBytes:       5 * GB,
		QuotaSet:         true,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://192.168.3.53:8080",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Register a peer with 20 GB free (larger than self 5 GB).
	mgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:     "node-big",
			NodeName:   "Linux-NAS",
			Role:       RoleWorker,
			OS:         "linux",
			HTTPURL:    "http://192.168.3.99:8080",
			QuotaGB:    20,
			QuotaBytes: 20 * GB,
			UsedBytes:  0,
			FreeBytes:  20 * GB,
			Online:     true,
		},
	})

	target, err := mgr.SelectUploadTarget("auto", 1024)
	if err != nil {
		t.Fatalf("SelectUploadTarget auto: %v", err)
	}
	if target.NodeID != "node-big" {
		t.Fatalf("expected largest free node 'node-big', got %s", target.NodeID)
	}

	if _, err := mgr.SelectUploadTarget("auto", 30*GB); err == nil || !strings.Contains(err.Error(), "预留空间不足") {
		t.Fatalf("expected insufficient quota error, got %v", err)
	}
}

func TestStorageDirRoleFilteringAndDrives(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := st.SaveUpload("hello.txt", "alice", storage.DeviceInfo{}, strings.NewReader("hello")); err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}

	mgr, err := NewManager(dir, st, Options{
		Role:             RoleEntry,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	adminPool := mgr.PoolSummaryForRole(true)
	userPool := mgr.PoolSummaryForRole(false)
	if adminPool.Nodes[0].StorageDir == "" {
		t.Fatalf("expected admin pool to expose storage_dir")
	}
	if userPool.Nodes[0].StorageDir != "" || len(userPool.Nodes[0].Drives) != 0 {
		t.Fatalf("expected non-admin pool to hide storage_dir and drives, got %+v", userPool.Nodes[0])
	}

	customDir := filepath.Join(t.TempDir(), "LFH-Custom")
	updated, moved, err := mgr.SetLocalStorageDir(customDir, true)
	if err != nil || moved != 1 || updated.StorageDir != customDir {
		t.Fatalf("SetLocalStorageDir: moved=%d err=%v node=%+v", moved, err, updated)
	}
	reloaded, err := NewManager(dir, st, Options{
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	if reloaded.StorageDir() != customDir {
		t.Fatalf("expected persisted storage_dir=%q, got %q", customDir, reloaded.StorageDir())
	}
}

func TestOfflinePeerFilesHiddenNormalizeProbeAndRemove(t *testing.T) {
	u1, err := NormalizePeerURL("192.168.3.88", "8080")
	if err != nil || u1 != "http://192.168.3.88:8080" {
		t.Fatalf("NormalizePeerURL bare ip: u=%q err=%v", u1, err)
	}
	u2, err := NormalizePeerURL("https://10.0.0.5:9443/", "8080")
	if err != nil || u2 != "https://10.0.0.5:9443" {
		t.Fatalf("NormalizePeerURL https: u=%q err=%v", u2, err)
	}
	if _, err := NormalizePeerURL("   ", "8080"); err == nil {
		t.Fatalf("expected empty URL error")
	}

	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	mgr, err := NewManager(dir, st, Options{
		Role:             RoleEntry,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Register an online peer carrying 1 file.
	changed := mgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:     "node-peer-1",
			NodeName:   "Peer-One",
			Role:       RoleWorker,
			OS:         "linux",
			HTTPURL:    "http://192.168.3.90:8080",
			QuotaBytes: 10 * GB,
			FreeBytes:  10 * GB,
			FileCount:  1,
		},
		Files: []storage.FileRecord{
			{ID: "f-peer-1", Name: "peer-doc.txt", Size: 128, UploadedAt: time.Now()},
		},
	})
	if !changed {
		t.Fatalf("expected first heartbeat changed=true")
	}
	if files := mgr.AllClusterFiles(); len(files) != 1 || files[0].ID != "f-peer-1" {
		t.Fatalf("expected online peer file visible, got %+v", files)
	}
	// Online peer cannot be removed.
	if err := mgr.RemovePeer("node-peer-1"); err == nil {
		t.Fatalf("expected error removing online peer")
	}

	// Expire peer -> AllClusterFiles hides peer's files, PoolSummary marks offline, RemovePeer works.
	mgr.SetPeerLastSeenForTest("node-peer-1", time.Now().Add(-30*time.Second))
	if files := mgr.AllClusterFiles(); len(files) != 0 {
		t.Fatalf("expected offline peer files hidden, got %+v", files)
	}
	pool := mgr.PoolSummary()
	if len(pool.Nodes) != 2 || pool.Nodes[1].Online || pool.OnlineNodes != 1 {
		t.Fatalf("expected peer listed offline in PoolSummary, got %+v", pool)
	}

	// ProbeNodeURL probes an HTTP /api/info endpoint cleanly.
	mockTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/info" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app_name": "QuailNest",
			"version":  "V1.11.25.1",
			"role":     "entry",
			"pool": PoolSummary{
				Nodes: []NodeStatus{{
					NodeID:     "node-mock-2",
					NodeName:   "Mock-PC",
					Role:       "entry",
					OS:         "windows",
					Arch:       "amd64",
					QuotaBytes: 20 * GB,
					IsSelf:     true,
				}},
			},
		})
	}))
	defer mockTS.Close()

	dn, err := mgr.ProbeNodeURL(context.Background(), strings.TrimPrefix(mockTS.URL, "http://"), "manual")
	if err != nil || dn.NodeID != "node-mock-2" || dn.NodeName != "Mock-PC" || dn.AlreadyInPool {
		t.Fatalf("ProbeNodeURL failed: dn=%+v err=%v", dn, err)
	}

	if err := mgr.RemovePeer("node-peer-1"); err != nil {
		t.Fatalf("RemovePeer offline peer: %v", err)
	}
	if len(mgr.PoolSummary().Nodes) != 1 {
		t.Fatalf("expected 1 node after RemovePeer")
	}
}

func TestDirectClusterClientIgnoresHTTPProxy(t *testing.T) {
	// Point HTTP_PROXY / HTTPS_PROXY at an unreachable blackhole port; direct LAN clients must still succeed.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	secret := "proxy-bypass-secret"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"app_name": "LFH",
				"version":  "V0.11.21.4",
				"role":     "entry",
				"pool": PoolSummary{
					Nodes: []NodeStatus{{
						NodeID:   "node-entry-direct",
						NodeName: "Entry-Direct",
						Role:     "entry",
						IsSelf:   true,
					}},
				},
			})
		case "/internal/cluster/heartbeat":
			if !VerifyRequest(r, secret) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	mgr, err := NewManager(dir, st, Options{
		Role:             RoleWorker,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:8081",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	dn, err := mgr.ProbeNodeURL(context.Background(), ts.URL, "manual")
	if err != nil || dn.NodeID != "node-entry-direct" {
		t.Fatalf("ProbeNodeURL failed with HTTP_PROXY set: dn=%+v err=%v", dn, err)
	}
	if err := mgr.SendTestHeartbeat(ts.URL, secret); err != nil {
		t.Fatalf("SendTestHeartbeat failed with HTTP_PROXY set: %v", err)
	}
}

func TestLeaveClusterRotatesSecretAndClearsPeers(t *testing.T) {
	oldSecret := "shared-cluster-secret-01"

	workerDir := t.TempDir()
	workerStore, err := storage.NewStore(workerDir)
	if err != nil {
		t.Fatalf("worker NewStore: %v", err)
	}
	workerMgr, err := NewManager(workerDir, workerStore, Options{
		Role:             RoleWorker,
		NodeName:         "worker-node",
		ClusterSecret:    oldSecret,
		EntryHint:        "http://127.0.0.1:18080",
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:18081",
	})
	if err != nil {
		t.Fatalf("worker NewManager: %v", err)
	}
	if workerMgr.Role() != RoleWorker || workerMgr.EntryURL() == "" {
		t.Fatalf("expected initial worker role & entry URL, got role=%s entry=%s", workerMgr.Role(), workerMgr.EntryURL())
	}

	self, err := workerMgr.LeaveCluster()
	if err != nil {
		t.Fatalf("LeaveCluster: %v", err)
	}
	if self.Role != RoleEntry || workerMgr.Role() != RoleEntry {
		t.Fatalf("expected standalone entry role after LeaveCluster, got %s", workerMgr.Role())
	}
	if workerMgr.EntryURL() == "http://127.0.0.1:18080" && workerMgr.Role() == RoleWorker {
		t.Fatalf("expected entry hint cleared after LeaveCluster")
	}
	if workerMgr.Secret() == "" || workerMgr.Secret() == oldSecret {
		t.Fatalf("expected rotated secret after LeaveCluster, got %q", workerMgr.Secret())
	}

	reloaded, err := NewManager(workerDir, workerStore, Options{
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
	})
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	if reloaded.Role() != RoleEntry || reloaded.Secret() != workerMgr.Secret() {
		t.Fatalf("reloaded config mismatch: role=%s secret=%s", reloaded.Role(), reloaded.Secret())
	}
}

func TestEvictPeerOnlineAndOffline(t *testing.T) {
	secret := "evict-test-secret-01"
	leaveCalls := 0
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/internal/cluster/leave" {
			if !VerifyRequest(r, secret) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			leaveCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer peerSrv.Close()

	entryDir := t.TempDir()
	entryStore, err := storage.NewStore(entryDir)
	if err != nil {
		t.Fatalf("entry NewStore: %v", err)
	}
	entryMgr, err := NewManager(entryDir, entryStore, Options{
		Role:             RoleEntry,
		NodeName:         "entry-node",
		ClusterSecret:    secret,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:18090",
	})
	if err != nil {
		t.Fatalf("entry NewManager: %v", err)
	}

	entryMgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:   "online-peer",
			NodeName: "Online-Worker",
			Role:     RoleWorker,
			HTTPURL:  peerSrv.URL,
		},
	})
	entryMgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:   "offline-peer",
			NodeName: "Offline-Worker",
			Role:     RoleWorker,
			HTTPURL:  "http://127.0.0.1:1",
		},
	})
	entryMgr.SetPeerLastSeenForTest("offline-peer", time.Now().Add(-30*time.Second))

	if got := len(entryMgr.PoolSummary().Nodes); got != 3 {
		t.Fatalf("expected 3 nodes before evict, got %d", got)
	}

	notified, name, err := entryMgr.EvictPeer("online-peer")
	if err != nil || !notified || name != "Online-Worker" || leaveCalls != 1 {
		t.Fatalf("EvictPeer(online) mismatch: notified=%v name=%q leaveCalls=%d err=%v", notified, name, leaveCalls, err)
	}

	notifiedOff, nameOff, err := entryMgr.EvictPeer("offline-peer")
	if err != nil || notifiedOff || nameOff != "Offline-Worker" || leaveCalls != 1 {
		t.Fatalf("EvictPeer(offline) mismatch: notified=%v name=%q leaveCalls=%d err=%v", notifiedOff, nameOff, leaveCalls, err)
	}

	if got := len(entryMgr.PoolSummary().Nodes); got != 1 {
		t.Fatalf("expected only self remaining after evicting both peers, got %d", got)
	}
	if _, _, err := entryMgr.EvictPeer(entryMgr.NodeID()); err == nil {
		t.Fatalf("expected error evicting self node")
	}
}

func TestLegacyPeerPromoteAndEvictFallbackOn404(t *testing.T) {
	secret := "legacy-compat-secret"
	// Legacy V0.11.21.4 peer returns 404 on /internal/cluster/promote and /internal/cluster/leave.
	legacySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer legacySrv.Close()

	entryDir := t.TempDir()
	entryStore, err := storage.NewStore(entryDir)
	if err != nil {
		t.Fatalf("entry NewStore: %v", err)
	}
	entryMgr, err := NewManager(entryDir, entryStore, Options{
		Role:             RoleEntry,
		NodeName:         "Entry-Laptop",
		ClusterSecret:    secret,
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:18080",
	})
	if err != nil {
		t.Fatalf("entry NewManager: %v", err)
	}

	hb := HeartbeatPayload{
		Node: NodeStatus{
			NodeID:     "legacy-peer",
			NodeName:   "DESKTOP-LEGACY",
			Role:       RoleWorker,
			HTTPURL:    legacySrv.URL,
			QuotaBytes: 50 * GB,
		},
	}
	entryMgr.HandleHeartbeat(hb)

	// 1. PromotePeer against legacy 404 peer designates it as PrimaryNodeID in-place without error.
	entryURL, promoted, err := entryMgr.PromotePeer("legacy-peer", []byte(`{"users":{}}`))
	if err != nil {
		t.Fatalf("expected PromotePeer fallback on 404 to succeed, got: %v", err)
	}
	if entryURL != "http://127.0.0.1:18080" || promoted.Role != RoleEntry {
		t.Fatalf("unexpected PromotePeer fallback result: url=%q node=%+v", entryURL, promoted)
	}
	pool := entryMgr.PoolSummary()
	if pool.PrimaryNodeID != "legacy-peer" || len(pool.Nodes) != 2 || pool.Nodes[0].NodeID != "legacy-peer" || pool.Nodes[0].Role != RoleEntry || pool.Nodes[1].Role != RoleWorker {
		t.Fatalf("expected legacy-peer listed first as entry and self as worker: %+v", pool)
	}

	// 2. PromotePeer targeting self switches PrimaryNodeID back to self.
	_, selfPromoted, err := entryMgr.PromotePeer(entryMgr.NodeID(), nil)
	if err != nil || selfPromoted.Role != RoleEntry {
		t.Fatalf("PromotePeer(self) failed: node=%+v err=%v", selfPromoted, err)
	}
	poolBack := entryMgr.PoolSummary()
	if poolBack.PrimaryNodeID != entryMgr.NodeID() || poolBack.Nodes[0].NodeID != entryMgr.NodeID() || poolBack.Nodes[0].Role != RoleEntry || poolBack.Nodes[1].Role != RoleWorker {
		t.Fatalf("expected self restored as entry: %+v", poolBack)
	}

	// 3. EvictPeer against legacy 404 peer succeeds and blocks subsequent heartbeats while keeping it in discovered list.
	notified, peerName, err := entryMgr.EvictPeer("legacy-peer")
	if err != nil || notified || peerName != "DESKTOP-LEGACY" {
		t.Fatalf("expected EvictPeer fallback on 404 to succeed: notified=%v name=%q err=%v", notified, peerName, err)
	}
	if got := len(entryMgr.PoolSummary().Nodes); got != 1 {
		t.Fatalf("expected 1 node after evicting legacy peer, got %d", got)
	}
	if entryMgr.HandleHeartbeat(hb) || len(entryMgr.PoolSummary().Nodes) != 1 {
		t.Fatalf("expected background heartbeat from evicted legacy peer to be ignored")
	}
	disc := entryMgr.PassiveDiscoveredNodes()
	if len(disc) != 1 || disc[0].NodeID != "legacy-peer" || disc[0].AlreadyInPool {
		t.Fatalf("expected evicted legacy peer visible in discovered list as not in pool: %+v", disc)
	}
}

func TestPluginHardwareRecommendations(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	mgr, err := NewManager(dir, st, Options{
		Role:             RoleEntry,
		NodeName:         "Entry-Host",
		DiscoveryPort:    0,
		DiscoveryPortSet: true,
		SelfURL:          "http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	pm, err := plugins.NewManager(dir)
	if err != nil {
		t.Fatalf("plugins NewManager: %v", err)
	}
	mgr.SetPluginManager(pm)

	// Strained peer: 2 cores, 95% RAM used (~100 MB free), 90% CPU load, tiny free disk.
	mgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:         "node-strained",
			NodeName:       "Weak-Node",
			Role:           RoleWorker,
			OS:             "linux",
			Arch:           "amd64",
			CPUCores:       2,
			CPUPercent:     90,
			MemTotalBytes:  2 * GB,
			MemUsedBytes:   (19 * GB) / 10,
			MemPercent:     95,
			DiskTotalBytes: 20 * GB,
			DiskFreeBytes:  300 * MB,
			QuotaBytes:     10 * GB,
			FreeBytes:      10 * GB,
		},
	})

	// High-spec workstation peer with GPU acceleration and abundant free RAM.
	mgr.HandleHeartbeat(HeartbeatPayload{
		Node: NodeStatus{
			NodeID:           "node-strong",
			NodeName:         "GPU-Workstation",
			Role:             RoleWorker,
			OS:               "windows",
			Arch:             "amd64",
			CPUCores:         32,
			CPUModel:         "AMD Ryzen 9 7950X",
			CPUPercent:       8,
			MemTotalBytes:    128 * GB,
			MemUsedBytes:     16 * GB,
			MemPercent:       12.5,
			DiskTotalBytes:   1000 * GB,
			DiskFreeBytes:    800 * GB,
			GPUName:          "NVIDIA GeForce RTX 4090",
			HasGPU:           true,
			LANLinkMbps:      2500,
			LANBandwidthMbps: 2150.5,
			QuotaBytes:       100 * GB,
			FreeBytes:        100 * GB,
		},
	})

	recs := mgr.RecommendPlugins(pm.Marketplace())
	if len(recs) < 3 {
		t.Fatalf("expected recommendations for all marketplace plugins, got %+v", recs)
	}
	var ocrRec *PluginRecommendation
	for i := range recs {
		if recs[i].PluginID == plugins.PluginOCRVision {
			ocrRec = &recs[i]
			break
		}
	}
	if ocrRec == nil {
		t.Fatalf("missing recommendation for ocr-vision")
	}
	if ocrRec.BestNodeID != "node-strong" || ocrRec.BestLevel != "recommended" {
		t.Fatalf("expected node-strong recommended for ocr-vision, got %+v", ocrRec)
	}

	var strainedFit, strongFit *NodePluginFit
	for i := range ocrRec.NodeFits {
		switch ocrRec.NodeFits[i].NodeID {
		case "node-strained":
			strainedFit = &ocrRec.NodeFits[i]
		case "node-strong":
			strongFit = &ocrRec.NodeFits[i]
		}
	}
	if strainedFit == nil || strainedFit.Level != "limited" || strainedFit.CanInstall {
		t.Fatalf("expected strained node marked limited/can_install=false, got %+v", strainedFit)
	}
	if strongFit == nil || strongFit.Level != "recommended" || strongFit.LANBandwidthMbps != 2150.5 {
		t.Fatalf("expected strong node marked recommended with LAN speed, got %+v", strongFit)
	}
}

func TestSelectBestLANLinkMbpsFiltersVirtualTunnels(t *testing.T) {
	candidates := []nicCandidate{
		{
			Name:          "Mihomo",
			Description:   "Meta Tunnel",
			InterfaceType: "53",
			SpeedBps:      100_000_000_000, // 100 Gbps synthetic tunnel speed
			IPv4s:         []string{"198.18.0.1"},
		},
		{
			Name:          "Tailscale",
			Description:   "Tailscale Tunnel",
			InterfaceType: "53",
			SpeedBps:      100_000_000_000,
			IPv4s:         []string{"100.120.53.119"},
		},
		{
			Name:          "vEthernet (Default Switch)",
			Description:   "Hyper-V Virtual Ethernet Adapter",
			InterfaceType: "Ethernet",
			SpeedBps:      10_000_000_000,
			IPv4s:         []string{"172.25.80.1"},
		},
		{
			Name:          "以太网 3",
			Description:   "VMware Virtual Ethernet Adapter for VMnet1",
			InterfaceType: "Ethernet",
			SpeedBps:      100_000_000,
			IPv4s:         []string{"192.168.126.1"},
		},
		{
			Name:          "WLAN",
			Description:   "Realtek 8852CE WiFi 6E PCI-E NIC",
			InterfaceType: "Wireless80211",
			SpeedBps:      866_700_000,
			IPv4s:         []string{"192.168.3.53"},
		},
	}

	got := selectBestLANLinkMbps(candidates)
	if got != 867 {
		t.Fatalf("expected physical WLAN link 867 Mbps after filtering tunnels/virtual adapters, got %d", got)
	}

	// Ensure a real 2.5GbE LAN adapter takes priority over a disconnected/non-LAN physical port or lower Wi-Fi.
	candidates = append(candidates, nicCandidate{
		Name:          "以太网",
		Description:   "Realtek Gaming 2.5GbE Family Controller",
		InterfaceType: "Ethernet",
		SpeedBps:      2_500_000_000,
		IPv4s:         []string{"192.168.3.63"},
	})
	if got := selectBestLANLinkMbps(candidates); got != 2500 {
		t.Fatalf("expected 2500 Mbps for 2.5GbE Ethernet adapter, got %d", got)
	}
}
