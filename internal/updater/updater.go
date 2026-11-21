package updater

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"lfh/internal/version"
)

type Config struct {
	Repo      string `json:"repo"`
	ProxyURL  string `json:"proxy_url"`
	AutoCheck bool   `json:"auto_check"`
}

type Status struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	Repo           string `json:"repo"`
	ProxyURL       string `json:"proxy_url"`
	AutoCheck      bool   `json:"auto_check"`
	ReleaseURL     string `json:"release_url,omitempty"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	AssetName      string `json:"asset_name,omitempty"`
	AssetSize      int64  `json:"asset_size,omitempty"`
	AssetURL       string `json:"asset_url,omitempty"`
	ChecksumsURL   string `json:"checksums_url,omitempty"`
	CheckedAt      string `json:"checked_at,omitempty"`
	State          string `json:"state"`    // idle, checking, downloading, verifying, swapping, restarting, done, error
	Progress       int    `json:"progress"` // 0..100
	Message        string `json:"message,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
}

type ReleaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Assets      []ReleaseAsset `json:"assets"`
}

type githubTag struct {
	Name string `json:"name"`
}

type HandoffFunc func(exePath, backupPath, targetVersion string) error
type EventCallback func(st Status)

type Manager struct {
	mu          sync.RWMutex
	opMu        sync.Mutex
	cfgPath     string
	cfg         Config
	status      Status
	apiBaseURL  string
	httpClient  *http.Client
	exePathFn   func() (string, error)
	verifyFn    func(ctx context.Context, binPath string) error
	handoffFn   HandoffFunc
	onAvailable EventCallback
	onProgress  EventCallback
}

func NewManager(dataDir string, defaultRepo string) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	repo := NormalizeRepo(defaultRepo)
	if repo == "" {
		repo = version.DefaultRepo
	}
	m := &Manager{
		cfgPath:    filepath.Join(dataDir, "updater.json"),
		apiBaseURL: "https://api.github.com",
		httpClient: &http.Client{Timeout: 30 * time.Second},
		exePathFn:  os.Executable,
		verifyFn:   defaultVerifyBinary,
		cfg: Config{
			Repo:      repo,
			AutoCheck: true,
		},
		status: Status{
			CurrentVersion: version.Version,
			Repo:           repo,
			AutoCheck:      true,
			State:          "idle",
			OS:             runtime.GOOS,
			Arch:           runtime.GOARCH,
		},
	}
	m.loadConfig()
	if strings.TrimSpace(defaultRepo) != "" {
		m.cfg.Repo = NormalizeRepo(defaultRepo)
	}
	m.status.Repo = m.cfg.Repo
	m.status.ProxyURL = m.cfg.ProxyURL
	m.status.AutoCheck = m.cfg.AutoCheck
	return m, nil
}

func (m *Manager) SetCallbacks(handoff HandoffFunc, onAvailable, onProgress EventCallback) {
	m.mu.Lock()
	m.handoffFn = handoff
	m.onAvailable = onAvailable
	m.onProgress = onProgress
	m.mu.Unlock()
}

func (m *Manager) loadConfig() {
	raw, err := os.ReadFile(m.cfgPath)
	if err != nil {
		return
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return
	}
	if clean := NormalizeRepo(cfg.Repo); clean != "" {
		m.cfg.Repo = clean
	}
	m.cfg.ProxyURL = NormalizeProxyURL(cfg.ProxyURL)
	m.cfg.AutoCheck = cfg.AutoCheck
}

func (m *Manager) saveConfigLocked() error {
	raw, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.cfgPath, raw, 0600)
}

func NormalizeRepo(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.Trim(s, "/")
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

func NormalizeProxyURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) UpdateConfig(repo string, proxyURL string, autoCheck *bool) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(repo) != "" {
		clean := NormalizeRepo(repo)
		if clean == "" {
			return Status{}, errors.New("GitHub 仓库格式错误，请使用 owner/repo（如 sxu99579-bit/LFH）")
		}
		m.cfg.Repo = clean
	}
	m.cfg.ProxyURL = NormalizeProxyURL(proxyURL)
	if autoCheck != nil {
		m.cfg.AutoCheck = *autoCheck
	}
	if err := m.saveConfigLocked(); err != nil {
		return Status{}, err
	}
	m.status.Repo = m.cfg.Repo
	m.status.ProxyURL = m.cfg.ProxyURL
	m.status.AutoCheck = m.cfg.AutoCheck
	return m.status, nil
}

