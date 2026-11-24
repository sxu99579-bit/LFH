package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lfh/internal/storage"
)

func TestTransferTicketSignVerifyTamperExpiryAndReplay(t *testing.T) {
	secret := "lfh-ticket:super-secret-cluster-key"
	now := time.Now().UTC()
	orig := TransferTicket{
		Action:       TicketActionUp,
		NodeID:       "node_w1",
		FolderPath:   "dropzone",
		FileName:     "build.tar.gz",
		AllowedUsers: []string{"alice"},
		ActorUser:    "alice",
		ActorDevice:  "Agent · Bot",
		ActorKeyID:   "agk_1",
		MaxBytes:     1024 * 1024,
		Exp:          now.Add(5 * time.Minute).Unix(),
	}
	payloadB64, sigHex, err := SignTicket(secret, orig)
	if err != nil {
		t.Fatalf("SignTicket: %v", err)
	}

	got, err := VerifyTicket(secret, payloadB64, sigHex, now)
	if err != nil {
		t.Fatalf("VerifyTicket: %v", err)
	}
	if got.NodeID != orig.NodeID || got.FileName != orig.FileName || got.Nonce == "" {
		t.Fatalf("unexpected verified ticket: %+v", got)
	}

	// Tampered payload or signature must fail.
	tampered := "A" + payloadB64[1:]
	if _, err := VerifyTicket(secret, tampered, sigHex, now); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid on tampered payload, got %v", err)
	}
	badSig := "ff" + sigHex[2:]
	if _, err := VerifyTicket(secret, payloadB64, badSig, now); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid on tampered sig, got %v", err)
	}

	// Expired timestamp must fail.
	if _, err := VerifyTicket(secret, payloadB64, sigHex, now.Add(10*time.Minute)); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expected ErrTicketExpired after expiry, got %v", err)
	}

	// Replay guard enforces strict single-use nonce consumption.
	guard := NewTicketReplayGuard()
	if !guard.Consume(got.Nonce, got.Exp, now) {
		t.Fatalf("expected first Consume to succeed")
	}
	if guard.Consume(got.Nonce, got.Exp, now.Add(time.Second)) {
		t.Fatalf("expected second Consume with same nonce to be rejected")
	}

	// LAN / Tailscale IP checks.
	privateAddrs := []string{
		"127.0.0.1:54321",
		"[::1]:8080",
		"192.168.10.25:443",
		"10.2.3.4:8080",
		"172.16.0.1:8080",
		"100.64.12.34:41641",
		"100.127.255.1:80",
	}
	for _, addr := range privateAddrs {
		if !IsPrivateOrTailscaleAddr(addr) {
			t.Fatalf("expected private/Tailscale addr true for %q", addr)
		}
	}
	publicAddrs := []string{"8.8.8.8:443", "1.1.1.1:80", "100.128.0.1:80", ""}
	for _, addr := range publicAddrs {
		if IsPrivateOrTailscaleAddr(addr) {
			t.Fatalf("expected public/invalid addr false for %q", addr)
		}
	}
}

