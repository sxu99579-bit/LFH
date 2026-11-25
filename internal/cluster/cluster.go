package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lfh/internal/bridge"
	"lfh/internal/plugins"
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
	hdrNonce     = "X-LFH-Nonce"
)

type Config struct {
	NodeID           string            `json:"node_id"`
	NodeName         string            `json:"node_name"`
	Role             string            `json:"role"`
	ClusterSecret    string            `json:"cluster_secret"`
	QuotaBytes       int64             `json:"quota_bytes"`
	QuotaGB          float64           `json:"quota_gb"`
	ExposeIP         string            `json:"expose_ip,omitempty"`
	ExposeIPs        []string          `json:"expose_ips,omitempty"`
	DisabledIPs      []string          `json:"disabled_ips,omitempty"`
	Port             string            `json:"port,omitempty"`
	EntryHint        string            `json:"entry_hint,omitempty"`
	StorageDir       string            `json:"storage_dir,omitempty"`
	PrimaryNodeID    string            `json:"primary_node_id,omitempty"`
	EvictedPeers     map[string]string `json:"evicted_peers,omitempty"`
	AutoSyncWorkers  *bool             `json:"auto_sync_workers,omitempty"`
	ClipboardEnabled bool              `json:"clipboard_enabled,omitempty"`
	DiscoveryPort    int               `json:"discovery_port"`
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
	ClipboardEnabled bool
	ClipboardSet     bool
	DiscoveryPort    int
	DiscoveryPortSet bool
	SelfURL          string
}

type hostMetrics struct {
	CPUPercent     float64
	CPUCores       int
	CPUModel       string
	GPUName        string
	HasGPU         bool
	LANLinkMbps    int64
	MemPercent     float64
	MemUsedBytes   int64
	MemTotalBytes  int64
	DiskTotalBytes int64
	DiskUsedBytes  int64
	DiskFreeBytes  int64
	DiskPercent    float64
}

type NodeStatus struct {
	NodeID             string      `json:"node_id"`
	NodeName           string      `json:"node_name"`
	Role               string      `json:"role"`
	OS                 string      `json:"os"`
	Arch               string      `json:"arch"`
	Version            string      `json:"version,omitempty"`
	HTTPURL            string      `json:"http_url"`
	QuotaGB            float64     `json:"quota_gb"`
	QuotaBytes         int64       `json:"quota_bytes"`
	UsedBytes          int64       `json:"used_bytes"`
	FreeBytes          int64       `json:"free_bytes"`
	FileCount          int         `json:"file_count"`
	StorageDir         string      `json:"storage_dir,omitempty"`
	Drives             []DriveInfo `json:"drives,omitempty"`
	CPUPercent         float64     `json:"cpu_percent"`
	CPUCores           int         `json:"cpu_cores,omitempty"`
	CPUModel           string      `json:"cpu_model,omitempty"`
	GPUName            string      `json:"gpu_name,omitempty"`
	HasGPU             bool        `json:"has_gpu,omitempty"`
	LANLinkMbps        int64       `json:"lan_link_mbps,omitempty"`
	LANBandwidthMbps   float64     `json:"lan_bandwidth_mbps,omitempty"`
	BandwidthProbedAt  *time.Time  `json:"bandwidth_probed_at,omitempty"`
	MemPercent         float64     `json:"mem_percent"`
	MemUsedBytes       int64       `json:"mem_used_bytes"`
	MemTotalBytes      int64       `json:"mem_total_bytes"`
	DiskTotalBytes     int64       `json:"disk_total_bytes"`
	DiskUsedBytes      int64       `json:"disk_used_bytes"`
	DiskFreeBytes      int64       `json:"disk_free_bytes"`
	DiskPercent        float64     `json:"disk_percent"`
	NetLatencyMs       float64     `json:"net_latency_ms"`
	HeartbeatLatencyMs float64     `json:"heartbeat_latency_ms"`
	UploadBps          int64       `json:"upload_bps"`
	DownloadBps        int64       `json:"download_bps"`
	TotalUploadBytes   int64       `json:"total_upload_bytes,omitempty"`
	TotalDownloadBytes int64       `json:"total_download_bytes,omitempty"`
	LastSyncAt         time.Time   `json:"last_sync_at"`
	IOReadErrors       int64       `json:"io_read_errors"`
	IOWriteErrors      int64       `json:"io_write_errors"`
	IOTotalErrors      int64       `json:"io_total_errors"`
	ZeroCopyCount      int                         `json:"zero_copy_count,omitempty"`
	ZeroCopyBytes      int64                       `json:"zero_copy_bytes,omitempty"`
	MountCount         int                         `json:"mount_count,omitempty"`
	ClipboardEnabled   bool                        `json:"clipboard_enabled"`
	Plugins            []plugins.CapabilitySummary `json:"plugins,omitempty"`
	Online             bool                        `json:"online"`
	IsSelf             bool        `json:"is_self"`
	LastSeen           time.Time   `json:"last_seen"`
}

type PoolSummary struct {
	EntryURL           string       `json:"entry_url"`
	EntryVersion       string       `json:"entry_version,omitempty"`
	AutoSyncWorkers    bool         `json:"auto_sync_workers"`
	PrimaryNodeID      string       `json:"primary_node_id,omitempty"`
	VersionConsistent  bool         `json:"version_consistent"`
	MismatchedNodes    []string     `json:"mismatched_nodes,omitempty"`
	HighestNodeVersion string       `json:"highest_node_version,omitempty"`
	HighestNodeID      string       `json:"highest_node_id,omitempty"`
	TotalNodes         int          `json:"total_nodes"`
	OnlineNodes        int          `json:"online_nodes"`
	TotalQuotaBytes    int64        `json:"total_quota_bytes"`
	TotalUsedBytes     int64        `json:"total_used_bytes"`
	TotalFreeBytes     int64        `json:"total_free_bytes"`
	TotalFileCount     int          `json:"total_file_count"`
	Nodes              []NodeStatus `json:"nodes"`
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
	status             NodeStatus
	files              []storage.FileRecord
	wasOnline          bool
	lastProbed         time.Time
	lastUpgradeSync    time.Time
	lastBandwidthProbe time.Time
}

type Manager struct {
	mu                  sync.RWMutex
	cfgPath             string
	cfg                 Config
	store               *storage.Store
	pluginMgr           *plugins.Manager
	selfURL             string
	discoveredEntryURL  string
	peers               map[string]*peerRecord
	discovered          map[string]DiscoveredNode
	apiClient           *http.Client
	streamClient        *http.Client
	hbTrigger           chan struct{}
	parentCtx           context.Context
	loopCancel          context.CancelFunc
	onPeerChange        func(nodeName string, online bool)
	onPeerVersionSync   func(peer NodeStatus)
	onPeerVersionAhead  func(peer NodeStatus)
	onEntryVersionAhead func(entryURL, entryVersion string)
	onClipboardToggle   func(enabled bool)
	lastSelfSyncAttempt time.Time
	lastHBLatencyMs     float64
	lastSyncAt          time.Time
}

func (m *Manager) SetPluginManager(pm *plugins.Manager) {
	m.mu.Lock()
	m.pluginMgr = pm
	m.mu.Unlock()
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
	if opts.ClipboardSet {
		cfg.ClipboardEnabled = opts.ClipboardEnabled
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

func (m *Manager) ExposeIPs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.cfg.ExposeIPs) == 0 {
		return nil
	}
	cp := make([]string, len(m.cfg.ExposeIPs))
	copy(cp, m.cfg.ExposeIPs)
	return cp
}

func (m *Manager) DisabledIPs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.cfg.DisabledIPs) == 0 {
		return nil
	}
	cp := make([]string, len(m.cfg.DisabledIPs))
	copy(cp, m.cfg.DisabledIPs)
	return cp
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
	return m.SetEndpointNICsConfig(exposeIP, nil, nil, false, port, newSelfURL)
}

func (m *Manager) SetEndpointNICsConfig(exposeIP string, exposeIPs, disabledIPs []string, updateNICSelection bool, port, newSelfURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.ExposeIP = strings.TrimSpace(exposeIP)
	if updateNICSelection {
		m.cfg.ExposeIPs = normalizeCleanStrings(exposeIPs)
		m.cfg.DisabledIPs = normalizeCleanStrings(disabledIPs)
	}
	if strings.TrimSpace(port) != "" {
		m.cfg.Port = strings.TrimSpace(port)
	}
	if newSelfURL != "" {
		m.selfURL = strings.TrimRight(strings.TrimSpace(newSelfURL), "/")
	}
	return m.saveLocked()
}

func normalizeCleanStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
	hbLat := m.lastHBLatencyMs
	lastSync := m.lastSyncAt
	pm := m.pluginMgr
	m.mu.RUnlock()

	var localPlugins []plugins.CapabilitySummary
	if pm != nil {
		localPlugins = pm.Capabilities()
	}

	now := time.Now()
	if lastSync.IsZero() {
		lastSync = now
	}
	if hbLat <= 0 {
		hbLat = 0.3
	}

	cnt, used := m.store.LocalStats()
	quotaBytes := cfg.QuotaBytes
	if quotaBytes <= 0 {
		quotaBytes = DefaultQuotaBytes
	}
	free := quotaBytes - used
	if free < 0 {
		free = 0
	}
	storageDir := m.store.UploadDir()
	drives := ListDrives()
	hm := collectHostMetrics(storageDir)
	if hm.DiskTotalBytes <= 0 && len(drives) > 0 {
		dTotal, dUsed, dFree, dPct := deriveDiskMetricsFromDrives(storageDir, drives)
		hm.DiskTotalBytes = dTotal
		hm.DiskUsedBytes = dUsed
		hm.DiskFreeBytes = dFree
		hm.DiskPercent = dPct
	}
	rErr, wErr := m.store.IOStats()
	upBps, downBps, totalUp, totalDown := m.store.TransferStats()
	hlCnt, refCnt, mntFileCnt, zcBytes := m.store.ZeroCopyStats()
	zcCount := hlCnt + refCnt + mntFileCnt
	mountEntries := len(m.store.ListMounts())
	netLat := 0.2
	if cfg.Role == RoleWorker && hbLat > 0 {
		netLat = hbLat
	}

	return NodeStatus{
		NodeID:             cfg.NodeID,
		NodeName:           cfg.NodeName,
		Role:               cfg.Role,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		Version:            version.Version,
		HTTPURL:            selfURL,
		QuotaGB:            float64(quotaBytes) / float64(GB),
		QuotaBytes:         quotaBytes,
		UsedBytes:          used,
		FreeBytes:          free,
		FileCount:          cnt,
		StorageDir:         storageDir,
		Drives:             drives,
		CPUPercent:         hm.CPUPercent,
		CPUCores:           hm.CPUCores,
		CPUModel:           hm.CPUModel,
		GPUName:            hm.GPUName,
		HasGPU:             hm.HasGPU,
		LANLinkMbps:        hm.LANLinkMbps,
		MemPercent:         hm.MemPercent,
		MemUsedBytes:       hm.MemUsedBytes,
		MemTotalBytes:      hm.MemTotalBytes,
		DiskTotalBytes:     hm.DiskTotalBytes,
		DiskUsedBytes:      hm.DiskUsedBytes,
		DiskFreeBytes:      hm.DiskFreeBytes,
		DiskPercent:        hm.DiskPercent,
		NetLatencyMs:       netLat,
		HeartbeatLatencyMs: hbLat,
		UploadBps:          upBps,
		DownloadBps:        downBps,
		TotalUploadBytes:   totalUp,
		TotalDownloadBytes: totalDown,
		LastSyncAt:         lastSync,
		IOReadErrors:       rErr,
		IOWriteErrors:      wErr,
		IOTotalErrors:      rErr + wErr,
		ZeroCopyCount:      zcCount,
		ZeroCopyBytes:      zcBytes,
		MountCount:         mountEntries,
		ClipboardEnabled:   cfg.ClipboardEnabled,
		Plugins:            localPlugins,
		Online:             true,
		IsSelf:             true,
		LastSeen:           now,
	}
}

