package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/storage"
)

func parseOptionalIntString(raw string) (*int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return nil, errors.New("expected_version 必须为非负整数")
	}
	return &v, nil
}

func parseIfMatchVersion(r *http.Request) (*int, error) {
	if r == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" || raw == "*" {
		return nil, nil
	}
	s := strings.TrimSpace(strings.TrimPrefix(raw, "W/"))
	s = strings.Trim(s, `"`)
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return nil, errors.New("If-Match 请求头版本号格式无效")
	}
	return &v, nil
}

func resolveExpectedVersion(bodyVer *int, rawParam string, r *http.Request) (*int, error) {
	ifMatchVer, err := parseIfMatchVersion(r)
	if err != nil {
		return nil, err
	}
	var explicit *int
	if bodyVer != nil {
		if *bodyVer < 0 {
			return nil, errors.New("expected_version 不能为负数")
		}
		v := *bodyVer
		explicit = &v
	} else if strings.TrimSpace(rawParam) != "" {
		parsed, pErr := parseOptionalIntString(rawParam)
		if pErr != nil {
			return nil, pErr
		}
		explicit = parsed
	}
	if explicit != nil && ifMatchVer != nil && *explicit != *ifMatchVer {
		return nil, errors.New("expected_version 与 If-Match 请求头不一致")
	}
	if explicit != nil {
		return explicit, nil
	}
	return ifMatchVer, nil
}

func parseBoolFlag(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func principalActorKeyID(p auth.Principal) (string, string) {
	if p.IsAgent && p.AgentKey != nil {
		return p.AgentKey.ID, p.ActorLabel()
	}
	return "user:" + p.User.Username, p.User.Username
}

func concurrencyOptionsForPrincipal(p auth.Principal, expectedVer *int, forceLease bool) storage.WriteConcurrencyOptions {
	actorKeyID, _ := principalActorKeyID(p)
	allowForce := forceLease && p.User.Role == "admin" && !p.IsAgent
	return storage.WriteConcurrencyOptions{
		ExpectedVersion: expectedVer,
		ActorKeyID:      actorKeyID,
		ForceLease:      allowForce,
	}
}

func writeConcurrencyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var conflict *storage.ConflictError
	if errors.As(err, &conflict) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    conflict.Error(),
			"code":     "version_conflict",
			"conflict": conflict,
		})
		return true
	}
	var leaseErr *storage.LeaseLockedError
	if errors.As(err, &leaseErr) {
		writeJSON(w, http.StatusLocked, map[string]any{
			"error": leaseErr.Error(),
			"code":  "lease_locked",
			"lease": leaseErr,
		})
		return true
	}
	var patchErr *storage.PatchError
	if errors.As(err, &patchErr) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":       patchErr.Error(),
			"code":        "patch_failed",
			"patch_error": patchErr,
		})
		return true
	}
	return false
}

func isStoragePolicyForbidden(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "安全策略禁止") ||
		strings.Contains(msg, "只读挂载") ||
		strings.Contains(msg, "未开启可写") ||
		strings.Contains(msg, "禁止通过符号链接") ||
		strings.Contains(msg, "超出挂载目录")
}

func writeStorageActionError(w http.ResponseWriter, err error, defaultStatus int) {
	if writeConcurrencyError(w, err) {
		return
	}
	if isStoragePolicyForbidden(err) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "忽略规则") ||
		strings.Contains(msg, "超过可写工作区单文件上限") ||
		strings.Contains(msg, "expected_version") ||
		strings.Contains(msg, "不能为空") ||
		strings.Contains(msg, "相对路径") ||
		strings.Contains(msg, "绝对路径") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	writeJSON(w, defaultStatus, map[string]string{"error": msg})
}

func (s *Server) handleGetFileLease(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	var (
		lease  storage.FileLease
		locked bool
		err    error
	)
	if isLocal {
		lease, locked = s.store.GetLease(id)
	} else {
		lease, locked, err = s.cluster.ProxyGetLease(peer, id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	if !locked {
		writeJSON(w, http.StatusOK, map[string]any{
			"locked": false,
			"lease":  nil,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"locked": true,
		"lease":  lease,
	})
}

func (s *Server) handleAcquireFileLease(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权锁定该目录文件"})
		return
	}
	var req struct {
		TTLSeconds int  `json:"ttl_seconds"`
		Force      bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	force := req.Force && p.User.Role == "admin" && !p.IsAgent
	holderID, holderName := principalActorKeyID(p)

	var (
		lease storage.FileLease
		err   error
	)
	if isLocal {
		lease, err = s.store.AcquireLease(id, holderID, holderName, time.Duration(req.TTLSeconds)*time.Second, force)
	} else {
		lease, err = s.cluster.ProxyAcquireLease(peer, id, holderID, holderName, req.TTLSeconds, force)
	}
	if err != nil {
		if writeConcurrencyError(w, err) {
			return
		}
		if isLocal {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"lease": lease,
	})
}

func (s *Server) handleReleaseFileLease(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权释放该目录文件租约"})
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	}
	force := (req.Force || parseBoolFlag(r.URL.Query().Get("force"))) && p.User.Role == "admin" && !p.IsAgent
	holderID, _ := principalActorKeyID(p)

	var err error
	if isLocal {
		err = s.store.ReleaseLease(id, holderID, force)
	} else {
		err = s.cluster.ProxyReleaseLease(peer, id, holderID, force)
	}
	if err != nil {
		if writeConcurrencyError(w, err) {
			return
		}
		if isLocal {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClusterGetFileLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lease, locked := s.store.GetLease(id)
	if !locked {
		writeJSON(w, http.StatusOK, map[string]any{"locked": false, "lease": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locked": true, "lease": lease})
}

func (s *Server) handleClusterAcquireFileLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		HolderKeyID string `json:"holder_key_id"`
		HolderName  string `json:"holder_name"`
		TTLSeconds  int    `json:"ttl_seconds"`
		Force       bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	lease, err := s.store.AcquireLease(id, req.HolderKeyID, req.HolderName, time.Duration(req.TTLSeconds)*time.Second, req.Force)
	if err != nil {
		if writeConcurrencyError(w, err) {
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lease": lease})
}

func (s *Server) handleClusterReleaseFileLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		HolderKeyID string `json:"holder_key_id"`
		Force       bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if err := s.store.ReleaseLease(id, req.HolderKeyID, req.Force); err != nil {
		if writeConcurrencyError(w, err) {
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminUpdateMount(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	nodeID := r.PathValue("nodeID")
	mountID := r.PathValue("mountID")
	var req storage.UpdateMountOptions
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if req.AllowedUsers != nil {
		cleanUsers, err := s.auth.ValidateExistingUsers(*req.AllowedUsers)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		req.AllowedUsers = &cleanUsers
	}
	entry, res, err := s.cluster.UpdateNodeMount(nodeID, mountID, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "mount_update", mountID, "ok", fmt.Sprintf("node=%s alias=%s writable=%v", nodeID, entry.Alias, entry.Writable))
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

func (s *Server) handleClusterUpdateMount(w http.ResponseWriter, r *http.Request) {
	mountID := r.PathValue("id")
	var req storage.UpdateMountOptions
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	entry, res, err := s.store.UpdateMount(mountID, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	files := s.localFilesForMount(mountID)
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"mount":  entry,
		"result": res,
		"files":  files,
	})
}
