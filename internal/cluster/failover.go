package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// TopologyNode describes one cluster node and all its reachable URLs for client failover.
type TopologyNode struct {
	NodeID        string   `json:"node_id"`
	NodeName      string   `json:"node_name"`
	Role          string   `json:"role"`
	IsHomePrimary bool     `json:"is_home_primary,omitempty"`
	Online        bool     `json:"online"`
	URLs          []string `json:"urls"`
	Score         float64  `json:"score"`
	Version       string   `json:"version,omitempty"`
}

// ClientTopology contains the authoritative multi-NIC and multi-node address table pushed to Desktop/Android clients.
type ClientTopology struct {
	ClusterID              string         `json:"cluster_id"`
	Epoch                  uint64         `json:"epoch"`
	HomePrimaryNodeID      string         `json:"home_primary_node_id,omitempty"`
	HomePrimaryURLs        []string       `json:"home_primary_urls,omitempty"`
	ActiveEntryNodeID      string         `json:"active_entry_node_id,omitempty"`
	ActiveEntryURLs        []string       `json:"active_entry_urls,omitempty"`
	FailoverActive         bool           `json:"failover_active,omitempty"`
	AutoFailover           bool           `json:"auto_failover"`
	PreferredStandbyNodeID string         `json:"preferred_standby_node_id,omitempty"`
	StandbyOrder           []string       `json:"standby_order,omitempty"`
	Nodes                  []TopologyNode `json:"nodes"`
	UpdatedAt              string         `json:"updated_at"`
}

// HAStatusResponse reports a node's runtime cluster role, epoch, and entry URLs over HMAC-signed RPC.
type HAStatusResponse struct {
	NodeID            string   `json:"node_id"`
	NodeName          string   `json:"node_name"`
	Role              string   `json:"role"`
	ClusterID         string   `json:"cluster_id"`
	Epoch             uint64   `json:"epoch"`
	FailoverActive    bool     `json:"failover_active"`
	HomePrimaryNodeID string   `json:"home_primary_node_id,omitempty"`
	EntryURL          string   `json:"entry_url"`
	EntryURLs         []string `json:"entry_urls,omitempty"`
}

// StateSnapshotPayload carries hot-standby replicated Auth + TaskBoard snapshots.
type StateSnapshotPayload struct {
	OK            bool   `json:"ok"`
	AuthHash      string `json:"auth_hash"`
	TasksHash     string `json:"tasks_hash"`
	AuthSnapshot  []byte `json:"auth_snapshot"`
	TasksSnapshot []byte `json:"tasks_snapshot"`
}

func cloneCleanURLs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		clean := strings.TrimRight(strings.TrimSpace(raw), "/")
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}

func mergeCleanURLs(lists ...[]string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 8)
	for _, list := range lists {
		for _, raw := range list {
			clean := strings.TrimRight(strings.TrimSpace(raw), "/")
			if clean == "" || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

func (m *Manager) selfAllURLsLocked() []string {
	// Note: when onEntryAddrs hook is installed, callers should resolve URLs outside m.mu.
	// selfAllURLsLocked safely merges stored selfURL + cached HomePrimaryURLs when local self is primary.
	if m.selfURL == "" {
		return nil
	}
	return []string{strings.TrimRight(m.selfURL, "/")}
}

func (m *Manager) resolveSelfEntryURLs() []string {
	m.mu.RLock()
	selfURL := strings.TrimRight(strings.TrimSpace(m.selfURL), "/")
	addrFn := m.onEntryAddrs
	m.mu.RUnlock()

	var hookAddrs []string
	if addrFn != nil {
		hookAddrs = addrFn()
	}
	var base []string
	if selfURL != "" {
		base = []string{selfURL}
	}
	return mergeCleanURLs(base, hookAddrs)
}

// SetHAHooks wires optional server callbacks for NIC URL enumeration, auth/task hot-standby replication, and SSE event publishing.
func (m *Manager) SetHAHooks(
	onEntryAddrs func() []string,
	onExportState func() (authHash string, authSnap []byte, tasksHash string, tasksSnap []byte),
	onImportState func(authSnap []byte, tasksSnap []byte) error,
	onHAEvent func(kind, reason, newEntryURL string, entryURLs []string, epoch uint64, topo *ClientTopology),
) {
	m.mu.Lock()
	m.onEntryAddrs = onEntryAddrs
	m.onExportState = onExportState
	m.onImportState = onImportState
	m.onHAEvent = onHAEvent
	m.mu.Unlock()
}

// SetHATimingsForTest overrides HA failover and failback debounce intervals for fast unit/integration testing.
func (m *Manager) SetHATimingsForTest(failoverTimeout, rankDelay, failbackProbe, failbackStable, broadcastGrace time.Duration) {
	m.mu.Lock()
	if failoverTimeout > 0 {
		m.failoverTimeout = failoverTimeout
	}
	if rankDelay >= 0 {
		m.failoverRankDelay = rankDelay
	}
	if failbackProbe > 0 {
		m.failbackProbeIntv = failbackProbe
	}
	if failbackStable >= 0 {
		m.failbackStableDur = failbackStable
	}
	if broadcastGrace >= 0 {
		m.failbackGrace = broadcastGrace
	}
	m.mu.Unlock()
}

func (m *Manager) ClusterID() string {
	return SecretHash(m.Secret())
}

func (m *Manager) ClusterEpoch() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ClusterEpoch
}

func (m *Manager) FailoverActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.FailoverActive
}

