//go:build windows

package cluster

import (
	"syscall"
	"unsafe"
)

var (
	kernel32DLL             = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives    = kernel32DLL.NewProc("GetLogicalDrives")
	procGetDriveTypeW       = kernel32DLL.NewProc("GetDriveTypeW")
	procGetDiskFreeSpaceExW = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
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
