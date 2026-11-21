package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DiscoveredNode struct {
	NodeID        string    `json:"node_id"`
	NodeName      string    `json:"node_name"`
	Role          string    `json:"role"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Version       string    `json:"version,omitempty"`
	URL           string    `json:"url"`
	EntryURL      string    `json:"entry_url,omitempty"`
	QuotaBytes    int64     `json:"quota_bytes"`
	UsedBytes     int64     `json:"used_bytes"`
	FileCount     int       `json:"file_count"`
	AlreadyInPool bool      `json:"already_in_pool"`
	Online        bool      `json:"online"`
	Source        string    `json:"source"` // "udp", "lan_scan", "manual"
	LastSeen      time.Time `json:"last_seen"`
}

// NormalizePeerURL parses an IP, host:port, or full HTTP URL and normalizes it without trailing slash.
func NormalizePeerURL(raw, defaultPort string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("节点地址不能为空")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("节点地址格式无效，请输入如 192.168.3.88:8080")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("仅支持 http:// 或 https:// 协议")
	}
	port := u.Port()
	if port == "" {
		dp := strings.TrimSpace(defaultPort)
		if dp == "" {
			dp = "8080"
		}
		host := u.Hostname()
		if strings.Contains(host, ":") {
			u.Host = "[" + host + "]:" + dp
		} else {
			u.Host = host + ":" + dp
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.Scheme + "://" + u.Host + u.Path, nil
}

// ProbeNodeURL queries <url>/api/info to verify a live LFH instance and inspect its metadata.
func (m *Manager) ProbeNodeURL(ctx context.Context, rawURL, source string) (DiscoveredNode, error) {
	cleanURL, err := NormalizePeerURL(rawURL, m.Port())
	if err != nil {
		return DiscoveredNode{}, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, cleanURL+"/api/info", nil)
	if err != nil {
		return DiscoveredNode{}, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DiscoveredNode{}, fmt.Errorf("连接 %s 失败: %w", cleanURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DiscoveredNode{}, fmt.Errorf("节点 %s 返回状态 %d", cleanURL, resp.StatusCode)
	}

	var info struct {
		AppName  string      `json:"app_name"`
		Version  string      `json:"version"`
		Role     string      `json:"role"`
		EntryURL string      `json:"entry_url"`
		Pool     PoolSummary `json:"pool"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&info); err != nil {
		return DiscoveredNode{}, fmt.Errorf("解析 %s 响应失败: %w", cleanURL, err)
	}
	if !strings.EqualFold(info.AppName, "LFH") || len(info.Pool.Nodes) == 0 {
		return DiscoveredNode{}, errors.New("目标地址不是有效的 LFH 节点服务")
	}

	selfNode := info.Pool.Nodes[0]
	for _, n := range info.Pool.Nodes {
		if n.IsSelf {
			selfNode = n
			break
		}
	}
	if selfNode.NodeID == m.NodeID() {
		return DiscoveredNode{}, errors.New("探测目标为当前本机节点")
	}

	if source == "" {
		source = "manual"
	}
	dn := DiscoveredNode{
		NodeID:     selfNode.NodeID,
		NodeName:   selfNode.NodeName,
		Role:       info.Role,
		OS:         selfNode.OS,
		Arch:       selfNode.Arch,
		Version:    info.Version,
		URL:        cleanURL,
		EntryURL:   strings.TrimRight(info.EntryURL, "/"),
		QuotaBytes: selfNode.QuotaBytes,
		UsedBytes:  selfNode.UsedBytes,
		FileCount:  selfNode.FileCount,
		Online:     true,
		Source:     source,
		LastSeen:   time.Now(),
	}
	if dn.Role == "" {
		dn.Role = selfNode.Role
	}

	m.mu.Lock()
	_, inPool := m.peers[dn.NodeID]
	dn.AlreadyInPool = inPool
	m.discovered[dn.NodeID] = dn
	m.mu.Unlock()

	return dn, nil
}

// PassiveDiscoveredNodes returns fresh UDP/cached nodes discovered on the LAN.
func (m *Manager) PassiveDiscoveredNodes() []DiscoveredNode {
	now := time.Now()
	m.mu.RLock()
	out := make([]DiscoveredNode, 0, len(m.discovered))
	for id, dn := range m.discovered {
		if id == m.cfg.NodeID {
			continue
		}
		if now.Sub(dn.LastSeen) > 45*time.Second {
			continue
		}
		_, inPool := m.peers[id]
		dn.AlreadyInPool = inPool
		dn.Online = true
		out = append(out, dn)
	}
	m.mu.RUnlock()
	sortDiscoveredNodes(out)
	return out
}

