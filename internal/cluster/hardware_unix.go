//go:build !windows

package cluster

import (
	"context"
	"net"
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
		var candidates []nicCandidate
		for _, devDir := range matches {
			name := filepath.Base(devDir)
			// Require /sys/class/net/<name>/device to confirm a backed physical PCI/USB NIC rather than virtual netdevs.
			if _, statErr := os.Stat(filepath.Join(devDir, "device")); statErr != nil {
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
			mbps, pErr := strconv.ParseInt(strings.TrimSpace(string(speedBytes)), 10, 64)
			if pErr != nil || mbps <= 0 {
				continue
			}
			var ips []string
			if iface, iErr := net.InterfaceByName(name); iErr == nil {
				if addrs, aErr := iface.Addrs(); aErr == nil {
					for _, addr := range addrs {
						if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
							ips = append(ips, ipNet.IP.To4().String())
						}
					}
				}
			}
			candidates = append(candidates, nicCandidate{
				Name:          name,
				InterfaceType: "Ethernet",
				SpeedBps:      mbps * 1_000_000,
				IPv4s:         ips,
			})
		}
		info.LANLinkMbps = selectBestLANLinkMbps(candidates)
	}

	hwMu.Lock()
	hwCached = info
	hwMu.Unlock()
}
