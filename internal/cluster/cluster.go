package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lfh/internal/storage"
	"lfh/internal/version"
)

const (
	RoleEntry            = "entry"
	RoleWorker           = "worker"
	DefaultQuotaGB       = 10.0
	DefaultDiscoveryPort = 53319
	PeerTimeout          = 12 * time.Second

	KB                = int64(1024)
	MB                = 1024 * KB
	GB                = 1024 * MB
	TB                = 1024 * GB
	MinUserQuotaBytes = 1 * MB
	MaxUserQuotaBytes = 100 * TB
	DefaultQuotaBytes = 10 * GB
)

const (
	hdrNodeID    = "X-Cluster-Node-ID"
	hdrTimestamp = "X-Cluster-Timestamp"
	hdrSignature = "X-Cluster-Signature"
)

type Config struct {
	NodeID        string  `json:"node_id"`
	NodeName      string  `json:"node_name"`
	Role          string  `json:"role"`
	ClusterSecret string  `json:"cluster_secret"`
	QuotaBytes    int64   `json:"quota_bytes"`
	QuotaGB       float64 `json:"quota_gb"`
	ExposeIP      string  `json:"expose_ip,omitempty"`
	Port          string  `json:"port,omitempty"`
	EntryHint     string  `json:"entry_hint,omitempty"`
	StorageDir    string  `json:"storage_dir,omitempty"`
	DiscoveryPort int     `json:"discovery_port"`
}

type Options struct {
	ConfigSeedPath   string
	Role             string
	NodeName         string
	ClusterSecret    string
	QuotaBytes       int64
	QuotaGB          float64
	QuotaSet         bool
	ExposeIP         string
	ExposeIPSet      bool
	Port             string
	PortSet          bool
	EntryHint        string
	StorageDir       string
	StorageDirSet    bool
	DiscoveryPort    int
	DiscoveryPortSet bool
	SelfURL          string
}

type NodeStatus struct {
	NodeID     string      `json:"node_id"`
	NodeName   string      `json:"node_name"`
	Role       string      `json:"role"`
	OS         string      `json:"os"`
	Arch       string      `json:"arch"`
	HTTPURL    string      `json:"http_url"`
	QuotaGB    float64     `json:"quota_gb"`
	QuotaBytes int64       `json:"quota_bytes"`
	UsedBytes  int64       `json:"used_bytes"`
	FreeBytes  int64       `json:"free_bytes"`
	FileCount  int         `json:"file_count"`
	StorageDir string      `json:"storage_dir,omitempty"`
	Drives     []DriveInfo `json:"drives,omitempty"`
	Online     bool        `json:"online"`
	IsSelf     bool        `json:"is_self"`
	LastSeen   time.Time   `json:"last_seen"`
}

type PoolSummary struct {
	EntryURL        string       `json:"entry_url"`
	TotalNodes      int          `json:"total_nodes"`
	OnlineNodes     int          `json:"online_nodes"`
	TotalQuotaBytes int64        `json:"total_quota_bytes"`
	TotalUsedBytes  int64        `json:"total_used_bytes"`
	TotalFreeBytes  int64        `json:"total_free_bytes"`
	TotalFileCount  int          `json:"total_file_count"`
	Nodes           []NodeStatus `json:"nodes"`
}

type HeartbeatPayload struct {
	Node  NodeStatus           `json:"node"`
	Files []storage.FileRecord `json:"files"`
}

