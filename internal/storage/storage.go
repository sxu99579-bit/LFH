package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type DeviceInfo struct {
	FP     string `json:"device_fp"`
	Name   string `json:"device_name"`
	IP     string `json:"device_ip"`
	Detail string `json:"device_detail,omitempty"`
}

const (
	MethodInitialUpload   = "initial_upload"
	MethodUploadOverwrite = "upload_overwrite"
	MethodOnlineEdit      = "online_edit"
	MethodVersionUpload   = "version_upload"
	MethodVersionRestore  = "version_restore"
	MethodLocalImport     = "local_import"
	MethodMountSync       = "mount_sync"

	StorageModeCopy     = "copy"
	StorageModeHardlink = "hardlink"
	StorageModeRef      = "ref"
	StorageModeMount    = "mount"
)

func DefaultUpdateMethod(method string, ver int) string {
	m := strings.TrimSpace(method)
	if m != "" {
		return m
	}
	if ver <= 1 {
		return MethodInitialUpload
	}
	return MethodUploadOverwrite
}

type FileVersion struct {
	Version      int       `json:"version"`
	StoredName   string    `json:"stored_name"`
	Size         int64     `json:"size"`
	Uploader     string    `json:"uploader"`
	UploadedAt   time.Time `json:"uploaded_at"`
	DeviceFP     string    `json:"device_fp,omitempty"`
	DeviceName   string    `json:"device_name,omitempty"`
	DeviceIP     string    `json:"device_ip,omitempty"`
	DeviceDetail string    `json:"device_detail,omitempty"`
	StorageDir   string    `json:"storage_dir,omitempty"`
	Note         string    `json:"note,omitempty"`
	UpdateMethod string    `json:"update_method,omitempty"`
}

type FileRecord struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	FolderPath   string        `json:"folder_path,omitempty"`
	StoredName   string        `json:"stored_name"`
	Size         int64         `json:"size"`
	Ext          string        `json:"ext"`
	Category     string        `json:"category"` // video, audio, document, markdown, image, archive, other
	Uploader     string        `json:"uploader"`
	UploadedAt   time.Time     `json:"uploaded_at"`
	MonthKey     string        `json:"month_key"` // YYYY-MM
	DateKey      string        `json:"date_key"`  // YYYY-MM-DD
	DeviceFP     string        `json:"device_fp,omitempty"`
	DeviceName   string        `json:"device_name,omitempty"`
	DeviceIP     string        `json:"device_ip,omitempty"`
	DeviceDetail string        `json:"device_detail,omitempty"`
	AllowedUsers []string      `json:"allowed_users,omitempty"`
	StorageDir    string        `json:"storage_dir,omitempty"`
	StorageMode   string        `json:"storage_mode,omitempty"` // copy, hardlink, ref, mount
	SourcePath    string        `json:"source_path,omitempty"`
	SourceMissing bool          `json:"source_missing,omitempty"`
	MountID       string        `json:"mount_id,omitempty"`
	Version       int           `json:"version"`
	VersionCount int           `json:"version_count"`
	VersionNote  string        `json:"version_note,omitempty"`
	UpdateMethod string        `json:"update_method,omitempty"`
	Versions     []FileVersion `json:"versions,omitempty"`
	NodeID       string        `json:"node_id,omitempty"`
	NodeName     string        `json:"node_name,omitempty"`
	NodeOS       string        `json:"node_os,omitempty"`
	NodeOnline   bool          `json:"node_online"`
}

type SummaryBucket struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

type SummaryResponse struct {
	TotalCount int             `json:"total_count"`
	TotalSize  int64           `json:"total_size"`
	ByTime     []SummaryBucket `json:"by_time"`
	ByCategory []SummaryBucket `json:"by_category"`
	ByDevice   []SummaryBucket `json:"by_device"`
	ByNode     []SummaryBucket `json:"by_node,omitempty"`
}

type rateBucket struct {
	sec   int64
	up    int64
	down  int64
}

type Store struct {
	mu               sync.RWMutex
	defaultUploadDir string
	uploadDir        string
	indexPath        string
	mountsPath       string
	records          map[string]FileRecord
	mounts           map[string]MountEntry

	readErrors         atomic.Int64
	writeErrors        atomic.Int64
	totalUploadBytes   atomic.Int64
	totalDownloadBytes atomic.Int64

	rateMu      sync.Mutex
	rateBuckets [8]rateBucket
	lastUpBps   int64
	lastUpAt    time.Time
	lastDownBps int64
	lastDownAt  time.Time
}

var categoryLabels = map[string]string{
	"video":    "视频",
	"audio":    "音乐 / 音频",
	"document": "文档 (Office / PDF)",
	"markdown": "Markdown / 文本",
	"image":    "图片",
	"archive":  "压缩包",
	"other":    "其他",
}

func ClassifyExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "mp4", "webm", "mkv", "mov", "avi", "m4v", "ogv":
		return "video"
	case "mp3", "wav", "flac", "aac", "ogg", "m4a", "opus", "wma":
		return "audio"
	case "pdf", "doc", "docx", "ppt", "pptx", "xls", "xlsx":
		return "document"
	case "md", "markdown", "txt", "json", "yaml", "yml", "log", "csv":
		return "markdown"
	case "jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico":
		return "image"
	case "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz":
		return "archive"
	default:
		return "other"
	}
}

func CategoryLabel(cat string) string {
	if label, ok := categoryLabels[cat]; ok {
		return label
	}
	return "其他"
}

func NewStore(dataDir string) (*Store, error) {
	uploadDir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return nil, err
	}
	s := &Store{
		defaultUploadDir: uploadDir,
		uploadDir:        uploadDir,
		indexPath:        filepath.Join(dataDir, "files.json"),
		mountsPath:       filepath.Join(dataDir, "mounts.json"),
		records:          make(map[string]FileRecord),
		mounts:           make(map[string]MountEntry),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) recordDirLocked(rec FileRecord) string {
	if d := strings.TrimSpace(rec.StorageDir); d != "" {
		return filepath.Clean(d)
	}
	return s.defaultUploadDir
}

func (s *Store) recordDiskPathLocked(rec FileRecord) string {
	if (rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount) && strings.TrimSpace(rec.SourcePath) != "" {
		return filepath.Clean(rec.SourcePath)
	}
	return filepath.Join(s.recordDirLocked(rec), rec.StoredName)
}

func (s *Store) versionDirLocked(rec FileRecord, v FileVersion) string {
	if d := strings.TrimSpace(v.StorageDir); d != "" {
		return filepath.Clean(d)
	}
	return s.recordDirLocked(rec)
}

func (s *Store) versionDiskPathLocked(rec FileRecord, v FileVersion) string {
	return filepath.Join(s.versionDirLocked(rec, v), v.StoredName)
}

func (s *Store) RecordReadError() {
	s.readErrors.Add(1)
}

func (s *Store) RecordWriteError() {
	s.writeErrors.Add(1)
}

func (s *Store) IOStats() (readErrs, writeErrs int64) {
	return s.readErrors.Load(), s.writeErrors.Load()
}

func (s *Store) AddUploadBytes(n int64) {
	if n <= 0 {
		return
	}
	s.totalUploadBytes.Add(n)
	now := time.Now()
	sec := now.Unix()
	s.rateMu.Lock()
	idx := int(sec % int64(len(s.rateBuckets)))
	if s.rateBuckets[idx].sec != sec {
		s.rateBuckets[idx] = rateBucket{sec: sec}
	}
	s.rateBuckets[idx].up += n
	s.lastUpBps = max(s.rateBuckets[idx].up, n)
	s.lastUpAt = now
	s.rateMu.Unlock()
}

func (s *Store) AddDownloadBytes(n int64) {
	if n <= 0 {
		return
	}
	s.totalDownloadBytes.Add(n)
	now := time.Now()
	sec := now.Unix()
	s.rateMu.Lock()
	idx := int(sec % int64(len(s.rateBuckets)))
	if s.rateBuckets[idx].sec != sec {
		s.rateBuckets[idx] = rateBucket{sec: sec}
	}
	s.rateBuckets[idx].down += n
	s.lastDownBps = max(s.rateBuckets[idx].down, n)
	s.lastDownAt = now
	s.rateMu.Unlock()
}

func (s *Store) TransferStats() (upBps, downBps, totalUp, totalDown int64) {
	totalUp = s.totalUploadBytes.Load()
	totalDown = s.totalDownloadBytes.Load()
	now := time.Now()
	sec := now.Unix()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	var sumUp, sumDown int64
	const windowSecs = int64(3)
	for _, b := range s.rateBuckets {
		if b.sec > 0 && sec-b.sec >= 0 && sec-b.sec < windowSecs {
			sumUp += b.up
			sumDown += b.down
		}
	}
	upBps = sumUp / windowSecs
	downBps = sumDown / windowSecs
	if upBps == 0 && !s.lastUpAt.IsZero() && now.Sub(s.lastUpAt) <= 4*time.Second {
		upBps = s.lastUpBps
	}
	if downBps == 0 && !s.lastDownAt.IsZero() && now.Sub(s.lastDownAt) <= 4*time.Second {
		downBps = s.lastDownBps
	}
	return upBps, downBps, totalUp, totalDown
}

func (s *Store) UploadDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uploadDir
}

func (s *Store) RecordDir(rec FileRecord) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordDirLocked(rec)
}

