package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"V0.11.21", "V0.11.21", 0},
		{"v0.11.21", "V0.11.21", 0},
		{"V0.11.22", "V0.11.21", 1},
		{"V0.12.0", "V0.11.21", 1},
		{"V1.0.0", "V0.99.99", 1},
		{"V0.11.20", "V0.11.21", -1},
		{"0.11.21.1", "V0.11.21", 1},
		{"V0.11.21.2", "V0.11.21", 1},
		{"V0.11.21", "V0.11.21.2", -1},
		{"V0.11.21.3", "V0.11.21.2", 1},
		{"V0.11.21.4", "V0.11.21.3", 1},
		{"V0.11.21.5", "V0.11.21.4", 1},
		{"V0.11.21.6", "V0.11.21.5", 1},
		{"V0.11.21.7", "V0.11.21.6", 1},
		{"V0.11.21.8", "V0.11.21.7", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSelectAssetAndConfigPersistence(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		{Name: "lfh-linux-amd64", Size: 100, BrowserDownloadURL: "https://example.com/lfh-linux-amd64"},
		{Name: "lfh-windows-amd64.exe", Size: 200, BrowserDownloadURL: "https://example.com/lfh-windows-amd64.exe"},
		{Name: "lfh-darwin-arm64.tar.gz", Size: 300, BrowserDownloadURL: "https://example.com/lfh-darwin-arm64.tar.gz"},
	}

	winAsset, cs := SelectAsset(assets, "windows", "amd64")
	if winAsset.Name != "lfh-windows-amd64.exe" || cs == "" {
		t.Fatalf("expected windows asset + checksums, got %+v cs=%q", winAsset, cs)
	}
	linAsset, _ := SelectAsset(assets, "linux", "amd64")
	if linAsset.Name != "lfh-linux-amd64" {
		t.Fatalf("expected linux asset, got %+v", linAsset)
	}
	macAsset, _ := SelectAsset(assets, "darwin", "arm64")
	if macAsset.Name != "lfh-darwin-arm64.tar.gz" {
		t.Fatalf("expected darwin arm64 asset, got %+v", macAsset)
	}

	dir := t.TempDir()
	mgr, err := NewManager(dir, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	autoOff := false
	st, err := mgr.UpdateConfig("https://github.com/myorg/LFH.git", "https://ghfast.top/", &autoOff)
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if st.Repo != "myorg/LFH" || st.ProxyURL != "https://ghfast.top" || st.AutoCheck {
		t.Fatalf("unexpected updated config status: %+v", st)
	}

	reloaded, err := NewManager(dir, "")
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	rst := reloaded.Status()
	if rst.Repo != "myorg/LFH" || rst.ProxyURL != "https://ghfast.top" || rst.AutoCheck {
		t.Fatalf("expected persisted config, got %+v", rst)
	}
}

func TestCheckDownloadVerifySwapAndRollback(t *testing.T) {
	newBytes := []byte("LFH-NEW-BINARY-PAYLOAD-V0.12.0")
	sum := sha256.Sum256(newBytes)
	newHex := hex.EncodeToString(sum[:])

	assetName := fmt.Sprintf("lfh-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	serveWrongChecksum := true
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/tester/LFH/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{
				TagName:     "V0.12.0",
				HTMLURL:     ts.URL + "/releases/V0.12.0",
				Body:        "Zero-downtime update test release",
				PublishedAt: "2026-11-21T18:00:00Z",
				Assets: []ReleaseAsset{
					{Name: assetName, Size: int64(len(newBytes)), BrowserDownloadURL: ts.URL + "/dl/" + assetName},
					{Name: "checksums.txt", Size: 128, BrowserDownloadURL: ts.URL + "/dl/checksums.txt"},
				},
			})
		case "/dl/" + assetName:
			_, _ = w.Write(newBytes)
		case "/dl/checksums.txt":
			digest := newHex
			if serveWrongChecksum {
				digest = strings.Repeat("0", 64)
			}
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "lfh.exe")
	origBytes := []byte("LFH-OLD-BINARY-V0.11.21")
	if err := os.WriteFile(fakeExe, origBytes, 0755); err != nil {
		t.Fatalf("write fakeExe: %v", err)
	}

	mgr, err := NewManager(dir, "tester/LFH")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.apiBaseURL = ts.URL
	mgr.exePathFn = func() (string, error) { return fakeExe, nil }

	availableNotified := false
	mgr.SetCallbacks(nil, func(st Status) {
		if st.LatestVersion == "V0.12.0" {
			availableNotified = true
		}
	}, nil)

	st, err := mgr.CheckNow(context.Background())
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if !st.HasUpdate || st.LatestVersion != "V0.12.0" || st.AssetName != assetName || !availableNotified {
		t.Fatalf("unexpected check status: %+v notified=%v", st, availableNotified)
	}

	// 1. Wrong checksum must abort and leave fakeExe untouched.
	if err := mgr.ApplyUpdateSync(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	gotCur, _ := os.ReadFile(fakeExe)
	if string(gotCur) != string(origBytes) {
		t.Fatalf("expected original binary intact after checksum error, got %q", string(gotCur))
	}

	// 2. Correct checksum, failing verifyFn must abort cleanly.
	serveWrongChecksum = false
	mgr.verifyFn = func(ctx context.Context, binPath string) error {
		return errors.New("bad startup self check")
	}
	if err := mgr.ApplyUpdateSync(context.Background()); err == nil || !strings.Contains(err.Error(), "自检失败") {
		t.Fatalf("expected verify error, got %v", err)
	}
	gotCur, _ = os.ReadFile(fakeExe)
	if string(gotCur) != string(origBytes) {
		t.Fatalf("expected original binary intact after verify error, got %q", string(gotCur))
	}

	// 3. Failing handoffFn must automatically roll fakeExe back from backup.
	mgr.verifyFn = func(ctx context.Context, binPath string) error { return nil }
	mgr.handoffFn = func(exePath, backupPath, targetVersion string) error {
		return errors.New("port handoff failed")
	}
	if err := mgr.ApplyUpdateSync(context.Background()); err == nil || !strings.Contains(err.Error(), "已自动回滚") {
		t.Fatalf("expected handoff rollback error, got %v", err)
	}
	gotCur, _ = os.ReadFile(fakeExe)
	if string(gotCur) != string(origBytes) {
		t.Fatalf("expected binary rolled back to original after failed handoff, got %q", string(gotCur))
	}

	// 4. Successful verify + swap + handoff replaces fakeExe cleanly.
	var handedVersion string
	mgr.handoffFn = func(exePath, backupPath, targetVersion string) error {
		handedVersion = targetVersion
		if _, err := os.Stat(backupPath); err != nil {
			return fmt.Errorf("missing backup %s: %w", backupPath, err)
		}
		return nil
	}
	if err := mgr.ApplyUpdateSync(context.Background()); err != nil {
		t.Fatalf("expected ApplyUpdateSync success, got %v", err)
	}
	if handedVersion != "V0.12.0" || mgr.Status().State != "done" || mgr.Status().Progress != 100 {
		t.Fatalf("unexpected final status: ver=%q status=%+v", handedVersion, mgr.Status())
	}
	gotCur, _ = os.ReadFile(fakeExe)
	if string(gotCur) != string(newBytes) {
		t.Fatalf("expected fakeExe replaced with new binary, got %q", string(gotCur))
	}
}

func TestApplyBinaryStreamAndLocalBinaryForPlatform(t *testing.T) {
	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "lfh.exe")
	origBytes := []byte("OLD-WORKER-BIN")
	if err := os.WriteFile(fakeExe, origBytes, 0755); err != nil {
		t.Fatalf("write fakeExe: %v", err)
	}
	mgr, err := NewManager(dir, "tester/LFH")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.exePathFn = func() (string, error) { return fakeExe, nil }
	mgr.verifyFn = func(ctx context.Context, binPath string) error { return nil }

	p, shaHex, sz, err := mgr.LocalBinaryForPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil || p != fakeExe || sz != int64(len(origBytes)) || shaHex == "" {
		t.Fatalf("LocalBinaryForPlatform mismatch: p=%q sha=%q sz=%d err=%v", p, shaHex, sz, err)
	}

	pushed := []byte("PRIMARY-PUSHED-BINARY-V0.11.21.7")
	sum := sha256.Sum256(pushed)
	pushedSHA := hex.EncodeToString(sum[:])

	// Wrong SHA must fail and preserve original file.
	if err := mgr.ApplyBinaryStream(context.Background(), strings.NewReader(string(pushed)), "V0.11.21.7", "deadbeef", false); err == nil {
		t.Fatalf("expected error on wrong SHA")
	}

	var handedVer string
	mgr.handoffFn = func(exePath, backupPath, targetVersion string) error {
		handedVer = targetVersion
		return nil
	}
	if err := mgr.ApplyBinaryStream(context.Background(), strings.NewReader(string(pushed)), "V0.11.21.7", pushedSHA, false); err != nil {
		t.Fatalf("ApplyBinaryStream failed: %v", err)
	}
	if handedVer != "V0.11.21.7" {
		t.Fatalf("expected handoff version V0.11.21.7, got %q", handedVer)
	}
	got, _ := os.ReadFile(fakeExe)
	if string(got) != string(pushed) {
		t.Fatalf("expected fakeExe updated to pushed payload, got %q", string(got))
	}
}
