package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func intPtr(v int) *int {
	return &v
}

func TestStripedLocksAndOptimisticVersionConcurrency(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 1. 20 concurrent goroutines writing 20 distinct files should all succeed.
	const parallelFiles = 20
	var wg sync.WaitGroup
	errs := make(chan error, parallelFiles)
	for i := 0; i < parallelFiles; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("handler_%02d.go", idx)
			_, wErr := s.SaveUploadWithConcurrency(
				name,
				"shopapp/backend",
				"parallel create",
				"alice",
				nil,
				DeviceInfo{Name: "Agent · coder"},
				strings.NewReader(fmt.Sprintf("package backend // %d", idx)),
				WriteConcurrencyOptions{ExpectedVersion: intPtr(0)},
			)
			if wErr != nil {
				errs <- wErr
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("parallel distinct create failed: %v", e)
	}

	// 2. 10 goroutines racing to create the same file with expected_version=0: only 1 may win.
	const racers = 10
	var createWins atomic.Int32
	var createConflicts atomic.Int32
	var winID string
	var winMu sync.Mutex

	startCh := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			rec, wErr := s.SaveUploadWithConcurrency(
				"shared.go",
				"shopapp/backend",
				fmt.Sprintf("racer-%d", idx),
				"alice",
				nil,
				DeviceInfo{Name: fmt.Sprintf("Agent · r%d", idx)},
				strings.NewReader(fmt.Sprintf("package shared // %d", idx)),
				WriteConcurrencyOptions{ExpectedVersion: intPtr(0)},
			)
			if wErr == nil {
				createWins.Add(1)
				winMu.Lock()
				winID = rec.ID
				winMu.Unlock()
				return
			}
			var cf *ConflictError
			if !errors.As(wErr, &cf) {
				t.Errorf("expected ConflictError on race create, got %T: %v", wErr, wErr)
				return
			}
			createConflicts.Add(1)
		}(i)
	}
	close(startCh)
	wg.Wait()
	if createWins.Load() != 1 || createConflicts.Load() != racers-1 || winID == "" {
		t.Fatalf("expected 1 create win & %d conflicts, got wins=%d conflicts=%d", racers-1, createWins.Load(), createConflicts.Load())
	}

	// 3. 10 goroutines racing to update v1 with expected_version=1: only 1 may win.
	var updWins atomic.Int32
	var updConflicts atomic.Int32
	startUpd := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startUpd
			rec, wErr := s.SaveEditedTextWithConcurrency(
				winID,
				fmt.Sprintf("package shared // edit-%d", idx),
				"concurrent edit",
				"alice",
				DeviceInfo{Name: fmt.Sprintf("Agent · e%d", idx)},
				WriteConcurrencyOptions{ExpectedVersion: intPtr(1)},
			)
			if wErr == nil {
				if rec.Version != 2 {
					t.Errorf("expected version 2 on winning edit, got %d", rec.Version)
				}
				updWins.Add(1)
				return
			}
			var cf *ConflictError
			if !errors.As(wErr, &cf) {
				t.Errorf("expected ConflictError on race edit, got %T: %v", wErr, wErr)
				return
			}
			updConflicts.Add(1)
		}(i)
	}
	close(startUpd)
	wg.Wait()
	if updWins.Load() != 1 || updConflicts.Load() != racers-1 {
		t.Fatalf("expected 1 update win & %d conflicts, got wins=%d conflicts=%d", racers-1, updWins.Load(), updConflicts.Load())
	}
}

