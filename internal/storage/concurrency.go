package storage

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultLeaseTTL = 60 * time.Second
	MinLeaseTTL     = 5 * time.Second
	MaxLeaseTTL     = 300 * time.Second
)

// ConflictError indicates an optimistic locking failure (HTTP 409 Conflict).
type ConflictError struct {
	FileID          string    `json:"file_id,omitempty"`
	Path            string    `json:"path"`
	ExpectedVersion int       `json:"expected_version"`
	CurrentVersion  int       `json:"current_version"`
	CurrentSHA256   string    `json:"current_sha256,omitempty"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

func (e *ConflictError) Error() string {
	if e.CurrentVersion <= 0 {
		return fmt.Sprintf("文件 %s 版本冲突: 期望版本 v%d，当前文件尚不存在", e.Path, e.ExpectedVersion)
	}
	if e.UpdatedBy != "" {
		return fmt.Sprintf("文件 %s 版本冲突: 期望版本 v%d，当前最新版本为 v%d（由 %s 更新）", e.Path, e.ExpectedVersion, e.CurrentVersion, e.UpdatedBy)
	}
	return fmt.Sprintf("文件 %s 版本冲突: 期望版本 v%d，当前最新版本为 v%d", e.Path, e.ExpectedVersion, e.CurrentVersion)
}

// LeaseLockedError indicates an active soft lease held by another actor (HTTP 423 Locked).
type LeaseLockedError struct {
	FileID       string    `json:"file_id"`
	HolderKeyID  string    `json:"holder_key_id"`
	HolderName   string    `json:"holder_name"`
	ExpiresAt    time.Time `json:"expires_at"`
	RemainingSec int       `json:"remaining_sec"`
}

func (e *LeaseLockedError) Error() string {
	holder := e.HolderName
	if holder == "" {
		holder = e.HolderKeyID
	}
	return fmt.Sprintf("文件 %s 正在被 %s 锁定编辑（剩余 %d 秒）", e.FileID, holder, e.RemainingSec)
}

// FileLease represents an in-memory expiring edit lock bound to an actor/AgentKey.
type FileLease struct {
	FileID      string    `json:"file_id"`
	HolderKeyID string    `json:"holder_key_id"`
	HolderName  string    `json:"holder_name"`
	AcquiredAt  time.Time `json:"acquired_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// WriteConcurrencyOptions configures optimistic locking and soft-lease enforcement per write.
type WriteConcurrencyOptions struct {
	ExpectedVersion *int
	ActorKeyID      string
	ForceLease      bool
}

func logicalFilePath(folderPath, name string) string {
	cleanName := sanitizeFilename(name)
	cleanFolder := SanitizeFolderPath(folderPath)
	if cleanFolder == "" {
		return cleanName
	}
	return cleanFolder + "/" + cleanName
}

func pathLockKey(folderPath, name string) string {
	return strings.ToLower(logicalFilePath(folderPath, name))
}

func (s *Store) stripeSlot(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(key))))
	return int(h.Sum32() % uint32(len(s.fileLocks)))
}

func (s *Store) lockPath(folderPath, name string) func() {
	slot := s.stripeSlot(pathLockKey(folderPath, name))
	s.fileLocks[slot].Lock()
	return func() {
		s.fileLocks[slot].Unlock()
	}
}

