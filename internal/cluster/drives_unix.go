//go:build !windows

package cluster

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	unixCPUMu       sync.Mutex
	prevUnixIdle    uint64
	prevUnixTotal   uint64
	lastUnixCPUAt   time.Time
	lastUnixCPUPct  float64
)

// ListDrives enumerates common mounted Unix storage paths with free/total space.
func ListDrives() []DriveInfo {
	candidates := []struct {
		path  string
		label string
	}{
		{"/", "根目录"},
		{"/data", "数据卷 /data"},
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, struct {
			path  string
			label string
		}{home, "用户目录"})
	}

	for _, base := range []string{"/mnt", "/media", "/Volumes"} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidates = append(candidates, struct {
				path  string
				label string
			}{filepath.Join(base, e.Name()), "挂载卷 " + e.Name()})
		}
	}

	seenDev := make(map[uint64]bool)
	drives := make([]DriveInfo, 0, len(candidates))
	for _, c := range candidates {
		fi, err := os.Stat(c.path)
		if err != nil || !fi.IsDir() {
			continue
		}
		statT, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		devID := uint64(statT.Dev)
		if seenDev[devID] {
			continue
		}

		var st syscall.Statfs_t
		if err := syscall.Statfs(c.path, &st); err != nil {
			continue
		}
		seenDev[devID] = true

		bsize := uint64(st.Bsize)
		total := clampInt64(uint64(st.Blocks) * bsize)
		free := clampInt64(uint64(st.Bavail) * bsize)
		defaultDir := filepath.Join(c.path, "lfh-storage")
		if c.path == "/" {
			defaultDir = "/var/lib/lfh-storage"
		}

		drives = append(drives, DriveInfo{
			Path:       c.path,
			Label:      c.label + " (" + c.path + ")",
			TotalBytes: total,
			FreeBytes:  free,
			DefaultDir: defaultDir,
		})
	}
	return drives
}

func clampInt64(v uint64) int64 {
	const maxInt64 = uint64(1<<63 - 1)
	if v > maxInt64 {
		return int64(maxInt64)
	}
	return int64(v)
}

func readProcStatCPU() (idle, total uint64, ok bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line := strings.SplitN(string(raw), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return idle, total, total > 0
}

func sampleUnixCPUPercent() float64 {
	unixCPUMu.Lock()
	defer unixCPUMu.Unlock()

	now := time.Now()
	if prevUnixTotal > 0 && now.Sub(lastUnixCPUAt) < 350*time.Millisecond {
		return lastUnixCPUPct
	}
	idle1, total1, ok := readProcStatCPU()
	if !ok {
		return lastUnixCPUPct
	}
	if prevUnixTotal == 0 {
		prevUnixIdle = idle1
		prevUnixTotal = total1
		time.Sleep(15 * time.Millisecond)
		idle2, total2, ok2 := readProcStatCPU()
		if !ok2 || total2 <= total1 {
			lastUnixCPUAt = time.Now()
			return 0
		}
		idle1, total1 = idle2, total2
	}
	totalDelta := total1 - prevUnixTotal
	idleDelta := idle1 - prevUnixIdle
	prevUnixIdle = idle1
	prevUnixTotal = total1
	lastUnixCPUAt = time.Now()

	if totalDelta > 0 && totalDelta >= idleDelta {
		pct := float64(totalDelta-idleDelta) * 100.0 / float64(totalDelta)
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}
		lastUnixCPUPct = math.Round(pct*10) / 10
	}
	return lastUnixCPUPct
}

func readProcMemInfo() (totalBytes, usedBytes int64, ok bool) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var memTotalKB, memAvailKB, memFreeKB, buffersKB, cachedKB int64
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			memTotalKB = val
		case "MemAvailable":
			memAvailKB = val
		case "MemFree":
			memFreeKB = val
		case "Buffers":
			buffersKB = val
		case "Cached":
			cachedKB = val
		}
	}
	if memTotalKB <= 0 {
		return 0, 0, false
	}
	if memAvailKB <= 0 {
		memAvailKB = memFreeKB + buffersKB + cachedKB
	}
	totalBytes = memTotalKB * 1024
	usedBytes = max(0, (memTotalKB-memAvailKB)*1024)
	return totalBytes, usedBytes, true
}

func collectHostMetrics(storageDir string) hostMetrics {
	hw := getCachedHostHardware()
	hm := hostMetrics{
		CPUCores:    runtime.NumCPU(),
		CPUPercent:  sampleUnixCPUPercent(),
		CPUModel:    hw.CPUModel,
		GPUName:     hw.GPUName,
		HasGPU:      hw.HasGPU,
		LANLinkMbps: hw.LANLinkMbps,
	}
	if total, used, ok := readProcMemInfo(); ok && total > 0 {
		hm.MemTotalBytes = total
		hm.MemUsedBytes = used
		hm.MemPercent = math.Round((float64(used)*100.0/float64(total))*10) / 10
	} else {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		if ms.Sys > 0 {
			hm.MemTotalBytes = int64(ms.Sys)
			hm.MemUsedBytes = int64(ms.Alloc)
			hm.MemPercent = math.Round((float64(hm.MemUsedBytes)*100.0/float64(hm.MemTotalBytes))*10) / 10
		}
	}

	candidates := []string{storageDir, filepath.Dir(storageDir), "/"}
	for _, p := range candidates {
		if strings.TrimSpace(p) == "" {
			continue
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err == nil && st.Blocks > 0 {
			bsize := uint64(st.Bsize)
			dTotal := clampInt64(uint64(st.Blocks) * bsize)
			dFree := clampInt64(uint64(st.Bavail) * bsize)
			hm.DiskTotalBytes = dTotal
			hm.DiskFreeBytes = dFree
			hm.DiskUsedBytes = max(0, dTotal-dFree)
			if dTotal > 0 {
				hm.DiskPercent = math.Round((float64(hm.DiskUsedBytes)*100.0/float64(dTotal))*10) / 10
			}
			break
		}
	}
	return hm
}
