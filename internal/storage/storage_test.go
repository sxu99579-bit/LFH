package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSaveListSummaryDelete(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	dev1 := DeviceInfo{
		FP:     "FP-AABBCCDDEE",
		Name:   "Windows · Edge",
		IP:     "192.168.3.53",
		Detail: "1920x1080 · 16核",
	}
	dev2 := DeviceInfo{
		FP:     "FP-1122334455",
		Name:   "iPhone · Safari",
		IP:     "192.168.3.88",
		Detail: "390x844",
	}

	r1, err := st.SaveUpload("demo.mp4", "admin", dev1, strings.NewReader("video-bytes"))
	if err != nil {
		t.Fatalf("SaveUpload 1: %v", err)
	}
	r2, err := st.SaveUpload("notes.md", "alice", dev2, strings.NewReader("# hello"))
	if err != nil {
		t.Fatalf("SaveUpload 2: %v", err)
	}
	if r1.Category != "video" || r2.Category != "markdown" {
		t.Fatalf("unexpected categories: %s, %s", r1.Category, r2.Category)
	}
	if r1.DeviceFP != dev1.FP || r1.DeviceName != dev1.Name || r1.DeviceIP != dev1.IP {
		t.Fatalf("unexpected device info on r1: %+v", r1)
	}

	cnt, used := st.LocalStats()
	if cnt != 2 || used != int64(len("video-bytes")+len("# hello")) {
		t.Fatalf("unexpected LocalStats: cnt=%d used=%d", cnt, used)
	}

	sum := st.Summary("", "")
	if sum.TotalCount != 2 || len(sum.ByTime) != 1 || len(sum.ByCategory) != 2 || len(sum.ByDevice) != 2 {
		t.Fatalf("unexpected summary: %+v", sum)
	}

	videos := st.List("video", "", "", "", "")
	if len(videos) != 1 || videos[0].ID != r1.ID {
		t.Fatalf("unexpected video list: %+v", videos)
	}

	byDev := st.List("", "", "", dev2.FP, "")
	if len(byDev) != 1 || byDev[0].ID != r2.ID {
		t.Fatalf("unexpected device filter list: %+v", byDev)
	}

	bySearch := st.List("", "", "", "", "iPhone")
	if len(bySearch) != 1 || bySearch[0].ID != r2.ID {
		t.Fatalf("unexpected device search list: %+v", bySearch)
	}

	byFPSearch := st.List("", "", "", "", "AABBCC")
	if len(byFPSearch) != 1 || byFPSearch[0].ID != r1.ID {
		t.Fatalf("unexpected FP search list: %+v", byFPSearch)
	}

	// Test pure FilterRecords and SummarizeRecords across heterogeneous nodes.
	r1.NodeID = "node-a"
	r1.NodeName = "PC-A"
	r2.NodeID = "node-b"
	r2.NodeName = "Linux-B"
	clusterList := []FileRecord{r1, r2}

	nodeBFiles := FilterRecords(clusterList, "", "", "", "", "node-b", "")
	if len(nodeBFiles) != 1 || nodeBFiles[0].ID != r2.ID {
		t.Fatalf("unexpected FilterRecords by nodeID: %+v", nodeBFiles)
	}
	nodeSearch := FilterRecords(clusterList, "", "", "", "", "", "Linux-B")
	if len(nodeSearch) != 1 || nodeSearch[0].ID != r2.ID {
		t.Fatalf("unexpected FilterRecords by nodeName query: %+v", nodeSearch)
	}

	clusterSum := SummarizeRecords(clusterList, "", "", "")
	if len(clusterSum.ByNode) != 2 {
		t.Fatalf("expected 2 nodes in ByNode, got %+v", clusterSum.ByNode)
	}

	// Reload store from disk to verify persistence of device fields.
	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	got, _, ok := reloaded.Get(r1.ID)
	if !ok || got.DeviceFP != dev1.FP || got.DeviceIP != dev1.IP {
		t.Fatalf("device info lost after reload: %+v", got)
	}

	// Test folder path sanitization and SaveUploadWithFolder.
	if got := SanitizeFolderPath(`C:\proj\..\docs\sub\`); got != "proj/docs/sub" {
		t.Fatalf("unexpected SanitizeFolderPath: %q", got)
	}
	rf, err := st.SaveUploadWithFolder("spec.md", "../../MyApp/docs", "admin", dev1, strings.NewReader("# spec"))
	if err != nil {
		t.Fatalf("SaveUploadWithFolder: %v", err)
	}
	if rf.FolderPath != "MyApp/docs" {
		t.Fatalf("expected folder_path 'MyApp/docs', got %q", rf.FolderPath)
	}
	if found := st.List("", "", "", "", "MyApp"); len(found) != 1 || found[0].ID != rf.ID {
		t.Fatalf("expected folder search to match %s, got %+v", rf.ID, found)
	}

	if err := st.Delete(r1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.Delete(rf.ID); err != nil {
		t.Fatalf("Delete folder file: %v", err)
	}
	if len(st.List("", "", "", "", "")) != 1 {
		t.Fatalf("expected 1 file after delete")
	}
}

func TestStorageACLAndUploadDirMigration(t *testing.T) {
	dataDir := t.TempDir()
	st, err := NewStore(dataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	dev := DeviceInfo{FP: "FP-1", Name: "PC", IP: "127.0.0.1"}

	pubRec, err := st.SaveUploadWithOptions("public.txt", "", "alice", nil, dev, strings.NewReader("pub"))
	if err != nil {
		t.Fatalf("SaveUploadWithOptions public: %v", err)
	}
	privRec, err := st.SaveUploadWithOptions("secret.txt", "", "alice", []string{" bob ", "bob", ""}, dev, strings.NewReader("secret-body"))
	if err != nil {
		t.Fatalf("SaveUploadWithOptions restricted: %v", err)
	}
	if len(privRec.AllowedUsers) != 1 || privRec.AllowedUsers[0] != "bob" {
		t.Fatalf("expected normalized allowed_users=[bob], got %+v", privRec.AllowedUsers)
	}

	all := st.AllRecords()
	if got := FilterAccessibleRecords(all, "carol", "user"); len(got) != 1 || got[0].ID != pubRec.ID {
		t.Fatalf("expected carol to only see public file, got %+v", got)
	}
	if got := FilterAccessibleRecords(all, "bob", "user"); len(got) != 2 {
		t.Fatalf("expected bob to see both files, got %+v", got)
	}
	if got := FilterAccessibleRecords(all, "alice", "user"); len(got) != 2 {
		t.Fatalf("expected uploader alice to see both files, got %+v", got)
	}
	if got := FilterAccessibleRecords(all, "other_admin", "admin"); len(got) != 2 {
		t.Fatalf("expected admin to see both files, got %+v", got)
	}

	// Update ACL via SetAllowedUsers.
	updated, err := st.SetAllowedUsers(privRec.ID, []string{"carol", "bob"})
	if err != nil {
		t.Fatalf("SetAllowedUsers: %v", err)
	}
	if !CanUserAccess(updated, "carol", "user") {
		t.Fatalf("expected carol to gain access after SetAllowedUsers")
	}

	// Switch storage dir WITHOUT migration: old files stay readable, new files go to new dir.
	dir2 := filepath.Join(t.TempDir(), "drive-d", "LFH-Storage")
	moved, err := st.SetUploadDir(dir2, false)
	if err != nil || moved != 0 {
		t.Fatalf("SetUploadDir without migrate: moved=%d err=%v", moved, err)
	}
	if st.UploadDir() != dir2 {
		t.Fatalf("expected UploadDir=%q, got %q", dir2, st.UploadDir())
	}
	if _, privDisk, ok := st.Get(privRec.ID); !ok || filepath.Dir(privDisk) != filepath.Join(dataDir, "uploads") {
		t.Fatalf("expected unmigrated file to remain readable at old dir, ok=%v path=%q", ok, privDisk)
	}
	newRec, err := st.SaveUploadWithOptions("in-d.txt", "", "alice", nil, dev, strings.NewReader("in-d"))
	if err != nil {
		t.Fatalf("SaveUploadWithOptions in dir2: %v", err)
	}
	if newRec.StorageDir != dir2 {
		t.Fatalf("expected new file StorageDir=%q, got %q", dir2, newRec.StorageDir)
	}

	// Switch storage dir WITH migration: moves all records to dir3.
	dir3 := filepath.Join(t.TempDir(), "drive-e", "LFH-Storage")
	moved3, err := st.SetUploadDir(dir3, true)
	if err != nil || moved3 != 3 {
		t.Fatalf("SetUploadDir with migrate: moved=%d err=%v", moved3, err)
	}
	for _, id := range []string{pubRec.ID, privRec.ID, newRec.ID} {
		rec, p, ok := st.Get(id)
		if !ok || rec.StorageDir != dir3 || filepath.Dir(p) != dir3 {
			t.Fatalf("expected migrated file %s in %s, got ok=%v rec.StorageDir=%q path=%q", id, dir3, ok, rec.StorageDir, p)
		}
		if _, statErr := os.Stat(p); statErr != nil {
			t.Fatalf("migrated file missing on disk at %q: %v", p, statErr)
		}
	}

	// Reload store cleanly from disk and verify migrated files remain indexed.
	reloaded, err := NewStore(dataDir)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	if len(reloaded.AllRecords()) != 3 {
		t.Fatalf("expected 3 records after reload across custom StorageDir, got %d", len(reloaded.AllRecords()))
	}
}
