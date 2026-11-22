package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportLocalHardlinkSurvivesSourceDeleteAndQuotaZeroCopy(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	srcDir := filepath.Join(tmp, "local-src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(srcDir, "hello.txt")
	content := "hello hardlink zero copy storage!"
	if err := os.WriteFile(srcFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	recs, summary, err := s.ImportLocalPath(srcFile, "我的资料", "admin", nil, DeviceInfo{Name: "PC"})
	if err != nil {
		t.Fatalf("ImportLocalPath failed: %v", err)
	}
	if len(recs) != 1 || summary.TotalImported != 1 {
		t.Fatalf("expected 1 imported record, got recs=%d summary=%+v", len(recs), summary)
	}
	rec := recs[0]
	if rec.StorageMode != StorageModeHardlink {
		t.Fatalf("expected StorageModeHardlink on same temp volume, got %q", rec.StorageMode)
	}

	// Copy quota bytes must remain 0, while ZeroCopyStats counts the saved bytes.
	cnt, copyBytes := s.LocalStats()
	if cnt != 1 || copyBytes != 0 {
		t.Fatalf("expected count=1 copyBytes=0, got count=%d copyBytes=%d", cnt, copyBytes)
	}
	hlCnt, refCnt, mntCnt, savedBytes := s.ZeroCopyStats()
	if hlCnt != 1 || refCnt != 0 || mntCnt != 0 || savedBytes != int64(len(content)) {
		t.Fatalf("unexpected ZeroCopyStats: hl=%d ref=%d mnt=%d saved=%d", hlCnt, refCnt, mntCnt, savedBytes)
	}

	// Deleting the original file outside LFH must NOT break reading the hardlinked blob.
	if err := os.Remove(srcFile); err != nil {
		t.Fatal(err)
	}
	_, diskPath, ok := s.Get(rec.ID)
	if !ok {
		t.Fatal("record disappeared after original deletion")
	}
	raw, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("reading hardlinked blob after source delete failed: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("unexpected hardlinked content %q", string(raw))
	}
}

func TestExternalRefMissingDetectionAndNonDestructiveDelete(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	extDir := filepath.Join(tmp, "external")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	extFile := filepath.Join(extDir, "cross.bin")
	if err := os.WriteFile(extFile, []byte("12345678"), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	s.mu.Lock()
	s.records["ref1"] = FileRecord{
		ID:           "ref1",
		Name:         "cross.bin",
		Size:         8,
		Ext:          ".bin",
		Category:     "other",
		Uploader:     "admin",
		UploadedAt:   now,
		MonthKey:     now.Format("2006-01"),
		DateKey:      now.Format("2006-01-02"),
		StorageMode:  StorageModeRef,
		SourcePath:   extFile,
		Version:      1,
		VersionCount: 1,
		UpdateMethod: MethodLocalImport,
	}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	if changed := s.RefreshRefStatus(); changed {
		t.Fatal("expected no change while ref target exists")
	}
	if err := os.Remove(extFile); err != nil {
		t.Fatal(err)
	}
	if changed := s.RefreshRefStatus(); !changed {
		t.Fatal("expected RefreshRefStatus to detect missing source")
	}
	got, _, ok := s.Get("ref1")
	if !ok || !got.SourceMissing {
		t.Fatalf("expected SourceMissing=true, got ok=%v rec=%+v", ok, got)
	}

	// Recreate source file and confirm Delete only removes the index entry, never the source file.
	if err := os.WriteFile(extFile, []byte("1234567890"), 0644); err != nil {
		t.Fatal(err)
	}
	if changed := s.RefreshRefStatus(); !changed {
		t.Fatal("expected RefreshRefStatus to detect recovered ref size/status")
	}
	got, diskPath, ok := s.Get("ref1")
	if !ok || got.SourceMissing || got.Size != 10 || diskPath != extFile {
		t.Fatalf("unexpected recovered ref state: %+v path=%s", got, diskPath)
	}
	if err := s.Delete("ref1"); err != nil {
		t.Fatalf("Delete(ref) failed: %v", err)
	}
	if _, err := os.Stat(extFile); err != nil {
		t.Fatalf("Delete(ref) should never delete external source file: %v", err)
	}
}

func TestVirtualDirectoryMountLifecycle(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	mountRoot := filepath.Join(tmp, "shared-media")
	subDir := filepath.Join(mountRoot, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	fileA := filepath.Join(mountRoot, "a.txt")
	fileB := filepath.Join(subDir, "b.md")
	if err := os.WriteFile(fileA, []byte("alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("# beta"), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	entry, added, err := s.AddMount(mountRoot, "影音库", "admin", nil)
	if err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	if added != 2 || entry.FileCount != 2 || entry.Status != "ok" {
		t.Fatalf("unexpected mount add result: added=%d entry=%+v", added, entry)
	}

	recs := s.AllRecords()
	if len(recs) != 2 {
		t.Fatalf("expected 2 mounted records, got %d", len(recs))
	}
	if err := s.Delete(recs[0].ID); err == nil {
		t.Fatal("expected single-file delete on mounted file to fail")
	}

	// Modify fileA, add fileC, remove fileB, then run SyncAllMounts.
	if err := os.WriteFile(fileA, []byte("alpha-updated"), 0644); err != nil {
		t.Fatal(err)
	}
	fileC := filepath.Join(mountRoot, "c.txt")
	if err := os.WriteFile(fileC, []byte("gamma"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fileB); err != nil {
		t.Fatal(err)
	}

	changed, addN, updN, remN := s.SyncAllMounts()
	if !changed || addN != 1 || updN != 1 || remN != 1 {
		t.Fatalf("unexpected sync diff: changed=%v add=%d upd=%d rem=%d", changed, addN, updN, remN)
	}
	if cnt, copyBytes := s.LocalStats(); cnt != 2 || copyBytes != 0 {
		t.Fatalf("expected 2 mounted records with 0 copy bytes, got cnt=%d copyBytes=%d", cnt, copyBytes)
	}

	// Removing mount cleans LFH index without touching underlying files on disk.
	removedCount, err := s.RemoveMount(entry.ID)
	if err != nil || removedCount != 2 {
		t.Fatalf("RemoveMount failed: removed=%d err=%v", removedCount, err)
	}
	if len(s.AllRecords()) != 0 || len(s.ListMounts()) != 0 {
		t.Fatalf("expected 0 records and 0 mounts after RemoveMount")
	}
	if _, err := os.Stat(fileA); err != nil {
		t.Fatalf("RemoveMount deleted original file: %v", err)
	}
}