func (m *Manager) AutoFailoverEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.AutoFailover == nil || *m.cfg.AutoFailover
}

func (m *Manager) PreferredStandbyNodeID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return strings.TrimSpace(m.cfg.PreferredStandbyNodeID)
}

// SetHAConfig updates cluster HA auto-failover and preferred standby policy.
func (m *Manager) SetHAConfig(autoFailover bool, preferredStandby string) error {
	m.mu.Lock()
	cleanPreferred := strings.TrimSpace(preferredStandby)
	m.cfg.AutoFailover = &autoFailover
	m.cfg.PreferredStandbyNodeID = cleanPreferred
	err := m.saveLocked()
	m.mu.Unlock()
	return err
}

// ComputeFailoverScore scores a node using a hybrid Network-first (50%) + Compute/Health (35%) + Stability (15%) rubric.
func ComputeFailoverScore(n NodeStatus, preferredID string) float64 {
	if !n.Online && !n.IsSelf {
		return -1
	}

	// 1. Network environment (50 pts)
	netScore := 0.0
	bw := n.LANBandwidthMbps
	if bw <= 0 && n.LANLinkMbps > 0 {
		bw = float64(n.LANLinkMbps)
	}
	switch {
	case bw >= 2500:
		netScore += 30
	case bw >= 900:
		netScore += 27
	case bw >= 400:
		netScore += 22
	case bw >= 90:
		netScore += 16
	case bw > 0:
		netScore += 10
	default:
		netScore += 14 // neutral baseline before probe
	}

	lat := n.HeartbeatLatencyMs
	if lat <= 0 {
		lat = n.NetLatencyMs
	}
	switch {
	case lat <= 0:
		netScore += 12
	case lat <= 2:
		netScore += 20
	case lat <= 8:
		netScore += 18
	case lat <= 20:
		netScore += 14
	case lat <= 60:
		netScore += 9
	default:
		netScore += 4
	}

	// 2. Compute & health (35 pts)
	cpuHeadroom := math.Max(0, 100.0-n.CPUPercent) / 100.0
	coreBonus := math.Min(5.0, float64(max(1, n.CPUCores))/2.0)
	cpuScore := cpuHeadroom*10.0 + coreBonus

	memHeadroom := math.Max(0, 100.0-n.MemPercent) / 100.0
	memCapBonus := 0.0
	if n.MemTotalBytes >= 16*GB {
		memCapBonus = 3.0
	} else if n.MemTotalBytes >= 8*GB {
		memCapBonus = 2.0
	} else if n.MemTotalBytes >= 4*GB {
		memCapBonus = 1.0
	}
	memScore := memHeadroom*7.0 + memCapBonus

	diskScore := 10.0
	if n.IOTotalErrors > 0 {
		diskScore -= math.Min(8.0, float64(n.IOTotalErrors)*2.0)
	}
	if n.FreeBytes <= 10*MB {
		diskScore -= 4.0
	}
	if diskScore < 0 {
		diskScore = 0
	}

	// 3. Platform stability & role baseline (15 pts)
	stabScore := 11.0
	lowOS := strings.ToLower(strings.TrimSpace(n.OS))
	if lowOS == "linux" {
		stabScore = 15.0
	} else if lowOS == "windows" || lowOS == "darwin" {
		stabScore = 13.0
	}

	total := math.Round((netScore+cpuScore+memScore+diskScore+stabScore)*10) / 10
	if preferredID != "" && strings.EqualFold(strings.TrimSpace(preferredID), strings.TrimSpace(n.NodeID)) {
		total += 1000.0
	}
	return total
}

func computeStandbyOrder(peers []NodeStatus, preferredID string) []string {
	type cand struct {
		id    string
		score float64
	}
	cands := make([]cand, 0, len(peers))
	for _, p := range peers {
		if !p.Online || p.Role == RoleEntry || strings.TrimSpace(p.HTTPURL) == "" {
			continue
		}
		cands = append(cands, cand{
			id:    p.NodeID,
			score: ComputeFailoverScore(p, preferredID),
		})
	}
	sort.Slice(cands, func(i, j int) bool {
		if math.Abs(cands[i].score-cands[j].score) > 0.001 {
			return cands[i].score > cands[j].score
		}
		return cands[i].id < cands[j].id
	})
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.id
	}
	return out
}