func (s *Store) load() error {
	s.loadMountsLocked()
	raw, err := os.ReadFile(s.indexPath)
	if err == nil {
		var list []FileRecord
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, rec := range list {
				_, statErr := os.Stat(s.recordDiskPathLocked(rec))
				isExternal := rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount
				if statErr != nil && !isExternal {
					continue
				}
				if isExternal {
					rec.SourceMissing = statErr != nil
				} else {
					rec.SourceMissing = false
				}
				if rec.MonthKey == "" {
					rec.MonthKey = rec.UploadedAt.Format("2006-01")
				}
				if rec.DateKey == "" {
					rec.DateKey = rec.UploadedAt.Format("2006-01-02")
				}
				rec.Category = ClassifyExt(rec.Ext)
				rec.AllowedUsers = NormalizeAllowedUsers(rec.AllowedUsers)
				if rec.Version <= 0 {
					rec.Version = 1
				}
				rec.UpdateMethod = DefaultUpdateMethod(rec.UpdateMethod, rec.Version)
				if len(rec.Versions) > 0 {
					validVers := make([]FileVersion, 0, len(rec.Versions))
					for _, v := range rec.Versions {
						if v.StoredName == "" {
							continue
						}
						if _, vErr := os.Stat(s.versionDiskPathLocked(rec, v)); vErr == nil {
							if v.Version <= 0 {
								v.Version = 1
							}
							v.UpdateMethod = DefaultUpdateMethod(v.UpdateMethod, v.Version)
							validVers = append(validVers, v)
						}
					}
					sort.Slice(validVers, func(i, j int) bool {
						return validVers[i].Version > validVers[j].Version
					})
					rec.Versions = validVers
				}
				rec.VersionCount = 1 + len(rec.Versions)
				s.records[rec.ID] = rec
			}
		}
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	list := make([]FileRecord, 0, len(s.records))
	for _, rec := range s.records {
		list = append(list, rec)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UploadedAt.After(list[j].UploadedAt)
	})
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.indexPath, raw, 0644)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "unnamed"
	}
	return name
}

func SanitizeFolderPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" {
		return ""
	}
	rawParts := strings.Split(p, "/")
	cleanParts := make([]string, 0, len(rawParts))
	for _, seg := range rawParts {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if len(seg) >= 2 && seg[1] == ':' {
			continue
		}
		cleanParts = append(cleanParts, seg)
	}
	joined := strings.Join(cleanParts, "/")
	runes := []rune(joined)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return joined
}