func (m *Manager) Start(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(12 * time.Second):
		}
		m.maybeAutoCheck(ctx)

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.maybeAutoCheck(ctx)
			}
		}
	}()
}

func (m *Manager) maybeAutoCheck(ctx context.Context) {
	m.mu.RLock()
	enabled := m.cfg.AutoCheck
	m.mu.RUnlock()
	if !enabled {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, _ = m.CheckNow(cctx)
}

func CompareVersions(a, b string) int {
	pa := parseVersionParts(a)
	pb := parseVersionParts(b)
	n := max(len(pa), len(pb))
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

func parseVersionParts(raw string) []int {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "vV")
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return []int{0}
	}
	segs := strings.Split(s, ".")
	out := make([]int, 0, len(segs))
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		digits := ""
		for _, r := range seg {
			if r >= '0' && r <= '9' {
				digits += string(r)
			} else {
				break
			}
		}
		if digits == "" {
			out = append(out, 0)
			continue
		}
		v, _ := strconv.Atoi(digits)
		out = append(out, v)
	}
	return out
}

func SelectAsset(assets []ReleaseAsset, goos, goarch string) (ReleaseAsset, string) {
	var best ReleaseAsset
	bestScore := -1
	var checksumsURL string

	goosLow := strings.ToLower(goos)
	archAliases := []string{strings.ToLower(goarch)}
	switch strings.ToLower(goarch) {
	case "amd64":
		archAliases = append(archAliases, "x86_64", "x64")
	case "arm64":
		archAliases = append(archAliases, "aarch64")
	}

	osAliases := []string{goosLow}
	if goosLow == "darwin" {
		osAliases = append(osAliases, "macos")
	}

	for _, a := range assets {
		low := strings.ToLower(strings.TrimSpace(a.Name))
		if low == "checksums.txt" || low == "sha256sums.txt" || low == "sha256sum.txt" || strings.HasSuffix(low, "checksums.txt") {
			checksumsURL = a.BrowserDownloadURL
			continue
		}
		if strings.HasSuffix(low, ".sha256") || strings.HasSuffix(low, ".sig") || strings.HasSuffix(low, ".asc") || strings.HasSuffix(low, ".txt") {
			continue
		}

		osMatch := false
		for _, oa := range osAliases {
			if strings.Contains(low, oa) {
				osMatch = true
				break
			}
		}
		archMatch := false
		for _, aa := range archAliases {
			if strings.Contains(low, aa) {
				archMatch = true
				break
			}
		}
		if !osMatch || !archMatch {
			continue
		}

		score := 10
		if strings.HasSuffix(low, ".zip") || strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") {
			score = 20
		} else if goosLow == "windows" && strings.HasSuffix(low, ".exe") {
			score = 40
		} else if goosLow != "windows" && !strings.HasSuffix(low, ".exe") && !strings.Contains(filepath.Base(low), ".") {
			score = 40
		} else if !strings.HasSuffix(low, ".exe") || goosLow == "windows" {
			score = 30
		}

		if score > bestScore {
			bestScore = score
			best = a
		}
	}
	return best, checksumsURL
}

func (m *Manager) applyProxy(rawURL string) string {
	m.mu.RLock()
	proxy := m.cfg.ProxyURL
	m.mu.RUnlock()
	if proxy == "" || rawURL == "" {
		return rawURL
	}
	if strings.HasPrefix(rawURL, proxy+"/") {
		return rawURL
	}
	return proxy + "/" + rawURL
}