type beaconPacket struct {
	ClusterHash string `json:"cluster_hash"`
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	Role        string `json:"role,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
	Version     string `json:"version,omitempty"`
	HTTPURL     string `json:"http_url,omitempty"`
	EntryURL    string `json:"entry_url"`
}

var directClusterTransport = &http.Transport{
	// Never route LAN node-to-node RPCs through HTTP_PROXY / HTTPS_PROXY.
	Proxy: nil,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          64,
	MaxIdleConnsPerHost:   16,
	IdleConnTimeout:       60 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

func newDirectClusterClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: directClusterTransport,
	}
}

type peerRecord struct {
	status    NodeStatus
	files     []storage.FileRecord
	wasOnline bool
}

type Manager struct {
	mu                 sync.RWMutex
	cfgPath            string
	cfg                Config
	store              *storage.Store
	selfURL            string
	discoveredEntryURL string
	peers              map[string]*peerRecord
	discovered         map[string]DiscoveredNode
	apiClient          *http.Client
	streamClient       *http.Client
	hbTrigger          chan struct{}
	parentCtx          context.Context
	loopCancel         context.CancelFunc
	onPeerChange       func(nodeName string, online bool)
}

func ParseQuotaBytes(input string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(input))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, errors.New("预留空间不能为空")
	}

	unitMultiplier := GB
	numPart := s
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"TB", TB},
		{"GB", GB},
		{"MB", MB},
		{"KB", KB},
		{"T", TB},
		{"G", GB},
		{"M", MB},
		{"K", KB},
		{"B", 1},
	}
	for _, item := range suffixes {
		if strings.HasSuffix(s, item.suffix) {
			unitMultiplier = item.mult
			numPart = strings.TrimSuffix(s, item.suffix)
			break
		}
	}

	if numPart == "" {
		return 0, errors.New("预留空间格式无效，例如：500MB、10GB、1.5TB")
	}
	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val <= 0 {
		return 0, errors.New("预留空间必须是大于 0 的数字（支持 B/KB/MB/GB/TB）")
	}

	fBytes := val * float64(unitMultiplier)
	if fBytes > float64(MaxUserQuotaBytes) {
		return 0, errors.New("预留空间不能超过 100 TB")
	}
	bytes := int64(math.Round(fBytes))
	if bytes <= 0 {
		return 0, errors.New("预留空间过小")
	}
	return bytes, nil
}

func NewManager(dataDir string, store *storage.Store, opts Options) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dataDir, "cluster.json")

	var cfg Config
	if raw, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	} else if opts.ConfigSeedPath != "" {
		if rawSeed, seedErr := os.ReadFile(opts.ConfigSeedPath); seedErr == nil {
			var seed Config
			if json.Unmarshal(rawSeed, &seed) == nil {
				cfg.ClusterSecret = seed.ClusterSecret
				cfg.Role = seed.Role
				cfg.QuotaBytes = seed.QuotaBytes
				cfg.QuotaGB = seed.QuotaGB
				cfg.ExposeIP = seed.ExposeIP
				cfg.Port = seed.Port
				cfg.EntryHint = seed.EntryHint
				cfg.StorageDir = seed.StorageDir
				cfg.DiscoveryPort = seed.DiscoveryPort
				if opts.DiscoveryPortSet {
					cfg.DiscoveryPort = opts.DiscoveryPort
				}
			}
		}
	}

	if cfg.QuotaBytes <= 0 && cfg.QuotaGB > 0 {
		cfg.QuotaBytes = int64(cfg.QuotaGB * float64(GB))
	}

	if opts.Role != "" {
		cfg.Role = opts.Role
	}
	if cfg.Role != RoleWorker {
		cfg.Role = RoleEntry
	}

	if opts.QuotaSet {
		if opts.QuotaBytes > 0 {
			cfg.QuotaBytes = opts.QuotaBytes
		} else if opts.QuotaGB > 0 {
			cfg.QuotaBytes = int64(opts.QuotaGB * float64(GB))
		}
	}
	if cfg.QuotaBytes <= 0 {
		cfg.QuotaBytes = DefaultQuotaBytes
	}
	cfg.QuotaGB = float64(cfg.QuotaBytes) / float64(GB)

	if opts.ExposeIPSet {
		cfg.ExposeIP = strings.TrimSpace(opts.ExposeIP)
	}
	if opts.PortSet && strings.TrimSpace(opts.Port) != "" {
		cfg.Port = strings.TrimSpace(opts.Port)
	}
	if strings.TrimSpace(cfg.Port) == "" {
		cfg.Port = "8080"
	}

	if opts.ClusterSecret != "" {
		cfg.ClusterSecret = strings.TrimSpace(opts.ClusterSecret)
	}
	if cfg.ClusterSecret == "" {
		sec, err := randomHex(16)
		if err != nil {
			return nil, err
		}
		cfg.ClusterSecret = sec
	}

	if opts.EntryHint != "" {
		cfg.EntryHint = strings.TrimRight(strings.TrimSpace(opts.EntryHint), "/")
	}

	if opts.DiscoveryPortSet {
		cfg.DiscoveryPort = opts.DiscoveryPort
	} else if cfg.DiscoveryPort == 0 && cfg.NodeID == "" {
		cfg.DiscoveryPort = DefaultDiscoveryPort
	}

	if cfg.NodeID == "" {
		suffix, err := randomHex(4)
		if err != nil {
			return nil, err
		}
		cfg.NodeID = "node-" + suffix
	}

	if opts.NodeName != "" {
		cfg.NodeName = strings.TrimSpace(opts.NodeName)
	}
	if opts.StorageDirSet {
		cfg.StorageDir = strings.TrimSpace(opts.StorageDir)
	}
	if cfg.StorageDir != "" {
		if _, err := store.SetUploadDir(cfg.StorageDir, false); err != nil {
			if opts.StorageDirSet {
				return nil, fmt.Errorf("初始化存储目录 %q 失败: %w", cfg.StorageDir, err)
			}
			log.Printf("[cluster] warning: saved storage_dir %q unavailable (%v); falling back to %s", cfg.StorageDir, err, store.UploadDir())
			cfg.StorageDir = ""
		} else {
			cfg.StorageDir = store.UploadDir()
		}
	}
	if cfg.NodeName == "" {
		hn, _ := os.Hostname()
		if hn == "" {
			hn = cfg.NodeID
		}
		cfg.NodeName = hn
	}

	m := &Manager{
		cfgPath:      cfgPath,
		cfg:          cfg,
		store:        store,
		selfURL:      strings.TrimRight(strings.TrimSpace(opts.SelfURL), "/"),
		peers:        make(map[string]*peerRecord),
		discovered:   make(map[string]DiscoveredNode),
		apiClient:    newDirectClusterClient(10 * time.Second),
		streamClient: newDirectClusterClient(0),
		hbTrigger:    make(chan struct{}, 1),
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) saveLocked() error {
	raw, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.cfgPath, raw, 0600)
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) Role() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Role
}

func (m *Manager) Secret() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ClusterSecret
}

func (m *Manager) NodeID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.NodeID
}

func (m *Manager) NodeName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.NodeName
}

func (m *Manager) ExposeIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ExposeIP
}

func (m *Manager) Port() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.Port == "" {
		return "8080"
	}
	return m.cfg.Port
}

func (m *Manager) StorageDir() string {
	return m.store.UploadDir()
}

func (m *Manager) SetSelfURL(u string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selfURL = strings.TrimRight(strings.TrimSpace(u), "/")
}

func (m *Manager) SetEndpointConfig(exposeIP, port, newSelfURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.ExposeIP = strings.TrimSpace(exposeIP)
	if strings.TrimSpace(port) != "" {
		m.cfg.Port = strings.TrimSpace(port)
	}
	if newSelfURL != "" {
		m.selfURL = strings.TrimRight(strings.TrimSpace(newSelfURL), "/")
	}
	return m.saveLocked()
}

func (m *Manager) EntryURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.Role == RoleEntry {
		return m.selfURL
	}
	if m.discoveredEntryURL != "" {
		return m.discoveredEntryURL
	}
	return m.cfg.EntryHint
}

func (m *Manager) SelfStatus() NodeStatus {
	m.mu.RLock()
	cfg := m.cfg
	selfURL := m.selfURL
	m.mu.RUnlock()

	cnt, used := m.store.LocalStats()
	quotaBytes := cfg.QuotaBytes
	if quotaBytes <= 0 {
		quotaBytes = DefaultQuotaBytes
	}
	free := quotaBytes - used
	if free < 0 {
		free = 0
	}
	return NodeStatus{
		NodeID:     cfg.NodeID,
		NodeName:   cfg.NodeName,
		Role:       cfg.Role,
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		HTTPURL:    selfURL,
		QuotaGB:    float64(quotaBytes) / float64(GB),
		QuotaBytes: quotaBytes,
		UsedBytes:  used,
		FreeBytes:  free,
		FileCount:  cnt,
		StorageDir: m.store.UploadDir(),
		Drives:     ListDrives(),
		Online:     true,
		IsSelf:     true,
		LastSeen:   time.Now(),
	}
}

func (m *Manager) CanAcceptLocal(size int64) error {
	st := m.SelfStatus()
	if size < 0 {
		size = 0
	}
	if st.UsedBytes+size > st.QuotaBytes {
		return fmt.Errorf("节点 %s 预留空间不足（剩余 %s，需要 %s）",
			st.NodeName, formatBytesHuman(st.FreeBytes), formatBytesHuman(size))
	}
	return nil
}

func (m *Manager) SetLocalQuotaBytes(bytes int64) (NodeStatus, error) {
	if bytes < MinUserQuotaBytes || bytes > MaxUserQuotaBytes {
		return NodeStatus{}, errors.New("预留空间范围需在 1 MB 到 100 TB 之间")
	}
	m.mu.Lock()
	m.cfg.QuotaBytes = bytes
	m.cfg.QuotaGB = float64(bytes) / float64(GB)
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return NodeStatus{}, err
	}
	m.mu.Unlock()
	return m.SelfStatus(), nil
}

func (m *Manager) SetLocalQuotaGB(gb float64) (NodeStatus, error) {
	return m.SetLocalQuotaBytes(int64(gb * float64(GB)))
}

func (m *Manager) HandleHeartbeat(payload HeartbeatPayload) bool {
	n := payload.Node
	if n.NodeID == "" || n.NodeID == m.NodeID() {
		return false
	}
	now := time.Now()
	n.Online = true
	n.IsSelf = false
	n.LastSeen = now
	if n.HTTPURL != "" {
		n.HTTPURL = strings.TrimRight(n.HTTPURL, "/")
	}

	files := make([]storage.FileRecord, len(payload.Files))
	for i, f := range payload.Files {
		f.NodeID = n.NodeID
		f.NodeName = n.NodeName
		f.NodeOS = n.OS
		f.NodeOnline = true
		if f.StorageDir == "" {
			f.StorageDir = n.StorageDir
		}
		files[i] = f
	}

	m.mu.Lock()
	prev, exists := m.peers[n.NodeID]
	changed := !exists ||
		!prev.wasOnline ||
		now.Sub(prev.status.LastSeen) > PeerTimeout ||
		prev.status.NodeName != n.NodeName ||
		prev.status.QuotaBytes != n.QuotaBytes ||
		prev.status.UsedBytes != n.UsedBytes ||
		prev.status.FileCount != n.FileCount ||
		prev.status.StorageDir != n.StorageDir ||
		len(prev.files) != len(files)
	if !changed {
		for i := range files {
			if prev.files[i].ID != files[i].ID ||
				prev.files[i].StorageDir != files[i].StorageDir ||
				!equalStringSlice(prev.files[i].AllowedUsers, files[i].AllowedUsers) {
				changed = true
				break
			}
		}
	}
	m.peers[n.NodeID] = &peerRecord{
		status:    n,
		files:     files,
		wasOnline: true,
	}
	m.mu.Unlock()
	return changed
}

func (m *Manager) SetOnPeerChange(fn func(nodeName string, online bool)) {
	m.mu.Lock()
	m.onPeerChange = fn
	m.mu.Unlock()
}

// SetPeerLastSeenForTest adjusts a peer's recorded heartbeat time for offline/recovery testing.
func (m *Manager) SetPeerLastSeenForTest(nodeID string, ts time.Time) {
	m.mu.Lock()
	if p, ok := m.peers[nodeID]; ok {
		p.status.LastSeen = ts
		p.wasOnline = time.Since(ts) <= PeerTimeout
	}
	m.mu.Unlock()
}

func (m *Manager) PoolSummary() PoolSummary {
	self := m.SelfStatus()
	now := time.Now()

	m.mu.RLock()
	entryURL := m.selfURL
	if m.cfg.Role == RoleWorker {
		if m.discoveredEntryURL != "" {
			entryURL = m.discoveredEntryURL
		} else {
			entryURL = m.cfg.EntryHint
		}
	}
	peers := make([]NodeStatus, 0, len(m.peers))
	for _, p := range m.peers {
		st := p.status
		st.IsSelf = false
		st.Online = now.Sub(st.LastSeen) <= PeerTimeout
		peers = append(peers, st)
	}
	m.mu.RUnlock()

	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Online != peers[j].Online {
			return peers[i].Online
		}
		if peers[i].NodeName != peers[j].NodeName {
			return peers[i].NodeName < peers[j].NodeName
		}
		return peers[i].NodeID < peers[j].NodeID
	})

	nodes := make([]NodeStatus, 0, 1+len(peers))
	nodes = append(nodes, self)
	nodes = append(nodes, peers...)

	summary := PoolSummary{
		EntryURL:   entryURL,
		TotalNodes: len(nodes),
		Nodes:      nodes,
	}
	for _, n := range nodes {
		if n.Online {
			summary.OnlineNodes++
			summary.TotalQuotaBytes += n.QuotaBytes
			summary.TotalUsedBytes += n.UsedBytes
			summary.TotalFreeBytes += n.FreeBytes
			summary.TotalFileCount += n.FileCount
		}
	}
	return summary
}

func (m *Manager) PoolSummaryForRole(isAdmin bool) PoolSummary {
	summary := m.PoolSummary()
	if isAdmin {
		return summary
	}
	sanitized := make([]NodeStatus, len(summary.Nodes))
	for i, n := range summary.Nodes {
		n.StorageDir = ""
		n.Drives = nil
		sanitized[i] = n
	}
	summary.Nodes = sanitized
	return summary
}

func (m *Manager) AllClusterFiles() []storage.FileRecord {
	self := m.SelfStatus()
	local := m.store.AllRecords()
	out := make([]storage.FileRecord, 0, len(local))
	for _, rec := range local {
		rec.NodeID = self.NodeID
		rec.NodeName = self.NodeName
		rec.NodeOS = self.OS
		rec.NodeOnline = true
		if rec.StorageDir == "" {
			rec.StorageDir = m.store.RecordDir(rec)
		}
		out = append(out, rec)
	}

	now := time.Now()
	m.mu.RLock()
	for _, p := range m.peers {
		online := now.Sub(p.status.LastSeen) <= PeerTimeout
		if !online {
			continue
		}
		for _, rec := range p.files {
			rec.NodeID = p.status.NodeID
			rec.NodeName = p.status.NodeName
			rec.NodeOS = p.status.OS
			rec.NodeOnline = online
			out = append(out, rec)
		}
	}
	m.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].UploadedAt.After(out[j].UploadedAt)
	})
	return out
}

func (m *Manager) LocateFile(fileID string) (storage.FileRecord, NodeStatus, bool) {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.peers {
		online := now.Sub(p.status.LastSeen) <= PeerTimeout
		st := p.status
		st.Online = online
		for _, rec := range p.files {
			if rec.ID == fileID {
				rec.NodeID = st.NodeID
				rec.NodeName = st.NodeName
				rec.NodeOS = st.OS
				rec.NodeOnline = online
				return rec, st, true
			}
		}
	}
	return storage.FileRecord{}, NodeStatus{}, false
}

func (m *Manager) AddPeerFile(nodeID string, rec storage.FileRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[nodeID]
	if !ok {
		return
	}
	rec.NodeID = p.status.NodeID
	rec.NodeName = p.status.NodeName
	rec.NodeOS = p.status.OS
	rec.NodeOnline = true
	p.files = append([]storage.FileRecord{rec}, p.files...)
	p.status.FileCount++
	p.status.UsedBytes += rec.Size
	p.status.FreeBytes = p.status.QuotaBytes - p.status.UsedBytes
	if p.status.FreeBytes < 0 {
		p.status.FreeBytes = 0
	}
}

func (m *Manager) RemovePeerFile(nodeID, fileID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[nodeID]
	if !ok {
		return
	}
	for i, f := range p.files {
		if f.ID == fileID {
			p.files = append(p.files[:i], p.files[i+1:]...)
			p.status.FileCount = max(0, p.status.FileCount-1)
			p.status.UsedBytes = max(0, p.status.UsedBytes-f.Size)
			p.status.FreeBytes = max(0, p.status.QuotaBytes-p.status.UsedBytes)
			break
		}
	}
}

func (m *Manager) SelectUploadTarget(preferredNodeID string, fileSize int64) (NodeStatus, error) {
	pool := m.PoolSummary()
	if fileSize < 0 {
		fileSize = 0
	}

	if preferredNodeID != "" && preferredNodeID != "auto" {
		for _, n := range pool.Nodes {
			if n.NodeID == preferredNodeID {
				if !n.Online {
					return NodeStatus{}, fmt.Errorf("目标节点 %s 当前离线", n.NodeName)
				}
				if n.FreeBytes < fileSize {
					return NodeStatus{}, fmt.Errorf("节点 %s 预留空间不足（剩余 %s，需要 %s）",
						n.NodeName, formatBytesHuman(n.FreeBytes), formatBytesHuman(fileSize))
				}
				return n, nil
			}
		}
		return NodeStatus{}, errors.New("指定的存储节点不存在")
	}

	var best *NodeStatus
	for i := range pool.Nodes {
		n := &pool.Nodes[i]
		if !n.Online || n.FreeBytes < fileSize {
			continue
		}
		if best == nil {
			best = n
			continue
		}
		if n.FreeBytes != best.FreeBytes {
			if n.FreeBytes > best.FreeBytes {
				best = n
			}
		} else if n.IsSelf && !best.IsSelf {
			best = n
		} else if n.IsSelf == best.IsSelf && n.NodeID < best.NodeID {
			best = n
		}
	}
	if best == nil {
		return NodeStatus{}, fmt.Errorf("集群所有在线节点预留空间不足（总剩余 %s，当前文件需要 %s），请在管理员中心调大节点预留空间或接入更多电脑",
			formatBytesHuman(pool.TotalFreeBytes), formatBytesHuman(fileSize))
	}
	return *best, nil
}

// ==================== HMAC Cluster Request Signing ====================

func SecretHash(secret string) string {
	sum := sha256.Sum256([]byte("lan-filehub-cluster:" + secret))
	return hex.EncodeToString(sum[:])[:16]
}

func SignRequest(req *http.Request, secret, nodeID string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := computeSignature(secret, req.Method, req.URL.Path, ts, nodeID)
	req.Header.Set(hdrNodeID, nodeID)
	req.Header.Set(hdrTimestamp, ts)
	req.Header.Set(hdrSignature, sig)
}

func VerifyRequest(r *http.Request, secret string) bool {
	nodeID := strings.TrimSpace(r.Header.Get(hdrNodeID))
	tsStr := strings.TrimSpace(r.Header.Get(hdrTimestamp))
	sig := strings.TrimSpace(r.Header.Get(hdrSignature))
	if nodeID == "" || tsStr == "" || sig == "" {
		return false
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > 120 {
		return false
	}
	expected := computeSignature(secret, r.Method, r.URL.Path, tsStr, nodeID)
	return hmac.Equal([]byte(sig), []byte(expected))
}

func computeSignature(secret, method, path, ts, nodeID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method + "|" + path + "|" + ts + "|" + nodeID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ==================== Cross-Node HTTP Proxy Helpers ====================

func (m *Manager) ProxyUpload(peer NodeStatus, filename, folderPath string, r io.Reader, uploader string, allowedUsers []string, dev storage.DeviceInfo) (storage.FileRecord, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		_ = mw.WriteField("uploader", uploader)
		_ = mw.WriteField("folder_path", folderPath)
		_ = mw.WriteField("device_fp", dev.FP)
		_ = mw.WriteField("device_name", dev.Name)
		_ = mw.WriteField("device_ip", dev.IP)
		_ = mw.WriteField("device_detail", dev.Detail)
		if len(allowedUsers) > 0 {
			if rawAllowed, err := json.Marshal(allowedUsers); err == nil {
				_ = mw.WriteField("allowed_users", string(rawAllowed))
			}
		}

		part, err := mw.CreateFormFile("files", filename)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/upload"
	req, err := http.NewRequest(http.MethodPost, targetURL, pr)
	if err != nil {
		_ = pr.Close()
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, fmt.Errorf("转发文件到节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return storage.FileRecord{}, errors.New(errBody.Error)
		}
		return storage.FileRecord{}, fmt.Errorf("节点 %s 返回状态 %d", peer.NodeName, resp.StatusCode)
	}

	var rec storage.FileRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
}

func (m *Manager) ProxyGet(w http.ResponseWriter, r *http.Request, peer NodeStatus, internalPath string) {
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + internalPath
	if r.URL.RawQuery != "" && !strings.Contains(internalPath, "?") {
		targetURL += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, "构造代理请求失败", http.StatusInternalServerError)
		return
	}
	for _, h := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
		if val := r.Header.Get(h); val != "" {
			req.Header.Set(h, val)
		}
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		http.Error(w, "连接存储节点失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{
		"Content-Type",
		"Content-Length",
		"Content-Range",
		"Accept-Ranges",
		"Content-Disposition",
		"Last-Modified",
		"ETag",
	} {
		if val := resp.Header.Get(h); val != "" {
			w.Header().Set(h, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (m *Manager) FetchPeerFileToTemp(peer NodeStatus, fileID string) (string, func(), error) {
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + fileID + "/download"
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return "", nil, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("从节点 %s 拉取文件失败 (%d)", peer.NodeName, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "lan-filehub-peer-*")
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmpPath, cleanup, nil
}

func (m *Manager) ProxyDelete(peer NodeStatus, fileID string) error {
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + fileID
	req, err := http.NewRequest(http.MethodDelete, targetURL, nil)
	if err != nil {
		return err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("远端节点删除失败 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	m.RemovePeerFile(peer.NodeID, fileID)
	return nil
}

func (m *Manager) UpdateNodeQuotaBytes(nodeID string, quotaBytes int64) (NodeStatus, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.SetLocalQuotaBytes(quotaBytes)
	}
	m.mu.RLock()
	p, ok := m.peers[nodeID]
	var peer NodeStatus
	if ok {
		peer = p.status
		peer.Online = time.Since(peer.LastSeen) <= PeerTimeout
	}
	m.mu.RUnlock()
	if !ok {
		return NodeStatus{}, errors.New("节点不存在")
	}
	if !peer.Online {
		return NodeStatus{}, fmt.Errorf("节点 %s 当前离线，无法远程调整配额", peer.NodeName)
	}

	payload, _ := json.Marshal(map[string]any{
		"quota_bytes": quotaBytes,
		"quota_gb":    float64(quotaBytes) / float64(GB),
	})
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/quota"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return NodeStatus{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return NodeStatus{}, fmt.Errorf("通知节点 %s 更新配额失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return NodeStatus{}, errors.New(errBody.Error)
		}
		return NodeStatus{}, fmt.Errorf("更新配额失败 (%d)", resp.StatusCode)
	}

	var updated NodeStatus
	if err := json.Unmarshal(body, &updated); err != nil {
		return NodeStatus{}, err
	}
	updated.IsSelf = false
	updated.Online = true
	updated.LastSeen = time.Now()

	m.mu.Lock()
	if cur, exists := m.peers[nodeID]; exists {
		cur.status = updated
	}
	m.mu.Unlock()
	return updated, nil
}

func (m *Manager) UpdateNodeQuota(nodeID string, quotaGB float64) (NodeStatus, error) {
	return m.UpdateNodeQuotaBytes(nodeID, int64(quotaGB*float64(GB)))
}

func (m *Manager) SetLocalStorageDir(newDir string, migrateExisting bool) (NodeStatus, int, error) {
	moved, err := m.store.SetUploadDir(newDir, migrateExisting)
	if err != nil {
		return NodeStatus{}, moved, err
	}
	m.mu.Lock()
	m.cfg.StorageDir = m.store.UploadDir()
	saveErr := m.saveLocked()
	m.mu.Unlock()
	if saveErr != nil {
		return NodeStatus{}, moved, saveErr
	}
	return m.SelfStatus(), moved, nil
}

func (m *Manager) UpdateNodeStorageDir(nodeID, newDir string, migrateExisting bool) (NodeStatus, int, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.SetLocalStorageDir(newDir, migrateExisting)
	}
	m.mu.RLock()
	p, ok := m.peers[nodeID]
	var peer NodeStatus
	if ok {
		peer = p.status
		peer.Online = time.Since(peer.LastSeen) <= PeerTimeout
	}
	m.mu.RUnlock()
	if !ok {
		return NodeStatus{}, 0, errors.New("节点不存在")
	}
	if !peer.Online {
		return NodeStatus{}, 0, fmt.Errorf("节点 %s 当前离线，无法调整存储位置", peer.NodeName)
	}

	payload, _ := json.Marshal(map[string]any{
		"storage_dir":      newDir,
		"migrate_existing": migrateExisting,
	})
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/storage-dir"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return NodeStatus{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	client := m.apiClient
	if migrateExisting {
		client = newDirectClusterClient(5 * time.Minute)
	}
	resp, err := client.Do(req)
	if err != nil {
		return NodeStatus{}, 0, fmt.Errorf("通知节点 %s 更新存储位置失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return NodeStatus{}, 0, errors.New(errBody.Error)
		}
		return NodeStatus{}, 0, fmt.Errorf("更新存储位置失败 (%d)", resp.StatusCode)
	}

	var out struct {
		Node       NodeStatus `json:"node"`
		MovedCount int        `json:"moved_count"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return NodeStatus{}, 0, err
	}
	out.Node.IsSelf = false
	out.Node.Online = true
	out.Node.LastSeen = time.Now()

	m.mu.Lock()
	if cur, exists := m.peers[nodeID]; exists {
		cur.status = out.Node
		if out.MovedCount > 0 {
			for i := range cur.files {
				cur.files[i].StorageDir = out.Node.StorageDir
			}
		}
	}
	m.mu.Unlock()
	return out.Node, out.MovedCount, nil
}