// NormalizeAllowedUsers trims, deduplicates, and sorts allowed usernames; empty list means public to all logged-in users.
func NormalizeAllowedUsers(users []string) []string {
	if len(users) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(users))
	out := make([]string, 0, len(users))
	for _, raw := range users {
		u := strings.TrimSpace(raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// CanUserAccess reports whether username/role may see or download rec.
func CanUserAccess(rec FileRecord, username, role string) bool {
	if len(rec.AllowedUsers) == 0 {
		return true
	}
	if role == "admin" || rec.Uploader == username {
		return true
	}
	for _, allowed := range rec.AllowedUsers {
		if allowed == username {
			return true
		}
	}
	return false
}

// FilterAccessibleRecords keeps only records visible to username/role.
func FilterAccessibleRecords(records []FileRecord, username, role string) []FileRecord {
	out := make([]FileRecord, 0, len(records))
	for _, rec := range records {
		if CanUserAccess(rec, username, role) {
			out = append(out, rec)
		}
	}
	return out
}

func (s *Store) SaveUpload(origName, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	return s.SaveUploadWithOptions(origName, "", uploader, nil, dev, r)
}

func (s *Store) SaveUploadWithFolder(origName, folderPath, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	return s.SaveUploadWithOptions(origName, folderPath, uploader, nil, dev, r)
}

func (s *Store) SaveUploadWithOptions(origName, folderPath, uploader string, allowedUsers []string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	return s.SaveUploadWithVersionNote(origName, folderPath, "", uploader, allowedUsers, dev, r)
}

func (s *Store) SaveUploadWithVersionNote(origName, folderPath, versionNote, uploader string, allowedUsers []string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	origName = sanitizeFilename(origName)
	folderPath = SanitizeFolderPath(folderPath)
	ext := strings.ToLower(filepath.Ext(origName))
	blobID := newID()
	storedName := blobID + ext
	activeDir := s.UploadDir()
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	dstPath := filepath.Join(activeDir, storedName)

	f, err := os.Create(dstPath)
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		s.RecordWriteError()
		_ = os.Remove(dstPath)
		return FileRecord{}, copyErr
	}
	if closeErr != nil {
		s.RecordWriteError()
		_ = os.Remove(dstPath)
		return FileRecord{}, closeErr
	}
	s.AddUploadBytes(n)

	now := time.Now()
	cleanNote := strings.TrimSpace(versionNote)
	normAllowed := NormalizeAllowedUsers(allowedUsers)

	s.mu.Lock()
	var existingID string
	for id, cur := range s.records {
		if cur.StorageMode == StorageModeMount {
			continue
		}
		if strings.EqualFold(cur.Name, origName) && cur.FolderPath == folderPath {
			existingID = id
			break
		}
	}

	var rec FileRecord
	if existingID != "" {
		cur := s.records[existingID]
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
		cur.VersionNote = cleanNote
		cur.UpdateMethod = MethodUploadOverwrite
		cur.StoredName = storedName
		cur.StorageMode = StorageModeCopy
		cur.SourcePath = ""
		cur.SourceMissing = false
		cur.MountID = ""
		cur.Size = n
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
		cur.StorageDir = activeDir
		cur.NodeOnline = true
		rec = cur
		s.records[existingID] = rec
	} else {
		rec = FileRecord{
			ID:           blobID,
			Name:         origName,
			FolderPath:   folderPath,
			StoredName:   storedName,
			Size:         n,
			Ext:          ext,
			Category:     ClassifyExt(ext),
			Uploader:     uploader,
			UploadedAt:   now,
			MonthKey:     now.Format("2006-01"),
			DateKey:      now.Format("2006-01-02"),
			DeviceFP:     strings.TrimSpace(dev.FP),
			DeviceName:   strings.TrimSpace(dev.Name),
			DeviceIP:     strings.TrimSpace(dev.IP),
			DeviceDetail: strings.TrimSpace(dev.Detail),
			AllowedUsers: normAllowed,
			StorageDir:   activeDir,
			Version:      1,
			VersionCount: 1,
			VersionNote:  cleanNote,
			UpdateMethod: MethodInitialUpload,
			NodeOnline:   true,
		}
		s.records[blobID] = rec
	}
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	return rec, nil
}

// SaveNewVersion appends a new version to an existing file record by ID.
func (s *Store) SaveNewVersion(id, origName, note, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	return s.SaveNewVersionWithMethod(id, origName, note, MethodVersionUpload, uploader, dev, r)
}

// SaveEditedText saves online-edited text content as a new version (MethodOnlineEdit) on an existing file record.
func (s *Store) SaveEditedText(id, content, note, uploader string, dev DeviceInfo) (FileRecord, error) {
	return s.SaveNewVersionWithMethod(id, "", note, MethodOnlineEdit, uploader, dev, strings.NewReader(content))
}

// SaveNewVersionWithMethod appends a new version with an explicit UpdateMethod to an existing file record by ID.
func (s *Store) SaveNewVersionWithMethod(id, origName, note, method, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	s.mu.RLock()
	cur, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}
	ext := cur.Ext
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(sanitizeFilename(origName)))
	}
	blobID := newID()
	storedName := blobID + ext
	activeDir := s.UploadDir()
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	dstPath := filepath.Join(activeDir, storedName)
	f, err := os.Create(dstPath)
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		s.RecordWriteError()
		_ = os.Remove(dstPath)
		return FileRecord{}, copyErr
	}
	if closeErr != nil {
		s.RecordWriteError()
		_ = os.Remove(dstPath)
		return FileRecord{}, closeErr
	}
	s.AddUploadBytes(n)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, exists := s.records[id]
	if !exists {
		_ = os.Remove(dstPath)
		return FileRecord{}, errors.New("文件不存在")
	}
	if latest.StorageMode == StorageModeMount {
		_ = os.Remove(dstPath)
		return FileRecord{}, errors.New("本机挂载目录文件由源文件夹自动同步，请在原目录修改或下载后另存")
	}
	curVer := latest.Version
	if curVer <= 0 {
		curVer = 1
	}
	maxVer := curVer
	for _, v := range latest.Versions {
		if v.Version > maxVer {
			maxVer = v.Version
		}
	}
	if latest.StoredName != "" {
		prevSnap := FileVersion{
			Version:      curVer,
			StoredName:   latest.StoredName,
			Size:         latest.Size,
			Uploader:     latest.Uploader,
			UploadedAt:   latest.UploadedAt,
			DeviceFP:     latest.DeviceFP,
			DeviceName:   latest.DeviceName,
			DeviceIP:     latest.DeviceIP,
			DeviceDetail: latest.DeviceDetail,
			StorageDir:   latest.StorageDir,
			Note:         latest.VersionNote,
			UpdateMethod: DefaultUpdateMethod(latest.UpdateMethod, curVer),
		}
		latest.Versions = append([]FileVersion{prevSnap}, latest.Versions...)
		sort.Slice(latest.Versions, func(i, j int) bool {
			return latest.Versions[i].Version > latest.Versions[j].Version
		})
	}
	latest.Version = maxVer + 1
	latest.VersionCount = 1 + len(latest.Versions)
	latest.VersionNote = strings.TrimSpace(note)
	if strings.TrimSpace(method) == "" {
		method = MethodVersionUpload
	}
	latest.UpdateMethod = strings.TrimSpace(method)
	latest.StoredName = storedName
	latest.StorageMode = StorageModeCopy
	latest.SourcePath = ""
	latest.SourceMissing = false
	latest.MountID = ""
	latest.Size = n
	latest.Uploader = uploader
	latest.UploadedAt = now
	latest.MonthKey = now.Format("2006-01")
	latest.DateKey = now.Format("2006-01-02")
	latest.DeviceFP = strings.TrimSpace(dev.FP)
	latest.DeviceName = strings.TrimSpace(dev.Name)
	latest.DeviceIP = strings.TrimSpace(dev.IP)
	latest.DeviceDetail = strings.TrimSpace(dev.Detail)
	latest.StorageDir = activeDir
	latest.NodeOnline = true
	s.records[id] = latest
	if err := s.saveLocked(); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	return latest, nil
}

