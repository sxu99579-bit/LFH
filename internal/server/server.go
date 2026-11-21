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
	mux.HandleFunc("POST /internal/cluster/quota", s.requireClusterAuth(s.handleClusterSetQuota))
	mux.HandleFunc("POST /internal/cluster/storage-dir", s.requireClusterAuth(s.handleClusterSetStorageDir))

	// Public & user-facing endpoints
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("POST /api/cluster/adopt", s.handleClusterAdopt)
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
	mux.HandleFunc("DELETE /api/admin/cluster/nodes/{nodeID}", s.requireAdmin(s.handleAdminClusterRemoveNode))
	mux.HandleFunc("POST /api/admin/cluster/quota", s.requireAdmin(s.handleAdminClusterQuota))
	mux.HandleFunc("POST /api/admin/cluster/storage-dir", s.requireAdmin(s.handleAdminClusterStorageDir))
	mux.HandleFunc("POST /api/admin/cluster/endpoint", s.requireAdmin(s.handleAdminClusterEndpoint))
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
	mux.HandleFunc("GET /api/files/{id}/download", s.requireAuth(s.handleDownload))
	mux.HandleFunc("GET /api/files/{id}/raw", s.requireAuth(s.handleRaw))
	mux.HandleFunc("GET /api/files/{id}/preview", s.requireAuth(s.handlePreview))
	mux.HandleFunc("GET /api/files/{id}/meta", s.requireAuth(s.handleFileMeta))
	mux.HandleFunc("GET /api/files/{id}/archive-entry", s.requireAuth(s.handleArchiveEntry))
	mux.HandleFunc("POST /api/files/{id}/access", s.requireAuth(s.handleUpdateFileAccess))
	mux.HandleFunc("POST /api/files/{id}/extract", s.requireAuth(s.handleExtractArchive))
	mux.HandleFunc("DELETE /api/files/{id}", s.requireAuth(s.handleDelete))

	fileServer := http.FileServer(http.FS(s.webFS))
	mux.Handle("/", fileServer)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/cluster/") || r.URL.Path == "/api/info" || r.URL.Path == "/api/cluster/adopt" {
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
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><title>LFH 存储节点</title></head><body style="font-family:sans-serif;background:#0b1120;color:#f1f5f9;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;"><div style="text-align:center;"><h2>本机为 LFH 存储扩容节点（Worker）</h2><p>正在等待主入口节点上线或自动发现中，请从唯一主入口访问平台。</p></div></body></html>`))
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
		"ok":        true,
		"entry_url": s.cluster.EntryURL(),
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
	rec, saveErr := s.store.SaveUploadWithOptions(fh.Filename, folderPath, uploader, allowedUsers, dev, f)
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

// ==================== Public & Entry Handlers ====================

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name":        version.AppName,
		"version":         version.Version,
		"entry_url":       s.cluster.EntryURL(),
		"local_url":       "http://localhost:" + s.Port(),
		"role":            s.cluster.Role(),
		"invite_required": s.auth.Settings().InviteRequired,
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
	resp := map[string]any{
		"app_name":        version.AppName,
		"version":         version.Version,
		"user":            u,
		"entry_url":       s.cluster.EntryURL(),
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
	resp := map[string]any{
		"pool":           s.cluster.PoolSummaryForRole(u.Role == "admin"),
		"cluster_secret": "",
		"events_version": s.events.Version(),
	}
	if u.Role == "admin" {
		resp["cluster_secret"] = s.cluster.Secret()
		resp["expose_ip"] = s.cluster.ExposeIP()
		resp["port"] = port
		resp["available_addrs"] = DiscoverNetworkAddrs(port)
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
	if err := s.cluster.RemovePeer(nodeID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.events.Publish(FileChangeEvent{
		Kind:     "cluster_sync",
		NodeName: nodeID,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"pool": s.cluster.PoolSummaryForRole(true),
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

func (s *Server) handleAdminClusterEndpoint(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		ExposeIP string `json:"expose_ip"`
		Port     string `json:"port"`
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

	newEntryURL := ResolveEntryURL(cleanIP, newPort)
	if err := s.cluster.SetEndpointConfig(cleanIP, newPort, newEntryURL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存入口设置失败: " + err.Error()})
		return
	}

	s.mu.Lock()
	s.port = newPort
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"entry_url":    newEntryURL,
		"expose_ip":    cleanIP,
		"port":         newPort,
		"port_changed": portChanged,
		"pool":         s.cluster.PoolSummaryForRole(true),
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
	}
	return rec
}

func (s *Server) sanitizeRecordsForUser(recs []storage.FileRecord, u auth.PublicUser) []storage.FileRecord {
	if u.Role == "admin" {
		return recs
	}
	out := make([]storage.FileRecord, len(recs))
	for i, rec := range recs {
		rec.StorageDir = ""
		out[i] = rec
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

		target, err := s.cluster.SelectUploadTarget(targetPref, fh.Size)
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

func (s *Server) serveFileAttachment(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	escaped := url.PathEscape(rec.Name)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+escaped)
	http.ServeContent(w, r, rec.Name, info.ModTime(), f)
}

func (s *Server) serveFileRaw(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := detectContentType(rec.Ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(rec.Name))
	http.ServeContent(w, r, rec.Name, info.ModTime(), f)
}

func (s *Server) serveFilePreview(w http.ResponseWriter, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
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
	}
	if includePath {
		resp["disk_path"] = fp
		resp["storage_dir"] = filepath.Dir(fp)
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

func extractDeviceInfo(r *http.Request) storage.DeviceInfo {
	clientIP := resolveClientIP(r)
	fp := truncateRunes(strings.TrimSpace(r.FormValue("device_fp")), 64)
	name := truncateRunes(strings.TrimSpace(r.FormValue("device_name")), 80)
	detail := truncateRunes(strings.TrimSpace(r.FormValue("device_detail")), 160)

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
