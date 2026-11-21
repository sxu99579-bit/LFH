package storage

import (
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