// GetVersion locates either the current active version or a historical version of a file record.
func (s *Store) GetVersion(id string, version int) (FileRecord, FileVersion, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, FileVersion{}, "", false
	}
	curVer := rec.Version
	if curVer <= 0 {
		curVer = 1
	}
	if version <= 0 || version == curVer {
		fv := FileVersion{
			Version:      curVer,
			StoredName:   rec.StoredName,
			Size:         rec.Size,
			Uploader:     rec.Uploader,
			UploadedAt:   rec.UploadedAt,
			DeviceFP:     rec.DeviceFP,
			DeviceName:   rec.DeviceName,
			DeviceIP:     rec.DeviceIP,
			DeviceDetail: rec.DeviceDetail,
			StorageDir:   rec.StorageDir,
			Note:         rec.VersionNote,
			UpdateMethod: DefaultUpdateMethod(rec.UpdateMethod, curVer),
		}
		return rec, fv, s.recordDiskPathLocked(rec), true
	}
	for _, v := range rec.Versions {
		if v.Version == version {
			v.UpdateMethod = DefaultUpdateMethod(v.UpdateMethod, v.Version)
			return rec, v, s.versionDiskPathLocked(rec, v), true
		}
	}
	return FileRecord{}, FileVersion{}, "", false
}

// RestoreVersion promotes a historical version to become the active version while preserving the prior active version in history.
func (s *Store) RestoreVersion(id string, targetVer int, note string) (FileRecord, error) {
	return s.RestoreVersionByUser(id, targetVer, note, "", DeviceInfo{})
}

// RestoreVersionByUser promotes a historical version to become the active version and records the operator/device when provided.
func (s *Store) RestoreVersionByUser(id string, targetVer int, note, actor string, dev DeviceInfo) (FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}
	curVer := rec.Version
	if curVer <= 0 {
		curVer = 1
	}
	if targetVer == curVer {
		if strings.TrimSpace(note) != "" {
			rec.VersionNote = strings.TrimSpace(note)
			s.records[id] = rec
			_ = s.saveLocked()
		}
		return rec, nil
	}
	foundIdx := -1
	for i, v := range rec.Versions {
		if v.Version == targetVer {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		return FileRecord{}, fmt.Errorf("未找到历史版本 v%d", targetVer)
	}
	target := rec.Versions[foundIdx]
	if _, err := os.Stat(s.versionDiskPathLocked(rec, target)); err != nil {
		s.RecordReadError()
		return FileRecord{}, fmt.Errorf("历史版本 v%d 的底层文件已丢失", targetVer)
	}

	prevActive := FileVersion{
		Version:      curVer,
		StoredName:   rec.StoredName,
		Size:         rec.Size,
		Uploader:     rec.Uploader,
		UploadedAt:   rec.UploadedAt,
		DeviceFP:     rec.DeviceFP,
		DeviceName:   rec.DeviceName,
		DeviceIP:     rec.DeviceIP,
		DeviceDetail: rec.DeviceDetail,
		StorageDir:   rec.StorageDir,
		Note:         rec.VersionNote,
		UpdateMethod: DefaultUpdateMethod(rec.UpdateMethod, curVer),
	}
	remaining := make([]FileVersion, 0, len(rec.Versions))
	for i, v := range rec.Versions {
		if i != foundIdx {
			remaining = append(remaining, v)
		}
	}
	remaining = append(remaining, prevActive)
	sort.Slice(remaining, func(i, j int) bool {
		return remaining[i].Version > remaining[j].Version
	})

	rec.Version = target.Version
	rec.StoredName = target.StoredName
	rec.Size = target.Size
	rec.UpdateMethod = MethodVersionRestore
	if strings.TrimSpace(actor) != "" {
		now := time.Now()
		rec.Uploader = strings.TrimSpace(actor)
		rec.UploadedAt = now
		rec.MonthKey = now.Format("2006-01")
		rec.DateKey = now.Format("2006-01-02")
	} else {
		rec.Uploader = target.Uploader
		rec.UploadedAt = target.UploadedAt
		rec.MonthKey = target.UploadedAt.Format("2006-01")
		rec.DateKey = target.UploadedAt.Format("2006-01-02")
	}
	if strings.TrimSpace(dev.FP) != "" || strings.TrimSpace(dev.Name) != "" || strings.TrimSpace(dev.IP) != "" {
		rec.DeviceFP = strings.TrimSpace(dev.FP)
		rec.DeviceName = strings.TrimSpace(dev.Name)
		rec.DeviceIP = strings.TrimSpace(dev.IP)
		rec.DeviceDetail = strings.TrimSpace(dev.Detail)
	} else {
		rec.DeviceFP = target.DeviceFP
		rec.DeviceName = target.DeviceName
		rec.DeviceIP = target.DeviceIP
		rec.DeviceDetail = target.DeviceDetail
	}
	if target.StorageDir != "" {
		rec.StorageDir = target.StorageDir
	}
	if strings.TrimSpace(note) != "" {
		rec.VersionNote = strings.TrimSpace(note)
	} else {
		rec.VersionNote = target.Note
	}
	rec.Versions = remaining
	rec.VersionCount = 1 + len(rec.Versions)
	s.records[id] = rec
	if err := s.saveLocked(); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	return rec, nil
}