func TestSoftLeaseLifecycleAndExpiration(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	rec, err := s.SaveUpload("doc.md", "alice", DeviceInfo{Name: "PC"}, strings.NewReader("v1"))
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}

	lease1, err := s.AcquireLease(rec.ID, "agk_a", "Agent · A", 60*time.Second, false)
	if err != nil {
		t.Fatalf("AcquireLease agk_a: %v", err)
	}

	// Idempotent renewal by same holder.
	leaseRenew, err := s.AcquireLease(rec.ID, "agk_a", "Agent · A", 120*time.Second, false)
	if err != nil || !leaseRenew.AcquiredAt.Equal(lease1.AcquiredAt) || !leaseRenew.ExpiresAt.After(lease1.ExpiresAt) {
		t.Fatalf("expected lease renewal preserving AcquiredAt, got %+v err=%v", leaseRenew, err)
	}

	// Different actor denied lease, edit, and release.
	if _, err := s.AcquireLease(rec.ID, "agk_b", "Agent · B", 60*time.Second, false); err == nil {
		t.Fatal("expected agk_b AcquireLease rejected while agk_a holds lease")
	} else {
		var le *LeaseLockedError
		if !errors.As(err, &le) || le.HolderKeyID != "agk_a" {
			t.Fatalf("expected LeaseLockedError for agk_a, got %T: %v", err, err)
		}
	}
	if _, err := s.SaveEditedTextWithConcurrency(rec.ID, "v2", "try", "bob", DeviceInfo{}, WriteConcurrencyOptions{ActorKeyID: "agk_b"}); err == nil {
		t.Fatal("expected agk_b edit rejected while agk_a holds lease")
	}
	if err := s.ReleaseLease(rec.ID, "agk_b", false); err == nil {
		t.Fatal("expected agk_b release rejected")
	}

	// Holder agk_a can edit cleanly.
	updated, err := s.SaveEditedTextWithConcurrency(rec.ID, "v2-by-a", "holder edit", "alice", DeviceInfo{Name: "Agent · A"}, WriteConcurrencyOptions{ActorKeyID: "agk_a"})
	if err != nil || updated.Version != 2 {
		t.Fatalf("holder edit failed: err=%v rec=%+v", err, updated)
	}

	// Simulate TTL expiration.
	s.leaseMu.Lock()
	expired := s.leases[rec.ID]
	expired.ExpiresAt = time.Now().Add(-time.Second)
	s.leases[rec.ID] = expired
	s.leaseMu.Unlock()

	if _, ok := s.GetLease(rec.ID); ok {
		t.Fatal("expected expired lease cleaned up on GetLease")
	}
	if _, err := s.AcquireLease(rec.ID, "agk_b", "Agent · B", 60*time.Second, false); err != nil {
		t.Fatalf("expected agk_b able to acquire after expiration: %v", err)
	}

	// Admin force release clears agk_b lease.
	if err := s.ReleaseLease(rec.ID, "user:admin", true); err != nil {
		t.Fatalf("force release failed: %v", err)
	}
	if _, ok := s.GetLease(rec.ID); ok {
		t.Fatal("expected lease gone after force release")
	}
}

