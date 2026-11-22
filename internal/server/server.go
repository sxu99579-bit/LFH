package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
	"lfh/internal/clipboard"
	"lfh/internal/cluster"
	"lfh/internal/preview"
	"lfh/internal/storage"
	"lfh/internal/updater"
	"lfh/internal/version"
)

type NetworkAddr struct {
	URL     string `json:"url"`
	IP      string `json:"ip"`
	Iface   string `json:"iface"`
	Tag     string `json:"tag"`
	Primary bool   `json:"primary"`
	Enabled bool   `json:"enabled"`
}

type Server struct {
	mu           sync.RWMutex
	auth         *auth.Manager
	store        *storage.Store
	cluster      *cluster.Manager
	events       *EventHub
	updater      *updater.Manager
	webFS        fs.FS
	port         string
	portSwitcher func(newPort string) error
}

func New(authMgr *auth.Manager, store *storage.Store, clusterMgr *cluster.Manager, embeddedWeb embed.FS, port string) (*Server, error) {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		return nil, err
	}
	return &Server{
		auth:    authMgr,
		store:   store,
		cluster: clusterMgr,
		events:  NewEventHub(),
		webFS:   sub,
		port:    port,
	}, nil
}

func (s *Server) SetUpdater(u *updater.Manager) {
	s.mu.Lock()
	s.updater = u
	s.mu.Unlock()
	if s.cluster != nil {
		s.cluster.SetOnPeerVersionSync(func(peer cluster.NodeStatus) {
			_, _ = s.syncSinglePeerVersion(peer)
		})
		s.cluster.SetOnEntryVersionAhead(func(entryURL, entryVersion string) {
			_ = s.pullAndInstallFromEntry(entryURL, entryVersion)
		})
	}
}

func (s *Server) getUpdater() *updater.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updater
}

func (s *Server) PublishEvent(ev FileChangeEvent) {
	if s.events != nil {
		s.events.Publish(ev)
	}
}

func (s *Server) PublishUpgrading(newVersion string) {
	if s.events != nil {
		s.events.Publish(FileChangeEvent{
			Kind:     "upgrading",
			NodeName: newVersion,
		})
	}
}

func (s *Server) CloseEventStreams() {
	if s.events != nil {
		s.events.Close()
	}
}

func (s *Server) SetPortSwitcher(fn func(newPort string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.portSwitcher = fn
}

func (s *Server) Port() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.port == "" {
		return "8080"
	}
	return s.port
}

func PrimaryEntryURL(port string) string {
	addrs := DiscoverNetworkAddrs(port)
	for _, a := range addrs {
		if a.Primary {
			return a.URL
		}
	}
	if len(addrs) > 0 {
		return addrs[0].URL
	}
	return "http://localhost:" + port
}

func ResolveEntryURL(exposeIP, port string) string {
	clean, err := sanitizeExposeHost(exposeIP)
	if err == nil && clean != "" {
		return "http://" + clean + ":" + port
	}
	return PrimaryEntryURL(port)
}

func (s *Server) allConfiguredAddrs() ([]NetworkAddr, []NetworkAddr, string) {
	port := s.Port()
	all := DiscoverNetworkAddrs(port)
	exposePrimary := strings.TrimSpace(s.cluster.ExposeIP())
	enabledList := s.cluster.ExposeIPs()
	disabledList := s.cluster.DisabledIPs()

	enabledSet := make(map[string]bool, len(enabledList))
	for _, ip := range enabledList {
		if clean, err := sanitizeExposeHost(ip); err == nil && clean != "" {
			enabledSet[clean] = true
		}
	}
	disabledSet := make(map[string]bool, len(disabledList))
	for _, ip := range disabledList {
		if clean, err := sanitizeExposeHost(ip); err == nil && clean != "" {
			disabledSet[clean] = true
		}
	}

	knownIPs := make(map[string]bool, len(all))
	for i := range all {
		ip := all[i].IP
		knownIPs[ip] = true
		if disabledSet[ip] {
			all[i].Enabled = false
		} else if len(enabledSet) > 0 {
			all[i].Enabled = enabledSet[ip]
		} else {
			// Default when admin hasn't customized NIC toggles yet: enable non-virtual NICs (or primary)
			all[i].Enabled = all[i].Tag != "虚拟网卡" || all[i].Primary
		}
		if exposePrimary != "" && ip == exposePrimary {
			all[i].Enabled = true
		}
	}

	// Include any custom IP/hostname configured by admin that is not a local NIC
	appendCustom := func(raw string, isPrimary bool) {
		clean, err := sanitizeExposeHost(raw)
		if err != nil || clean == "" || knownIPs[clean] {
			return
		}
		knownIPs[clean] = true
		all = append(all, NetworkAddr{
			URL:     "http://" + clean + ":" + port,
			IP:      clean,
			Iface:   "自定义入口",
			Tag:     "自定义地址",
			Primary: isPrimary,
			Enabled: !disabledSet[clean],
		})
	}
	if exposePrimary != "" {
		appendCustom(exposePrimary, true)
	}
	for _, ip := range enabledList {
		appendCustom(ip, false)
	}

	// Ensure at least one address is enabled
	anyEnabled := false
	for _, a := range all {
		if a.Enabled {
			anyEnabled = true
			break
		}
	}
	if !anyEnabled && len(all) > 0 {
		all[0].Enabled = true
	}

	// Determine primary address among enabled addresses
	primaryIP := ""
	if exposePrimary != "" && !disabledSet[exposePrimary] {
		primaryIP = exposePrimary
	}
	if primaryIP == "" {
		for _, a := range all {
			if a.Enabled && a.Primary {
				primaryIP = a.IP
				break
			}
		}
	}
	if primaryIP == "" {
		for _, a := range all {
			if a.Enabled {
				primaryIP = a.IP
				break
			}
		}
	}

	active := make([]NetworkAddr, 0, len(all))
	primaryURL := ""
	for i := range all {
		all[i].Primary = (all[i].IP == primaryIP)
		if all[i].Enabled {
			active = append(active, all[i])
			if all[i].Primary {
				primaryURL = all[i].URL
			}
		}
	}
	// Put primary first in active list
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Primary != active[j].Primary {
			return active[i].Primary
		}
		return false
	})
	if primaryURL == "" {
		if len(active) > 0 {
			primaryURL = active[0].URL
		} else {
			primaryURL = ResolveEntryURL(exposePrimary, port)
		}
	}
	return all, active, primaryURL
}

func isMobileRequest(r *http.Request) bool {
	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view == "desktop" || view == "pc" {
		return false
	}
	if view == "mobile" || view == "m" {
		return true
	}
	if c, err := r.Cookie("lfh_view_mode"); err == nil {
		cv := strings.ToLower(strings.TrimSpace(c.Value))
		if cv == "desktop" || cv == "pc" {
			return false
		}
		if cv == "mobile" || cv == "m" {
			return true
		}
	}
	if strings.TrimSpace(r.Header.Get("Sec-CH-UA-Mobile")) == "?1" {
		return true
	}
	ua := strings.ToLower(r.UserAgent())
	mobileKeywords := []string{
		"iphone", "ipod", "android", "mobile", "phone",
		"harmonyos", "arkweb", "micromessenger", "ucbrowser",
		"quark", "baiduboxapp", "qqbrowser/mobile", "opera mini", "iemobile",
	}
	for _, kw := range mobileKeywords {
		if strings.Contains(ua, kw) {
			return true
		}
	}
	return false
}

func sanitizeExposeHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	if host, _, err := net.SplitHostPort(s); err == nil && host != "" {
		s = host
	}
	s = strings.TrimSpace(s)
	if len(s) > 128 {
		return "", errors.New("暴露地址长度不能超过 128 个字符")
	}
	for _, r := range s {
		if r <= 32 || r == '/' || r == '\\' || r == '?' || r == '#' || r == '@' {
			return "", errors.New("暴露 IP 或域名包含非法字符")
		}
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Internal node-to-node cluster endpoints (HMAC signed)
	mux.HandleFunc("POST /internal/cluster/heartbeat", s.requireClusterAuth(s.handleClusterHeartbeat))
	mux.HandleFunc("POST /internal/cluster/upload", s.requireClusterAuth(s.handleClusterUpload))
	mux.HandleFunc("GET /internal/cluster/files/{id}/download", s.requireClusterAuth(s.handleClusterDownload))
	mux.HandleFunc("GET /internal/cluster/files/{id}/raw", s.requireClusterAuth(s.handleClusterRaw))
	mux.HandleFunc("GET /internal/cluster/files/{id}/preview", s.requireClusterAuth(s.handleClusterPreview))
	mux.HandleFunc("GET /internal/cluster/files/{id}/meta", s.requireClusterAuth(s.handleClusterMeta))
	mux.HandleFunc("GET /internal/cluster/files/{id}/archive-entry", s.requireClusterAuth(s.handleClusterArchiveEntry))
	mux.HandleFunc("DELETE /internal/cluster/files/{id}", s.requireClusterAuth(s.handleClusterDelete))
	mux.HandleFunc("POST /internal/cluster/files/{id}/access", s.requireClusterAuth(s.handleClusterUpdateAccess))
	mux.HandleFunc("POST /internal/cluster/files/{id}/edit", s.requireClusterAuth(s.handleClusterEditFile))
	mux.HandleFunc("POST /internal/cluster/files/{id}/versions", s.requireClusterAuth(s.handleClusterNewVersion))
	mux.HandleFunc("GET /internal/cluster/files/{id}/versions/{ver}/download", s.requireClusterAuth(s.handleClusterDownloadVersion))
	mux.HandleFunc("POST /internal/cluster/files/{id}/versions/{ver}/restore", s.requireClusterAuth(s.handleClusterRestoreVersion))
	mux.HandleFunc("DELETE /internal/cluster/files/{id}/versions/{ver}", s.requireClusterAuth(s.handleClusterDeleteVersion))
	mux.HandleFunc("POST /internal/cluster/quota", s.requireClusterAuth(s.handleClusterSetQuota))
	mux.HandleFunc("POST /internal/cluster/storage-dir", s.requireClusterAuth(s.handleClusterSetStorageDir))
	mux.HandleFunc("POST /internal/cluster/clipboard", s.requireClusterAuth(s.handleClusterSetClipboard))
	mux.HandleFunc("POST /internal/cluster/import-local", s.requireClusterAuth(s.handleClusterImportLocal))
	mux.HandleFunc("GET /internal/cluster/mounts", s.requireClusterAuth(s.handleClusterListMounts))
	mux.HandleFunc("POST /internal/cluster/mounts", s.requireClusterAuth(s.handleClusterAddMount))
	mux.HandleFunc("DELETE /internal/cluster/mounts/{id}", s.requireClusterAuth(s.handleClusterRemoveMount))
	mux.HandleFunc("POST /internal/cluster/mounts/{id}/sync", s.requireClusterAuth(s.handleClusterSyncMount))
	mux.HandleFunc("POST /internal/cluster/promote", s.requireClusterAuth(s.handleClusterPromote))
	mux.HandleFunc("POST /internal/cluster/repoint", s.requireClusterAuth(s.handleClusterRepoint))
	mux.HandleFunc("POST /internal/cluster/leave", s.requireClusterAuth(s.handleClusterLeave))
	mux.HandleFunc("GET /internal/cluster/binary", s.requireClusterAuth(s.handleClusterGetBinary))
	mux.HandleFunc("POST /internal/cluster/upgrade-binary", s.requireClusterAuth(s.handleClusterUpgradeBinary))
	mux.HandleFunc("POST /internal/cluster/upgrade", s.requireClusterAuth(s.handleClusterUpgrade))

	// Public & user-facing endpoints
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("POST /api/cluster/adopt", s.handleClusterAdopt)
	mux.HandleFunc("GET /worker", s.handleWorkerPage)
	mux.HandleFunc("POST /api/cluster/leave-self", s.handleClusterLeaveSelf)
	mux.HandleFunc("POST /api/cluster/clipboard-self", s.handleClusterClipboardSelf)
	mux.HandleFunc("GET /api/cluster/download-binary", s.handleDownloadClusterBinary)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/password", s.requireAuth(s.handleChangePassword))

	mux.HandleFunc("GET /api/cluster", s.requireAuth(s.handleGetCluster))
	mux.HandleFunc("GET /api/users/selectable", s.requireAuth(s.handleListSelectableUsers))
	mux.HandleFunc("GET /api/admin/cluster/discovered", s.requireAdmin(s.handleAdminClusterDiscovered))
	mux.HandleFunc("POST /api/admin/cluster/discover", s.requireAdmin(s.handleAdminClusterDiscover))
	mux.HandleFunc("POST /api/admin/cluster/adopt-peer", s.requireAdmin(s.handleAdminClusterAdoptPeer))
	mux.HandleFunc("POST /api/admin/cluster/join", s.requireAdmin(s.handleAdminClusterJoin))
	mux.HandleFunc("POST /api/admin/cluster/promote", s.requireAdmin(s.handleAdminClusterPromote))
	mux.HandleFunc("POST /api/admin/cluster/leave", s.requireAdmin(s.handleAdminClusterLeave))
	mux.HandleFunc("POST /api/admin/cluster/upgrade-nodes", s.requireAdmin(s.handleAdminClusterUpgradeNodes))
	mux.HandleFunc("DELETE /api/admin/cluster/nodes/{nodeID}", s.requireAdmin(s.handleAdminClusterRemoveNode))
	mux.HandleFunc("POST /api/admin/cluster/quota", s.requireAdmin(s.handleAdminClusterQuota))
	mux.HandleFunc("POST /api/admin/cluster/storage-dir", s.requireAdmin(s.handleAdminClusterStorageDir))
	mux.HandleFunc("POST /api/admin/cluster/clipboard", s.requireAdmin(s.handleAdminClusterClipboard))
	mux.HandleFunc("POST /api/admin/cluster/endpoint", s.requireAdmin(s.handleAdminClusterEndpoint))
	mux.HandleFunc("POST /api/admin/files/import-local", s.requireAdmin(s.handleAdminImportLocal))
	mux.HandleFunc("GET /api/admin/mounts", s.requireAdmin(s.handleAdminListMounts))
	mux.HandleFunc("POST /api/admin/mounts", s.requireAdmin(s.handleAdminAddMount))
	mux.HandleFunc("DELETE /api/admin/mounts/{nodeID}/{mountID}", s.requireAdmin(s.handleAdminRemoveMount))
	mux.HandleFunc("POST /api/admin/mounts/{nodeID}/{mountID}/sync", s.requireAdmin(s.handleAdminSyncMount))
	mux.HandleFunc("GET /api/admin/settings", s.requireAdmin(s.handleGetAdminSettings))
	mux.HandleFunc("POST /api/admin/settings", s.requireAdmin(s.handleUpdateAdminSettings))
	mux.HandleFunc("GET /api/admin/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("POST /api/admin/users/{username}/approve", s.requireAdmin(s.handleApproveUser))
	mux.HandleFunc("POST /api/admin/users/{username}/role", s.requireAdmin(s.handleSetUserRole))
	mux.HandleFunc("POST /api/admin/users/{username}/ban", s.requireAdmin(s.handleBanUser))
	mux.HandleFunc("POST /api/admin/users/{username}/unban", s.requireAdmin(s.handleUnbanUser))
	mux.HandleFunc("DELETE /api/admin/users/{username}", s.requireAdmin(s.handleDeleteUser))
	mux.HandleFunc("GET /api/admin/invites", s.requireAdmin(s.handleListInvites))
	mux.HandleFunc("POST /api/admin/invites", s.requireAdmin(s.handleCreateInvite))
	mux.HandleFunc("DELETE /api/admin/invites/{code}", s.requireAdmin(s.handleDeleteInvite))
	mux.HandleFunc("GET /api/admin/update", s.requireAdmin(s.handleGetUpdateStatus))
	mux.HandleFunc("POST /api/admin/update/config", s.requireAdmin(s.handleSaveUpdateConfig))
	mux.HandleFunc("POST /api/admin/update/check", s.requireAdmin(s.handleCheckUpdate))
	mux.HandleFunc("POST /api/admin/update/apply", s.requireAdmin(s.handleApplyUpdate))

	mux.HandleFunc("GET /api/files", s.requireAuth(s.handleListFiles))
	mux.HandleFunc("GET /api/summary", s.requireAuth(s.handleSummary))
	mux.HandleFunc("GET /api/events", s.requireAuth(s.handleEvents))
	mux.HandleFunc("POST /api/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("POST /api/files/create-txt", s.requireAuth(s.handleCreateTxtFile))
	mux.HandleFunc("GET /api/files/{id}/download", s.requireAuth(s.handleDownload))
	mux.HandleFunc("GET /api/files/{id}/raw", s.requireAuth(s.handleRaw))
	mux.HandleFunc("GET /api/files/{id}/preview", s.requireAuth(s.handlePreview))
	mux.HandleFunc("GET /api/files/{id}/meta", s.requireAuth(s.handleFileMeta))
	mux.HandleFunc("GET /api/files/{id}/archive-entry", s.requireAuth(s.handleArchiveEntry))
	mux.HandleFunc("POST /api/files/{id}/access", s.requireAuth(s.handleUpdateFileAccess))
	mux.HandleFunc("POST /api/files/{id}/edit", s.requireAuth(s.handleEditFile))
	mux.HandleFunc("GET /api/files/{id}/versions", s.requireAuth(s.handleGetFileVersions))
	mux.HandleFunc("POST /api/files/{id}/versions", s.requireAuth(s.handleUploadFileVersion))
	mux.HandleFunc("GET /api/files/{id}/versions/{ver}/download", s.requireAuth(s.handleDownloadFileVersion))
	mux.HandleFunc("POST /api/files/{id}/versions/{ver}/restore", s.requireAuth(s.handleRestoreFileVersion))
	mux.HandleFunc("DELETE /api/files/{id}/versions/{ver}", s.requireAuth(s.handleDeleteFileVersion))
	mux.HandleFunc("POST /api/files/{id}/extract", s.requireAuth(s.handleExtractArchive))
	mux.HandleFunc("DELETE /api/files/{id}", s.requireAuth(s.handleDelete))

	fileServer := http.FileServer(http.FS(s.webFS))
	mux.HandleFunc("GET /mobile", s.handleServeMobileHTML)
	mux.HandleFunc("GET /mobile.html", s.handleServeMobileHTML)
	mux.HandleFunc("GET /desktop", s.handleServeDesktopHTML)
	mux.HandleFunc("GET /index.html", s.handleServeDesktopHTML)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view"))); view == "desktop" || view == "pc" {
				http.SetCookie(w, &http.Cookie{Name: "lfh_view_mode", Value: "desktop", Path: "/", MaxAge: 86400 * 30})
				s.handleServeDesktopHTML(w, r)
				return
			} else if view == "mobile" || view == "m" {
				http.SetCookie(w, &http.Cookie{Name: "lfh_view_mode", Value: "mobile", Path: "/", MaxAge: 86400 * 30})
				s.handleServeMobileHTML(w, r)
				return
			}
			if isMobileRequest(r) {
				s.handleServeMobileHTML(w, r)
				return
			}
			s.handleServeDesktopHTML(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/cluster/") ||
			r.URL.Path == "/api/info" ||
			r.URL.Path == "/api/cluster/adopt" ||
			r.URL.Path == "/worker" ||
			r.URL.Path == "/api/cluster/leave-self" ||
			r.URL.Path == "/api/cluster/clipboard-self" ||
			r.URL.Path == "/api/cluster/download-binary" {
			mux.ServeHTTP(w, r)
			return
		}
		if s.cluster.Role() == cluster.RoleWorker {
			s.redirectWorkerToEntry(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) serveEmbeddedHTML(w http.ResponseWriter, filename string) {
	raw, err := fs.ReadFile(s.webFS, filename)
	if err != nil {
		raw, err = fs.ReadFile(s.webFS, "index.html")
	}
	if err != nil {
		raw = []byte("<!DOCTYPE html><html><body>LFH (" + filename + ")</body></html>")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "User-Agent, Sec-CH-UA-Mobile, Cookie")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleServeDesktopHTML(w http.ResponseWriter, r *http.Request) {
	s.serveEmbeddedHTML(w, "index.html")
}

func (s *Server) handleServeMobileHTML(w http.ResponseWriter, r *http.Request) {
	s.serveEmbeddedHTML(w, "mobile.html")
}

func (s *Server) redirectWorkerToEntry(w http.ResponseWriter, r *http.Request) {
	entry := s.cluster.EntryURL()
	if entry != "" {
		target := strings.TrimRight(entry, "/")
		if r.URL.Path != "/" {
			target += r.URL.RequestURI()
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><title>LFH 存储节点</title></head><body style="font-family:sans-serif;background:#0b1120;color:#f1f5f9;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;"><div style="text-align:center;"><h2>本机为 LFH 存储扩容节点（Worker）</h2><p>正在等待主入口节点上线或自动发现中，请从唯一主入口访问平台。</p><p style="margin-top:16px;"><a href="/worker" style="color:#38bdf8;text-decoration:underline;">打开本机节点管理 / 退出集群</a></p></div></body></html>`))
}

type authedHandler func(http.ResponseWriter, *http.Request, auth.PublicUser)

func (s *Server) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.auth.CurrentUser(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		next(w, r, u)
	}
}

func (s *Server) requireAdmin(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.auth.CurrentUser(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		if u.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅管理员可执行此操作"})
			return
		}
		next(w, r, u)
	}
}

func (s *Server) requireClusterAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cluster.VerifyRequest(r, s.cluster.Secret()) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "集群节点签名无效"})
			return
		}
		next(w, r)
	}
}

// ==================== Internal Cluster Handlers ====================

func (s *Server) handleClusterHeartbeat(w http.ResponseWriter, r *http.Request) {
	var payload cluster.HeartbeatPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的心跳数据"})
		return
	}
	if s.cluster.HandleHeartbeat(payload) {
		s.events.Publish(FileChangeEvent{
			Kind:     "cluster_sync",
			NodeName: payload.Node.NodeName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"entry_url":         s.cluster.EntryURL(),
		"entry_version":     version.Version,
		"auto_sync_workers": s.cluster.AutoSyncWorkers(),
	})
}

func (s *Server) handleClusterUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析上传表单失败: " + err.Error()})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		headers = r.MultipartForm.File["file"]
	}
	if len(headers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未收到文件"})
		return
	}

	fh := headers[0]
	if err := s.cluster.CanAcceptLocal(fh.Size); err != nil {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
		return
	}

	uploader := truncateRunes(strings.TrimSpace(r.FormValue("uploader")), 64)
	if uploader == "" {
		uploader = "unknown"
	}
	folderPath := storage.SanitizeFolderPath(r.FormValue("folder_path"))
	dev := storage.DeviceInfo{
		FP:     truncateRunes(strings.TrimSpace(r.FormValue("device_fp")), 64),
		Name:   truncateRunes(strings.TrimSpace(r.FormValue("device_name")), 80),
		IP:     truncateRunes(strings.TrimSpace(r.FormValue("device_ip")), 64),
		Detail: truncateRunes(strings.TrimSpace(r.FormValue("device_detail")), 160),
	}

	f, err := fh.Open()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取上传文件失败"})
		return
	}
	allowedUsers := parseAllowedUsersForm(r.MultipartForm.Value["allowed_users"])
	versionNote := strings.TrimSpace(r.FormValue("version_note"))
	rec, saveErr := s.store.SaveUploadWithVersionNote(fh.Filename, folderPath, versionNote, uploader, allowedUsers, dev, f)
	_ = f.Close()
	if saveErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件失败: " + saveErr.Error()})
		return
	}

	rec.NodeID = s.cluster.NodeID()
	rec.NodeName = s.cluster.NodeName()
	rec.NodeOS = runtime.GOOS
	rec.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleClusterDownload(w http.ResponseWriter, r *http.Request) {
	s.serveFileAttachment(w, r, r.PathValue("id"))
}

func (s *Server) handleClusterRaw(w http.ResponseWriter, r *http.Request) {
	s.serveFileRaw(w, r, r.PathValue("id"))
}

func (s *Server) handleClusterPreview(w http.ResponseWriter, r *http.Request) {
	s.serveFilePreview(w, r.PathValue("id"))
}

func (s *Server) handleClusterMeta(w http.ResponseWriter, r *http.Request) {
	includePath := r.URL.Query().Get("include_path") == "1"
	s.serveFileMeta(w, r.PathValue("id"), includePath)
}

func (s *Server) handleClusterArchiveEntry(w http.ResponseWriter, r *http.Request) {
	s.serveArchiveEntry(w, r, r.PathValue("id"))
}

func (s *Server) handleClusterDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Delete(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClusterNewVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析上传数据失败"})
		return
	}
	uploader := r.FormValue("uploader")
	if uploader == "" {
		uploader = "unknown"
	}
	note := r.FormValue("note")
	dev := storage.DeviceInfo{
		FP:     r.FormValue("device_fp"),
		Name:   r.FormValue("device_name"),
		IP:     r.FormValue("device_ip"),
		Detail: r.FormValue("device_detail"),
	}
	fh, err := firstMultipartFile(r, "file", "files")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未收到文件"})
		return
	}
	if err := s.cluster.CanAcceptLocal(fh.Size); err != nil {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
		return
	}
	f, err := fh.Open()
	if err != nil {
		s.store.RecordReadError()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取上传文件失败"})
		return
	}
	rec, saveErr := s.store.SaveNewVersion(id, fh.Filename, note, uploader, dev, f)
	_ = f.Close()
	if saveErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": saveErr.Error()})
		return
	}
	rec.NodeID = s.cluster.NodeID()
	rec.NodeName = s.cluster.NodeName()
	rec.NodeOS = runtime.GOOS
	rec.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleClusterEditFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Content      string `json:"content"`
		Note         string `json:"note"`
		Uploader     string `json:"uploader"`
		DeviceFP     string `json:"device_fp"`
		DeviceName   string `json:"device_name"`
		DeviceIP     string `json:"device_ip"`
		DeviceDetail string `json:"device_detail"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析编辑内容失败"})
		return
	}
	rec, _, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}
	if !preview.IsTextPreviewExt(rec.Ext) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "该文件类型不支持在线文本编辑"})
		return
	}
	if err := s.cluster.CanAcceptLocal(int64(len(req.Content))); err != nil {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
		return
	}
	uploader := strings.TrimSpace(req.Uploader)
	if uploader == "" {
		uploader = "unknown"
	}
	dev := storage.DeviceInfo{
		FP:     req.DeviceFP,
		Name:   req.DeviceName,
		IP:     req.DeviceIP,
		Detail: req.DeviceDetail,
	}
	updated, err := s.store.SaveEditedText(id, req.Content, req.Note, uploader, dev)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated.NodeID = s.cluster.NodeID()
	updated.NodeName = s.cluster.NodeName()
	updated.NodeOS = runtime.GOOS
	updated.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleClusterDownloadVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		http.NotFound(w, r)
		return
	}
	s.serveVersionAttachment(w, r, id, ver)
}

func (s *Server) handleClusterRestoreVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "版本号无效"})
		return
	}
	var req struct {
		Note         string `json:"note"`
		Uploader     string `json:"uploader"`
		DeviceFP     string `json:"device_fp"`
		DeviceName   string `json:"device_name"`
		DeviceIP     string `json:"device_ip"`
		DeviceDetail string `json:"device_detail"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	dev := storage.DeviceInfo{
		FP:     req.DeviceFP,
		Name:   req.DeviceName,
		IP:     req.DeviceIP,
		Detail: req.DeviceDetail,
	}
	rec, err := s.store.RestoreVersionByUser(id, ver, req.Note, req.Uploader, dev)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rec.NodeID = s.cluster.NodeID()
	rec.NodeName = s.cluster.NodeName()
	rec.NodeOS = runtime.GOOS
	rec.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleClusterDeleteVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "版本号无效"})
		return
	}
	rec, err := s.store.DeleteVersion(id, ver)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rec.NodeID = s.cluster.NodeID()
	rec.NodeName = s.cluster.NodeName()
	rec.NodeOS = runtime.GOOS
	rec.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, rec)
}

func firstMultipartFile(r *http.Request, keys ...string) (*multipart.FileHeader, error) {
	if r.MultipartForm == nil {
		return nil, errors.New("empty multipart form")
	}
	for _, k := range keys {
		if list := r.MultipartForm.File[k]; len(list) > 0 {
			return list[0], nil
		}
	}
	return nil, errors.New("no file found")
}

type quotaRequest struct {
	NodeID     string  `json:"node_id"`
	QuotaBytes int64   `json:"quota_bytes"`
	QuotaText  string  `json:"quota_text"`
	QuotaGB    float64 `json:"quota_gb"`
}

func parseQuotaRequestBytes(req quotaRequest) (int64, error) {
	if req.QuotaBytes > 0 {
		return req.QuotaBytes, nil
	}
	if strings.TrimSpace(req.QuotaText) != "" {
		return cluster.ParseQuotaBytes(req.QuotaText)
	}
	if req.QuotaGB > 0 {
		return int64(req.QuotaGB * float64(cluster.GB)), nil
	}
	return 0, errors.New("请提供有效的预留空间（如 500MB、10GB、1TB）")
}

func (s *Server) handleClusterSetQuota(w http.ResponseWriter, r *http.Request) {
	var req quotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	quotaBytes, err := parseQuotaRequestBytes(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	st, err := s.cluster.SetLocalQuotaBytes(quotaBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleClusterSetStorageDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StorageDir      string `json:"storage_dir"`
		MigrateExisting bool   `json:"migrate_existing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	st, moved, err := s.cluster.SetLocalStorageDir(req.StorageDir, req.MigrateExisting)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"node":        st,
		"moved_count": moved,
	})
}

