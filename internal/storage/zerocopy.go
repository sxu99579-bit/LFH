package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	maxZeroCopyWalkFiles           = 10000
	DefaultMountMaxFileBytes int64 = 50 * 1024 * 1024
)

// DefaultProjectIgnorePatterns returns common dependency/build artifact directories ignored for mounted project workspaces.
func DefaultProjectIgnorePatterns() []string {
	return []string{
		"node_modules",
		".git",
		"dist",
		"build",
		".next",
		".nuxt",
		"target",
		"vendor",
		"__pycache__",
		".venv",
		".idea",
		".vscode",
		".DS_Store",
	}
}

// VerifyPreset defines an admin-configured, non-shell workspace build/test command.
type VerifyPreset struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	WorkSubDir string   `json:"work_sub_dir,omitempty"`
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
	LogRelPath string   `json:"log_rel_path,omitempty"`
}

// NormalizeVerifyPresets validates preset IDs, subdirs, non-empty commands, timeouts, and safe log paths.
func NormalizeVerifyPresets(raw []VerifyPreset) ([]VerifyPreset, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]VerifyPreset, 0, len(raw))
	for i, p := range raw {
		id := strings.TrimSpace(p.ID)
		if id == "" || len(id) > 64 {
			return nil, fmt.Errorf("verify_presets[%d] id 长度必须在 1-64 字符之间", i)
		}
		for _, r := range id {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return nil, fmt.Errorf("verify_presets[%d] id 仅允许字母、数字、- 和 _", i)
			}
		}
		lowID := strings.ToLower(id)
		if seen[lowID] {
			return nil, fmt.Errorf("verify_presets 存在重复 ID: %s", id)
		}
		seen[lowID] = true

		cmd := strings.TrimSpace(p.Command)
		if cmd == "" || strings.ContainsRune(cmd, '\x00') || strings.ContainsAny(cmd, "\r\n") {
			return nil, fmt.Errorf("verify_presets[%s] command 无效", id)
		}
		label := strings.TrimSpace(p.Label)
		if label == "" {
			label = id
		}

		sub := strings.TrimSpace(strings.ReplaceAll(p.WorkSubDir, "\\", "/"))
		sub = strings.Trim(sub, "/")
		if sub != "" {
			cleanSub, err := cleanMountRelPath(sub)
			if err != nil {
				return nil, fmt.Errorf("verify_presets[%s] work_sub_dir 无效: %w", id, err)
			}
			sub = cleanSub
		}

		logRel := strings.TrimSpace(strings.ReplaceAll(p.LogRelPath, "\\", "/"))
		logRel = strings.Trim(logRel, "/")
		if logRel == "" {
			logRel = ".lfh-logs/" + id + ".log"
		} else {
			cleanLog, err := cleanMountRelPath(logRel)
			if err != nil {
				return nil, fmt.Errorf("verify_presets[%s] log_rel_path 无效: %w", id, err)
			}
			logRel = cleanLog
		}
		if isHardcodedBlockedMountPath(logRel) {
			return nil, fmt.Errorf("verify_presets[%s] log_rel_path 命中受保护路径黑名单", id)
		}

		timeoutSec := p.TimeoutSec
		if timeoutSec <= 0 {
			timeoutSec = 60
		}
		if timeoutSec > 300 {
			timeoutSec = 300
		}

		args := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			if strings.ContainsRune(a, '\x00') {
				return nil, fmt.Errorf("verify_presets[%s] args 包含非法空字符", id)
			}
			args = append(args, a)
		}

		out = append(out, VerifyPreset{
			ID:         id,
			Label:      label,
			WorkSubDir: sub,
			Command:    cmd,
			Args:       args,
			TimeoutSec: timeoutSec,
			LogRelPath: logRel,
		})
	}
	return out, nil
}

// MountEntry describes a host directory exposed through zero-copy virtual mount (and optional writable workspace).
type MountEntry struct {
	ID             string         `json:"id"`
	DirPath        string         `json:"dir_path"`
	Alias          string         `json:"alias"`
	AllowedUsers   []string       `json:"allowed_users,omitempty"`
	AddedBy        string         `json:"added_by,omitempty"`
	Status         string         `json:"status"` // ok, missing
	FileCount      int            `json:"file_count"`
	TotalBytes     int64          `json:"total_bytes"`
	Writable       bool           `json:"writable,omitempty"`
	ShadowBackup   bool           `json:"shadow_backup,omitempty"`
	IgnorePatterns []string       `json:"ignore_patterns,omitempty"`
	MaxFileBytes   int64          `json:"max_file_bytes,omitempty"`
	VerifyPresets  []VerifyPreset `json:"verify_presets,omitempty"`
	LastSyncAt     time.Time      `json:"last_sync_at"`
	CreatedAt      time.Time      `json:"created_at"`
	LastError      string         `json:"last_error,omitempty"`
}

// AddMountOptions configures writable project workspace behavior when registering a directory mount.
type AddMountOptions struct {
	Writable       bool           `json:"writable,omitempty"`
	ShadowBackup   *bool          `json:"shadow_backup,omitempty"`
	IgnorePatterns []string       `json:"ignore_patterns,omitempty"`
	IgnoreSet      bool           `json:"ignore_set,omitempty"`
	MaxFileBytes   int64          `json:"max_file_bytes,omitempty"`
	VerifyPresets  []VerifyPreset `json:"verify_presets,omitempty"`
}

// UpdateMountOptions selectively updates writable/ignore settings on an existing mount.
type UpdateMountOptions struct {
	Alias          *string         `json:"alias,omitempty"`
	AllowedUsers   *[]string       `json:"allowed_users,omitempty"`
	Writable       *bool           `json:"writable,omitempty"`
	ShadowBackup   *bool           `json:"shadow_backup,omitempty"`
	IgnorePatterns *[]string       `json:"ignore_patterns,omitempty"`
	MaxFileBytes   *int64          `json:"max_file_bytes,omitempty"`
	VerifyPresets  *[]VerifyPreset `json:"verify_presets,omitempty"`
}

// ImportSummary reports metrics from a zero-copy local path import operation.
type ImportSummary struct {
	TotalImported int      `json:"total_imported"`
	HardlinkCount int      `json:"hardlink_count"`
	RefCount      int      `json:"ref_count"`
	SkippedCount  int      `json:"skipped_count"`
	TotalBytes    int64    `json:"total_bytes"`
	Warnings      []string `json:"warnings,omitempty"`
}