func TestWritableProjectMountAtomicWriteShadowRestoreAndSecurity(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	projDir := filepath.Join(tmp, "ShopApp")
	outsideDir := filepath.Join(tmp, "outside-secret")

	mustMkdir := func(p string) {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(p, content string) {
		mustMkdir(filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(filepath.Join(projDir, "docs", "spec.md"), "# Spec v1")
	mustWrite(filepath.Join(projDir, "backend", "main.go"), "package main // v1")
	mustWrite(filepath.Join(projDir, ".env"), "DB_PASS=supersecret")
	mustWrite(filepath.Join(projDir, ".env.example"), "DB_PASS=change_me")
	mustWrite(filepath.Join(projDir, "certs", "server.key"), "PRIVATE KEY")
	mustWrite(filepath.Join(projDir, "id_rsa"), "SSH KEY")
	mustWrite(filepath.Join(projDir, "node_modules", "leftpad", "index.js"), "module.exports = {}")
	mustWrite(filepath.Join(projDir, ".git", "config"), "[core]")
	mustWrite(filepath.Join(projDir, "dist", "app.js"), "console.log(1)")
	mustMkdir(outsideDir)

	s, err := NewStore(dataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 1. Default AddMount is read-only: write-through and deletes must fail.
	roMount, _, err := s.AddMount(projDir, "shopapp", "admin", nil)
	if err != nil {
		t.Fatalf("AddMount ro: %v", err)
	}
	if _, err := s.SaveUploadWithConcurrency("new.go", "shopapp/backend", "", "alice", nil, DeviceInfo{}, strings.NewReader("x"), WriteConcurrencyOptions{}); err == nil {
		t.Fatal("expected write into read-only mount rejected")
	}

	// 2. Upgrade to writable workspace (inherits ShadowBackup=true + DefaultProjectIgnorePatterns).
	writableTrue := true
	updatedMount, _, err := s.UpdateMount(roMount.ID, UpdateMountOptions{Writable: &writableTrue})
	if err != nil {
		t.Fatalf("UpdateMount writable: %v", err)
	}
	if !updatedMount.Writable || !updatedMount.ShadowBackup {
		t.Fatalf("expected Writable=true and ShadowBackup=true, got %+v", updatedMount)
	}

	// Sensitive / dependency files must be absent from store records.
	var mainRec FileRecord
	var specRec FileRecord
	foundExample := false
	for _, r := range s.AllRecords() {
		low := strings.ToLower(r.Name)
		if low == ".env" || low == "server.key" || low == "id_rsa" || low == "index.js" || low == "config" || low == "app.js" {
			t.Fatalf("sensitive or ignored file must not be indexed: %+v", r)
		}
		if r.Name == "main.go" && r.FolderPath == "shopapp/backend" {
			mainRec = r
		}
		if r.Name == "spec.md" && r.FolderPath == "shopapp/docs" {
			specRec = r
		}
		if r.Name == ".env.example" {
			foundExample = true
		}
	}
	if mainRec.ID == "" || specRec.ID == "" || !foundExample {
		t.Fatalf("expected main.go, spec.md, and .env.example indexed; got main=%q spec=%q example=%v", mainRec.ID, specRec.ID, foundExample)
	}

	// 3. Atomic write-through into new subdirectory via SaveUploadWithConcurrency.
	subRec, err := s.SaveUploadWithConcurrency(
		"user.go",
		"shopapp/backend/handlers",
		"create user handler",
		"alice",
		nil,
		DeviceInfo{Name: "Agent · backend-bot"},
		strings.NewReader("package handlers // v1"),
		WriteConcurrencyOptions{ExpectedVersion: intPtr(0), ActorKeyID: "agk_backend"},
	)
	if err != nil {
		t.Fatalf("SaveUploadWithConcurrency new mount subdir: %v", err)
	}
	if subRec.StorageMode != StorageModeMount || subRec.Version != 1 {
		t.Fatalf("unexpected subRec: %+v", subRec)
	}
	hostSubBytes, err := os.ReadFile(filepath.Join(projDir, "backend", "handlers", "user.go"))
	if err != nil || string(hostSubBytes) != "package handlers // v1" {
		t.Fatalf("host file mismatch: err=%v data=%q", err, string(hostSubBytes))
	}
	entries, _ := os.ReadDir(filepath.Join(projDir, "backend", "handlers"))
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".lfh_tmp_") {
			t.Fatalf("temp file leaked after atomic write: %s", ent.Name())
		}
	}

	// 4. Shadow backup + optimistic locking across multiple edits.
	v2Rec, err := s.SaveEditedTextWithConcurrency(mainRec.ID, "package main // v2", "add auth", "alice", DeviceInfo{Name: "Agent · backend-bot"}, WriteConcurrencyOptions{ExpectedVersion: intPtr(1)})
	if err != nil || v2Rec.Version != 2 || len(v2Rec.Versions) != 1 {
		t.Fatalf("edit v1->v2 failed: err=%v rec=%+v", err, v2Rec)
	}
	v3Rec, err := s.SaveEditedTextWithConcurrency(mainRec.ID, "package main // v3", "add metrics", "alice", DeviceInfo{Name: "Agent · backend-bot"}, WriteConcurrencyOptions{ExpectedVersion: intPtr(2)})
	if err != nil || v3Rec.Version != 3 || len(v3Rec.Versions) != 2 {
		t.Fatalf("edit v2->v3 failed: err=%v rec=%+v", err, v3Rec)
	}

	// Stale expected_version=1 must return ConflictError and keep host file at v3.
	if _, err := s.SaveEditedTextWithConcurrency(mainRec.ID, "stale overwrite", "", "alice", DeviceInfo{}, WriteConcurrencyOptions{ExpectedVersion: intPtr(1)}); err == nil {
		t.Fatal("expected ConflictError on stale expected_version=1")
	} else {
		var cf *ConflictError
		if !errors.As(err, &cf) || cf.CurrentVersion != 3 {
			t.Fatalf("expected ConflictError(CurrentVersion=3), got %T: %v", err, err)
		}
	}
	diskMain, _ := os.ReadFile(filepath.Join(projDir, "backend", "main.go"))
	if string(diskMain) != "package main // v3" {
		t.Fatalf("host disk modified on failed conflict: %q", string(diskMain))
	}

	// 5. Rollback to v1 writes Shadow backup back to host disk atomically.
	restored, err := s.RestoreVersionWithConcurrency(mainRec.ID, 1, "rollback to clean v1", "admin", DeviceInfo{Name: "Desktop"}, WriteConcurrencyOptions{ExpectedVersion: intPtr(3)})
	if err != nil || restored.Version != 4 {
		t.Fatalf("RestoreVersionWithConcurrency failed: err=%v rec=%+v", err, restored)
	}
	diskMain, _ = os.ReadFile(filepath.Join(projDir, "backend", "main.go"))
	if string(diskMain) != "package main // v1" {
		t.Fatalf("expected host disk restored to v1, got %q", string(diskMain))
	}

	// 6. External editor mutation on host triggers SyncMount version bump (v4 -> v5).
	if err := os.WriteFile(filepath.Join(projDir, "backend", "main.go"), []byte("package main // edited locally on host A"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncMount(roMount.ID); err != nil {
		t.Fatalf("SyncMount after external edit: %v", err)
	}
	if _, err := s.SaveEditedTextWithConcurrency(mainRec.ID, "stale after host edit", "", "alice", DeviceInfo{}, WriteConcurrencyOptions{ExpectedVersion: intPtr(4)}); err == nil {
		t.Fatal("expected ConflictError after external host change bumped version")
	} else {
		var cf *ConflictError
		if !errors.As(err, &cf) || cf.CurrentVersion != 5 {
			t.Fatalf("expected ConflictError(CurrentVersion=5), got %T: %v", err, err)
		}
	}

	// 7. Hardcoded security blacklist and ignore paths must block writes cleanly.
	blockedPaths := []string{
		".env",
		"backend/.env.local",
		"certs/server.key",
		"tls/ca.pem",
		".git/config",
		".ssh/authorized_keys",
		"id_rsa",
		"../outside.go",
		"node_modules/leftpad/evil.js",
	}
	for _, bp := range blockedPaths {
		if _, wErr := s.WriteMountFile(roMount.ID, bp, "", "", "alice", nil, DeviceInfo{}, strings.NewReader("evil"), WriteConcurrencyOptions{}); wErr == nil {
			t.Fatalf("expected blocked path %q to be rejected", bp)
		}
	}
	if envBytes, _ := os.ReadFile(filepath.Join(projDir, ".env")); string(envBytes) != "DB_PASS=supersecret" {
		t.Fatalf(".env on disk was altered!")
	}

	// 8. Symlink escape protection (best-effort if host OS allows creating symlinks).
	symPath := filepath.Join(projDir, "escape-link")
	if symErr := os.Symlink(outsideDir, symPath); symErr == nil {
		if _, wErr := s.WriteMountFile(roMount.ID, "escape-link/pwn.go", "", "", "alice", nil, DeviceInfo{}, strings.NewReader("pwn"), WriteConcurrencyOptions{}); wErr == nil {
			t.Fatal("expected symlink escape write rejected")
		}
		if _, stErr := os.Stat(filepath.Join(outsideDir, "pwn.go")); !os.IsNotExist(stErr) {
			t.Fatalf("symlink escape wrote outside mount dir!")
		}
	} else {
		t.Logf("skipping OS symlink creation test (insufficient privilege): %v", symErr)
	}

	// 9. DeleteWithConcurrency in writable mount removes host file and Shadow blobs.
	_, _, shadowV1Path, hasShadow := s.GetVersion(mainRec.ID, 1)
	if !hasShadow {
		t.Fatal("expected v1 shadow path present")
	}
	if err := s.DeleteWithConcurrency(mainRec.ID, WriteConcurrencyOptions{ExpectedVersion: intPtr(5)}); err != nil {
		t.Fatalf("DeleteWithConcurrency failed: %v", err)
	}
	if _, stErr := os.Stat(filepath.Join(projDir, "backend", "main.go")); !os.IsNotExist(stErr) {
		t.Fatalf("expected deleted host file removed")
	}
	if _, stErr := os.Stat(shadowV1Path); !os.IsNotExist(stErr) {
		t.Fatalf("expected deleted file's shadow blob cleaned up")
	}

	// 10. RemoveMount cleans remaining index/Shadow blobs without deleting surviving host files.
	if _, err := s.RemoveMount(roMount.ID); err != nil {
		t.Fatalf("RemoveMount: %v", err)
	}
	if specBytes, err := os.ReadFile(filepath.Join(projDir, "docs", "spec.md")); err != nil || string(specBytes) != "# Spec v1" {
		t.Fatalf("RemoveMount must preserve host source files: err=%v data=%q", err, string(specBytes))
	}
}