func (m *Manager) CheckNow(ctx context.Context) (Status, error) {
	if !m.opMu.TryLock() {
		return m.Status(), errors.New("正在执行检查或升级操作，请稍候")
	}
	defer m.opMu.Unlock()

	m.mu.Lock()
	prevHasUpdate := m.status.HasUpdate
	prevLatest := m.status.LatestVersion
	repo := m.cfg.Repo
	m.status.State = "checking"
	m.status.LastError = ""
	m.status.Message = "正在从 GitHub 查询最新版本..."
	m.mu.Unlock()

	relURL := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(m.apiBaseURL, "/"), repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relURL, nil)
	if err != nil {
		return m.failCheck("构建请求失败: " + err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LFH/"+version.Version)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return m.failCheck("连接 GitHub 失败: " + err.Error())
	}
	defer resp.Body.Close()

	nowStr := time.Now().Format("2006-01-02 15:04:05")

	if resp.StatusCode == http.StatusNotFound {
		return m.checkTagsFallback(ctx, repo, nowStr)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return m.failCheck(fmt.Sprintf("GitHub API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return m.failCheck("解析 Release 数据失败: " + err.Error())
	}

	latest := strings.TrimSpace(rel.TagName)
	if latest == "" {
		latest = strings.TrimSpace(rel.Name)
	}
	hasUpdate := latest != "" && CompareVersions(latest, version.Version) > 0
	asset, checksumsURL := SelectAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)

	msg := "当前已是最新版本"
	if hasUpdate {
		if asset.Name != "" {
			msg = fmt.Sprintf("发现新版本 %s，可立即不停机在线升级", latest)
		} else {
			msg = fmt.Sprintf("发现新版本 %s，但暂未匹配到 %s/%s 安装包", latest, runtime.GOOS, runtime.GOARCH)
		}
	}

	m.mu.Lock()
	m.status.LatestVersion = latest
	m.status.HasUpdate = hasUpdate
	m.status.ReleaseURL = rel.HTMLURL
	m.status.ReleaseNotes = rel.Body
	m.status.PublishedAt = rel.PublishedAt
	m.status.AssetName = asset.Name
	m.status.AssetSize = asset.Size
	m.status.AssetURL = asset.BrowserDownloadURL
	m.status.ChecksumsURL = checksumsURL
	m.status.CheckedAt = nowStr
	m.status.State = "idle"
	m.status.Progress = 0
	m.status.Message = msg
	m.status.LastError = ""
	st := m.status
	cb := m.onAvailable
	m.mu.Unlock()

	if hasUpdate && cb != nil && (!prevHasUpdate || prevLatest != latest) {
		cb(st)
	}
	return st, nil
}

func (m *Manager) checkTagsFallback(ctx context.Context, repo, nowStr string) (Status, error) {
	tagURL := fmt.Sprintf("%s/repos/%s/tags?per_page=1", strings.TrimRight(m.apiBaseURL, "/"), repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagURL, nil)
	if err != nil {
		return m.failCheck("构建标签请求失败: " + err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LFH/"+version.Version)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return m.failCheck("查询仓库标签失败: " + err.Error())
	}
	defer resp.Body.Close()

	var tags []githubTag
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&tags)
	}

	m.mu.Lock()
	m.status.CheckedAt = nowStr
	m.status.State = "idle"
	m.status.Progress = 0
	m.status.LastError = ""
	m.status.AssetName = ""
	m.status.AssetSize = 0
	m.status.AssetURL = ""
	m.status.ChecksumsURL = ""
	if len(tags) > 0 && CompareVersions(tags[0].Name, version.Version) > 0 {
		m.status.LatestVersion = tags[0].Name
		m.status.HasUpdate = true
		m.status.ReleaseURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, tags[0].Name)
		m.status.Message = fmt.Sprintf("发现新标签 %s，请等待 GitHub Actions 上传二进制 Release 后在线升级", tags[0].Name)
	} else {
		m.status.LatestVersion = version.Version
		m.status.HasUpdate = false
		m.status.ReleaseURL = fmt.Sprintf("https://github.com/%s/releases", repo)
		m.status.Message = "仓库暂未发布更新的版本（当前为最新 " + version.Version + "）"
	}
	st := m.status
	m.mu.Unlock()
	return st, nil
}

func (m *Manager) failCheck(msg string) (Status, error) {
	m.mu.Lock()
	m.status.State = "error"
	m.status.LastError = msg
	m.status.Message = msg
	m.status.CheckedAt = time.Now().Format("2006-01-02 15:04:05")
	st := m.status
	m.mu.Unlock()
	return st, errors.New(msg)
}

func (m *Manager) StartApplyAsync() (Status, error) {
	if !m.opMu.TryLock() {
		return m.Status(), errors.New("正在执行检查或升级操作，请稍候")
	}

	m.mu.RLock()
	st := m.status
	m.mu.RUnlock()

	if !st.HasUpdate {
		m.opMu.Unlock()
		return st, errors.New("当前没有可升级的新版本，请先点击检查更新")
	}
	if st.AssetURL == "" {
		m.opMu.Unlock()
		return st, errors.New("该 Release 未附带当前系统架构的可执行程序")
	}

	m.setProgress("downloading", 2, "正在准备下载新版本 "+st.LatestVersion+"...")
	go func() {
		defer m.opMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = m.applyUpdateLocked(ctx)
	}()

	return m.Status(), nil
}