func (m *Manager) UpdateFileAccess(fileID string, allowedUsers []string) (storage.FileRecord, error) {
	if _, _, ok := m.store.Get(fileID); ok {
		rec, err := m.store.SetAllowedUsers(fileID, allowedUsers)
		if err != nil {
			return storage.FileRecord{}, err
		}
		self := m.SelfStatus()
		rec.NodeID = self.NodeID
		rec.NodeName = self.NodeName
		rec.NodeOS = self.OS
		rec.NodeOnline = true
		if rec.StorageDir == "" {
			rec.StorageDir = m.store.RecordDir(rec)
		}
		return rec, nil
	}

	_, peer, ok := m.LocateFile(fileID)
	if !ok {
		return storage.FileRecord{}, errors.New("文件不存在")
	}
	if !peer.Online {
		return storage.FileRecord{}, fmt.Errorf("文件所在节点 %s 当前离线，无法修改可见权限", peer.NodeName)
	}

	payload, _ := json.Marshal(map[string]any{
		"allowed_users": allowedUsers,
	})
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + fileID + "/access"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, fmt.Errorf("通知节点 %s 修改文件权限失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return storage.FileRecord{}, errors.New(errBody.Error)
		}
		return storage.FileRecord{}, fmt.Errorf("修改文件权限失败 (%d)", resp.StatusCode)
	}

	var updated storage.FileRecord
	if err := json.Unmarshal(body, &updated); err != nil {
		return storage.FileRecord{}, err
	}
	updated.NodeID = peer.NodeID
	updated.NodeName = peer.NodeName
	updated.NodeOS = peer.OS
	updated.NodeOnline = true

	m.mu.Lock()
	if cur, exists := m.peers[peer.NodeID]; exists {
		for i := range cur.files {
			if cur.files[i].ID == fileID {
				cur.files[i].AllowedUsers = append([]string(nil), updated.AllowedUsers...)
				break
			}
		}
	}
	m.mu.Unlock()
	return updated, nil
}