func deriveDiskMetricsFromDrives(storageDir string, drives []DriveInfo) (total, used, free int64, pct float64) {
	if len(drives) == 0 {
		return 0, 0, 0, 0
	}
	chosen := drives[0]
	cleanDir := strings.ToLower(strings.TrimSpace(storageDir))
	bestMatchLen := -1
	for _, d := range drives {
		p := strings.ToLower(strings.TrimSpace(d.Path))
		if p != "" && strings.HasPrefix(cleanDir, p) && len(p) > bestMatchLen {
			chosen = d
			bestMatchLen = len(p)
		}
	}
	total = chosen.TotalBytes
	free = chosen.FreeBytes
	if total > 0 {
		used = max(0, total-free)
		pct = math.Round((float64(used)*100.0/float64(total))*10) / 10
	}
	return total, used, free, pct
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
	n.LastSyncAt = now
	n.IOTotalErrors = n.IOReadErrors + n.IOWriteErrors
	if n.HTTPURL != "" {
		n.HTTPURL = strings.TrimRight(n.HTTPURL, "/")
	}
	if n.DiskTotalBytes <= 0 && len(n.Drives) > 0 {
		n.DiskTotalBytes, n.DiskUsedBytes, n.DiskFreeBytes, n.DiskPercent = deriveDiskMetricsFromDrives(n.StorageDir, n.Drives)
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
	m.lastSyncAt = now
	if len(m.cfg.EvictedPeers) > 0 {
		if _, evicted := m.cfg.EvictedPeers[n.NodeID]; evicted {
			if n.HTTPURL != "" {
				m.discovered[n.NodeID] = DiscoveredNode{
					NodeID:        n.NodeID,
					NodeName:      n.NodeName,
					Role:          n.Role,
					OS:            n.OS,
					Arch:          n.Arch,
					Version:       n.Version,
					URL:           n.HTTPURL,
					QuotaBytes:    n.QuotaBytes,
					UsedBytes:     n.UsedBytes,
					FileCount:     n.FileCount,
					Online:        true,
					AlreadyInPool: false,
					Source:        "udp",
					LastSeen:      now,
				}
			}
			m.mu.Unlock()
			return false
		}
	}
	prev, exists := m.peers[n.NodeID]
	var lastProbed time.Time
	var lastUpgradeSync time.Time
	var lastBandwidthProbe time.Time
	if exists {
		lastProbed = prev.lastProbed
		lastUpgradeSync = prev.lastUpgradeSync
		lastBandwidthProbe = prev.lastBandwidthProbe
		if n.Version == "" {
			n.Version = prev.status.Version
		}
		if n.LANBandwidthMbps <= 0 && prev.status.LANBandwidthMbps > 0 {
			n.LANBandwidthMbps = prev.status.LANBandwidthMbps
			n.BandwidthProbedAt = prev.status.BandwidthProbedAt
		}
		if n.CPUModel == "" {
			n.CPUModel = prev.status.CPUModel
		}
		if n.GPUName == "" {
			n.GPUName = prev.status.GPUName
			n.HasGPU = prev.status.HasGPU
		}
		if n.LANLinkMbps <= 0 {
			n.LANLinkMbps = prev.status.LANLinkMbps
		}
		if n.NetLatencyMs <= 0 {
			n.NetLatencyMs = prev.status.NetLatencyMs
		}
		if n.HeartbeatLatencyMs <= 0 {
			if !prev.status.LastSeen.IsZero() {
				gapMs := float64(now.Sub(prev.status.LastSeen).Microseconds()) / 1000.0
				if prev.status.NetLatencyMs > 0 {
					n.HeartbeatLatencyMs = prev.status.NetLatencyMs
				} else if gapMs > 0 && gapMs < 500 {
					n.HeartbeatLatencyMs = math.Round(gapMs*10) / 10
				}
			}
			if n.HeartbeatLatencyMs <= 0 {
				n.HeartbeatLatencyMs = prev.status.HeartbeatLatencyMs
			}
		}
	}
	needProbe := n.HTTPURL != "" && (n.Version == "" || n.NetLatencyMs <= 0 || now.Sub(lastProbed) > 9*time.Second)
	if needProbe {
		lastProbed = now
	}
	autoSyncEnabled := m.cfg.AutoSyncWorkers == nil || *m.cfg.AutoSyncWorkers
	var syncCb func(peer NodeStatus)
	needVersionSync := false
	if m.cfg.Role == RoleEntry && autoSyncEnabled && n.HTTPURL != "" && n.Version != "" && now.Sub(lastUpgradeSync) > 25*time.Second {
		cmp := compareClusterVersions(n.Version, version.Version)
		if cmp < 0 && m.onPeerVersionSync != nil {
			needVersionSync = true
			syncCb = m.onPeerVersionSync
			lastUpgradeSync = now
		} else if cmp > 0 && m.onPeerVersionAhead != nil {
			needVersionSync = true
			syncCb = m.onPeerVersionAhead
			lastUpgradeSync = now
		}
	}
	changed := !exists ||
		!prev.wasOnline ||
		now.Sub(prev.status.LastSeen) > PeerTimeout ||
		prev.status.NodeName != n.NodeName ||
		prev.status.Version != n.Version ||
		prev.status.QuotaBytes != n.QuotaBytes ||
		prev.status.UsedBytes != n.UsedBytes ||
		prev.status.FileCount != n.FileCount ||
		prev.status.StorageDir != n.StorageDir ||
		prev.status.ZeroCopyCount != n.ZeroCopyCount ||
		prev.status.MountCount != n.MountCount ||
		prev.status.ClipboardEnabled != n.ClipboardEnabled ||
		!equalPluginCapabilities(prev.status.Plugins, n.Plugins) ||
		len(prev.files) != len(files)
	if !changed {
		for i := range files {
			if prev.files[i].ID != files[i].ID ||
				prev.files[i].Version != files[i].Version ||
				prev.files[i].VersionCount != files[i].VersionCount ||
				prev.files[i].StorageDir != files[i].StorageDir ||
				prev.files[i].StorageMode != files[i].StorageMode ||
				prev.files[i].SourceMissing != files[i].SourceMissing ||
				prev.files[i].Size != files[i].Size ||
				!equalStringSlice(prev.files[i].AllowedUsers, files[i].AllowedUsers) {
				changed = true
				break
			}
		}
	}
	m.peers[n.NodeID] = &peerRecord{
		status:             n,
		files:              files,
		wasOnline:          true,
		lastProbed:         lastProbed,
		lastUpgradeSync:    lastUpgradeSync,
		lastBandwidthProbe: lastBandwidthProbe,
	}
	m.mu.Unlock()
	if needProbe {
		go m.refreshPeerTelemetry(n.NodeID, n.HTTPURL)
	}
	if needVersionSync {
		go syncCb(n)
	}
	return changed
}

func (m *Manager) refreshPeerTelemetry(nodeID, httpURL string) {
	cleanURL := strings.TrimRight(strings.TrimSpace(httpURL), "/")
	if cleanURL == "" {
		return
	}
	t0 := time.Now()
	req, err := http.NewRequest(http.MethodGet, cleanURL+"/api/info", nil)
	if err != nil {
		return
	}
	resp, err := newDirectClusterClient(2 * time.Second).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	rttMs := math.Round((float64(time.Since(t0).Microseconds())/1000.0)*10) / 10
	if rttMs <= 0 {
		rttMs = 0.2
	}
	var info struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&info)

	var syncCb func(peer NodeStatus)
	var peerSnap NodeStatus
	var bwPeer NodeStatus
	needBWProbe := false
	m.mu.Lock()
	if p, ok := m.peers[nodeID]; ok {
		p.status.NetLatencyMs = rttMs
		if p.status.HeartbeatLatencyMs <= 0 {
			p.status.HeartbeatLatencyMs = rttMs
		}
		if p.status.Version == "" && info.Version != "" {
			p.status.Version = info.Version
		}
		if m.cfg.Role == RoleEntry && p.status.LANBandwidthMbps <= 0 && time.Since(p.lastBandwidthProbe) > 5*time.Minute && p.status.HTTPURL != "" {
			p.lastBandwidthProbe = time.Now()
			needBWProbe = true
			bwPeer = p.status
		}
		autoSyncEnabled := m.cfg.AutoSyncWorkers == nil || *m.cfg.AutoSyncWorkers
		if m.cfg.Role == RoleEntry &&
			autoSyncEnabled &&
			p.status.Version != "" &&
			time.Since(p.lastUpgradeSync) > 25*time.Second {
			cmp := compareClusterVersions(p.status.Version, version.Version)
			if cmp < 0 && m.onPeerVersionSync != nil {
				p.lastUpgradeSync = time.Now()
				syncCb = m.onPeerVersionSync
				peerSnap = p.status
			} else if cmp > 0 && m.onPeerVersionAhead != nil {
				p.lastUpgradeSync = time.Now()
				syncCb = m.onPeerVersionAhead
				peerSnap = p.status
			}
		}
	}
	m.mu.Unlock()
	if needBWProbe {
		go func(target NodeStatus) {
			_, _, _ = m.ProbePeerBandwidth(target, 2<<20)
		}(bwPeer)
	}
	if syncCb != nil {
		go syncCb(peerSnap)
	}
}