func (m *Manager) updateCachedTopologyLocked(topo *ClientTopology) {
	if topo == nil {
		return
	}
	cp := *topo
	cp.HomePrimaryURLs = cloneCleanURLs(topo.HomePrimaryURLs)
	cp.ActiveEntryURLs = cloneCleanURLs(topo.ActiveEntryURLs)
	cp.StandbyOrder = cloneCleanStrings(topo.StandbyOrder)
	cp.Nodes = make([]TopologyNode, len(topo.Nodes))
	for i, n := range topo.Nodes {
		nCopy := n
		nCopy.URLs = cloneCleanURLs(n.URLs)
		cp.Nodes[i] = nCopy
	}
	m.cachedTopology = &cp

	dirty := false
	if cp.Epoch > m.cfg.ClusterEpoch {
		m.cfg.ClusterEpoch = cp.Epoch
		dirty = true
	}
	if cp.HomePrimaryNodeID != "" && cp.HomePrimaryNodeID != m.cfg.HomePrimaryNodeID {
		m.cfg.HomePrimaryNodeID = cp.HomePrimaryNodeID
		dirty = true
	}
	if len(cp.HomePrimaryURLs) > 0 && !equalStringSlice(m.cfg.HomePrimaryURLs, cp.HomePrimaryURLs) {
		m.cfg.HomePrimaryURLs = cloneCleanURLs(cp.HomePrimaryURLs)
		dirty = true
	}
	if dirty {
		_ = m.saveLocked()
	}
}

func cloneCleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// BuildClientTopology constructs the authoritative NIC & peer address table pushed to Desktop and Android clients.
func (m *Manager) BuildClientTopology() ClientTopology {
	selfEntryURLs := m.resolveSelfEntryURLs()
	self := m.SelfStatus()
	if len(selfEntryURLs) > 0 {
		self.HTTPURLs = mergeCleanURLs(self.HTTPURLs, selfEntryURLs)
	}

	now := time.Now()
	m.mu.RLock()
	role := m.cfg.Role
	clusterID := SecretHash(m.cfg.ClusterSecret)
	epoch := m.cfg.ClusterEpoch
	homePrimaryID := m.cfg.HomePrimaryNodeID
	homePrimaryURLs := cloneCleanURLs(m.cfg.HomePrimaryURLs)
	failoverActive := m.cfg.FailoverActive
	autoFailover := m.cfg.AutoFailover == nil || *m.cfg.AutoFailover
	preferredStandby := m.cfg.PreferredStandbyNodeID
	entryHint := strings.TrimRight(strings.TrimSpace(m.cfg.EntryHint), "/")
	discEntry := strings.TrimRight(strings.TrimSpace(m.discoveredEntryURL), "/")
	cached := m.cachedTopology

	peers := make([]NodeStatus, 0, len(m.peers))
	for _, p := range m.peers {
		st := p.status
		st.IsSelf = false
		st.Online = now.Sub(st.LastSeen) <= PeerTimeout
		st.FailoverScore = ComputeFailoverScore(st, preferredStandby)
		peers = append(peers, st)
	}
	m.mu.RUnlock()

	if role == RoleWorker && cached != nil {
		cp := *cached
		cp.UpdatedAt = now.Format(time.RFC3339)
		return cp
	}

	var activeURLs []string
	var activeID string
	var nodes []TopologyNode

	if role == RoleEntry {
		activeID = self.NodeID
		var fallbackBase []string
		if self.HTTPURL != "" {
			fallbackBase = []string{self.HTTPURL}
		}
		activeURLs = mergeCleanURLs(selfEntryURLs, fallbackBase)
		if !failoverActive {
			homePrimaryID = self.NodeID
			homePrimaryURLs = cloneCleanURLs(activeURLs)
		} else if homePrimaryID == "" {
			homePrimaryID = self.NodeID
			homePrimaryURLs = cloneCleanURLs(activeURLs)
		}

		self.FailoverScore = ComputeFailoverScore(self, preferredStandby)
		nodes = append(nodes, TopologyNode{
			NodeID:        self.NodeID,
			NodeName:      self.NodeName,
			Role:          RoleEntry,
			IsHomePrimary: self.NodeID == homePrimaryID,
			Online:        true,
			URLs:          activeURLs,
			Score:         self.FailoverScore,
			Version:       self.Version,
		})

		standbyOrder := computeStandbyOrder(peers, preferredStandby)
		rankMap := make(map[string]int, len(standbyOrder))
		for i, id := range standbyOrder {
			rankMap[id] = i
		}
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].Online != peers[j].Online {
				return peers[i].Online
			}
			if math.Abs(peers[i].FailoverScore-peers[j].FailoverScore) > 0.001 {
				return peers[i].FailoverScore > peers[j].FailoverScore
			}
			return peers[i].NodeID < peers[j].NodeID
		})
		for _, p := range peers {
			var pBase []string
			if p.HTTPURL != "" {
				pBase = []string{p.HTTPURL}
			}
			pURLs := mergeCleanURLs(pBase, p.HTTPURLs)
			if len(pURLs) == 0 {
				continue
			}
			nodes = append(nodes, TopologyNode{
				NodeID:        p.NodeID,
				NodeName:      p.NodeName,
				Role:          RoleWorker,
				IsHomePrimary: p.NodeID == homePrimaryID,
				Online:        p.Online,
				URLs:          pURLs,
				Score:         p.FailoverScore,
				Version:       p.Version,
			})
		}

		return ClientTopology{
			ClusterID:              clusterID,
			Epoch:                  epoch,
			HomePrimaryNodeID:      homePrimaryID,
			HomePrimaryURLs:        homePrimaryURLs,
			ActiveEntryNodeID:      activeID,
			ActiveEntryURLs:        activeURLs,
			FailoverActive:         failoverActive,
			AutoFailover:           autoFailover,
			PreferredStandbyNodeID: preferredStandby,
			StandbyOrder:           standbyOrder,
			Nodes:                  nodes,
			UpdatedAt:              now.Format(time.RFC3339),
		}
	}

	// Worker synthesis fallback when no cached topology yet
	entryURL := discEntry
	if entryURL == "" {
		entryURL = entryHint
	}
	if entryURL != "" {
		activeURLs = mergeCleanURLs([]string{entryURL}, homePrimaryURLs)
	}
	var selfFallback []string
	if self.HTTPURL != "" {
		selfFallback = []string{self.HTTPURL}
	}
	nodes = append(nodes, TopologyNode{
		NodeID:        self.NodeID,
		NodeName:      self.NodeName,
		Role:          RoleWorker,
		IsHomePrimary: false,
		Online:        true,
		URLs:          mergeCleanURLs(selfEntryURLs, selfFallback),
		Score:         ComputeFailoverScore(self, preferredStandby),
		Version:       self.Version,
	})
	return ClientTopology{
		ClusterID:              clusterID,
		Epoch:                  epoch,
		HomePrimaryNodeID:      homePrimaryID,
		HomePrimaryURLs:        homePrimaryURLs,
		ActiveEntryURLs:        activeURLs,
		FailoverActive:         failoverActive,
		AutoFailover:           autoFailover,
		PreferredStandbyNodeID: preferredStandby,
		Nodes:                  nodes,
		UpdatedAt:              now.Format(time.RFC3339),
	}
}

