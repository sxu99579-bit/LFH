package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultChunkSize int64 = 4 * 1024 * 1024
	MaxChunkSize     int64 = 32 * 1024 * 1024
)

// ChunkUploadInit holds client parameters for initializing or resuming a chunked upload session.
type ChunkUploadInit struct {
	Name         string
	FolderPath   string
	FileSize     int64
	ChunkSize    int64
	FileSHA256   string
	TargetNode   string
	Uploader     string
	AllowedUsers []string
	Dev          DeviceInfo
}

// UploadSession tracks persistent server-side chunked upload progress for resumable transfers.
type UploadSession struct {
	UploadID       string     `json:"upload_id"`
	Name           string     `json:"name"`
	FolderPath     string     `json:"folder_path,omitempty"`
	FileSize       int64      `json:"file_size"`
	ChunkSize      int64      `json:"chunk_size"`
	TotalChunks    int        `json:"total_chunks"`
	FileSHA256     string     `json:"file_sha256,omitempty"`
	TargetNode     string     `json:"target_node,omitempty"`
	Uploader       string     `json:"uploader"`
	AllowedUsers   []string   `json:"allowed_users,omitempty"`
	Dev            DeviceInfo `json:"dev"`
	Fingerprint    string     `json:"fingerprint"`
	UploadedChunks []int      `json:"uploaded_chunks"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func isValidSessionID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func computeUploadFingerprint(uploader, name, folderPath string, fileSize, chunkSize int64, fileSHA256 string) string {
	raw := uploader + "|" + name + "|" + folderPath + "|" +
		strconv.FormatInt(fileSize, 10) + "|" + strconv.FormatInt(chunkSize, 10) + "|" +
		strings.ToLower(strings.TrimSpace(fileSHA256))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func expectedChunkSize(s UploadSession, index int) int64 {
	if s.FileSize == 0 {
		return 0
	}
	if index < s.TotalChunks-1 {
		return s.ChunkSize
	}
	rem := s.FileSize - int64(index)*s.ChunkSize
	if rem < 0 {
		return 0
	}
	return rem
}

func chunkFilePath(sessionDir string, index int) string {
	return filepath.Join(sessionDir, fmt.Sprintf("chunk_%06d.part", index))
}

func scanUploadedChunks(sessionDir string, session UploadSession) []int {
	out := make([]int, 0, session.TotalChunks)
	for i := 0; i < session.TotalChunks; i++ {
		fi, err := os.Stat(chunkFilePath(sessionDir, i))
		if err != nil || fi.IsDir() {
			continue
		}
		if fi.Size() == expectedChunkSize(session, i) {
			out = append(out, i)
		}
	}
	return out
}

func (s *Store) saveUploadMeta(sessionDir string, session UploadSession) error {
	raw, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sessionDir, "meta.json"), raw, 0644)
}

func (s *Store) loadUploadMeta(sessionDir string) (UploadSession, error) {
	raw, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return UploadSession{}, err
	}
	var session UploadSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return UploadSession{}, err
	}
	session.UploadedChunks = scanUploadedChunks(sessionDir, session)
	return session, nil
}

// InitChunkUpload creates or resumes a resumable chunked upload session matching the upload fingerprint.
func (s *Store) InitChunkUpload(req ChunkUploadInit) (UploadSession, bool, error) {
	if req.FileSize < 0 {
		return UploadSession{}, false, errors.New("文件大小无效")
	}
	name := sanitizeFilename(req.Name)
	folderPath := SanitizeFolderPath(req.FolderPath)
	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if chunkSize > MaxChunkSize {
		chunkSize = MaxChunkSize
	}
	totalChunks := 1
	if req.FileSize > 0 {
		totalChunks = int((req.FileSize + chunkSize - 1) / chunkSize)
	}
	uploader := strings.TrimSpace(req.Uploader)
	if uploader == "" {
		uploader = "unknown"
	}
	cleanSHA := strings.ToLower(strings.TrimSpace(req.FileSHA256))
	fp := computeUploadFingerprint(uploader, name, folderPath, req.FileSize, chunkSize, cleanSHA)

	if err := os.MkdirAll(s.uploadsTmpDir, 0755); err != nil {
		return UploadSession{}, false, err
	}

	// Resume existing matching upload session for the same user and fingerprint.
	if entries, err := os.ReadDir(s.uploadsTmpDir); err == nil {
		for _, ent := range entries {
			if !ent.IsDir() || !isValidSessionID(ent.Name()) {
				continue
			}
			sessionDir := filepath.Join(s.uploadsTmpDir, ent.Name())
			existing, loadErr := s.loadUploadMeta(sessionDir)
			if loadErr != nil {
				continue
			}
			if existing.Fingerprint == fp && existing.Uploader == uploader &&
				existing.FileSize == req.FileSize && existing.ChunkSize == chunkSize {
				existing.TargetNode = strings.TrimSpace(req.TargetNode)
				existing.AllowedUsers = NormalizeAllowedUsers(req.AllowedUsers)
				existing.UpdatedAt = time.Now()
				_ = s.saveUploadMeta(sessionDir, existing)
				return existing, true, nil
			}
		}
	}

	now := time.Now()
	uploadID := "up-" + newID()
	session := UploadSession{
		UploadID:       uploadID,
		Name:           name,
		FolderPath:     folderPath,
		FileSize:       req.FileSize,
		ChunkSize:      chunkSize,
		TotalChunks:    totalChunks,
		FileSHA256:     cleanSHA,
		TargetNode:     strings.TrimSpace(req.TargetNode),
		Uploader:       uploader,
		AllowedUsers:   NormalizeAllowedUsers(req.AllowedUsers),
		Dev:            req.Dev,
		Fingerprint:    fp,
		UploadedChunks: []int{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	sessionDir := filepath.Join(s.uploadsTmpDir, uploadID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return UploadSession{}, false, err
	}
	if err := s.saveUploadMeta(sessionDir, session); err != nil {
		_ = os.RemoveAll(sessionDir)
		return UploadSession{}, false, err
	}
	return session, false, nil
}

// GetUploadSession returns current session metadata and present chunk indices.
func (s *Store) GetUploadSession(uploadID string) (UploadSession, error) {
	if !isValidSessionID(uploadID) {
		return UploadSession{}, errors.New("无效的上传会话编号")
	}
	sessionDir := filepath.Join(s.uploadsTmpDir, uploadID)
	session, err := s.loadUploadMeta(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return UploadSession{}, errors.New("上传会话不存在或已过期")
		}
		return UploadSession{}, err
	}
	return session, nil
}

// SaveUploadChunk persists one chunk atomically and verifies optional expected SHA-256 digest.
func (s *Store) SaveUploadChunk(uploadID string, index int, expectedSHA256 string, r io.Reader) (int64, error) {
	session, err := s.GetUploadSession(uploadID)
	if err != nil {
		return 0, err
	}
	if index < 0 || index >= session.TotalChunks {
		return 0, fmt.Errorf("分片索引 %d 超出范围 (0-%d)", index, session.TotalChunks-1)
	}
	wantSize := expectedChunkSize(session, index)
	sessionDir := filepath.Join(s.uploadsTmpDir, uploadID)
	tmpFile, err := os.CreateTemp(sessionDir, fmt.Sprintf(".chunk_%06d_*.tmp", index))
	if err != nil {
		s.RecordWriteError()
		return 0, err
	}
	tmpPath := tmpFile.Name()

	h := sha256.New()
	limited := io.LimitReader(r, session.ChunkSize+1)
	n, copyErr := io.Copy(tmpFile, io.TeeReader(limited, h))
	closeErr := tmpFile.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		s.RecordWriteError()
		if copyErr != nil {
			return 0, copyErr
		}
		return 0, closeErr
	}
	if n != wantSize {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("分片 %d 大小不匹配：收到 %d 字节，期望 %d 字节", index, n, wantSize)
	}
	if cleanWant := strings.TrimSpace(expectedSHA256); cleanWant != "" {
		actual := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(actual, cleanWant) {
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("分片 %d 校验失败（SHA-256 不匹配）", index)
		}
	}
	dstPath := chunkFilePath(sessionDir, index)
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		s.RecordWriteError()
		return 0, err
	}
	session.UpdatedAt = time.Now()
	_ = s.saveUploadMeta(sessionDir, session)
	return n, nil
}

// AssembleChunkUpload concatenates all chunks in order, validates total size and optional SHA-256,
// and returns the temporary assembled file path inside the session directory.
func (s *Store) AssembleChunkUpload(uploadID string) (UploadSession, string, string, error) {
	session, err := s.GetUploadSession(uploadID)
	if err != nil {
		return UploadSession{}, "", "", err
	}
	if len(session.UploadedChunks) != session.TotalChunks {
		return session, "", "", fmt.Errorf("分片尚未全部上传完成 (%d/%d)", len(session.UploadedChunks), session.TotalChunks)
	}

	sessionDir := filepath.Join(s.uploadsTmpDir, uploadID)
	mergedPath := filepath.Join(sessionDir, "merged.tmp")
	outFile, err := os.Create(mergedPath)
	if err != nil {
		s.RecordWriteError()
		return session, "", "", err
	}

	h := sha256.New()
	var totalWritten int64
	buf := make([]byte, 64*1024)
	for i := 0; i < session.TotalChunks; i++ {
		cf, openErr := os.Open(chunkFilePath(sessionDir, i))
		if openErr != nil {
			_ = outFile.Close()
			_ = os.Remove(mergedPath)
			return session, "", "", fmt.Errorf("读取分片 %d 失败: %w", i, openErr)
		}
		n, copyErr := io.CopyBuffer(outFile, io.TeeReader(cf, h), buf)
		_ = cf.Close()
		if copyErr != nil {
			_ = outFile.Close()
			_ = os.Remove(mergedPath)
			s.RecordWriteError()
			return session, "", "", copyErr
		}
		totalWritten += n
	}
	if closeErr := outFile.Close(); closeErr != nil {
		_ = os.Remove(mergedPath)
		return session, "", "", closeErr
	}
	if totalWritten != session.FileSize {
		_ = os.Remove(mergedPath)
		return session, "", "", fmt.Errorf("合并后文件大小不匹配：%d != %d", totalWritten, session.FileSize)
	}
	actualSHA := hex.EncodeToString(h.Sum(nil))
	if session.FileSHA256 != "" && !strings.EqualFold(actualSHA, session.FileSHA256) {
		_ = os.Remove(mergedPath)
		return session, "", "", errors.New("整文件 SHA-256 校验失败，请重新上传")
	}
	return session, mergedPath, actualSHA, nil
}

// CleanupUploadSession deletes temporary chunk files and session directory.
func (s *Store) CleanupUploadSession(uploadID string) {
	if !isValidSessionID(uploadID) {
		return
	}
	_ = os.RemoveAll(filepath.Join(s.uploadsTmpDir, uploadID))
}

// GCUploadSessions removes incomplete chunked upload directories older than maxAge.
func (s *Store) GCUploadSessions(maxAge time.Duration) int {
	entries, err := os.ReadDir(s.uploadsTmpDir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	type cleanItem struct {
		dir string
		at  time.Time
	}
	var items []cleanItem
	for _, ent := range entries {
		if !ent.IsDir() || !isValidSessionID(ent.Name()) {
			continue
		}
		sessionDir := filepath.Join(s.uploadsTmpDir, ent.Name())
		var updatedAt time.Time
		if meta, loadErr := s.loadUploadMeta(sessionDir); loadErr == nil {
			updatedAt = meta.UpdatedAt
		} else if info, infoErr := ent.Info(); infoErr == nil {
			updatedAt = info.ModTime()
		}
		if updatedAt.IsZero() || updatedAt.Before(cutoff) {
			items = append(items, cleanItem{dir: sessionDir, at: updatedAt})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for _, it := range items {
		if _ = os.RemoveAll(it.dir); true {
			removed++
		}
	}
	return removed
}
