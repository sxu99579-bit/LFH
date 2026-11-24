package storage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultWALDebounce       = 2 * time.Second
	defaultWALMaxOps         = 300
	defaultWALMaxBytes int64 = 4 << 20
	maxWALLineBytes          = 16 << 20
)

type walEntry struct {
	Seq       uint64       `json:"seq"`
	TS        int64        `json:"ts"`
	Op        string       `json:"op"` // "upsert", "delete", "batch_upsert"
	Record    *FileRecord  `json:"record,omitempty"`
	ID        string       `json:"id,omitempty"`
	BatchID   string       `json:"batch_id,omitempty"`
	Records   []FileRecord `json:"records,omitempty"`
	DeleteIDs []string     `json:"delete_ids,omitempty"`
}

// WALStats exposes write-ahead log sequence and pending compaction metrics.
type WALStats struct {
	Seq          uint64 `json:"seq"`
	PendingOps   int    `json:"pending_ops"`
	PendingBytes int64  `json:"pending_bytes"`
	Compactions  uint64 `json:"compactions"`
}

func (s *Store) normalizeLoadedRecordLocked(rec FileRecord) (FileRecord, bool) {
	_, statErr := os.Stat(s.recordDiskPathLocked(rec))
	isExternal := rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount
	if statErr != nil && !isExternal {
		return FileRecord{}, false
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
	return rec, true
}

func (s *Store) replayWALLocked() (replayed int, corruptedTail bool, err error) {
	f, err := os.Open(s.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxWALLineBytes)

	var maxSeq uint64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry walEntry
		if uErr := json.Unmarshal([]byte(line), &entry); uErr != nil {
			// Crash-safe tail tolerance: ignore a partial/corrupted trailing line written before crash.
			corruptedTail = true
			break
		}
		if entry.Seq > maxSeq {
			maxSeq = entry.Seq
		}
		switch entry.Op {
		case "upsert":
			if entry.Record != nil && entry.Record.ID != "" {
				if norm, ok := s.normalizeLoadedRecordLocked(*entry.Record); ok {
					s.records[norm.ID] = norm
				} else {
					delete(s.records, entry.Record.ID)
				}
				replayed++
			}
		case "delete":
			if entry.ID != "" {
				delete(s.records, entry.ID)
				replayed++
			}
		case "batch_upsert":
			for _, rec := range entry.Records {
				if rec.ID == "" {
					continue
				}
				if norm, ok := s.normalizeLoadedRecordLocked(rec); ok {
					s.records[norm.ID] = norm
				} else {
					delete(s.records, rec.ID)
				}
			}
			for _, delID := range entry.DeleteIDs {
				if delID != "" {
					delete(s.records, delID)
				}
			}
			replayed++
		}
	}
	if scErr := sc.Err(); scErr != nil {
		corruptedTail = true
	}
	s.walSeq = maxSeq
	return replayed, corruptedTail, nil
}

func (s *Store) marshalSnapshotRecords() ([]byte, error) {
	list := make([]FileRecord, 0, len(s.records))
	for _, rec := range s.records {
		list = append(list, rec)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UploadedAt.After(list[j].UploadedAt)
	})
	return json.MarshalIndent(list, "", "  ")
}