func (s *Server) handleClusterSetClipboard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	st, err := s.cluster.SetLocalClipboardEnabled(req.Enabled)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) localFilesForMount(mountID string) []storage.FileRecord {
	all := s.store.AllRecords()
	out := make([]storage.FileRecord, 0)
	selfID := s.cluster.NodeID()
	selfName := s.cluster.NodeName()
	for _, rec := range all {
		if rec.MountID == mountID {
			rec.NodeID = selfID
			rec.NodeName = selfName
			rec.NodeOS = runtime.GOOS
			rec.NodeOnline = true
			out = append(out, rec)
		}
	}
	return out
}

func (s *Server) handleClusterImportLocal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourcePath   string   `json:"source_path"`
		FolderPath   string   `json:"folder_path"`
		Uploader     string   `json:"uploader"`
		AllowedUsers []string `json:"allowed_users"`
		DeviceFP     string   `json:"device_fp"`
		DeviceName   string   `json:"device_name"`
		DeviceIP     string   `json:"device_ip"`
		DeviceDetail string   `json:"device_detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	uploader := strings.TrimSpace(req.Uploader)
	if uploader == "" {
		uploader = "admin"
	}
	dev := storage.DeviceInfo{
		FP:     req.DeviceFP,
		Name:   req.DeviceName,
		IP:     req.DeviceIP,
		Detail: req.DeviceDetail,
	}
	recs, summary, err := s.store.ImportLocalPath(req.SourcePath, req.FolderPath, uploader, req.AllowedUsers, dev)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	selfID := s.cluster.NodeID()
	selfName := s.cluster.NodeName()
	for i := range recs {
		recs[i].NodeID = selfID
		recs[i].NodeName = selfName
		recs[i].NodeOS = runtime.GOOS
		recs[i].NodeOnline = true
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   recs,
		"summary": summary,
	})
}

func (s *Server) handleClusterListMounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mounts": s.store.ListMounts(),
	})
}

func (s *Server) handleClusterAddMount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DirPath      string   `json:"dir_path"`
		Alias        string   `json:"alias"`
		AddedBy      string   `json:"added_by"`
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	entry, added, err := s.store.AddMount(req.DirPath, req.Alias, req.AddedBy, req.AllowedUsers)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	files := s.localFilesForMount(entry.ID)
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"mount": entry,
		"added": added,
		"files": files,
	})
}

func (s *Server) handleClusterRemoveMount(w http.ResponseWriter, r *http.Request) {
	mountID := r.PathValue("id")
	removed, err := s.store.RemoveMount(mountID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"removed": removed,
	})
}

func (s *Server) handleClusterSyncMount(w http.ResponseWriter, r *http.Request) {
	mountID := r.PathValue("id")
	res, err := s.store.SyncMount(mountID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	var mount storage.MountEntry
	for _, entry := range s.store.ListMounts() {
		if entry.ID == mountID {
			mount = entry
			break
		}
	}
	files := s.localFilesForMount(mountID)
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"mount":  mount,
		"result": res,
		"files":  files,
	})
}

func (s *Server) handleClusterUpdateAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	rec, err := s.store.SetAllowedUsers(r.PathValue("id"), req.AllowedUsers)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleClusterPromote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterSecret string `json:"cluster_secret"`
		AuthSnapshot  []byte `json:"auth_snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if len(req.AuthSnapshot) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少账号权限快照"})
		return
	}
	backup, err := s.auth.ExportSnapshot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "备份当前账号状态失败: " + err.Error()})
		return
	}
	if err := s.auth.ImportSnapshot(req.AuthSnapshot); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	node, err := s.cluster.PromoteSelfToEntry(req.ClusterSecret)
	if err != nil {
		_ = s.auth.ImportSnapshot(backup)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: node.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"node": node,
	})
}

func (s *Server) handleClusterRepoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NewEntryURL string `json:"new_entry_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if err := s.cluster.RepointEntry(req.NewEntryURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "entry_switched",
		NodeName: s.cluster.NodeName(),
		EntryURL: s.cluster.EntryURL(),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClusterLeave(w http.ResponseWriter, r *http.Request) {
	node, err := s.cluster.LeaveCluster()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: node.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"node": node,
	})
}

func (s *Server) handleClusterLeaveSelf(w http.ResponseWriter, r *http.Request) {
	if s.cluster.Role() != cluster.RoleWorker {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "当前节点已是独立主入口节点，无需退出集群"})
		return
	}
	var req struct {
		AdminUser     string `json:"admin_user"`
		AdminPass     string `json:"admin_pass"`
		ClusterSecret string `json:"cluster_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	authorized := false
	if strings.TrimSpace(req.AdminUser) != "" && req.AdminPass != "" {
		if u, ok := s.auth.Authenticate(strings.TrimSpace(req.AdminUser), req.AdminPass); ok && u.Role == "admin" {
			authorized = true
		}
	}
	if !authorized {
		providedSecret := strings.TrimSpace(req.ClusterSecret)
		expectedSecret := strings.TrimSpace(s.cluster.Secret())
		if providedSecret != "" && expectedSecret != "" && hmac.Equal([]byte(providedSecret), []byte(expectedSecret)) {
			authorized = true
		}
	}
	if !authorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "本机管理员账号密码或当前集群密钥验证失败"})
		return
	}

	node, err := s.cluster.LeaveCluster()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "退出集群失败: " + err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: node.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"node":           node,
		"cluster_secret": s.cluster.Secret(),
		"redirect_url":   "/",
	})
}

