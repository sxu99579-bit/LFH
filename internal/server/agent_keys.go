package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

type principalHandler func(http.ResponseWriter, *http.Request, auth.Principal)

func writePrincipalAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrRateLimited) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
}

func (s *Server) requireHumanAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.CurrentPrincipal(r)
		if err != nil {
			writePrincipalAuthError(w, err)
			return
		}
		if p.IsAgent {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥禁止访问该账号或管理接口"})
			return
		}
		next(w, r, p.User)
	}
}

func (s *Server) requirePrincipal(scope string, next principalHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.CurrentPrincipal(r)
		if err != nil {
			writePrincipalAuthError(w, err)
			return
		}
		if scope != "" && !p.HasScope(scope) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前 Agent 密钥缺少权限: " + scope})
			return
		}
		next(w, r, p)
	}
}

func applyAgentDeviceInfo(dev storage.DeviceInfo, p auth.Principal) storage.DeviceInfo {
	if !p.IsAgent || p.AgentKey == nil {
		return dev
	}
	dev.Name = p.ActorLabel()
	dev.FP = p.ActorFingerprint()
	dev.Detail = "agent_key:" + p.AgentKey.ID
	return dev
}

func extractDeviceInfoForPrincipal(r *http.Request, p auth.Principal) storage.DeviceInfo {
	return applyAgentDeviceInfo(extractDeviceInfo(r), p)
}

func (s *Server) sanitizeRecordForPrincipal(rec storage.FileRecord, p auth.Principal) storage.FileRecord {
	if p.IsAgent || p.User.Role != "admin" {
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

func (s *Server) sanitizeRecordsForPrincipal(recs []storage.FileRecord, p auth.Principal) []storage.FileRecord {
	if !p.IsAgent && p.User.Role == "admin" {
		return recs
	}
	out := make([]storage.FileRecord, len(recs))
	for i, rec := range recs {
		out[i] = s.sanitizeRecordForPrincipal(rec, p)
	}
	return out
}

func (s *Server) findAccessibleFileForPrincipal(id string, p auth.Principal) (storage.FileRecord, string, cluster.NodeStatus, bool, int, string) {
	if rLocal, fp, ok := s.store.Get(id); ok {
		rLocal.NodeID = s.cluster.NodeID()
		rLocal.NodeName = s.cluster.NodeName()
		rLocal.NodeOS = runtime.GOOS
		rLocal.NodeOnline = true
		rLocal.StorageDir = s.store.RecordDir(rLocal)
		if !storage.CanUserAccess(rLocal, p.User.Username, p.User.Role) {
			return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在或无权访问"
		}
		if p.IsAgent && !p.CanReadFolder(rLocal.FolderPath) {
			return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusForbidden, "Agent 密钥无权访问该目录"
		}
		return rLocal, fp, cluster.NodeStatus{}, true, http.StatusOK, ""
	}

	rPeer, peer, ok := s.cluster.LocateFile(id)
	if !ok {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在"
	}
	if !storage.CanUserAccess(rPeer, p.User.Username, p.User.Role) {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在或无权访问"
	}
	if p.IsAgent && !p.CanReadFolder(rPeer.FolderPath) {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusForbidden, "Agent 密钥无权访问该目录"
	}
	if !peer.Online {
		return rPeer, "", peer, false, http.StatusServiceUnavailable, "该文件所在节点当前离线，请开机后重试"
	}
	return rPeer, "", peer, false, http.StatusOK, ""
}

func collectEventFolderPaths(recs []storage.FileRecord) []string {
	if len(recs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(recs))
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		fp := storage.SanitizeFolderPath(rec.FolderPath)
		if !seen[fp] {
			seen[fp] = true
			out = append(out, fp)
		}
	}
	return out
}

func writeAgentKeyError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "无权"):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
	case strings.Contains(msg, "不存在"):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": msg})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
	}
}

func (s *Server) handleListAgentKeys(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	ownerFilter := strings.TrimSpace(r.URL.Query().Get("owner"))
	if u.Role != "admin" {
		ownerFilter = u.Username
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys": s.auth.ListAgentKeys(ownerFilter),
	})
}

func (s *Server) handleCreateAgentKey(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Name                string     `json:"name"`
		OwnerUsername       string     `json:"owner_username"`
		Scopes              []string   `json:"scopes"`
		ReadFolders         []string   `json:"read_folders"`
		WriteFolders        []string   `json:"write_folders"`
		DefaultAllowedUsers []string   `json:"default_allowed_users"`
		AllowSetVisibility  bool       `json:"allow_set_visibility"`
		RateLimitPerMin     int        `json:"rate_limit_per_min"`
		ExpiresAt           *time.Time `json:"expires_at,omitempty"`
		ExpiresHours        int        `json:"expires_hours,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	owner := strings.TrimSpace(req.OwnerUsername)
	if u.Role != "admin" {
		if owner != "" && owner != u.Username {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅管理员可为其他用户创建 Agent 密钥"})
			return
		}
		owner = u.Username
	} else if owner == "" {
		owner = u.Username
	}

	cleanDefaultUsers, err := s.auth.ValidateExistingUsers(req.DefaultAllowedUsers)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	exp := req.ExpiresAt
	if exp == nil && req.ExpiresHours > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresHours) * time.Hour)
		exp = &t
	}

	plainToken, pub, err := s.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:                req.Name,
		OwnerUsername:       owner,
		Scopes:              req.Scopes,
		ReadFolders:         req.ReadFolders,
		WriteFolders:        req.WriteFolders,
		DefaultAllowedUsers: cleanDefaultUsers,
		AllowSetVisibility:  req.AllowSetVisibility,
		RateLimitPerMin:     req.RateLimitPerMin,
		ExpiresAt:           exp,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "agent_key_create", pub.Name, "ok", fmt.Sprintf("id=%s owner=%s", pub.ID, pub.OwnerUsername))
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":    true,
		"key":   pub,
		"token": plainToken,
	})
}

func (s *Server) handleUpdateAgentKey(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	var req auth.UpdateAgentKeyInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if req.DefaultAllowedUsers != nil {
		cleanUsers, err := s.auth.ValidateExistingUsers(*req.DefaultAllowedUsers)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		req.DefaultAllowedUsers = &cleanUsers
	}
	updated, err := s.auth.UpdateAgentKey(id, u.Username, u.Role, req)
	if err != nil {
		writeAgentKeyError(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_key_update", updated.Name, "ok", "id="+updated.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"key": updated,
	})
}

func (s *Server) handleToggleAgentKey(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	var nextDisabled bool
	if req.Disabled != nil {
		nextDisabled = *req.Disabled
	} else {
		cur, ok := s.auth.GetAgentKey(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Agent 密钥不存在"})
			return
		}
		nextDisabled = !cur.Disabled
	}

	updated, err := s.auth.SetAgentKeyDisabled(id, u.Username, u.Role, nextDisabled)
	if err != nil {
		writeAgentKeyError(w, err)
		return
	}
	if updated.Disabled {
		s.getMCPServer().DisconnectByAgentKey(id)
	}
	s.logAudit(r, u.Username, "agent_key_toggle", updated.Name, "ok", fmt.Sprintf("id=%s disabled=%v", updated.ID, updated.Disabled))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"key": updated,
	})
}

func (s *Server) handleDeleteAgentKey(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := r.PathValue("id")
	if err := s.auth.DeleteAgentKey(id, u.Username, u.Role); err != nil {
		writeAgentKeyError(w, err)
		return
	}
	s.getMCPServer().DisconnectByAgentKey(id)
	s.logAudit(r, u.Username, "agent_key_delete", id, "ok", "id="+id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