// ExportStandbyHashes returns current (authHash, tasksHash) via the registered hook, if present.
func (m *Manager) ExportStandbyHashes() (string, string) {
	m.mu.RLock()
	fn := m.onExportState
	m.mu.RUnlock()
	if fn == nil {
		return "", ""
	}
	ah, _, th, _ := fn()
	return ah, th
}

// HAStatus returns compact node HA state for peer failover/failback probes.
func (m *Manager) HAStatus() HAStatusResponse {
	urls := m.resolveSelfEntryURLs()
	m.mu.RLock()
	selfURL := strings.TrimRight(strings.TrimSpace(m.selfURL), "/")
	role := m.cfg.Role
	entryURL := selfURL
	if role == RoleWorker {
		if m.discoveredEntryURL != "" {
			entryURL = m.discoveredEntryURL
		} else {
			entryURL = m.cfg.EntryHint
		}
	}
	resp := HAStatusResponse{
		NodeID:            m.cfg.NodeID,
		NodeName:          m.cfg.NodeName,
		Role:              role,
		ClusterID:         SecretHash(m.cfg.ClusterSecret),
		Epoch:             m.cfg.ClusterEpoch,
		FailoverActive:    m.cfg.FailoverActive,
		HomePrimaryNodeID: m.cfg.HomePrimaryNodeID,
		EntryURL:          strings.TrimRight(strings.TrimSpace(entryURL), "/"),
		EntryURLs:         urls,
	}
	m.mu.RUnlock()
	return resp
}