// ==================== Discovery & Heartbeat Loops ====================

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.parentCtx = ctx
	m.restartLoopsLocked()
	m.mu.Unlock()
}

func (m *Manager) restartLoopsLocked() {
	if m.loopCancel != nil {
		m.loopCancel()
		m.loopCancel = nil
	}
	if m.parentCtx == nil {
		return
	}
	loopCtx, cancel := context.WithCancel(m.parentCtx)
	m.loopCancel = cancel
	role := m.cfg.Role
	discPort := m.cfg.DiscoveryPort

	if discPort > 0 {
		go m.runPresenceBeacon(loopCtx, discPort)
		go m.runDiscoveryListener(loopCtx, discPort)
	}
	if role == RoleEntry {
		go m.runPeerLivenessWatcher(loopCtx)
	} else if role == RoleWorker {
		go m.runWorkerHeartbeat(loopCtx)
	}
}

func (m *Manager) SendTestHeartbeat(entryURL, secret string) error {
	cleanEntry, err := NormalizePeerURL(entryURL, m.Port())
	if err != nil {
		return err
	}
	sec := strings.TrimSpace(secret)
	if sec == "" {
		return errors.New("主节点集群密钥不能为空")
	}

	self := m.SelfStatus()
	self.Role = RoleWorker
	payload := HeartbeatPayload{
		Node:  self,
		Files: m.store.AllRecords(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	target := strings.TrimRight(cleanEntry, "/") + "/internal/cluster/heartbeat"
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, sec, m.NodeID())

	client := newDirectClusterClient(6 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接到主入口节点 %s: %w", cleanEntry, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("主节点集群密钥不匹配")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("主节点返回异常状态 (%d)", resp.StatusCode)
	}
	return nil
}

// JoinEntryCluster verifies reachability + secret and switches the local node live to worker mode under entryURL.
func (m *Manager) JoinEntryCluster(entryURL, secret string, quotaBytes int64) error {
	cleanEntry, err := NormalizePeerURL(entryURL, m.Port())
	if err != nil {
		return err
	}
	sec := strings.TrimSpace(secret)
	if sec == "" {
		return errors.New("主节点集群密钥不能为空")
	}
	m.mu.RLock()
	selfURL := m.selfURL
	m.mu.RUnlock()
	if selfURL != "" && strings.EqualFold(cleanEntry, selfURL) {
		return errors.New("不能将本机加入自身作为主入口")
	}
	if quotaBytes > 0 && (quotaBytes < MinUserQuotaBytes || quotaBytes > MaxUserQuotaBytes) {
		return errors.New("预留空间范围需在 1 MB 到 100 TB 之间")
	}

	if err := m.SendTestHeartbeat(cleanEntry, sec); err != nil {
		return err
	}

	m.mu.Lock()
	m.cfg.Role = RoleWorker
	m.cfg.ClusterSecret = sec
	m.cfg.EntryHint = cleanEntry
	m.discoveredEntryURL = cleanEntry
	if quotaBytes > 0 {
		m.cfg.QuotaBytes = quotaBytes
		m.cfg.QuotaGB = float64(quotaBytes) / float64(GB)
	}
	m.peers = make(map[string]*peerRecord)
	err = m.saveLocked()
	if err == nil && m.parentCtx != nil {
		m.restartLoopsLocked()
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.TriggerHeartbeat()
	return nil
}

// AdoptPeer instructs a remote standalone LFH node to authenticate and join this Entry node's cluster live.
func (m *Manager) AdoptPeer(peerURL, targetAdminUser, targetAdminPass, targetAuthSecret, quotaText string) (NodeStatus, error) {
	if m.Role() != RoleEntry {
		return NodeStatus{}, errors.New("仅主入口节点可执行纳管操作")
	}
	entryURL := m.EntryURL()
	if entryURL == "" {
		return NodeStatus{}, errors.New("当前主入口地址未就绪")
	}
	cleanPeer, err := NormalizePeerURL(peerURL, m.Port())
	if err != nil {
		return NodeStatus{}, err
	}
	if strings.EqualFold(cleanPeer, entryURL) {
		return NodeStatus{}, errors.New("不能纳管当前本机主节点")
	}

	reqBody := map[string]any{
		"entry_url":          entryURL,
		"cluster_secret":     m.Secret(),
		"target_admin_user":  strings.TrimSpace(targetAdminUser),
		"target_admin_pass":  targetAdminPass,
		"target_auth_secret": strings.TrimSpace(targetAuthSecret),
		"quota_text":         strings.TrimSpace(quotaText),
	}
	raw, _ := json.Marshal(reqBody)
	client := newDirectClusterClient(15 * time.Second)
	req, err := http.NewRequest(http.MethodPost, cleanPeer+"/api/cluster/adopt", bytes.NewReader(raw))
	if err != nil {
		return NodeStatus{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return NodeStatus{}, fmt.Errorf("连接目标节点 %s 失败: %w", cleanPeer, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return NodeStatus{}, errors.New(errBody.Error)
		}
		return NodeStatus{}, fmt.Errorf("纳管目标节点失败 (%d)", resp.StatusCode)
	}

	var out struct {
		OK   bool       `json:"ok"`
		Node NodeStatus `json:"node"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return NodeStatus{}, err
	}

	m.mu.Lock()
	if dn, ok := m.discovered[out.Node.NodeID]; ok {
		dn.AlreadyInPool = true
		dn.Role = RoleWorker
		m.discovered[out.Node.NodeID] = dn
	}
	m.mu.Unlock()
	return out.Node, nil
}

// RemovePeer removes a retired offline peer from the in-memory cluster registry.
func (m *Manager) RemovePeer(nodeID string) error {
	cleanID := strings.TrimSpace(nodeID)
	if cleanID == "" || cleanID == m.NodeID() {
		return errors.New("不能移除本机节点")
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[cleanID]
	if !ok {
		return errors.New("节点不存在")
	}
	if now.Sub(p.status.LastSeen) <= PeerTimeout {
		return errors.New("节点当前在线，请先停止该节点服务后再移除")
	}
	delete(m.peers, cleanID)
	if dn, exists := m.discovered[cleanID]; exists {
		dn.AlreadyInPool = false
		m.discovered[cleanID] = dn
	}
	return nil
}

// PromoteSelfToEntry upgrades this node into the cluster entry node while keeping its cluster secret.
func (m *Manager) PromoteSelfToEntry(clusterSecret string) (NodeStatus, error) {
	m.mu.Lock()
	expected := strings.TrimSpace(m.cfg.ClusterSecret)
	incoming := strings.TrimSpace(clusterSecret)
	if incoming != "" && expected != "" && incoming != expected {
		m.mu.Unlock()
		return NodeStatus{}, errors.New("集群密钥不匹配，拒绝升主")
	}
	oldRole := m.cfg.Role
	oldHint := m.cfg.EntryHint
	oldDiscEntry := m.discoveredEntryURL

	m.cfg.Role = RoleEntry
	m.cfg.EntryHint = ""
	m.discoveredEntryURL = ""
	if m.peers == nil {
		m.peers = make(map[string]*peerRecord)
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Role = oldRole
		m.cfg.EntryHint = oldHint
		m.discoveredEntryURL = oldDiscEntry
		m.mu.Unlock()
		return NodeStatus{}, err
	}
	m.restartLoopsLocked()
	m.mu.Unlock()
	return m.SelfStatus(), nil
}

// RepointEntry switches this node to worker role pointing at a new entry URL and sends an immediate heartbeat.
func (m *Manager) RepointEntry(newEntryURL string) error {
	cleanURL, err := NormalizePeerURL(newEntryURL, m.Port())
	if err != nil {
		return fmt.Errorf("新主节点地址无效: %w", err)
	}

	m.mu.Lock()
	if m.selfURL != "" && strings.EqualFold(strings.TrimRight(cleanURL, "/"), strings.TrimRight(m.selfURL, "/")) {
		m.mu.Unlock()
		return errors.New("不能将本机作为远端主入口重指目标")
	}
	oldRole := m.cfg.Role
	oldHint := m.cfg.EntryHint
	oldDiscEntry := m.discoveredEntryURL
	oldPeers := m.peers

	m.cfg.Role = RoleWorker
	m.cfg.EntryHint = cleanURL
	m.discoveredEntryURL = cleanURL
	m.peers = make(map[string]*peerRecord)
	for id, dn := range m.discovered {
		dn.AlreadyInPool = false
		m.discovered[id] = dn
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Role = oldRole
		m.cfg.EntryHint = oldHint
		m.discoveredEntryURL = oldDiscEntry
		m.peers = oldPeers
		m.mu.Unlock()
		return err
	}
	m.restartLoopsLocked()
	m.mu.Unlock()

	if err := m.SendHeartbeatNow(); err != nil {
		m.TriggerHeartbeat()
	}
	return nil
}

// LeaveCluster detaches this node from its current cluster, rotates its cluster secret, and returns to standalone entry mode.
func (m *Manager) LeaveCluster() (NodeStatus, error) {
	newSecret, err := randomHex(16)
	if err != nil {
		return NodeStatus{}, err
	}

	m.mu.Lock()
	oldRole := m.cfg.Role
	oldHint := m.cfg.EntryHint
	oldDiscEntry := m.discoveredEntryURL
	oldSecret := m.cfg.ClusterSecret
	oldPeers := m.peers

	m.cfg.Role = RoleEntry
	m.cfg.EntryHint = ""
	m.discoveredEntryURL = ""
	m.cfg.ClusterSecret = newSecret
	m.peers = make(map[string]*peerRecord)
	for id, dn := range m.discovered {
		dn.AlreadyInPool = false
		m.discovered[id] = dn
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Role = oldRole
		m.cfg.EntryHint = oldHint
		m.discoveredEntryURL = oldDiscEntry
		m.cfg.ClusterSecret = oldSecret
		m.peers = oldPeers
		m.mu.Unlock()
		return NodeStatus{}, err
	}
	m.restartLoopsLocked()
	cb := m.onPeerChange
	nodeName := m.cfg.NodeName
	m.mu.Unlock()

	if cb != nil {
		cb(nodeName, true)
	}
	return m.SelfStatus(), nil
}

// SendLeaveToPeer notifies an online worker peer to leave the cluster cleanly.
func (m *Manager) SendLeaveToPeer(peer NodeStatus) error {
	targetURL := strings.TrimRight(strings.TrimSpace(peer.HTTPURL), "/")
	if targetURL == "" {
		return errors.New("节点缺少有效 HTTP 地址")
	}
	body, _ := json.Marshal(map[string]string{"reason": "evicted_by_entry"})
	req, err := http.NewRequest(http.MethodPost, targetURL+"/internal/cluster/leave", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())
	resp, err := newDirectClusterClient(5 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// EvictPeer removes either an online peer (after instructing it to leave) or an offline peer from the cluster pool.
func (m *Manager) EvictPeer(nodeID string) (bool, string, error) {
	cleanID := strings.TrimSpace(nodeID)
	if cleanID == "" || cleanID == m.NodeID() {
		return false, "", errors.New("不能移除本机节点")
	}

	now := time.Now()
	m.mu.RLock()
	p, ok := m.peers[cleanID]
	if !ok {
		m.mu.RUnlock()
		return false, "", errors.New("节点不存在")
	}
	peer := p.status
	peerName := peer.NodeName
	isOnline := now.Sub(peer.LastSeen) <= PeerTimeout
	m.mu.RUnlock()

	notified := false
	if isOnline {
		if err := m.SendLeaveToPeer(peer); err != nil {
			return false, peerName, fmt.Errorf("通知在线节点 %s 退出集群失败: %w", peerName, err)
		}
		notified = true
	}

	m.mu.Lock()
	delete(m.peers, cleanID)
	if dn, exists := m.discovered[cleanID]; exists {
		dn.AlreadyInPool = false
		m.discovered[cleanID] = dn
	}
	cb := m.onPeerChange
	m.mu.Unlock()

	if cb != nil {
		cb(peerName, false)
	}
	return notified, peerName, nil
}

// DissolveCluster detaches all online worker nodes best-effort, clears peers, and rotates the entry cluster secret.
func (m *Manager) DissolveCluster() (int, error) {
	if m.Role() != RoleEntry {
		return 0, errors.New("当前节点不是主入口节点")
	}
	now := time.Now()
	m.mu.RLock()
	var onlinePeers []NodeStatus
	for _, p := range m.peers {
		if now.Sub(p.status.LastSeen) <= PeerTimeout {
			onlinePeers = append(onlinePeers, p.status)
		}
	}
	m.mu.RUnlock()

	detached := 0
	for _, peer := range onlinePeers {
		if err := m.SendLeaveToPeer(peer); err == nil {
			detached++
		}
	}

	if _, err := m.LeaveCluster(); err != nil {
		return detached, err
	}
	return detached, nil
}

// PromotePeer transfers cluster entry authority and auth state to an online worker node, repoints other workers, and demotes self.
func (m *Manager) PromotePeer(targetNodeID string, authSnapshot []byte) (string, NodeStatus, error) {
	if m.Role() != RoleEntry {
		return "", NodeStatus{}, errors.New("当前节点不是主入口节点")
	}
	cleanTarget := strings.TrimSpace(targetNodeID)
	if cleanTarget == "" || cleanTarget == m.NodeID() {
		return "", NodeStatus{}, errors.New("请选择其他在线从节点作为新主节点")
	}
	if len(authSnapshot) == 0 {
		return "", NodeStatus{}, errors.New("账号权限快照为空，无法迁移主节点")
	}

	now := time.Now()
	m.mu.RLock()
	targetRec, ok := m.peers[cleanTarget]
	if !ok {
		m.mu.RUnlock()
		return "", NodeStatus{}, errors.New("目标节点不存在")
	}
	if now.Sub(targetRec.status.LastSeen) > PeerTimeout {
		m.mu.RUnlock()
		return "", NodeStatus{}, errors.New("目标节点当前离线，无法切换为主节点")
	}
	targetPeer := targetRec.status
	secret := m.cfg.ClusterSecret
	selfID := m.cfg.NodeID
	var otherPeers []NodeStatus
	for id, rec := range m.peers {
		if id == cleanTarget {
			continue
		}
		if now.Sub(rec.status.LastSeen) <= PeerTimeout && strings.TrimSpace(rec.status.HTTPURL) != "" {
			otherPeers = append(otherPeers, rec.status)
		}
	}
	m.mu.RUnlock()

	newEntryURL, err := NormalizePeerURL(targetPeer.HTTPURL, m.Port())
	if err != nil {
		return "", NodeStatus{}, fmt.Errorf("目标节点地址无效: %w", err)
	}

	promotePayload, err := json.Marshal(struct {
		ClusterSecret string `json:"cluster_secret"`
		AuthSnapshot  []byte `json:"auth_snapshot"`
	}{
		ClusterSecret: secret,
		AuthSnapshot:  authSnapshot,
	})
	if err != nil {
		return "", NodeStatus{}, err
	}

	promoteReq, err := http.NewRequest(http.MethodPost, newEntryURL+"/internal/cluster/promote", bytes.NewReader(promotePayload))
	if err != nil {
		return "", NodeStatus{}, err
	}
	promoteReq.Header.Set("Content-Type", "application/json")
	SignRequest(promoteReq, secret, selfID)
	promoteResp, err := newDirectClusterClient(10 * time.Second).Do(promoteReq)
	if err != nil {
		return "", NodeStatus{}, fmt.Errorf("无法连接目标节点完成升主: %w", err)
	}
	defer promoteResp.Body.Close()
	rawPromoteResp, _ := io.ReadAll(io.LimitReader(promoteResp.Body, 1<<20))
	if promoteResp.StatusCode >= 300 {
		return "", NodeStatus{}, fmt.Errorf("目标节点升主失败 (HTTP %d): %s", promoteResp.StatusCode, strings.TrimSpace(string(rawPromoteResp)))
	}
	var parsedPromote struct {
		OK   bool       `json:"ok"`
		Node NodeStatus `json:"node"`
	}
	if err := json.Unmarshal(rawPromoteResp, &parsedPromote); err != nil {
		return "", NodeStatus{}, fmt.Errorf("解析目标节点升主响应失败: %w", err)
	}
	if parsedPromote.Node.HTTPURL == "" {
		parsedPromote.Node.HTTPURL = newEntryURL
	}

	repointPayload, _ := json.Marshal(map[string]string{"new_entry_url": newEntryURL})
	for _, peer := range otherPeers {
		peerURL, err := NormalizePeerURL(peer.HTTPURL, m.Port())
		if err != nil {
			continue
		}
		repReq, err := http.NewRequest(http.MethodPost, peerURL+"/internal/cluster/repoint", bytes.NewReader(repointPayload))
		if err != nil {
			continue
		}
		repReq.Header.Set("Content-Type", "application/json")
		SignRequest(repReq, secret, selfID)
		repResp, err := newDirectClusterClient(5 * time.Second).Do(repReq)
		if err == nil {
			_ = repResp.Body.Close()
		}
	}

	if err := m.RepointEntry(newEntryURL); err != nil {
		return "", NodeStatus{}, fmt.Errorf("新主节点已就绪，但本机切换为从节点失败: %w", err)
	}
	return newEntryURL, parsedPromote.Node, nil
}

func (m *Manager) runPeerLivenessWatcher(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			var offlineNames []string
			m.mu.Lock()
			for _, p := range m.peers {
				if p.wasOnline && now.Sub(p.status.LastSeen) > PeerTimeout {
					p.wasOnline = false
					offlineNames = append(offlineNames, p.status.NodeName)
				}
			}
			cb := m.onPeerChange
			m.mu.Unlock()

			if cb != nil {
				for _, name := range offlineNames {
					cb(name, false)
				}
			}
		}
	}
}

func (m *Manager) TriggerHeartbeat() {
	select {
	case m.hbTrigger <- struct{}{}:
	default:
	}
}

func (m *Manager) SendHeartbeatNow() error {
	entryURL := m.EntryURL()
	if entryURL == "" {
		return errors.New("尚未发现主入口节点地址")
	}
	payload := HeartbeatPayload{
		Node:  m.SelfStatus(),
		Files: m.store.AllRecords(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	target := strings.TrimRight(entryURL, "/") + "/internal/cluster/heartbeat"
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat HTTP %d", resp.StatusCode)
	}

	var ack struct {
		OK       bool   `json:"ok"`
		EntryURL string `json:"entry_url"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&ack) == nil && ack.EntryURL != "" {
		clean := strings.TrimRight(strings.TrimSpace(ack.EntryURL), "/")
		m.mu.Lock()
		m.discoveredEntryURL = clean
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) runWorkerHeartbeat(ctx context.Context) {
	if err := m.SendHeartbeatNow(); err != nil {
		log.Printf("[cluster] initial heartbeat pending: %v", err)
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.SendHeartbeatNow()
		case <-m.hbTrigger:
			_ = m.SendHeartbeatNow()
		}
	}
}

func (m *Manager) runPresenceBeacon(ctx context.Context, discPort int) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		log.Printf("[cluster] udp beacon disabled: %v", err)
		return
	}
	defer conn.Close()

	sendOnce := func() {
		self := m.SelfStatus()
		entryURL := m.EntryURL()
		pkt := beaconPacket{
			ClusterHash: SecretHash(m.Secret()),
			NodeID:      self.NodeID,
			NodeName:    self.NodeName,
			Role:        self.Role,
			OS:          self.OS,
			Arch:        self.Arch,
			Version:     version.Version,
			HTTPURL:     self.HTTPURL,
			EntryURL:    entryURL,
		}
		raw, err := json.Marshal(pkt)
		if err != nil {
			return
		}
		for _, ip := range broadcastTargets() {
			_, _ = conn.WriteToUDP(raw, &net.UDPAddr{IP: ip, Port: discPort})
		}
	}

	sendOnce()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendOnce()
		}
	}
}

