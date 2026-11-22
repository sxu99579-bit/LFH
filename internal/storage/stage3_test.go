package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistentSHA256AndIntegrityScrub(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("persistent sha256 verification payload 2026")
	sum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(sum[:])

	rec, err := s.SaveUpload("verified.txt", "admin", DeviceInfo{Name: "PC"}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("SaveUpload failed: %v", err)
	}
	if rec.SHA256 != wantHex || rec.IntegrityStatus != "ok" {
		t.Fatalf("unexpected persisted sha/status: sha=%s want=%s status=%s", rec.SHA256, wantHex, rec.IntegrityStatus)
	}

	matchRec, matchPath, found := s.FindBySHA256(wantHex)
	if !found || matchRec.ID != rec.ID || matchPath == "" {
		t.Fatalf("FindBySHA256 failed: found=%v rec=%+v path=%s", found, matchRec, matchPath)
	}

	// Healthy scrub passes cleanly.
	okRes := s.ScrubIntegrity(0)
	if okRes.Checked != 1 || okRes.OK != 1 || okRes.Corrupted != 0 {
		t.Fatalf("expected healthy scrub, got %+v", okRes)
	}

	// Simulate bit-rot corruption on underlying blob without updating files.json.
	corrupted := []byte("corrupted sha256 verification payload 2026")
	if err := os.WriteFile(matchPath, corrupted, 0644); err != nil {
		t.Fatal(err)
	}

	badRes := s.ScrubIntegrity(0)
	if badRes.Checked != 1 || badRes.Corrupted != 1 || len(badRes.CorruptedIDs) != 1 || badRes.CorruptedIDs[0] != rec.ID {
		t.Fatalf("expected bit-rot detection, got %+v", badRes)
	}
	updated, _, ok := s.Get(rec.ID)
	if !ok || updated.IntegrityStatus != "corrupted" {
		t.Fatalf("expected IntegrityStatus=corrupted, got ok=%v rec=%+v", ok, updated)
	}
	rErr, _ := s.IOStats()
	if rErr < 1 {
		t.Fatalf("expected readErrors incremented on corruption, got %d", rErr)
	}
	if _, _, stillFound := s.FindBySHA256(wantHex); stillFound {
		t.Fatal("corrupted file must not be returned by FindBySHA256")
	}
}

func TestChunkedUploadResumeVerifyAssembleAndGC(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	chunk0 := bytes.Repeat([]byte("A"), 1024)
	chunk1 := bytes.Repeat([]byte("B"), 1024)
	chunk2 := []byte("TAIL-CHUNK-2")
	full := append(append([]byte(nil), chunk0...), chunk1...)
	full = append(full, chunk2...)
	fullSum := sha256.Sum256(full)
	fullHex := hex.EncodeToString(fullSum[:])
	c1Sum := sha256.Sum256(chunk1)
	c1Hex := hex.EncodeToString(c1Sum[:])

	sess, resumed, err := s.InitChunkUpload(ChunkUploadInit{
		Name:       "big.bin",
		FolderPath: "archives",
		FileSize:   int64(len(full)),
		ChunkSize:  1024,
		FileSHA256: fullHex,
		Uploader:   "alice",
	})
	if err != nil {
		t.Fatalf("InitChunkUpload: %v", err)
	}
	if resumed || sess.TotalChunks != 3 {
		t.Fatalf("expected new 3-chunk session, got resumed=%v total=%d", resumed, sess.TotalChunks)
	}

	// Out-of-order upload: chunk 2 first, then reject bad SHA on chunk 1, then valid chunk 1.
	if _, err := s.SaveUploadChunk(sess.UploadID, 2, "", bytes.NewReader(chunk2)); err != nil {
		t.Fatalf("SaveUploadChunk(2): %v", err)
	}
	if _, err := s.SaveUploadChunk(sess.UploadID, 1, strings.Repeat("0", 64), bytes.NewReader(chunk1)); err == nil {
		t.Fatal("expected SaveUploadChunk to reject mismatched chunk sha256")
	}
	if _, err := s.SaveUploadChunk(sess.UploadID, 1, c1Hex, bytes.NewReader(chunk1)); err != nil {
		t.Fatalf("SaveUploadChunk(1): %v", err)
	}

	// Re-init with same fingerprint resumes existing session with chunks [1, 2].
	sess2, resumed2, err := s.InitChunkUpload(ChunkUploadInit{
		Name:       "big.bin",
		FolderPath: "archives",
		FileSize:   int64(len(full)),
		ChunkSize:  1024,
		FileSHA256: fullHex,
		Uploader:   "alice",
	})
	if err != nil {
		t.Fatalf("resume InitChunkUpload: %v", err)
	}
	if !resumed2 || sess2.UploadID != sess.UploadID || len(sess2.UploadedChunks) != 2 {
		t.Fatalf("expected resumed session with 2 chunks, got resumed=%v id=%s chunks=%v", resumed2, sess2.UploadID, sess2.UploadedChunks)
	}

	// Upload remaining chunk 0 and assemble.
	if _, err := s.SaveUploadChunk(sess.UploadID, 0, "", bytes.NewReader(chunk0)); err != nil {
		t.Fatalf("SaveUploadChunk(0): %v", err)
	}
	_, mergedPath, gotSHA, err := s.AssembleChunkUpload(sess.UploadID)
	if err != nil {
		t.Fatalf("AssembleChunkUpload: %v", err)
	}
	if gotSHA != fullHex {
		t.Fatalf("assembled SHA mismatch: got %s want %s", gotSHA, fullHex)
	}
	mergedData, err := os.ReadFile(mergedPath)
	if err != nil || !bytes.Equal(mergedData, full) {
		t.Fatalf("assembled file bytes mismatch (err=%v len=%d want=%d)", err, len(mergedData), len(full))
	}
	s.CleanupUploadSession(sess.UploadID)
	if _, err := s.GetUploadSession(sess.UploadID); err == nil {
		t.Fatal("expected session removed after CleanupUploadSession")
	}

	// Test GC cleans stale sessions while preserving fresh ones.
	staleSess, _, err := s.InitChunkUpload(ChunkUploadInit{Name: "stale.bin", FileSize: 10, ChunkSize: 1024, Uploader: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	staleSess.UpdatedAt = time.Now().Add(-48 * time.Hour)
	_ = s.saveUploadMeta(filepath.Join(s.uploadsTmpDir, staleSess.UploadID), staleSess)
	removed := s.GCUploadSessions(24 * time.Hour)
	if removed != 1 {
		t.Fatalf("expected GC to remove 1 stale session, got %d", removed)
	}
}
