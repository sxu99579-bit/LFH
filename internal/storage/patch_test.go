package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestApplyPatchContentSearchReplaceAndCRLF(t *testing.T) {
	crlfInput := "package main\r\n\r\nfunc greet() string {\r\n\treturn \"hello\"\r\n}\r\n"
	out, err := ApplyPatchContent(crlfInput, PatchRequest{
		Mode: "search_replace",
		Hunks: []PatchHunk{
			{
				OldText: "func greet() string {\n\treturn \"hello\"\n}",
				NewText: "func greet() string {\n\treturn \"world\"\n}",
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyPatchContent failed: %v", err)
	}
	want := "package main\r\n\r\nfunc greet() string {\r\n\treturn \"world\"\r\n}\r\n"
	if out != want {
		t.Fatalf("CRLF preservation failed:\ngot:  %q\nwant: %q", out, want)
	}

	// Verify replace_all vs ambiguous_match.
	lfDup := "a = 1\nb = 1\nc = 1\n"
	_, ambErr := ApplyPatchContent(lfDup, PatchRequest{
		Hunks: []PatchHunk{{OldText: "= 1", NewText: "= 2"}},
	})
	var pErr *PatchError
	if !errors.As(ambErr, &pErr) || pErr.Reason != "ambiguous_match" || pErr.MatchCount != 3 {
		t.Fatalf("expected ambiguous_match count=3, got %#v (err=%v)", pErr, ambErr)
	}

	allOut, err := ApplyPatchContent(lfDup, PatchRequest{
		Hunks: []PatchHunk{{OldText: "= 1", NewText: "= 2", ReplaceAll: true}},
	})
	if err != nil {
		t.Fatalf("replace_all failed: %v", err)
	}
	if allOut != "a = 2\nb = 2\nc = 2\n" || strings.Contains(allOut, "\r\n") {
		t.Fatalf("unexpected LF output: %q", allOut)
	}

	// Verify not_found error.
	_, nfErr := ApplyPatchContent(lfDup, PatchRequest{
		Hunks: []PatchHunk{{OldText: "missing_symbol", NewText: "x"}},
	})
	if !errors.As(nfErr, &pErr) || pErr.Reason != "not_found" {
		t.Fatalf("expected not_found error, got %#v (err=%v)", pErr, nfErr)
	}
}

func TestApplyPatchContentUnifiedDiff(t *testing.T) {
	orig := strings.Join([]string{
		"line1",
		"line2",
		"line3",
		"line4",
		"line5",
		"line6",
		"line7",
		"line8",
		"line9",
	}, "\n") + "\n"

	// Intentionally shift hunk header line numbers slightly to verify unique offset tolerance,
	// and apply two hunks in one unified diff.
	diff := strings.Join([]string{
		"--- a/sample.txt",
		"+++ b/sample.txt",
		"@@ -2,3 +2,4 @@",
		" line2",
		"-line3",
		"+line3_updated",
		"+line3_extra",
		" line4",
		"@@ -8,2 +9,2 @@",
		" line8",
		"-line9",
		"+line9_final",
	}, "\n") + "\n"

	patched, err := ApplyPatchContent(orig, PatchRequest{
		Mode:        "unified_diff",
		UnifiedDiff: diff,
	})
	if err != nil {
		t.Fatalf("ApplyPatchContent unified_diff failed: %v", err)
	}
	want := strings.Join([]string{
		"line1",
		"line2",
		"line3_updated",
		"line3_extra",
		"line4",
		"line5",
		"line6",
		"line7",
		"line8",
		"line9_final",
	}, "\n") + "\n"
	if patched != want {
		t.Fatalf("unified_diff mismatch:\ngot:\n%s\nwant:\n%s", patched, want)
	}

	// Shifted context offset match (+2 lines offset) still applies uniquely.
	shiftedOrig := "hdrA\nhdrB\n" + orig
	shiftedDiff := "@@ -2,3 +2,3 @@\n line2\n-line3\n+line3_shifted\n line4\n"
	shiftedOut, err := ApplyPatchContent(shiftedOrig, PatchRequest{
		Mode:        "unified_diff",
		UnifiedDiff: shiftedDiff,
	})
	if err != nil {
		t.Fatalf("expected shifted offset unified_diff to succeed: %v", err)
	}
	if !strings.Contains(shiftedOut, "line3_shifted") {
		t.Fatalf("expected shifted replacement in output: %s", shiftedOut)
	}

	// Mismatched context fails cleanly.
	_, badErr := ApplyPatchContent(orig, PatchRequest{
		Mode:        "unified_diff",
		UnifiedDiff: "@@ -1,2 +1,2 @@\n does_not_exist\n-line2\n+x\n",
	})
	var pErr *PatchError
	if !errors.As(badErr, &pErr) || pErr.Reason != "context_mismatch" {
		t.Fatalf("expected context_mismatch, got %#v (err=%v)", pErr, badErr)
	}

	// Empty file without trailing newline -> insert lines cleanly.
	created, err := ApplyPatchContent("", PatchRequest{
		Mode:        "unified_diff",
		UnifiedDiff: "@@ -0,0 +1,2 @@\n+first\n+second\n",
	})
	if err != nil || created != "first\nsecond\n" {
		t.Fatalf("empty file unified_diff unexpected: err=%v out=%q", err, created)
	}
}

func TestPatchFileAndBatchCommitAtomicityAndConcurrency(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	projDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(projDir, "backend"), 0755)
	_ = os.WriteFile(filepath.Join(projDir, "backend", "handler.go"), []byte("package backend\n\nconst Status = \"alpha\"\n"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, "backend", "models.go"), []byte("package backend\n\ntype User struct { ID int }\n"), 0644)
	_ = os.WriteFile(filepath.Join(projDir, ".env"), []byte("DB_SECRET=topsecret"), 0644)

	shadowOn := true
	mount, _, err := s.AddMountWithOptions(projDir, "shopapp", "alice", nil, AddMountOptions{
		Writable:     true,
		ShadowBackup: &shadowOn,
	})
	if err != nil {
		t.Fatalf("AddMountWithOptions: %v", err)
	}

	handlerRec, ok := s.FindByLogicalPath("shopapp/backend", "handler.go")
	if !ok {
		t.Fatal("expected handler.go indexed from mount")
	}

	// 1. Patch writable mount file -> Shadow backup + atomic host write + version bump.
	dev := DeviceInfo{Name: "Agent · backend-bot"}
	patchedRec, err := s.PatchFileWithConcurrency(handlerRec.ID, PatchRequest{
		Mode:            "search_replace",
		Hunks:           []PatchHunk{{OldText: `const Status = "alpha"`, NewText: `const Status = "beta"`}},
		ExpectedVersion: intPtr(1),
		Note:            "bump status",
	}, MethodAgentPatch, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"})
	if err != nil {
		t.Fatalf("PatchFileWithConcurrency failed: %v", err)
	}
	if patchedRec.Version != 2 || len(patchedRec.Versions) != 1 {
		t.Fatalf("expected v2 with 1 shadow backup, got v%d hist=%d", patchedRec.Version, len(patchedRec.Versions))
	}
	diskHandler, _ := os.ReadFile(filepath.Join(projDir, "backend", "handler.go"))
	if !strings.Contains(string(diskHandler), `"beta"`) {
		t.Fatalf("host file not written through: %s", string(diskHandler))
	}

	// Stale expected_version returns *ConflictError.
	_, conflictErr := s.PatchFileWithConcurrency(handlerRec.ID, PatchRequest{
		Hunks:           []PatchHunk{{OldText: `"beta"`, NewText: `"gamma"`}},
		ExpectedVersion: intPtr(1),
	}, MethodAgentPatch, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"})
	var cErr *ConflictError
	if !errors.As(conflictErr, &cErr) || cErr.CurrentVersion != 2 {
		t.Fatalf("expected ConflictError current=2, got %v", conflictErr)
	}

	// Active lease by agk_other blocks patch with *LeaseLockedError.
	if _, err := s.AcquireLease(handlerRec.ID, "agk_other", "Agent · other", 0, false); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	_, leaseErr := s.PatchFileWithConcurrency(handlerRec.ID, PatchRequest{
		Hunks: []PatchHunk{{OldText: `"beta"`, NewText: `"blocked"`}},
	}, MethodAgentPatch, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"})
	var lErr *LeaseLockedError
	if !errors.As(leaseErr, &lErr) || lErr.HolderKeyID != "agk_other" {
		t.Fatalf("expected LeaseLockedError for agk_other, got %v", leaseErr)
	}
	_ = s.ReleaseLease(handlerRec.ID, "agk_other", false)

	// Restore v1 reverts host file cleanly!
	restored, err := s.RestoreVersionWithConcurrency(handlerRec.ID, 1, "rollback beta", "alice", dev, WriteConcurrencyOptions{ExpectedVersion: intPtr(2)})
	if err != nil {
		t.Fatalf("RestoreVersionWithConcurrency: %v", err)
	}
	if restored.Version != 3 {
		t.Fatalf("expected restored v3, got v%d", restored.Version)
	}
	diskAfterRestore, _ := os.ReadFile(filepath.Join(projDir, "backend", "handler.go"))
	if !strings.Contains(string(diskAfterRestore), `"alpha"`) {
		t.Fatalf("expected host file restored to alpha, got %s", string(diskAfterRestore))
	}

	// Binary upload rejects PatchFileWithConcurrency with binary_file.
	binRec, err := s.SaveUploadWithConcurrency("blob.dat", "uploads", "bin", "alice", nil, dev, strings.NewReader("abc\x00def"), WriteConcurrencyOptions{})
	if err != nil {
		t.Fatalf("SaveUploadWithConcurrency binary: %v", err)
	}
	_, binErr := s.PatchFileWithConcurrency(binRec.ID, PatchRequest{
		Hunks: []PatchHunk{{OldText: "abc", NewText: "xyz"}},
	}, MethodPatch, "alice", dev, WriteConcurrencyOptions{})
	var pErr *PatchError
	if !errors.As(binErr, &pErr) || pErr.Reason != "binary_file" {
		t.Fatalf("expected binary_file PatchError, got %#v (err=%v)", pErr, binErr)
	}

	// 2. Atomic batch_commit rollback: op 0 is valid, op 1 targets .env (blocked) -> zero side effects!
	modelsRec, _ := s.FindByLogicalPath("shopapp/backend", "models.go")
	_, atomicFailErr := s.BatchCommitWithConcurrency(BatchCommitRequest{
		Note: "should rollback completely",
		Operations: []BatchOp{
			{
				Action:          "patch",
				Path:            "shopapp/backend/handler.go",
				Hunks:           []PatchHunk{{OldText: `"alpha"`, NewText: `"NEVER_COMMITTED"`}},
				ExpectedVersion: intPtr(3),
			},
			{
				Action:  "write",
				Path:    "shopapp/.env",
				Content: "PWNED=1",
			},
		},
	}, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"}, BatchPolicyHooks{})
	if atomicFailErr == nil {
		t.Fatal("expected batch_commit touching .env to fail")
	}
	diskHandlerAfterFail, _ := os.ReadFile(filepath.Join(projDir, "backend", "handler.go"))
	if strings.Contains(string(diskHandlerAfterFail), "NEVER_COMMITTED") {
		t.Fatalf("op 0 mutated disk despite op 1 failure!")
	}
	checkHandler, _ := s.FindByLogicalPath("shopapp/backend", "handler.go")
	if checkHandler.Version != 3 {
		t.Fatalf("expected handler.go version unchanged at 3, got %d", checkHandler.Version)
	}

	// Also fail batch_commit on stale expected_version in op 1 after valid new file op 0 -> new file must not be created.
	_, staleBatchErr := s.BatchCommitWithConcurrency(BatchCommitRequest{
		Note: "stale op1",
		Operations: []BatchOp{
			{Action: "write", Path: "shopapp/backend/ghost.go", Content: "package backend\n", ExpectedVersion: intPtr(0)},
			{Action: "patch", Path: "shopapp/backend/models.go", Hunks: []PatchHunk{{OldText: "ID int", NewText: "ID int64"}}, ExpectedVersion: intPtr(99)},
		},
	}, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"}, BatchPolicyHooks{})
	if !errors.As(staleBatchErr, &cErr) {
		t.Fatalf("expected ConflictError on stale batch, got %v", staleBatchErr)
	}
	if _, stErr := os.Stat(filepath.Join(projDir, "backend", "ghost.go")); !os.IsNotExist(stErr) {
		t.Fatalf("ghost.go should not exist after failed batch dry-run!")
	}

	// 3. Valid multi-file atomic batch_commit across patch + write.
	batchRes, err := s.BatchCommitWithConcurrency(BatchCommitRequest{
		Note: "coord change",
		Operations: []BatchOp{
			{
				Action:          "patch",
				Path:            "shopapp/backend/handler.go",
				Hunks:           []PatchHunk{{OldText: `"alpha"`, NewText: `"gamma"`}},
				ExpectedVersion: intPtr(3),
			},
			{
				Action:          "patch",
				Path:            "shopapp/backend/models.go",
				Hunks:           []PatchHunk{{OldText: "ID int", NewText: "ID int64"}},
				ExpectedVersion: intPtr(modelsRec.Version),
			},
			{
				Action:          "write",
				Path:            "shopapp/backend/new_service.go",
				Content:         "package backend\n\nfunc Service() {}\n",
				ExpectedVersion: intPtr(0),
			},
		},
	}, "alice", dev, WriteConcurrencyOptions{ActorKeyID: "agk_b"}, BatchPolicyHooks{})
	if err != nil {
		t.Fatalf("valid BatchCommitWithConcurrency failed: %v", err)
	}
	if batchRes.BatchID == "" || len(batchRes.Results) != 3 {
		t.Fatalf("unexpected batch result: %+v", batchRes)
	}
	if b, _ := os.ReadFile(filepath.Join(projDir, "backend", "new_service.go")); !strings.Contains(string(b), "func Service()") {
		t.Fatalf("new_service.go not written cleanly: %s", string(b))
	}

	// 4. High-concurrency race/deadlock stress across 20 goroutines writing/patching independent & overlapping files.
	_ = mount
	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p1 := fmt.Sprintf("shopapp/backend/worker_%02d.go", idx)
			p2 := fmt.Sprintf("shopapp/backend/worker_%02d_b.go", idx)
			if _, bErr := s.BatchCommitWithConcurrency(BatchCommitRequest{
				Note: "worker init",
				Operations: []BatchOp{
					{Action: "write", Path: p1, Content: fmt.Sprintf("package backend\nvar W%d = 1\n", idx), ExpectedVersion: intPtr(0)},
					{Action: "write", Path: p2, Content: fmt.Sprintf("package backend\nvar X%d = 1\n", idx), ExpectedVersion: intPtr(0)},
				},
			}, "alice", dev, WriteConcurrencyOptions{ActorKeyID: fmt.Sprintf("agk_%d", idx)}, BatchPolicyHooks{}); bErr != nil {
				errCh <- bErr
				return
			}
			// Now patch in reverse order concurrently with expected_version=1 -> verifies lock slot sorting prevents deadlocks.
			if _, pErr := s.BatchCommitWithConcurrency(BatchCommitRequest{
				Note: "worker patch",
				Operations: []BatchOp{
					{Action: "patch", Path: p2, Hunks: []PatchHunk{{OldText: "= 1", NewText: "= 2"}}, ExpectedVersion: intPtr(1)},
					{Action: "patch", Path: p1, Hunks: []PatchHunk{{OldText: "= 1", NewText: "= 2"}}, ExpectedVersion: intPtr(1)},
				},
			}, "alice", dev, WriteConcurrencyOptions{ActorKeyID: fmt.Sprintf("agk_%d", idx)}, BatchPolicyHooks{}); pErr != nil {
				errCh <- pErr
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrent batch stress failed: %v", e)
	}
}
