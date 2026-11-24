package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"lfh/internal/storage"
)

const maxVerifyTailBytes = 128 << 10 // Keep strictly the final 128KB of build/test output.

type VerifyResult struct {
	OK         bool      `json:"ok"`
	MountID    string    `json:"mount_id"`
	MountAlias string    `json:"mount_alias"`
	PresetID   string    `json:"preset_id"`
	Label      string    `json:"label"`
	ExitCode   int       `json:"exit_code"`
	TimedOut   bool      `json:"timed_out,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	TailOutput string    `json:"tail_output"`
	Truncated  bool      `json:"truncated,omitempty"`
	LogPath    string    `json:"log_path,omitempty"`
	Coalesced  bool      `json:"coalesced,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

type tailBuffer struct {
	mu        sync.Mutex
	maxBytes  int
	buf       []byte
	truncated bool
}

func newTailBuffer(maxBytes int) *tailBuffer {
	if maxBytes <= 0 {
		maxBytes = maxVerifyTailBytes
	}
	return &tailBuffer{maxBytes: maxBytes}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(p) >= b.maxBytes {
		b.buf = append(b.buf[:0], p[len(p)-b.maxBytes:]...)
		b.truncated = true
		return len(p), nil
	}
	total := len(b.buf) + len(p)
	if total > b.maxBytes {
		drop := total - b.maxBytes
		copy(b.buf, b.buf[drop:])
		b.buf = b.buf[:len(b.buf)-drop]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *tailBuffer) Bytes() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := append([]byte(nil), b.buf...)
	return cp, b.truncated
}

type verifyCall struct {
	done chan struct{}
	res  VerifyResult
	err  error
}

// VerifyRunner executes pre-approved admin VerifyPresets safely without shell interpolation,
// coalescing overlapping triggers for the same (mountID, presetID) pair.
type VerifyRunner struct {
	mu       sync.Mutex
	inflight map[string]*verifyCall
}

func NewVerifyRunner() *VerifyRunner {
	return &VerifyRunner{
		inflight: make(map[string]*verifyCall),
	}
}

func isSubOrSameOSPath(candidate, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ensureNoSymlinkInsideRoot(root, target string) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if !isSubOrSameOSPath(cleanTarget, cleanRoot) {
		return errors.New("工作或日志路径超出挂载目录范围")
	}
	rootReal, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return fmt.Errorf("无法解析挂载根目录: %w", err)
	}
	if cleanTarget == cleanRoot {
		return nil
	}
	parent := filepath.Dir(cleanTarget)
	if mkErr := os.MkdirAll(parent, 0755); mkErr != nil {
		return mkErr
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return err
	}
	cur := cleanRoot
	if rel != "." {
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, seg)
			fi, lErr := os.Lstat(cur)
			if os.IsNotExist(lErr) {
				break
			}
			if lErr != nil {
				return lErr
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return errors.New("安全策略禁止通过符号链接执行校验或写入日志")
			}
		}
	}
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if !isSubOrSameOSPath(strings.ToLower(parentReal), strings.ToLower(rootReal)) {
			return errors.New("目标父目录解析路径超出挂载范围")
		}
	} else if !isSubOrSameOSPath(parentReal, rootReal) {
		return errors.New("目标父目录解析路径超出挂载范围")
	}
	return nil
}

// Run executes preset against mount with single-flight coalescing per (mount.ID, preset.ID).
func (r *VerifyRunner) Run(ctx context.Context, mount storage.MountEntry, preset storage.VerifyPreset) (VerifyResult, error) {
	key := mount.ID + "|" + strings.ToLower(strings.TrimSpace(preset.ID))

	r.mu.Lock()
	if r.inflight == nil {
		r.inflight = make(map[string]*verifyCall)
	}
	if active, exists := r.inflight[key]; exists {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return VerifyResult{}, ctx.Err()
		case <-active.done:
			res := active.res
			res.Coalesced = true
			return res, active.err
		}
	}
	call := &verifyCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.inflight, key)
		r.mu.Unlock()
		close(call.done)
	}()

	res, err := r.execOnce(ctx, mount, preset)
	call.res = res
	call.err = err
	return res, err
}

func (r *VerifyRunner) execOnce(ctx context.Context, mount storage.MountEntry, preset storage.VerifyPreset) (VerifyResult, error) {
	cleanRoot := filepath.Clean(mount.DirPath)
	workDir := cleanRoot
	if strings.TrimSpace(preset.WorkSubDir) != "" {
		workDir = filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(preset.WorkSubDir)))
	}
	if err := ensureNoSymlinkInsideRoot(cleanRoot, workDir); err != nil {
		return VerifyResult{}, err
	}
	info, stErr := os.Stat(workDir)
	if stErr != nil || !info.IsDir() {
		return VerifyResult{}, fmt.Errorf("校验工作目录不可访问: %s", preset.WorkSubDir)
	}

	timeout := time.Duration(preset.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Never invoke via shell (sh -c / cmd /c); execute command + static args directly.
	cmd := exec.CommandContext(runCtx, preset.Command, preset.Args...)
	cmd.Dir = workDir
	tail := newTailBuffer(maxVerifyTailBytes)
	cmd.Stdout = tail
	cmd.Stderr = tail

	start := time.Now()
	runErr := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return VerifyResult{}, fmt.Errorf("启动校验命令 %q 失败: %w", preset.Command, runErr)
		}
	}

	tailBytes, truncated := tail.Bytes()

	logRel := strings.TrimSpace(preset.LogRelPath)
	if logRel == "" {
		logRel = ".lfh-logs/" + preset.ID + ".log"
	}
	var logicalLogPath string
	if !storage.IsBlockedMountRelPath(logRel) {
		absLog := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(logRel)))
		if err := ensureNoSymlinkInsideRoot(cleanRoot, absLog); err == nil {
			header := fmt.Sprintf("=== LFH Verify [%s] mount=%s exit=%d timed_out=%v duration=%dms at=%s ===\n",
				preset.ID, mount.Alias, exitCode, timedOut, durationMS, time.Now().Format(time.RFC3339))
			var combined []byte
			combined = append(combined, header...)
			combined = append(combined, tailBytes...)
			if tmp, tErr := os.CreateTemp(filepath.Dir(absLog), ".lfh_tmp_*"); tErr == nil {
				tmpPath := tmp.Name()
				_, wErr := tmp.Write(combined)
				cErr := tmp.Close()
				if wErr == nil && cErr == nil && os.Rename(tmpPath, absLog) == nil {
					logicalLogPath = storage.SanitizeFolderPath(mount.Alias) + "/" + strings.TrimLeft(filepath.ToSlash(logRel), "/")
				} else {
					_ = os.Remove(tmpPath)
				}
			}
		}
	}

	return VerifyResult{
		OK:         runErr == nil && !timedOut && exitCode == 0,
		MountID:    mount.ID,
		MountAlias: mount.Alias,
		PresetID:   preset.ID,
		Label:      preset.Label,
		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMS: durationMS,
		TailOutput: string(tailBytes),
		Truncated:  truncated,
		LogPath:    logicalLogPath,
		FinishedAt: time.Now(),
	}, nil
}
