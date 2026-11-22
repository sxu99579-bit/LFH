//go:build windows

package cluster

import (
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32DLL              = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives     = kernel32DLL.NewProc("GetLogicalDrives")
	procGetDriveTypeW        = kernel32DLL.NewProc("GetDriveTypeW")
	procGetDiskFreeSpaceExW  = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
	procGlobalMemoryStatusEx = kernel32DLL.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32DLL.NewProc("GetSystemTimes")

	cpuSampleMu     sync.Mutex
	prevCPUIdle     uint64
	prevCPUTotal    uint64
	lastCPUSampleAt time.Time
	lastCPUPercent  float64
)

const (
	driveRemovable = 2
	driveFixed     = 3
	driveRemote    = 4
	driveRAMDisk   = 6
)

// ListDrives enumerates mounted Windows drive letters with free/total space.
func ListDrives() []DriveInfo {
	mask, _, _ := procGetLogicalDrives.Call()
	if mask == 0 {
		return nil
	}

	drives := make([]DriveInfo, 0, 8)
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		rootPtr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}

		dtype, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootPtr)))
		var typeLabel string
		switch dtype {
		case driveFixed:
			typeLabel = "本地磁盘"
		case driveRemovable:
			typeLabel = "可移动磁盘"
		case driveRemote:
			typeLabel = "网络驱动器"
		case driveRAMDisk:
			typeLabel = "内存盘"
		default:
			continue
		}

		var freeAvail, totalBytes, totalFree uint64
		ret, _, _ := procGetDiskFreeSpaceExW.Call(
			uintptr(unsafe.Pointer(rootPtr)),
			uintptr(unsafe.Pointer(&freeAvail)),
			uintptr(unsafe.Pointer(&totalBytes)),
			uintptr(unsafe.Pointer(&totalFree)),
		)
		if ret == 0 {
			continue
		}

		drives = append(drives, DriveInfo{
			Path:       root,
			Label:      letter + ": " + typeLabel,
			TotalBytes: clampInt64(totalBytes),
			FreeBytes:  clampInt64(freeAvail),
			DefaultDir: root + "LFH-Storage",
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

type winFileTime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (ft winFileTime) uint64() uint64 {
	return (uint64(ft.HighDateTime) << 32) | uint64(ft.LowDateTime)
}

type winMemoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func readWinCPUTimes() (idle, total uint64, ok bool) {
	var ftIdle, ftKernel, ftUser winFileTime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&ftIdle)),
		uintptr(unsafe.Pointer(&ftKernel)),
		uintptr(unsafe.Pointer(&ftUser)),
	)
	if ret == 0 {
		return 0, 0, false
	}
	idle = ftIdle.uint64()
	total = ftKernel.uint64() + ftUser.uint64()
	return idle, total, true
}

func sampleWinCPUPercent() float64 {
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()

	now := time.Now()
	if prevCPUTotal > 0 && now.Sub(lastCPUSampleAt) < 350*time.Millisecond {
		return lastCPUPercent
	}
	idle1, total1, ok := readWinCPUTimes()
	if !ok {
		return lastCPUPercent
	}
	if prevCPUTotal == 0 {
		prevCPUIdle = idle1
		prevCPUTotal = total1
		time.Sleep(15 * time.Millisecond)
		idle2, total2, ok2 := readWinCPUTimes()
		if !ok2 || total2 <= total1 {
			lastCPUSampleAt = time.Now()
			return 0
		}
		idle1, total1 = idle2, total2
	}

	totalDelta := total1 - prevCPUTotal
	idleDelta := idle1 - prevCPUIdle
	prevCPUIdle = idle1
	prevCPUTotal = total1
	lastCPUSampleAt = time.Now()

	if totalDelta > 0 && totalDelta >= idleDelta {
		busy := totalDelta - idleDelta
		pct := (float64(busy) * 100.0) / float64(totalDelta)
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}
		lastCPUPercent = math.Round(pct*10) / 10
	}
	return lastCPUPercent
}

func queryWinDiskSpace(dir string) (total, free int64) {
	candidates := make([]string, 0, 3)
	if dir != "" {
		candidates = append(candidates, dir)
		if vol := filepath.VolumeName(dir); vol != "" {
			candidates = append(candidates, vol+`\`)
		}
	}
	candidates = append(candidates, `C:\`)

	for _, p := range candidates {
		ptr, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		var freeAvail, totalBytes, totalFree uint64
		ret, _, _ := procGetDiskFreeSpaceExW.Call(
			uintptr(unsafe.Pointer(ptr)),
			uintptr(unsafe.Pointer(&freeAvail)),
			uintptr(unsafe.Pointer(&totalBytes)),
			uintptr(unsafe.Pointer(&totalFree)),
		)
		if ret != 0 && totalBytes > 0 {
			return clampInt64(totalBytes), clampInt64(freeAvail)
		}
	}
	return 0, 0
}

func collectHostMetrics(storageDir string) hostMetrics {
	hw := getCachedHostHardware()
	hm := hostMetrics{
		CPUCores:    runtime.NumCPU(),
		CPUPercent:  sampleWinCPUPercent(),
		CPUModel:    hw.CPUModel,
		GPUName:     hw.GPUName,
		HasGPU:      hw.HasGPU,
		LANLinkMbps: hw.LANLinkMbps,
	}

	var ms winMemoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); ret != 0 && ms.TotalPhys > 0 {
		hm.MemTotalBytes = clampInt64(ms.TotalPhys)
		avail := clampInt64(ms.AvailPhys)
		hm.MemUsedBytes = max(0, hm.MemTotalBytes-avail)
		hm.MemPercent = math.Round((float64(hm.MemUsedBytes)*100.0/float64(hm.MemTotalBytes))*10) / 10
	}

	dTotal, dFree := queryWinDiskSpace(storageDir)
	if dTotal > 0 {
		hm.DiskTotalBytes = dTotal
		hm.DiskFreeBytes = dFree
		hm.DiskUsedBytes = max(0, dTotal-dFree)
		hm.DiskPercent = math.Round((float64(hm.DiskUsedBytes)*100.0/float64(dTotal))*10) / 10
	}
	return hm
}
