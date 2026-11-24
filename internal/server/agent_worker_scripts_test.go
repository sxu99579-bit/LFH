package server

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentWorkerScriptsRenderAndInjectionSafety(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	for _, ep := range []string{"/agent/worker.ps1", "/agent/worker.sh"} {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", ep, resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
			t.Fatalf("%s unexpected Content-Type=%q", ep, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s expected Cache-Control=no-store, got %q", ep, resp.Header.Get("Cache-Control"))
		}
		for _, want := range []string{ts.URL, "/api/agent-tasks/poll", "claim_task", "complete_task", "mcp_config.json", "NO_PROXY"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing expected marker %q", ep, want)
			}
		}

		// Hostile query params must sanitize cleanly with zero command injection.
		hostile := ts.URL + ep + "?runner=" + url.QueryEscape("claude';rm -rf /;") +
			"&mode=" + url.QueryEscape("interval$(whoami)") +
			"&interval_min=999999" +
			"&cooldown_sec=1" +
			"&server_name=" + url.QueryEscape("My_Lfh!@#")
		hResp, err := http.Get(hostile)
		if err != nil {
			t.Fatalf("GET hostile %s: %v", ep, err)
		}
		hRaw, _ := io.ReadAll(hResp.Body)
		hResp.Body.Close()
		hBody := string(hRaw)

		if strings.Contains(hBody, "$(whoami)") || strings.Contains(hBody, "rm -rf") {
			t.Fatalf("%s leaked unsanitized query param", ep)
		}
		if ep == "/agent/worker.ps1" {
			if !strings.Contains(hBody, "$runnerMode = 'auto'") ||
				!strings.Contains(hBody, "$pollMode = 'poll'") ||
				!strings.Contains(hBody, "$intervalMin = 1440") ||
				!strings.Contains(hBody, "$cooldownSec = 5") ||
				!strings.Contains(hBody, "$serverName = 'my_lfh'") {
				t.Fatalf("worker.ps1 unexpected sanitized assignments:\n%s", hBody)
			}
		} else {
			if !strings.Contains(hBody, "RUNNER_MODE='auto'") ||
				!strings.Contains(hBody, "POLL_MODE='poll'") ||
				!strings.Contains(hBody, "INTERVAL_MIN='1440'") ||
				!strings.Contains(hBody, "COOLDOWN_SEC='5'") ||
				!strings.Contains(hBody, "SERVER_NAME='my_lfh'") {
				t.Fatalf("worker.sh unexpected sanitized assignments")
			}
		}
	}
}

func TestAgentWorkerScriptsSyntaxValidation(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	tmpDir := t.TempDir()

	psResp, err := http.Get(ts.URL + "/agent/worker.ps1?runner=agy&mode=interval&interval_min=30&cooldown_sec=20&server_name=lfh-lab")
	if err != nil {
		t.Fatalf("GET /agent/worker.ps1: %v", err)
	}
	psBytes, _ := io.ReadAll(psResp.Body)
	psResp.Body.Close()
	psFile := filepath.Join(tmpDir, "worker.ps1")
	if err := os.WriteFile(psFile, psBytes, 0644); err != nil {
		t.Fatal(err)
	}

	psExe := ""
	for _, candidate := range []string{"powershell.exe", "pwsh"} {
		if p, lookErr := exec.LookPath(candidate); lookErr == nil {
			psExe = p
			break
		}
	}
	if psExe != "" {
		cmd := exec.Command(psExe, "-NoProfile", "-NonInteractive", "-Command",
			"$errs=$null; [void][System.Management.Automation.Language.Parser]::ParseFile($env:LFH_TEST_PS1, [ref]$null, [ref]$errs); if ($errs.Count -gt 0) { $errs | ForEach-Object { Write-Error $_.Message }; exit 1 }")
		cmd.Env = append(os.Environ(), "LFH_TEST_PS1="+psFile)
		if out, pErr := cmd.CombinedOutput(); pErr != nil {
			t.Fatalf("PowerShell AST validation failed (%s on %s): %v\n%s", psExe, runtime.GOOS, pErr, string(out))
		}
	}

	shResp, err := http.Get(ts.URL + "/agent/worker.sh?runner=claude&mode=poll&interval_min=15&cooldown_sec=10&server_name=lfh-lab")
	if err != nil {
		t.Fatalf("GET /agent/worker.sh: %v", err)
	}
	shBytes, _ := io.ReadAll(shResp.Body)
	shResp.Body.Close()
	shFile := filepath.Join(tmpDir, "worker.sh")
	if err := os.WriteFile(shFile, shBytes, 0644); err != nil {
		t.Fatal(err)
	}

	shPath := ""
	if p, lookErr := exec.LookPath("sh"); lookErr == nil && (runtime.GOOS != "windows" || !strings.Contains(strings.ToLower(p), `\system32\`)) {
		shPath = p
	} else if runtime.GOOS == "windows" {
		if _, statErr := os.Stat(`C:\Program Files\Git\bin\sh.exe`); statErr == nil {
			shPath = `C:\Program Files\Git\bin\sh.exe`
		}
	}
	if shPath != "" {
		cmd := exec.Command(shPath, "-n", shFile)
		if out, sErr := cmd.CombinedOutput(); sErr != nil {
			t.Fatalf("sh -n syntax validation failed (%s): %v\n%s", shPath, sErr, string(out))
		}
	}
}