func (m *Manager) probePeerHAStatus(ctx context.Context, peerBaseURL string, timeout time.Duration) (HAStatusResponse, error) {
	cleanURL := strings.TrimRight(strings.TrimSpace(peerBaseURL), "/")
	if cleanURL == "" {
		return HAStatusResponse{}, errors.New("empty url")
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, cleanURL+"/internal/cluster/ha-status", nil)
	if err != nil {
		return HAStatusResponse{}, err
	}
	SignRequest(req, m.Secret(), m.NodeID())

	resp, err := newDirectClusterClient(timeout).Do(req)
	if err != nil {
		return HAStatusResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HAStatusResponse{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out HAStatusResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&out); err != nil {
		return HAStatusResponse{}, err
	}
	return out, nil
}

func (m *Manager) probeFirstMatchingHA(
	ctx context.Context,
	urls []string,
	timeout time.Duration,
	match func(HAStatusResponse) bool,
) (string, HAStatusResponse, bool) {
	cleanList := cloneCleanURLs(urls)
	if len(cleanList) == 0 {
		return "", HAStatusResponse{}, false
	}
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type probeOut struct {
		url string
		st  HAStatusResponse
	}
	ch := make(chan probeOut, len(cleanList))
	var wg sync.WaitGroup
	for _, u := range cleanList {
		target := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := m.probePeerHAStatus(pctx, target, timeout)
			if err == nil && (match == nil || match(st)) {
				select {
				case ch <- probeOut{url: target, st: st}:
				default:
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case hit := <-ch:
		cancel()
		<-done
		return hit.url, hit.st, true
	case <-done:
		select {
		case hit := <-ch:
			return hit.url, hit.st, true
		default:
			return "", HAStatusResponse{}, false
		}
	case <-ctx.Done():
		cancel()
		<-done
		return "", HAStatusResponse{}, false
	}
}

func (m *Manager) pullStateSnapshotFromEntry(entryURL string) error {
	m.mu.RLock()
	importFn := m.onImportState
	secret := m.cfg.ClusterSecret
	selfID := m.cfg.NodeID
	m.mu.RUnlock()
	if importFn == nil {
		return nil
	}

	target := strings.TrimRight(strings.TrimSpace(entryURL), "/") + "/internal/cluster/state-snapshot"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	SignRequest(req, secret, selfID)
	resp, err := newDirectClusterClient(6 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("state-snapshot HTTP %d", resp.StatusCode)
	}

	var snap StateSnapshotPayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&snap); err != nil {
		return err
	}
	if !snap.OK {
		return errors.New("state-snapshot returned not ok")
	}
	if err := importFn(snap.AuthSnapshot, snap.TasksSnapshot); err != nil {
		return err
	}
	m.mu.Lock()
	m.lastAuthHash = snap.AuthHash
	m.lastTasksHash = snap.TasksHash
	m.mu.Unlock()
	return nil
}

func (m *Manager) recordHeartbeatFailure() {
	m.mu.Lock()
	if m.hbFailSince.IsZero() {
		m.hbFailSince = time.Now()
	}
	m.mu.Unlock()
}

func (m *Manager) maybeRunAutoFailover(ctx context.Context) {
	m.mu.Lock()
	if m.cfg.Role != RoleWorker {
		m.mu.Unlock()
		return
	}
	autoEnabled := m.cfg.AutoFailover == nil || *m.cfg.AutoFailover
	if !autoEnabled || m.failoverInProgress || m.hbFailSince.IsZero() {
		m.mu.Unlock()
		return
	}
	if time.Since(m.hbFailSince) < m.failoverTimeout {
		m.mu.Unlock()
		return
	}
	m.failoverInProgress = true
	topo := m.cachedTopology
	selfID := m.cfg.NodeID
	myEpoch := m.cfg.ClusterEpoch
	curEntry := strings.TrimRight(strings.TrimSpace(m.discoveredEntryURL), "/")
	if curEntry == "" {
		curEntry = strings.TrimRight(strings.TrimSpace(m.cfg.EntryHint), "/")
	}
	homeID := m.cfg.HomePrimaryNodeID
	homeURLs := cloneCleanURLs(m.cfg.HomePrimaryURLs)
	rankDelay := m.failoverRankDelay
	port := m.cfg.Port
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.failoverInProgress = false
		m.mu.Unlock()
	}()

	// Step 1: Try alternate NIC IPs of the current active Entry or Home Primary first!
	var candidateEntryURLs []string
	if topo != nil {
		candidateEntryURLs = mergeCleanURLs(topo.ActiveEntryURLs, topo.HomePrimaryURLs, homeURLs)
	} else {
		candidateEntryURLs = cloneCleanURLs(homeURLs)
	}
	var altEntryURLs []string
	for _, u := range candidateEntryURLs {
		if !strings.EqualFold(strings.TrimRight(u, "/"), curEntry) {
			altEntryURLs = append(altEntryURLs, u)
		}
	}
	clusterID := m.ClusterID()
	if hitURL, _, ok := m.probeFirstMatchingHA(ctx, altEntryURLs, 1500*time.Millisecond, func(st HAStatusResponse) bool {
		return st.Role == RoleEntry && st.ClusterID == clusterID
	}); ok {
		log.Printf("[ha] switched entry NIC path to %s", hitURL)
		m.mu.Lock()
		m.discoveredEntryURL = hitURL
		m.cfg.EntryHint = hitURL
		m.hbFailSince = time.Time{}
		_ = m.saveLocked()
		m.mu.Unlock()
		_ = m.SendHeartbeatNow()
		return
	}

	// Step 2: Check rank in deterministic standby order
	var rank int
	if topo != nil && len(topo.StandbyOrder) > 0 {
		found := false
		for i, id := range topo.StandbyOrder {
			if id == selfID {
				rank = i
				found = true
				break
			}
		}
		if !found {
			rank = len(topo.StandbyOrder)
		}
	}
	if rank > 0 && rankDelay > 0 {
		waitDur := time.Duration(rank) * rankDelay
		timer := time.NewTimer(waitDur)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	// Step 3: Check peer nodes in parallel — maybe a higher-priority peer already promoted itself!
	var otherNodes []TopologyNode
	var otherPeerURLs []string
	if topo != nil {
		for _, n := range topo.Nodes {
			if n.NodeID == selfID {
				continue
			}
			otherNodes = append(otherNodes, n)
			otherPeerURLs = append(otherPeerURLs, n.URLs...)
		}
	}
	if hitURL, st, ok := m.probeFirstMatchingHA(ctx, otherPeerURLs, 1500*time.Millisecond, func(st HAStatusResponse) bool {
		return st.Role == RoleEntry && st.ClusterID == clusterID && (st.Epoch > myEpoch || st.NodeID == homeID)
	}); ok {
		targetURL := strings.TrimRight(strings.TrimSpace(hitURL), "/")
		log.Printf("[ha] discovered promoted entry %s (%s epoch=%d), repointing", st.NodeName, targetURL, st.Epoch)
		_ = m.RepointEntry(targetURL)
		return
	}

	// Step 4: Confirm alternate Home Primary NICs didn't recover during rank wait (skip already-failed curEntry)
	if topo != nil && len(altEntryURLs) > 0 {
		if hitURL, _, ok := m.probeFirstMatchingHA(ctx, altEntryURLs, 1200*time.Millisecond, func(st HAStatusResponse) bool {
			return st.Role == RoleEntry && st.ClusterID == clusterID
		}); ok {
			m.mu.Lock()
			m.discoveredEntryURL = hitURL
			m.cfg.EntryHint = hitURL
			m.hbFailSince = time.Time{}
			_ = m.saveLocked()
			m.mu.Unlock()
			return
		}
	}

	// Step 5: Promote self to Temporary Failover Entry
	if homeID == "" && topo != nil {
		homeID = topo.HomePrimaryNodeID
		if homeID == "" {
			homeID = topo.ActiveEntryNodeID
		}
	}
	if len(homeURLs) == 0 && topo != nil {
		homeURLs = mergeCleanURLs(topo.HomePrimaryURLs, topo.ActiveEntryURLs)
	}
	if len(homeURLs) == 0 && curEntry != "" {
		homeURLs = []string{curEntry}
	}

	selfURLs := m.resolveSelfEntryURLs()
	m.mu.Lock()
	liveCur := strings.TrimRight(strings.TrimSpace(m.discoveredEntryURL), "/")
	if liveCur == "" {
		liveCur = strings.TrimRight(strings.TrimSpace(m.cfg.EntryHint), "/")
	}
	if ctx.Err() != nil || m.cfg.Role == RoleEntry || m.hbFailSince.IsZero() || m.cfg.ClusterEpoch > myEpoch || (curEntry != "" && liveCur != curEntry) {
		m.mu.Unlock()
		return
	}
	newEpoch := m.cfg.ClusterEpoch
	if topo != nil && topo.Epoch > newEpoch {
		newEpoch = topo.Epoch
	}
	newEpoch++

	m.cfg.Role = RoleEntry
	m.cfg.FailoverActive = homeID != "" && homeID != selfID
	m.cfg.HomePrimaryNodeID = homeID
	m.cfg.HomePrimaryURLs = cloneCleanURLs(homeURLs)
	m.cfg.ClusterEpoch = newEpoch
	m.cfg.PrimaryNodeID = selfID
	m.cfg.EntryHint = ""
	m.discoveredEntryURL = ""
	m.hbFailSince = time.Time{}
	if m.peers == nil {
		m.peers = make(map[string]*peerRecord)
	}
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		log.Printf("[ha] failed saving failover promotion config: %v", err)
		return
	}
	m.restartLoopsLocked()
	secret := m.cfg.ClusterSecret
	haEvtFn := m.onHAEvent
	selfBaseURL := strings.TrimRight(strings.TrimSpace(m.selfURL), "/")
	if selfBaseURL == "" && len(selfURLs) > 0 {
		selfBaseURL = selfURLs[0]
	}
	m.mu.Unlock()

	log.Printf("[ha] auto-failover promoted local node %s to Entry (epoch=%d, homePrimary=%s)", selfID, newEpoch, homeID)

	// Notify remaining peer workers to repoint to us
	repointBody, _ := json.Marshal(map[string]any{
		"new_entry_url": selfBaseURL,
		"sender_id":     selfID,
		"epoch":         newEpoch,
	})
	for _, pn := range otherNodes {
		if pn.NodeID == homeID {
			continue
		}
		for _, rawPeerURL := range pn.URLs {
			peerURL, err := NormalizePeerURL(rawPeerURL, port)
			if err != nil {
				continue
			}
			req, err := http.NewRequest(http.MethodPost, peerURL+"/internal/cluster/repoint", bytes.NewReader(repointBody))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			SignRequest(req, secret, selfID)
			resp, err := newDirectClusterClient(4 * time.Second).Do(req)
			if err == nil {
				_ = resp.Body.Close()
				break
			}
		}
	}

	topoAfter := m.BuildClientTopology()
	if haEvtFn != nil {
		haEvtFn("entry_switched", "failover_promoted", selfBaseURL, topoAfter.ActiveEntryURLs, newEpoch, &topoAfter)
	}
}

func (m *Manager) runFailbackWatcher(ctx context.Context) {
	m.mu.RLock()
	probeIntv := m.failbackProbeIntv
	m.mu.RUnlock()
	if probeIntv <= 0 {
		probeIntv = 5 * time.Second
	}
	ticker := time.NewTicker(probeIntv)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAndExecuteFailback(ctx)
		}
	}
}

