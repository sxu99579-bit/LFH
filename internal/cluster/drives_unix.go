//go:build !windows

package cluster

import (
	"os"
	"path/filepath"
	"syscall"
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
