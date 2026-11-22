//go:build !windows

package cluster

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type hostHardwareInfo struct {
	CPUModel    string
	GPUName     string
	HasGPU      bool
	LANLinkMbps int64
}

var (
	hwMu     sync.RWMutex
	hwCached hostHardwareInfo
	hwOnce   sync.Once
)

func getCachedHostHardware() hostHardwareInfo {
	hwOnce.Do(func() {
		go refreshHostHardwareAsync()
	})
	hwMu.RLock()
	defer hwMu.RUnlock()
	return hwCached
}

func refreshHostHardwareAsync() {
	var info hostHardwareInfo

	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "model name") {
				if idx := strings.Index(line, ":"); idx >= 0 {
					info.CPUModel = strings.TrimSpace(line[idx+1:])
					break
				}
			}
		}
	}
	if info.CPUModel == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if out, err := exec.CommandContext(ctx, "sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			info.CPUModel = strings.TrimSpace(string(out))
		}
		cancel()
	}

	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
			first := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
			if first != "" {
				info.GPUName = first
				info.HasGPU = true
			}
		}
		cancel()
	}

	if matches, err := filepath.Glob("/sys/class/net/*"); err == nil {
		var maxSpeed int64
		for _, devDir := range matches {
			name := filepath.Base(devDir)
			if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") ||
				strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "tailscale") {
				continue
			}
			stateBytes, err := os.ReadFile(filepath.Join(devDir, "operstate"))
			if err != nil || strings.TrimSpace(string(stateBytes)) != "up" {
				continue
			}
			speedBytes, err := os.ReadFile(filepath.Join(devDir, "speed"))
			if err != nil {
				continue
			}
			if mbps, pErr := strconv.ParseInt(strings.TrimSpace(string(speedBytes)), 10, 64); pErr == nil && mbps > maxSpeed {
				maxSpeed = mbps
			}
		}
		info.LANLinkMbps = maxSpeed
	}

	hwMu.Lock()
	hwCached = info
	hwMu.Unlock()
}
