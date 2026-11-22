package cluster

import (
	"fmt"
	"sort"
	"strings"

	"lfh/internal/plugins"
)

// NodePluginFit scores how well a specific cluster node's hardware can host a plugin.
type NodePluginFit struct {
	NodeID           string   `json:"node_id"`
	NodeName         string   `json:"node_name"`
	Online           bool     `json:"online"`
	IsSelf           bool     `json:"is_self"`
	Installed        bool     `json:"installed"`
	Enabled          bool     `json:"enabled"`
	Score            int      `json:"score"`
	Level            string   `json:"level"` // recommended | compatible | limited
	CanInstall       bool     `json:"can_install"`
	CPUCores         int      `json:"cpu_cores"`
	CPUModel         string   `json:"cpu_model,omitempty"`
	CPUPercent       float64  `json:"cpu_percent"`
	MemTotalBytes    int64    `json:"mem_total_bytes"`
	MemFreeBytes     int64    `json:"mem_free_bytes"`
	MemPercent       float64  `json:"mem_percent"`
	DiskFreeBytes    int64    `json:"disk_free_bytes"`
	GPUName          string   `json:"gpu_name,omitempty"`
	HasGPU           bool     `json:"has_gpu"`
	LANLinkMbps      int64    `json:"lan_link_mbps,omitempty"`
	LANBandwidthMbps float64  `json:"lan_bandwidth_mbps,omitempty"`
	Reasons          []string `json:"reasons"`
}

// PluginRecommendation recommends the strongest cluster node for installing a plugin.
type PluginRecommendation struct {
	PluginID     string          `json:"plugin_id"`
	BestNodeID   string          `json:"best_node_id,omitempty"`
	BestNodeName string          `json:"best_node_name,omitempty"`
	BestScore    int             `json:"best_score"`
	BestLevel    string          `json:"best_level"`
	BestReason   string          `json:"best_reason"`
	NodeFits     []NodePluginFit `json:"node_fits"`
}

// RecommendPlugins evaluates every online node's hardware resources against each marketplace plugin.
func (m *Manager) RecommendPlugins(catalog []plugins.MarketplaceItem) []PluginRecommendation {
	pool := m.PoolSummaryForRole(true)
	out := make([]PluginRecommendation, 0, len(catalog))

	for _, item := range catalog {
		fits := make([]NodePluginFit, 0, len(pool.Nodes))
		for _, n := range pool.Nodes {
			if !n.Online {
				continue
			}
			fits = append(fits, scoreNodeForPlugin(n, item))
		}
		sort.SliceStable(fits, func(i, j int) bool {
			if fits[i].Score != fits[j].Score {
				return fits[i].Score > fits[j].Score
			}
			if fits[i].MemFreeBytes != fits[j].MemFreeBytes {
				return fits[i].MemFreeBytes > fits[j].MemFreeBytes
			}
			if fits[i].IsSelf != fits[j].IsSelf {
				return fits[i].IsSelf
			}
			return fits[i].NodeName < fits[j].NodeName
		})

		rec := PluginRecommendation{
			PluginID:  item.ID,
			BestLevel: "limited",
			NodeFits:  fits,
		}
		if len(fits) > 0 {
			best := fits[0]
			rec.BestNodeID = best.NodeID
			rec.BestNodeName = best.NodeName
			rec.BestScore = best.Score
			rec.BestLevel = best.Level
			if len(best.Reasons) > 0 {
				rec.BestReason = strings.Join(best.Reasons, "；")
			}
		} else {
			rec.BestReason = "暂无在线节点可评估硬件"
		}
		out = append(out, rec)
	}
	return out
}

