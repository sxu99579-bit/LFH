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

func TestFileVersionControlAndTelemetryCounters(t *testing.T) {
	dataDir := t.TempDir()
	st, err := NewStore(dataDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	dev1 := DeviceInfo{FP: "FP-1", Name: "PC-1", IP: "192.168.3.10"}
	dev2 := DeviceInfo{FP: "FP-2", Name: "PC-2", IP: "192.168.3.20"}

	// 1. Upload initial v1
	v1Rec, err := st.SaveUploadWithVersionNote("design.md", "docs", "初版草稿", "alice", nil, dev1, strings.NewReader("version-1"))
	if err != nil {
		t.Fatalf("SaveUpload v1: %v", err)
	}
	if v1Rec.Version != 1 || v1Rec.VersionCount != 1 || len(v1Rec.Versions) != 0 {
		t.Fatalf("expected v1 with VersionCount=1, got %+v", v1Rec)
	}

	// 2. Uploading the same filename + folderPath automatically creates v2 while preserving v1
	v2Rec, err := st.SaveUploadWithVersionNote("design.md", "docs", "补充架构图", "bob", nil, dev2, strings.NewReader("version-2-longer"))
	if err != nil {
		t.Fatalf("SaveUpload v2: %v", err)
	}
	if v2Rec.ID != v1Rec.ID || v2Rec.Version != 2 || v2Rec.VersionCount != 2 || len(v2Rec.Versions) != 1 {
		t.Fatalf("expected same ID and Version=2 VersionCount=2, got %+v", v2Rec)
	}
	if v2Rec.Versions[0].Version != 1 || v2Rec.Versions[0].Note != "初版草稿" {
		t.Fatalf("unexpected archived v1: %+v", v2Rec.Versions[0])
	}

	// 3. Explicit SaveNewVersion creates v3
	v3Rec, err := st.SaveNewVersion(v1Rec.ID, "design.md", "定稿发布", "alice", dev1, strings.NewReader("version-3-final"))
	if err != nil {
		t.Fatalf("SaveNewVersion v3: %v", err)
	}
	if v3Rec.Version != 3 || v3Rec.VersionCount != 3 || len(v3Rec.Versions) != 2 {
		t.Fatalf("expected Version=3 VersionCount=3, got %+v", v3Rec)
	}

	// 4. GetVersion reads both current (v3) and historical (v1) versions from disk
	_, fv1, path1, ok := st.GetVersion(v1Rec.ID, 1)
	if !ok || fv1.Version != 1 {
		t.Fatalf("GetVersion(1) failed: ok=%v fv=%+v", ok, fv1)
	}
	raw1, err := os.ReadFile(path1)
	if err != nil || string(raw1) != "version-1" {
		t.Fatalf("unexpected v1 content: %q err=%v", string(raw1), err)
	}

	// 5. RestoreVersion promotes v1 back to active while keeping v3 and v2 in history
	restored, err := st.RestoreVersion(v1Rec.ID, 1, "回滚至初版")
	if err != nil {
		t.Fatalf("RestoreVersion(1): %v", err)
	}
	if restored.Version != 1 || restored.VersionCount != 3 || restored.VersionNote != "回滚至初版" {
		t.Fatalf("unexpected restored record: %+v", restored)
	}
	_, curPath, ok := st.Get(v1Rec.ID)
	if !ok {
		t.Fatalf("Get after restore failed")
	}
	curBytes, _ := os.ReadFile(curPath)
	if string(curBytes) != "version-1" {
		t.Fatalf("expected active content 'version-1' after restore, got %q", string(curBytes))
	}

	// 6. DeleteVersion removes historical v2
	afterDelV2, err := st.DeleteVersion(v1Rec.ID, 2)
	if err != nil || afterDelV2.VersionCount != 2 || len(afterDelV2.Versions) != 1 {
		t.Fatalf("DeleteVersion(2) mismatch: rec=%+v err=%v", afterDelV2, err)
	}

	// 7. Transfer and IO error stats work accurately
	st.AddDownloadBytes(2048)
	upBps, downBps, totalUp, totalDown := st.TransferStats()
	if totalUp <= 0 || totalDown != 2048 || upBps <= 0 || downBps <= 0 {
		t.Fatalf("unexpected TransferStats: upBps=%d downBps=%d totalUp=%d totalDown=%d", upBps, downBps, totalUp, totalDown)
	}
	st.RecordReadError()
	st.RecordWriteError()
	st.RecordWriteError()
	rErr, wErr := st.IOStats()
	if rErr != 1 || wErr != 2 {
		t.Fatalf("unexpected IOStats: r=%d w=%d", rErr, wErr)
	}
}
