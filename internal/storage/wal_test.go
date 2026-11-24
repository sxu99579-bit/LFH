package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWALAppendReplayCompactionAndTruncatedTailRecovery(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	baseCompactions := st.WALStats().Compactions
	// Hold automatic debounce open so mutations accumulate in files.wal.
	st.SetWALDebounceForTest(time.Hour)

	recA, err := st.SaveUploadWithConcurrency("a.txt", "proj", "init", "alice", nil, DeviceInfo{Name: "PC"}, strings.NewReader("alpha\nbeta\n"), WriteConcurrencyOptions{})
	if err != nil {
		t.Fatalf("SaveUploadWithConcurrency a.txt: %v", err)
	}
	_, err = st.PatchFileWithConcurrency(recA.ID, PatchRequest{
		Mode: "search_replace",
		Hunks: []PatchHunk{
			{OldText: "beta", NewText: "beta-patched"},
		},
		Note: "patch-1",
	}, "patch", "alice", DeviceInfo{Name: "PC"}, WriteConcurrencyOptions{})
	if err != nil {
		t.Fatalf("PatchFileWithConcurrency a.txt: %v", err)
	}

	batchRes, err := st.BatchCommitWithConcurrency(BatchCommitRequest{
		Note: "batch-wal",
		Operations: []BatchOp{
			{Action: "write", Path: "proj/b.txt", Content: "created-in-batch"},
			{Action: "delete", Path: "proj/a.txt"},
		},
	}, "alice", DeviceInfo{Name: "PC"}, WriteConcurrencyOptions{}, BatchPolicyHooks{})
	if err != nil {
		t.Fatalf("BatchCommitWithConcurrency: %v", err)
	}
	if len(batchRes.Results) != 2 {
		t.Fatalf("unexpected batch results len: %d", len(batchRes.Results))
	}

	mountDir := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddMountWithOptions(mountDir, "ws", "admin", nil, AddMountOptions{
		Writable: true,
		VerifyPresets: []VerifyPreset{
			{ID: "go-ver", Label: "Go Version", Command: "go", Args: []string{"version"}, TimeoutSec: 15},
		},
	}); err != nil {
		t.Fatalf("AddMountWithOptions: %v", err)
	}

	stats := st.WALStats()
	if stats.PendingOps < 3 || stats.Seq < 3 || stats.Compactions != baseCompactions {
		t.Fatalf("unexpected WALStats before crash simulation: %+v (baseCompactions=%d)", stats, baseCompactions)
	}
	walPath := filepath.Join(dir, "files.wal")
	walBytes, err := os.ReadFile(walPath)
	if err != nil || len(walBytes) == 0 {
		t.Fatalf("expected non-empty files.wal, err=%v len=%d", err, len(walBytes))
	}

	// Simulate an unclean crash leaving a half-written JSON line at the end of WAL.
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open wal to append truncated line: %v", err)
	}
	_, _ = f.WriteString("{\"seq\":9999,\"op\":\"upsert\",\"record\":{\"id\":\"broken")
	_ = f.Close()

	// Reopen without calling Close() to test WAL replay + truncated tail recovery.
	st2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore after truncated WAL tail: %v", err)
	}
	defer func() { _ = st2.Close() }()

	if _, _, ok := st2.Get(recA.ID); ok {
		t.Fatalf("deleted file %s should remain deleted after WAL replay", recA.ID)
	}
	list := st2.AllRecords()
	if len(list) != 1 || list[0].Name != "b.txt" || list[0].FolderPath != "proj" {
		t.Fatalf("unexpected files after WAL replay: %+v", list)
	}
	if st2.WALStats().PendingOps != 0 {
		t.Fatalf("expected PendingOps=0 right after NewStore replay compaction, got %+v", st2.WALStats())
	}
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Fatalf("expected WAL file compacted away after clean replay, stat err=%v", err)
	}

	// Test explicit CompactWAL & concurrent -race stress mutations + compaction.
	baseCompactions2 := st2.WALStats().Compactions
	st2.SetWALDebounceForTest(time.Hour)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				name := fmt.Sprintf("w%d-%d.txt", worker, i)
				_, err := st2.SaveUploadWithConcurrency(name, "stress", "s", "alice", nil, DeviceInfo{Name: "PC"}, strings.NewReader("payload"), WriteConcurrencyOptions{})
				if err != nil {
					t.Errorf("concurrent SaveUpload(%s): %v", name, err)
				}
				if i%3 == 0 {
					_ = st2.CompactWAL()
				}
			}
		}(g)
	}
	wg.Wait()

	if err := st2.CompactWAL(); err != nil {
		t.Fatalf("final CompactWAL: %v", err)
	}
	if st2.WALStats().PendingOps != 0 || st2.WALStats().Compactions <= baseCompactions2 {
		t.Fatalf("expected compacted WAL stats (>%d), got %+v", baseCompactions2, st2.WALStats())
	}
	if err := st2.Close(); err != nil {
		t.Fatalf("st2.Close(): %v", err)
	}

	st3, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore after CompactWAL+Close: %v", err)
	}
	defer func() { _ = st3.Close() }()
	if len(st3.AllRecords()) != 25 { // 1 (b.txt) + 4*6 stress files
		t.Fatalf("expected 25 persisted records, got %d", len(st3.AllRecords()))
	}
}