// DeleteVersion removes a specific historical version (or promotes the latest historical version if deleting the active version).
func (s *Store) DeleteVersion(id string, targetVer int) (FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}
	curVer := rec.Version
	if curVer <= 0 {
		curVer = 1
	}
	if targetVer == curVer {
		if len(rec.Versions) == 0 {
			return FileRecord{}, errors.New("该文件仅剩最后一个版本，如需删除请直接删除文件")
		}
		sort.Slice(rec.Versions, func(i, j int) bool {
			return rec.Versions[i].Version > rec.Versions[j].Version
		})
		nextActive := rec.Versions[0]
		oldPath := s.recordDiskPathLocked(rec)
		oldMode := rec.StorageMode
		rec.Versions = rec.Versions[1:]
		rec.Version = nextActive.Version
		rec.StoredName = nextActive.StoredName
		rec.StorageMode = StorageModeCopy
		rec.SourcePath = ""
		rec.SourceMissing = false
		rec.MountID = ""
		rec.Size = nextActive.Size
		rec.Uploader = nextActive.Uploader
		rec.UploadedAt = nextActive.UploadedAt
		rec.MonthKey = nextActive.UploadedAt.Format("2006-01")
		rec.DateKey = nextActive.UploadedAt.Format("2006-01-02")
		rec.DeviceFP = nextActive.DeviceFP
		rec.DeviceName = nextActive.DeviceName
		rec.DeviceIP = nextActive.DeviceIP
		rec.DeviceDetail = nextActive.DeviceDetail
		if nextActive.StorageDir != "" {
			rec.StorageDir = nextActive.StorageDir
		}
		rec.VersionNote = nextActive.Note
		rec.UpdateMethod = DefaultUpdateMethod(nextActive.UpdateMethod, nextActive.Version)
		rec.VersionCount = 1 + len(rec.Versions)
		s.records[id] = rec
		if err := s.saveLocked(); err != nil {
			s.RecordWriteError()
			return FileRecord{}, err
		}
		if oldMode != StorageModeRef && oldMode != StorageModeMount {
			_ = os.Remove(oldPath)
		}
		return rec, nil
	}

	foundIdx := -1
	for i, v := range rec.Versions {
		if v.Version == targetVer {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		return FileRecord{}, fmt.Errorf("未找到历史版本 v%d", targetVer)
	}
	vPath := s.versionDiskPathLocked(rec, rec.Versions[foundIdx])
	rec.Versions = append(rec.Versions[:foundIdx], rec.Versions[foundIdx+1:]...)
	rec.VersionCount = 1 + len(rec.Versions)
	s.records[id] = rec
	if err := s.saveLocked(); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	_ = os.Remove(vPath)
	return rec, nil
}

func (s *Store) Get(id string) (FileRecord, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, "", false
	}
	return rec, s.recordDiskPathLocked(rec), true
}

func (s *Store) SetAllowedUsers(id string, allowedUsers []string) (FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}
	rec.AllowedUsers = NormalizeAllowedUsers(allowedUsers)
	s.records[id] = rec
	if err := s.saveLocked(); err != nil {
		return FileRecord{}, err
	}
	return rec, nil
}