func (m *Manager) runDiscoveryListener(ctx context.Context, discPort int) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: discPort})
	if err != nil {
		log.Printf("[cluster] udp discovery listener on :%d skipped (%v); passive UDP listen disabled on this port", discPort, err)
		return
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var pkt beaconPacket
		if json.Unmarshal(buf[:n], &pkt) != nil {
			continue
		}
		if pkt.NodeID == "" || pkt.NodeID == m.NodeID() {
			continue
		}

		peerURL := strings.TrimRight(strings.TrimSpace(pkt.HTTPURL), "/")
		if peerURL == "" && pkt.Role != RoleWorker {
			peerURL = strings.TrimRight(strings.TrimSpace(pkt.EntryURL), "/")
		}
		if peerURL == "" && remoteAddr != nil {
			peerURL = fmt.Sprintf("http://%s:%s", remoteAddr.IP.String(), m.Port())
		}

		m.mu.Lock()
		if peerURL != "" {
			_, inPool := m.peers[pkt.NodeID]
			m.discovered[pkt.NodeID] = DiscoveredNode{
				NodeID:        pkt.NodeID,
				NodeName:      pkt.NodeName,
				Role:          pkt.Role,
				OS:            pkt.OS,
				Arch:          pkt.Arch,
				Version:       pkt.Version,
				URL:           peerURL,
				EntryURL:      strings.TrimRight(strings.TrimSpace(pkt.EntryURL), "/"),
				AlreadyInPool: inPool,
				Online:        true,
				Source:        "udp",
				LastSeen:      time.Now(),
			}
		}

		isWorker := m.cfg.Role == RoleWorker
		expectedHash := SecretHash(m.cfg.ClusterSecret)
		cleanEntry := strings.TrimRight(strings.TrimSpace(pkt.EntryURL), "/")
		isEntryBeacon := pkt.Role == "" || pkt.Role == RoleEntry
		entryUpdated := false
		if isWorker && isEntryBeacon && pkt.ClusterHash == expectedHash && cleanEntry != "" {
			if m.discoveredEntryURL != cleanEntry {
				m.discoveredEntryURL = cleanEntry
				entryUpdated = true
			}
		}
		m.mu.Unlock()

		if entryUpdated {
			log.Printf("[cluster] discovered entry node %s at %s", pkt.NodeName, cleanEntry)
			m.TriggerHeartbeat()
		}
	}
}

func broadcastTargets() []net.IP {
	seen := map[string]bool{"255.255.255.255": true}
	targets := []net.IP{net.IPv4bcast}

	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		low := strings.ToLower(iface.Name)
		if strings.Contains(low, "mihomo") || strings.Contains(low, "clash") || strings.Contains(low, "bluetooth") || strings.Contains(low, "蓝牙") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			mask := ipNet.Mask
			if ip4 == nil || len(mask) != 4 || ip4.IsLoopback() {
				continue
			}
			if (ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)) || (ip4[0] == 169 && ip4[1] == 254) {
				continue
			}
			bcast := net.IPv4(
				ip4[0]|^mask[0],
				ip4[1]|^mask[1],
				ip4[2]|^mask[2],
				ip4[3]|^mask[3],
			)
			key := bcast.String()
			if !seen[key] {
				seen[key] = true
				targets = append(targets, bcast)
			}
		}
	}
	return targets
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatBytesHuman(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	val := float64(b)
	idx := 0
	for val >= 1024 && idx < len(units)-1 {
		val /= 1024
		idx++
	}
	return fmt.Sprintf("%.2f %s", val, units[idx])
}