// BandwidthProbeResult reports LAN link speed and active measured HTTP throughput to a cluster node.
type BandwidthProbeResult struct {
	NodeID        string  `json:"node_id"`
	NodeName      string  `json:"node_name"`
	BandwidthMbps float64 `json:"bandwidth_mbps"`
	LinkMbps      int64   `json:"link_mbps,omitempty"`
	Bytes         int64   `json:"bytes"`
	DurationMs    int64   `json:"duration_ms"`
	OK            bool    `json:"ok"`
	Error         string  `json:"error,omitempty"`
}

// ProbePeerBandwidth streams sizeBytes from a peer's /internal/cluster/speedtest endpoint to measure real LAN throughput in Mbps.
func (m *Manager) ProbePeerBandwidth(peer NodeStatus, sizeBytes int64) (float64, int64, error) {
	peerURL := strings.TrimRight(strings.TrimSpace(peer.HTTPURL), "/")
	if peerURL == "" {
		return 0, 0, errors.New("节点缺少有效 HTTP 地址")
	}
	if sizeBytes < 256*1024 {
		sizeBytes = 256 * 1024
	}
	if sizeBytes > 16<<20 {
		sizeBytes = 16 << 20
	}
	target := fmt.Sprintf("%s/internal/cluster/speedtest?bytes=%d", peerURL, sizeBytes)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return 0, 0, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	t0 := time.Now()
	resp, err := newDirectClusterClient(15 * time.Second).Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("测速端点返回 HTTP %d", resp.StatusCode)
	}
	n, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<20))
	if copyErr != nil {
		return 0, 0, copyErr
	}
	elapsed := time.Since(t0)
	if elapsed < 100*time.Microsecond {
		elapsed = 100 * time.Microsecond
	}
	if n <= 0 {
		return 0, 0, errors.New("测速响应体为空")
	}
	mbps := math.Round((float64(n)*8.0/(elapsed.Seconds()*1_000_000.0))*10) / 10
	if math.IsNaN(mbps) || math.IsInf(mbps, 0) {
		mbps = 0
	}
	now := time.Now()
	m.mu.Lock()
	if p, ok := m.peers[peer.NodeID]; ok {
		p.status.LANBandwidthMbps = mbps
		p.status.BandwidthProbedAt = &now
		p.lastBandwidthProbe = now
	}
	m.mu.Unlock()
	return mbps, elapsed.Milliseconds(), nil
}