func (s *Store) SetUploadDir(newDir string, migrateExisting bool) (int, error) {
	trimmed := strings.TrimSpace(newDir)
	if trimmed == "" {
		return 0, errors.New("存储位置不能为空")
	}
	if runtime.GOOS == "windows" && len(trimmed) == 2 && trimmed[1] == ':' &&
		((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z')) {
		trimmed = strings.ToUpper(trimmed[:1]) + `:\LFH-Storage`
	}
	cleanDir := filepath.Clean(trimmed)
	if !filepath.IsAbs(cleanDir) {
		return 0, errors.New(`存储位置必须是完整的绝对路径（例如 D:\LFH-Storage 或 /mnt/data/lfh）`)
	}
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return 0, fmt.Errorf("创建存储目录失败: %w", err)
	}
	probe, err := os.CreateTemp(cleanDir, ".lfh-write-probe-*")
	if err != nil {
		return 0, fmt.Errorf("目标存储目录不可写: %w", err)
	}
	probePath := probe.Name()
	_, writeErr := probe.WriteString("ok")
	_ = probe.Close()
	_ = os.Remove(probePath)
	if writeErr != nil {
		return 0, fmt.Errorf("测试写入存储目录失败: %w", writeErr)
	}

	s.mu.Lock()
	s.uploadDir = cleanDir
	var candidates []FileRecord
	if migrateExisting {
		candidates = make([]FileRecord, 0, len(s.records))
		for _, rec := range s.records {
			if rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount {
				continue
			}
			if s.recordDirLocked(rec) != cleanDir {
				candidates = append(candidates, rec)
			}
		}
	}
	s.mu.Unlock()

	if !migrateExisting || len(candidates) == 0 {
		return 0, nil
	}

	moved := 0
	for _, cand := range candidates {
		s.mu.RLock()
		cur, exists := s.records[cand.ID]
		var srcPath string
		if exists {
			srcPath = s.recordDiskPathLocked(cur)
		}
		s.mu.RUnlock()
		if !exists {
			continue
		}
		dstPath := filepath.Join(cleanDir, cur.StoredName)
		if srcPath == dstPath {
			continue
		}
		if err := moveFileCrossDevice(srcPath, dstPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return moved, fmt.Errorf("迁移文件 %s 失败: %w", cur.Name, err)
		}
		s.mu.Lock()
		if latest, ok := s.records[cur.ID]; ok {
			for i, v := range latest.Versions {
				vSrc := s.versionDiskPathLocked(latest, v)
				vDst := filepath.Join(cleanDir, v.StoredName)
				if vSrc != vDst {
					if err := moveFileCrossDevice(vSrc, vDst); err == nil || os.IsNotExist(err) {
						latest.Versions[i].StorageDir = cleanDir
					}
				} else {
					latest.Versions[i].StorageDir = cleanDir
				}
			}
			latest.StorageDir = cleanDir
			s.records[cur.ID] = latest
			if saveErr := s.saveLocked(); saveErr != nil {
				s.mu.Unlock()
				return moved, saveErr
			}
			moved++
		}
		s.mu.Unlock()
	}
	return moved, nil
}

func moveFileCrossDevice(srcPath, dstPath string) error {
	if srcPath == dstPath {
		return nil
	}
	if err := os.Rename(srcPath, dstPath); err == nil {
		return nil
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".lfh-migrate-*")
	if err != nil {
		_ = in.Close()
		return err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, in)
	_ = in.Close()
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return errors.New("文件不存在")
	}
	if rec.StorageMode == StorageModeMount {
		return errors.New("该文件来自本机挂载目录，请通过移除对应挂载目录或在原文件夹中管理")
	}
	if rec.StorageMode != StorageModeRef {
		_ = os.Remove(s.recordDiskPathLocked(rec))
	}
	for _, v := range rec.Versions {
		_ = os.Remove(s.versionDiskPathLocked(rec, v))
	}
	delete(s.records, id)
	return s.saveLocked()
}

func (s *Store) LocalStats() (int, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, rec := range s.records {
		if rec.StorageMode == "" || rec.StorageMode == StorageModeCopy {
			total += rec.Size
		}
		for _, v := range rec.Versions {
			total += v.Size
		}
	}
	return len(s.records), total
}

func (s *Store) ZeroCopyStats() (hardlinkCount, refCount, mountCount int, savedBytes int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.records {
		switch rec.StorageMode {
		case StorageModeHardlink:
			hardlinkCount++
			savedBytes += rec.Size
		case StorageModeRef:
			refCount++
			savedBytes += rec.Size
		case StorageModeMount:
			mountCount++
			savedBytes += rec.Size
		}
	}
	return hardlinkCount, refCount, mountCount, savedBytes
}

func (s *Store) AllRecords() []FileRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FileRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UploadedAt.After(out[j].UploadedAt)
	})
	return out
}

func (s *Store) List(category, monthKey, dateKey, deviceFP, query string) []FileRecord {
	return FilterRecords(s.AllRecords(), category, monthKey, dateKey, deviceFP, "", query)
}

