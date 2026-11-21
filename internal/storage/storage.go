package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type DeviceInfo struct {
	FP     string `json:"device_fp"`
	Name   string `json:"device_name"`
	IP     string `json:"device_ip"`
	Detail string `json:"device_detail,omitempty"`
}

type FileRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	FolderPath   string    `json:"folder_path,omitempty"`
	StoredName   string    `json:"stored_name"`
	Size         int64     `json:"size"`
	Ext          string    `json:"ext"`
	Category     string    `json:"category"` // video, audio, document, markdown, image, archive, other
	Uploader     string    `json:"uploader"`
	UploadedAt   time.Time `json:"uploaded_at"`
	MonthKey     string    `json:"month_key"` // YYYY-MM
	DateKey      string    `json:"date_key"`  // YYYY-MM-DD
	DeviceFP     string    `json:"device_fp,omitempty"`
	DeviceName   string    `json:"device_name,omitempty"`
	DeviceIP     string    `json:"device_ip,omitempty"`
	DeviceDetail string    `json:"device_detail,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	NodeName     string    `json:"node_name,omitempty"`
	NodeOS       string    `json:"node_os,omitempty"`
	NodeOnline   bool      `json:"node_online"`
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

type Store struct {
	mu        sync.RWMutex
	uploadDir string
	indexPath string
	records   map[string]FileRecord
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
		uploadDir: uploadDir,
		indexPath: filepath.Join(dataDir, "files.json"),
		records:   make(map[string]FileRecord),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.indexPath)
	if err == nil {
		var list []FileRecord
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, rec := range list {
				if _, statErr := os.Stat(filepath.Join(s.uploadDir, rec.StoredName)); statErr == nil {
					if rec.MonthKey == "" {
						rec.MonthKey = rec.UploadedAt.Format("2006-01")
					}
					if rec.DateKey == "" {
						rec.DateKey = rec.UploadedAt.Format("2006-01-02")
					}
					rec.Category = ClassifyExt(rec.Ext)
					s.records[rec.ID] = rec
				}
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

func (s *Store) SaveUpload(origName, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	return s.SaveUploadWithFolder(origName, "", uploader, dev, r)
}

func (s *Store) SaveUploadWithFolder(origName, folderPath, uploader string, dev DeviceInfo, r io.Reader) (FileRecord, error) {
	origName = sanitizeFilename(origName)
	folderPath = SanitizeFolderPath(folderPath)
	ext := strings.ToLower(filepath.Ext(origName))
	id := newID()
	storedName := id + ext
	dstPath := filepath.Join(s.uploadDir, storedName)

	f, err := os.Create(dstPath)
	if err != nil {
		return FileRecord{}, err
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dstPath)
		return FileRecord{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dstPath)
		return FileRecord{}, closeErr
	}

	now := time.Now()
	rec := FileRecord{
		ID:           id,
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
		NodeOnline:   true,
	}

	s.mu.Lock()
	s.records[id] = rec
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return FileRecord{}, err
	}
	return rec, nil
}

func (s *Store) Get(id string) (FileRecord, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return FileRecord{}, "", false
	}
	return rec, filepath.Join(s.uploadDir, rec.StoredName), true
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return errors.New("文件不存在")
	}
	_ = os.Remove(filepath.Join(s.uploadDir, rec.StoredName))
	delete(s.records, id)
	return s.saveLocked()
}

func (s *Store) LocalStats() (int, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, rec := range s.records {
		total += rec.Size
	}
	return len(s.records), total
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