func (s *Server) handleClusterClipboardSelf(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled       bool   `json:"enabled"`
		AdminUser     string `json:"admin_user"`
		AdminPass     string `json:"admin_pass"`
		ClusterSecret string `json:"cluster_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	authorized := false
	if u, ok := s.auth.CurrentUser(r); ok && u.Role == "admin" {
		authorized = true
	}
	if !authorized && strings.TrimSpace(req.AdminUser) != "" && req.AdminPass != "" {
		if u, ok := s.auth.Authenticate(strings.TrimSpace(req.AdminUser), req.AdminPass); ok && u.Role == "admin" {
			authorized = true
		}
	}
	if !authorized {
		providedSecret := strings.TrimSpace(req.ClusterSecret)
		expectedSecret := strings.TrimSpace(s.cluster.Secret())
		if providedSecret != "" && expectedSecret != "" && hmac.Equal([]byte(providedSecret), []byte(expectedSecret)) {
			authorized = true
		}
	}
	if !authorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "验证失败：请先输入本机管理员账号密码或当前集群密钥"})
		return
	}

	node, err := s.cluster.SetLocalClipboardEnabled(req.Enabled)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "切换本机超级剪贴板失败: " + err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: node.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"node": node,
	})
}

func (s *Server) handleWorkerPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(workerControlHTML))
}

const workerControlHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>LFH 本机节点管理</title>
  <style>
    :root { color-scheme: dark; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
      background: radial-gradient(circle at top, #1e293b 0%, #0b1120 60%);
      color: #f1f5f9;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 20px;
    }
    .card {
      width: 100%;
      max-width: 520px;
      background: rgba(15, 23, 42, 0.92);
      border: 1px solid rgba(148, 163, 184, 0.2);
      border-radius: 16px;
      padding: 26px;
      box-shadow: 0 24px 60px rgba(0, 0, 0, 0.45);
    }
    .head { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 18px; }
    .title { font-size: 20px; font-weight: 700; margin: 0; }
    .badge {
      font-size: 12px;
      padding: 4px 10px;
      border-radius: 999px;
      font-weight: 600;
      background: rgba(56, 189, 248, 0.16);
      color: #38bdf8;
      border: 1px solid rgba(56, 189, 248, 0.35);
    }
    .badge.entry { background: rgba(34, 197, 94, 0.16); color: #4ade80; border-color: rgba(34, 197, 94, 0.35); }
    .meta {
      background: rgba(30, 41, 59, 0.65);
      border: 1px solid rgba(148, 163, 184, 0.16);
      border-radius: 12px;
      padding: 14px 16px;
      margin-bottom: 18px;
      font-size: 13px;
      line-height: 1.75;
    }
    .meta-row { display: flex; justify-content: space-between; gap: 12px; word-break: break-all; }
    .meta-k { color: #94a3b8; white-space: nowrap; }
    .meta-v { color: #f8fafc; font-weight: 500; text-align: right; }
    .tip {
      font-size: 12.5px;
      color: #cbd5e1;
      background: rgba(245, 158, 11, 0.1);
      border: 1px solid rgba(245, 158, 11, 0.28);
      border-radius: 10px;
      padding: 10px 12px;
      margin-bottom: 16px;
      line-height: 1.6;
    }
    .field { margin-bottom: 12px; }
    label { display: block; font-size: 12.5px; color: #cbd5e1; margin-bottom: 6px; }
    input {
      width: 100%;
      padding: 10px 12px;
      border-radius: 10px;
      border: 1px solid rgba(148, 163, 184, 0.28);
      background: #0f172a;
      color: #f8fafc;
      font-size: 14px;
    }
    .divider { text-align: center; font-size: 12px; color: #94a3b8; margin: 10px 0; }
    .actions { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 16px; }
    .btn {
      flex: 1;
      min-width: 140px;
      padding: 10px 14px;
      border-radius: 10px;
      border: 0;
      font-size: 14px;
      font-weight: 600;
      cursor: pointer;
      text-align: center;
      text-decoration: none;
      display: inline-flex;
      align-items: center;
      justify-content: center;
    }
    .btn-danger { background: #dc2626; color: #fff; }
    .btn-danger:hover { background: #b91c1c; }
    .btn-primary { background: #2563eb; color: #fff; }
    .btn-primary:hover { background: #1d4ed8; }
    .btn-ghost { background: rgba(148, 163, 184, 0.16); color: #f1f5f9; }
    .msg { margin-top: 12px; font-size: 13px; min-height: 20px; }
    .msg.err { color: #f87171; }
    .msg.ok { color: #4ade80; }
    .hidden { display: none !important; }
  </style>
</head>
<body>
  <div class="card">
    <div class="head">
      <h1 class="title">LFH 本机节点控制台</h1>
      <span id="role-badge" class="badge">加载中...</span>
    </div>
    <div class="meta">
      <div class="meta-row"><span class="meta-k">节点名称</span><span id="m-name" class="meta-v">-</span></div>
      <div class="meta-row"><span class="meta-k">节点 ID</span><span id="m-id" class="meta-v">-</span></div>
      <div class="meta-row"><span class="meta-k">版本</span><span id="m-ver" class="meta-v">-</span></div>
      <div class="meta-row"><span class="meta-k">当前主入口</span><span id="m-entry" class="meta-v">-</span></div>
      <div id="m-dir-row" class="meta-row"><span class="meta-k">本机存储目录</span><span id="m-dir" class="meta-v">-</span></div>
      <div class="meta-row"><span class="meta-k">本机文件数 / 容量</span><span id="m-cap" class="meta-v">-</span></div>
      <div class="meta-row"><span class="meta-k">本机超级剪贴板</span><span id="m-clip" class="meta-v">已关闭</span></div>
    </div>

    <div class="meta" style="margin-bottom:14px;">
      <div style="font-size:13px;font-weight:700;margin-bottom:8px;">本机超级剪贴板开关</div>
      <div style="font-size:12px;color:#94a3b8;line-height:1.5;margin-bottom:10px;">
        开启后将自动监听本机复制的文字与截图，按时间戳自动命名并上传至集群「超级剪贴板」目录（可在下方填写本机管理员凭据或集群密钥切换，主节点管理员也可在 Web 集群面板集中开关）。
      </div>
      <div class="actions" style="margin-top:0;">
        <button id="btn-toggle-clip" class="btn btn-primary" type="button">开启本机超级剪贴板</button>
      </div>
    </div>

    <div id="entry-box" class="hidden">
      <div class="tip">当前本机已是主入口节点（Entry），可以直接打开完整 Web 控制台。</div>
      <div class="actions">
        <a class="btn btn-primary" href="/">进入 LFH 主界面</a>
      </div>
    </div>

    <form id="leave-form" class="hidden">
      <div class="tip">如果主入口节点已离线或需要解除当前节点从属关系，可在此验证本机管理员账号密码或当前集群密钥，将本机安全退出集群并恢复为独立主入口（本机文件与目录将完整保留）。</div>
      <div class="field">
        <label for="admin-user">本机管理员用户名（方式一）</label>
        <input id="admin-user" type="text" autocomplete="username" placeholder="例如 admin_xxx" />
      </div>
      <div class="field">
        <label for="admin-pass">本机管理员密码</label>
        <input id="admin-pass" type="password" autocomplete="current-password" placeholder="输入本机管理员密码" />
      </div>
      <div class="divider">—— 或使用 ——</div>
      <div class="field">
        <label for="cluster-secret">当前集群密钥（方式二）</label>
        <input id="cluster-secret" type="password" placeholder="输入当前集群的 Cluster Secret" />
      </div>
      <div class="actions">
        <button id="btn-leave-self" class="btn btn-danger" type="submit">退出集群并恢复独立节点</button>
        <a id="btn-go-entry" class="btn btn-ghost hidden" href="#">前往当前主入口</a>
      </div>
    </form>
    <div id="status-msg" class="msg"></div>
  </div>
  <script>
    function fmtBytes(n) {
      if (!n || n <= 0) return "0 B";
      const units = ["B", "KB", "MB", "GB", "TB"];
      let idx = 0, val = Number(n);
      while (val >= 1024 && idx < units.length - 1) { val /= 1024; idx++; }
      return (val >= 100 || idx === 0 ? val.toFixed(0) : val.toFixed(1)) + " " + units[idx];
    }
    async function loadNodeInfo() {
      const msg = document.getElementById("status-msg");
      try {
        const res = await fetch("/api/info", { cache: "no-store" });
        const data = await res.json();
        const selfNode = (data.pool && Array.isArray(data.pool.nodes))
          ? (data.pool.nodes.find((n) => n.is_self) || data.pool.nodes[0] || {})
          : {};
        document.getElementById("m-name").textContent = selfNode.node_name || "-";
        document.getElementById("m-id").textContent = selfNode.node_id || "-";
        document.getElementById("m-ver").textContent = (data.app_name || "LFH") + " " + (data.version || "");
        document.getElementById("m-entry").textContent = data.entry_url || (data.role === "entry" ? "本机即主入口" : "未连接");
        const dirRow = document.getElementById("m-dir-row");
        if (selfNode.storage_dir) {
          document.getElementById("m-dir").textContent = selfNode.storage_dir;
          dirRow.classList.remove("hidden");
        } else {
          dirRow.classList.add("hidden");
        }
        document.getElementById("m-cap").textContent = String(selfNode.file_count || 0) + " 个文件 · " + fmtBytes(selfNode.used_bytes || 0) + " / " + fmtBytes(selfNode.quota_bytes || 0);
        const clipEnabled = Boolean(selfNode.clipboard_enabled);
        const clipEl = document.getElementById("m-clip");
        const clipBtn = document.getElementById("btn-toggle-clip");
        clipEl.textContent = clipEnabled ? "已开启（自动监听上传中）" : "已关闭";
        clipEl.style.color = clipEnabled ? "#4ade80" : "#94a3b8";
        clipBtn.dataset.enabled = clipEnabled ? "1" : "0";
        clipBtn.textContent = clipEnabled ? "关闭本机超级剪贴板" : "开启本机超级剪贴板";
        clipBtn.className = clipEnabled ? "btn btn-danger" : "btn btn-primary";

        const badge = document.getElementById("role-badge");
        if (data.role === "worker") {
          badge.textContent = "Worker 从节点";
          badge.classList.remove("entry");
          document.getElementById("leave-form").classList.remove("hidden");
          document.getElementById("entry-box").classList.add("hidden");
          const goEntry = document.getElementById("btn-go-entry");
          if (data.entry_url) {
            goEntry.href = data.entry_url;
            goEntry.classList.remove("hidden");
          }
        } else {
          badge.textContent = "Entry 主入口";
          badge.classList.add("entry");
          document.getElementById("entry-box").classList.remove("hidden");
          document.getElementById("leave-form").classList.add("hidden");
        }
      } catch (err) {
        msg.className = "msg err";
        msg.textContent = "读取节点状态失败: " + err.message;
      }
    }
    document.getElementById("btn-toggle-clip").addEventListener("click", async () => {
      const msg = document.getElementById("status-msg");
      const btn = document.getElementById("btn-toggle-clip");
      const nextEnabled = btn.dataset.enabled !== "1";
      msg.className = "msg";
      msg.textContent = "";
      btn.disabled = true;
      try {
        const res = await fetch("/api/cluster/clipboard-self", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            enabled: nextEnabled,
            admin_user: document.getElementById("admin-user").value.trim(),
            admin_pass: document.getElementById("admin-pass").value,
            cluster_secret: document.getElementById("cluster-secret").value.trim()
          })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "切换失败");
        msg.className = "msg ok";
        msg.textContent = nextEnabled ? "已开启本机超级剪贴板自动上传" : "已关闭本机超级剪贴板";
        await loadNodeInfo();
      } catch (err) {
        msg.className = "msg err";
        msg.textContent = err.message;
      } finally {
        btn.disabled = false;
      }
    });
    document.getElementById("leave-form").addEventListener("submit", async (e) => {
      e.preventDefault();
      const msg = document.getElementById("status-msg");
      const btn = document.getElementById("btn-leave-self");
      msg.className = "msg";
      msg.textContent = "";
      if (!confirm("确定退出当前集群并恢复为独立主入口节点吗？（本机所有文件将保留）")) return;
      btn.disabled = true;
      try {
        const res = await fetch("/api/cluster/leave-self", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            admin_user: document.getElementById("admin-user").value.trim(),
            admin_pass: document.getElementById("admin-pass").value,
            cluster_secret: document.getElementById("cluster-secret").value.trim()
          })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "请求失败");
        msg.className = "msg ok";
        msg.textContent = "已退出集群并恢复为独立主入口，正在跳转...";
        setTimeout(() => { window.location.href = data.redirect_url || "/"; }, 800);
      } catch (err) {
        msg.className = "msg err";
        msg.textContent = err.message;
      } finally {
        btn.disabled = false;
      }
    });
    loadNodeInfo();
  </script>
</body>
</html>`

// ==================== Public & Entry Handlers ====================

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	_, activeAddrs, primaryURL := s.allConfiguredAddrs()
	entryURL := s.cluster.EntryURL()
	if s.cluster.Role() == cluster.RoleEntry && primaryURL != "" {
		entryURL = primaryURL
	}
	entryURLs := make([]string, 0, len(activeAddrs))
	for _, a := range activeAddrs {
		entryURLs = append(entryURLs, a.URL)
	}
	if len(entryURLs) == 0 && entryURL != "" {
		entryURLs = []string{entryURL}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name":        version.AppName,
		"version":         version.Version,
		"entry_url":       entryURL,
		"entry_urls":      entryURLs,
		"entry_addrs":     activeAddrs,
		"local_url":       "http://localhost:" + s.Port(),
		"role":            s.cluster.Role(),
		"invite_required": s.auth.Settings().InviteRequired,
		"is_mobile_ua":    isMobileRequest(r),
		"pool":            s.cluster.PoolSummaryForRole(false),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	u, status, err := s.auth.AuthenticateWithReason(req.Username, req.Password)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if err := s.auth.IssueCookie(w, u); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发会话失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	u, err := s.auth.Register(req.Username, req.Password, req.InviteCode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if u.Status == auth.StatusPending {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"user":    u,
			"pending": true,
			"message": "注册申请已提交，请等待管理员审核同意后登录",
		})
		return
	}
	if err := s.auth.IssueCookie(w, u); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发会话失败"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":    u,
		"pending": false,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.auth.CurrentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	settings := s.auth.Settings()
	_, activeAddrs, primaryURL := s.allConfiguredAddrs()
	entryURL := s.cluster.EntryURL()
	if s.cluster.Role() == cluster.RoleEntry && primaryURL != "" {
		entryURL = primaryURL
	}
	entryURLs := make([]string, 0, len(activeAddrs))
	for _, a := range activeAddrs {
		entryURLs = append(entryURLs, a.URL)
	}
	if len(entryURLs) == 0 && entryURL != "" {
		entryURLs = []string{entryURL}
	}
	resp := map[string]any{
		"app_name":        version.AppName,
		"version":         version.Version,
		"user":            u,
		"entry_url":       entryURL,
		"entry_urls":      entryURLs,
		"entry_addrs":     activeAddrs,
		"local_url":       "http://localhost:" + s.Port(),
		"invite_required": settings.InviteRequired,
		"events_version":  s.events.Version(),
		"upload_policy": map[string]any{
			"allowed_exts": settings.AllowedExts,
			"blocked_exts": settings.BlockedExts,
		},
		"pool": s.cluster.PoolSummaryForRole(u.Role == "admin"),
	}
	if u.Role == "admin" {
		resp["pending_users"] = s.auth.PendingCount()
		if up := s.getUpdater(); up != nil {
			resp["update_status"] = up.Status()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetUpdateStatus(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	up := s.getUpdater()
	if up == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "升级组件未启用"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": up.Status()})
}

func (s *Server) handleSaveUpdateConfig(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	up := s.getUpdater()
	if up == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "升级组件未启用"})
		return
	}
	var req struct {
		Repo      string `json:"repo"`
		ProxyURL  string `json:"proxy_url"`
		AutoCheck *bool  `json:"auto_check"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	st, err := up.UpdateConfig(req.Repo, req.ProxyURL, req.AutoCheck)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st})
}

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	up := s.getUpdater()
	if up == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "升级组件未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, err := up.CheckNow(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  err.Error(),
			"status": st,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st})
}

func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	up := s.getUpdater()
	if up == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "升级组件未启用"})
		return
	}
	st, err := up.StartApplyAsync()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  err.Error(),
			"status": st,
		})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": st})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if err := s.auth.ChangePassword(u.Username, req.OldPassword, req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	port := s.Port()
	allAddrs, activeAddrs, primaryURL := s.allConfiguredAddrs()
	resp := map[string]any{
		"pool":           s.cluster.PoolSummaryForRole(u.Role == "admin"),
		"cluster_secret": "",
		"events_version": s.events.Version(),
		"entry_addrs":    activeAddrs,
		"entry_url":      primaryURL,
	}
	if u.Role == "admin" {
		resp["cluster_secret"] = s.cluster.Secret()
		resp["expose_ip"] = s.cluster.ExposeIP()
		resp["expose_ips"] = s.cluster.ExposeIPs()
		resp["disabled_ips"] = s.cluster.DisabledIPs()
		resp["port"] = port
		resp["available_addrs"] = allAddrs
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleClusterAdopt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntryURL         string `json:"entry_url"`
		ClusterSecret    string `json:"cluster_secret"`
		TargetAdminUser  string `json:"target_admin_user"`
		TargetAdminPass  string `json:"target_admin_pass"`
		TargetAuthSecret string `json:"target_auth_secret"`
		QuotaText        string `json:"quota_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	if strings.TrimSpace(req.TargetAuthSecret) != "" {
		if !hmac.Equal([]byte(strings.TrimSpace(req.TargetAuthSecret)), []byte(s.cluster.Secret())) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "目标节点集群密钥验证失败"})
			return
		}
	} else if strings.TrimSpace(req.TargetAdminUser) != "" && req.TargetAdminPass != "" {
		u, _, authErr := s.auth.AuthenticateWithReason(req.TargetAdminUser, req.TargetAdminPass)
		if authErr != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "目标节点管理员认证失败：" + authErr.Error()})
			return
		}
		if u.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "该账号不是目标节点的管理员"})
			return
		}
	} else {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请提供目标节点管理员账号密码或目标节点集群密钥"})
		return
	}

	var quotaBytes int64
	if strings.TrimSpace(req.QuotaText) != "" {
		parsed, err := cluster.ParseQuotaBytes(req.QuotaText)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		quotaBytes = parsed
	}

	if err := s.cluster.JoinEntryCluster(req.EntryURL, req.ClusterSecret, quotaBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"node":      s.cluster.SelfStatus(),
		"entry_url": s.cluster.EntryURL(),
	})
}

func (s *Server) handleAdminClusterDiscovered(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": s.cluster.PassiveDiscoveredNodes(),
	})
}