func (s *Store) lockPaths(keys []string) func() {
	if len(keys) == 0 {
		return func() {}
	}
	seen := make(map[int]bool, len(keys))
	slots := make([]int, 0, len(keys))
	for _, k := range keys {
		slot := s.stripeSlot(k)
		if !seen[slot] {
			seen[slot] = true
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	for _, slot := range slots {
		s.fileLocks[slot].Lock()
	}
	return func() {
		for i := len(slots) - 1; i >= 0; i-- {
			s.fileLocks[slots[i]].Unlock()
		}
	}
}

func formatRecordUpdatedBy(rec FileRecord) string {
	if strings.HasPrefix(rec.DeviceName, "Agent · ") {
		if rec.Uploader != "" {
			return rec.DeviceName + " (" + rec.Uploader + ")"
		}
		return rec.DeviceName
	}
	if rec.Uploader != "" {
		return rec.Uploader
	}
	return rec.DeviceName
}

func checkExpectedVersion(existing *FileRecord, folderPath, name string, expected *int) error {
	if expected == nil {
		return nil
	}
	want := *expected
	if want < 0 {
		return errors.New("expected_version 不能为负数")
	}
	pathLabel := logicalFilePath(folderPath, name)
	if existing == nil {
		if want == 0 {
			return nil
		}
		return &ConflictError{
			Path:            pathLabel,
			ExpectedVersion: want,
			CurrentVersion:  0,
		}
	}
	curVer := existing.Version
	if curVer <= 0 {
		curVer = 1
	}
	if want == 0 || want != curVer {
		return &ConflictError{
			FileID:          existing.ID,
			Path:            pathLabel,
			ExpectedVersion: want,
			CurrentVersion:  curVer,
			CurrentSHA256:   existing.SHA256,
			UpdatedBy:       formatRecordUpdatedBy(*existing),
			UpdatedAt:       existing.UploadedAt,
		}
	}
	return nil
}

func newLeaseLockedErr(cur FileLease, now time.Time) *LeaseLockedError {
	rem := int(math.Ceil(cur.ExpiresAt.Sub(now).Seconds()))
	if rem < 1 {
		rem = 1
	}
	return &LeaseLockedError{
		FileID:       cur.FileID,
		HolderKeyID:  cur.HolderKeyID,
		HolderName:   cur.HolderName,
		ExpiresAt:    cur.ExpiresAt,
		RemainingSec: rem,
	}
}

func clampLeaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultLeaseTTL
	}
	if ttl < MinLeaseTTL {
		return MinLeaseTTL
	}
	if ttl > MaxLeaseTTL {
		return MaxLeaseTTL
	}
	return ttl
}

// AcquireLease acquires or renews an expiring in-memory edit lease on fileID.
func (s *Store) AcquireLease(fileID, holderKeyID, holderName string, ttl time.Duration, force bool) (FileLease, error) {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return FileLease{}, errors.New("文件 ID 不能为空")
	}
	s.mu.RLock()
	_, exists := s.records[id]
	s.mu.RUnlock()
	if !exists {
		return FileLease{}, errors.New("文件不存在")
	}

	holderID := strings.TrimSpace(holderKeyID)
	name := strings.TrimSpace(holderName)
	if holderID == "" {
		holderID = name
	}
	if holderID == "" {
		return FileLease{}, errors.New("租约持有者标识不能为空")
	}
	if name == "" {
		name = holderID
	}

	ttl = clampLeaseTTL(ttl)
	now := time.Now()

	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leases == nil {
		s.leases = make(map[string]FileLease)
	}
	for k, v := range s.leases {
		if !now.Before(v.ExpiresAt) {
			delete(s.leases, k)
		}
	}

	acquiredAt := now
	if cur, ok := s.leases[id]; ok && now.Before(cur.ExpiresAt) {
		if cur.HolderKeyID != holderID && !force {
			return FileLease{}, newLeaseLockedErr(cur, now)
		}
		if cur.HolderKeyID == holderID && !cur.AcquiredAt.IsZero() {
			acquiredAt = cur.AcquiredAt
		}
	}

	lease := FileLease{
		FileID:      id,
		HolderKeyID: holderID,
		HolderName:  name,
		AcquiredAt:  acquiredAt,
		ExpiresAt:   now.Add(ttl),
	}
	s.leases[id] = lease
	return lease, nil
}

// ReleaseLease releases an active lease if held by holderKeyID (or if force is true).
func (s *Store) ReleaseLease(fileID, holderKeyID string, force bool) error {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return nil
	}
	holderID := strings.TrimSpace(holderKeyID)
	now := time.Now()

	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leases == nil {
		return nil
	}
	cur, ok := s.leases[id]
	if !ok {
		return nil
	}
	if !now.Before(cur.ExpiresAt) {
		delete(s.leases, id)
		return nil
	}
	if cur.HolderKeyID != holderID && !force {
		return newLeaseLockedErr(cur, now)
	}
	delete(s.leases, id)
	return nil
}

// GetLease returns the active non-expired lease for fileID if present.
func (s *Store) GetLease(fileID string) (FileLease, bool) {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return FileLease{}, false
	}
	now := time.Now()
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leases == nil {
		return FileLease{}, false
	}
	cur, ok := s.leases[id]
	if !ok {
		return FileLease{}, false
	}
	if !now.Before(cur.ExpiresAt) {
		delete(s.leases, id)
		return FileLease{}, false
	}
	return cur, true
}

