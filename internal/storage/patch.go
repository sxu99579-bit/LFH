package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	MaxPatchFileBytes  int64 = 10 << 20 // 10MB max text patch target
	MaxBatchOps              = 50
	MaxBatchTotalBytes int64 = 20 << 20 // 20MB max aggregate content per batch
)

// PatchHunk describes a single exact-text search-and-replace operation.
type PatchHunk struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// PatchRequest defines either search_replace hunks or a unified_diff payload.
type PatchRequest struct {
	Mode            string      `json:"mode,omitempty"` // "search_replace" (default) or "unified_diff"
	Hunks           []PatchHunk `json:"hunks,omitempty"`
	UnifiedDiff     string      `json:"unified_diff,omitempty"`
	ExpectedVersion *int        `json:"expected_version,omitempty"`
	Note            string      `json:"note,omitempty"`
}

// PatchError describes why a text patch failed (HTTP 422 Unprocessable Entity).
type PatchError struct {
	FileID     string `json:"file_id,omitempty"`
	Path       string `json:"path,omitempty"`
	Mode       string `json:"mode,omitempty"`
	HunkIndex  int    `json:"hunk_index"`
	Reason     string `json:"reason"` // "not_found", "ambiguous_match", "invalid_patch", "context_mismatch", "binary_file", "too_large"
	MatchCount int    `json:"match_count,omitempty"`
	Message    string `json:"message"`
}

func (e *PatchError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("补丁应用失败 (%s, hunk #%d)", e.Reason, e.HunkIndex)
}

// BatchOp is a single atomic file mutation within BatchCommitRequest.
type BatchOp struct {
	Action          string      `json:"action"` // "write", "patch", "delete"
	Path            string      `json:"path"`
	Content         string      `json:"content,omitempty"`
	Mode            string      `json:"mode,omitempty"`
	Hunks           []PatchHunk `json:"hunks,omitempty"`
	UnifiedDiff     string      `json:"unified_diff,omitempty"`
	ExpectedVersion *int        `json:"expected_version,omitempty"`
	AllowedUsers    []string    `json:"allowed_users,omitempty"`
}

// BatchCommitRequest commits multiple file changes under sorted striped locks and dry-run validation.
type BatchCommitRequest struct {
	Note       string    `json:"note,omitempty"`
	Operations []BatchOp `json:"operations"`
}

// BatchOpResult reports the per-operation outcome of a committed batch.
type BatchOpResult struct {
	Index   int         `json:"index"`
	Action  string      `json:"action"`
	Path    string      `json:"path"`
	FileID  string      `json:"file_id"`
	Version int         `json:"version,omitempty"`
	Deleted bool        `json:"deleted,omitempty"`
	Record  *FileRecord `json:"record,omitempty"`
}

// BatchCommitResult returns the committed batch identifier and per-file outcomes.
type BatchCommitResult struct {
	BatchID string          `json:"batch_id"`
	Note    string          `json:"note,omitempty"`
	Results []BatchOpResult `json:"results"`
}

// BatchPolicyHooks lets upper layers (HTTP/MCP) inject zero-trust access checks inside the batch lock.
type BatchPolicyHooks struct {
	CheckAccess         func(action, folderPath, fileName string, existing *FileRecord) error
	ResolveAllowedUsers func(requested []string) ([]string, error)
}

// SplitLogicalPath splits a workspace logical path ("shopapp/backend/main.go" or "notes.txt")
// into normalized (folderPath, fileName) while rejecting traversal or control characters.
func SplitLogicalPath(raw string) (string, string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	s = strings.Trim(s, "/")
	if s == "" {
		return "", "", errors.New("文件路径不能为空")
	}
	for _, r := range s {
		if r < 32 {
			return "", "", errors.New("文件路径包含非法控制字符")
		}
	}
	parts := strings.Split(s, "/")
	for i, seg := range parts {
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" || trimmed == "." || trimmed == ".." || trimmed == "*" {
			return "", "", errors.New("文件路径包含非法目录段")
		}
		parts[i] = trimmed
	}
	rawBase := parts[len(parts)-1]
	fileName := sanitizeFilename(rawBase)
	if fileName == "" || fileName == "." || fileName == ".." {
		return "", "", errors.New("无效的文件名")
	}
	if len(parts) == 1 {
		return "", fileName, nil
	}
	folderPath := SanitizeFolderPath(strings.Join(parts[:len(parts)-1], "/"))
	if folderPath == "" {
		return "", "", errors.New("文件路径目录段无效")
	}
	return folderPath, fileName, nil
}

// LogicalPathOf joins folderPath and filename into a clean forward-slash logical path.
func LogicalPathOf(folderPath, name string) string {
	return logicalFilePath(folderPath, name)
}

// IsLikelyBinaryBytes returns true when data appears to be binary (contains NUL bytes in prefix).
func IsLikelyBinaryBytes(data []byte) bool {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	return bytes.IndexByte(sample, 0) >= 0
}

// IsBlockedMountRelPath reports whether relSlash hits the hardcoded server secret/control path blacklist.
func IsBlockedMountRelPath(relSlash string) bool {
	return isHardcodedBlockedMountPath(relSlash)
}

// ApplyPatchContent applies either search_replace hunks or a unified_diff against original text
// while detecting and preserving the dominant line ending style (CRLF vs LF).
func ApplyPatchContent(original string, req PatchRequest) (string, error) {
	crlfCount := strings.Count(original, "\r\n")
	lfOnlyCount := strings.Count(original, "\n") - crlfCount
	useCRLF := crlfCount > 0 && crlfCount >= lfOnlyCount

	norm := strings.ReplaceAll(original, "\r\n", "\n")
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		if strings.TrimSpace(req.UnifiedDiff) != "" && len(req.Hunks) == 0 {
			mode = "unified_diff"
		} else {
			mode = "search_replace"
		}
	}

	var outNorm string
	var err error
	switch mode {
	case "search_replace":
		outNorm, err = applySearchReplace(norm, req.Hunks)
	case "unified_diff":
		outNorm, err = applyUnifiedDiff(norm, req.UnifiedDiff)
	default:
		return "", &PatchError{
			Mode:    mode,
			Reason:  "invalid_patch",
			Message: "不支持的补丁模式: " + mode + "（仅支持 search_replace / unified_diff）",
		}
	}
	if err != nil {
		return "", err
	}
	if useCRLF {
		return strings.ReplaceAll(outNorm, "\n", "\r\n"), nil
	}
	return outNorm, nil
}