func (s *Server) handleAdminClusterDiscover(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Mode    string `json:"mode"` // "scan" or "manual"
		Address string `json:"address"`
		Ports   []int  `json:"ports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		if strings.TrimSpace(req.Address) != "" {
			mode = "manual"
		} else {
			mode = "scan"
		}
	}

	if mode == "manual" {
		dn, err := s.cluster.ProbeNodeURL(r.Context(), req.Address, "manual")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node":  dn,
			"nodes": s.cluster.PassiveDiscoveredNodes(),
		})
		return
	}

	nodes := s.cluster.ScanLAN(r.Context(), req.Ports)
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
	})
}

func (s *Server) handleAdminClusterAdoptPeer(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Address          string `json:"address"`
		TargetAdminUser  string `json:"target_admin_user"`
		TargetAdminPass  string `json:"target_admin_pass"`
		TargetAuthSecret string `json:"target_auth_secret"`
		QuotaText        string `json:"quota_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	node, err := s.cluster.AdoptPeer(req.Address, req.TargetAdminUser, req.TargetAdminPass, req.TargetAuthSecret, req.QuotaText)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: node.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"node": node,
		"pool": s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminClusterJoin(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		EntryURL      string `json:"entry_url"`
		ClusterSecret string `json:"cluster_secret"`
		QuotaText     string `json:"quota_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	var quotaBytes int64
	if strings.TrimSpace(req.QuotaText) != "" {
		parsed, err := cluster.ParseQuotaBytes(req.QuotaText)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		quotaBytes = parsed
	}
	if err := s.cluster.JoinEntryCluster(req.EntryURL, req.ClusterSecret, quotaBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: s.cluster.NodeName(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"entry_url": s.cluster.EntryURL(),
		"node":      s.cluster.SelfStatus(),
	})
}

func (s *Server) handleAdminClusterRemoveNode(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	nodeID := r.PathValue("nodeID")
	notified, peerName, err := s.cluster.EvictPeer(nodeID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: peerName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"notified": notified,
		"pool":     s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminClusterPromote(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	if s.cluster.Role() != cluster.RoleEntry {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "仅主入口节点可执行主节点切换"})
		return
	}
	var req struct {
		TargetNodeID string `json:"target_node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	snap, err := s.auth.ExportSnapshot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "导出账号快照失败: " + err.Error()})
		return
	}
	newEntryURL, targetNode, err := s.cluster.PromotePeer(req.TargetNodeID, snap)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "entry_switched",
		NodeName: targetNode.NodeName,
		EntryURL: newEntryURL,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"new_entry_url": newEntryURL,
		"target_node":   targetNode,
		"pool":          s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminClusterLeave(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	if s.cluster.Role() != cluster.RoleEntry {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "仅主入口节点可执行解散/退出集群操作"})
		return
	}
	detached, err := s.cluster.DissolveCluster()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "退出/解散集群失败: " + err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: s.cluster.NodeName(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"detached":       detached,
		"cluster_secret": s.cluster.Secret(),
		"pool":           s.cluster.PoolSummaryForRole(true),
	})
}

type peerUpgradeResult struct {
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name"`
	FromVersion   string `json:"from_version"`
	TargetVersion string `json:"target_version"`
	Method        string `json:"method"`
	OK            bool   `json:"ok"`
	Message       string `json:"message,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (s *Server) resolveLocalPlatformBinary(targetOS, targetArch string) (string, string, int64, error) {
	if u := s.getUpdater(); u != nil {
		return u.LocalBinaryForPlatform(targetOS, targetArch)
	}
	goos := strings.ToLower(strings.TrimSpace(targetOS))
	goarch := strings.ToLower(strings.TrimSpace(targetArch))
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		if exePath, err := os.Executable(); err == nil && exePath != "" {
			if f, oErr := os.Open(exePath); oErr == nil {
				h := sha256.New()
				n, cErr := io.Copy(h, f)
				_ = f.Close()
				if cErr == nil && n > 0 {
					return exePath, hex.EncodeToString(h.Sum(nil)), n, nil
				}
			}
		}
	}
	return "", "", 0, fmt.Errorf("未找到 %s/%s 的本地安装包", goos, goarch)
}

func (s *Server) syncSinglePeerVersion(peer cluster.NodeStatus) (peerUpgradeResult, error) {
	targetVer := version.Version
	res := peerUpgradeResult{
		NodeID:        peer.NodeID,
		NodeName:      peer.NodeName,
		FromVersion:   peer.Version,
		TargetVersion: targetVer,
	}
	if !peer.Online {
		res.Error = "节点当前离线，无法同步版本"
		return res, errors.New(res.Error)
	}

	if binPath, shaHex, _, err := s.resolveLocalPlatformBinary(peer.OS, peer.Arch); err == nil && binPath != "" {
		res.Method = "lan_binary_push"
		if pushErr := s.cluster.PushBinaryToPeer(peer, binPath, shaHex, targetVer); pushErr != nil {
			res.Error = pushErr.Error()
			return res, pushErr
		}
		res.OK = true
		res.Message = fmt.Sprintf("已通过局域网直推主节点版本 %s 至 %s，节点正在平滑热重启", targetVer, peer.NodeName)
		return res, nil
	}

	repo := version.DefaultRepo
	proxyURL := ""
	if u := s.getUpdater(); u != nil {
		st := u.Status()
		if st.Repo != "" {
			repo = st.Repo
		}
		proxyURL = st.ProxyURL
	}
	res.Method = "github_release"
	if ghErr := s.cluster.TriggerPeerGitHubUpgrade(peer, repo, proxyURL, targetVer); ghErr != nil {
		res.Error = ghErr.Error()
		return res, ghErr
	}
	res.OK = true
	res.Message = fmt.Sprintf("已指令异构节点 %s (%s/%s) 在线同步升级至 %s", peer.NodeName, peer.OS, peer.Arch, targetVer)
	return res, nil
}

func (s *Server) pullAndInstallFromEntry(entryURL, targetVersion string) error {
	u := s.getUpdater()
	if u == nil {
		return errors.New("updater not configured")
	}
	rc, verHeader, shaHeader, err := s.cluster.PullBinaryFromEntry(entryURL)
	if err == nil && rc != nil {
		defer rc.Close()
		ver := strings.TrimSpace(verHeader)
		if ver == "" {
			ver = targetVersion
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		return u.ApplyBinaryStream(ctx, rc, ver, shaHeader, false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, cErr := u.CheckNow(ctx); cErr != nil {
		return cErr
	}
	return u.ApplyUpdateSync(ctx)
}

func (s *Server) handleClusterGetBinary(w http.ResponseWriter, r *http.Request) {
	targetOS := r.URL.Query().Get("os")
	targetArch := r.URL.Query().Get("arch")
	binPath, shaHex, _, err := s.resolveLocalPlatformBinary(targetOS, targetArch)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(binPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-LFH-Target-Version", version.Version)
	w.Header().Set("X-LFH-Binary-SHA256", shaHex)
	http.ServeContent(w, r, filepath.Base(binPath), info.ModTime(), f)
}

func (s *Server) handleClusterUpgradeBinary(w http.ResponseWriter, r *http.Request) {
	u := s.getUpdater()
	if u == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "当前节点未启用在线升级模块"})
		return
	}
	targetVer := strings.TrimSpace(r.Header.Get("X-LFH-Target-Version"))
	if targetVer == "" {
		targetVer = version.Version
	}
	expectedSHA := strings.TrimSpace(r.Header.Get("X-LFH-Binary-SHA256"))
	if err := u.ApplyBinaryStream(r.Context(), io.LimitReader(r.Body, 256<<20), targetVer, expectedSHA, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": targetVer,
	})
}

func (s *Server) handleClusterUpgrade(w http.ResponseWriter, r *http.Request) {
	u := s.getUpdater()
	if u == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "当前节点未启用在线升级模块"})
		return
	}
	var req struct {
		Repo          string `json:"repo"`
		ProxyURL      string `json:"proxy_url"`
		TargetVersion string `json:"target_version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Repo != "" || req.ProxyURL != "" {
		_, _ = u.UpdateConfig(req.Repo, req.ProxyURL, nil)
	}
	if _, err := u.CheckNow(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	st, err := u.StartApplyAsync()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"status": st,
	})
}

func (s *Server) handleDownloadClusterBinary(w http.ResponseWriter, r *http.Request) {
	targetOS := r.URL.Query().Get("os")
	targetArch := r.URL.Query().Get("arch")
	binPath, shaHex, _, err := s.resolveLocalPlatformBinary(targetOS, targetArch)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(binPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	dlName := "lfh"
	goos := strings.ToLower(strings.TrimSpace(targetOS))
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" {
		dlName = "lfh.exe"
	} else if targetArch != "" {
		dlName = fmt.Sprintf("lfh-%s-%s", goos, strings.ToLower(strings.TrimSpace(targetArch)))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+dlName+"\"")
	w.Header().Set("X-LFH-Target-Version", version.Version)
	w.Header().Set("X-LFH-Binary-SHA256", shaHex)
	http.ServeContent(w, r, dlName, info.ModTime(), f)
}