// ListActiveLeases returns all non-expired soft edit leases currently held on this node.
func (s *Store) ListActiveLeases() []FileLease {
	now := time.Now()
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if len(s.leases) == 0 {
		return nil
	}
	out := make([]FileLease, 0, len(s.leases))
	for id, cur := range s.leases {
		if !now.Before(cur.ExpiresAt) {
			delete(s.leases, id)
			continue
		}
		out = append(out, cur)
	}
	return out
}

// CheckLeaseForWrite verifies whether actorKeyID may mutate fileID right now.
func (s *Store) CheckLeaseForWrite(fileID, actorKeyID string, force bool) error {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return nil
	}
	now := time.Now()
	actor := strings.TrimSpace(actorKeyID)

	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leases == nil {
		return nil
	}
	cur, ok := s.leases[id]
	if !ok {
		return nil
	}
	if !now.Before(cur.ExpiresAt) {
		delete(s.leases, id)
		return nil
	}
	if force || (actor != "" && cur.HolderKeyID == actor) {
		return nil
	}
	return newLeaseLockedErr(cur, now)
}

func (s *Store) clearLease(fileID string) {
	if fileID == "" {
		return
	}
	s.leaseMu.Lock()
	delete(s.leases, fileID)
	s.leaseMu.Unlock()
}

func (s *Store) clearLeases(fileIDs []string) {
	if len(fileIDs) == 0 {
		return
	}
	s.leaseMu.Lock()
	for _, id := range fileIDs {
		delete(s.leases, id)
	}
	s.leaseMu.Unlock()
}

// SaveUploadWithConcurrency saves or updates a file with striped locking, soft-lease checks,
// optimistic concurrency control, and transparent routing into writable mounted directories.
func (s *Store) SaveUploadWithConcurrency(origName, folderPath, versionNote, uploader string, allowedUsers []string, dev DeviceInfo, r io.Reader, conc WriteConcurrencyOptions) (FileRecord, error) {
	if conc.ExpectedVersion != nil && *conc.ExpectedVersion < 0 {
		return FileRecord{}, errors.New("expected_version 不能为负数")
	}
	origName = sanitizeFilename(origName)
	folderPath = SanitizeFolderPath(folderPath)

	if mount, relDir, inMount := s.FindMountByFolderPath(folderPath); inMount {
		if !mount.Writable {
			return FileRecord{}, errors.New("该目录属于只读挂载目录，请先在挂载配置中开启可写工作区")
		}
		relSlash := origName
		if relDir != "" {
			relSlash = relDir + "/" + origName
		}
		return s.WriteMountFile(mount.ID, relSlash, versionNote, "", uploader, allowedUsers, dev, r, conc)
	}

	unlock := s.lockPath(folderPath, origName)
	defer unlock()

	// Fast pre-check under read lock before streaming payload to disk.
	s.mu.RLock()
	var preExisting *FileRecord
	for _, cur := range s.records {
		if cur.StorageMode == StorageModeMount {
			continue
		}
		if strings.EqualFold(cur.Name, origName) && cur.FolderPath == folderPath {
			cp := cur
			preExisting = &cp
			break
		}
	}
	s.mu.RUnlock()

	if preExisting != nil {
		if err := s.CheckLeaseForWrite(preExisting.ID, conc.ActorKeyID, conc.ForceLease); err != nil {
			return FileRecord{}, err
		}
	}
	if err := checkExpectedVersion(preExisting, folderPath, origName, conc.ExpectedVersion); err != nil {
		return FileRecord{}, err
	}

	ext := strings.ToLower(filepath.Ext(origName))
	blobID := newID()
	storedName := blobID + ext
	activeDir := s.UploadDir()
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	dstPath := filepath.Join(activeDir, storedName)

	n, shaHex, err := writeBlobAndHash(dstPath, r)
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}

	now := time.Now()
	cleanNote := strings.TrimSpace(versionNote)
	normAllowed := NormalizeAllowedUsers(allowedUsers)

	// Re-check lease right before taking s.mu.Lock() (disjoint lock ordering).
	if preExisting != nil {
		if leaseErr := s.CheckLeaseForWrite(preExisting.ID, conc.ActorKeyID, conc.ForceLease); leaseErr != nil {
			_ = os.Remove(dstPath)
			return FileRecord{}, leaseErr
		}
	}

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

	var curPtr *FileRecord
	if existingID != "" {
		cp := s.records[existingID]
		curPtr = &cp
	}
	if verErr := checkExpectedVersion(curPtr, folderPath, origName, conc.ExpectedVersion); verErr != nil {
		s.mu.Unlock()
		_ = os.Remove(dstPath)
		return FileRecord{}, verErr
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
				SHA256:       cur.SHA256,
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
		cur.SHA256 = shaHex
		cur.IntegrityStatus = "ok"
		verifiedNow := now
		cur.LastVerifiedAt = &verifiedNow
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
		verifiedNow := now
		rec = FileRecord{
			ID:              blobID,
			Name:            origName,
			FolderPath:      folderPath,
			StoredName:      storedName,
			Size:            n,
			Ext:             ext,
			Category:        ClassifyExt(ext),
			Uploader:        uploader,
			UploadedAt:      now,
			MonthKey:        now.Format("2006-01"),
			DateKey:         now.Format("2006-01-02"),
			DeviceFP:        strings.TrimSpace(dev.FP),
			DeviceName:      strings.TrimSpace(dev.Name),
			DeviceIP:        strings.TrimSpace(dev.IP),
			DeviceDetail:    strings.TrimSpace(dev.Detail),
			AllowedUsers:    normAllowed,
			StorageDir:      activeDir,
			SHA256:          shaHex,
			IntegrityStatus: "ok",
			LastVerifiedAt:  &verifiedNow,
			Version:         1,
			VersionCount:    1,
			VersionNote:     cleanNote,
			UpdateMethod:    MethodInitialUpload,
			NodeOnline:      true,
		}
		s.records[blobID] = rec
	}
	err = s.appendWALUpsertLocked(rec)
	s.mu.Unlock()
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}
	s.AddUploadBytes(n)
	return rec, nil
}