func scoreNodeForPlugin(n NodeStatus, item plugins.MarketplaceItem) NodePluginFit {
	const gb = int64(1024 * 1024 * 1024)
	const mb = int64(1024 * 1024)

	installed := false
	enabled := false
	for _, p := range n.Plugins {
		if p.PluginID == item.ID {
			installed = true
			enabled = p.Enabled
			break
		}
	}

	freeMem := max(int64(0), n.MemTotalBytes-n.MemUsedBytes)
	score := 50
	var reasons []string

	// CPU evaluation
	switch {
	case n.CPUCores >= 8:
		score += 15
		reasons = append(reasons, fmt.Sprintf("%d核CPU性能充沛", n.CPUCores))
	case n.CPUCores >= 4:
		score += 8
		reasons = append(reasons, fmt.Sprintf("%d核CPU满足运行需求", n.CPUCores))
	case n.CPUCores > 0 && n.CPUCores < 2:
		score -= 15
		reasons = append(reasons, "单核CPU并发能力有限")
	}

	if n.CPUPercent >= 85 {
		score -= 20
		reasons = append(reasons, fmt.Sprintf("CPU负载过高(%.0f%%)", n.CPUPercent))
	} else if n.CPUPercent > 0 && n.CPUPercent <= 35 {
		score += 8
		reasons = append(reasons, fmt.Sprintf("CPU空闲(%.0f%%)", n.CPUPercent))
	}

	// Memory evaluation
	if n.MemTotalBytes > 0 {
		switch {
		case n.MemTotalBytes >= 16*gb:
			score += 18
		case n.MemTotalBytes >= 8*gb:
			score += 12
		case n.MemTotalBytes >= 4*gb:
			score += 6
		case n.MemTotalBytes < 2*gb:
			score -= 20
			reasons = append(reasons, "总内存低于2GB")
		}

		if n.MemPercent >= 92 || freeMem < 768*mb {
			score -= 32
			reasons = append(reasons, fmt.Sprintf("可用内存紧张(仅余%.1fGB/%.0f%%)", float64(freeMem)/float64(gb), n.MemPercent))
		} else if n.MemPercent >= 85 || freeMem < 1536*mb {
			score -= 16
			reasons = append(reasons, fmt.Sprintf("内存占用偏高(%.0f%%)", n.MemPercent))
		} else if freeMem >= 4*gb {
			score += 14
			reasons = append(reasons, fmt.Sprintf("可用内存%.1fGB", float64(freeMem)/float64(gb)))
		}
	}

	// Storage headroom
	if n.DiskTotalBytes > 0 && n.DiskFreeBytes < 512*mb {
		score -= 25
		reasons = append(reasons, "磁盘剩余空间不足512MB")
	} else if n.DiskFreeBytes >= 20*gb {
		score += 5
	}

	// Plugin workload specialization
	switch item.ID {
	case plugins.PluginOCRVision:
		if n.HasGPU {
			score += 12
			reasons = append(reasons, "具备GPU硬件加速")
		}
		if n.CPUCores >= 8 && freeMem >= 4*gb {
			score += 5
		}
	case plugins.PluginPDFToWord:
		if freeMem >= 3*gb {
			score += 6
		}
	case plugins.PluginAutoOrganizer:
		if n.CPUCores >= 2 && (n.MemTotalBytes == 0 || freeMem >= 512*mb) {
			score += 10
		}
		if n.LANBandwidthMbps >= 200 || n.LANLinkMbps >= 1000 {
			score += 4
			reasons = append(reasons, "内网带宽充裕适合扫描")
		}
	}

	if score < 5 {
		score = 5
	} else if score > 99 {
		score = 99
	}

	level := "compatible"
	canInstall := true
	switch {
	case score >= 75:
		level = "recommended"
	case score >= 50:
		level = "compatible"
	default:
		level = "limited"
		canInstall = false
	}

	return NodePluginFit{
		NodeID:           n.NodeID,
		NodeName:         n.NodeName,
		Online:           n.Online,
		IsSelf:           n.IsSelf,
		Installed:        installed,
		Enabled:          enabled,
		Score:            score,
		Level:            level,
		CanInstall:       canInstall,
		CPUCores:         n.CPUCores,
		CPUModel:         n.CPUModel,
		CPUPercent:       n.CPUPercent,
		MemTotalBytes:    n.MemTotalBytes,
		MemFreeBytes:     freeMem,
		MemPercent:       n.MemPercent,
		DiskFreeBytes:    n.DiskFreeBytes,
		GPUName:          n.GPUName,
		HasGPU:           n.HasGPU,
		LANLinkMbps:      n.LANLinkMbps,
		LANBandwidthMbps: n.LANBandwidthMbps,
		Reasons:          reasons,
	}
}