func (s *Server) handleAdminClusterUpgradeNodes(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		NodeID          string `json:"node_id"`
		AutoSyncWorkers *bool  `json:"auto_sync_workers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if req.AutoSyncWorkers != nil {
		if err := s.cluster.SetAutoSyncWorkers(*req.AutoSyncWorkers); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(req.NodeID) == "" && req.NodeID != "all" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                true,
				"auto_sync_workers": s.cluster.AutoSyncWorkers(),
				"pool":              s.cluster.PoolSummaryForRole(true),
			})
			return
		}
	}

	targetID := strings.TrimSpace(req.NodeID)
	pool := s.cluster.PoolSummaryForRole(true)
	var targets []cluster.NodeStatus
	for _, n := range pool.Nodes {
		if n.IsSelf {
			continue
		}
		if targetID != "" && targetID != "all" {
			if n.NodeID == targetID {
				targets = append(targets, n)
			}
			continue
		}
		if n.Online {
			targets = append(targets, n)
		}
	}

	if targetID != "" && targetID != "all" && len(targets) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "未找到指定的分节点"})
		return
	}

	results := make([]peerUpgradeResult, 0, len(targets))
	syncedCount := 0
	var firstErr string
	for _, peer := range targets {
		res, err := s.syncSinglePeerVersion(peer)
		results = append(results, res)
		if err == nil && res.OK {
			syncedCount++
		} else if firstErr == "" && err != nil {
			firstErr = err.Error()
		}
	}

	if targetID != "" && targetID != "all" && syncedCount == 0 && firstErr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   firstErr,
			"results": results,
			"pool":    s.cluster.PoolSummaryForRole(true),
		})
		return
	}

	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: s.cluster.NodeName(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"synced":            syncedCount,
		"total_targets":     len(targets),
		"entry_version":     version.Version,
		"auto_sync_workers": s.cluster.AutoSyncWorkers(),
		"results":           results,
		"first_error":       firstErr,
		"pool":              s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleListSelectableUsers(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"users": s.auth.ListActiveUsers(),
	})
}

func (s *Server) handleAdminClusterStorageDir(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		NodeID          string `json:"node_id"`
		StorageDir      string `json:"storage_dir"`
		MigrateExisting bool   `json:"migrate_existing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	updated, moved, err := s.cluster.UpdateNodeStorageDir(req.NodeID, req.StorageDir, req.MigrateExisting)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: updated.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"node":        updated,
		"moved_count": moved,
		"pool":        s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminClusterQuota(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req quotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	quotaBytes, err := parseQuotaRequestBytes(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, err := s.cluster.UpdateNodeQuotaBytes(req.NodeID, quotaBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node": updated,
		"pool": s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminClusterClipboard(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		NodeID  string `json:"node_id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	updated, err := s.cluster.UpdateNodeClipboardEnabled(req.NodeID, req.Enabled)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: updated.NodeName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"node": updated,
		"pool": s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminImportLocal(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		NodeID       string   `json:"node_id"`
		SourcePath   string   `json:"source_path"`
		FolderPath   string   `json:"folder_path"`
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	dev := extractDeviceInfo(r)
	files, summary, err := s.cluster.ProxyImportLocal(req.NodeID, req.SourcePath, req.FolderPath, u.Username, req.AllowedUsers, dev)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(files) > 0 {
		s.publishRecordChange("upload", u.Username, files, req.AllowedUsers)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   s.sanitizeRecordsForUser(files, u),
		"summary": summary,
		"pool":    s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminListMounts(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mounts": s.cluster.ListAllMounts(),
	})
}

func (s *Server) handleAdminAddMount(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		NodeID       string   `json:"node_id"`
		DirPath      string   `json:"dir_path"`
		Alias        string   `json:"alias"`
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	entry, added, err := s.cluster.AddNodeMount(req.NodeID, req.DirPath, req.Alias, u.Username, req.AllowedUsers)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		Uploader: u.Username,
		Count:    added,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"mount":  entry,
		"added":  added,
		"mounts": s.cluster.ListAllMounts(),
		"pool":   s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminRemoveMount(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	nodeID := r.PathValue("nodeID")
	mountID := r.PathValue("mountID")
	removed, err := s.cluster.RemoveNodeMount(nodeID, mountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		Uploader: u.Username,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"removed": removed,
		"mounts":  s.cluster.ListAllMounts(),
		"pool":    s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleAdminSyncMount(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	nodeID := r.PathValue("nodeID")
	mountID := r.PathValue("mountID")
	entry, res, err := s.cluster.SyncNodeMount(nodeID, mountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		Uploader: u.Username,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"mount":  entry,
		"result": res,
		"mounts": s.cluster.ListAllMounts(),
		"pool":   s.cluster.PoolSummaryForRole(true),
	})
}

// SyncLocalZeroCopy refreshes local mounted directories and external path reference status.
func (s *Server) SyncLocalZeroCopy() {
	mountChanged, _, _, _ := s.store.SyncAllMounts()
	refChanged := s.store.RefreshRefStatus()
	if mountChanged || refChanged {
		s.cluster.TriggerHeartbeat()
		s.events.Publish(FileChangeEvent{
			Kind:     "cluster_sync",
			NodeName: s.cluster.NodeName(),
		})
	}
}

func (s *Server) handleAdminClusterEndpoint(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		ExposeIP    string    `json:"expose_ip"`
		ExposeIPs   *[]string `json:"expose_ips"`
		DisabledIPs *[]string `json:"disabled_ips"`
		Port        string    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	cleanIP, err := sanitizeExposeHost(req.ExposeIP)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var cleanExposeIPs, cleanDisabledIPs []string
	updateNICSelection := req.ExposeIPs != nil || req.DisabledIPs != nil
	if req.ExposeIPs != nil {
		for _, raw := range *req.ExposeIPs {
			c, cErr := sanitizeExposeHost(raw)
			if cErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": cErr.Error()})
				return
			}
			if c != "" {
				cleanExposeIPs = append(cleanExposeIPs, c)
			}
		}
	}
	if req.DisabledIPs != nil {
		for _, raw := range *req.DisabledIPs {
			c, cErr := sanitizeExposeHost(raw)
			if cErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": cErr.Error()})
				return
			}
			if c != "" {
				cleanDisabledIPs = append(cleanDisabledIPs, c)
			}
		}
	}

	newPort := strings.TrimSpace(req.Port)
	if newPort == "" {
		newPort = s.Port()
	}
	pNum, err := strconv.Atoi(newPort)
	if err != nil || pNum < 1 || pNum > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "端口号必须在 1 到 65535 之间"})
		return
	}
	newPort = strconv.Itoa(pNum)

	s.mu.RLock()
	currentPort := s.port
	switcher := s.portSwitcher
	s.mu.RUnlock()

	portChanged := newPort != currentPort
	if portChanged && switcher != nil {
		if err := switcher(newPort); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "切换监听端口失败: " + err.Error()})
			return
		}
	}

	s.mu.Lock()
	s.port = newPort
	s.mu.Unlock()

	if cleanIP == "" && len(cleanExposeIPs) > 0 {
		cleanIP = cleanExposeIPs[0]
	}
	newEntryURL := ResolveEntryURL(cleanIP, newPort)
	if err := s.cluster.SetEndpointNICsConfig(cleanIP, cleanExposeIPs, cleanDisabledIPs, updateNICSelection, newPort, newEntryURL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存入口设置失败: " + err.Error()})
		return
	}

	allAddrs, activeAddrs, primaryURL := s.allConfiguredAddrs()
	if primaryURL != "" && primaryURL != newEntryURL {
		newEntryURL = primaryURL
		_ = s.cluster.SetEndpointNICsConfig(cleanIP, cleanExposeIPs, cleanDisabledIPs, updateNICSelection, newPort, newEntryURL)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entry_url":       newEntryURL,
		"entry_addrs":     activeAddrs,
		"available_addrs": allAddrs,
		"expose_ip":       cleanIP,
		"expose_ips":      s.cluster.ExposeIPs(),
		"disabled_ips":    s.cluster.DisabledIPs(),
		"port":            newPort,
		"port_changed":    portChanged,
		"pool":            s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleGetAdminSettings(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":      s.auth.Settings(),
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleUpdateAdminSettings(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		InviteRequired *bool     `json:"invite_required"`
		AllowedExts    *[]string `json:"allowed_exts"`
		BlockedExts    *[]string `json:"blocked_exts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	settings, err := s.auth.UpdateSettings(req.InviteRequired, req.AllowedExts, req.BlockedExts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "policy",
		Uploader: u.Username,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":      settings,
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"users":         s.auth.ListUsers(),
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleApproveUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	approved, err := s.auth.ApproveUser(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":          approved,
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	updated, err := s.auth.SetUserRole(u.Username, target, req.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "role_change",
		Uploader: target,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":          updated,
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleBanUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	updated, err := s.auth.SetUserBanned(u.Username, target, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": updated})
}

func (s *Server) handleUnbanUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	updated, err := s.auth.SetUserBanned(u.Username, target, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": updated})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	if err := s.auth.DeleteUser(u.Username, target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"invites": s.auth.ListInvites(),
		"users":   s.auth.ListUsers(),
	})
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Code        string `json:"code"`
		Note        string `json:"note"`
		MaxUses     int    `json:"max_uses"`
		ExpireHours int    `json:"expire_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	inv, err := s.auth.CreateInvite(u.Username, req.Code, req.Note, req.MaxUses, req.ExpireHours)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv})
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	code := r.PathValue("code")
	if err := s.auth.DeleteInvite(code); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func parseAllowedUsersForm(vals []string) []string {
	var out []string
	for _, raw := range vals {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "[") {
			var parsed []string
			if json.Unmarshal([]byte(s), &parsed) == nil {
				out = append(out, parsed...)
				continue
			}
		}
		parts := strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == '，'
		})
		out = append(out, parts...)
	}
	return storage.NormalizeAllowedUsers(out)
}

func (s *Server) sanitizeRecordForUser(rec storage.FileRecord, u auth.PublicUser) storage.FileRecord {
	if u.Role != "admin" {
		rec.StorageDir = ""
		rec.SourcePath = ""
		if len(rec.Versions) > 0 {
			cleanVers := make([]storage.FileVersion, len(rec.Versions))
			for i, v := range rec.Versions {
				v.StorageDir = ""
				cleanVers[i] = v
			}
			rec.Versions = cleanVers
		}
	}
	return rec
}

func (s *Server) sanitizeRecordsForUser(recs []storage.FileRecord, u auth.PublicUser) []storage.FileRecord {
	if u.Role == "admin" {
		return recs
	}
	out := make([]storage.FileRecord, len(recs))
	for i, rec := range recs {
		out[i] = s.sanitizeRecordForUser(rec, u)
	}
	return out
}

func (s *Server) findAccessibleFile(id string, u auth.PublicUser) (rec storage.FileRecord, localPath string, peer cluster.NodeStatus, isLocal bool, status int, errMsg string) {
	if rLocal, fp, ok := s.store.Get(id); ok {
		rLocal.NodeID = s.cluster.NodeID()
		rLocal.NodeName = s.cluster.NodeName()
		rLocal.NodeOS = runtime.GOOS
		rLocal.NodeOnline = true
		rLocal.StorageDir = s.store.RecordDir(rLocal)
		if !storage.CanUserAccess(rLocal, u.Username, u.Role) {
			return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在或无权访问"
		}
		return rLocal, fp, cluster.NodeStatus{}, true, http.StatusOK, ""
	}
	rPeer, p, ok := s.cluster.LocateFile(id)
	if !ok {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在"
	}
	if !storage.CanUserAccess(rPeer, u.Username, u.Role) {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在或无权访问"
	}
	if !p.Online {
		return rPeer, "", p, false, http.StatusServiceUnavailable, "该文件所在节点当前离线，请开机后重试"
	}
	return rPeer, "", p, false, http.StatusOK, ""
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	q := r.URL.Query()
	accessible := storage.FilterAccessibleRecords(s.cluster.AllClusterFiles(), u.Username, u.Role)
	list := storage.FilterRecords(
		accessible,
		q.Get("category"),
		q.Get("month"),
		q.Get("date"),
		q.Get("device"),
		q.Get("node"),
		q.Get("q"),
	)
	writeJSON(w, http.StatusOK, map[string]any{"files": s.sanitizeRecordsForUser(list, u)})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	q := r.URL.Query()
	accessible := storage.FilterAccessibleRecords(s.cluster.AllClusterFiles(), u.Username, u.Role)
	sum := storage.SummarizeRecords(
		accessible,
		q.Get("category"),
		q.Get("month"),
		q.Get("node"),
	)
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析上传表单失败: " + err.Error()})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		headers = r.MultipartForm.File["file"]
	}
	if len(headers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未收到任何文件"})
		return
	}

	rawAllowed := parseAllowedUsersForm(r.MultipartForm.Value["allowed_users"])
	allowedUsers, userErr := s.auth.ValidateExistingUsers(rawAllowed)
	if userErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": userErr.Error()})
		return
	}

	dev := extractDeviceInfo(r)
	targetPref := strings.TrimSpace(r.FormValue("target_node"))
	if targetPref == "" || u.Role != "admin" {
		targetPref = "auto"
	}
	relPaths := r.MultipartForm.Value["relative_paths"]
	fallbackFolder := storage.SanitizeFolderPath(r.FormValue("folder_path"))

	for _, fh := range headers {
		if err := s.auth.CheckUploadAllowed(fh.Filename); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	uploaded := make([]storage.FileRecord, 0, len(headers))
	for i, fh := range headers {
		folderPath := fallbackFolder
		if i < len(relPaths) && strings.TrimSpace(relPaths[i]) != "" {
			cleanRel := strings.ReplaceAll(strings.TrimSpace(relPaths[i]), "\\", "/")
			if strings.Contains(cleanRel, "/") {
				dir := path.Dir(cleanRel)
				if dir != "." {
					folderPath = storage.SanitizeFolderPath(dir)
				}
			}
		}

		effectiveTargetPref := targetPref
		if effectiveTargetPref == "auto" {
			cleanBase := filepath.Base(strings.TrimSpace(fh.Filename))
			for _, existing := range s.cluster.AllClusterFiles() {
				if existing.NodeOnline && existing.NodeID != "" &&
					strings.EqualFold(existing.Name, cleanBase) &&
					existing.FolderPath == folderPath {
					if _, err := s.cluster.SelectUploadTarget(existing.NodeID, fh.Size); err == nil {
						effectiveTargetPref = existing.NodeID
					}
					break
				}
			}
		}

		target, err := s.cluster.SelectUploadTarget(effectiveTargetPref, fh.Size)
		if err != nil {
			writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
			return
		}

		f, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取上传文件失败"})
			return
		}

		if target.IsSelf {
			if capErr := s.cluster.CanAcceptLocal(fh.Size); capErr != nil {
				_ = f.Close()
				writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": capErr.Error()})
				return
			}
			rec, saveErr := s.store.SaveUploadWithOptions(fh.Filename, folderPath, u.Username, allowedUsers, dev, f)
			_ = f.Close()
			if saveErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件失败: " + saveErr.Error()})
				return
			}
			rec.NodeID = s.cluster.NodeID()
			rec.NodeName = s.cluster.NodeName()
			rec.NodeOS = runtime.GOOS
			rec.NodeOnline = true
			uploaded = append(uploaded, rec)
		} else {
			rec, proxyErr := s.cluster.ProxyUpload(target, fh.Filename, folderPath, f, u.Username, allowedUsers, dev)
			_ = f.Close()
			if proxyErr != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": proxyErr.Error()})
				return
			}
			uploaded = append(uploaded, rec)
		}
	}
	s.publishRecordChange("upload", u.Username, uploaded, allowedUsers)
	writeJSON(w, http.StatusCreated, map[string]any{"uploaded": s.sanitizeRecordsForUser(uploaded, u)})
}

func (s *Server) handleCreateTxtFile(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Name         string   `json:"name"`
		FolderPath   string   `json:"folder_path"`
		Content      string   `json:"content"`
		AllowedUsers []string `json:"allowed_users"`
		TargetNode   string   `json:"target_node"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	rawName := filepath.Base(strings.TrimSpace(req.Name))
	if rawName == "" || rawName == "." || rawName == ".." {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请输入有效的 TXT 文件名"})
		return
	}
	ext := filepath.Ext(rawName)
	if ext != "" && strings.ToLower(ext) != ".txt" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新建文件功能仅支持创建 .txt 文本文件"})
		return
	}
	if ext == "" {
		rawName += ".txt"
	} else {
		rawName = strings.TrimSuffix(rawName, ext) + ".txt"
	}

	if err := s.auth.CheckUploadAllowed(rawName); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cleanAllowed, userErr := s.auth.ValidateExistingUsers(storage.NormalizeAllowedUsers(req.AllowedUsers))
	if userErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": userErr.Error()})
		return
	}
	folderPath := storage.SanitizeFolderPath(req.FolderPath)
	dev := extractDeviceInfo(r)
	contentBytes := []byte(req.Content)
	sz := int64(len(contentBytes))

	targetPref := strings.TrimSpace(req.TargetNode)
	if targetPref == "" || u.Role != "admin" {
		targetPref = "auto"
	}
	if targetPref == "auto" {
		for _, existing := range s.cluster.AllClusterFiles() {
			if existing.NodeOnline && existing.NodeID != "" &&
				strings.EqualFold(existing.Name, rawName) &&
				existing.FolderPath == folderPath {
				if _, err := s.cluster.SelectUploadTarget(existing.NodeID, sz); err == nil {
					targetPref = existing.NodeID
				}
				break
			}
		}
	}

	target, err := s.cluster.SelectUploadTarget(targetPref, sz)
	if err != nil {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
		return
	}

	var saved storage.FileRecord
	if target.IsSelf {
		if capErr := s.cluster.CanAcceptLocal(sz); capErr != nil {
			writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": capErr.Error()})
			return
		}
		saved, err = s.store.SaveUploadWithVersionNote(rawName, folderPath, "新建 TXT 文件", u.Username, cleanAllowed, dev, bytes.NewReader(contentBytes))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建文件失败: " + err.Error()})
			return
		}
		saved.NodeID = s.cluster.NodeID()
		saved.NodeName = s.cluster.NodeName()
		saved.NodeOS = runtime.GOOS
		saved.NodeOnline = true
	} else {
		saved, err = s.cluster.ProxyUpload(target, rawName, folderPath, bytes.NewReader(contentBytes), u.Username, cleanAllowed, dev)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}

	s.publishRecordChange("upload", u.Username, []storage.FileRecord{saved}, cleanAllowed)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForUser(saved, u),
	})
}

