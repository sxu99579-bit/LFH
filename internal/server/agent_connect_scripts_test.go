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

func TestAgentConnectScriptsRenderAndInjectionSafety(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	for _, ep := range []string{"/agent/connect.ps1", "/agent/connect.sh"} {
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
		for _, want := range []string{ts.URL, "/api/agent-pairing/request", "X-Pairing-Secret", "whoami"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing expected marker %q", ep, want)
			}
		}

		// Hostile query params must be sanitized cleanly with zero shell/PS injection.
		hostile := ts.URL + ep + "?client=" + url.QueryEscape("cursor';rm -rf /;") +
			"&owner=" + url.QueryEscape("bob$(id)") +
			"&server_name=" + url.QueryEscape("My_Server!@#") +
			"&name=" + url.QueryEscape("foo'bar$(whoami)")
		hResp, err := http.Get(hostile)
		if err != nil {
			t.Fatalf("GET hostile %s: %v", ep, err)
		}
		hRaw, _ := io.ReadAll(hResp.Body)
		hResp.Body.Close()
		hBody := string(hRaw)

		if strings.Contains(hBody, "$(id)") || strings.Contains(hBody, "$(whoami)") || strings.Contains(hBody, "rm -rf") {
			t.Fatalf("%s leaked unsanitized query param into script", ep)
		}
		if ep == "/agent/connect.ps1" {
			if !strings.Contains(hBody, "$clientPref = 'auto'") ||
				!strings.Contains(hBody, "$targetOwner = 'bobid'") ||
				!strings.Contains(hBody, "$serverName = 'my_server'") ||
				!strings.Contains(hBody, "$customName = 'foobarwhoami'") {
				t.Fatalf("connect.ps1 unexpected sanitized assignments:\n%s", hBody)
			}
		} else {
			if !strings.Contains(hBody, "CLIENT_PREF='auto'") ||
				!strings.Contains(hBody, "TARGET_OWNER='bobid'") ||
				!strings.Contains(hBody, "SERVER_NAME='my_server'") ||
				!strings.Contains(hBody, "CUSTOM_NAME='foobarwhoami'") {
				t.Fatalf("connect.sh unexpected sanitized assignments")
			}
		}
	}
}

func TestAgentConnectScriptsSyntaxValidation(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	tmpDir := t.TempDir()

	// Validate PowerShell AST when powershell.exe (or pwsh) is available.
	psResp, err := http.Get(ts.URL + "/agent/connect.ps1?client=cursor&server_name=lfh-lab&name=TestAgent")
	if err != nil {
		t.Fatalf("GET /agent/connect.ps1: %v", err)
	}
	psBytes, _ := io.ReadAll(psResp.Body)
	psResp.Body.Close()
	psFile := filepath.Join(tmpDir, "connect.ps1")
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

	// Validate POSIX sh syntax when sh is available.
	shResp, err := http.Get(ts.URL + "/agent/connect.sh?client=antigravity&server_name=lfh-lab&name=TestAgent")
	if err != nil {
		t.Fatalf("GET /agent/connect.sh: %v", err)
	}
	shBytes, _ := io.ReadAll(shResp.Body)
	shResp.Body.Close()
	shFile := filepath.Join(tmpDir, "connect.sh")
	if err := os.WriteFile(shFile, shBytes, 0644); err != nil {
		t.Fatal(err)
	}

	if shPath, lookErr := exec.LookPath("sh"); lookErr == nil {
		// Skip WSL launcher shim on Windows if it has no distro installed; still test real POSIX sh.
		if runtime.GOOS != "windows" || !strings.Contains(strings.ToLower(shPath), `\system32\`) {
			cmd := exec.Command(shPath, "-n", shFile)
			if out, sErr := cmd.CombinedOutput(); sErr != nil {
				t.Fatalf("sh -n syntax validation failed (%s): %v\n%s", shPath, sErr, string(out))
			}
		}
	}
}