func TestWebhookDispatcherSignatureFolderFilterAndRetry(t *testing.T) {
	var attempts atomic.Int32
	type deliveryRecord struct {
		Sig   string
		Event string
		Body  []byte
	}
	var mu sync.Mutex
	var received []deliveryRecord
	notifyCh := make(chan struct{}, 8)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		received = append(received, deliveryRecord{
			Sig:   r.Header.Get("X-LFH-Signature-256"),
			Event: r.Header.Get("X-LFH-Event"),
			Body:  body,
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case notifyCh <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	wm, err := NewWebhookManager(dir)
	if err != nil {
		t.Fatalf("NewWebhookManager: %v", err)
	}
	defer wm.Close()
	wm.SetRetryDelaysForTest([]time.Duration{15 * time.Millisecond, 30 * time.Millisecond})

	ruleShop, err := wm.Create(CreateWebhookInput{
		Name:                  "ShopCI",
		FolderPrefix:          "shopapp",
		Events:                []string{EventFileUploaded, EventVerifyCompleted},
		TargetURL:             ts.URL + "/hook",
		Secret:                "sec_shop",
		IncludeDownloadTicket: false,
	}, "admin")
	if err != nil {
		t.Fatalf("Create ShopCI: %v", err)
	}
	ruleDocs, err := wm.Create(CreateWebhookInput{
		Name:                  "DocsCI",
		FolderPrefix:          "docs",
		Events:                []string{EventFileUploaded},
		TargetURL:             ts.URL + "/docs",
		Secret:                "sec_docs",
		IncludeDownloadTicket: true,
	}, "admin")
	if err != nil {
		t.Fatalf("Create DocsCI: %v", err)
	}

	// Dispatch matching shopapp event (first attempt returns 500, second succeeds; DownloadTicketURL must be stripped for ruleShop).
	wm.Dispatch(WebhookEvent{
		Event:             EventFileUploaded,
		Version:           7,
		Uploader:          "alice",
		Count:             1,
		FileNames:         []string{"main.go"},
		FolderPaths:       []string{"shopapp/cmd"},
		DownloadTicketURL: "http://worker/s/ticket?secret=1",
	})

	select {
	case <-notifyCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for retried webhook delivery")
	}

	mu.Lock()
	if len(received) != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 successful delivery after retry, got %d", len(received))
	}
	d := received[0]
	mu.Unlock()

	if attempts.Load() < 2 {
		t.Fatalf("expected at least 2 attempts (1 failure + 1 retry), got %d", attempts.Load())
	}
	if d.Sig != SignWebhookPayload("sec_shop", d.Body) {
		t.Fatalf("webhook signature mismatch: got %q", d.Sig)
	}
	if strings.Contains(string(d.Body), "secret=1") {
		t.Fatalf("expected DownloadTicketURL stripped when IncludeDownloadTicket=false, got body=%s", string(d.Body))
	}

	// Dispatch matching docs event -> IncludeDownloadTicket=true keeps DownloadTicketURL.
	wm.Dispatch(WebhookEvent{
		Event:             EventFileUploaded,
		Version:           8,
		Uploader:          "bob",
		FileNames:         []string{"guide.md"},
		FolderPaths:       []string{"docs"},
		DownloadTicketURL: "http://worker/s/ticket?secret=docs",
	})
	select {
	case <-notifyCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for docs webhook delivery")
	}
	mu.Lock()
	lastBody := string(received[len(received)-1].Body)
	mu.Unlock()
	if !strings.Contains(lastBody, "secret=docs") {
		t.Fatalf("expected DownloadTicketURL retained for ruleDocs, got %s", lastBody)
	}

	// Synchronous test button + persistence reload + Update/Delete.
	testRes, err := wm.SendTest(context.Background(), ruleShop.ID, "admin")
	if err != nil || !testRes.OK || testRes.StatusCode != http.StatusOK {
		t.Fatalf("SendTest failed: res=%+v err=%v", testRes, err)
	}
	disabled := false
	upd, err := wm.Update(ruleDocs.ID, UpdateWebhookInput{Enabled: &disabled})
	if err != nil || upd.Enabled {
		t.Fatalf("Update disabled failed: upd=%+v err=%v", upd, err)
	}
	wm.Close()

	wm2, err := NewWebhookManager(dir)
	if err != nil {
		t.Fatalf("reload NewWebhookManager: %v", err)
	}
	defer wm2.Close()
	if len(wm2.List()) != 2 {
		t.Fatalf("expected 2 persisted webhooks after reload, got %d", len(wm2.List()))
	}
	if err := wm2.Delete(ruleDocs.ID); err != nil {
		t.Fatalf("Delete webhook: %v", err)
	}
	if len(wm2.List()) != 1 {
		t.Fatalf("expected 1 webhook after delete, got %d", len(wm2.List()))
	}
}

// TestVerifyHelperProcess acts as a cross-platform mock build/test subprocess when LFH_VERIFY_HELPER=1.
func TestVerifyHelperProcess(t *testing.T) {
	if os.Getenv("LFH_VERIFY_HELPER") != "1" {
		return
	}
	mode := ""
	counterFile := os.Getenv("LFH_VERIFY_COUNTER_FILE")
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	if counterFile != "" {
		f, err := os.OpenFile(counterFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			_, _ = f.WriteString("x")
			_ = f.Close()
		}
	}
	switch mode {
	case "ok":
		fmt.Println("line-1 build starting")
		fmt.Fprintln(os.Stderr, "VERIFY_OK_MARKER")
		os.Exit(0)
	case "sleep180ms":
		fmt.Println("COALESCE_START")
		time.Sleep(180 * time.Millisecond)
		fmt.Println("COALESCE_DONE")
		os.Exit(0)
	case "sleep3s":
		fmt.Println("TIMEOUT_START")
		time.Sleep(3 * time.Second)
		os.Exit(0)
	case "big":
		chunk := strings.Repeat("A", 1024) + "\n"
		for i := 0; i < 190; i++ {
			fmt.Print(chunk)
		}
		fmt.Println("TAIL_END_MARKER")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestVerifyRunnerCoalescingTimeoutAndLogCapture(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LFH_VERIFY_HELPER", "1")

	root := t.TempDir()
	mount := storage.MountEntry{
		ID:      "mnt_verify",
		Alias:   "workspace",
		DirPath: root,
	}
	runner := NewVerifyRunner()

	// 1. Clean execution + log capture + Truncated 128KB tail check.
	bigRes, err := runner.Run(context.Background(), mount, storage.VerifyPreset{
		ID:         "big-out",
		Label:      "Big Output",
		Command:    exe,
		Args:       []string{"-test.run=^TestVerifyHelperProcess$", "--", "big"},
		TimeoutSec: 15,
	})
	if err != nil {
		t.Fatalf("Run big-out: %v", err)
	}
	if !bigRes.OK || bigRes.ExitCode != 0 || !bigRes.Truncated || len(bigRes.TailOutput) > maxVerifyTailBytes {
		t.Fatalf("unexpected big-out VerifyResult: ok=%v exit=%d trunc=%v len=%d", bigRes.OK, bigRes.ExitCode, bigRes.Truncated, len(bigRes.TailOutput))
	}
	if !strings.Contains(bigRes.TailOutput, "TAIL_END_MARKER") {
		t.Fatalf("expected TAIL_END_MARKER in truncated tail")
	}
	if bigRes.LogPath != "workspace/.lfh-logs/big-out.log" {
		t.Fatalf("expected LogPath=workspace/.lfh-logs/big-out.log, got %q", bigRes.LogPath)
	}
	logRaw, err := os.ReadFile(filepath.Join(root, ".lfh-logs", "big-out.log"))
	if err != nil || !strings.Contains(string(logRaw), "TAIL_END_MARKER") {
		t.Fatalf("expected persisted log file containing TAIL_END_MARKER, err=%v", err)
	}

	// 2. Single-flight coalescing across two concurrent callers for same mount+preset.
	counterFile := filepath.Join(t.TempDir(), "count.txt")
	t.Setenv("LFH_VERIFY_COUNTER_FILE", counterFile)
	presetCoal := storage.VerifyPreset{
		ID:         "coal",
		Label:      "Coalesce",
		Command:    exe,
		Args:       []string{"-test.run=^TestVerifyHelperProcess$", "--", "sleep180ms"},
		TimeoutSec: 15,
	}
	var wg sync.WaitGroup
	results := make([]VerifyResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = runner.Run(context.Background(), mount, presetCoal)
		}(i)
		if i == 0 {
			time.Sleep(40 * time.Millisecond)
		}
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("coalesced verify errs: %v / %v", errs[0], errs[1])
	}
	if !(results[0].Coalesced != results[1].Coalesced) {
		t.Fatalf("expected exactly one coalesced result, got r0=%v r1=%v", results[0].Coalesced, results[1].Coalesced)
	}
	cntBytes, _ := os.ReadFile(counterFile)
	if len(cntBytes) != 1 {
		t.Fatalf("expected helper process executed once under coalescing, got %d executions", len(cntBytes))
	}

	// 3. Timeout enforcement (1s timeout vs 3s sleep).
	t.Setenv("LFH_VERIFY_COUNTER_FILE", "")
	toRes, err := runner.Run(context.Background(), mount, storage.VerifyPreset{
		ID:         "slow",
		Label:      "Timeout Test",
		Command:    exe,
		Args:       []string{"-test.run=^TestVerifyHelperProcess$", "--", "sleep3s"},
		TimeoutSec: 1,
	})
	if err != nil {
		t.Fatalf("timeout Run returned unexpected error instead of TimedOut result: %v", err)
	}
	if toRes.OK || !toRes.TimedOut {
		t.Fatalf("expected TimedOut=true and OK=false, got %+v", toRes)
	}

	// 4. Symlink escape containment check.
	outsideDir := t.TempDir()
	linkPath := filepath.Join(root, "escape-link")
	if symErr := os.Symlink(outsideDir, linkPath); symErr == nil {
		_, runErr := runner.Run(context.Background(), mount, storage.VerifyPreset{
			ID:         "sym",
			Label:      "Symlink",
			WorkSubDir: "escape-link",
			Command:    exe,
			Args:       []string{"-test.run=^TestVerifyHelperProcess$", "--", "ok"},
			TimeoutSec: 5,
		})
		if runErr == nil {
			t.Fatalf("expected symlink escape out of mount root (%s) to be rejected", runtime.GOOS)
		}
	}
}