// MountSyncResult summarizes the diff applied while scanning a mounted directory.
type MountSyncResult struct {
	MountID    string `json:"mount_id"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Removed    int    `json:"removed"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

type walkedLocalFile struct {
	fullPath string
	relSlash string
	name     string
	size     int64
	modTime  time.Time
}

func cleanLocalAbsPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 2 && ((trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') || (trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'')) {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	if trimmed == "" {
		return "", errors.New("路径不能为空")
	}
	if runtime.GOOS == "windows" && len(trimmed) == 2 && trimmed[1] == ':' &&
		((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z')) {
		trimmed += `\`
	}
	cleaned := filepath.Clean(trimmed)
	if !filepath.IsAbs(cleaned) {
		return "", errors.New(`请填写完整的本机绝对路径（例如 D:\Videos 或 /mnt/media）`)
	}
	return cleaned, nil
}

func sameOSPath(a, b string) bool {
	ca := filepath.Clean(strings.TrimSpace(a))
	cb := filepath.Clean(strings.TrimSpace(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

func isSubOrSamePath(candidate, root string) bool {
	if candidate == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func shouldSkipZeroCopyDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node_modules", ".git", "dist", "build", ".next", ".nuxt", "target", "__pycache__", ".venv", "$recycle.bin", "system volume information":
		return true
	default:
		return false
	}
}

func normalizeIgnorePatterns(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		p := strings.TrimSpace(strings.ReplaceAll(item, "\\", "/"))
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func effectiveMaxFileBytes(m MountEntry) int64 {
	if m.MaxFileBytes > 0 {
		return m.MaxFileBytes
	}
	return DefaultMountMaxFileBytes
}

func cleanMountRelPath(raw string) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return "", errors.New("目标文件相对路径不能为空")
	}
	if strings.ContainsRune(s, '\x00') {
		return "", errors.New("路径包含非法空字符")
	}
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return "", errors.New("禁止使用包含盘符的绝对路径")
	}
	parts := strings.Split(s, "/")
	clean := make([]string, 0, len(parts))
	for _, seg := range parts {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." {
			return "", errors.New("相对路径包含无效空段或点路径")
		}
		if seg == ".." {
			return "", errors.New("禁止通过 .. 跨越挂载目录边界")
		}
		clean = append(clean, seg)
	}
	return strings.Join(clean, "/"), nil
}

// isHardcodedBlockedMountPath enforces server-side security boundaries for writable workspaces
// regardless of custom IgnorePatterns settings.
func isHardcodedBlockedMountPath(relSlash string) bool {
	trimmed := strings.TrimSpace(strings.ReplaceAll(relSlash, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return true
	}
	parts := strings.Split(trimmed, "/")
	for _, seg := range parts {
		low := strings.ToLower(strings.TrimSpace(seg))
		if low == "" || low == "." || low == ".." || low == ".git" || low == ".ssh" {
			return true
		}
	}
	base := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	if strings.HasPrefix(base, ".lfh_tmp_") {
		return true
	}
	if base == ".env" {
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		switch base {
		case ".env.example", ".env.sample", ".env.template":
		default:
			return true
		}
	}
	ext := strings.ToLower(path.Ext(base))
	switch ext {
	case ".pem", ".key", ".p12", ".pfx", ".jks":
		return true
	}
	if base == "id_rsa" || base == "id_ed25519" || base == "id_ecdsa" ||
		strings.HasPrefix(base, "id_rsa.") || strings.HasPrefix(base, "id_ed25519.") || strings.HasPrefix(base, "id_ecdsa.") {
		return true
	}
	return false
}

func matchesIgnorePatterns(relSlash, baseName string, patterns []string) bool {
	lowBase := strings.ToLower(strings.TrimSpace(baseName))
	if strings.HasPrefix(lowBase, ".lfh_tmp_") {
		return true
	}
	if len(patterns) == 0 {
		return false
	}
	cleanRel := strings.Trim(strings.ToLower(strings.ReplaceAll(relSlash, "\\", "/")), "/")
	segs := strings.Split(cleanRel, "/")
	for _, rawPat := range patterns {
		pat := strings.ToLower(strings.Trim(strings.TrimSpace(strings.ReplaceAll(rawPat, "\\", "/")), "/"))
		if pat == "" {
			continue
		}
		if strings.Contains(pat, "/") {
			if ok, _ := path.Match(pat, cleanRel); ok || cleanRel == pat || strings.HasPrefix(cleanRel, pat+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, lowBase); ok {
			return true
		}
		for _, seg := range segs {
			if seg == "" {
				continue
			}
			if seg == pat {
				return true
			}
			if ok, _ := path.Match(pat, seg); ok {
				return true
			}
		}
	}
	return false
}

func verifyNoSymlinkPath(mountRoot, targetAbs string) error {
	cleanRoot := filepath.Clean(mountRoot)
	cleanTarget := filepath.Clean(targetAbs)
	if !isSubOrSamePath(cleanTarget, cleanRoot) || sameOSPath(cleanTarget, cleanRoot) {
		return errors.New("目标路径超出挂载目录范围")
	}
	rootInfo, err := os.Lstat(cleanRoot)
	if err != nil {
		return fmt.Errorf("无法访问挂载根目录: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("挂载根目录无效或包含符号链接")
	}

	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return errors.New("无法解析挂载相对路径")
	}
	cur := cleanRoot
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, lErr := os.Lstat(cur)
		if os.IsNotExist(lErr) {
			break
		}
		if lErr != nil {
			return lErr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("安全策略禁止通过符号链接写入挂载目录")
		}
		if i == len(parts)-1 && fi.IsDir() {
			return errors.New("目标路径已是目录，无法作为文件写入")
		}
	}
	return nil
}

func verifyParentRealPathInsideMount(mountRoot, parentAbs string) error {
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(mountRoot))
	if err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Clean(parentAbs))
	if err != nil {
		return err
	}
	if !isSubOrSamePath(realParent, realRoot) {
		return errors.New("安全策略禁止符号链接逃逸挂载根目录")
	}
	return nil
}

func collectLocalFiles(root string, maxFiles int) (isDir bool, items []walkedLocalFile, skipped int, err error) {
	return collectLocalFilesWithIgnore(root, maxFiles, nil, false)
}

func collectLocalFilesWithIgnore(root string, maxFiles int, ignorePatterns []string, skipSensitive bool) (isDir bool, items []walkedLocalFile, skipped int, err error) {
	info, statErr := os.Stat(root)
	if statErr != nil {
		return false, nil, 0, fmt.Errorf("无法访问路径 %s: %w", root, statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, nil, 1, errors.New("不支持直接导入符号链接")
	}
	if !info.IsDir() {
		return false, []walkedLocalFile{{
			fullPath: root,
			relSlash: filepath.Base(root),
			name:     filepath.Base(root),
			size:     info.Size(),
			modTime:  info.ModTime(),
		}}, 0, nil
	}

	items = make([]walkedLocalFile, 0, 64)
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			skipped++
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p != root && d.Type()&os.ModeSymlink != 0 {
			skipped++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		var relSlash string
		if p != root {
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				skipped++
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			relSlash = filepath.ToSlash(rel)
		}
		if d.IsDir() {
			if p != root {
				lowName := strings.ToLower(strings.TrimSpace(d.Name()))
				if shouldSkipZeroCopyDir(d.Name()) || matchesIgnorePatterns(relSlash, d.Name(), ignorePatterns) ||
					(skipSensitive && (lowName == ".git" || lowName == ".ssh")) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasPrefix(strings.ToLower(d.Name()), ".lfh_tmp_") {
			return nil
		}
		if matchesIgnorePatterns(relSlash, d.Name(), ignorePatterns) {
			skipped++
			return nil
		}
		if skipSensitive && isHardcodedBlockedMountPath(relSlash) {
			skipped++
			return nil
		}
		if len(items) >= maxFiles {
			return fmt.Errorf("文件数量超过单次上限 %d", maxFiles)
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			skipped++
			return nil
		}
		items = append(items, walkedLocalFile{
			fullPath: p,
			relSlash: relSlash,
			name:     d.Name(),
			size:     fi.Size(),
			modTime:  fi.ModTime(),
		})
		return nil
	})
	if walkErr != nil && len(items) == 0 {
		return true, nil, skipped, walkErr
	}
	return true, items, skipped, walkErr
}

func (s *Store) loadMountsLocked() {
	if s.mounts == nil {
		s.mounts = make(map[string]MountEntry)
	}
	if s.mountsPath == "" {
		return
	}
	raw, err := os.ReadFile(s.mountsPath)
	if err != nil {
		return
	}
	var list []MountEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		return
	}
	for _, m := range list {
		if m.ID == "" || strings.TrimSpace(m.DirPath) == "" {
			continue
		}
		m.DirPath = filepath.Clean(m.DirPath)
		m.Alias = SanitizeFolderPath(m.Alias)
		m.AllowedUsers = NormalizeAllowedUsers(m.AllowedUsers)
		m.IgnorePatterns = normalizeIgnorePatterns(m.IgnorePatterns)
		if m.Status == "" {
			m.Status = "ok"
		}
		s.mounts[m.ID] = m
	}
}

func (s *Store) saveMountsLocked() error {
	if s.mountsPath == "" {
		return nil
	}
	list := make([]MountEntry, 0, len(s.mounts))
	for _, m := range s.mounts {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.mountsPath, raw, 0644)
}

func folderPathForWalkedItem(baseFolder, relSlash string, isDir bool) string {
	baseFolder = SanitizeFolderPath(baseFolder)
	if !isDir {
		return baseFolder
	}
	subDir := ""
	if idx := strings.LastIndex(relSlash, "/"); idx > 0 {
		subDir = relSlash[:idx]
	}
	if baseFolder != "" && subDir != "" {
		return SanitizeFolderPath(baseFolder + "/" + subDir)
	}
	if baseFolder != "" {
		return baseFolder
	}
	return SanitizeFolderPath(subDir)
}

// ImportLocalPath indexes one local file or directory using zero-copy storage:
// same-volume files create an OS hardlink inside UploadDir (surviving source deletion/moves),
// while cross-volume files fall back to read-only external path references.
func (s *Store) ImportLocalPath(srcPath, folderPath, uploader string, allowedUsers []string, dev DeviceInfo) ([]FileRecord, ImportSummary, error) {
	cleanSrc, err := cleanLocalAbsPath(srcPath)
	if err != nil {
		return nil, ImportSummary{}, err
	}
	activeDir := s.UploadDir()
	if isSubOrSamePath(cleanSrc, activeDir) || isSubOrSamePath(cleanSrc, s.defaultUploadDir) {
		return nil, ImportSummary{}, errors.New("不能零拷贝导入 QuailNest 自身内部存储目录")
	}

	isDir, items, skipped, walkErr := collectLocalFiles(cleanSrc, maxZeroCopyWalkFiles)
	if walkErr != nil && len(items) == 0 {
		return nil, ImportSummary{}, walkErr
	}
	if len(items) == 0 {
		return nil, ImportSummary{}, errors.New("目标路径下没有可导入的文件")
	}
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return nil, ImportSummary{}, err
	}

	baseFolder := SanitizeFolderPath(folderPath)
	if isDir && baseFolder == "" {
		rootBase := filepath.Base(cleanSrc)
		if rootBase != "/" && rootBase != `\` && rootBase != "." {
			baseFolder = SanitizeFolderPath(rootBase)
		}
	}

	normAllowed := NormalizeAllowedUsers(allowedUsers)
	summary := ImportSummary{SkippedCount: skipped}
	if walkErr != nil {
		summary.Warnings = append(summary.Warnings, walkErr.Error())
	}
	now := time.Now()
	out := make([]FileRecord, 0, len(items))

	for _, item := range items {
		origName := sanitizeFilename(item.name)
		targetFolder := folderPathForWalkedItem(baseFolder, item.relSlash, isDir)
		ext := strings.ToLower(filepath.Ext(origName))
		blobID := newID()
		storedName := blobID + ext
		dstPath := filepath.Join(activeDir, storedName)

		mode := StorageModeHardlink
		versionNote := "本机零拷贝导入（同盘硬链接）"
		linkErr := os.Link(item.fullPath, dstPath)
		if linkErr != nil {
			mode = StorageModeRef
			storedName = ""
			versionNote = "本机零拷贝导入（跨盘引用）"
		}

		s.mu.Lock()
		var sameSrcID string
		var sameNameID string
		for id, cur := range s.records {
			if cur.StorageMode == StorageModeMount {
				continue
			}
			if cur.SourcePath != "" && sameOSPath(cur.SourcePath, item.fullPath) {
				sameSrcID = id
				break
			}
			if sameNameID == "" && strings.EqualFold(cur.Name, origName) && cur.FolderPath == targetFolder {
				sameNameID = id
			}
		}

		var rec FileRecord
		if sameSrcID != "" {
			cur := s.records[sameSrcID]
			oldMode := cur.StorageMode
			oldDisk := ""
			if oldMode == StorageModeHardlink && cur.StoredName != "" {
				oldDisk = s.recordDiskPathLocked(cur)
			}
			if cur.Version <= 0 {
				cur.Version = 1
			}
			cur.Name = origName
			if folderPath != "" || !isDir {
				cur.FolderPath = targetFolder
			}
			cur.StoredName = storedName
			cur.StorageMode = mode
			cur.SourcePath = item.fullPath
			cur.SourceMissing = false
			cur.MountID = ""
			cur.Size = item.size
			cur.Ext = ext
			cur.Category = ClassifyExt(ext)
			cur.Uploader = uploader
			cur.UploadedAt = now
			cur.MonthKey = now.Format("2006-01")
			cur.DateKey = now.Format("2006-01-02")
			cur.DeviceFP = strings.TrimSpace(dev.FP)
			cur.DeviceName = strings.TrimSpace(dev.Name)
			cur.DeviceIP = strings.TrimSpace(dev.IP)
			cur.DeviceDetail = strings.TrimSpace(dev.Detail)
			if len(normAllowed) > 0 {
				cur.AllowedUsers = normAllowed
			}
			if mode == StorageModeHardlink {
				cur.StorageDir = activeDir
			} else {
				cur.StorageDir = ""
			}
			cur.VersionNote = versionNote
			cur.UpdateMethod = MethodLocalImport
			cur.VersionCount = 1 + len(cur.Versions)
			cur.NodeOnline = true
			s.records[sameSrcID] = cur
			rec = cur
			if err := s.saveLocked(); err != nil {
				s.mu.Unlock()
				if storedName != "" {
					_ = os.Remove(dstPath)
				}
				s.RecordWriteError()
				return nil, summary, err
			}
			s.mu.Unlock()
			if oldDisk != "" && !sameOSPath(oldDisk, dstPath) {
				_ = os.Remove(oldDisk)
			}
		} else if sameNameID != "" {
			cur := s.records[sameNameID]
			curVer := cur.Version
			if curVer <= 0 {
				curVer = 1
			}
			maxVer := curVer
			for _, v := range cur.Versions {
				if v.Version > maxVer {
					maxVer = v.Version
				}
			}
			if cur.StoredName != "" {
				prevSnap := FileVersion{
					Version:      curVer,
					StoredName:   cur.StoredName,
					Size:         cur.Size,
					Uploader:     cur.Uploader,
					UploadedAt:   cur.UploadedAt,
					DeviceFP:     cur.DeviceFP,
					DeviceName:   cur.DeviceName,
					DeviceIP:     cur.DeviceIP,
					DeviceDetail: cur.DeviceDetail,
					StorageDir:   cur.StorageDir,
					Note:         cur.VersionNote,
					UpdateMethod: DefaultUpdateMethod(cur.UpdateMethod, curVer),
				}
				cur.Versions = append([]FileVersion{prevSnap}, cur.Versions...)
				sort.Slice(cur.Versions, func(i, j int) bool {
					return cur.Versions[i].Version > cur.Versions[j].Version
				})
			}
			cur.Version = maxVer + 1
			cur.VersionCount = 1 + len(cur.Versions)
			cur.VersionNote = versionNote
			cur.UpdateMethod = MethodLocalImport
			cur.StoredName = storedName
			cur.StorageMode = mode
			cur.SourcePath = item.fullPath
			cur.SourceMissing = false
			cur.MountID = ""
			cur.Size = item.size
			cur.Ext = ext
			cur.Category = ClassifyExt(ext)
			cur.Uploader = uploader
			cur.UploadedAt = now
			cur.MonthKey = now.Format("2006-01")
			cur.DateKey = now.Format("2006-01-02")
			cur.DeviceFP = strings.TrimSpace(dev.FP)
			cur.DeviceName = strings.TrimSpace(dev.Name)
			cur.DeviceIP = strings.TrimSpace(dev.IP)
			cur.DeviceDetail = strings.TrimSpace(dev.Detail)
			if len(normAllowed) > 0 {
				cur.AllowedUsers = normAllowed
			}
			if mode == StorageModeHardlink {
				cur.StorageDir = activeDir
			} else {
				cur.StorageDir = ""
			}
			cur.NodeOnline = true
			s.records[sameNameID] = cur
			rec = cur
			if err := s.saveLocked(); err != nil {
				s.mu.Unlock()
				if storedName != "" {
					_ = os.Remove(dstPath)
				}
				s.RecordWriteError()
				return nil, summary, err
			}
			s.mu.Unlock()
		} else {
			rec = FileRecord{
				ID:            blobID,
				Name:          origName,
				FolderPath:    targetFolder,
				StoredName:    storedName,
				Size:          item.size,
				Ext:           ext,
				Category:      ClassifyExt(ext),
				Uploader:      uploader,
				UploadedAt:    now,
				MonthKey:      now.Format("2006-01"),
				DateKey:       now.Format("2006-01-02"),
				DeviceFP:      strings.TrimSpace(dev.FP),
				DeviceName:    strings.TrimSpace(dev.Name),
				DeviceIP:      strings.TrimSpace(dev.IP),
				DeviceDetail:  strings.TrimSpace(dev.Detail),
				AllowedUsers:  normAllowed,
				StorageMode:   mode,
				SourcePath:    item.fullPath,
				SourceMissing: false,
				Version:       1,
				VersionCount:  1,
				VersionNote:   versionNote,
				UpdateMethod:  MethodLocalImport,
				NodeOnline:    true,
			}
			if mode == StorageModeHardlink {
				rec.StorageDir = activeDir
			}
			s.records[blobID] = rec
			if err := s.saveLocked(); err != nil {
				delete(s.records, blobID)
				s.mu.Unlock()
				if storedName != "" {
					_ = os.Remove(dstPath)
				}
				s.RecordWriteError()
				return nil, summary, err
			}
			s.mu.Unlock()
		}

		summary.TotalImported++
		summary.TotalBytes += rec.Size
		if mode == StorageModeHardlink {
			summary.HardlinkCount++
		} else {
			summary.RefCount++
		}
		out = append(out, rec)
	}

	return out, summary, nil
}

// RefreshRefStatus checks external ref records on disk and updates SourceMissing/Size flags.
func (s *Store) RefreshRefStatus() bool {
	s.mu.RLock()
	type refSnap struct {
		id      string
		path    string
		size    int64
		missing bool
	}
	snaps := make([]refSnap, 0)
	for id, rec := range s.records {
		if rec.StorageMode == StorageModeRef {
			snaps = append(snaps, refSnap{
				id:      id,
				path:    s.recordDiskPathLocked(rec),
				size:    rec.Size,
				missing: rec.SourceMissing,
			})
		}
	}
	s.mu.RUnlock()
	if len(snaps) == 0 {
		return false
	}

	type refUpdate struct {
		id      string
		size    int64
		modTime time.Time
		missing bool
	}
	updates := make([]refUpdate, 0)
	for _, sn := range snaps {
		info, err := os.Stat(sn.path)
		missing := err != nil || info.IsDir()
		newSize := sn.size
		var modTime time.Time
		if !missing {
			newSize = info.Size()
			modTime = info.ModTime()
		}
		if missing != sn.missing || (!missing && newSize != sn.size) {
			updates = append(updates, refUpdate{id: sn.id, size: newSize, modTime: modTime, missing: missing})
		}
	}
	if len(updates) == 0 {
		return false
	}

	s.mu.Lock()
	changed := false
	for _, up := range updates {
		rec, ok := s.records[up.id]
		if !ok || rec.StorageMode != StorageModeRef {
			continue
		}
		if rec.SourceMissing != up.missing || (!up.missing && rec.Size != up.size) {
			rec.SourceMissing = up.missing
			if !up.missing {
				rec.Size = up.size
				if !up.modTime.IsZero() {
					rec.UploadedAt = up.modTime
					rec.MonthKey = up.modTime.Format("2006-01")
					rec.DateKey = up.modTime.Format("2006-01-02")
				}
			}
			s.records[up.id] = rec
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	return changed
}

// ListMounts returns all configured zero-copy mounted directories ordered by creation time.
func (s *Store) ListMounts() []MountEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MountEntry, 0, len(s.mounts))
	for _, m := range s.mounts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// AddMount registers a host directory as a read-only zero-copy mount and runs its initial scan.
func (s *Store) AddMount(dirPath, alias, addedBy string, allowedUsers []string) (MountEntry, int, error) {
	return s.AddMountWithOptions(dirPath, alias, addedBy, allowedUsers, AddMountOptions{})
}

// AddMountWithOptions registers a host directory mount with optional writable workspace settings.
func (s *Store) AddMountWithOptions(dirPath, alias, addedBy string, allowedUsers []string, opts AddMountOptions) (MountEntry, int, error) {
	cleanDir, err := cleanLocalAbsPath(dirPath)
	if err != nil {
		return MountEntry{}, 0, err
	}
	info, statErr := os.Stat(cleanDir)
	if statErr != nil {
		return MountEntry{}, 0, fmt.Errorf("无法访问挂载目录 %s: %w", cleanDir, statErr)
	}
	if !info.IsDir() {
		return MountEntry{}, 0, errors.New("挂载路径必须是文件夹")
	}
	activeDir := s.UploadDir()
	if isSubOrSamePath(cleanDir, activeDir) || isSubOrSamePath(cleanDir, s.defaultUploadDir) {
		return MountEntry{}, 0, errors.New("不能挂载 QuailNest 自身内部存储目录")
	}

	cleanAlias := SanitizeFolderPath(alias)
	if cleanAlias == "" {
		base := filepath.Base(cleanDir)
		if base == "/" || base == `\` || base == "." || base == "" {
			base = strings.TrimSuffix(strings.TrimSuffix(cleanDir, `\`), ":")
			if base == "" {
				base = "mounted-root"
			}
		}
		cleanAlias = SanitizeFolderPath(base)
	}

	ignorePatterns := normalizeIgnorePatterns(opts.IgnorePatterns)
	if !opts.IgnoreSet && opts.Writable && len(ignorePatterns) == 0 {
		ignorePatterns = DefaultProjectIgnorePatterns()
	}
	shadowBackup := false
	if opts.ShadowBackup != nil {
		shadowBackup = *opts.ShadowBackup
	} else if opts.Writable {
		shadowBackup = true
	}
	maxFileBytes := opts.MaxFileBytes
	if maxFileBytes <= 0 && opts.Writable {
		maxFileBytes = DefaultMountMaxFileBytes
	}
	verifyPresets, vErr := NormalizeVerifyPresets(opts.VerifyPresets)
	if vErr != nil {
		return MountEntry{}, 0, vErr
	}

	s.mu.Lock()
	for _, existing := range s.mounts {
		if sameOSPath(existing.DirPath, cleanDir) {
			s.mu.Unlock()
			return MountEntry{}, 0, fmt.Errorf("目录 %s 已挂载（别名：%s）", cleanDir, existing.Alias)
		}
	}
	entry := MountEntry{
		ID:             "mnt-" + newID(),
		DirPath:        cleanDir,
		Alias:          cleanAlias,
		AllowedUsers:   NormalizeAllowedUsers(allowedUsers),
		AddedBy:        strings.TrimSpace(addedBy),
		Status:         "ok",
		Writable:       opts.Writable,
		ShadowBackup:   shadowBackup,
		IgnorePatterns: ignorePatterns,
		MaxFileBytes:   maxFileBytes,
		VerifyPresets:  verifyPresets,
		CreatedAt:      time.Now(),
	}
	if entry.AddedBy == "" {
		entry.AddedBy = "admin"
	}
	s.mounts[entry.ID] = entry
	if saveErr := s.saveMountsLocked(); saveErr != nil {
		delete(s.mounts, entry.ID)
		s.mu.Unlock()
		return MountEntry{}, 0, saveErr
	}
	s.mu.Unlock()

	res, syncErr := s.SyncMount(entry.ID)
	s.mu.RLock()
	latest := s.mounts[entry.ID]
	s.mu.RUnlock()
	if syncErr != nil {
		return latest, res.Added, syncErr
	}
	return latest, res.Added, nil
}

// UpdateMount updates mount settings (such as Writable, ShadowBackup, IgnorePatterns) and re-syncs the mount index.
func (s *Store) UpdateMount(mountID string, opts UpdateMountOptions) (MountEntry, MountSyncResult, error) {
	id := strings.TrimSpace(mountID)
	s.mu.Lock()
	cur, ok := s.mounts[id]
	if !ok {
		s.mu.Unlock()
		return MountEntry{}, MountSyncResult{}, errors.New("挂载目录不存在")
	}
	prev := cur
	wasWritable := cur.Writable
	if opts.Alias != nil {
		cleanAlias := SanitizeFolderPath(*opts.Alias)
		if cleanAlias == "" {
			s.mu.Unlock()
			return MountEntry{}, MountSyncResult{}, errors.New("挂载别名不能为空")
		}
		cur.Alias = cleanAlias
	}
	if opts.AllowedUsers != nil {
		cur.AllowedUsers = NormalizeAllowedUsers(*opts.AllowedUsers)
	}
	if opts.Writable != nil {
		cur.Writable = *opts.Writable
	}
	if opts.ShadowBackup != nil {
		cur.ShadowBackup = *opts.ShadowBackup
	} else if !wasWritable && cur.Writable {
		cur.ShadowBackup = true
	}
	if opts.IgnorePatterns != nil {
		cur.IgnorePatterns = normalizeIgnorePatterns(*opts.IgnorePatterns)
	} else if !wasWritable && cur.Writable && len(cur.IgnorePatterns) == 0 {
		cur.IgnorePatterns = DefaultProjectIgnorePatterns()
	}
	if opts.MaxFileBytes != nil {
		if *opts.MaxFileBytes <= 0 {
			cur.MaxFileBytes = DefaultMountMaxFileBytes
		} else {
			cur.MaxFileBytes = *opts.MaxFileBytes
		}
	} else if cur.Writable && cur.MaxFileBytes <= 0 {
		cur.MaxFileBytes = DefaultMountMaxFileBytes
	}
	if opts.VerifyPresets != nil {
		cleanPresets, vErr := NormalizeVerifyPresets(*opts.VerifyPresets)
		if vErr != nil {
			s.mu.Unlock()
			return MountEntry{}, MountSyncResult{}, vErr
		}
		cur.VerifyPresets = cleanPresets
	}

	s.mounts[id] = cur
	if err := s.saveMountsLocked(); err != nil {
		s.mounts[id] = prev
		s.mu.Unlock()
		return MountEntry{}, MountSyncResult{}, err
	}
	s.mu.Unlock()

	res, syncErr := s.SyncMount(id)
	s.mu.RLock()
	latest := s.mounts[id]
	s.mu.RUnlock()
	if syncErr != nil {
		return latest, res, syncErr
	}
	return latest, res, nil
}

// GetMount looks up a mount configuration entry by mountID.
func (s *Store) GetMount(mountID string) (MountEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mounts[strings.TrimSpace(mountID)]
	return m, ok
}

// FindMountByFolderPath locates the configured mount owning folderPath and returns the relative subpath inside it.
func (s *Store) FindMountByFolderPath(folderPath string) (MountEntry, string, bool) {
	clean := SanitizeFolderPath(folderPath)
	if clean == "" {
		return MountEntry{}, "", false
	}
	lowClean := strings.ToLower(clean)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var best MountEntry
	var bestRel string
	bestAliasLen := -1
	for _, m := range s.mounts {
		alias := SanitizeFolderPath(m.Alias)
		if alias == "" {
			continue
		}
		lowAlias := strings.ToLower(alias)
		if lowClean == lowAlias {
			if len(alias) > bestAliasLen {
				best = m
				bestRel = ""
				bestAliasLen = len(alias)
			}
			continue
		}
		if strings.HasPrefix(lowClean, lowAlias+"/") && len(alias) > bestAliasLen {
			best = m
			bestRel = clean[len(alias)+1:]
			bestAliasLen = len(alias)
		}
	}
	if bestAliasLen < 0 {
		return MountEntry{}, "", false
	}
	return best, bestRel, true
}

// RemoveMount removes a directory mount, cleans its Shadow history blobs in uploads/, without deleting host source files.
func (s *Store) RemoveMount(mountID string) (int, error) {
	s.mu.Lock()
	if _, ok := s.mounts[mountID]; !ok {
		s.mu.Unlock()
		return 0, errors.New("挂载目录不存在")
	}
	delete(s.mounts, mountID)
	removed := 0
	var shadowPaths []string
	var removedIDs []string
	for id, rec := range s.records {
		if rec.MountID == mountID {
			for _, v := range rec.Versions {
				shadowPaths = append(shadowPaths, s.versionDiskPathLocked(rec, v))
			}
			removedIDs = append(removedIDs, id)
			delete(s.records, id)
			removed++
		}
	}
	if err := s.saveMountsLocked(); err != nil {
		s.mu.Unlock()
		return removed, err
	}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return removed, err
	}
	s.mu.Unlock()

	for _, sp := range shadowPaths {
		if sp != "" {
			_ = os.Remove(sp)
		}
	}
	s.clearLeases(removedIDs)
	return removed, nil
}

// SyncMount scans a single mounted directory and reconciles index entries against disk contents.
func (s *Store) SyncMount(mountID string) (MountSyncResult, error) {
	s.mu.RLock()
	mount, ok := s.mounts[mountID]
	s.mu.RUnlock()
	if !ok {
		return MountSyncResult{}, errors.New("挂载目录不存在")
	}

	rootInfo, statErr := os.Stat(mount.DirPath)
	now := time.Now()
	if statErr != nil || !rootInfo.IsDir() {
		errText := "目录不可访问"
		if statErr != nil {
			errText = statErr.Error()
		}
		s.mu.Lock()
		cur, exists := s.mounts[mountID]
		if !exists {
			s.mu.Unlock()
			return MountSyncResult{}, errors.New("挂载目录不存在")
		}
		recordsChanged := false
		mountChanged := cur.Status != "missing" || cur.LastError != errText
		cur.Status = "missing"
		cur.LastError = errText
		cur.LastSyncAt = now
		s.mounts[mountID] = cur
		for id, rec := range s.records {
			if rec.MountID == mountID && !rec.SourceMissing {
				rec.SourceMissing = true
				s.records[id] = rec
				recordsChanged = true
			}
		}
		_ = s.saveMountsLocked()
		if recordsChanged {
			_ = s.saveLocked()
		}
		res := MountSyncResult{
			MountID:    mountID,
			FileCount:  cur.FileCount,
			TotalBytes: cur.TotalBytes,
		}
		s.mu.Unlock()
		if mountChanged && recordsChanged {
			return res, nil
		}
		return res, nil
	}

	_, items, _, walkErr := collectLocalFilesWithIgnore(mount.DirPath, maxZeroCopyWalkFiles, mount.IgnorePatterns, mount.Writable)
	walkedByPath := make(map[string]walkedLocalFile, len(items))
	var totalBytes int64
	for _, it := range items {
		key := filepath.Clean(it.fullPath)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		walkedByPath[key] = it
		totalBytes += it.size
	}

	s.mu.Lock()
	cur, exists := s.mounts[mountID]
	if !exists {
		s.mu.Unlock()
		return MountSyncResult{}, errors.New("挂载目录不存在")
	}

	existingByPath := make(map[string]string)
	var res MountSyncResult
	res.MountID = mountID
	res.FileCount = len(items)
	res.TotalBytes = totalBytes

	var removedIDs []string
	var shadowPathsToRemove []string
	for id, rec := range s.records {
		if rec.MountID != mountID {
			continue
		}
		key := filepath.Clean(rec.SourcePath)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, keep := walkedByPath[key]; !keep {
			for _, v := range rec.Versions {
				shadowPathsToRemove = append(shadowPathsToRemove, s.versionDiskPathLocked(rec, v))
			}
			removedIDs = append(removedIDs, id)
			delete(s.records, id)
			res.Removed++
			continue
		}
		existingByPath[key] = id
	}

	for key, it := range walkedByPath {
		folderPath := folderPathForWalkedItem(cur.Alias, it.relSlash, true)
		name := sanitizeFilename(it.name)
		ext := strings.ToLower(filepath.Ext(name))
		if id, ok := existingByPath[key]; ok {
			rec := s.records[id]
			modDiff := rec.UploadedAt.Unix() != it.modTime.Unix()
			if rec.SourceMissing || rec.Size != it.size || modDiff || rec.FolderPath != folderPath || rec.Name != name {
				if rec.Size != it.size || modDiff {
					if rec.Version <= 0 {
						rec.Version = 1
					}
					rec.Version++
					rec.VersionCount = 1 + len(rec.Versions)
					rec.SHA256 = ""
					rec.UpdateMethod = MethodMountSync
					rec.VersionNote = "宿主机文件变更同步"
				}
				rec.Name = name
				rec.FolderPath = folderPath
				rec.Ext = ext
				rec.Category = ClassifyExt(ext)
				rec.Size = it.size
				rec.UploadedAt = it.modTime
				rec.MonthKey = it.modTime.Format("2006-01")
				rec.DateKey = it.modTime.Format("2006-01-02")
				rec.SourceMissing = false
				rec.AllowedUsers = cur.AllowedUsers
				rec.NodeOnline = true
				s.records[id] = rec
				res.Updated++
			}
			continue
		}

		id := newID()
		s.records[id] = FileRecord{
			ID:           id,
			Name:         name,
			FolderPath:   folderPath,
			Size:         it.size,
			Ext:          ext,
			Category:     ClassifyExt(ext),
			Uploader:     cur.AddedBy,
			UploadedAt:   it.modTime,
			MonthKey:     it.modTime.Format("2006-01"),
			DateKey:      it.modTime.Format("2006-01-02"),
			AllowedUsers: cur.AllowedUsers,
			StorageMode:  StorageModeMount,
			SourcePath:   it.fullPath,
			MountID:      mountID,
			Version:      1,
			VersionCount: 1,
			VersionNote:  "本机目录零拷贝挂载",
			UpdateMethod: MethodMountSync,
			NodeOnline:   true,
		}
		res.Added++
	}

	warnErr := ""
	if walkErr != nil {
		warnErr = walkErr.Error()
	}
	metaChanged := cur.Status != "ok" || cur.FileCount != res.FileCount || cur.TotalBytes != res.TotalBytes || cur.LastError != warnErr
	cur.Status = "ok"
	cur.FileCount = res.FileCount
	cur.TotalBytes = res.TotalBytes
	cur.LastSyncAt = now
	cur.LastError = warnErr
	s.mounts[mountID] = cur

	if res.Added > 0 || res.Updated > 0 || res.Removed > 0 {
		if err := s.saveLocked(); err != nil {
			s.mu.Unlock()
			s.RecordWriteError()
			return res, err
		}
	}
	if metaChanged || res.Added > 0 || res.Updated > 0 || res.Removed > 0 {
		if err := s.saveMountsLocked(); err != nil {
			s.mu.Unlock()
			s.RecordWriteError()
			return res, err
		}
	}
	s.mu.Unlock()

	for _, sp := range shadowPathsToRemove {
		if sp != "" {
			_ = os.Remove(sp)
		}
	}
	s.clearLeases(removedIDs)
	return res, nil
}

func (s *Store) recomputeMountStatsLocked(mountID string) {
	m, ok := s.mounts[mountID]
	if !ok {
		return
	}
	var cnt int
	var total int64
	for _, rec := range s.records {
		if rec.MountID == mountID && !rec.SourceMissing {
			cnt++
			total += rec.Size
		}
	}
	m.FileCount = cnt
	m.TotalBytes = total
	m.LastSyncAt = time.Now()
	s.mounts[mountID] = m
}

// backupFileToShadowBlob creates an independent copy of srcPath inside activeDir so future in-place host writes never mutate history.
func backupFileToShadowBlob(srcPath, activeDir, ext string) (string, int64, string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, "", err
	}
	defer src.Close()
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		return "", 0, "", err
	}
	storedName := newID() + ext
	dstPath := filepath.Join(activeDir, storedName)
	n, shaHex, err := writeBlobAndHash(dstPath, src)
	if err != nil {
		_ = os.Remove(dstPath)
		return "", 0, "", err
	}
	return storedName, n, shaHex, nil
}

// atomicWriteFile streams up to maxBytes into a same-directory temp file, fsyncs, and atomically replaces dstPath.
func atomicWriteFile(dstPath string, r io.Reader, maxBytes int64) (int64, string, os.FileInfo, error) {
	dir := filepath.Dir(dstPath)
	tmp, err := os.CreateTemp(dir, ".lfh_tmp_*")
	if err != nil {
		return 0, "", nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	h := sha256.New()
	reader := r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}
	n, copyErr := io.Copy(tmp, io.TeeReader(reader, h))
	if copyErr != nil {
		_ = tmp.Close()
		cleanup()
		return 0, "", nil, copyErr
	}
	if maxBytes > 0 && n > maxBytes {
		_ = tmp.Close()
		cleanup()
		return 0, "", nil, fmt.Errorf("文件大小超过可写工作区单文件上限 (%d 字节)", maxBytes)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		cleanup()
		return 0, "", nil, syncErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		cleanup()
		return 0, "", nil, closeErr
	}

	var renameErr error
	for attempt := 0; attempt < 4; attempt++ {
		renameErr = os.Rename(tmpPath, dstPath)
		if renameErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if renameErr != nil {
		cleanup()
		return 0, "", nil, renameErr
	}
	fi, statErr := os.Stat(dstPath)
	if statErr != nil {
		return n, hex.EncodeToString(h.Sum(nil)), nil, nil
	}
	return n, hex.EncodeToString(h.Sum(nil)), fi, nil
}

// WriteMountFile safely writes through a file into a writable mounted workspace directory
// using striped path locking, optimistic version & lease checks, Shadow backup, and temp+rename.
func (s *Store) WriteMountFile(mountID, relSlash, note, method, uploader string, allowedUsers []string, dev DeviceInfo, r io.Reader, conc WriteConcurrencyOptions) (FileRecord, error) {
	cleanRel, err := cleanMountRelPath(relSlash)
	if err != nil {
		return FileRecord{}, err
	}
	s.mu.RLock()
	mount, ok := s.mounts[strings.TrimSpace(mountID)]
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("挂载目录不存在")
	}
	if !mount.Writable {
		return FileRecord{}, errors.New("该挂载目录未开启可写工作区权限")
	}
	folderPath := folderPathForWalkedItem(mount.Alias, cleanRel, true)
	fileName := sanitizeFilename(path.Base(cleanRel))
	unlock := s.lockPath(folderPath, fileName)
	defer unlock()

	return s.writeMountFileUnderLock(mount.ID, cleanRel, note, method, uploader, allowedUsers, dev, r, conc)
}

func (s *Store) writeMountFileUnderLock(mountID, relSlash, note, method, uploader string, allowedUsers []string, dev DeviceInfo, r io.Reader, conc WriteConcurrencyOptions) (FileRecord, error) {
	if conc.ExpectedVersion != nil && *conc.ExpectedVersion < 0 {
		return FileRecord{}, errors.New("expected_version 不能为负数")
	}
	cleanRel, err := cleanMountRelPath(relSlash)
	if err != nil {
		return FileRecord{}, err
	}
	if isHardcodedBlockedMountPath(cleanRel) {
		return FileRecord{}, errors.New("安全策略禁止读写 .git / .ssh / .env 或私钥等敏感文件")
	}

	s.mu.RLock()
	mount, ok := s.mounts[strings.TrimSpace(mountID)]
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("挂载目录不存在")
	}
	if !mount.Writable {
		return FileRecord{}, errors.New("该挂载目录未开启可写工作区权限")
	}
	baseName := sanitizeFilename(path.Base(cleanRel))
	if matchesIgnorePatterns(cleanRel, baseName, mount.IgnorePatterns) {
		return FileRecord{}, errors.New("目标路径命中挂载目录忽略规则，禁止写入")
	}

	absPath := filepath.Clean(filepath.Join(mount.DirPath, filepath.FromSlash(cleanRel)))
	if err := verifyNoSymlinkPath(mount.DirPath, absPath); err != nil {
		return FileRecord{}, err
	}

	folderPath := folderPathForWalkedItem(mount.Alias, cleanRel, true)
	ext := strings.ToLower(filepath.Ext(baseName))

	s.mu.RLock()
	var existing *FileRecord
	for _, cur := range s.records {
		if cur.MountID == mount.ID && (sameOSPath(cur.SourcePath, absPath) || (cur.FolderPath == folderPath && strings.EqualFold(cur.Name, baseName))) {
			cp := cur
			existing = &cp
			break
		}
	}
	s.mu.RUnlock()

	if existing != nil {
		if leaseErr := s.CheckLeaseForWrite(existing.ID, conc.ActorKeyID, conc.ForceLease); leaseErr != nil {
			return FileRecord{}, leaseErr
		}
	}
	if err := checkExpectedVersion(existing, folderPath, baseName, conc.ExpectedVersion); err != nil {
		return FileRecord{}, err
	}

	// If file exists untracked on disk and caller asserted create-only (expected_version=0), reject cleanly.
	_, diskStatErr := os.Stat(absPath)
	diskExists := diskStatErr == nil
	if existing == nil && diskExists && conc.ExpectedVersion != nil && *conc.ExpectedVersion == 0 {
		return FileRecord{}, &ConflictError{
			Path:            logicalFilePath(folderPath, baseName),
			ExpectedVersion: 0,
			CurrentVersion:  1,
		}
	}

	// Read incoming payload (up to maxBytes + 1) BEFORE touching disk so failed limit/lease/version leaves disk untouched.
	maxBytes := effectiveMaxFileBytes(mount)
	var dataBuf []byte
	if maxBytes > 0 {
		dataBuf, err = io.ReadAll(io.LimitReader(r, maxBytes+1))
		if err != nil {
			s.RecordReadError()
			return FileRecord{}, err
		}
		if int64(len(dataBuf)) > maxBytes {
			return FileRecord{}, fmt.Errorf("文件大小超过可写工作区单文件上限 (%d 字节)", maxBytes)
		}
	} else {
		dataBuf, err = io.ReadAll(r)
		if err != nil {
			s.RecordReadError()
			return FileRecord{}, err
		}
	}

	// Create parent directories safely and verify realpath containment against symlinks.
	parentDir := filepath.Dir(absPath)
	if mkErr := os.MkdirAll(parentDir, 0755); mkErr != nil {
		s.RecordWriteError()
		return FileRecord{}, mkErr
	}
	if symErr := verifyNoSymlinkPath(mount.DirPath, absPath); symErr != nil {
		return FileRecord{}, symErr
	}
	if realErr := verifyParentRealPathInsideMount(mount.DirPath, parentDir); realErr != nil {
		return FileRecord{}, realErr
	}

	// Shadow backup existing host file into uploads/ before overwriting.
	var shadowSnap *FileVersion
	activeDir := s.UploadDir()
	if diskExists && mount.ShadowBackup {
		shadowStored, shadowSize, shadowSHA, bErr := backupFileToShadowBlob(absPath, activeDir, ext)
		if bErr != nil {
			s.RecordWriteError()
			return FileRecord{}, fmt.Errorf("创建 Shadow 版本备份失败: %w", bErr)
		}
		snapVer := 1
		snapUploader := uploader
		snapTime := time.Now()
		snapMethod := MethodMountSync
		snapNote := "写穿前自动备份"
		var snapDev DeviceInfo
		if existing != nil {
			if existing.Version > 0 {
				snapVer = existing.Version
			}
			snapUploader = existing.Uploader
			snapTime = existing.UploadedAt
			snapMethod = DefaultUpdateMethod(existing.UpdateMethod, snapVer)
			snapNote = existing.VersionNote
			snapDev = DeviceInfo{
				FP:     existing.DeviceFP,
				Name:   existing.DeviceName,
				IP:     existing.DeviceIP,
				Detail: existing.DeviceDetail,
			}
		} else if st, stErr := os.Stat(absPath); stErr == nil {
			snapTime = st.ModTime()
		}
		shadowSnap = &FileVersion{
			Version:      snapVer,
			StoredName:   shadowStored,
			Size:         shadowSize,
			Uploader:     snapUploader,
			UploadedAt:   snapTime,
			DeviceFP:     snapDev.FP,
			DeviceName:   snapDev.Name,
			DeviceIP:     snapDev.IP,
			DeviceDetail: snapDev.Detail,
			StorageDir:   activeDir,
			SHA256:       shadowSHA,
			Note:         snapNote,
			UpdateMethod: snapMethod,
		}
	}

	if existing != nil {
		if leaseErr := s.CheckLeaseForWrite(existing.ID, conc.ActorKeyID, conc.ForceLease); leaseErr != nil {
			if shadowSnap != nil {
				_ = os.Remove(filepath.Join(activeDir, shadowSnap.StoredName))
			}
			return FileRecord{}, leaseErr
		}
	}

	n, shaHex, fi, writeErr := atomicWriteFile(absPath, strings.NewReader(string(dataBuf)), maxBytes)
	if writeErr != nil {
		if shadowSnap != nil {
			_ = os.Remove(filepath.Join(activeDir, shadowSnap.StoredName))
		}
		s.RecordWriteError()
		return FileRecord{}, writeErr
	}

	fileMod := time.Now()
	if fi != nil {
		fileMod = fi.ModTime()
	}
	cleanNote := strings.TrimSpace(note)
	normAllowed := NormalizeAllowedUsers(allowedUsers)

	s.mu.Lock()
	curMount, mStill := s.mounts[mount.ID]
	if !mStill {
		s.mu.Unlock()
		return FileRecord{}, errors.New("挂载目录已移除")
	}

	var recID string
	for id, cur := range s.records {
		if cur.MountID == curMount.ID && (sameOSPath(cur.SourcePath, absPath) || (cur.FolderPath == folderPath && strings.EqualFold(cur.Name, baseName))) {
			recID = id
			break
		}
	}
	var curPtr *FileRecord
	if recID != "" {
		cp := s.records[recID]
		curPtr = &cp
	}
	if verErr := checkExpectedVersion(curPtr, folderPath, baseName, conc.ExpectedVersion); verErr != nil {
		s.mu.Unlock()
		return FileRecord{}, verErr
	}

	var rec FileRecord
	if recID != "" {
		rec = s.records[recID]
		curVer := rec.Version
		if curVer <= 0 {
			curVer = 1
		}
		maxVer := curVer
		for _, v := range rec.Versions {
			if v.Version > maxVer {
				maxVer = v.Version
			}
		}
		if shadowSnap != nil {
			shadowSnap.Version = curVer
			rec.Versions = append([]FileVersion{*shadowSnap}, rec.Versions...)
			sort.Slice(rec.Versions, func(i, j int) bool {
				return rec.Versions[i].Version > rec.Versions[j].Version
			})
		}
		rec.Name = baseName
		rec.FolderPath = folderPath
		rec.Ext = ext
		rec.Category = ClassifyExt(ext)
		rec.Size = n
		rec.StoredName = ""
		rec.StorageDir = ""
		rec.StorageMode = StorageModeMount
		rec.SourcePath = absPath
		rec.SourceMissing = false
		rec.MountID = curMount.ID
		rec.SHA256 = shaHex
		rec.IntegrityStatus = "ok"
		verifiedNow := time.Now()
		rec.LastVerifiedAt = &verifiedNow
		rec.Version = maxVer + 1
		rec.VersionCount = 1 + len(rec.Versions)
		rec.VersionNote = cleanNote
		if strings.TrimSpace(method) == "" {
			method = MethodUploadOverwrite
		}
		rec.UpdateMethod = strings.TrimSpace(method)
		rec.Uploader = uploader
		rec.UploadedAt = fileMod
		rec.MonthKey = fileMod.Format("2006-01")
		rec.DateKey = fileMod.Format("2006-01-02")
		rec.DeviceFP = strings.TrimSpace(dev.FP)
		rec.DeviceName = strings.TrimSpace(dev.Name)
		rec.DeviceIP = strings.TrimSpace(dev.IP)
		rec.DeviceDetail = strings.TrimSpace(dev.Detail)
		if len(normAllowed) > 0 {
			rec.AllowedUsers = normAllowed
		} else if len(rec.AllowedUsers) == 0 && len(curMount.AllowedUsers) > 0 {
			rec.AllowedUsers = curMount.AllowedUsers
		}
		rec.NodeOnline = true
		s.records[recID] = rec
	} else {
		newRecID := newID()
		var versions []FileVersion
		nextVer := 1
		if shadowSnap != nil {
			versions = []FileVersion{*shadowSnap}
			nextVer = 2
		}
		effAllowed := normAllowed
		if len(effAllowed) == 0 && len(curMount.AllowedUsers) > 0 {
			effAllowed = curMount.AllowedUsers
		}
		verifiedNow := time.Now()
		if strings.TrimSpace(method) == "" {
			method = MethodInitialUpload
		}
		rec = FileRecord{
			ID:              newRecID,
			Name:            baseName,
			FolderPath:      folderPath,
			Size:            n,
			Ext:             ext,
			Category:        ClassifyExt(ext),
			Uploader:        uploader,
			UploadedAt:      fileMod,
			MonthKey:        fileMod.Format("2006-01"),
			DateKey:         fileMod.Format("2006-01-02"),
			DeviceFP:        strings.TrimSpace(dev.FP),
			DeviceName:      strings.TrimSpace(dev.Name),
			DeviceIP:        strings.TrimSpace(dev.IP),
			DeviceDetail:    strings.TrimSpace(dev.Detail),
			AllowedUsers:    effAllowed,
			StorageMode:     StorageModeMount,
			SourcePath:      absPath,
			MountID:         curMount.ID,
			SHA256:          shaHex,
			IntegrityStatus: "ok",
			LastVerifiedAt:  &verifiedNow,
			Version:         nextVer,
			VersionCount:    1 + len(versions),
			VersionNote:     cleanNote,
			UpdateMethod:    strings.TrimSpace(method),
			Versions:        versions,
			NodeOnline:      true,
		}
		s.records[newRecID] = rec
	}

	s.recomputeMountStatsLocked(curMount.ID)
	if err := s.appendWALUpsertLocked(rec); err != nil {
		s.mu.Unlock()
		s.RecordWriteError()
		return FileRecord{}, err
	}
	if err := s.saveMountsLocked(); err != nil {
		s.mu.Unlock()
		s.RecordWriteError()
		return FileRecord{}, err
	}
	s.mu.Unlock()
	s.AddUploadBytes(n)
	return rec, nil
}

func (s *Store) restoreMountVersionUnderLock(id string, targetVer int, note, actor string, dev DeviceInfo, conc WriteConcurrencyOptions) (FileRecord, error) {
	s.mu.RLock()
	rec, ok := s.records[id]
	var mount MountEntry
	var mOK bool
	if ok {
		mount, mOK = s.mounts[rec.MountID]
	}
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}
	if !mOK || !mount.Writable {
		return FileRecord{}, errors.New("仅开启可写工作区的挂载目录支持版本回滚写回")
	}
	if err := checkExpectedVersion(&rec, rec.FolderPath, rec.Name, conc.ExpectedVersion); err != nil {
		return FileRecord{}, err
	}
	curVer := rec.Version
	if curVer <= 0 {
		curVer = 1
	}
	if targetVer == curVer {
		if strings.TrimSpace(note) != "" {
			s.mu.Lock()
			if cur, exists := s.records[id]; exists {
				cur.VersionNote = strings.TrimSpace(note)
				s.records[id] = cur
				_ = s.appendWALUpsertLocked(cur)
				rec = cur
			}
			s.mu.Unlock()
		}
		return rec, nil
	}

	var target FileVersion
	found := false
	for _, v := range rec.Versions {
		if v.Version == targetVer {
			target = v
			found = true
			break
		}
	}
	if !found {
		return FileRecord{}, fmt.Errorf("未找到历史版本 v%d", targetVer)
	}
	vPath := s.versionDiskPathLocked(rec, target)
	if _, err := os.Stat(vPath); err != nil {
		s.RecordReadError()
		return FileRecord{}, fmt.Errorf("历史版本 v%d 的 Shadow 备份已丢失", targetVer)
	}

	rel, relErr := filepath.Rel(mount.DirPath, rec.SourcePath)
	if relErr != nil {
		return FileRecord{}, relErr
	}
	cleanRel, cleanErr := cleanMountRelPath(filepath.ToSlash(rel))
	if cleanErr != nil {
		return FileRecord{}, cleanErr
	}
	if isHardcodedBlockedMountPath(cleanRel) {
		return FileRecord{}, errors.New("安全策略禁止回滚写入受保护文件")
	}
	if symErr := verifyNoSymlinkPath(mount.DirPath, rec.SourcePath); symErr != nil {
		return FileRecord{}, symErr
	}
	if realErr := verifyParentRealPathInsideMount(mount.DirPath, filepath.Dir(rec.SourcePath)); realErr != nil {
		return FileRecord{}, realErr
	}

	// Backup active host file before overwriting with historical snapshot.
	activeDir := s.UploadDir()
	var shadowSnap *FileVersion
	if mount.ShadowBackup {
		if _, stErr := os.Stat(rec.SourcePath); stErr == nil {
			shadowStored, shadowSize, shadowSHA, bErr := backupFileToShadowBlob(rec.SourcePath, activeDir, rec.Ext)
			if bErr != nil {
				s.RecordWriteError()
				return FileRecord{}, fmt.Errorf("创建回滚前 Shadow 备份失败: %w", bErr)
			}
			shadowSnap = &FileVersion{
				Version:      curVer,
				StoredName:   shadowStored,
				Size:         shadowSize,
				Uploader:     rec.Uploader,
				UploadedAt:   rec.UploadedAt,
				DeviceFP:     rec.DeviceFP,
				DeviceName:   rec.DeviceName,
				DeviceIP:     rec.DeviceIP,
				DeviceDetail: rec.DeviceDetail,
				StorageDir:   activeDir,
				SHA256:       shadowSHA,
				Note:         rec.VersionNote,
				UpdateMethod: DefaultUpdateMethod(rec.UpdateMethod, curVer),
			}
		}
	}

	vf, openErr := os.Open(vPath)
	if openErr != nil {
		if shadowSnap != nil {
			_ = os.Remove(filepath.Join(activeDir, shadowSnap.StoredName))
		}
		s.RecordReadError()
		return FileRecord{}, openErr
	}
	n, shaHex, fi, writeErr := atomicWriteFile(rec.SourcePath, vf, effectiveMaxFileBytes(mount))
	_ = vf.Close()
	if writeErr != nil {
		if shadowSnap != nil {
			_ = os.Remove(filepath.Join(activeDir, shadowSnap.StoredName))
		}
		s.RecordWriteError()
		return FileRecord{}, writeErr
	}

	fileMod := time.Now()
	if fi != nil {
		fileMod = fi.ModTime()
	}

	s.mu.Lock()
	latest, exists := s.records[id]
	if !exists {
		s.mu.Unlock()
		return FileRecord{}, errors.New("文件不存在")
	}
	maxVer := curVer
	for _, v := range latest.Versions {
		if v.Version > maxVer {
			maxVer = v.Version
		}
	}
	if shadowSnap != nil {
		latest.Versions = append([]FileVersion{*shadowSnap}, latest.Versions...)
		sort.Slice(latest.Versions, func(i, j int) bool {
			return latest.Versions[i].Version > latest.Versions[j].Version
		})
	}
	latest.Version = maxVer + 1
	latest.VersionCount = 1 + len(latest.Versions)
	latest.Size = n
	latest.SHA256 = shaHex
	latest.IntegrityStatus = "ok"
	latest.SourceMissing = false
	verifiedNow := time.Now()
	latest.LastVerifiedAt = &verifiedNow
	latest.UpdateMethod = MethodVersionRestore
	if strings.TrimSpace(actor) != "" {
		latest.Uploader = strings.TrimSpace(actor)
	}
	latest.UploadedAt = fileMod
	latest.MonthKey = fileMod.Format("2006-01")
	latest.DateKey = fileMod.Format("2006-01-02")
	if strings.TrimSpace(dev.FP) != "" || strings.TrimSpace(dev.Name) != "" || strings.TrimSpace(dev.IP) != "" {
		latest.DeviceFP = strings.TrimSpace(dev.FP)
		latest.DeviceName = strings.TrimSpace(dev.Name)
		latest.DeviceIP = strings.TrimSpace(dev.IP)
		latest.DeviceDetail = strings.TrimSpace(dev.Detail)
	}
	if strings.TrimSpace(note) != "" {
		latest.VersionNote = strings.TrimSpace(note)
	} else {
		latest.VersionNote = fmt.Sprintf("回滚至 v%d (%s)", targetVer, target.Note)
	}
	s.records[id] = latest
	s.recomputeMountStatsLocked(mount.ID)
	if err := s.appendWALUpsertLocked(latest); err != nil {
		s.mu.Unlock()
		s.RecordWriteError()
		return FileRecord{}, err
	}
	if err := s.saveMountsLocked(); err != nil {
		s.mu.Unlock()
		s.RecordWriteError()
		return FileRecord{}, err
	}
	s.mu.Unlock()
	return latest, nil
}

// SyncAllMounts scans every configured directory mount and reports aggregate changes.
func (s *Store) SyncAllMounts() (changed bool, added, updated, removed int) {
	mounts := s.ListMounts()
	for _, m := range mounts {
		res, err := s.SyncMount(m.ID)
		if err != nil {
			continue
		}
		added += res.Added
		updated += res.Updated
		removed += res.Removed
		if res.Added > 0 || res.Updated > 0 || res.Removed > 0 {
			changed = true
		}
	}
	return changed, added, updated, removed
}