// SaveEditedTextWithConcurrency saves online-edited text with lease and optimistic locking checks.
func (s *Store) SaveEditedTextWithConcurrency(id, content, note, uploader string, dev DeviceInfo, conc WriteConcurrencyOptions) (FileRecord, error) {
	return s.SaveNewVersionWithConcurrency(id, "", note, MethodOnlineEdit, uploader, dev, strings.NewReader(content), conc)
}

// SaveNewVersionWithConcurrency appends a new version (or writes through a writable mount) under striped locks.
func (s *Store) SaveNewVersionWithConcurrency(id, origName, note, method, uploader string, dev DeviceInfo, r io.Reader, conc WriteConcurrencyOptions) (FileRecord, error) {
	if conc.ExpectedVersion != nil && *conc.ExpectedVersion < 0 {
		return FileRecord{}, errors.New("expected_version 不能为负数")
	}
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
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}

	if latest.StorageMode == StorageModeMount {
		if !hasMount || !mount.Writable {
			return FileRecord{}, errors.New("本机挂载目录文件由源文件夹自动同步，请在原目录修改或下载后另存")
		}
		rel, relErr := filepath.Rel(mount.DirPath, latest.SourcePath)
		if relErr != nil {
			return FileRecord{}, relErr
		}
		return s.writeMountFileUnderLock(mount.ID, filepath.ToSlash(rel), note, method, uploader, latest.AllowedUsers, dev, r, conc)
	}

	if err := s.CheckLeaseForWrite(id, conc.ActorKeyID, conc.ForceLease); err != nil {
		return FileRecord{}, err
	}
	if err := checkExpectedVersion(&latest, latest.FolderPath, latest.Name, conc.ExpectedVersion); err != nil {
		return FileRecord{}, err
	}

	ext := latest.Ext
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
	n, shaHex, err := writeBlobAndHash(dstPath, r)
	if err != nil {
		s.RecordWriteError()
		return FileRecord{}, err
	}

	if leaseErr := s.CheckLeaseForWrite(id, conc.ActorKeyID, conc.ForceLease); leaseErr != nil {
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
		return FileRecord{}, errors.New("本机挂载目录文件由源文件夹自动同步，请在原目录修改或下载后另存")
	}
	if verErr := checkExpectedVersion(&finalRec, finalRec.FolderPath, finalRec.Name, conc.ExpectedVersion); verErr != nil {
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
	finalRec.VersionNote = strings.TrimSpace(note)
	if strings.TrimSpace(method) == "" {
		method = MethodVersionUpload
	}
	finalRec.UpdateMethod = strings.TrimSpace(method)
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

// RestoreVersionWithConcurrency restores a historical version under striped lock, lease, and expected_version checks.
func (s *Store) RestoreVersionWithConcurrency(id string, targetVer int, note, actor string, dev DeviceInfo, conc WriteConcurrencyOptions) (FileRecord, error) {
	if conc.ExpectedVersion != nil && *conc.ExpectedVersion < 0 {
		return FileRecord{}, errors.New("expected_version 不能为负数")
	}
	s.mu.RLock()
	cur, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		return FileRecord{}, errors.New("文件不存在")
	}

	unlock := s.lockPath(cur.FolderPath, cur.Name)
	defer unlock()

	if err := s.CheckLeaseForWrite(id, conc.ActorKeyID, conc.ForceLease); err != nil {
		return FileRecord{}, err
	}

	s.mu.RLock()
	latest, exists := s.records[id]
	s.mu.RUnlock()
	if !exists {
		return FileRecord{}, errors.New("文件不存在")
	}
	if latest.StorageMode == StorageModeMount {
		return s.restoreMountVersionUnderLock(id, targetVer, note, actor, dev, conc)
	}
	return s.restoreVersionLocked(id, targetVer, note, actor, dev, conc)
}