func applySearchReplace(norm string, hunks []PatchHunk) (string, error) {
	if len(hunks) == 0 {
		return "", &PatchError{
			Mode:    "search_replace",
			Reason:  "invalid_patch",
			Message: "search_replace 模式必须至少提供一个 hunk",
		}
	}
	cur := norm
	for i, h := range hunks {
		oldNorm := strings.ReplaceAll(h.OldText, "\r\n", "\n")
		newNorm := strings.ReplaceAll(h.NewText, "\r\n", "\n")
		if oldNorm == "" {
			return "", &PatchError{
				Mode:      "search_replace",
				HunkIndex: i,
				Reason:    "invalid_patch",
				Message:   fmt.Sprintf("hunk #%d 的 old_text 不能为空", i),
			}
		}
		cnt := strings.Count(cur, oldNorm)
		if cnt == 0 {
			return "", &PatchError{
				Mode:       "search_replace",
				HunkIndex:  i,
				Reason:     "not_found",
				MatchCount: 0,
				Message:    fmt.Sprintf("hunk #%d 未匹配到目标文本片段", i),
			}
		}
		if !h.ReplaceAll && cnt > 1 {
			return "", &PatchError{
				Mode:       "search_replace",
				HunkIndex:  i,
				Reason:     "ambiguous_match",
				MatchCount: cnt,
				Message:    fmt.Sprintf("hunk #%d 匹配到 %d 处位置，存在歧义；请补充上下文或设置 replace_all=true", i, cnt),
			}
		}
		cur = strings.ReplaceAll(cur, oldNorm, newNorm)
	}
	return cur, nil
}

type parsedUnifiedHunk struct {
	oldStart       int
	oldCount       int
	newStart       int
	newCount       int
	expectedOld    []string
	replacementNew []string
	noNewlineOld   bool
	noNewlineNew   bool
	sawNew         bool
}

func parseHunkHeader(line string) (oldStart, oldCount, newStart, newCount int, ok bool) {
	if !strings.HasPrefix(line, "@@ ") {
		return 0, 0, 0, 0, false
	}
	rest := line[3:]
	endIdx := strings.Index(rest, " @@")
	if endIdx < 0 {
		return 0, 0, 0, 0, false
	}
	rangeSpec := strings.TrimSpace(rest[:endIdx])
	fields := strings.Fields(rangeSpec)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return 0, 0, 0, 0, false
	}
	var err error
	oldStart, oldCount, err = parseRangePair(fields[0][1:])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	newStart, newCount, err = parseRangePair(fields[1][1:])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return oldStart, oldCount, newStart, newCount, true
}

func parseRangePair(raw string) (int, int, error) {
	if s, c, hasComma := strings.Cut(raw, ","); hasComma {
		start, err1 := strconv.Atoi(s)
		count, err2 := strconv.Atoi(c)
		if err1 != nil || err2 != nil || start < 0 || count < 0 {
			return 0, 0, errors.New("invalid range")
		}
		return start, count, nil
	}
	start, err := strconv.Atoi(raw)
	if err != nil || start < 0 {
		return 0, 0, errors.New("invalid range start")
	}
	return start, 1, nil
}

