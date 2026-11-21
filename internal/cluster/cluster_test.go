package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			"app_name": "LFH",
			"version":  "V0.11.21.3",
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