// DeleteWithConcurrency removes a file record (and deletes writable mount host files when enabled) safely under locks.
func (s *Store) DeleteWithConcurrency(id string, conc WriteConcurrencyOptions) error {
	if conc.ExpectedVersion != nil && *conc.ExpectedVersion < 0 {
		return errors.New("expected_version 不能为负数")
	}
	s.mu.RLock()
	cur, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		return errors.New("文件不存在")
	}

	unlock := s.lockPath(cur.FolderPath, cur.Name)
	defer unlock()

	if err := s.CheckLeaseForWrite(id, conc.ActorKeyID, conc.ForceLease); err != nil {
		return err
	}

	s.mu.Lock()
	rec, ok := s.records[id]
	if !ok {
		s.mu.Unlock()
		return errors.New("文件不存在")
	}
	if err := checkExpectedVersion(&rec, rec.FolderPath, rec.Name, conc.ExpectedVersion); err != nil {
		s.mu.Unlock()
		return err
	}

	var mountIDToUpdate string
	var pathsToRemove []string

	if rec.StorageMode == StorageModeMount {
		mount, mOK := s.mounts[rec.MountID]
		if !mOK || !mount.Writable {
			s.mu.Unlock()
			return errors.New("该文件来自本机挂载目录，请通过移除对应挂载目录或在原文件夹中管理")
		}
		rel, relErr := filepath.Rel(mount.DirPath, rec.SourcePath)
		if relErr != nil {
			s.mu.Unlock()
			return relErr
		}
		relSlash := filepath.ToSlash(rel)
		if isHardcodedBlockedMountPath(relSlash) {
			s.mu.Unlock()
			return errors.New("安全策略禁止删除受保护的敏感路径或控制文件")
		}
		if err := verifyNoSymlinkPath(mount.DirPath, rec.SourcePath); err != nil {
			s.mu.Unlock()
			return err
		}
		mountIDToUpdate = mount.ID
		pathsToRemove = append(pathsToRemove, rec.SourcePath)
	} else if rec.StorageMode != StorageModeRef {
		pathsToRemove = append(pathsToRemove, s.recordDiskPathLocked(rec))
	}

	for _, v := range rec.Versions {
		pathsToRemove = append(pathsToRemove, s.versionDiskPathLocked(rec, v))
	}

	delete(s.records, id)
	if mountIDToUpdate != "" {
		s.recomputeMountStatsLocked(mountIDToUpdate)
		if err := s.saveMountsLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	if err := s.appendWALDeleteLocked(id); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	for _, p := range pathsToRemove {
		if p != "" {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				s.RecordWriteError()
			}
		}
	}
	s.clearLease(id)
	return nil
}