// HandleCapturedClipboard saves a local OS clipboard capture into this node's store and notifies cluster peers.
func (s *Server) HandleCapturedClipboard(item clipboard.Item) {
	if len(item.Data) == 0 {
		return
	}
	if err := s.cluster.CanAcceptLocal(int64(len(item.Data))); err != nil {
		return
	}
	name := clipboard.AutoFileName(item)
	if err := s.auth.CheckUploadAllowed(name); err != nil {
		return
	}
	folder := clipboard.FolderPath(item.CreatedAt)
	primaryIP := ""
	for _, a := range DiscoverNetworkAddrs(s.Port()) {
		if a.Primary {
			primaryIP = a.IP
			break
		}
	}
	nodeName := s.cluster.NodeName()
	dev := storage.DeviceInfo{
		FP:     "CLIP-" + strings.ToUpper(s.cluster.NodeID()),
		Name:   nodeName + " · 超级剪贴板",
		IP:     primaryIP,
		Detail: runtime.GOOS + "/" + runtime.GOARCH,
	}
	uploader := "超级剪贴板(" + nodeName + ")"
	rec, err := s.store.SaveUploadWithVersionNote(name, folder, "超级剪贴板自动上传", uploader, nil, dev, bytes.NewReader(item.Data))
	if err != nil {
		return
	}
	rec.NodeID = s.cluster.NodeID()
	rec.NodeName = nodeName
	rec.NodeOS = runtime.GOOS
	rec.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	s.publishRecordChange("upload", uploader, []storage.FileRecord{rec}, nil)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		if status == http.StatusNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, errMsg, status)
		}
		return
	}
	if isLocal {
		s.serveFileAttachment(w, r, id)
		return
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/download")
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		if status == http.StatusNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, errMsg, status)
		}
		return
	}
	if isLocal {
		s.serveFileRaw(w, r, id)
		return
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/raw")
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if isLocal {
		s.serveFilePreview(w, id)
		return
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/preview")
}

func (s *Server) handleFileMeta(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	includePath := u.Role == "admin"
	if isLocal {
		s.serveFileMeta(w, id, includePath)
		return
	}
	flag := "0"
	if includePath {
		flag = "1"
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/meta?include_path="+flag)
}

func (s *Server) handleArchiveEntry(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if isLocal {
		s.serveArchiveEntry(w, r, id)
		return
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/archive-entry")
}

func (s *Server) handleUpdateFileAccess(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, _, _, _, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if u.Role != "admin" && rec.Uploader != u.Username {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅上传者或管理员可修改文件可见权限"})
		return
	}
	var req struct {
		AllowedUsers []string `json:"allowed_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	cleanUsers, err := s.auth.ValidateExistingUsers(req.AllowedUsers)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, err := s.cluster.UpdateFileAccess(id, cleanUsers)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "access",
		Uploader: u.Username,
		Count:    1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"file": s.sanitizeRecordForUser(updated, u)})
}

func archiveBaseFolderName(name string) string {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, ".tar.gz") {
		return name[:len(name)-len(".tar.gz")]
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" {
		return "extracted"
	}
	return base
}

func (s *Server) handleExtractArchive(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, localPath, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	var cleanup func()
	if !isLocal {
		tmpPath, fn, err := s.cluster.FetchPeerFileToTemp(peer, id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "读取远端压缩包失败: " + err.Error()})
			return
		}
		localPath = tmpPath
		cleanup = fn
	}
	if cleanup != nil {
		defer cleanup()
	}

	if !preview.IsArchiveName(rec.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "仅支持在线解压 .zip / .tar / .tar.gz / .tgz / .gz 格式"})
		return
	}

	dev := extractDeviceInfo(r)
	archiveFolder := storage.SanitizeFolderPath(archiveBaseFolderName(rec.Name))
	targetPref := strings.TrimSpace(r.URL.Query().Get("target_node"))
	if targetPref == "" || u.Role != "admin" {
		targetPref = "auto"
	}

	extracted := make([]storage.FileRecord, 0)
	skipped := make([]string, 0)

	err := preview.WalkArchiveFiles(localPath, rec.Name, 200, 256*1024*1024, func(relPath string, sz int64, rd io.Reader) error {
		base := path.Base(relPath)
		dir := path.Dir(relPath)
		if dir == "." {
			dir = ""
		}
		targetFolder := storage.SanitizeFolderPath(archiveFolder + "/" + dir)

		if err := s.auth.CheckUploadAllowed(base); err != nil {
			skipped = append(skipped, relPath+" ("+err.Error()+")")
			return nil
		}

		target, err := s.cluster.SelectUploadTarget(targetPref, sz)
		if err != nil {
			skipped = append(skipped, relPath+" ("+err.Error()+")")
			return nil
		}

		if target.IsSelf {
			if capErr := s.cluster.CanAcceptLocal(sz); capErr != nil {
				skipped = append(skipped, relPath+" ("+capErr.Error()+")")
				return nil
			}
			saved, saveErr := s.store.SaveUploadWithOptions(base, targetFolder, u.Username, rec.AllowedUsers, dev, rd)
			if saveErr != nil {
				return saveErr
			}
			saved.NodeID = s.cluster.NodeID()
			saved.NodeName = s.cluster.NodeName()
			saved.NodeOS = runtime.GOOS
			saved.NodeOnline = true
			extracted = append(extracted, saved)
		} else {
			saved, proxyErr := s.cluster.ProxyUpload(target, base, targetFolder, rd, u.Username, rec.AllowedUsers, dev)
			if proxyErr != nil {
				return proxyErr
			}
			extracted = append(extracted, saved)
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.publishRecordChange("extract", u.Username, extracted, rec.AllowedUsers)
	writeJSON(w, http.StatusCreated, map[string]any{
		"extracted": s.sanitizeRecordsForUser(extracted, u),
		"skipped":   skipped,
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if u.Role != "admin" && rec.Uploader != u.Username {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅上传者或管理员可删除该文件"})
		return
	}

	if isLocal {
		if err := s.store.Delete(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.events.Publish(FileChangeEvent{
			Kind:         "delete",
			Uploader:     rec.Uploader,
			Count:        1,
			FileNames:    []string{rec.Name},
			NodeName:     s.cluster.NodeName(),
			AllowedUsers: rec.AllowedUsers,
		})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	if err := s.cluster.ProxyDelete(peer, id); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:         "delete",
		Uploader:     rec.Uploader,
		Count:        1,
		FileNames:    []string{rec.Name},
		NodeName:     rec.NodeName,
		AllowedUsers: rec.AllowedUsers,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEditFile(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if !preview.IsTextPreviewExt(rec.Ext) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "该文件类型不支持在线文本编辑"})
		return
	}
	var req struct {
		Content string `json:"content"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析编辑内容失败"})
		return
	}
	dev := extractDeviceInfo(r)
	note := strings.TrimSpace(req.Note)
	var updated storage.FileRecord
	var err error
	if isLocal {
		if err := s.cluster.CanAcceptLocal(int64(len(req.Content))); err != nil {
			writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
			return
		}
		updated, err = s.store.SaveEditedText(id, req.Content, note, u.Username, dev)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		updated.NodeID = s.cluster.NodeID()
		updated.NodeName = s.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "文件所在存储节点当前离线"})
			return
		}
		updated, err = s.cluster.ProxyEditFile(peer, id, req.Content, note, u.Username, dev)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	s.publishRecordChange("upload", u.Username, []storage.FileRecord{updated}, rec.AllowedUsers)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForUser(updated, u),
	})
}

func (s *Server) handleGetFileVersions(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, _, _, _, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	clean := s.sanitizeRecordForUser(rec, u)
	curVer := clean.Version
	if curVer <= 0 {
		curVer = 1
	}
	currentItem := storage.FileVersion{
		Version:      curVer,
		StoredName:   clean.StoredName,
		Size:         clean.Size,
		Uploader:     clean.Uploader,
		UploadedAt:   clean.UploadedAt,
		DeviceFP:     clean.DeviceFP,
		DeviceName:   clean.DeviceName,
		DeviceIP:     clean.DeviceIP,
		DeviceDetail: clean.DeviceDetail,
		StorageDir:   clean.StorageDir,
		Note:         clean.VersionNote,
		UpdateMethod: storage.DefaultUpdateMethod(clean.UpdateMethod, curVer),
	}
	allVersions := make([]storage.FileVersion, 0, 1+len(clean.Versions))
	allVersions = append(allVersions, currentItem)
	for _, v := range clean.Versions {
		v.UpdateMethod = storage.DefaultUpdateMethod(v.UpdateMethod, v.Version)
		allVersions = append(allVersions, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":            clean,
		"current_version": curVer,
		"version_count":   len(allVersions),
		"versions":        allVersions,
	})
}

func (s *Server) handleUploadFileVersion(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析上传表单失败"})
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	fh, err := firstMultipartFile(r, "file", "files")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择要上传的新版本文件"})
		return
	}
	if err := s.auth.CheckUploadAllowed(fh.Filename); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dev := extractDeviceInfo(r)
	f, err := fh.Open()
	if err != nil {
		s.store.RecordReadError()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取上传文件失败"})
		return
	}
	defer f.Close()

	var updated storage.FileRecord
	if isLocal {
		if err := s.cluster.CanAcceptLocal(fh.Size); err != nil {
			writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
			return
		}
		updated, err = s.store.SaveNewVersion(id, fh.Filename, note, u.Username, dev, f)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		updated.NodeID = s.cluster.NodeID()
		updated.NodeName = s.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "文件所在存储节点当前离线"})
			return
		}
		updated, err = s.cluster.ProxyNewVersion(peer, id, fh.Filename, note, f, u.Username, dev)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}

	s.publishRecordChange("upload", u.Username, []storage.FileRecord{updated}, rec.AllowedUsers)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForUser(updated, u),
	})
}

func (s *Server) handleDownloadFileVersion(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "版本号无效"})
		return
	}
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if isLocal {
		s.serveVersionAttachment(w, r, id, ver)
		return
	}
	if !peer.Online {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "文件所在存储节点当前离线"})
		return
	}
	s.cluster.ProxyGet(w, r, peer, fmt.Sprintf("/internal/cluster/files/%s/versions/%d/download", id, ver))
}

func (s *Server) handleRestoreFileVersion(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "版本号无效"})
		return
	}
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if u.Role != "admin" && rec.Uploader != u.Username {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅文件上传者或管理员可切换/回滚文件版本"})
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	dev := extractDeviceInfo(r)

	var updated storage.FileRecord
	if isLocal {
		updated, err = s.store.RestoreVersionByUser(id, ver, req.Note, u.Username, dev)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		updated.NodeID = s.cluster.NodeID()
		updated.NodeName = s.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "文件所在存储节点当前离线"})
			return
		}
		updated, err = s.cluster.ProxyRestoreVersion(peer, id, ver, req.Note, u.Username, dev)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	s.publishRecordChange("upload", u.Username, []storage.FileRecord{updated}, rec.AllowedUsers)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForUser(updated, u),
	})
}

func (s *Server) handleDeleteFileVersion(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	ver, err := strconv.Atoi(r.PathValue("ver"))
	if err != nil || ver <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "版本号无效"})
		return
	}
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFile(id, u)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if u.Role != "admin" && rec.Uploader != u.Username {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅文件上传者或管理员可删除历史版本"})
		return
	}
	var updated storage.FileRecord
	if isLocal {
		updated, err = s.store.DeleteVersion(id, ver)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		updated.NodeID = s.cluster.NodeID()
		updated.NodeName = s.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "文件所在存储节点当前离线"})
			return
		}
		updated, err = s.cluster.ProxyDeleteVersion(peer, id, ver)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	s.publishRecordChange("upload", u.Username, []storage.FileRecord{updated}, rec.AllowedUsers)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForUser(updated, u),
	})
}

