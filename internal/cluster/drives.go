package cluster

// DriveInfo describes an available disk drive or storage mount point on a node.
type DriveInfo struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	DefaultDir string `json:"default_dir"`
}