func (m *Manager) ApplyUpdateSync(ctx context.Context) error {
	if !m.opMu.TryLock() {
		return errors.New("正在执行检查或升级操作，请稍候")
	}
	defer m.opMu.Unlock()
	return m.applyUpdateLocked(ctx)
}

// LocalBinaryForPlatform locates a local binary matching targetOS/targetArch (self executable or dist/ build) and returns its path, SHA-256, and size.
func (m *Manager) LocalBinaryForPlatform(targetOS, targetArch string) (string, string, int64, error) {
	goos := strings.ToLower(strings.TrimSpace(targetOS))
	goarch := strings.ToLower(strings.TrimSpace(targetArch))
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	m.mu.RLock()
	exeFn := m.exePathFn
	m.mu.RUnlock()

	var candidates []string
	if goos == runtime.GOOS && goarch == runtime.GOARCH && exeFn != nil {
		if exePath, err := exeFn(); err == nil && exePath != "" {
			if resolved, rErr := filepath.EvalSymlinks(exePath); rErr == nil && resolved != "" {
				exePath = resolved
			}
			candidates = append(candidates, exePath)
		}
	}

	assetName := fmt.Sprintf("lfh-%s-%s", goos, goarch)
	if goos == "windows" {
		assetName += ".exe"
	}
	candidates = append(candidates, filepath.Join("dist", assetName))
	if exeFn != nil {
		if exePath, err := exeFn(); err == nil && exePath != "" {
			candidates = append(candidates, filepath.Join(filepath.Dir(exePath), "dist", assetName))
		}
	}
	if goos == "windows" && goarch == "amd64" {
		candidates = append(candidates, "lfh.exe")
	}

	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, f)
		_ = f.Close()
		if copyErr != nil || n <= 0 {
			continue
		}
		return p, hex.EncodeToString(h.Sum(nil)), n, nil
	}
	return "", "", 0, fmt.Errorf("未找到适用于 %s/%s 的本地二进制安装包", goos, goarch)
}

// ApplyBinaryStream verifies, installs, and triggers hot-handoff from a binary stream pushed or pulled from the cluster primary node.
func (m *Manager) ApplyBinaryStream(ctx context.Context, r io.Reader, targetVersion, expectedSHA256 string, asyncHandoff bool) error {
	if !m.opMu.TryLock() {
		return errors.New("节点正在执行升级操作，请稍候")
	}
	unlocked := false
	defer func() {
		if !unlocked {
			m.opMu.Unlock()
		}
	}()

	m.mu.RLock()
	exeFn := m.exePathFn
	verifyFn := m.verifyFn
	handoffFn := m.handoffFn
	m.mu.RUnlock()

	exePath, err := exeFn()
	if err != nil {
		return m.failApply(fmt.Errorf("获取当前程序路径失败: %w", err))
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil && resolved != "" {
		exePath = resolved
	}

	tmpDir, err := os.MkdirTemp("", "lfh-cluster-sync-*")
	if err != nil {
		return m.failApply(err)
	}
	defer os.RemoveAll(tmpDir)

	tmpBin := filepath.Join(tmpDir, "lfh-sync.bin")
	out, err := os.OpenFile(tmpBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return m.failApply(err)
	}
	m.setProgress("downloading", 25, "正在接收主节点同步版本 "+targetVersion+"...")
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), r)
	closeErr := out.Close()
	if copyErr != nil {
		return m.failApply(fmt.Errorf("接收同步程序失败: %w", copyErr))
	}
	if closeErr != nil {
		return m.failApply(closeErr)
	}
	if written <= 0 {
		return m.failApply(errors.New("接收到的升级程序内容为空"))
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	cleanExpected := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if cleanExpected != "" && !strings.EqualFold(actualSHA, cleanExpected) {
		return m.failApply(fmt.Errorf("同步程序 SHA-256 校验不一致 (期望 %s，实际 %s)", cleanExpected, actualSHA))
	}

	newPath := exePath + ".new"
	backupPath := exePath + ".old"
	_ = os.Remove(newPath)
	if err := copyFile(tmpBin, newPath, 0755); err != nil {
		return m.failApply(fmt.Errorf("写入新版本文件失败: %w", err))
	}

	m.setProgress("verifying", 90, "正在校验同步版本可启动性...")
	if verifyFn != nil {
		if err := verifyFn(ctx, newPath); err != nil {
			_ = os.Remove(newPath)
			return m.failApply(fmt.Errorf("新版本自检失败: %w", err))
		}
	}

	m.setProgress("swapping", 96, "正在原子替换可执行程序...")
	_ = os.Remove(backupPath)
	if err := os.Rename(exePath, backupPath); err != nil {
		_ = os.Remove(newPath)
		return m.failApply(fmt.Errorf("备份原程序失败: %w", err))
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(backupPath, exePath)
		_ = os.Remove(newPath)
		return m.failApply(fmt.Errorf("替换新版本程序失败: %w", err))
	}

	m.setProgress("restarting", 99, "正在平滑切换至主节点版本 "+targetVersion+"...")
	if handoffFn != nil {
		if asyncHandoff {
			unlocked = true
			go func() {
				defer m.opMu.Unlock()
				time.Sleep(150 * time.Millisecond)
				if err := handoffFn(exePath, backupPath, targetVersion); err != nil {
					_ = os.Remove(exePath)
					_ = os.Rename(backupPath, exePath)
					_ = m.failApply(fmt.Errorf("热切换新版本失败，已回滚: %w", err))
					return
				}
				m.setProgress("done", 100, "已同步至版本 "+targetVersion)
			}()
			return nil
		}
		if err := handoffFn(exePath, backupPath, targetVersion); err != nil {
			_ = os.Remove(exePath)
			_ = os.Rename(backupPath, exePath)
			return m.failApply(fmt.Errorf("热切换新版本失败，已回滚: %w", err))
		}
	}
	m.setProgress("done", 100, "已同步至版本 "+targetVersion)
	return nil
}