func (m *Manager) checkAndExecuteFailback(ctx context.Context) {
	m.mu.Lock()
	if m.cfg.Role != RoleEntry || !m.cfg.FailoverActive || m.failbackInProgress {
		m.mu.Unlock()
		return
	}
	homeID := strings.TrimSpace(m.cfg.HomePrimaryNodeID)
	homeURLs := cloneCleanURLs(m.cfg.HomePrimaryURLs)
	if homeID == "" || homeID == m.cfg.NodeID || len(homeURLs) == 0 {
		m.mu.Unlock()
		return
	}
	stableDur := m.failbackStableDur
	graceDur := m.failbackGrace
	curEpoch := m.cfg.ClusterEpoch
	selfID := m.cfg.NodeID
	secret := m.cfg.ClusterSecret
	port := m.cfg.Port
	exportFn := m.onExportState
	haEvtFn := m.onHAEvent
	peersCopy := make([]NodeStatus, 0, len(m.peers))
	for _, p := range m.peers {
		if p.status.NodeID != homeID && p.status.NodeID != selfID && p.status.HTTPURL != "" {
			peersCopy = append(peersCopy, p.status)
		}
	}
	m.mu.Unlock()

	clusterID := m.ClusterID()
	reachableHomeURL, _, _ := m.probeFirstMatchingHA(ctx, homeURLs, 1500*time.Millisecond, func(st HAStatusResponse) bool {
		return st.NodeID == homeID && st.ClusterID == clusterID
	})

	if reachableHomeURL == "" {
		m.mu.Lock()
		m.homeStableSince = time.Time{}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	if m.homeStableSince.IsZero() {
		m.homeStableSince = time.Now()
	}
	ready := time.Since(m.homeStableSince) >= stableDur
	if ready {
		m.failbackInProgress = true
	}
	m.mu.Unlock()

	if !ready {
		return
	}
	defer func() {
		m.mu.Lock()
		m.failbackInProgress = false
		m.mu.Unlock()
	}()

	var authSnap, tasksSnap []byte
	if exportFn != nil {
		_, authSnap, _, tasksSnap = exportFn()
	}

	newEpoch := curEpoch + 1
	prepPayload, err := json.Marshal(struct {
		ClusterSecret string `json:"cluster_secret"`
		NewEpoch      uint64 `json:"new_epoch"`
		AuthSnapshot  []byte `json:"auth_snapshot,omitempty"`
		TasksSnapshot []byte `json:"tasks_snapshot,omitempty"`
	}{
		ClusterSecret: secret,
		NewEpoch:      newEpoch,
		AuthSnapshot:  authSnap,
		TasksSnapshot: tasksSnap,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, reachableHomeURL+"/internal/cluster/failback-prepare", bytes.NewReader(prepPayload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, secret, selfID)

	resp, err := newDirectClusterClient(10 * time.Second).Do(req)
	if err != nil {
		log.Printf("[ha] failback prepare to %s failed: %v", reachableHomeURL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[ha] failback prepare rejected (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	var prepResp struct {
		OK        bool     `json:"ok"`
		EntryURL  string   `json:"entry_url"`
		EntryURLs []string `json:"entry_urls"`
		Epoch     uint64   `json:"epoch"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&prepResp); err != nil || !prepResp.OK {
		return
	}

	finalHomeURL := strings.TrimRight(strings.TrimSpace(prepResp.EntryURL), "/")
	if finalHomeURL == "" {
		finalHomeURL = reachableHomeURL
	}
	finalHomeURLs := mergeCleanURLs([]string{finalHomeURL}, prepResp.EntryURLs, homeURLs)
	if prepResp.Epoch > newEpoch {
		newEpoch = prepResp.Epoch
	}

	log.Printf("[ha] home primary %s recovered stably; broadcasting client switchback to %s (epoch=%d)", homeID, finalHomeURL, newEpoch)

	// Tell other workers to repoint back to restored Home Primary
	repointBody, _ := json.Marshal(map[string]any{
		"new_entry_url": finalHomeURL,
		"sender_id":     homeID,
		"epoch":         newEpoch,
	})
	for _, peer := range peersCopy {
		peerURL, pErr := NormalizePeerURL(peer.HTTPURL, port)
		if pErr != nil {
			continue
		}
		repReq, rErr := http.NewRequest(http.MethodPost, peerURL+"/internal/cluster/repoint", bytes.NewReader(repointBody))
		if rErr != nil {
			continue
		}
		repReq.Header.Set("Content-Type", "application/json")
		SignRequest(repReq, secret, selfID)
		if repResp, doErr := newDirectClusterClient(4 * time.Second).Do(repReq); doErr == nil {
			_ = repResp.Body.Close()
		}
	}

	// Build failback topology pointing to restored Home Primary & notify all connected clients before stepping down
	restoredTopo := m.BuildClientTopology()
	restoredTopo.Epoch = newEpoch
	restoredTopo.FailoverActive = false
	restoredTopo.ActiveEntryNodeID = homeID
	restoredTopo.ActiveEntryURLs = finalHomeURLs
	restoredTopo.HomePrimaryNodeID = homeID
	restoredTopo.HomePrimaryURLs = finalHomeURLs
	if haEvtFn != nil {
		haEvtFn("entry_switched", "primary_restored", finalHomeURL, finalHomeURLs, newEpoch, &restoredTopo)
	}

	if graceDur > 0 {
		timer := time.NewTimer(graceDur)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}

	m.mu.Lock()
	m.cfg.ClusterEpoch = newEpoch
	m.cfg.HomePrimaryURLs = finalHomeURLs
	m.homeStableSince = time.Time{}
	_ = m.saveLocked()
	m.mu.Unlock()

	if err := m.RepointEntry(finalHomeURL); err != nil {
		log.Printf("[ha] step down repoint to %s error: %v", finalHomeURL, err)
	}
}

// RepointEntryWithEpoch enforces cluster epoch and deterministic standby priority before stepping down an Entry node.
func (m *Manager) RepointEntryWithEpoch(newEntryURL, senderID string, senderEpoch uint64) error {
	m.mu.RLock()
	role := m.cfg.Role
	selfID := m.cfg.NodeID
	curEpoch := m.cfg.ClusterEpoch
	homeID := m.cfg.HomePrimaryNodeID
	preferred := m.cfg.PreferredStandbyNodeID
	topo := m.cachedTopology
	m.mu.RUnlock()

	if role == RoleEntry && senderEpoch > 0 {
		if senderEpoch < curEpoch {
			return fmt.Errorf("拒绝过期任期的切换指令 (epoch %d < %d)", senderEpoch, curEpoch)
		}
		if senderEpoch == curEpoch && senderID != "" && senderID != selfID {
			if senderID != homeID {
				if selfID == homeID {
					return errors.New("当前节点为原主节点，拒绝同任期降级")
				}
				selfRank, senderRank := -1, -1
				if topo != nil {
					for i, id := range topo.StandbyOrder {
						if id == selfID {
							selfRank = i
						}
						if id == senderID {
							senderRank = i
						}
					}
				}
				switch {
				case selfID == preferred && senderID != preferred:
					return errors.New("当前节点为首选备用主节点，拒绝降级")
				case senderID == preferred:
					// preferred wins tie-break
				case selfRank >= 0 && senderRank >= 0 && selfRank != senderRank:
					if selfRank < senderRank {
						return errors.New("当前节点接班优先级更高，拒绝同任期降级")
					}
				case selfID < senderID:
					return errors.New("同任期仲裁保留本地主节点身份")
				}
			}
		}
	}
	return m.RepointEntry(newEntryURL)
}

// AcceptFailback applies hot-replicated state from the temporary failover node and restores this node as cluster Entry.
func (m *Manager) AcceptFailback(clusterSecret string, newEpoch uint64, authSnap, tasksSnap []byte) (string, []string, error) {
	m.mu.Lock()
	expected := strings.TrimSpace(m.cfg.ClusterSecret)
	incoming := strings.TrimSpace(clusterSecret)
	if incoming != "" && expected != "" && incoming != expected {
		m.mu.Unlock()
		return "", nil, errors.New("集群密钥不匹配，拒绝回切恢复")
	}
	importFn := m.onImportState
	m.mu.Unlock()

	if importFn != nil && (len(authSnap) > 0 || len(tasksSnap) > 0) {
		if err := importFn(authSnap, tasksSnap); err != nil {
			return "", nil, fmt.Errorf("应用回切快照失败: %w", err)
		}
	}

	selfURLs := m.resolveSelfEntryURLs()
	m.mu.Lock()
	m.cfg.Role = RoleEntry
	m.cfg.EntryHint = ""
	m.cfg.FailoverActive = false
	m.cfg.PrimaryNodeID = m.cfg.NodeID
	m.cfg.HomePrimaryNodeID = m.cfg.NodeID
	if len(selfURLs) > 0 {
		m.cfg.HomePrimaryURLs = cloneCleanURLs(selfURLs)
	}
	if newEpoch > m.cfg.ClusterEpoch {
		m.cfg.ClusterEpoch = newEpoch
	} else {
		m.cfg.ClusterEpoch++
	}
	m.discoveredEntryURL = ""
	m.homeStableSince = time.Time{}
	if m.peers == nil {
		m.peers = make(map[string]*peerRecord)
	}
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return "", nil, err
	}
	m.restartLoopsLocked()
	entryURL := strings.TrimRight(strings.TrimSpace(m.selfURL), "/")
	if entryURL == "" && len(selfURLs) > 0 {
		entryURL = selfURLs[0]
	}
	finalURLs := mergeCleanURLs([]string{entryURL}, selfURLs)
	m.mu.Unlock()

	return entryURL, finalURLs, nil
}
