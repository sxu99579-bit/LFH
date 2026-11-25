package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/security"
	"lfh/internal/storage"
)

type EnrichedUser struct {
	auth.PublicUser
	FileCount           int   `json:"file_count"`
	UsedBytes           int64 `json:"used_bytes"`
	AgentKeyCount       int   `json:"agent_key_count"`
	ActiveAgentKeyCount int   `json:"active_agent_key_count"`
}

type userUsageStat struct {
	count int
	bytes int64
}

func formatQuotaHuman(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(b)
	idx := 0
	for v >= 1024 && idx < len(units)-1 {
		v /= 1024
		idx++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[idx])
	}
	return fmt.Sprintf("%.1f %s", v, units[idx])
}

func (s *Server) buildUserUsageMap() map[string]userUsageStat {
	stats := make(map[string]userUsageStat)
	for _, rec := range s.cluster.AllClusterFiles() {
		if rec.StorageMode == storage.StorageModeMount || rec.MountID != "" {
			continue
		}
		uploader := strings.TrimSpace(rec.Uploader)
		if uploader == "" {
			continue
		}
		cur := stats[uploader]
		cur.count++
		if rec.StorageMode == "" || rec.StorageMode == storage.StorageModeCopy {
			cur.bytes += rec.Size
			for _, ver := range rec.Versions {
				cur.bytes += ver.Size
			}
		} else {
			cur.bytes += rec.Size
		}
		stats[uploader] = cur
	}
	return stats
}

func (s *Server) enrichUsers(list []auth.PublicUser) ([]EnrichedUser, map[string]int) {
	usage := s.buildUserUsageMap()
	agentCounts := s.auth.CountAgentKeysByOwner()
	summary := map[string]int{
		"total":    len(list),
		"active":   0,
		"pending":  0,
		"banned":   0,
		"expired":  0,
		"admins":   0,
		"readonly": 0,
	}
	out := make([]EnrichedUser, 0, len(list))
	for _, u := range list {
		st := usage[u.Username]
		ac := agentCounts[u.Username]
		out = append(out, EnrichedUser{
			PublicUser:          u,
			FileCount:           st.count,
			UsedBytes:           st.bytes,
			AgentKeyCount:       ac[0],
			ActiveAgentKeyCount: ac[1],
		})
		switch {
		case u.Status == auth.StatusPending:
			summary["pending"]++
		case u.Status == auth.StatusBanned:
			summary["banned"]++
		case u.Expired:
			summary["expired"]++
		default:
			summary["active"]++
		}
		if u.Role == auth.RoleAdmin {
			summary["admins"]++
		} else if u.Role == auth.RoleReadonly {
			summary["readonly"]++
		}
	}
	return out, summary
}

func (s *Server) checkPrincipalQuota(p auth.Principal, addBytes int64) (int, string) {
	if p.User.QuotaBytes > 0 && addBytes > 0 {
		usage := s.buildUserUsageMap()
		cur := usage[p.User.Username].bytes
		if cur+addBytes > p.User.QuotaBytes {
			return http.StatusInsufficientStorage, fmt.Sprintf(
				"个人空间配额不足（已用 %s，本次 %s，个人上限 %s）",
				formatQuotaHuman(cur),
				formatQuotaHuman(addBytes),
				formatQuotaHuman(p.User.QuotaBytes),
			)
		}
	}
	return http.StatusOK, ""
}

