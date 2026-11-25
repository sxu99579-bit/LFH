package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestDateAndDateRangeFilteringOnFilesAndSummary(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)

	rToday := uploadTestBytes(t, adminClient, ts.URL, "today.txt", []byte("today-content"), nil)
	todayKey := rToday.UploadedAt.In(time.Local).Format("2006-01-02")
	if todayKey == "" {
		todayKey = time.Now().Format("2006-01-02")
	}

	// Exact today date returns the uploaded file.
	respDay, err := adminClient.Get(ts.URL + "/api/files?date=" + todayKey)
	if err != nil {
		t.Fatalf("GET date filter: %v", err)
	}
	var listDay struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	_ = json.NewDecoder(respDay.Body).Decode(&listDay)
	respDay.Body.Close()
	if len(listDay.Files) != 1 || listDay.Files[0].ID != rToday.ID {
		t.Fatalf("expected [%s] for date=%s, got %+v", rToday.ID, todayKey, listDay.Files)
	}

	// Non-matching day returns 0 files and summary total_count = 0.
	respMiss, err := adminClient.Get(ts.URL + "/api/files?date=2099-01-01")
	if err != nil {
		t.Fatalf("GET miss date: %v", err)
	}
	var listMiss struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	_ = json.NewDecoder(respMiss.Body).Decode(&listMiss)
	respMiss.Body.Close()
	if len(listMiss.Files) != 0 {
		t.Fatalf("expected 0 files for date=2099-01-01, got %d", len(listMiss.Files))
	}

	respSumMiss, err := adminClient.Get(ts.URL + "/api/summary?date=2099-01-01")
	if err != nil {
		t.Fatalf("GET summary miss date: %v", err)
	}
	var sumMiss struct {
		TotalCount int `json:"total_count"`
	}
	_ = json.NewDecoder(respSumMiss.Body).Decode(&sumMiss)
	respSumMiss.Body.Close()
	if sumMiss.TotalCount != 0 {
		t.Fatalf("expected summary total_count=0 for 2099-01-01, got %d", sumMiss.TotalCount)
	}

	// Inclusive date_from..date_to covering today returns 1 file + summary total_count = 1.
	respRange, err := adminClient.Get(ts.URL + "/api/files?date_from=2020-01-01&date_to=" + todayKey)
	if err != nil {
		t.Fatalf("GET range filter: %v", err)
	}
	var listRange struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	_ = json.NewDecoder(respRange.Body).Decode(&listRange)
	respRange.Body.Close()
	if len(listRange.Files) != 1 || listRange.Files[0].ID != rToday.ID {
		t.Fatalf("expected [%s] for date range, got %+v", rToday.ID, listRange.Files)
	}

	respSumRange, err := adminClient.Get(ts.URL + "/api/summary?date_from=2020-01-01&date_to=" + todayKey)
	if err != nil {
		t.Fatalf("GET summary range: %v", err)
	}
	var sumRange struct {
		TotalCount int `json:"total_count"`
	}
	_ = json.NewDecoder(respSumRange.Body).Decode(&sumRange)
	respSumRange.Body.Close()
	if sumRange.TotalCount != 1 {
		t.Fatalf("expected summary total_count=1 for date range, got %d", sumRange.TotalCount)
	}
}

func TestFolderLifecyclePermissionsAndFileMove(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)
	bobClient := createAndLoginMember(t, adminClient, ts.URL, "bob", "Pass123456!")

	// 1. Admin creates explicit empty nested folder.
	createBody, _ := json.Marshal(map[string]string{"path": "考研数学/线性代数"})
	respCreate, err := adminClient.Post(ts.URL+"/api/folders", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST /api/folders: %v", err)
	}
	respCreate.Body.Close()
	if respCreate.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating folder, got %d", respCreate.StatusCode)
	}

	// 2. GET /api/folders includes both ancestor and leaf.
	respList, err := adminClient.Get(ts.URL + "/api/folders")
	if err != nil {
		t.Fatalf("GET /api/folders: %v", err)
	}
	var folderRes struct {
		Folders []string `json:"folders"`
	}
	_ = json.NewDecoder(respList.Body).Decode(&folderRes)
	respList.Body.Close()
	if len(folderRes.Folders) < 2 || folderRes.Folders[0] != "考研数学" || folderRes.Folders[1] != "考研数学/线性代数" {
		t.Fatalf("unexpected folders list: %+v", folderRes.Folders)
	}

	// 3. Upload file by admin and move it into nested folder; non-owner bob must be forbidden.
	fRec := uploadTestBytes(t, adminClient, ts.URL, "chapter1.txt", []byte("det"), nil)

	moveBobBody, _ := json.Marshal(map[string]string{"folder_path": "考研数学/线性代数"})
	respBobMove, err := bobClient.Post(ts.URL+"/api/files/"+fRec.ID+"/folder", "application/json", bytes.NewReader(moveBobBody))
	if err != nil {
		t.Fatalf("bob move file: %v", err)
	}
	respBobMove.Body.Close()
	if respBobMove.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when non-owner bob moves admin file, got %d", respBobMove.StatusCode)
	}

	moveBody, _ := json.Marshal(map[string]string{"folder_path": "考研数学/线性代数"})
	respMove, err := adminClient.Post(ts.URL+"/api/files/"+fRec.ID+"/folder", "application/json", bytes.NewReader(moveBody))
	if err != nil {
		t.Fatalf("admin move file: %v", err)
	}
	var movedOut struct {
		File struct {
			FolderPath string `json:"folder_path"`
		} `json:"file"`
	}
	_ = json.NewDecoder(respMove.Body).Decode(&movedOut)
	respMove.Body.Close()
	if respMove.StatusCode != http.StatusOK || movedOut.File.FolderPath != "考研数学/线性代数" {
		t.Fatalf("unexpected move response (%d): %+v", respMove.StatusCode, movedOut)
	}

	// 4. Rename parent folder -> contained file folder_path updates too.
	renBody, _ := json.Marshal(map[string]string{"old_path": "考研数学", "new_path": "数二资料"})
	reqRen, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/folders", bytes.NewReader(renBody))
	reqRen.Header.Set("Content-Type", "application/json")
	respRen, err := adminClient.Do(reqRen)
	if err != nil {
		t.Fatalf("PATCH /api/folders: %v", err)
	}
	respRen.Body.Close()
	if respRen.StatusCode != http.StatusOK {
		t.Fatalf("rename folder status=%d", respRen.StatusCode)
	}
	afterRen, _, ok := srv.store.Get(fRec.ID)
	if !ok || afterRen.FolderPath != "数二资料/线性代数" {
		t.Fatalf("expected file folder_path propagated to '数二资料/线性代数', got %+v", afterRen)
	}

	// 5. Delete subfolder with move_to_parent -> file moves safely to 数二资料.
	delBody, _ := json.Marshal(map[string]string{"path": "数二资料/线性代数", "mode": "move_to_parent"})
	reqDel, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/folders", bytes.NewReader(delBody))
	reqDel.Header.Set("Content-Type", "application/json")
	respDel, err := adminClient.Do(reqDel)
	if err != nil {
		t.Fatalf("DELETE /api/folders: %v", err)
	}
	respDel.Body.Close()
	if respDel.StatusCode != http.StatusOK {
		t.Fatalf("delete folder status=%d", respDel.StatusCode)
	}
	afterDel, _, _ := srv.store.Get(fRec.ID)
	if afterDel.FolderPath != "数二资料" {
		t.Fatalf("expected file moved to parent '数二资料', got %q", afterDel.FolderPath)
	}
}
