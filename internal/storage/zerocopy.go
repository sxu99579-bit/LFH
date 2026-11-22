package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const maxZeroCopyWalkFiles = 10000

// MountEntry describes a host directory exposed through zero-copy virtual mount.
type MountEntry struct {
	ID           string    `json:"id"`
	DirPath      string    `json:"dir_path"`
	Alias        string    `json:"alias"`
	AllowedUsers []string  `json:"allowed_users,omitempty"`
	AddedBy      string    `json:"added_by,omitempty"`
	Status       string    `json:"status"` // ok, missing
	FileCount    int       `json:"file_count"`
	TotalBytes   int64     `json:"total_bytes"`
	LastSyncAt   time.Time `json:"last_sync_at"`
	CreatedAt    time.Time `json:"created_at"`
	LastError    string    `json:"last_error,omitempty"`
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
	case ".git", "$recycle.bin", "system volume information":
		return true
	default:
		return false
	}
}

func collectLocalFiles(root string, maxFiles int) (isDir bool, items []walkedLocalFile, skipped int, err error) {
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
		if d.IsDir() {
			if p != root && shouldSkipZeroCopyDir(d.Name()) {
				return filepath.SkipDir
			}
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
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			skipped++
			return nil
		}
		items = append(items, walkedLocalFile{
			fullPath: p,
			relSlash: filepath.ToSlash(rel),
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
		return nil, ImportSummary{}, errors.New("不能零拷贝导入 LFH 自身内部存储目录")
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
		return MountEntry{}, 0, errors.New("不能挂载 LFH 自身内部存储目录")
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

	s.mu.Lock()
	for _, existing := range s.mounts {
		if sameOSPath(existing.DirPath, cleanDir) {
			s.mu.Unlock()
			return MountEntry{}, 0, fmt.Errorf("目录 %s 已挂载（别名：%s）", cleanDir, existing.Alias)
		}
	}
	entry := MountEntry{
		ID:           "mnt-" + newID(),
		DirPath:      cleanDir,
		Alias:        cleanAlias,
		AllowedUsers: NormalizeAllowedUsers(allowedUsers),
		AddedBy:      strings.TrimSpace(addedBy),
		Status:       "ok",
		CreatedAt:    time.Now(),
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

// RemoveMount removes a directory mount and purges its indexed FileRecords without deleting disk files.
func (s *Store) RemoveMount(mountID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mounts[mountID]; !ok {
		return 0, errors.New("挂载目录不存在")
	}
	delete(s.mounts, mountID)
	removed := 0
	for id, rec := range s.records {
		if rec.MountID == mountID {
			delete(s.records, id)
			removed++
		}
	}
	if err := s.saveMountsLocked(); err != nil {
		return removed, err
	}
	if err := s.saveLocked(); err != nil {
		return removed, err
	}
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

	_, items, _, walkErr := collectLocalFiles(mount.DirPath, maxZeroCopyWalkFiles)
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
	defer s.mu.Unlock()
	cur, exists := s.mounts[mountID]
	if !exists {
		return MountSyncResult{}, errors.New("挂载目录不存在")
	}

	existingByPath := make(map[string]string)
	var res MountSyncResult
	res.MountID = mountID
	res.FileCount = len(items)
	res.TotalBytes = totalBytes

	for id, rec := range s.records {
		if rec.MountID != mountID {
			continue
		}
		key := filepath.Clean(rec.SourcePath)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, keep := walkedByPath[key]; !keep {
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
			s.RecordWriteError()
			return res, err
		}
	}
	if metaChanged || res.Added > 0 || res.Updated > 0 || res.Removed > 0 {
		if err := s.saveMountsLocked(); err != nil {
			s.RecordWriteError()
			return res, err
		}
	}
	return res, nil
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