func parseUnifiedDiffHunks(diffText string) ([]parsedUnifiedHunk, error) {
	norm := strings.ReplaceAll(diffText, "\r\n", "\n")
	if strings.TrimSpace(norm) == "" {
		return nil, &PatchError{
			Mode:    "unified_diff",
			Reason:  "invalid_patch",
			Message: "unified_diff 内容不能为空",
		}
	}
	rawLines := strings.Split(strings.TrimSuffix(norm, "\n"), "\n")
	var hunks []parsedUnifiedHunk
	var cur *parsedUnifiedHunk
	var oldSeen, newSeen int
	var lastTag byte

	flushCur := func(hi int) error {
		if cur == nil {
			return nil
		}
		if oldSeen != cur.oldCount || newSeen != cur.newCount {
			return &PatchError{
				Mode:      "unified_diff",
				HunkIndex: hi,
				Reason:    "invalid_patch",
				Message:   fmt.Sprintf("unified_diff hunk #%d 行数与头声明不符 (-%d/+%d vs -%d/+%d)", hi, oldSeen, newSeen, cur.oldCount, cur.newCount),
			}
		}
		hunks = append(hunks, *cur)
		cur = nil
		return nil
	}

	for _, line := range rawLines {
		if osStart, osCount, nsStart, nsCount, isHdr := parseHunkHeader(line); isHdr {
			if err := flushCur(len(hunks)); err != nil {
				return nil, err
			}
			cur = &parsedUnifiedHunk{
				oldStart: osStart,
				oldCount: osCount,
				newStart: nsStart,
				newCount: nsCount,
			}
			oldSeen = 0
			newSeen = 0
			lastTag = 0
			continue
		}
		if cur == nil {
			if strings.HasPrefix(line, "diff ") ||
				strings.HasPrefix(line, "index ") ||
				strings.HasPrefix(line, "--- ") ||
				strings.HasPrefix(line, "+++ ") ||
				strings.TrimSpace(line) == "" {
				continue
			}
			return nil, &PatchError{
				Mode:    "unified_diff",
				Reason:  "invalid_patch",
				Message: "unified_diff 缺少有效的 @@ hunk 头标记",
			}
		}

		if line == "" {
			// Treat blank line inside incomplete hunk as an empty context line (tolerant of stripped space).
			if oldSeen < cur.oldCount && newSeen < cur.newCount {
				cur.expectedOld = append(cur.expectedOld, "")
				cur.replacementNew = append(cur.replacementNew, "")
				oldSeen++
				newSeen++
				lastTag = ' '
				continue
			}
			continue
		}

		tag := line[0]
		body := line[1:]
		switch tag {
		case ' ':
			cur.expectedOld = append(cur.expectedOld, body)
			cur.replacementNew = append(cur.replacementNew, body)
			oldSeen++
			newSeen++
			lastTag = ' '
		case '-':
			cur.expectedOld = append(cur.expectedOld, body)
			oldSeen++
			lastTag = '-'
		case '+':
			cur.replacementNew = append(cur.replacementNew, body)
			newSeen++
			cur.sawNew = true
			lastTag = '+'
		case '\\':
			switch lastTag {
			case '-':
				cur.noNewlineOld = true
			case '+':
				cur.noNewlineNew = true
			case ' ':
				cur.noNewlineOld = true
				cur.noNewlineNew = true
			}
		default:
			if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
				if err := flushCur(len(hunks)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &PatchError{
				Mode:      "unified_diff",
				HunkIndex: len(hunks),
				Reason:    "invalid_patch",
				Message:   fmt.Sprintf("unified_diff hunk #%d 存在非法行前缀 %q", len(hunks), string(tag)),
			}
		}
	}

	if err := flushCur(len(hunks)); err != nil {
		return nil, err
	}
	if len(hunks) == 0 {
		return nil, &PatchError{
			Mode:    "unified_diff",
			Reason:  "invalid_patch",
			Message: "unified_diff 未包含任何有效 hunk",
		}
	}
	return hunks, nil
}

func splitNormalizedLines(norm string) ([]string, bool) {
	if norm == "" {
		return nil, false
	}
	trailingNL := strings.HasSuffix(norm, "\n")
	trimmed := strings.TrimSuffix(norm, "\n")
	return strings.Split(trimmed, "\n"), trailingNL
}

func joinNormalizedLines(lines []string, trailingNL bool) string {
	if len(lines) == 0 {
		return ""
	}
	joined := strings.Join(lines, "\n")
	if trailingNL {
		return joined + "\n"
	}
	return joined
}

func matchLinesAt(lines []string, idx int, expected []string) bool {
	if idx < 0 || idx+len(expected) > len(lines) {
		return false
	}
	for i := range expected {
		if lines[idx+i] != expected[i] {
			return false
		}
	}
	return true
}

func applyUnifiedDiff(norm, diffText string) (string, error) {
	hunks, err := parseUnifiedDiffHunks(diffText)
	if err != nil {
		return "", err
	}
	lines, trailingNL := splitNormalizedLines(norm)
	hadContent := len(lines) > 0
	delta := 0

	for hi, h := range hunks {
		nominal := 0
		if h.oldStart > 0 {
			nominal = h.oldStart - 1 + delta
		}
		if nominal < 0 {
			nominal = 0
		}
		if nominal > len(lines) {
			nominal = len(lines)
		}

		applyAt := -1
		if matchLinesAt(lines, nominal, h.expectedOld) {
			applyAt = nominal
		} else if len(h.expectedOld) > 0 {
			var found []int
			seenIdx := make(map[int]bool)
			for dist := 1; dist <= 50; dist++ {
				for _, cand := range []int{nominal - dist, nominal + dist} {
					if cand < 0 || cand+len(h.expectedOld) > len(lines) || seenIdx[cand] {
						continue
					}
					seenIdx[cand] = true
					if matchLinesAt(lines, cand, h.expectedOld) {
						found = append(found, cand)
					}
				}
			}
			if len(found) > 1 {
				return "", &PatchError{
					Mode:       "unified_diff",
					HunkIndex:  hi,
					Reason:     "ambiguous_match",
					MatchCount: len(found),
					Message:    fmt.Sprintf("unified_diff hunk #%d 上下文偏移匹配到 %d 处位置，存在歧义", hi, len(found)),
				}
			}
			if len(found) == 1 {
				applyAt = found[0]
			}
		}
		if applyAt < 0 {
			return "", &PatchError{
				Mode:      "unified_diff",
				HunkIndex: hi,
				Reason:    "context_mismatch",
				Message:   fmt.Sprintf("unified_diff hunk #%d 与当前文件内容上下文不匹配（行 %d）", hi, h.oldStart),
			}
		}

		touchesEOF := applyAt+len(h.expectedOld) == len(lines)
		nextLines := make([]string, 0, len(lines)-len(h.expectedOld)+len(h.replacementNew))
		nextLines = append(nextLines, lines[:applyAt]...)
		nextLines = append(nextLines, h.replacementNew...)
		nextLines = append(nextLines, lines[applyAt+len(h.expectedOld):]...)
		lines = nextLines
		delta += len(h.replacementNew) - len(h.expectedOld)

		if touchesEOF {
			if h.noNewlineNew {
				trailingNL = false
			} else if !hadContent && h.sawNew && len(lines) > 0 {
				trailingNL = true
			} else if h.noNewlineOld && h.sawNew {
				trailingNL = true
			}
		}
	}

	return joinNormalizedLines(lines, trailingNL), nil
}

// FindByLogicalPath resolves a file record by normalized (folderPath, name), preferring local mounts
// when the directory maps to an active mount alias.
func (s *Store) FindByLogicalPath(folderPath, name string) (FileRecord, bool) {
	cleanFolder := SanitizeFolderPath(folderPath)
	cleanName := sanitizeFilename(name)
	_, _, inMount := s.FindMountByFolderPath(cleanFolder)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.records {
		if inMount && rec.StorageMode != StorageModeMount {
			continue
		}
		if !inMount && rec.StorageMode == StorageModeMount {
			continue
		}
		if rec.FolderPath == cleanFolder && strings.EqualFold(rec.Name, cleanName) {
			return rec, true
		}
	}
	return FileRecord{}, false
}

// PatchFileWithConcurrency applies search_replace or unified_diff edits under striped lock,
// optimistic concurrency control, soft lease enforcement, and atomic write-through / Shadow backup.
func (s *Store) PatchFileWithConcurrency(id string, req PatchRequest, method, uploader string, dev DeviceInfo, conc WriteConcurrencyOptions) (FileRecord, error) {
	effExpected := conc.ExpectedVersion
	if effExpected == nil {
		effExpected = req.ExpectedVersion
	}
	if effExpected != nil && *effExpected < 0 {
		return FileRecord{}, errors.New("expected_version 不能为负数")
	}
	effConc := conc
	effConc.ExpectedVersion = effExpected

	s.mu.RLock()
	cur, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}

	unlock := s.lockPath(cur.FolderPath, cur.Name)
	defer unlock()

	s.mu.RLock()
	latest, ok := s.records[id]
	var mount MountEntry
	var hasMount bool
	if ok && latest.StorageMode == StorageModeMount {
		mount, hasMount = s.mounts[latest.MountID]
	}
	var diskPath string
	if ok {
		diskPath = s.recordDiskPathLocked(latest)
	}
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}

	if err := s.CheckLeaseForWrite(id, effConc.ActorKeyID, effConc.ForceLease); err != nil {
		return FileRecord{}, err
	}
	if err := checkExpectedVersion(&latest, latest.FolderPath, latest.Name, effExpected); err != nil {
		return FileRecord{}, err
	}

	pathLabel := logicalFilePath(latest.FolderPath, latest.Name)
	maxBytes := MaxPatchFileBytes
	var relSlash string

	if latest.StorageMode == StorageModeMount {
		if !hasMount || !mount.Writable {
			return FileRecord{}, errors.New("该文件属于只读挂载目录，请先在挂载配置中开启可写工作区")
		}
		if mountLimit := effectiveMaxFileBytes(mount); mountLimit > 0 && mountLimit < maxBytes {
			maxBytes = mountLimit
		}
		rel, relErr := filepath.Rel(mount.DirPath, latest.SourcePath)
		if relErr != nil {
			return FileRecord{}, relErr
		}
		cleanRel, cErr := cleanMountRelPath(filepath.ToSlash(rel))
		if cErr != nil {
			return FileRecord{}, cErr
		}
		if isHardcodedBlockedMountPath(cleanRel) {
			return FileRecord{}, errors.New("安全策略禁止读写 .git / .ssh / .env 或私钥等敏感文件")
		}
		if matchesIgnorePatterns(cleanRel, latest.Name, mount.IgnorePatterns) {
			return FileRecord{}, errors.New("目标路径命中挂载目录忽略规则，禁止写入")
		}
		absPath := filepath.Clean(filepath.Join(mount.DirPath, filepath.FromSlash(cleanRel)))
		if sErr := verifyNoSymlinkPath(mount.DirPath, absPath); sErr != nil {
			return FileRecord{}, sErr
		}
		relSlash = cleanRel
		diskPath = absPath
	}

	fi, statErr := os.Stat(diskPath)
	if statErr != nil {
		s.RecordReadError()
		if os.IsNotExist(statErr) {
			return FileRecord{}, errors.New("源文件不存在或已移除")
		}
		return FileRecord{}, statErr
	}
	if fi.Size() > maxBytes {
		return FileRecord{}, &PatchError{
			FileID:  id,
			Path:    pathLabel,
			Mode:    req.Mode,
			Reason:  "too_large",
			Message: fmt.Sprintf("文件大小 %d 字节超过文本补丁上限 (%d 字节)", fi.Size(), maxBytes),
		}
	}

	f, openErr := os.Open(diskPath)
	if openErr != nil {
		s.RecordReadError()
		return FileRecord{}, openErr
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, maxBytes+1))
	_ = f.Close()
	if readErr != nil {
		s.RecordReadError()
		return FileRecord{}, readErr
	}
	if int64(len(raw)) > maxBytes {
		return FileRecord{}, &PatchError{
			FileID:  id,
			Path:    pathLabel,
			Mode:    req.Mode,
			Reason:  "too_large",
			Message: fmt.Sprintf("文件大小超过文本补丁上限 (%d 字节)", maxBytes),
		}
	}
	if IsLikelyBinaryBytes(raw) {
		return FileRecord{}, &PatchError{
			FileID:  id,
			Path:    pathLabel,
			Mode:    req.Mode,
			Reason:  "binary_file",
			Message: "检测到二进制文件内容，请改用二进制上传接口",
		}
	}

	patched, patchErr := ApplyPatchContent(string(raw), req)
	if patchErr != nil {
		var pErr *PatchError
		if errors.As(patchErr, &pErr) {
			pErr.FileID = id
			pErr.Path = pathLabel
		}
		return FileRecord{}, patchErr
	}
	if int64(len(patched)) > maxBytes {
		return FileRecord{}, &PatchError{
			FileID:  id,
			Path:    pathLabel,
			Mode:    req.Mode,
			Reason:  "too_large",
			Message: fmt.Sprintf("补丁后文件大小超过文本补丁上限 (%d 字节)", maxBytes),
		}
	}

	effMethod := strings.TrimSpace(method)
	if effMethod == "" {
		effMethod = MethodPatch
	}

	if latest.StorageMode == StorageModeMount {
		return s.writeMountFileUnderLock(mount.ID, relSlash, req.Note, effMethod, uploader, latest.AllowedUsers, dev, strings.NewReader(patched), effConc)
	}

	blobID := newID()
	storedName := blobID + latest.Ext
	activeDir := s.UploadDir()
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	dstPath := filepath.Join(activeDir, storedName)
	n, shaHex, err := writeBlobAndHash(dstPath, strings.NewReader(patched))
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}

	if leaseErr := s.CheckLeaseForWrite(id, effConc.ActorKeyID, effConc.ForceLease); leaseErr != nil {
		_ = os.Remove(dstPath)
		return FileRecord{}, leaseErr
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	finalRec, exists := s.records[id]
	if !exists {
		_ = os.Remove(dstPath)
		return FileRecord{}, errors.New("文件不存在")
	}
	if finalRec.StorageMode == StorageModeMount {
		_ = os.Remove(dstPath)
		return FileRecord{}, errors.New("文件存储模式已变更，请重试")
	}
	if verErr := checkExpectedVersion(&finalRec, finalRec.FolderPath, finalRec.Name, effExpected); verErr != nil {
		_ = os.Remove(dstPath)
		return FileRecord{}, verErr
	}

	curVer := finalRec.Version
	if curVer <= 0 {
		curVer = 1
	}
	maxVer := curVer
	for _, v := range finalRec.Versions {
		if v.Version > maxVer {
			maxVer = v.Version
		}
	}
	if finalRec.StoredName != "" {
		prevSnap := FileVersion{
			Version:      curVer,
			StoredName:   finalRec.StoredName,
			Size:         finalRec.Size,
			Uploader:     finalRec.Uploader,
			UploadedAt:   finalRec.UploadedAt,
			DeviceFP:     finalRec.DeviceFP,
			DeviceName:   finalRec.DeviceName,
			DeviceIP:     finalRec.DeviceIP,
			DeviceDetail: finalRec.DeviceDetail,
			StorageDir:   finalRec.StorageDir,
			SHA256:       finalRec.SHA256,
			Note:         finalRec.VersionNote,
			UpdateMethod: DefaultUpdateMethod(finalRec.UpdateMethod, curVer),
		}
		finalRec.Versions = append([]FileVersion{prevSnap}, finalRec.Versions...)
		sort.Slice(finalRec.Versions, func(i, j int) bool {
			return finalRec.Versions[i].Version > finalRec.Versions[j].Version
		})
	}
	finalRec.Version = maxVer + 1
	finalRec.VersionCount = 1 + len(finalRec.Versions)
	finalRec.VersionNote = strings.TrimSpace(req.Note)
	finalRec.UpdateMethod = effMethod
	finalRec.StoredName = storedName
	finalRec.StorageMode = StorageModeCopy
	finalRec.SourcePath = ""
	finalRec.SourceMissing = false
	finalRec.MountID = ""
	finalRec.SHA256 = shaHex
	finalRec.IntegrityStatus = "ok"
	verifiedNow := now
	finalRec.LastVerifiedAt = &verifiedNow
	finalRec.Size = n
	finalRec.Uploader = uploader
	finalRec.UploadedAt = now
	finalRec.MonthKey = now.Format("2006-01")
	finalRec.DateKey = now.Format("2006-01-02")
	finalRec.DeviceFP = strings.TrimSpace(dev.FP)
	finalRec.DeviceName = strings.TrimSpace(dev.Name)
	finalRec.DeviceIP = strings.TrimSpace(dev.IP)
	finalRec.DeviceDetail = strings.TrimSpace(dev.Detail)
	finalRec.StorageDir = activeDir
	finalRec.NodeOnline = true
	s.records[id] = finalRec
	if err := s.appendWALUpsertLocked(finalRec); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	s.AddUploadBytes(n)
	return finalRec, nil
}