// ScanLAN actively scans local private /24 LAN subnets for live LFH nodes and merges passive UDP discoveries.
func (m *Manager) ScanLAN(ctx context.Context, ports []int) []DiscoveredNode {
	cleanPorts := normalizeScanPorts(ports, m.Port())
	scanTargets := buildLANScanTargets(cleanPorts, m.Port())

	results := make(map[string]DiscoveredNode)
	for _, dn := range m.PassiveDiscoveredNodes() {
		results[dn.NodeID] = dn
	}

	if len(scanTargets) > 0 {
		scanCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()

		jobs := make(chan string, len(scanTargets))
		for _, target := range scanTargets {
			jobs <- target
		}
		close(jobs)

		var resMu sync.Mutex
		var wg sync.WaitGroup
		workers := 64
		if len(scanTargets) < workers {
			workers = len(scanTargets)
		}

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				dialer := &net.Dialer{Timeout: 300 * time.Millisecond}
				for addr := range jobs {
					if scanCtx.Err() != nil {
						return
					}
					conn, err := dialer.DialContext(scanCtx, "tcp", addr)
					if err != nil {
						continue
					}
					_ = conn.Close()

					dn, probeErr := m.ProbeNodeURL(scanCtx, "http://"+addr, "lan_scan")
					if probeErr != nil {
						continue
					}
					resMu.Lock()
					results[dn.NodeID] = dn
					resMu.Unlock()
				}
			}()
		}
		wg.Wait()
	}

	out := make([]DiscoveredNode, 0, len(results))
	m.mu.RLock()
	for _, dn := range results {
		if dn.NodeID == m.cfg.NodeID {
			continue
		}
		_, inPool := m.peers[dn.NodeID]
		dn.AlreadyInPool = inPool
		out = append(out, dn)
	}
	m.mu.RUnlock()

	sortDiscoveredNodes(out)
	return out
}

func sortDiscoveredNodes(out []DiscoveredNode) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].AlreadyInPool != out[j].AlreadyInPool {
			return !out[i].AlreadyInPool
		}
		if out[i].NodeName != out[j].NodeName {
			return out[i].NodeName < out[j].NodeName
		}
		return out[i].URL < out[j].URL
	})
}

func normalizeScanPorts(ports []int, selfPort string) []int {
	seen := make(map[int]bool)
	out := make([]int, 0, 5)
	for _, p := range ports {
		if p < 1 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= 5 {
			break
		}
	}
	if len(out) == 0 {
		p, err := strconv.Atoi(strings.TrimSpace(selfPort))
		if err != nil || p < 1 || p > 65535 {
			p = 8080
		}
		out = append(out, p)
	}
	return out
}

func buildLANScanTargets(ports []int, selfPort string) []string {
	selfPortNum, _ := strconv.Atoi(strings.TrimSpace(selfPort))
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	ownIPs := make(map[string]bool)
	prefixes := make(map[string]bool)

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualOrProxyIface(iface.Name) {
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
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}
			if (ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)) || (ip4[0] == 169 && ip4[1] == 254) {
				continue
			}
			ownIPs[ip4.String()] = true
			if isPrivateIPv4(ip4) {
				prefix := fmt.Sprintf("%d.%d.%d", ip4[0], ip4[1], ip4[2])
				prefixes[prefix] = true
			}
		}
	}

	prefixList := make([]string, 0, len(prefixes))
	for p := range prefixes {
		prefixList = append(prefixList, p)
	}
	sort.Strings(prefixList)
	if len(prefixList) > 4 {
		prefixList = prefixList[:4]
	}

	targets := make([]string, 0, len(prefixList)*254*len(ports))
	for _, prefix := range prefixList {
		for host := 1; host <= 254; host++ {
			ipStr := fmt.Sprintf("%s.%d", prefix, host)
			isSelfIP := ownIPs[ipStr]
			for _, port := range ports {
				if isSelfIP && port == selfPortNum {
					continue
				}
				targets = append(targets, net.JoinHostPort(ipStr, strconv.Itoa(port)))
			}
		}
	}
	return targets
}

func isPrivateIPv4(ip4 net.IP) bool {
	return (ip4[0] == 192 && ip4[1] == 168) ||
		ip4[0] == 10 ||
		(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31)
}

func isVirtualOrProxyIface(name string) bool {
	low := strings.ToLower(name)
	for _, kw := range []string{
		"vmware", "vmnet", "vethernet", "virtualbox", "vbox",
		"docker", "wsl", "hyper-v", "mihomo", "clash", "tap",
		"bluetooth", "蓝牙", "corplink",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}