func (s *Server) publishRecordChange(kind, uploader string, recs []storage.FileRecord, allowedUsers []string) {
	if len(recs) == 0 {
		return
	}
	names := make([]string, 0, min(len(recs), 3))
	nodeName := ""
	for i, rec := range recs {
		if i < 3 {
			names = append(names, rec.Name)
		}
		if nodeName == "" && rec.NodeName != "" {
			nodeName = rec.NodeName
		}
	}
	s.events.Publish(FileChangeEvent{
		Kind:         kind,
		Uploader:     uploader,
		Count:        len(recs),
		FileNames:    names,
		NodeName:     nodeName,
		AllowedUsers: allowedUsers,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前连接不支持流式推送"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := s.events.Subscribe()
	defer unsub()

	_, _ = fmt.Fprintf(w, "event: ready\ndata: {\"version\":%d,\"app_version\":%q}\n\n", s.events.Version(), version.Version)
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.events.Done():
			return
		case ev := <-ch:
			if len(ev.AllowedUsers) > 0 && !storage.CanUserAccess(storage.FileRecord{Uploader: ev.Uploader, AllowedUsers: ev.AllowedUsers}, u.Username, u.Role) {
				continue
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: change\ndata: %s\n\n", b)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// ==================== Local File Serving Helpers ====================

type downloadCountWriter struct {
	http.ResponseWriter
	store *storage.Store
}

func (dw *downloadCountWriter) Write(p []byte) (int, error) {
	n, err := dw.ResponseWriter.Write(p)
	if n > 0 && dw.store != nil {
		dw.store.AddDownloadBytes(int64(n))
	}
	return n, err
}

func isExternalSourceMissingErr(rec storage.FileRecord, err error) bool {
	if err == nil || !os.IsNotExist(err) {
		return false
	}
	return rec.StorageMode == storage.StorageModeRef || rec.StorageMode == storage.StorageModeMount
}

func (s *Server) serveFileAttachment(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		s.store.RecordReadError()
		if isExternalSourceMissingErr(rec, err) {
			http.Error(w, "源文件已移动或删除，外部引用已失效", http.StatusGone)
			return
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		s.store.RecordReadError()
		http.NotFound(w, r)
		return
	}
	escaped := url.PathEscape(rec.Name)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+escaped)
	http.ServeContent(&downloadCountWriter{ResponseWriter: w, store: s.store}, r, rec.Name, info.ModTime(), f)
}

func (s *Server) serveVersionAttachment(w http.ResponseWriter, r *http.Request, id string, ver int) {
	rec, fv, fp, ok := s.store.GetVersion(id, ver)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		s.store.RecordReadError()
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		s.store.RecordReadError()
		http.NotFound(w, r)
		return
	}
	ext := filepath.Ext(rec.Name)
	base := strings.TrimSuffix(rec.Name, ext)
	dlName := fmt.Sprintf("%s (v%d)%s", base, fv.Version, ext)
	escaped := url.PathEscape(dlName)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+escaped)
	http.ServeContent(&downloadCountWriter{ResponseWriter: w, store: s.store}, r, dlName, info.ModTime(), f)
}

func (s *Server) serveFileRaw(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		s.store.RecordReadError()
		if isExternalSourceMissingErr(rec, err) {
			http.Error(w, "源文件已移动或删除，外部引用已失效", http.StatusGone)
			return
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		s.store.RecordReadError()
		http.NotFound(w, r)
		return
	}
	if ct := detectContentType(rec.Ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(rec.Name))
	http.ServeContent(&downloadCountWriter{ResponseWriter: w, store: s.store}, r, rec.Name, info.ModTime(), f)
}

func (s *Server) serveFilePreview(w http.ResponseWriter, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}
	if _, statErr := os.Stat(fp); isExternalSourceMissingErr(rec, statErr) {
		s.store.RecordReadError()
		writeJSON(w, http.StatusGone, map[string]string{"error": "源文件已移动或删除，外部引用已失效"})
		return
	}

	ext := strings.ToLower(strings.TrimPrefix(rec.Ext, "."))
	if preview.IsArchiveName(rec.Name) || ext == "rar" || ext == "7z" {
		res, err := preview.InspectArchive(fp, rec.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if preview.IsTextPreviewExt(ext) {
		raw, err := os.ReadFile(fp)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
			return
		}
		truncated := false
		if len(raw) > preview.MaxTextPreviewBytes {
			raw = raw[:preview.MaxTextPreviewBytes]
			truncated = true
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"type":      ext,
			"is_md":     ext == "md" || ext == "markdown",
			"content":   string(raw),
			"truncated": truncated,
		})
		return
	}

	switch ext {
	case "docx", "xlsx", "pptx", "doc", "xls", "ppt":
		res, err := preview.ParseOffice(fp, ext)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "该格式请使用原生媒体/PDF 预览或直接下载"})
	}
}

func (s *Server) serveFileMeta(w http.ResponseWriter, id string, includePath bool) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}
	if rec.NodeID == "" {
		rec.NodeID = s.cluster.NodeID()
		rec.NodeName = s.cluster.NodeName()
		rec.NodeOS = runtime.GOOS
		rec.NodeOnline = true
	}
	if includePath {
		rec.StorageDir = filepath.Dir(fp)
	} else {
		rec.StorageDir = ""
		rec.SourcePath = ""
	}
	storageMode := rec.StorageMode
	if storageMode == "" {
		storageMode = storage.StorageModeCopy
	}

	if _, statErr := os.Stat(fp); isExternalSourceMissingErr(rec, statErr) {
		s.store.RecordReadError()
		writeJSON(w, http.StatusGone, map[string]string{"error": "源文件已移动或删除，外部引用已失效"})
		return
	}

	ct := detectContentType(rec.Ext)
	if ct == "" {
		ct = "application/octet-stream"
	}

	const maxMetaHashBytes int64 = 256 * 1024 * 1024
	var shaHex string
	shaSkipped := false
	if rec.Size > maxMetaHashBytes {
		shaSkipped = true
	} else {
		f, err := os.Open(fp)
		if err != nil {
			if isExternalSourceMissingErr(rec, err) {
				writeJSON(w, http.StatusGone, map[string]string{"error": "源文件已移动或删除，外部引用已失效"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取本地文件失败"})
			return
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		_ = f.Close()
		if copyErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "计算 SHA-256 失败"})
			return
		}
		shaHex = hex.EncodeToString(h.Sum(nil))
	}

	isArch := preview.IsArchiveName(rec.Name)
	canPrev := isArch || preview.CanPreviewExt(rec.Ext)
	resp := map[string]any{
		"file":           rec,
		"content_type":   ct,
		"sha256":         shaHex,
		"sha256_skipped": shaSkipped,
		"can_preview":    canPrev,
		"is_archive":     isArch,
		"storage_mode":   storageMode,
		"source_missing": rec.SourceMissing,
	}
	if includePath {
		resp["disk_path"] = fp
		resp["storage_dir"] = filepath.Dir(fp)
		if rec.SourcePath != "" {
			resp["source_path"] = rec.SourcePath
		}
	}
	if isArch {
		if archRes, err := preview.InspectArchive(fp, rec.Name); err == nil {
			resp["archive_info"] = map[string]any{
				"format":      archRes.Format,
				"total_files": archRes.TotalFiles,
				"total_dirs":  archRes.TotalDirs,
				"total_size":  archRes.TotalSize,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) serveArchiveEntry(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}
	entryPath := r.URL.Query().Get("path")
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "preview"
	}

	data, entryName, ext, err := preview.ReadArchiveEntry(fp, rec.Name, entryPath, preview.MaxArchiveEntryReadBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	switch mode {
	case "download":
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(entryName))
		http.ServeContent(w, r, entryName, rec.UploadedAt, bytes.NewReader(data))
	case "raw":
		if ct := detectContentType("." + ext); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(entryName))
		http.ServeContent(w, r, entryName, rec.UploadedAt, bytes.NewReader(data))
	case "preview":
		switch ext {
		case "docx", "xlsx", "pptx", "doc", "xls", "ppt":
			tmp, err := os.CreateTemp("", "lan-filehub-entry-*."+ext)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建临时预览文件失败"})
				return
			}
			tmpPath := tmp.Name()
			defer os.Remove(tmpPath)
			if _, err := tmp.Write(data); err != nil {
				_ = tmp.Close()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入临时预览文件失败"})
				return
			}
			_ = tmp.Close()
			res, err := preview.ParseOffice(tmpPath, ext)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, res)
		default:
			if !preview.IsTextPreviewExt(ext) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "该格式请使用原生预览或直接下载"})
				return
			}
			truncated := false
			if len(data) > preview.MaxTextPreviewBytes {
				data = data[:preview.MaxTextPreviewBytes]
				truncated = true
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"type":      ext,
				"is_md":     ext == "md" || ext == "markdown",
				"content":   string(data),
				"truncated": truncated,
			})
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不支持的预览模式"})
	}
}

// ==================== Device Fingerprint & Network Helpers ====================

func decodeHeaderValue(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "%") {
		if dec, err := url.QueryUnescape(s); err == nil && dec != "" {
			return strings.TrimSpace(dec)
		}
	}
	return s
}

func extractDeviceInfo(r *http.Request) storage.DeviceInfo {
	clientIP := resolveClientIP(r)
	fp := truncateRunes(strings.TrimSpace(r.FormValue("device_fp")), 64)
	if fp == "" {
		fp = truncateRunes(decodeHeaderValue(r.Header.Get("X-Device-FP")), 64)
	}
	name := truncateRunes(strings.TrimSpace(r.FormValue("device_name")), 80)
	if name == "" {
		name = truncateRunes(decodeHeaderValue(r.Header.Get("X-Device-Name")), 80)
	}
	detail := truncateRunes(strings.TrimSpace(r.FormValue("device_detail")), 160)
	if detail == "" {
		detail = truncateRunes(decodeHeaderValue(r.Header.Get("X-Device-Detail")), 160)
	}

	if name == "" {
		name = deriveDeviceName(r.UserAgent())
	}
	if fp == "" {
		sum := sha256.Sum256([]byte(clientIP + "|" + r.UserAgent()))
		fp = "FP-" + strings.ToUpper(hex.EncodeToString(sum[:])[:10])
	}
	if detail == "" && r.UserAgent() != "" {
		detail = truncateRunes(r.UserAgent(), 120)
	}
	return storage.DeviceInfo{
		FP:     fp,
		Name:   name,
		IP:     clientIP,
		Detail: detail,
	}
}

func resolveClientIP(r *http.Request) string {
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return normalizeIP(xr)
	}
	if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xf != "" {
		first := strings.TrimSpace(strings.Split(xf, ",")[0])
		if first != "" {
			return normalizeIP(first)
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return normalizeIP(strings.TrimSpace(r.RemoteAddr))
	}
	return normalizeIP(host)
}

func normalizeIP(ip string) string {
	if ip == "::1" {
		return "127.0.0.1"
	}
	return strings.TrimPrefix(ip, "::ffff:")
}

func deriveDeviceName(ua string) string {
	low := strings.ToLower(ua)
	osName := "未知设备"
	switch {
	case strings.Contains(low, "iphone"):
		osName = "iPhone"
	case strings.Contains(low, "ipad"):
		osName = "iPad"
	case strings.Contains(low, "android"):
		osName = "Android"
	case strings.Contains(low, "windows"):
		osName = "Windows"
	case strings.Contains(low, "mac os x") || strings.Contains(low, "macintosh"):
		osName = "macOS"
	case strings.Contains(low, "linux"):
		osName = "Linux"
	}

	browser := ""
	switch {
	case strings.Contains(low, "edg/"):
		browser = "Edge"
	case strings.Contains(low, "micromessenger"):
		browser = "微信浏览器"
	case strings.Contains(low, "opr/") || strings.Contains(low, "opera"):
		browser = "Opera"
	case strings.Contains(low, "firefox/"):
		browser = "Firefox"
	case strings.Contains(low, "chrome/") || strings.Contains(low, "crios/"):
		browser = "Chrome"
	case strings.Contains(low, "safari/"):
		browser = "Safari"
	case strings.Contains(low, "curl/"):
		browser = "curl"
	}

	if browser != "" && osName != "未知设备" {
		return osName + " · " + browser
	}
	if browser != "" {
		return browser
	}
	return osName
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func detectContentType(ext string) string {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log", ".csv", ".go", ".js", ".ts", ".py", ".sh", ".bat", ".ini", ".sql", ".yaml", ".yml":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return mime.TypeByExtension(filepath.Ext(ext))
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type scoredAddr struct {
	addr  NetworkAddr
	score int
}

type ifaceCandidate struct {
	Iface string
	IP    net.IP
}

func DiscoverNetworkAddrs(port string) []NetworkAddr {
	var candidates []ifaceCandidate
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			candidates = append(candidates, ifaceCandidate{
				Iface: iface.Name,
				IP:    ip,
			})
		}
	}
	return scoreNetworkCandidates(candidates, port)
}

func scoreNetworkCandidates(candidates []ifaceCandidate, port string) []NetworkAddr {
	var list []scoredAddr
	for _, c := range candidates {
		ip4 := c.IP.To4()
		if ip4 == nil || ip4.IsLoopback() {
			continue
		}
		if (ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)) || (ip4[0] == 169 && ip4[1] == 254) {
			continue
		}

		score, tag := classifyCandidate(c.Iface, ip4)
		list = append(list, scoredAddr{
			score: score,
			addr: NetworkAddr{
				URL:   "http://" + ip4.String() + ":" + port,
				IP:    ip4.String(),
				Iface: c.Iface,
				Tag:   tag,
			},
		})
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].addr.IP < list[j].addr.IP
	})

	out := make([]NetworkAddr, len(list))
	primarySet := false
	for i, item := range list {
		out[i] = item.addr
		if !primarySet && (item.score >= 80 || i == 0) {
			out[i].Primary = true
			primarySet = true
		}
	}
	return out
}

func classifyCandidate(ifaceName string, ip4 net.IP) (int, string) {
	low := strings.ToLower(ifaceName)
	virtualKeywords := []string{
		"vmware", "vmnet", "vethernet", "virtualbox", "vbox",
		"docker", "wsl", "hyper-v", "mihomo", "clash", "tap",
		"bluetooth", "蓝牙", "corplink",
	}
	for _, kw := range virtualKeywords {
		if strings.Contains(low, kw) {
			return 20, "虚拟网卡"
		}
	}

	if (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) || strings.Contains(low, "tailscale") {
		return 60, "Tailscale"
	}

	isPrivate := ip4[0] == 192 && ip4[1] == 168 ||
		ip4[0] == 10 ||
		(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31)
	if isPrivate {
		if ip4[3] == 1 {
			return 40, "内网网关"
		}
		lanKeywords := []string{"wlan", "wi-fi", "wifi", "ethernet", "以太网", "en0", "eth"}
		for _, kw := range lanKeywords {
			if strings.Contains(low, kw) {
				return 100, "推荐局域网"
			}
		}
		return 85, "局域网"
	}

	return 50, "网络地址"
}