func stageTempFile(dstPath string, content []byte) (string, string, error) {
	dir := filepath.Dir(dstPath)
	tmp, err := os.CreateTemp(dir, ".lfh_tmp_*")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	h := sha256.New()
	_, _ = h.Write(content)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	return tmpPath, hex.EncodeToString(h.Sum(nil)), nil
}

func renameFileWithRetry(srcPath, dstPath string) error {
	var renameErr error
	for attempt := 0; attempt < 4; attempt++ {
		renameErr = os.Rename(srcPath, dstPath)
		if renameErr == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return renameErr
}

func copyFileOverwrite(srcPath, dstPath string) error {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	tmp, _, err := stageTempFile(dstPath, raw)
	if err != nil {
		return err
	}
	if err := renameFileWithRetry(tmp, dstPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

type preparedBatchOp struct {
	index        int
	action       string
	folderPath   string
	fileName     string
	pathLabel    string
	ext          string
	expectedVer  *int
	allowedUsers []string

	existing *FileRecord
	inMount  bool
	mount    MountEntry
	relSlash string
	absPath  string
	diskExisted bool

	finalBytes   []byte
	blobID       string
	storedName   string
	activeDir    string
	stagedPath   string
	shaHex       string
	shadowSnap   *FileVersion
	shadowPath   string
	deletePaths  []string
	renamed      bool
}

// BatchCommitWithConcurrency executes up to MaxBatchOps file writes, patches, or deletes using
// sorted striped locks, a full zero-side-effect dry run, staged temp files, and a single index commit.
func (s *Store) BatchCommitWithConcurrency(req BatchCommitRequest, actor string, dev DeviceInfo, conc WriteConcurrencyOptions, hooks BatchPolicyHooks) (BatchCommitResult, error) {
	if len(req.Operations) == 0 {
		return BatchCommitResult{}, errors.New("operations 不能为空")
	}
	if len(req.Operations) > MaxBatchOps {
		return BatchCommitResult{}, fmt.Errorf("单次 batch_commit 最多允许 %d 个操作", MaxBatchOps)
	}

	preps := make([]*preparedBatchOp, 0, len(req.Operations))
	lockKeys := make([]string, 0, len(req.Operations))
	seenPaths := make(map[string]bool, len(req.Operations))
	var declaredWriteBytes int64

	for i, op := range req.Operations {
		action := strings.ToLower(strings.TrimSpace(op.Action))
		switch action {
		case "write", "patch", "delete":
		default:
			return BatchCommitResult{}, fmt.Errorf("operations[%d] 不支持的操作类型: %s", i, op.Action)
		}
		if op.ExpectedVersion != nil && *op.ExpectedVersion < 0 {
			return BatchCommitResult{}, fmt.Errorf("operations[%d] expected_version 不能为负数", i)
		}
		folderPath, fileName, err := SplitLogicalPath(op.Path)
		if err != nil {
			return BatchCommitResult{}, fmt.Errorf("operations[%d] 路径无效: %w", i, err)
		}
		lockKey := pathLockKey(folderPath, fileName)
		if seenPaths[lockKey] {
			return BatchCommitResult{}, fmt.Errorf("operations[%d] 在同一批次中重复操作路径 %s", i, logicalFilePath(folderPath, fileName))
		}
		seenPaths[lockKey] = true
		lockKeys = append(lockKeys, lockKey)

		if action == "write" {
			declaredWriteBytes += int64(len(op.Content))
			if declaredWriteBytes > MaxBatchTotalBytes {
				return BatchCommitResult{}, fmt.Errorf("单次 batch_commit 总写入内容超过上限 (%d 字节)", MaxBatchTotalBytes)
			}
		}

		preps = append(preps, &preparedBatchOp{
			index:       i,
			action:      action,
			folderPath:  folderPath,
			fileName:    fileName,
			pathLabel:   logicalFilePath(folderPath, fileName),
			ext:         strings.ToLower(filepath.Ext(fileName)),
			expectedVer: op.ExpectedVersion,
		})
	}

	// Sort lock slots in strict ascending order across the batch to guarantee deadlock freedom.
	unlock := s.lockPaths(lockKeys)
	defer unlock()

	// Phase 1: Dry-run validation and in-memory patch resolution (zero side effects).
	var cumulativeBytes int64
	var existingIDs []string
	activeDir := s.UploadDir()

	for i, op := range req.Operations {
		p := preps[i]
		mount, relDir, inMount := s.FindMountByFolderPath(p.folderPath)
		p.inMount = inMount
		p.mount = mount

		s.mu.RLock()
		var found *FileRecord
		for _, cur := range s.records {
			if inMount && cur.StorageMode != StorageModeMount {
				continue
			}
			if !inMount && cur.StorageMode == StorageModeMount {
				continue
			}
			if cur.FolderPath == p.folderPath && strings.EqualFold(cur.Name, p.fileName) {
				cp := cur
				found = &cp
				break
			}
		}
		var mountDiskPath string
		if found != nil {
			mountDiskPath = s.recordDiskPathLocked(*found)
		}
		s.mu.RUnlock()
		p.existing = found

		if hooks.CheckAccess != nil {
			if err := hooks.CheckAccess(p.action, p.folderPath, p.fileName, found); err != nil {
				return BatchCommitResult{}, err
			}
		}
		if p.action == "write" {
			if hooks.ResolveAllowedUsers != nil {
				resolved, err := hooks.ResolveAllowedUsers(op.AllowedUsers)
				if err != nil {
					return BatchCommitResult{}, err
				}
				p.allowedUsers = NormalizeAllowedUsers(resolved)
			} else {
				p.allowedUsers = NormalizeAllowedUsers(op.AllowedUsers)
			}
		}

		if found != nil {
			existingIDs = append(existingIDs, found.ID)
			if err := s.CheckLeaseForWrite(found.ID, conc.ActorKeyID, conc.ForceLease); err != nil {
				return BatchCommitResult{}, err
			}
		}
		if err := checkExpectedVersion(found, p.folderPath, p.fileName, p.expectedVer); err != nil {
			return BatchCommitResult{}, err
		}

		if inMount {
			if !mount.Writable {
				return BatchCommitResult{}, fmt.Errorf("%s 属于只读挂载目录，请先在挂载配置中开启可写工作区", p.pathLabel)
			}
			rawRel := p.fileName
			if relDir != "" {
				rawRel = relDir + "/" + p.fileName
			}
			cleanRel, cErr := cleanMountRelPath(rawRel)
			if cErr != nil {
				return BatchCommitResult{}, cErr
			}
			if isHardcodedBlockedMountPath(cleanRel) {
				return BatchCommitResult{}, errors.New("安全策略禁止读写 .git / .ssh / .env 或私钥等敏感文件")
			}
			if matchesIgnorePatterns(cleanRel, p.fileName, mount.IgnorePatterns) {
				return BatchCommitResult{}, fmt.Errorf("目标路径 %s 命中挂载目录忽略规则，禁止写入", p.pathLabel)
			}
			absPath := filepath.Clean(filepath.Join(mount.DirPath, filepath.FromSlash(cleanRel)))
			if sErr := verifyNoSymlinkPath(mount.DirPath, absPath); sErr != nil {
				return BatchCommitResult{}, sErr
			}
			p.relSlash = cleanRel
			p.absPath = absPath
			_, stErr := os.Stat(absPath)
			p.diskExisted = stErr == nil
			if p.action == "write" && found == nil && p.diskExisted && p.expectedVer != nil && *p.expectedVer == 0 {
				return BatchCommitResult{}, &ConflictError{
					Path:            p.pathLabel,
					ExpectedVersion: 0,
					CurrentVersion:  1,
				}
			}
		}

		switch p.action {
		case "write":
			rawBytes := []byte(op.Content)
			if inMount {
				if limit := effectiveMaxFileBytes(mount); limit > 0 && int64(len(rawBytes)) > limit {
					return BatchCommitResult{}, fmt.Errorf("文件 %s 大小超过可写工作区单文件上限 (%d 字节)", p.pathLabel, limit)
				}
			}
			cumulativeBytes += int64(len(rawBytes))
			if cumulativeBytes > MaxBatchTotalBytes {
				return BatchCommitResult{}, fmt.Errorf("单次 batch_commit 总写入内容超过上限 (%d 字节)", MaxBatchTotalBytes)
			}
			p.finalBytes = rawBytes

		case "patch":
			if found == nil {
				return BatchCommitResult{}, fmt.Errorf("待修补文件不存在: %s", p.pathLabel)
			}
			diskPath := mountDiskPath
			if inMount {
				diskPath = p.absPath
			}
			limit := MaxPatchFileBytes
			if inMount {
				if ml := effectiveMaxFileBytes(mount); ml > 0 && ml < limit {
					limit = ml
				}
			}
			fi, stErr := os.Stat(diskPath)
			if stErr != nil {
				return BatchCommitResult{}, fmt.Errorf("无法读取待修补文件 %s: %w", p.pathLabel, stErr)
			}
			if fi.Size() > limit {
				return BatchCommitResult{}, &PatchError{
					FileID:  found.ID,
					Path:    p.pathLabel,
					Mode:    op.Mode,
					Reason:  "too_large",
					Message: fmt.Sprintf("文件 %s 大小超过文本补丁上限 (%d 字节)", p.pathLabel, limit),
				}
			}
			raw, rErr := os.ReadFile(diskPath)
			if rErr != nil {
				return BatchCommitResult{}, rErr
			}
			if IsLikelyBinaryBytes(raw) {
				return BatchCommitResult{}, &PatchError{
					FileID:  found.ID,
					Path:    p.pathLabel,
					Mode:    op.Mode,
					Reason:  "binary_file",
					Message: fmt.Sprintf("文件 %s 为二进制文件，无法执行文本补丁", p.pathLabel),
				}
			}
			patched, patchErr := ApplyPatchContent(string(raw), PatchRequest{
				Mode:        op.Mode,
				Hunks:       op.Hunks,
				UnifiedDiff: op.UnifiedDiff,
			})
			if patchErr != nil {
				var pe *PatchError
				if errors.As(patchErr, &pe) {
					pe.FileID = found.ID
					pe.Path = p.pathLabel
				}
				return BatchCommitResult{}, patchErr
			}
			if int64(len(patched)) > limit {
				return BatchCommitResult{}, &PatchError{
					FileID:  found.ID,
					Path:    p.pathLabel,
					Mode:    op.Mode,
					Reason:  "too_large",
					Message: fmt.Sprintf("补丁后文件 %s 超过大小上限 (%d 字节)", p.pathLabel, limit),
				}
			}
			cumulativeBytes += int64(len(patched))
			if cumulativeBytes > MaxBatchTotalBytes {
				return BatchCommitResult{}, fmt.Errorf("单次 batch_commit 总写入内容超过上限 (%d 字节)", MaxBatchTotalBytes)
			}
			p.finalBytes = []byte(patched)

		case "delete":
			if found == nil {
				return BatchCommitResult{}, fmt.Errorf("待删除文件不存在: %s", p.pathLabel)
			}
			s.mu.RLock()
			if inMount {
				p.deletePaths = append(p.deletePaths, p.absPath)
			} else if found.StorageMode != StorageModeRef {
				p.deletePaths = append(p.deletePaths, s.recordDiskPathLocked(*found))
			}
			for _, v := range found.Versions {
				p.deletePaths = append(p.deletePaths, s.versionDiskPathLocked(*found, v))
			}
			s.mu.RUnlock()
		}
	}

	// Phase 2: Stage temp files and Shadow backups; any error cleans up everything without touching index.
	var cleanupStagingPaths []string
	cleanupStaging := func() {
		for _, sp := range cleanupStagingPaths {
			if sp != "" {
				_ = os.Remove(sp)
			}
		}
	}

	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return BatchCommitResult{}, err
	}

	for _, p := range preps {
		if p.action == "delete" {
			continue
		}
		if p.inMount {
			parentDir := filepath.Dir(p.absPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				cleanupStaging()
				s.RecordWriteError()
				return BatchCommitResult{}, err
			}
			if err := verifyNoSymlinkPath(p.mount.DirPath, p.absPath); err != nil {
				cleanupStaging()
				return BatchCommitResult{}, err
			}
			if err := verifyParentRealPathInsideMount(p.mount.DirPath, parentDir); err != nil {
				cleanupStaging()
				return BatchCommitResult{}, err
			}
			if p.diskExisted && p.mount.ShadowBackup {
				shadowStored, shadowSize, shadowSHA, bErr := backupFileToShadowBlob(p.absPath, activeDir, p.ext)
				if bErr != nil {
					cleanupStaging()
					s.RecordWriteError()
					return BatchCommitResult{}, fmt.Errorf("创建 Shadow 版本备份失败: %w", bErr)
				}
				p.shadowPath = filepath.Join(activeDir, shadowStored)
				cleanupStagingPaths = append(cleanupStagingPaths, p.shadowPath)
				snapVer := 1
				snapUploader := actor
				snapTime := time.Now()
				snapMethod := MethodMountSync
				snapNote := "写穿前自动备份"
				var snapDev DeviceInfo
				if p.existing != nil {
					if p.existing.Version > 0 {
						snapVer = p.existing.Version
					}
					snapUploader = p.existing.Uploader
					snapTime = p.existing.UploadedAt
					snapMethod = DefaultUpdateMethod(p.existing.UpdateMethod, snapVer)
					snapNote = p.existing.VersionNote
					snapDev = DeviceInfo{
						FP:     p.existing.DeviceFP,
						Name:   p.existing.DeviceName,
						IP:     p.existing.DeviceIP,
						Detail: p.existing.DeviceDetail,
					}
				}
				p.shadowSnap = &FileVersion{
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
			tmpPath, shaHex, err := stageTempFile(p.absPath, p.finalBytes)
			if err != nil {
				cleanupStaging()
				s.RecordWriteError()
				return BatchCommitResult{}, err
			}
			p.stagedPath = tmpPath
			p.shaHex = shaHex
			cleanupStagingPaths = append(cleanupStagingPaths, tmpPath)
		} else {
			p.blobID = newID()
			p.storedName = p.blobID + p.ext
			p.activeDir = activeDir
			dstPath := filepath.Join(activeDir, p.storedName)
			_, shaHex, err := writeBlobAndHash(dstPath, bytes.NewReader(p.finalBytes))
			if err != nil {
				cleanupStaging()
				s.RecordWriteError()
				return BatchCommitResult{}, err
			}
			p.stagedPath = dstPath
			p.shaHex = shaHex
			cleanupStagingPaths = append(cleanupStagingPaths, dstPath)
		}
	}

	// Re-check all active leases before renaming/committing.
	for _, existingID := range existingIDs {
		if leaseErr := s.CheckLeaseForWrite(existingID, conc.ActorKeyID, conc.ForceLease); leaseErr != nil {
			cleanupStaging()
			return BatchCommitResult{}, leaseErr
		}
	}

	// Phase 3: Atomic renames into mount targets (with best-effort rollback if any rename fails).
	rollbackRenames := func() {
		for _, p := range preps {
			if !p.inMount || !p.renamed {
				continue
			}
			if p.diskExisted && p.shadowPath != "" {
				_ = copyFileOverwrite(p.shadowPath, p.absPath)
			} else if !p.diskExisted {
				_ = os.Remove(p.absPath)
			}
		}
		cleanupStaging()
	}

	for _, p := range preps {
		if !p.inMount || p.action == "delete" {
			continue
		}
		if err := renameFileWithRetry(p.stagedPath, p.absPath); err != nil {
			rollbackRenames()
			s.RecordWriteError()
			return BatchCommitResult{}, err
		}
		p.renamed = true
		p.stagedPath = "" // Already moved to destination
	}

	batchID := "bat_" + newID()[:8]
	cleanNote := strings.TrimSpace(req.Note)
	verNote := cleanNote
	if verNote != "" {
		verNote += " [batch:" + batchID + "]"
	} else {
		verNote = "batch_commit [" + batchID + "]"
	}
	now := time.Now()

	// Single s.mu.Lock commit for the entire batch.
	s.mu.Lock()
	var touchedMounts []string
	seenMount := make(map[string]bool)
	markMount := func(mid string) {
		if mid != "" && !seenMount[mid] {
			seenMount[mid] = true
			touchedMounts = append(touchedMounts, mid)
		}
	}

	// Verify state right before commit.
	for _, p := range preps {
		var cur *FileRecord
		for _, rec := range s.records {
			if p.inMount && rec.StorageMode != StorageModeMount {
				continue
			}
			if !p.inMount && rec.StorageMode == StorageModeMount {
				continue
			}
			if rec.FolderPath == p.folderPath && strings.EqualFold(rec.Name, p.fileName) {
				cp := rec
				cur = &cp
				break
			}
		}
		if err := checkExpectedVersion(cur, p.folderPath, p.fileName, p.expectedVer); err != nil {
			s.mu.Unlock()
			rollbackRenames()
			return BatchCommitResult{}, err
		}
		if p.inMount {
			if _, ok := s.mounts[p.mount.ID]; !ok {
				s.mu.Unlock()
				rollbackRenames()
				return BatchCommitResult{}, errors.New("挂载目录已移除")
			}
		}
		p.existing = cur
	}

	results := make([]BatchOpResult, len(preps))
	var deletedIDs []string
	var filesToRemoveAfter []string
	var totalBytesWritten int64

	for i, p := range preps {
		if p.action == "delete" {
			id := p.existing.ID
			delete(s.records, id)
			deletedIDs = append(deletedIDs, id)
			filesToRemoveAfter = append(filesToRemoveAfter, p.deletePaths...)
			if p.inMount {
				markMount(p.mount.ID)
			}
			results[i] = BatchOpResult{
				Index:   p.index,
				Action:  p.action,
				Path:    p.pathLabel,
				FileID:  id,
				Deleted: true,
			}
			continue
		}

		n := int64(len(p.finalBytes))
		totalBytesWritten += n

		if p.inMount {
			fileMod := now
			if fi, stErr := os.Stat(p.absPath); stErr == nil {
				fileMod = fi.ModTime()
			}
			curMount := s.mounts[p.mount.ID]
			markMount(curMount.ID)
			var rec FileRecord
			if p.existing != nil {
				rec = *p.existing
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
				if p.shadowSnap != nil {
					p.shadowSnap.Version = curVer
					rec.Versions = append([]FileVersion{*p.shadowSnap}, rec.Versions...)
					sort.Slice(rec.Versions, func(a, b int) bool {
						return rec.Versions[a].Version > rec.Versions[b].Version
					})
				}
				rec.Name = p.fileName
				rec.FolderPath = p.folderPath
				rec.Ext = p.ext
				rec.Category = ClassifyExt(p.ext)
				rec.Size = n
				rec.StoredName = ""
				rec.StorageDir = ""
				rec.StorageMode = StorageModeMount
				rec.SourcePath = p.absPath
				rec.SourceMissing = false
				rec.MountID = curMount.ID
				rec.SHA256 = p.shaHex
				rec.IntegrityStatus = "ok"
				verifiedAt := now
				rec.LastVerifiedAt = &verifiedAt
				rec.Version = maxVer + 1
				rec.VersionCount = 1 + len(rec.Versions)
				rec.VersionNote = verNote
				rec.UpdateMethod = MethodBatchCommit
				rec.Uploader = actor
				rec.UploadedAt = fileMod
				rec.MonthKey = fileMod.Format("2006-01")
				rec.DateKey = fileMod.Format("2006-01-02")
				rec.DeviceFP = strings.TrimSpace(dev.FP)
				rec.DeviceName = strings.TrimSpace(dev.Name)
				rec.DeviceIP = strings.TrimSpace(dev.IP)
				rec.DeviceDetail = strings.TrimSpace(dev.Detail)
				if len(p.allowedUsers) > 0 {
					rec.AllowedUsers = p.allowedUsers
				} else if len(rec.AllowedUsers) == 0 && len(curMount.AllowedUsers) > 0 {
					rec.AllowedUsers = curMount.AllowedUsers
				}
				rec.NodeOnline = true
				s.records[rec.ID] = rec
			} else {
				newRecID := newID()
				var versions []FileVersion
				nextVer := 1
				if p.shadowSnap != nil {
					versions = []FileVersion{*p.shadowSnap}
					nextVer = 2
				}
				effAllowed := p.allowedUsers
				if len(effAllowed) == 0 && len(curMount.AllowedUsers) > 0 {
					effAllowed = curMount.AllowedUsers
				}
				verifiedAt := now
				rec = FileRecord{
					ID:              newRecID,
					Name:            p.fileName,
					FolderPath:      p.folderPath,
					Size:            n,
					Ext:             p.ext,
					Category:        ClassifyExt(p.ext),
					Uploader:        actor,
					UploadedAt:      fileMod,
					MonthKey:        fileMod.Format("2006-01"),
					DateKey:         fileMod.Format("2006-01-02"),
					DeviceFP:        strings.TrimSpace(dev.FP),
					DeviceName:      strings.TrimSpace(dev.Name),
					DeviceIP:        strings.TrimSpace(dev.IP),
					DeviceDetail:    strings.TrimSpace(dev.Detail),
					AllowedUsers:    effAllowed,
					StorageMode:     StorageModeMount,
					SourcePath:      p.absPath,
					MountID:         curMount.ID,
					SHA256:          p.shaHex,
					IntegrityStatus: "ok",
					LastVerifiedAt:  &verifiedAt,
					Version:         nextVer,
					VersionCount:    1 + len(versions),
					VersionNote:     verNote,
					UpdateMethod:    MethodBatchCommit,
					Versions:        versions,
					NodeOnline:      true,
				}
				s.records[newRecID] = rec
			}
			recCopy := rec
			results[i] = BatchOpResult{
				Index:   p.index,
				Action:  p.action,
				Path:    p.pathLabel,
				FileID:  rec.ID,
				Version: rec.Version,
				Record:  &recCopy,
			}
			continue
		}

		// Non-mount upload/patch record
		var rec FileRecord
		if p.existing != nil {
			rec = *p.existing
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
			if rec.StoredName != "" {
				prevSnap := FileVersion{
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
					SHA256:       rec.SHA256,
					Note:         rec.VersionNote,
					UpdateMethod: DefaultUpdateMethod(rec.UpdateMethod, curVer),
				}
				rec.Versions = append([]FileVersion{prevSnap}, rec.Versions...)
				sort.Slice(rec.Versions, func(a, b int) bool {
					return rec.Versions[a].Version > rec.Versions[b].Version
				})
			}
			rec.Version = maxVer + 1
			rec.VersionCount = 1 + len(rec.Versions)
			rec.VersionNote = verNote
			rec.UpdateMethod = MethodBatchCommit
			rec.StoredName = p.storedName
			rec.StorageMode = StorageModeCopy
			rec.SourcePath = ""
			rec.SourceMissing = false
			rec.MountID = ""
			rec.SHA256 = p.shaHex
			rec.IntegrityStatus = "ok"
			verifiedAt := now
			rec.LastVerifiedAt = &verifiedAt
			rec.Size = n
			rec.Uploader = actor
			rec.UploadedAt = now
			rec.MonthKey = now.Format("2006-01")
			rec.DateKey = now.Format("2006-01-02")
			rec.DeviceFP = strings.TrimSpace(dev.FP)
			rec.DeviceName = strings.TrimSpace(dev.Name)
			rec.DeviceIP = strings.TrimSpace(dev.IP)
			rec.DeviceDetail = strings.TrimSpace(dev.Detail)
			if len(p.allowedUsers) > 0 {
				rec.AllowedUsers = p.allowedUsers
			}
			rec.StorageDir = p.activeDir
			rec.NodeOnline = true
			s.records[rec.ID] = rec
		} else {
			verifiedAt := now
			rec = FileRecord{
				ID:              p.blobID,
				Name:            p.fileName,
				FolderPath:      p.folderPath,
				StoredName:      p.storedName,
				Size:            n,
				Ext:             p.ext,
				Category:        ClassifyExt(p.ext),
				Uploader:        actor,
				UploadedAt:      now,
				MonthKey:        now.Format("2006-01"),
				DateKey:         now.Format("2006-01-02"),
				DeviceFP:        strings.TrimSpace(dev.FP),
				DeviceName:      strings.TrimSpace(dev.Name),
				DeviceIP:        strings.TrimSpace(dev.IP),
				DeviceDetail:    strings.TrimSpace(dev.Detail),
				AllowedUsers:    p.allowedUsers,
				StorageDir:      p.activeDir,
				SHA256:          p.shaHex,
				IntegrityStatus: "ok",
				LastVerifiedAt:  &verifiedAt,
				Version:         1,
				VersionCount:    1,
				VersionNote:     verNote,
				UpdateMethod:    MethodBatchCommit,
				NodeOnline:      true,
			}
			s.records[rec.ID] = rec
		}
		recCopy := rec
		results[i] = BatchOpResult{
			Index:   p.index,
			Action:  p.action,
			Path:    p.pathLabel,
			FileID:  rec.ID,
			Version: rec.Version,
			Record:  &recCopy,
		}
	}

	for _, mid := range touchedMounts {
		s.recomputeMountStatsLocked(mid)
	}
	if len(touchedMounts) > 0 {
		if err := s.saveMountsLocked(); err != nil {
			s.mu.Unlock()
			rollbackRenames()
			s.RecordWriteError()
			return BatchCommitResult{}, err
		}
	}
	var batchUpserts []FileRecord
	for _, r := range results {
		if !r.Deleted && r.Record != nil {
			batchUpserts = append(batchUpserts, *r.Record)
		}
	}
	if err := s.appendWALBatchLocked(batchID, batchUpserts, deletedIDs); err != nil {
		s.mu.Unlock()
		rollbackRenames()
		s.RecordWriteError()
		return BatchCommitResult{}, err
	}
	s.mu.Unlock()

	for _, dp := range filesToRemoveAfter {
		if dp != "" {
			if err := os.Remove(dp); err != nil && !os.IsNotExist(err) {
				s.RecordWriteError()
			}
		}
	}
	s.clearLeases(deletedIDs)
	s.AddUploadBytes(totalBytesWritten)

	return BatchCommitResult{
		BatchID: batchID,
		Note:    cleanNote,
		Results: results,
	}, nil
}