func (m *Manager) applyUpdateLocked(ctx context.Context) error {
	m.mu.RLock()
	st := m.status
	exeFn := m.exePathFn
	verifyFn := m.verifyFn
	handoffFn := m.handoffFn
	m.mu.RUnlock()

	if !st.HasUpdate || st.AssetURL == "" {
		return m.failApply(errors.New("没有可下载的升级安装包"))
	}

	exePath, err := exeFn()
	if err != nil {
		return m.failApply(fmt.Errorf("获取当前程序路径失败: %w", err))
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil && resolved != "" {
		exePath = resolved
	}

	tmpDir, err := os.MkdirTemp("", "lfh-update-*")
	if err != nil {
		return m.failApply(err)
	}
	defer os.RemoveAll(tmpDir)

	dlPath := filepath.Join(tmpDir, filepath.Base(st.AssetName))
	dlClient := &http.Client{Timeout: 4 * time.Minute}
	m.setProgress("downloading", 5, "正在下载 "+st.AssetName+"...")
	actualSHA, err := m.downloadFile(ctx, dlClient, m.applyProxy(st.AssetURL), dlPath, st.AssetSize)
	if err != nil {
		return m.failApply(fmt.Errorf("下载升级包失败: %w", err))
	}

	m.setProgress("verifying", 86, "正在校验文件完整性...")
	if st.ChecksumsURL != "" {
		if err := m.verifyChecksum(ctx, dlClient, m.applyProxy(st.ChecksumsURL), st.AssetName, actualSHA); err != nil {
			return m.failApply(err)
		}
	}

	binTemp := dlPath
	lowName := strings.ToLower(st.AssetName)
	if strings.HasSuffix(lowName, ".zip") {
		binTemp, err = extractFromZip(dlPath, tmpDir)
		if err != nil {
			return m.failApply(fmt.Errorf("解压 ZIP 升级包失败: %w", err))
		}
	} else if strings.HasSuffix(lowName, ".tar.gz") || strings.HasSuffix(lowName, ".tgz") {
		binTemp, err = extractFromTarGz(dlPath, tmpDir)
		if err != nil {
			return m.failApply(fmt.Errorf("解压 TAR.GZ 升级包失败: %w", err))
		}
	}

	newPath := exePath + ".new"
	backupPath := exePath + ".old"
	_ = os.Remove(newPath)

	if err := copyFile(binTemp, newPath, 0755); err != nil {
		return m.failApply(fmt.Errorf("写入新版本文件失败（请检查目录写入权限）: %w", err))
	}

	m.setProgress("verifying", 92, "正在校验新版本可启动性...")
	if verifyFn != nil {
		if err := verifyFn(ctx, newPath); err != nil {
			_ = os.Remove(newPath)
			return m.failApply(fmt.Errorf("新版本自检失败: %w", err))
		}
	}

	m.setProgress("swapping", 96, "正在原子替换可执行文件...")
	_ = os.Remove(backupPath)
	if err := os.Rename(exePath, backupPath); err != nil {
		_ = os.Remove(newPath)
		return m.failApply(fmt.Errorf("备份当前程序失败: %w", err))
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(backupPath, exePath)
		_ = os.Remove(newPath)
		return m.failApply(fmt.Errorf("替换为新版本失败: %w", err))
	}

	m.setProgress("restarting", 99, "正在不停机平滑切换至新版本 "+st.LatestVersion+"...")
	if handoffFn != nil {
		if err := handoffFn(exePath, backupPath, st.LatestVersion); err != nil {
			_ = os.Remove(exePath)
			_ = os.Rename(backupPath, exePath)
			return m.failApply(fmt.Errorf("热切换新版本失败，已自动回滚: %w", err))
		}
	}

	m.setProgress("done", 100, "已切换至新版本 "+st.LatestVersion)
	return nil
}

func (m *Manager) setProgress(state string, progress int, msg string) {
	m.mu.Lock()
	m.status.State = state
	m.status.Progress = progress
	m.status.Message = msg
	m.status.LastError = ""
	st := m.status
	cb := m.onProgress
	m.mu.Unlock()
	if cb != nil {
		cb(st)
	}
}

func (m *Manager) failApply(err error) error {
	m.mu.Lock()
	m.status.State = "error"
	m.status.LastError = err.Error()
	m.status.Message = err.Error()
	st := m.status
	cb := m.onProgress
	m.mu.Unlock()
	if cb != nil {
		cb(st)
	}
	return err
}

func (m *Manager) downloadFile(ctx context.Context, client *http.Client, urlStr, dstPath string, expectedSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "LFH/"+version.Version)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := expectedSize
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	defer out.Close()

	hasher := sha256.New()
	buf := make([]byte, 64*1024)
	var written int64
	lastPct := -1

	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return "", werr
			}
			_, _ = hasher.Write(buf[:n])
			written += int64(n)
			if total > 0 {
				pct := 5 + int((written*80)/total)
				if pct > 85 {
					pct = 85
				}
				if pct >= lastPct+5 {
					lastPct = pct
					m.setProgress("downloading", pct, fmt.Sprintf("正在下载升级包 (%d%%)...", pct))
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (m *Manager) verifyChecksum(ctx context.Context, client *http.Client, urlStr, assetName, actualSHA string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LFH/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载 SHA256 校验文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("获取 SHA256 校验文件失败: HTTP %d", resp.StatusCode)
	}

	targetBase := filepath.Base(assetName)
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 512*1024))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		fileCol := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(fileCol) == targetBase {
			expected := strings.ToLower(strings.TrimSpace(fields[0]))
			if !strings.EqualFold(expected, actualSHA) {
				return fmt.Errorf("SHA256 校验不匹配（期望 %s，实际 %s）", expected, actualSHA)
			}
			return nil
		}
	}
	return nil
}