func (s *Server) checkPrincipalCanUpload(p auth.Principal, folderPath string, addBytes int64) (int, string) {
	if !p.User.EffectiveCanUpload() {
		return http.StatusForbidden, "当前账号为只读权限或已被管理员禁止上传写入"
	}
	if !p.User.CanAccessFolder(folderPath) {
		return http.StatusForbidden, "当前账号无权向该目录写入文件"
	}
	return s.checkPrincipalQuota(p, addBytes)
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Username           string     `json:"username"`
		Password           string     `json:"password"`
		Role               string     `json:"role"`
		DisplayName        string     `json:"display_name"`
		Note               string     `json:"note"`
		QuotaBytes         int64      `json:"quota_bytes"`
		AllowedFolders     []string   `json:"allowed_folders"`
		CanUpload          *bool      `json:"can_upload,omitempty"`
		CanManageFolders   *bool      `json:"can_manage_folders,omitempty"`
		CanUseAgents       *bool      `json:"can_use_agents,omitempty"`
		MustChangePassword bool       `json:"must_change_password"`
		ExpireHours        int        `json:"expire_hours"`
		ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	created, plainPwd, err := s.auth.AdminCreateUser(auth.AdminCreateUserInput{
		Username:           req.Username,
		Password:           req.Password,
		Role:               req.Role,
		DisplayName:        req.DisplayName,
		Note:               req.Note,
		QuotaBytes:         req.QuotaBytes,
		AllowedFolders:     req.AllowedFolders,
		CanUpload:          req.CanUpload,
		CanManageFolders:   req.CanManageFolders,
		CanUseAgents:       req.CanUseAgents,
		MustChangePassword: req.MustChangePassword,
		ExpireHours:        req.ExpireHours,
		ExpiresAt:          req.ExpiresAt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "user_create", created.Username, "ok", fmt.Sprintf("role=%s quota=%d", created.Role, created.QuotaBytes))
	enriched, summary := s.enrichUsers(s.auth.ListUsers())
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":                 true,
		"user":               created,
		"generated_password": plainPwd,
		"users":              enriched,
		"summary":            summary,
		"pending_count":      s.auth.PendingCount(),
	})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	var req auth.AdminUpdateUserInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	updated, err := s.auth.AdminUpdateUser(u.Username, target, req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if !updated.EffectiveCanUseAgents() {
		if mcpSrv := s.getMCPServer(); mcpSrv != nil {
			for _, ak := range s.auth.ListAgentKeys(target) {
				mcpSrv.DisconnectByAgentKey(ak.ID)
			}
		}
	}
	s.logAudit(r, u.Username, "user_update", target, "ok", fmt.Sprintf("role=%s quota=%d", updated.Role, updated.QuotaBytes))
	s.events.Publish(FileChangeEvent{
		Kind:     "role_change",
		Uploader: target,
	})
	enrichedList, summary := s.enrichUsers([]auth.PublicUser{updated})
	_ = summary
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"user":          enrichedList[0],
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleAdminResetUserPassword(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	var req struct {
		NewPassword        string `json:"new_password"`
		MustChangePassword *bool  `json:"must_change_password,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	mustChange := true
	if req.MustChangePassword != nil {
		mustChange = *req.MustChangePassword
	}
	updated, plainPwd, err := s.auth.AdminResetPassword(u.Username, target, req.NewPassword, mustChange)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "user_reset_password", target, "ok", fmt.Sprintf("must_change=%v", mustChange))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"user":               updated,
		"temporary_password": plainPwd,
	})
}

func (s *Server) handleAdminForceLogoutUser(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	updated, err := s.auth.ForceLogoutUser(u.Username, target)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "user_force_logout", target, "ok", "session_version_incremented")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": updated,
	})
}

func (s *Server) handleAdminFreezeUserAgents(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := r.PathValue("username")
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	affected, err := s.auth.SetOwnerAgentKeysDisabled(target, req.Disabled)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if req.Disabled {
		if mcpSrv := s.getMCPServer(); mcpSrv != nil {
			for _, keyID := range affected {
				mcpSrv.DisconnectByAgentKey(keyID)
			}
			for _, ak := range s.auth.ListAgentKeys(target) {
				mcpSrv.DisconnectByAgentKey(ak.ID)
			}
		}
	}
	action := "user_agents_unfreeze"
	if req.Disabled {
		action = "user_agents_freeze"
	}
	s.logAudit(r, u.Username, action, target, "ok", "affected="+strconv.Itoa(len(affected)))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"disabled": req.Disabled,
		"affected": len(affected),
		"keys":     s.auth.ListAgentKeys(target),
	})
}

func (s *Server) handleAdminBatchUsers(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		Usernames  []string `json:"usernames"`
		Action     string   `json:"action"`
		QuotaBytes *int64   `json:"quota_bytes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	res, err := s.auth.BatchUsersAction(u.Username, req.Usernames, req.Action, req.QuotaBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if res.Action == "ban" {
		if mcpSrv := s.getMCPServer(); mcpSrv != nil {
			for _, target := range res.Updated {
				for _, ak := range s.auth.ListAgentKeys(target) {
					mcpSrv.DisconnectByAgentKey(ak.ID)
				}
			}
		}
	}
	s.logAudit(r, u.Username, "user_batch_"+res.Action, strings.Join(res.Updated, ","), "ok", fmt.Sprintf("updated=%d skipped=%d", len(res.Updated), len(res.Skipped)))
	enriched, summary := s.enrichUsers(s.auth.ListUsers())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"result":        res,
		"users":         enriched,
		"summary":       summary,
		"pending_count": s.auth.PendingCount(),
	})
}

func (s *Server) handleAdminUserDetail(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	target := strings.TrimSpace(r.PathValue("username"))
	pub, ok := s.auth.GetUser(target)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "用户不存在"})
		return
	}
	enriched, _ := s.enrichUsers([]auth.PublicUser{pub})
	keys := s.auth.ListAgentKeys(target)
	if keys == nil {
		keys = []auth.PublicAgentKey{}
	}
	auditEntries := s.audit.RecentByActor(target, 40)
	if auditEntries == nil {
		auditEntries = []security.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"user":          enriched[0],
		"agent_keys":    keys,
		"audit_entries": auditEntries,
	})
}

func (s *Server) handleClusterReassignUserFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldUser string `json:"old_user"`
		NewUser string `json:"new_user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	n := s.store.ReassignUploader(req.OldUser, req.NewUser)
	if n > 0 {
		s.cluster.TriggerHeartbeat()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"reassigned": n,
	})
}

func (s *Server) deleteAllUserOwnedFiles(username string) int {
	target := strings.TrimSpace(username)
	if target == "" {
		return 0
	}
	deleted := 0
	for _, rec := range s.cluster.AllClusterFiles() {
		if rec.Uploader != target || rec.StorageMode == storage.StorageModeMount || rec.MountID != "" {
			continue
		}
		if _, _, ok := s.store.Get(rec.ID); ok {
			if err := s.store.DeleteWithConcurrency(rec.ID, storage.WriteConcurrencyOptions{}); err == nil {
				deleted++
			}
			continue
		}
		if _, peer, ok := s.cluster.LocateFile(rec.ID); ok && peer.Online {
			if err := s.cluster.ProxyDeleteWithConcurrency(peer, rec.ID, storage.WriteConcurrencyOptions{}); err == nil {
				deleted++
			}
		}
	}
	if deleted > 0 {
		s.cluster.TriggerHeartbeat()
	}
	return deleted
}

func (s *Server) disconnectAllAgentSessionsForUser(username string) {
	mcpSrv := s.getMCPServer()
	if mcpSrv == nil {
		return
	}
	for _, ak := range s.auth.ListAgentKeys(username) {
		mcpSrv.DisconnectByAgentKey(ak.ID)
	}
}

func _keepClusterImportUsed(_ cluster.NodeStatus) {}