// ProbeClusterBandwidths measures real LAN throughput to every online peer node in the cluster.
func (m *Manager) ProbeClusterBandwidths(sizeBytes int64) []BandwidthProbeResult {
	if sizeBytes <= 0 {
		sizeBytes = 4 << 20
	}
	pool := m.PoolSummaryForRole(true)
	results := make([]BandwidthProbeResult, 0, len(pool.Nodes))
	for _, n := range pool.Nodes {
		if !n.Online {
			continue
		}
		if n.IsSelf {
			results = append(results, BandwidthProbeResult{
				NodeID:        n.NodeID,
				NodeName:      n.NodeName,
				BandwidthMbps: n.LANBandwidthMbps,
				LinkMbps:      n.LANLinkMbps,
				OK:            true,
			})
			continue
		}
		mbps, durMs, err := m.ProbePeerBandwidth(n, sizeBytes)
		res := BandwidthProbeResult{
			NodeID:        n.NodeID,
			NodeName:      n.NodeName,
			BandwidthMbps: mbps,
			LinkMbps:      n.LANLinkMbps,
			Bytes:         sizeBytes,
			DurationMs:    durMs,
			OK:            err == nil,
		}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

func (m *Manager) SetOnPeerChange(fn func(nodeName string, online bool)) {
	m.mu.Lock()
	m.onPeerChange = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnPeerVersionSync(fn func(peer NodeStatus)) {
	m.mu.Lock()
	m.onPeerVersionSync = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnPeerVersionAhead(fn func(peer NodeStatus)) {
	m.mu.Lock()
	m.onPeerVersionAhead = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnEntryVersionAhead(fn func(entryURL, entryVersion string)) {
	m.mu.Lock()
	m.onEntryVersionAhead = fn
	m.mu.Unlock()
}

func (m *Manager) SetOnClipboardToggle(fn func(enabled bool)) {
	m.mu.Lock()
	m.onClipboardToggle = fn
	m.mu.Unlock()
}

func (m *Manager) ClipboardEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ClipboardEnabled
}

func (m *Manager) SetLocalClipboardEnabled(enabled bool) (NodeStatus, error) {
	m.mu.Lock()
	m.cfg.ClipboardEnabled = enabled
	cb := m.onClipboardToggle
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return NodeStatus{}, err
	}
	if cb != nil {
		cb(enabled)
	}
	return m.SelfStatus(), nil
}

func (m *Manager) AutoSyncWorkers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.AutoSyncWorkers == nil {
		return true
	}
	return *m.cfg.AutoSyncWorkers
}

func (m *Manager) SetAutoSyncWorkers(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	val := enabled
	m.cfg.AutoSyncWorkers = &val
	return m.saveLocked()
}

func compareClusterVersions(a, b string) int {
	pa := parseClusterVerParts(a)
	pb := parseClusterVerParts(b)
	n := max(len(pa), len(pb))
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

func parseClusterVerParts(raw string) []int {
	s := strings.TrimLeft(strings.TrimSpace(raw), "vV")
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return []int{0}
	}
	segs := strings.Split(s, ".")
	out := make([]int, 0, len(segs))
	for _, seg := range segs {
		digits := ""
		for _, r := range strings.TrimSpace(seg) {
			if r >= '0' && r <= '9' {
				digits += string(r)
			} else {
				break
			}
		}
		v, _ := strconv.Atoi(digits)
		out = append(out, v)
	}
	return out
}

// PushBinaryToPeer pushes a verified binary directly over LAN to an online worker peer for zero-downtime hot upgrade.
func (m *Manager) PushBinaryToPeer(peer NodeStatus, binPath, sha256Hex, targetVersion string) error {
	peerURL := strings.TrimRight(strings.TrimSpace(peer.HTTPURL), "/")
	if peerURL == "" {
		return errors.New("目标从节点缺少有效 HTTP 地址")
	}
	f, err := os.Open(binPath)
	if err != nil {
		return fmt.Errorf("打开主节点二进制程序失败: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, peerURL+"/internal/cluster/upgrade-binary", f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-LFH-Target-Version", targetVersion)
	req.Header.Set("X-LFH-Binary-SHA256", sha256Hex)
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := newDirectClusterClient(90 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("推送新版本到节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		curVer := peer.Version
		if curVer == "" {
			curVer = "V0.11.21.4"
		}
		return fmt.Errorf("节点 %s 当前运行旧版 (%s)，尚未内置远程版本热接收接口；请先在该电脑替换一次新版程序后即可开启永久自动同步", peer.NodeName, curVer)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return errors.New(errBody.Error)
		}
		return fmt.Errorf("节点 %s 返回状态 %d: %s", peer.NodeName, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// TriggerPeerGitHubUpgrade instructs an online worker peer (e.g. different OS/Arch) to switch version from GitHub Release.
func (m *Manager) TriggerPeerGitHubUpgrade(peer NodeStatus, repo, proxyURL, targetVersion string, allowDowngrade bool) error {
	peerURL := strings.TrimRight(strings.TrimSpace(peer.HTTPURL), "/")
	if peerURL == "" {
		return errors.New("目标从节点缺少有效 HTTP 地址")
	}
	payload, _ := json.Marshal(map[string]any{
		"repo":            repo,
		"proxy_url":       proxyURL,
		"target_version":  targetVersion,
		"allow_downgrade": allowDowngrade,
	})
	req, err := http.NewRequest(http.MethodPost, peerURL+"/internal/cluster/upgrade", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := newDirectClusterClient(25 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("触发节点 %s 在线升级失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		curVer := peer.Version
		if curVer == "" {
			curVer = "V0.11.21.4"
		}
		return fmt.Errorf("节点 %s 当前运行旧版 (%s)，请先在该电脑更新至 V0.11.21.7+ 后即可自动同步版本", peer.NodeName, curVer)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return errors.New(errBody.Error)
		}
		return fmt.Errorf("节点 %s 升级失败 (%d)", peer.NodeName, resp.StatusCode)
	}
	return nil
}

// PullBinaryFromPeer downloads a matching platform binary from any cluster node over LAN.
func (m *Manager) PullBinaryFromPeer(peerURL, targetOS, targetArch string) (io.ReadCloser, string, string, error) {
	cleanPeer := strings.TrimRight(strings.TrimSpace(peerURL), "/")
	if cleanPeer == "" {
		return nil, "", "", errors.New("未找到目标节点地址")
	}
	goos := strings.TrimSpace(targetOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(targetArch)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	target := fmt.Sprintf("%s/internal/cluster/binary?os=%s&arch=%s", cleanPeer, goos, goarch)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := newDirectClusterClient(90 * time.Second).Do(req)
	if err != nil {
		return nil, "", "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, "", "", fmt.Errorf("从节点拉取二进制程序失败 (HTTP %d)", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("X-LFH-Target-Version"), resp.Header.Get("X-LFH-Binary-SHA256"), nil
}

// PullBinaryFromEntry downloads the matching platform binary directly from the Primary node over LAN.
func (m *Manager) PullBinaryFromEntry(entryURL string) (io.ReadCloser, string, string, error) {
	cleanEntry := strings.TrimRight(strings.TrimSpace(entryURL), "/")
	if cleanEntry == "" {
		cleanEntry = strings.TrimRight(strings.TrimSpace(m.EntryURL()), "/")
	}
	if cleanEntry == "" {
		return nil, "", "", errors.New("未找到主节点地址")
	}
	return m.PullBinaryFromPeer(cleanEntry, runtime.GOOS, runtime.GOARCH)
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
	role := m.cfg.Role
	entryURL := m.selfURL
	if role == RoleWorker {
		if m.discoveredEntryURL != "" {
			entryURL = m.discoveredEntryURL
		} else {
			entryURL = m.cfg.EntryHint
		}
	}
	primaryID := strings.TrimSpace(m.cfg.PrimaryNodeID)
	if role == RoleEntry {
		if primaryID != "" && primaryID != self.NodeID {
			if _, ok := m.peers[primaryID]; !ok {
				primaryID = self.NodeID
			}
		} else {
			primaryID = self.NodeID
		}
	}
	peers := make([]NodeStatus, 0, len(m.peers))
	for _, p := range m.peers {
		st := p.status
		st.IsSelf = false
		st.Online = now.Sub(st.LastSeen) <= PeerTimeout
		if role == RoleEntry {
			if st.NodeID == primaryID {
				st.Role = RoleEntry
			} else {
				st.Role = RoleWorker
			}
		}
		peers = append(peers, st)
	}
	m.mu.RUnlock()

	if role == RoleEntry {
		if primaryID == self.NodeID {
			self.Role = RoleEntry
		} else {
			self.Role = RoleWorker
		}
	}

	sort.Slice(peers, func(i, j int) bool {
		if (peers[i].NodeID == primaryID) != (peers[j].NodeID == primaryID) {
			return peers[i].NodeID == primaryID
		}
		if peers[i].Online != peers[j].Online {
			return peers[i].Online
		}
		if peers[i].NodeName != peers[j].NodeName {
			return peers[i].NodeName < peers[j].NodeName
		}
		return peers[i].NodeID < peers[j].NodeID
	})

	nodes := make([]NodeStatus, 0, 1+len(peers))
	if role == RoleEntry && primaryID != self.NodeID && len(peers) > 0 && peers[0].NodeID == primaryID {
		nodes = append(nodes, peers[0])
		nodes = append(nodes, self)
		nodes = append(nodes, peers[1:]...)
	} else {
		nodes = append(nodes, self)
		nodes = append(nodes, peers...)
	}

	summary := PoolSummary{
		EntryURL:        entryURL,
		EntryVersion:    version.Version,
		AutoSyncWorkers: m.AutoSyncWorkers(),
		PrimaryNodeID:   primaryID,
		TotalNodes:      len(nodes),
		Nodes:           nodes,
	}
	highestVer := version.Version
	highestNodeID := self.NodeID
	for _, n := range nodes {
		if n.Online {
			summary.OnlineNodes++
			summary.TotalQuotaBytes += n.QuotaBytes
			summary.TotalUsedBytes += n.UsedBytes
			summary.TotalFreeBytes += n.FreeBytes
			summary.TotalFileCount += n.FileCount
			if n.Version != "" && compareClusterVersions(n.Version, highestVer) > 0 {
				highestVer = n.Version
				highestNodeID = n.NodeID
			}
		}
	}
	var mismatched []string
	for _, n := range nodes {
		if !n.Online {
			continue
		}
		ver := strings.TrimSpace(n.Version)
		if ver == "" {
			ver = "V0.11.21.4"
		}
		if compareClusterVersions(ver, highestVer) != 0 {
			mismatched = append(mismatched, n.NodeID)
		}
	}
	summary.VersionConsistent = len(mismatched) == 0
	summary.MismatchedNodes = mismatched
	summary.HighestNodeVersion = highestVer
	summary.HighestNodeID = highestNodeID
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

func isQuotaCountedRecord(rec storage.FileRecord) bool {
	return rec.StorageMode == "" || rec.StorageMode == storage.StorageModeCopy
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
	for i, existing := range p.files {
		if existing.ID == rec.ID {
			if isQuotaCountedRecord(existing) {
				p.status.UsedBytes = max(0, p.status.UsedBytes-existing.Size)
			}
			if isQuotaCountedRecord(rec) {
				p.status.UsedBytes += rec.Size
			}
			p.status.FreeBytes = max(0, p.status.QuotaBytes-p.status.UsedBytes)
			p.files[i] = rec
			return
		}
	}
	p.files = append([]storage.FileRecord{rec}, p.files...)
	p.status.FileCount++
	if isQuotaCountedRecord(rec) {
		p.status.UsedBytes += rec.Size
	} else {
		p.status.ZeroCopyCount++
		p.status.ZeroCopyBytes += rec.Size
	}
	p.status.FreeBytes = max(0, p.status.QuotaBytes-p.status.UsedBytes)
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
			if isQuotaCountedRecord(f) {
				p.status.UsedBytes = max(0, p.status.UsedBytes-f.Size)
			} else {
				p.status.ZeroCopyCount = max(0, p.status.ZeroCopyCount-1)
				p.status.ZeroCopyBytes = max(0, p.status.ZeroCopyBytes-f.Size)
			}
			p.status.FreeBytes = max(0, p.status.QuotaBytes-p.status.UsedBytes)
			break
		}
	}
}

func (m *Manager) RemovePeerMountFiles(nodeID, mountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[nodeID]
	if !ok {
		return
	}
	kept := make([]storage.FileRecord, 0, len(p.files))
	for _, f := range p.files {
		if f.MountID == mountID {
			p.status.FileCount = max(0, p.status.FileCount-1)
			p.status.ZeroCopyCount = max(0, p.status.ZeroCopyCount-1)
			p.status.ZeroCopyBytes = max(0, p.status.ZeroCopyBytes-f.Size)
			continue
		}
		kept = append(kept, f)
	}
	p.files = kept
	p.status.MountCount = max(0, p.status.MountCount-1)
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
		} else if (n.Role == RoleEntry) != (best.Role == RoleEntry) {
			if n.Role == RoleEntry {
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

var seenNonceCache = struct {
	mu   sync.Mutex
	seen map[string]int64
}{
	seen: make(map[string]int64),
}

func markNonceFresh(key string, nowUnix int64) bool {
	const ttlSeconds = int64(360)
	seenNonceCache.mu.Lock()
	defer seenNonceCache.mu.Unlock()
	if exp, ok := seenNonceCache.seen[key]; ok && exp >= nowUnix {
		return false
	}
	seenNonceCache.seen[key] = nowUnix + ttlSeconds
	if len(seenNonceCache.seen) > 2048 {
		for k, exp := range seenNonceCache.seen {
			if exp < nowUnix {
				delete(seenNonceCache.seen, k)
			}
		}
	}
	return true
}

func SignRequest(req *http.Request, secret, nodeID string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	var b [12]byte
	_, _ = rand.Read(b[:])
	nonce := hex.EncodeToString(b[:])
	sig := computeSignature(secret, req.Method, req.URL.Path, ts, nodeID, nonce)
	req.Header.Set(hdrNodeID, nodeID)
	req.Header.Set(hdrTimestamp, ts)
	req.Header.Set(hdrNonce, nonce)
	req.Header.Set(hdrSignature, sig)
}

func VerifyRequest(r *http.Request, secret string) bool {
	nodeID := strings.TrimSpace(r.Header.Get(hdrNodeID))
	tsStr := strings.TrimSpace(r.Header.Get(hdrTimestamp))
	sig := strings.TrimSpace(r.Header.Get(hdrSignature))
	nonce := strings.TrimSpace(r.Header.Get(hdrNonce))
	if nodeID == "" || tsStr == "" || sig == "" {
		return false
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}
	nowUnix := time.Now().Unix()
	diff := nowUnix - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		return false
	}
	expected := computeSignature(secret, r.Method, r.URL.Path, tsStr, nodeID, nonce)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return false
	}
	if nonce != "" {
		return markNonceFresh(nodeID+"|"+nonce, nowUnix)
	}
	return true
}

func computeSignature(secret, method, path, ts, nodeID, nonce string) string {
	payload := method + "|" + path + "|" + ts + "|" + nodeID
	if nonce != "" {
		payload += "|" + nonce
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// ==================== Cross-Node HTTP Proxy Helpers ====================

func decodePeerError(statusCode int, body []byte, fallback string) error {
	var envelope struct {
		Error      string                    `json:"error"`
		Code       string                    `json:"code"`
		Conflict   *storage.ConflictError    `json:"conflict"`
		Lease      *storage.LeaseLockedError `json:"lease"`
		PatchError *storage.PatchError       `json:"patch_error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if (envelope.Code == "version_conflict" || statusCode == http.StatusConflict) && envelope.Conflict != nil {
			return envelope.Conflict
		}
		if (envelope.Code == "lease_locked" || statusCode == http.StatusLocked) && envelope.Lease != nil {
			return envelope.Lease
		}
		if (envelope.Code == "patch_failed" || statusCode == http.StatusUnprocessableEntity) && envelope.PatchError != nil {
			return envelope.PatchError
		}
		if envelope.Error != "" {
			return errors.New(envelope.Error)
		}
	}
	return errors.New(fallback)
}

func (m *Manager) ProxyUpload(peer NodeStatus, filename, folderPath string, r io.Reader, uploader string, allowedUsers []string, dev storage.DeviceInfo) (storage.FileRecord, error) {
	return m.ProxyUploadWithConcurrency(peer, filename, folderPath, r, uploader, allowedUsers, dev, storage.WriteConcurrencyOptions{})
}

func (m *Manager) ProxyUploadWithConcurrency(peer NodeStatus, filename, folderPath string, r io.Reader, uploader string, allowedUsers []string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.FileRecord, error) {
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
		if conc.ExpectedVersion != nil {
			_ = mw.WriteField("expected_version", strconv.Itoa(*conc.ExpectedVersion))
		}
		if conc.ActorKeyID != "" {
			_ = mw.WriteField("actor_key_id", conc.ActorKeyID)
		}
		if conc.ForceLease {
			_ = mw.WriteField("force_lease", "true")
		}
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
		return storage.FileRecord{}, decodePeerError(resp.StatusCode, body, fmt.Sprintf("节点 %s 返回状态 %d", peer.NodeName, resp.StatusCode))
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
	n, _ := io.Copy(w, resp.Body)
	if n > 0 && m.store != nil {
		m.store.AddDownloadBytes(n)
	}
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
	return m.ProxyDeleteWithConcurrency(peer, fileID, storage.WriteConcurrencyOptions{})
}

func (m *Manager) ProxyDeleteWithConcurrency(peer NodeStatus, fileID string, conc storage.WriteConcurrencyOptions) error {
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + fileID
	req, err := http.NewRequest(http.MethodDelete, targetURL, nil)
	if err != nil {
		return err
	}
	if conc.ExpectedVersion != nil {
		req.Header.Set("X-Expected-Version", strconv.Itoa(*conc.ExpectedVersion))
	}
	if conc.ActorKeyID != "" {
		req.Header.Set("X-Actor-Key-ID", conc.ActorKeyID)
	}
	if conc.ForceLease {
		req.Header.Set("X-Force-Lease", "true")
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
		return decodePeerError(resp.StatusCode, body, fmt.Sprintf("远端节点删除失败 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	m.RemovePeerFile(peer.NodeID, fileID)
	return nil
}

func (m *Manager) ProxyNewVersion(peer NodeStatus, fileID, filename, note string, r io.Reader, uploader string, dev storage.DeviceInfo) (storage.FileRecord, error) {
	return m.ProxyNewVersionWithConcurrency(peer, fileID, filename, note, r, uploader, dev, storage.WriteConcurrencyOptions{})
}

func (m *Manager) ProxyNewVersionWithConcurrency(peer NodeStatus, fileID, filename, note string, r io.Reader, uploader string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.FileRecord, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		_ = mw.WriteField("uploader", uploader)
		_ = mw.WriteField("note", note)
		_ = mw.WriteField("device_fp", dev.FP)
		_ = mw.WriteField("device_name", dev.Name)
		_ = mw.WriteField("device_ip", dev.IP)
		_ = mw.WriteField("device_detail", dev.Detail)
		if conc.ExpectedVersion != nil {
			_ = mw.WriteField("expected_version", strconv.Itoa(*conc.ExpectedVersion))
		}
		if conc.ActorKeyID != "" {
			_ = mw.WriteField("actor_key_id", conc.ActorKeyID)
		}
		if conc.ForceLease {
			_ = mw.WriteField("force_lease", "true")
		}

		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + fileID + "/versions"
	req, err := http.NewRequest(http.MethodPost, targetURL, pr)
	if err != nil {
		_ = pr.Close()
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, fmt.Errorf("转发新版本到节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return storage.FileRecord{}, decodePeerError(resp.StatusCode, body, fmt.Sprintf("节点 %s 返回状态 %d", peer.NodeName, resp.StatusCode))
	}

	var rec storage.FileRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
}

func (m *Manager) ProxyEditFile(peer NodeStatus, fileID, content, note, uploader string, dev storage.DeviceInfo) (storage.FileRecord, error) {
	return m.ProxyEditFileWithConcurrency(peer, fileID, content, note, uploader, dev, storage.WriteConcurrencyOptions{})
}

func (m *Manager) ProxyEditFileWithConcurrency(peer NodeStatus, fileID, content, note, uploader string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.FileRecord, error) {
	reqBody := map[string]any{
		"content":       content,
		"note":          note,
		"uploader":      uploader,
		"device_fp":     dev.FP,
		"device_name":   dev.Name,
		"device_ip":     dev.IP,
		"device_detail": dev.Detail,
		"actor_key_id":  conc.ActorKeyID,
		"force_lease":   conc.ForceLease,
	}
	if conc.ExpectedVersion != nil {
		reqBody["expected_version"] = *conc.ExpectedVersion
	}
	payload, _ := json.Marshal(reqBody)
	targetURL := fmt.Sprintf("%s/internal/cluster/files/%s/edit", strings.TrimRight(peer.HTTPURL, "/"), fileID)
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, fmt.Errorf("转发在线编辑内容到节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return storage.FileRecord{}, decodePeerError(resp.StatusCode, body, fmt.Sprintf("远端节点在线保存失败 (%d)", resp.StatusCode))
	}
	var rec storage.FileRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
}

func (m *Manager) ProxyRestoreVersion(peer NodeStatus, fileID string, ver int, note, uploader string, dev storage.DeviceInfo) (storage.FileRecord, error) {
	return m.ProxyRestoreVersionWithConcurrency(peer, fileID, ver, note, uploader, dev, storage.WriteConcurrencyOptions{})
}

func (m *Manager) ProxyRestoreVersionWithConcurrency(peer NodeStatus, fileID string, ver int, note, uploader string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.FileRecord, error) {
	reqBody := map[string]any{
		"note":          note,
		"uploader":      uploader,
		"device_fp":     dev.FP,
		"device_name":   dev.Name,
		"device_ip":     dev.IP,
		"device_detail": dev.Detail,
		"actor_key_id":  conc.ActorKeyID,
		"force_lease":   conc.ForceLease,
	}
	if conc.ExpectedVersion != nil {
		reqBody["expected_version"] = *conc.ExpectedVersion
	}
	payload, _ := json.Marshal(reqBody)
	targetURL := fmt.Sprintf("%s/internal/cluster/files/%s/versions/%d/restore", strings.TrimRight(peer.HTTPURL, "/"), fileID, ver)
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return storage.FileRecord{}, decodePeerError(resp.StatusCode, body, fmt.Sprintf("远端节点回滚版本失败 (%d)", resp.StatusCode))
	}
	var rec storage.FileRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
}

func (m *Manager) ProxyGetLease(peer NodeStatus, fileID string) (storage.FileLease, bool, error) {
	var resp struct {
		Locked bool               `json:"locked"`
		Lease  *storage.FileLease `json:"lease"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodGet, "/internal/cluster/files/"+url.PathEscape(fileID)+"/lease", nil, &resp); err != nil {
		return storage.FileLease{}, false, err
	}
	if !resp.Locked || resp.Lease == nil {
		return storage.FileLease{}, false, nil
	}
	return *resp.Lease, true, nil
}

func (m *Manager) ProxyAcquireLease(peer NodeStatus, fileID, holderKeyID, holderName string, ttlSeconds int, force bool) (storage.FileLease, error) {
	reqBody := map[string]any{
		"holder_key_id": holderKeyID,
		"holder_name":   holderName,
		"ttl_seconds":   ttlSeconds,
		"force":         force,
	}
	var resp struct {
		Lease storage.FileLease `json:"lease"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/files/"+url.PathEscape(fileID)+"/lease", reqBody, &resp); err != nil {
		return storage.FileLease{}, err
	}
	return resp.Lease, nil
}

func (m *Manager) ProxyReleaseLease(peer NodeStatus, fileID, holderKeyID string, force bool) error {
	reqBody := map[string]any{
		"holder_key_id": holderKeyID,
		"force":         force,
	}
	return m.doSignedPeerJSON(peer, http.MethodDelete, "/internal/cluster/files/"+url.PathEscape(fileID)+"/lease", reqBody, nil)
}

func (m *Manager) ReadPeerFileBytes(peer NodeStatus, fileID string, maxBytes int64) ([]byte, error) {
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + url.PathEscape(fileID) + "/raw"
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("从节点 %s 读取文件内容失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return nil, errors.New("源文件已移动或删除，外部引用已失效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
		return nil, decodePeerError(resp.StatusCode, body, fmt.Sprintf("从节点 %s 读取文件失败 (%d)", peer.NodeName, resp.StatusCode))
	}

	var data []byte
	if maxBytes > 0 {
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("文件大小超过文本读取上限 (%d 字节)", maxBytes)
		}
	} else {
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
	}
	if m.store != nil && len(data) > 0 {
		m.store.AddDownloadBytes(int64(len(data)))
	}
	return data, nil
}

func (m *Manager) ProxyPatchFileWithConcurrency(peer NodeStatus, fileID string, patchReq storage.PatchRequest, method, uploader string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.FileRecord, error) {
	effExpected := conc.ExpectedVersion
	if effExpected == nil {
		effExpected = patchReq.ExpectedVersion
	}
	reqBody := map[string]any{
		"mode":          patchReq.Mode,
		"hunks":         patchReq.Hunks,
		"unified_diff":  patchReq.UnifiedDiff,
		"note":          patchReq.Note,
		"method":        method,
		"uploader":      uploader,
		"device_fp":     dev.FP,
		"device_name":   dev.Name,
		"device_ip":     dev.IP,
		"device_detail": dev.Detail,
		"actor_key_id":  conc.ActorKeyID,
		"force_lease":   conc.ForceLease,
	}
	if effExpected != nil {
		reqBody["expected_version"] = *effExpected
	}
	var rec storage.FileRecord
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/files/"+url.PathEscape(fileID)+"/patch", reqBody, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	rec.NodeID = peer.NodeID
	rec.NodeName = peer.NodeName
	rec.NodeOS = peer.OS
	rec.NodeOnline = true
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
}

func (m *Manager) ProxyBatchCommit(peer NodeStatus, req storage.BatchCommitRequest, uploader string, dev storage.DeviceInfo, conc storage.WriteConcurrencyOptions) (storage.BatchCommitResult, error) {
	reqBody := map[string]any{
		"note":          req.Note,
		"operations":    req.Operations,
		"uploader":      uploader,
		"device_fp":     dev.FP,
		"device_name":   dev.Name,
		"device_ip":     dev.IP,
		"device_detail": dev.Detail,
		"actor_key_id":  conc.ActorKeyID,
		"force_lease":   conc.ForceLease,
	}
	var res storage.BatchCommitResult
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/files/batch-commit", reqBody, &res); err != nil {
		return storage.BatchCommitResult{}, err
	}
	for i := range res.Results {
		item := &res.Results[i]
		if item.Deleted {
			m.RemovePeerFile(peer.NodeID, item.FileID)
			continue
		}
		if item.Record != nil {
			item.Record.NodeID = peer.NodeID
			item.Record.NodeName = peer.NodeName
			item.Record.NodeOS = peer.OS
			item.Record.NodeOnline = true
			m.AddPeerFile(peer.NodeID, *item.Record)
		}
	}
	return res, nil
}

// ProxyVerifyMount runs an allowlisted build/test preset on a remote online peer node.
func (m *Manager) ProxyVerifyMount(peer NodeStatus, mountID, presetID string) (bridge.VerifyResult, error) {
	targetURL := fmt.Sprintf("%s/internal/cluster/mounts/%s/verify/%s", strings.TrimRight(peer.HTTPURL, "/"), url.PathEscape(mountID), url.PathEscape(presetID))
	req, err := http.NewRequest(http.MethodPost, targetURL, nil)
	if err != nil {
		return bridge.VerifyResult{}, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := newDirectClusterClient(330 * time.Second).Do(req)
	if err != nil {
		return bridge.VerifyResult{}, fmt.Errorf("远端节点 %s 执行构建校验失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return bridge.VerifyResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bridge.VerifyResult{}, decodePeerError(resp.StatusCode, body, fmt.Sprintf("远端节点构建校验失败 (%d)", resp.StatusCode))
	}
	var out bridge.VerifyResult
	if err := json.Unmarshal(body, &out); err != nil {
		return bridge.VerifyResult{}, err
	}
	return out, nil
}

func (m *Manager) FindMountByFolderPath(folderPath string) (ClusterMountView, string, NodeStatus, bool) {
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return ClusterMountView{}, "", NodeStatus{}, false
	}
	if localMount, rel, ok := m.store.FindMountByFolderPath(clean); ok {
		self := m.SelfStatus()
		return ClusterMountView{
			MountEntry: localMount,
			NodeID:     self.NodeID,
			NodeName:   self.NodeName,
			IsSelf:     true,
		}, rel, self, true
	}

	hasPeerMounts := false
	m.mu.RLock()
	for _, p := range m.peers {
		if time.Since(p.status.LastSeen) <= PeerTimeout && strings.TrimSpace(p.status.HTTPURL) != "" && p.status.MountCount > 0 {
			hasPeerMounts = true
			break
		}
	}
	m.mu.RUnlock()
	if !hasPeerMounts {
		return ClusterMountView{}, "", NodeStatus{}, false
	}

	allMounts := m.ListAllMounts()
	lowClean := strings.ToLower(clean)
	var best ClusterMountView
	var bestRel string
	bestAliasLen := -1
	for _, view := range allMounts {
		if view.IsSelf {
			continue
		}
		alias := storage.SanitizeFolderPath(view.Alias)
		if alias == "" {
			continue
		}
		lowAlias := strings.ToLower(alias)
		if lowClean == lowAlias {
			if len(alias) > bestAliasLen {
				best = view
				bestRel = ""
				bestAliasLen = len(alias)
			}
			continue
		}
		if strings.HasPrefix(lowClean, lowAlias+"/") && len(alias) > bestAliasLen {
			best = view
			bestRel = clean[len(alias)+1:]
			bestAliasLen = len(alias)
		}
	}
	if bestAliasLen < 0 {
		return ClusterMountView{}, "", NodeStatus{}, false
	}
	peer, err := m.resolveOnlinePeer(best.NodeID)
	if err != nil {
		return best, bestRel, NodeStatus{NodeID: best.NodeID, NodeName: best.NodeName, Online: false}, true
	}
	return best, bestRel, peer, true
}

func (m *Manager) ProxyDeleteVersion(peer NodeStatus, fileID string, ver int) (storage.FileRecord, error) {
	targetURL := fmt.Sprintf("%s/internal/cluster/files/%s/versions/%d", strings.TrimRight(peer.HTTPURL, "/"), fileID, ver)
	req, err := http.NewRequest(http.MethodDelete, targetURL, nil)
	if err != nil {
		return storage.FileRecord{}, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, err
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
		return storage.FileRecord{}, fmt.Errorf("远端节点删除版本失败 (%d)", resp.StatusCode)
	}
	var rec storage.FileRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return storage.FileRecord{}, err
	}
	m.AddPeerFile(peer.NodeID, rec)
	return rec, nil
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

func (m *Manager) UpdateNodeClipboardEnabled(nodeID string, enabled bool) (NodeStatus, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.SetLocalClipboardEnabled(enabled)
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
		return NodeStatus{}, fmt.Errorf("节点 %s 当前离线，无法远程控制超级剪贴板", peer.NodeName)
	}

	payload, _ := json.Marshal(map[string]bool{"enabled": enabled})
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/clipboard"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return NodeStatus{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return NodeStatus{}, fmt.Errorf("通知节点 %s 切换超级剪贴板失败: %w", peer.NodeName, err)
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
		return NodeStatus{}, fmt.Errorf("更新超级剪贴板状态失败 (%d)", resp.StatusCode)
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

// ClusterMountView annotates a host MountEntry with owning node metadata.
type ClusterMountView struct {
	storage.MountEntry
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	IsSelf   bool   `json:"is_self"`
}

func (m *Manager) resolveOnlinePeer(nodeID string) (NodeStatus, error) {
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
	if !peer.Online || strings.TrimSpace(peer.HTTPURL) == "" {
		return NodeStatus{}, fmt.Errorf("节点 %s 当前离线", peer.NodeName)
	}
	return peer, nil
}

// ResolveNode resolves the local node (when nodeID is empty or matches self) or an online remote peer.
func (m *Manager) ResolveNode(nodeID string) (NodeStatus, error) {
	cleanID := strings.TrimSpace(nodeID)
	if cleanID == "" || cleanID == m.NodeID() {
		return m.SelfStatus(), nil
	}
	return m.resolveOnlinePeer(cleanID)
}

func (m *Manager) doSignedPeerJSON(peer NodeStatus, method, endpoint string, reqBody any, dst any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + endpoint
	req, err := http.NewRequest(method, targetURL, bodyReader)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodePeerError(resp.StatusCode, body, fmt.Sprintf("节点 %s 返回状态 %d", peer.NodeName, resp.StatusCode))
	}
	if dst != nil && len(body) > 0 {
		return json.Unmarshal(body, dst)
	}
	return nil
}

func (m *Manager) replacePeerMountFiles(nodeID, mountID string, mountFiles []storage.FileRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[nodeID]
	if !ok {
		return
	}
	kept := make([]storage.FileRecord, 0, len(p.files)+len(mountFiles))
	var oldMountBytes int64
	oldMountCnt := 0
	for _, f := range p.files {
		if f.MountID == mountID {
			oldMountCnt++
			oldMountBytes += f.Size
			continue
		}
		kept = append(kept, f)
	}
	var newMountBytes int64
	for i := range mountFiles {
		rec := mountFiles[i]
		rec.NodeID = p.status.NodeID
		rec.NodeName = p.status.NodeName
		rec.NodeOS = p.status.OS
		rec.NodeOnline = true
		newMountBytes += rec.Size
		kept = append(kept, rec)
	}
	p.files = kept
	p.status.FileCount = len(kept)
	p.status.ZeroCopyCount = max(0, p.status.ZeroCopyCount-oldMountCnt) + len(mountFiles)
	p.status.ZeroCopyBytes = max(0, p.status.ZeroCopyBytes-oldMountBytes) + newMountBytes
}

func (m *Manager) annotateSelfRecords(recs []storage.FileRecord) []storage.FileRecord {
	selfID := m.NodeID()
	selfName := m.NodeName()
	for i := range recs {
		recs[i].NodeID = selfID
		recs[i].NodeName = selfName
		recs[i].NodeOS = runtime.GOOS
		recs[i].NodeOnline = true
	}
	return recs
}

func (m *Manager) ProxyImportLocal(nodeID, srcPath, folderPath, uploader string, allowedUsers []string, dev storage.DeviceInfo) ([]storage.FileRecord, storage.ImportSummary, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		recs, summary, err := m.store.ImportLocalPath(srcPath, folderPath, uploader, allowedUsers, dev)
		return m.annotateSelfRecords(recs), summary, err
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return nil, storage.ImportSummary{}, err
	}
	var resp struct {
		Files   []storage.FileRecord  `json:"files"`
		Summary storage.ImportSummary `json:"summary"`
	}
	reqBody := map[string]any{
		"source_path":   srcPath,
		"folder_path":   folderPath,
		"uploader":      uploader,
		"allowed_users": allowedUsers,
		"device_fp":     dev.FP,
		"device_name":   dev.Name,
		"device_ip":     dev.IP,
		"device_detail": dev.Detail,
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/import-local", reqBody, &resp); err != nil {
		return nil, storage.ImportSummary{}, err
	}
	for i := range resp.Files {
		resp.Files[i].NodeID = peer.NodeID
		resp.Files[i].NodeName = peer.NodeName
		resp.Files[i].NodeOS = peer.OS
		resp.Files[i].NodeOnline = true
		m.AddPeerFile(peer.NodeID, resp.Files[i])
	}
	return resp.Files, resp.Summary, nil
}

func (m *Manager) ListAllMounts() []ClusterMountView {
	selfID := m.NodeID()
	selfName := m.NodeName()
	local := m.store.ListMounts()
	out := make([]ClusterMountView, 0, len(local))
	for _, entry := range local {
		out = append(out, ClusterMountView{
			MountEntry: entry,
			NodeID:     selfID,
			NodeName:   selfName,
			IsSelf:     true,
		})
	}

	m.mu.RLock()
	peers := make([]NodeStatus, 0, len(m.peers))
	for _, p := range m.peers {
		if time.Since(p.status.LastSeen) <= PeerTimeout && strings.TrimSpace(p.status.HTTPURL) != "" {
			peers = append(peers, p.status)
		}
	}
	m.mu.RUnlock()

	for _, peer := range peers {
		var resp struct {
			Mounts []storage.MountEntry `json:"mounts"`
		}
		if err := m.doSignedPeerJSON(peer, http.MethodGet, "/internal/cluster/mounts", nil, &resp); err != nil {
			continue
		}
		for _, entry := range resp.Mounts {
			out = append(out, ClusterMountView{
				MountEntry: entry,
				NodeID:     peer.NodeID,
				NodeName:   peer.NodeName,
				IsSelf:     false,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].IsSelf != out[j].IsSelf {
			return out[i].IsSelf
		}
		if out[i].NodeName != out[j].NodeName {
			return out[i].NodeName < out[j].NodeName
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *Manager) AddNodeMount(nodeID, dirPath, alias, addedBy string, allowedUsers []string) (storage.MountEntry, int, error) {
	return m.AddNodeMountWithOptions(nodeID, dirPath, alias, addedBy, allowedUsers, storage.AddMountOptions{})
}

func (m *Manager) AddNodeMountWithOptions(nodeID, dirPath, alias, addedBy string, allowedUsers []string, opts storage.AddMountOptions) (storage.MountEntry, int, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.store.AddMountWithOptions(dirPath, alias, addedBy, allowedUsers, opts)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return storage.MountEntry{}, 0, err
	}
	var resp struct {
		Mount storage.MountEntry   `json:"mount"`
		Added int                  `json:"added"`
		Files []storage.FileRecord `json:"files,omitempty"`
	}
	reqBody := map[string]any{
		"dir_path":        dirPath,
		"alias":           alias,
		"added_by":        addedBy,
		"allowed_users":   allowedUsers,
		"writable":        opts.Writable,
		"shadow_backup":   opts.ShadowBackup,
		"ignore_patterns": opts.IgnorePatterns,
		"ignore_set":      opts.IgnoreSet,
		"max_file_bytes":  opts.MaxFileBytes,
		"verify_presets":  opts.VerifyPresets,
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/mounts", reqBody, &resp); err != nil {
		return storage.MountEntry{}, 0, err
	}
	m.replacePeerMountFiles(peer.NodeID, resp.Mount.ID, resp.Files)
	m.mu.Lock()
	if cur, ok := m.peers[peer.NodeID]; ok {
		cur.status.MountCount++
	}
	m.mu.Unlock()
	return resp.Mount, resp.Added, nil
}

func (m *Manager) UpdateNodeMount(nodeID, mountID string, opts storage.UpdateMountOptions) (storage.MountEntry, storage.MountSyncResult, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.store.UpdateMount(mountID, opts)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return storage.MountEntry{}, storage.MountSyncResult{}, err
	}
	var resp struct {
		Mount  storage.MountEntry      `json:"mount"`
		Result storage.MountSyncResult `json:"result"`
		Files  []storage.FileRecord    `json:"files,omitempty"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPatch, "/internal/cluster/mounts/"+url.PathEscape(mountID), opts, &resp); err != nil {
		return storage.MountEntry{}, storage.MountSyncResult{}, err
	}
	m.replacePeerMountFiles(peer.NodeID, mountID, resp.Files)
	return resp.Mount, resp.Result, nil
}

func (m *Manager) RemoveNodeMount(nodeID, mountID string) (int, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		return m.store.RemoveMount(mountID)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Removed int `json:"removed"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodDelete, "/internal/cluster/mounts/"+url.PathEscape(mountID), nil, &resp); err != nil {
		return 0, err
	}
	m.RemovePeerMountFiles(peer.NodeID, mountID)
	return resp.Removed, nil
}

func (m *Manager) SyncNodeMount(nodeID, mountID string) (storage.MountEntry, storage.MountSyncResult, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		res, err := m.store.SyncMount(mountID)
		if err != nil {
			return storage.MountEntry{}, res, err
		}
		for _, entry := range m.store.ListMounts() {
			if entry.ID == mountID {
				return entry, res, nil
			}
		}
		return storage.MountEntry{ID: mountID}, res, nil
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return storage.MountEntry{}, storage.MountSyncResult{}, err
	}
	var resp struct {
		Mount  storage.MountEntry      `json:"mount"`
		Result storage.MountSyncResult `json:"result"`
		Files  []storage.FileRecord    `json:"files,omitempty"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/mounts/"+url.PathEscape(mountID)+"/sync", nil, &resp); err != nil {
		return storage.MountEntry{}, storage.MountSyncResult{}, err
	}
	m.replacePeerMountFiles(peer.NodeID, mountID, resp.Files)
	return resp.Mount, resp.Result, nil
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

func (m *Manager) UpdateFileFolder(fileID, folderPath string) (storage.FileRecord, error) {
	if _, _, ok := m.store.Get(fileID); ok {
		rec, err := m.store.SetFolderPath(fileID, folderPath)
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
		return storage.FileRecord{}, fmt.Errorf("文件所在节点 %s 当前离线，无法移动目录", peer.NodeName)
	}

	payload, _ := json.Marshal(map[string]any{
		"folder_path": folderPath,
	})
	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/files/" + url.PathEscape(fileID) + "/folder"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return storage.FileRecord{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.apiClient.Do(req)
	if err != nil {
		return storage.FileRecord{}, fmt.Errorf("通知节点 %s 移动文件目录失败: %w", peer.NodeName, err)
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
		return storage.FileRecord{}, fmt.Errorf("移动文件目录失败 (%d)", resp.StatusCode)
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
				cur.files[i].FolderPath = updated.FolderPath
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

	// Clear any prior eviction block for this peer URL before triggering its test heartbeat.
	m.mu.Lock()
	if len(m.cfg.EvictedPeers) > 0 {
		for id, u := range m.cfg.EvictedPeers {
			if strings.EqualFold(strings.TrimRight(u, "/"), cleanPeer) {
				delete(m.cfg.EvictedPeers, id)
			}
		}
		for id, dn := range m.discovered {
			if strings.EqualFold(strings.TrimRight(dn.URL, "/"), cleanPeer) {
				delete(m.cfg.EvictedPeers, id)
			}
		}
		_ = m.saveLocked()
	}
	m.mu.Unlock()

	authSecret := strings.TrimSpace(targetAuthSecret)
	if strings.TrimSpace(targetAdminUser) == "" && authSecret == "" {
		authSecret = m.Secret()
	}

	reqBody := map[string]any{
		"entry_url":          entryURL,
		"cluster_secret":     m.Secret(),
		"target_admin_user":  strings.TrimSpace(targetAdminUser),
		"target_admin_pass":  targetAdminPass,
		"target_auth_secret": authSecret,
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
	if len(m.cfg.EvictedPeers) > 0 {
		delete(m.cfg.EvictedPeers, out.Node.NodeID)
		_ = m.saveLocked()
	}
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
	if m.cfg.PrimaryNodeID == cleanID {
		m.cfg.PrimaryNodeID = ""
		_ = m.saveLocked()
	}
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
	oldPrimary := m.cfg.PrimaryNodeID

	m.cfg.Role = RoleEntry
	m.cfg.EntryHint = ""
	m.cfg.PrimaryNodeID = ""
	m.discoveredEntryURL = ""
	if m.peers == nil {
		m.peers = make(map[string]*peerRecord)
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Role = oldRole
		m.cfg.EntryHint = oldHint
		m.cfg.PrimaryNodeID = oldPrimary
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
	oldPrimary := m.cfg.PrimaryNodeID
	oldDiscEntry := m.discoveredEntryURL
	oldPeers := m.peers

	m.cfg.Role = RoleWorker
	m.cfg.EntryHint = cleanURL
	m.cfg.PrimaryNodeID = ""
	m.discoveredEntryURL = cleanURL
	m.peers = make(map[string]*peerRecord)
	for id, dn := range m.discovered {
		dn.AlreadyInPool = false
		m.discovered[id] = dn
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Role = oldRole
		m.cfg.EntryHint = oldHint
		m.cfg.PrimaryNodeID = oldPrimary
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
	oldPrimary := m.cfg.PrimaryNodeID
	oldEvicted := m.cfg.EvictedPeers
	oldDiscEntry := m.discoveredEntryURL
	oldSecret := m.cfg.ClusterSecret
	oldPeers := m.peers

	m.cfg.Role = RoleEntry
	m.cfg.EntryHint = ""
	m.cfg.PrimaryNodeID = ""
	m.cfg.EvictedPeers = nil
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
		m.cfg.PrimaryNodeID = oldPrimary
		m.cfg.EvictedPeers = oldEvicted
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
			// Legacy <= V0.11.21.4 peers return HTTP 404 for /internal/cluster/leave; evict locally and block heartbeats.
			if !strings.Contains(err.Error(), "HTTP 404") && !strings.Contains(err.Error(), "HTTP 405") {
				return false, peerName, fmt.Errorf("通知在线节点 %s 退出集群失败: %w", peerName, err)
			}
		} else {
			notified = true
		}
	}

	m.mu.Lock()
	delete(m.peers, cleanID)
	if m.cfg.PrimaryNodeID == cleanID {
		m.cfg.PrimaryNodeID = ""
	}
	if !notified && isOnline {
		if m.cfg.EvictedPeers == nil {
			m.cfg.EvictedPeers = make(map[string]string)
		}
		m.cfg.EvictedPeers[cleanID] = strings.TrimRight(peer.HTTPURL, "/")
	}
	_ = m.saveLocked()
	if dn, exists := m.discovered[cleanID]; exists {
		dn.AlreadyInPool = false
		m.discovered[cleanID] = dn
	} else if strings.TrimSpace(peer.HTTPURL) != "" {
		m.discovered[cleanID] = DiscoveredNode{
			NodeID:        peer.NodeID,
			NodeName:      peer.NodeName,
			Role:          peer.Role,
			OS:            peer.OS,
			Arch:          peer.Arch,
			URL:           strings.TrimRight(peer.HTTPURL, "/"),
			QuotaBytes:    peer.QuotaBytes,
			UsedBytes:     peer.UsedBytes,
			FileCount:     peer.FileCount,
			Online:        isOnline,
			AlreadyInPool: false,
			Source:        "udp",
			LastSeen:      now,
		}
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

// PromotePeer transfers cluster entry authority and auth state to an online worker node (or designates primary node for legacy workers).
func (m *Manager) PromotePeer(targetNodeID string, authSnapshot []byte) (string, NodeStatus, error) {
	if m.Role() != RoleEntry {
		return "", NodeStatus{}, errors.New("当前节点不是主入口节点")
	}
	cleanTarget := strings.TrimSpace(targetNodeID)
	if cleanTarget == "" {
		return "", NodeStatus{}, errors.New("请选择要设为主节点的在线节点")
	}
	if cleanTarget == m.NodeID() {
		m.mu.Lock()
		m.cfg.PrimaryNodeID = m.cfg.NodeID
		err := m.saveLocked()
		cb := m.onPeerChange
		selfName := m.cfg.NodeName
		m.mu.Unlock()
		if err != nil {
			return "", NodeStatus{}, err
		}
		if cb != nil {
			cb(selfName, true)
		}
		self := m.SelfStatus()
		self.Role = RoleEntry
		return m.EntryURL(), self, nil
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
	if promoteResp.StatusCode == http.StatusNotFound || promoteResp.StatusCode == http.StatusMethodNotAllowed {
		// Compatible fallback for older (<= V0.11.21.4) worker nodes: designate target as cluster Primary Node in-place.
		m.mu.Lock()
		m.cfg.PrimaryNodeID = cleanTarget
		saveErr := m.saveLocked()
		cb := m.onPeerChange
		m.mu.Unlock()
		if saveErr != nil {
			return "", NodeStatus{}, saveErr
		}
		if cb != nil {
			cb(targetPeer.NodeName, true)
		}
		promoted := targetPeer
		promoted.Role = RoleEntry
		promoted.Online = true
		return m.EntryURL(), promoted, nil
	}
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

	t0 := time.Now()
	resp, err := m.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat HTTP %d", resp.StatusCode)
	}
	rttMs := math.Round((float64(time.Since(t0).Microseconds())/1000.0)*10) / 10
	if rttMs <= 0 {
		rttMs = 0.2
	}
	m.mu.Lock()
	m.lastHBLatencyMs = rttMs
	m.lastSyncAt = time.Now()
	m.mu.Unlock()

	var ack struct {
		OK              bool   `json:"ok"`
		EntryURL        string `json:"entry_url"`
		EntryVersion    string `json:"entry_version"`
		AutoSyncWorkers bool   `json:"auto_sync_workers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&ack) == nil {
		clean := strings.TrimRight(strings.TrimSpace(ack.EntryURL), "/")
		var aheadCb func(entryURL, entryVersion string)
		var targetEntry string
		m.mu.Lock()
		if clean != "" {
			m.discoveredEntryURL = clean
		}
		targetEntry = clean
		if targetEntry == "" {
			targetEntry = strings.TrimRight(entryURL, "/")
		}
		if m.cfg.Role == RoleWorker &&
			ack.AutoSyncWorkers &&
			ack.EntryVersion != "" &&
			compareClusterVersions(version.Version, ack.EntryVersion) < 0 &&
			time.Since(m.lastSelfSyncAttempt) > 25*time.Second &&
			m.onEntryVersionAhead != nil {
			m.lastSelfSyncAttempt = time.Now()
			aheadCb = m.onEntryVersionAhead
		}
		m.mu.Unlock()
		if aheadCb != nil {
			go aheadCb(targetEntry, ack.EntryVersion)
		}
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

func equalPluginCapabilities(a, b []plugins.CapabilitySummary) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PluginID != b[i].PluginID ||
			a[i].Version != b[i].Version ||
			a[i].Enabled != b[i].Enabled ||
			a[i].BusyJobs != b[i].BusyJobs ||
			!equalStringSlice(a[i].Capabilities, b[i].Capabilities) {
			return false
		}
	}
	return true
}

// PluginProviderNode describes one online cluster node hosting a plugin capability.
type PluginProviderNode struct {
	NodeID         string  `json:"node_id"`
	NodeName       string  `json:"node_name"`
	IsSelf         bool    `json:"is_self"`
	BusyJobs       int     `json:"busy_jobs"`
	MaxConcurrency int     `json:"max_concurrency"`
	CPUPercent     float64 `json:"cpu_percent"`
	HasGPU         bool    `json:"has_gpu,omitempty"`
}

// ClusterCapabilityView summarizes an enabled capability aggregated across all online nodes.
type ClusterCapabilityView struct {
	Capability    string               `json:"capability"`
	PluginID      string               `json:"plugin_id"`
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	SupportedExts []string             `json:"supported_exts"`
	TriggerModes  []string             `json:"trigger_modes,omitempty"`
	Providers     []PluginProviderNode `json:"providers"`
}

func (m *Manager) onlineClusterNodesSnapshot() []NodeStatus {
	self := m.SelfStatus()
	nodes := []NodeStatus{self}
	m.mu.RLock()
	for _, p := range m.peers {
		st := p.status
		if time.Since(st.LastSeen) <= PeerTimeout {
			st.Online = true
			st.IsSelf = false
			nodes = append(nodes, st)
		}
	}
	m.mu.RUnlock()
	return nodes
}

// ClusterCapabilities aggregates enabled plugin capabilities across self and all online peer nodes.
func (m *Manager) ClusterCapabilities() []ClusterCapabilityView {
	nodes := m.onlineClusterNodesSnapshot()
	byKey := make(map[string]*ClusterCapabilityView)
	for _, n := range nodes {
		for _, capItem := range n.Plugins {
			if !capItem.Enabled {
				continue
			}
			for _, capName := range capItem.Capabilities {
				key := capItem.PluginID + "|" + capName
				view, ok := byKey[key]
				if !ok {
					view = &ClusterCapabilityView{
						Capability:    capName,
						PluginID:      capItem.PluginID,
						Name:          capItem.Name,
						Version:       capItem.Version,
						SupportedExts: append([]string(nil), capItem.SupportedExts...),
						TriggerModes:  append([]string(nil), capItem.TriggerModes...),
					}
					byKey[key] = view
				}
				view.Providers = append(view.Providers, PluginProviderNode{
					NodeID:         n.NodeID,
					NodeName:       n.NodeName,
					IsSelf:         n.IsSelf,
					BusyJobs:       capItem.BusyJobs,
					MaxConcurrency: capItem.MaxConcurrency,
					CPUPercent:     n.CPUPercent,
					HasGPU:         capItem.HasGPU,
				})
			}
		}
	}
	out := make([]ClusterCapabilityView, 0, len(byKey))
	for _, v := range byKey {
		sort.Slice(v.Providers, func(i, j int) bool {
			if v.Providers[i].IsSelf != v.Providers[j].IsSelf {
				return v.Providers[i].IsSelf
			}
			return v.Providers[i].NodeName < v.Providers[j].NodeName
		})
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Capability != out[j].Capability {
			return out[i].Capability < out[j].Capability
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

// SelectComputeNode chooses the optimal online compute node hosting the requested plugin/capability.
func (m *Manager) SelectComputeNode(capability, pluginID, preferredNodeID string) (NodeStatus, plugins.CapabilitySummary, error) {
	wantCap := strings.TrimSpace(capability)
	wantPlugin := strings.TrimSpace(strings.ToLower(pluginID))
	type candidate struct {
		node NodeStatus
		cap  plugins.CapabilitySummary
	}
	var candidates []candidate
	skippedMismatch := 0
	for _, n := range m.onlineClusterNodesSnapshot() {
		if n.Version != "" && compareClusterVersions(n.Version, version.Version) != 0 {
			skippedMismatch++
			continue
		}
		for _, p := range n.Plugins {
			if !p.Enabled {
				continue
			}
			if wantPlugin != "" && p.PluginID != wantPlugin {
				continue
			}
			if wantCap != "" {
				hasCap := false
				for _, c := range p.Capabilities {
					if c == wantCap {
						hasCap = true
						break
					}
				}
				if !hasCap {
					continue
				}
			}
			candidates = append(candidates, candidate{node: n, cap: p})
		}
	}
	if len(candidates) == 0 {
		if skippedMismatch > 0 {
			return NodeStatus{}, plugins.CapabilitySummary{}, errors.New("算力节点与主节点版本不一致，已禁止跨版本混用，请先执行全集群版本同步")
		}
		return NodeStatus{}, plugins.CapabilitySummary{}, errors.New("当前集群中没有在线且已启用的对应算力插件节点")
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		aAvail := a.cap.BusyJobs < max(1, a.cap.MaxConcurrency)
		bAvail := b.cap.BusyJobs < max(1, b.cap.MaxConcurrency)
		if aAvail != bAvail {
			return aAvail
		}
		if preferredNodeID != "" {
			aLocal := a.node.NodeID == preferredNodeID
			bLocal := b.node.NodeID == preferredNodeID
			if aLocal != bLocal {
				return aLocal
			}
		}
		if a.cap.HasGPU != b.cap.HasGPU {
			return a.cap.HasGPU
		}
		if a.cap.BusyJobs != b.cap.BusyJobs {
			return a.cap.BusyJobs < b.cap.BusyJobs
		}
		if a.node.CPUPercent != b.node.CPUPercent {
			return a.node.CPUPercent < b.node.CPUPercent
		}
		return a.node.NodeID < b.node.NodeID
	})
	return candidates[0].node, candidates[0].cap, nil
}

func (m *Manager) setPeerPlugins(nodeID string, caps []plugins.CapabilitySummary) {
	m.mu.Lock()
	if cur, ok := m.peers[nodeID]; ok {
		cur.status.Plugins = caps
	}
	m.mu.Unlock()
}

func (m *Manager) getLocalPluginManager() (*plugins.Manager, error) {
	m.mu.RLock()
	pm := m.pluginMgr
	m.mu.RUnlock()
	if pm == nil {
		return nil, errors.New("本节点尚未初始化插件管理器")
	}
	return pm, nil
}

func (m *Manager) InstallNodePlugin(nodeID, pluginID string) (plugins.Manifest, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		pm, err := m.getLocalPluginManager()
		if err != nil {
			return plugins.Manifest{}, err
		}
		return pm.InstallFromMarketplace(pluginID)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return plugins.Manifest{}, err
	}
	var resp struct {
		Manifest plugins.Manifest            `json:"manifest"`
		Plugins  []plugins.CapabilitySummary `json:"plugins"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/plugins/install", map[string]string{"plugin_id": pluginID}, &resp); err != nil {
		return plugins.Manifest{}, err
	}
	m.setPeerPlugins(peer.NodeID, resp.Plugins)
	return resp.Manifest, nil
}

func (m *Manager) ToggleNodePlugin(nodeID, pluginID string, enabled bool) (plugins.Manifest, error) {
	if nodeID == "" || nodeID == m.NodeID() {
		pm, err := m.getLocalPluginManager()
		if err != nil {
			return plugins.Manifest{}, err
		}
		return pm.SetEnabled(pluginID, enabled)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return plugins.Manifest{}, err
	}
	var resp struct {
		Manifest plugins.Manifest            `json:"manifest"`
		Plugins  []plugins.CapabilitySummary `json:"plugins"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodPost, "/internal/cluster/plugins/toggle", map[string]any{"plugin_id": pluginID, "enabled": enabled}, &resp); err != nil {
		return plugins.Manifest{}, err
	}
	m.setPeerPlugins(peer.NodeID, resp.Plugins)
	return resp.Manifest, nil
}

func (m *Manager) UninstallNodePlugin(nodeID, pluginID string) error {
	if nodeID == "" || nodeID == m.NodeID() {
		pm, err := m.getLocalPluginManager()
		if err != nil {
			return err
		}
		return pm.Uninstall(pluginID)
	}
	peer, err := m.resolveOnlinePeer(nodeID)
	if err != nil {
		return err
	}
	var resp struct {
		Plugins []plugins.CapabilitySummary `json:"plugins"`
	}
	if err := m.doSignedPeerJSON(peer, http.MethodDelete, "/internal/cluster/plugins/"+url.PathEscape(pluginID), nil, &resp); err != nil {
		return err
	}
	m.setPeerPlugins(peer.NodeID, resp.Plugins)
	return nil
}

// ExecuteRemotePlugin dispatches a file to an online peer node for plugin execution and returns output bytes & name.
func (m *Manager) ExecuteRemotePlugin(peer NodeStatus, capability, pluginID, fileName string, r io.Reader) ([]byte, string, string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer mw.Close()
		_ = mw.WriteField("capability", capability)
		_ = mw.WriteField("plugin_id", pluginID)
		part, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	targetURL := strings.TrimRight(peer.HTTPURL, "/") + "/internal/cluster/plugins/execute"
	req, err := http.NewRequest(http.MethodPost, targetURL, pr)
	if err != nil {
		_ = pr.Close()
		return nil, "", "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := m.streamClient.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("调度插件任务到节点 %s 失败: %w", peer.NodeName, err)
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rawBody, &errBody) == nil && errBody.Error != "" {
			return nil, "", "", errors.New(errBody.Error)
		}
		return nil, "", "", fmt.Errorf("节点 %s 执行插件失败 (%d)", peer.NodeName, resp.StatusCode)
	}
	var out struct {
		OutputName   string `json:"output_name"`
		OutputBase64 string `json:"output_base64"`
		Summary      string `json:"summary"`
	}
	if err := json.Unmarshal(rawBody, &out); err != nil {
		return nil, "", "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(out.OutputBase64)
	if err != nil {
		return nil, "", "", err
	}
	return decoded, out.OutputName, out.Summary, nil
}