func (s *Store) Summary(category, monthKey string) SummaryResponse {
	return SummarizeRecords(s.AllRecords(), category, monthKey, "")
}

func FilterRecords(records []FileRecord, category, monthKey, dateKey, deviceFP, nodeID, query string) []FileRecord {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]FileRecord, 0, len(records))
	for _, rec := range records {
		if category != "" && category != "all" && rec.Category != category {
			continue
		}
		if monthKey != "" && monthKey != "all" && rec.MonthKey != monthKey {
			continue
		}
		if dateKey != "" && dateKey != "all" && rec.DateKey != dateKey {
			continue
		}
		if deviceFP != "" && deviceFP != "all" && rec.DeviceFP != deviceFP {
			continue
		}
		if nodeID != "" && nodeID != "all" && rec.NodeID != nodeID {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(rec.Name), q) &&
			!strings.Contains(strings.ToLower(rec.FolderPath), q) &&
			!strings.Contains(strings.ToLower(rec.Uploader), q) &&
			!strings.Contains(strings.ToLower(rec.DeviceName), q) &&
			!strings.Contains(strings.ToLower(rec.DeviceFP), q) &&
			!strings.Contains(strings.ToLower(rec.DeviceIP), q) &&
			!strings.Contains(strings.ToLower(rec.NodeName), q) {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UploadedAt.After(out[j].UploadedAt)
	})
	return out
}

func SummarizeRecords(records []FileRecord, category, monthKey, nodeID string) SummaryResponse {
	resp := SummaryResponse{}
	timeMap := make(map[string]*SummaryBucket)
	catMap := make(map[string]*SummaryBucket)
	devMap := make(map[string]*SummaryBucket)
	nodeMap := make(map[string]*SummaryBucket)

	for _, rec := range records {
		if category != "" && category != "all" && rec.Category != category {
			continue
		}
		if monthKey != "" && monthKey != "all" && rec.MonthKey != monthKey {
			continue
		}
		if nodeID != "" && nodeID != "all" && rec.NodeID != nodeID {
			continue
		}
		resp.TotalCount++
		resp.TotalSize += rec.Size

		tb, ok := timeMap[rec.MonthKey]
		if !ok {
			tb = &SummaryBucket{Key: rec.MonthKey, Label: rec.MonthKey}
			timeMap[rec.MonthKey] = tb
		}
		tb.Count++
		tb.Size += rec.Size

		cb, ok := catMap[rec.Category]
		if !ok {
			cb = &SummaryBucket{Key: rec.Category, Label: CategoryLabel(rec.Category)}
			catMap[rec.Category] = cb
		}
		cb.Count++
		cb.Size += rec.Size

		if rec.DeviceFP != "" {
			label := rec.DeviceName
			if label == "" {
				label = rec.DeviceFP
			}
			db, ok := devMap[rec.DeviceFP]
			if !ok {
				db = &SummaryBucket{Key: rec.DeviceFP, Label: label}
				devMap[rec.DeviceFP] = db
			}
			db.Count++
			db.Size += rec.Size
		}

		if rec.NodeID != "" {
			label := rec.NodeName
			if label == "" {
				label = rec.NodeID
			}
			nb, ok := nodeMap[rec.NodeID]
			if !ok {
				nb = &SummaryBucket{Key: rec.NodeID, Label: label}
				nodeMap[rec.NodeID] = nb
			}
			nb.Count++
			nb.Size += rec.Size
		}
	}

	for _, b := range timeMap {
		resp.ByTime = append(resp.ByTime, *b)
	}
	sort.Slice(resp.ByTime, func(i, j int) bool {
		return resp.ByTime[i].Key > resp.ByTime[j].Key
	})

	catOrder := []string{"video", "audio", "document", "markdown", "image", "archive", "other"}
	for _, key := range catOrder {
		if b, ok := catMap[key]; ok {
			resp.ByCategory = append(resp.ByCategory, *b)
		}
	}

	for _, b := range devMap {
		resp.ByDevice = append(resp.ByDevice, *b)
	}
	sort.Slice(resp.ByDevice, func(i, j int) bool {
		if resp.ByDevice[i].Count != resp.ByDevice[j].Count {
			return resp.ByDevice[i].Count > resp.ByDevice[j].Count
		}
		return resp.ByDevice[i].Key < resp.ByDevice[j].Key
	})

	for _, b := range nodeMap {
		resp.ByNode = append(resp.ByNode, *b)
	}
	sort.Slice(resp.ByNode, func(i, j int) bool {
		if resp.ByNode[i].Count != resp.ByNode[j].Count {
			return resp.ByNode[i].Count > resp.ByNode[j].Count
		}
		return resp.ByNode[i].Key < resp.ByNode[j].Key
	})

	return resp
}