func defaultVerifyBinary(ctx context.Context, binPath string) error {
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, binPath, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), version.AppName) {
		return fmt.Errorf("输出异常: %q", strings.TrimSpace(string(out)))
	}
	return nil
}

func CleanupOldBinary(exePath string) {
	if exePath == "" {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil && resolved != "" {
		exePath = resolved
	}
	oldPath := exePath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		return
	}
	go func() {
		for i := 0; i < 12; i++ {
			if err := os.Remove(oldPath); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

func isMatchingBinaryEntry(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	if runtime.GOOS == "windows" {
		return base == "lfh.exe" || (strings.HasPrefix(base, "lfh") && strings.HasSuffix(base, ".exe"))
	}
	return base == "lfh" || (strings.HasPrefix(base, "lfh-") && !strings.HasSuffix(base, ".exe") && !strings.HasSuffix(base, ".txt"))
}

func extractFromZip(zipPath, dstDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if isMatchingBinaryEntry(f.Name) {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			outPath := filepath.Join(dstDir, "extracted-bin")
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				_ = rc.Close()
				return "", err
			}
			_, copyErr := io.Copy(out, io.LimitReader(rc, 256*1024*1024))
			_ = rc.Close()
			closeErr := out.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			return outPath, nil
		}
	}
	return "", errors.New("ZIP 包内未找到可执行文件")
}

func extractFromTarGz(tgzPath, dstDir string) (string, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if isMatchingBinaryEntry(hdr.Name) {
			outPath := filepath.Join(dstDir, "extracted-bin")
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(out, io.LimitReader(tr, 256*1024*1024))
			closeErr := out.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			return outPath, nil
		}
	}
	return "", errors.New("TAR.GZ 包内未找到可执行文件")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