func writeSnapshotTempFile(indexPath string, raw []byte) error {
	dir := filepath.Dir(indexPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".files.json.tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := renameFileWithRetry(tmpPath, indexPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (s *Store) writeFullSnapshotLocked() error {
	raw, err := s.marshalSnapshotRecords()
	if err != nil {
		return err
	}
	if err := writeSnapshotTempFile(s.indexPath, raw); err != nil {
		return err
	}
	_ = os.Remove(s.walPath)

	s.walMu.Lock()
	if s.walTimer != nil {
		s.walTimer.Stop()
		s.walTimer = nil
	}
	s.walOps = 0
	s.walBytes = 0
	s.walCompactions++
	s.walMu.Unlock()
	return nil
}

func (s *Store) appendWALEntryLocked(entry walEntry) error {
	s.walSeq++
	entry.Seq = s.walSeq
	entry.TS = time.Now().UnixMilli()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	// Open-write-close per append so no background handle lingers across Windows temp-dir teardown.
	f, err := os.OpenFile(s.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	needImmediate := false
	s.walMu.Lock()
	s.walOps++
	s.walBytes += int64(len(line))
	if s.walOps >= defaultWALMaxOps || s.walBytes >= defaultWALMaxBytes {
		needImmediate = true
	} else if !s.closed.Load() {
		debounce := s.walDebounce
		if debounce <= 0 {
			debounce = defaultWALDebounce
		}
		if s.walTimer != nil {
			s.walTimer.Stop()
		}
		s.walTimer = time.AfterFunc(debounce, func() {
			_ = s.CompactWAL()
		})
	}
	s.walMu.Unlock()

	if needImmediate && !s.closed.Load() {
		go func() {
			_ = s.CompactWAL()
		}()
	}
	return nil
}

func (s *Store) appendWALUpsertLocked(rec FileRecord) error {
	cp := rec
	return s.appendWALEntryLocked(walEntry{
		Op:     "upsert",
		Record: &cp,
	})
}

func (s *Store) appendWALDeleteLocked(id string) error {
	return s.appendWALEntryLocked(walEntry{
		Op: "delete",
		ID: id,
	})
}

func (s *Store) appendWALBatchLocked(batchID string, records []FileRecord, deleteIDs []string) error {
	var recsCopy []FileRecord
	if len(records) > 0 {
		recsCopy = make([]FileRecord, len(records))
		copy(recsCopy, records)
	}
	var delsCopy []string
	if len(deleteIDs) > 0 {
		delsCopy = make([]string, len(deleteIDs))
		copy(delsCopy, deleteIDs)
	}
	return s.appendWALEntryLocked(walEntry{
		Op:        "batch_upsert",
		BatchID:   batchID,
		Records:   recsCopy,
		DeleteIDs: delsCopy,
	})
}

// CompactWAL atomically materializes files.json from current in-memory records and truncates files.wal.
func (s *Store) CompactWAL() error {
	if s.closed.Load() {
		return nil
	}
	if _, err := os.Stat(s.DataDir()); err != nil {
		return nil
	}

	s.mu.RLock()
	s.walMu.Lock()
	hasPending := s.walOps > 0 || s.walBytes > 0
	s.walMu.Unlock()
	if !hasPending {
		s.mu.RUnlock()
		return nil
	}
	snapSeq := s.walSeq
	list := make([]FileRecord, 0, len(s.records))
	for _, rec := range s.records {
		list = append(list, rec)
	}
	s.mu.RUnlock()

	sort.Slice(list, func(i, j int) bool {
		return list[i].UploadedAt.After(list[j].UploadedAt)
	})
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.indexPath)
	tmp, err := os.CreateTemp(dir, ".files.json.tmp.*")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	if _, wErr := tmp.Write(raw); wErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return wErr
	}
	if sErr := tmp.Sync(); sErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return sErr
	}
	if cErr := tmp.Close(); cErr != nil {
		_ = os.Remove(tmpPath)
		return cErr
	}

	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		_ = os.Remove(tmpPath)
		return nil
	}
	if s.walSeq != snapSeq {
		// Concurrent writes landed while marshaling outside lock; drop stale snapshot and re-schedule.
		s.mu.Unlock()
		_ = os.Remove(tmpPath)
		s.walMu.Lock()
		if !s.closed.Load() {
			debounce := s.walDebounce
			if debounce <= 0 {
				debounce = defaultWALDebounce
			}
			if s.walTimer != nil {
				s.walTimer.Stop()
			}
			s.walTimer = time.AfterFunc(debounce, func() {
				_ = s.CompactWAL()
			})
		}
		s.walMu.Unlock()
		return nil
	}

	if rErr := renameFileWithRetry(tmpPath, s.indexPath); rErr != nil {
		s.mu.Unlock()
		_ = os.Remove(tmpPath)
		return rErr
	}
	_ = os.Remove(s.walPath)

	s.walMu.Lock()
	if s.walTimer != nil {
		s.walTimer.Stop()
		s.walTimer = nil
	}
	s.walOps = 0
	s.walBytes = 0
	s.walCompactions++
	s.walMu.Unlock()
	s.mu.Unlock()
	return nil
}

// Close flushes pending WAL mutations cleanly into files.json during graceful shutdown or upgrade handoff.
func (s *Store) Close() error {
	s.closed.Store(true)

	s.walMu.Lock()
	if s.walTimer != nil {
		s.walTimer.Stop()
		s.walTimer = nil
	}
	pending := s.walOps > 0 || s.walBytes > 0
	s.walMu.Unlock()

	if !pending {
		return nil
	}
	if _, err := os.Stat(s.DataDir()); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFullSnapshotLocked()
}

// SetWALDebounceForTest adjusts the debounce window for unit tests.
func (s *Store) SetWALDebounceForTest(d time.Duration) {
	s.walMu.Lock()
	if d > 0 {
		s.walDebounce = d
	}
	s.walMu.Unlock()
}

// WALStats returns a consistent snapshot of WAL counters.
func (s *Store) WALStats() WALStats {
	s.mu.RLock()
	seq := s.walSeq
	s.mu.RUnlock()

	s.walMu.Lock()
	ops := s.walOps
	bytes := s.walBytes
	comp := s.walCompactions
	s.walMu.Unlock()

	return WALStats{
		Seq:          seq,
		PendingOps:   ops,
		PendingBytes: bytes,
		Compactions:  comp,
	}
}
