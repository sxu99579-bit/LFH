package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"lfh/internal/auth"
	"lfh/internal/mcp"
	"lfh/internal/storage"
)

const (
	agentPairingTTL            = 5 * time.Minute
	agentPairingClaimTTL       = 60 * time.Second
	agentPairingRecentTTL      = 10 * time.Minute
	maxPendingAgentPairings    = 20
	agentPairingRateLimitPerIP = 15
	confirmCodeAlphabet        = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type AgentPairingRequestInput struct {
	Name                  string   `json:"name"`
	ClientType            string   `json:"client_type"`
	ClientVersion         string   `json:"client_version,omitempty"`
	Hostname              string   `json:"hostname,omitempty"`
	OS                    string   `json:"os,omitempty"`
	DeviceKind            string   `json:"device_kind,omitempty"`
	TargetOwner           string   `json:"target_owner,omitempty"`
	RequestedScopes       []string `json:"requested_scopes,omitempty"`
	RequestedReadFolders  []string `json:"requested_read_folders,omitempty"`
	RequestedWriteFolders []string `json:"requested_write_folders,omitempty"`
	Note                  string   `json:"note,omitempty"`
}

type ApprovePairingInput struct {
	Name                string     `json:"name,omitempty"`
	OwnerUsername       string     `json:"owner_username,omitempty"`
	Scopes              *[]string  `json:"scopes,omitempty"`
	ReadFolders         *[]string  `json:"read_folders,omitempty"`
	WriteFolders        *[]string  `json:"write_folders,omitempty"`
	DefaultAllowedUsers *[]string  `json:"default_allowed_users,omitempty"`
	AllowSetVisibility  *bool      `json:"allow_set_visibility,omitempty"`
	RateLimitPerMin     *int       `json:"rate_limit_per_min,omitempty"`
	ExpiresHours        int        `json:"expires_hours,omitempty"`
	AvatarID            string     `json:"avatar_id,omitempty"`
	DisplayAlias        string     `json:"display_alias,omitempty"`
	DeviceAlias         string     `json:"device_alias,omitempty"`
	DeviceKind          string     `json:"device_kind,omitempty"`
}

type PublicAgentPairing struct {
	ID                    string     `json:"id"`
	ConfirmCode           string     `json:"confirm_code"`
	Name                  string     `json:"name"`
	ClientType            string     `json:"client_type"`
	ClientVersion         string     `json:"client_version,omitempty"`
	Hostname              string     `json:"hostname,omitempty"`
	OS                    string     `json:"os,omitempty"`
	DeviceKind            string     `json:"device_kind,omitempty"`
	SourceIP              string     `json:"source_ip,omitempty"`
	TargetOwner           string     `json:"target_owner,omitempty"`
	RequestedScopes       []string   `json:"requested_scopes"`
	RequestedReadFolders  []string   `json:"requested_read_folders,omitempty"`
	RequestedWriteFolders []string   `json:"requested_write_folders,omitempty"`
	ApprovedScopes        []string   `json:"approved_scopes,omitempty"`
	ApprovedReadFolders   []string   `json:"approved_read_folders,omitempty"`
	ApprovedWriteFolders  []string   `json:"approved_write_folders,omitempty"`
	Note                  string     `json:"note,omitempty"`
	Status                string     `json:"status"` // "pending" | "approved" | "claimed" | "rejected" | "expired"
	CreatedAt             time.Time  `json:"created_at"`
	ExpiresAt             time.Time  `json:"expires_at"`
	ResolvedAt            *time.Time `json:"resolved_at,omitempty"`
	ApprovedBy            string     `json:"approved_by,omitempty"`
	ApprovedKeyID         string     `json:"approved_key_id,omitempty"`
}

type AgentLeaseView struct {
	FileID     string    `json:"file_id"`
	FileName   string    `json:"file_name,omitempty"`
	Path       string    `json:"path,omitempty"`
	FolderPath string    `json:"folder_path,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type AgentPresenceItem struct {
	Key              auth.PublicAgentKey `json:"key"`
	Online           bool                `json:"online"`
	Status           string              `json:"status"` // "working" | "watching" | "online" | "offline"
	ClientName       string              `json:"client_name,omitempty"`
	ClientVersion    string              `json:"client_version,omitempty"`
	ClientIP         string              `json:"client_ip,omitempty"`
	ProtocolVersion  string              `json:"protocol_version,omitempty"`
	SSEConnected     bool                `json:"sse_connected"`
	ActiveTool       string              `json:"active_tool,omitempty"`
	ActiveTarget     string              `json:"active_target,omitempty"`
	ActiveSince      *time.Time          `json:"active_since,omitempty"`
	ReportedState    string              `json:"reported_state,omitempty"`
	ReportedTask     string              `json:"reported_task,omitempty"`
	ReportedProgress string              `json:"reported_progress,omitempty"`
	ReportedAt       *time.Time          `json:"reported_at,omitempty"`
	LastTool         string              `json:"last_tool,omitempty"`
	LastTarget       string              `json:"last_target,omitempty"`
	LastToolOK       bool                `json:"last_tool_ok"`
	ToolCalls        int64               `json:"tool_calls"`
	SessionIDs       []string            `json:"session_ids,omitempty"`
	ActiveLeases     []AgentLeaseView    `json:"active_leases,omitempty"`
	LastSeen         *time.Time          `json:"last_seen,omitempty"`
}

type AgentPresenceSummary struct {
	TotalAgents     int `json:"total_agents"`
	OnlineAgents    int `json:"online_agents"`
	WorkingAgents   int `json:"working_agents"`
	WatchingAgents  int `json:"watching_agents"`
	ActiveSessions  int `json:"active_sessions"`
	PendingPairings int `json:"pending_pairings"`
}

type agentPairingRecord struct {
	PublicAgentPairing
	PollSecret     string
	PlainToken     string
	TokenExpiresAt time.Time
	ApprovedKey    *auth.PublicAgentKey
}

type agentPairingManager struct {
	mu      sync.Mutex
	records map[string]*agentPairingRecord
	ipHits  map[string][]time.Time
}

func newAgentPairingManager() *agentPairingManager {
	return &agentPairingManager{
		records: make(map[string]*agentPairingRecord),
		ipHits:  make(map[string][]time.Time),
	}
}

func (s *Server) getAgentPairing() *agentPairingManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentPairing == nil {
		s.agentPairing = newAgentPairingManager()
	}
	return s.agentPairing
}

func sanitizePairingText(raw string, maxRunes int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	runes := []rune(cleaned)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return cleaned
}

func randomConfirmCode() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	chars := make([]byte, 6)
	for i := 0; i < 6; i++ {
		chars[i] = confirmCodeAlphabet[int(buf[i])%len(confirmCodeAlphabet)]
	}
	return string(chars[:3]) + "-" + string(chars[3:])
}

func sanitizeProposedScopes(raw []string) []string {
	valid := []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare, auth.ScopeVerify, auth.ScopeDelete}
	seen := make(map[string]bool)
	var out []string
	for _, s := range raw {
		clean := strings.ToLower(strings.TrimSpace(s))
		for _, v := range valid {
			if clean == v && !seen[v] {
				seen[v] = true
				out = append(out, v)
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{auth.ScopeRead, auth.ScopeWrite}
	}
	return out
}

func stripDeleteScope(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s != auth.ScopeDelete {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{auth.ScopeRead}
	}
	return out
}

func sanitizeProposedFolders(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	if validated, err := auth.ValidateFolderRules(raw); err == nil {
		return validated
	}
	var out []string
	for _, item := range raw {
		if v, err := auth.ValidateFolderRules([]string{item}); err == nil {
			out = append(out, v...)
		}
	}
	return out
}

func (m *agentPairingManager) checkIPRateLimitLocked(ip string, now time.Time) bool {
	if ip == "" {
		ip = "unknown"
	}
	cutoff := now.Add(-1 * time.Minute)
	hits := m.ipHits[ip]
	kept := hits[:0]
	for _, ts := range hits {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= agentPairingRateLimitPerIP {
		m.ipHits[ip] = kept
		return false
	}
	m.ipHits[ip] = append(kept, now)
	return true
}

func (m *agentPairingManager) pruneLocked(now time.Time) {
	for id, rec := range m.records {
		switch rec.Status {
		case "pending":
			if now.After(rec.ExpiresAt) {
				rec.Status = "expired"
				res := rec.ExpiresAt
				rec.ResolvedAt = &res
			}
		case "approved":
			if now.After(rec.TokenExpiresAt) {
				rec.Status = "expired"
				rec.PlainToken = ""
			}
		}
		ref := rec.CreatedAt
		if rec.ResolvedAt != nil && rec.ResolvedAt.After(ref) {
			ref = *rec.ResolvedAt
		}
		if rec.Status != "pending" && now.Sub(ref) > agentPairingRecentTTL {
			rec.PlainToken = ""
			delete(m.records, id)
		}
	}
}

func (m *agentPairingManager) pendingCountLocked() int {
	n := 0
	for _, rec := range m.records {
		if rec.Status == "pending" {
			n++
		}
	}
	return n
}

func clonePublicPairing(rec *agentPairingRecord) PublicAgentPairing {
	cp := rec.PublicAgentPairing
	cp.RequestedScopes = append([]string(nil), rec.RequestedScopes...)
	cp.RequestedReadFolders = append([]string(nil), rec.RequestedReadFolders...)
	cp.RequestedWriteFolders = append([]string(nil), rec.RequestedWriteFolders...)
	cp.ApprovedScopes = append([]string(nil), rec.ApprovedScopes...)
	cp.ApprovedReadFolders = append([]string(nil), rec.ApprovedReadFolders...)
	cp.ApprovedWriteFolders = append([]string(nil), rec.ApprovedWriteFolders...)
	return cp
}

func canUserManagePairing(rec *agentPairingRecord, u auth.PublicUser) bool {
	if u.Role == "admin" {
		return true
	}
	return rec.TargetOwner == "" || strings.EqualFold(rec.TargetOwner, u.Username)
}

func (s *Server) pendingPairingCountForUser(u auth.PublicUser) int {
	pm := s.getAgentPairing()
	now := time.Now()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.pruneLocked(now)
	n := 0
	for _, rec := range pm.records {
		if rec.Status == "pending" && canUserManagePairing(rec, u) {
			n++
		}
	}
	return n
}

func (s *Server) handleCreateAgentPairing(w http.ResponseWriter, r *http.Request) {
	var in AgentPairingRequestInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	sourceIP := requestRemoteIP(r)
	clientType := sanitizePairingText(in.ClientType, 32)
	if clientType == "" {
		clientType = "generic"
	}
	hostname := sanitizePairingText(in.Hostname, 64)
	name := sanitizePairingText(in.Name, 64)
	if name == "" {
		if hostname != "" {
			name = sanitizePairingText(hostname+"-"+clientType, 64)
		} else {
			name = "MCP Agent"
		}
	}

	now := time.Now()
	pm := s.getAgentPairing()
	pm.mu.Lock()
	pm.pruneLocked(now)
	if !pm.checkIPRateLimitLocked(sourceIP, now) {
		pm.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "申请过于频繁，请 1 分钟后再试"})
		return
	}
	if pm.pendingCountLocked() >= maxPendingAgentPairings {
		pm.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "当前待审批接入队列已满（上限 20 条），请稍后再试"})
		return
	}

	id := "pair_" + randomHexToken(12)
	pollSecret := "ps_" + randomHexToken(24)
	confirmCode := randomConfirmCode()
	targetOwner := sanitizePairingText(in.TargetOwner, 64)

	deviceKind := auth.NormalizeDeviceKind(in.DeviceKind)
	if deviceKind == "" {
		deviceKind = auth.InferDeviceKind(in.OS, hostname)
	}

	rec := &agentPairingRecord{
		PublicAgentPairing: PublicAgentPairing{
			ID:                    id,
			ConfirmCode:           confirmCode,
			Name:                  name,
			ClientType:            clientType,
			ClientVersion:         sanitizePairingText(in.ClientVersion, 32),
			Hostname:              hostname,
			OS:                    sanitizePairingText(in.OS, 32),
			DeviceKind:            deviceKind,
			SourceIP:              sourceIP,
			TargetOwner:           targetOwner,
			RequestedScopes:       sanitizeProposedScopes(in.RequestedScopes),
			RequestedReadFolders:  sanitizeProposedFolders(in.RequestedReadFolders),
			RequestedWriteFolders: sanitizeProposedFolders(in.RequestedWriteFolders),
			Note:                  sanitizePairingText(in.Note, 200),
			Status:                "pending",
			CreatedAt:             now,
			ExpiresAt:             now.Add(agentPairingTTL),
		},
		PollSecret: pollSecret,
	}
	pm.records[id] = rec
	pub := clonePublicPairing(rec)
	pm.mu.Unlock()

	if s.events != nil {
		var allowed []string
		if targetOwner != "" {
			allowed = []string{targetOwner}
		}
		s.PublishEvent(FileChangeEvent{
			Kind:         "agent_pairing",
			Uploader:     targetOwner,
			ActorName:    pub.Name,
			NodeName:     pub.ConfirmCode,
			AllowedUsers: allowed,
		})
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":               true,
		"id":               pub.ID,
		"confirm_code":     pub.ConfirmCode,
		"poll_secret":      pollSecret,
		"status":           pub.Status,
		"expires_at":       pub.ExpiresAt,
		"poll_interval_ms": 2000,
		"request":          pub,
	})
}

func (s *Server) handlePollAgentPairing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := strings.TrimSpace(r.PathValue("id"))
	secret := strings.TrimSpace(r.Header.Get("X-Pairing-Secret"))
	if secret == "" {
		secret = strings.TrimSpace(r.URL.Query().Get("secret"))
	}

	now := time.Now()
	pm := s.getAgentPairing()
	pm.mu.Lock()
	pm.pruneLocked(now)
	rec, ok := pm.records[id]
	if !ok {
		pm.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接入申请不存在或已过期"})
		return
	}
	if secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(rec.PollSecret)) != 1 {
		pm.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "轮询凭证无效"})
		return
	}

	if rec.Status == "approved" {
		if rec.PlainToken == "" || now.After(rec.TokenExpiresAt) {
			rec.PlainToken = ""
			rec.Status = "expired"
			pub := clonePublicPairing(rec)
			pm.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"status":  "expired",
				"request": pub,
			})
			return
		}
		tok := rec.PlainToken
		rec.PlainToken = ""
		rec.Status = "claimed"
		var keyCopy *auth.PublicAgentKey
		if rec.ApprovedKey != nil {
			k := *rec.ApprovedKey
			keyCopy = &k
		}
		pub := clonePublicPairing(rec)
		pm.mu.Unlock()

		selfURL := ""
		if s.cluster != nil {
			selfURL = s.cluster.SelfStatus().HTTPURL
		}
		baseURL := requestBaseURL(r, selfURL, s.port)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"status":    "approved",
			"token":     tok,
			"key":       keyCopy,
			"entry_url": baseURL,
			"mcp_url":   baseURL + "/mcp",
			"request":   pub,
		})
		return
	}

	pub := clonePublicPairing(rec)
	pm.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"status":  pub.Status,
		"request": pub,
	})
}

func (s *Server) handleListAgentPairings(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	pm := s.getAgentPairing()
	now := time.Now()
	pm.mu.Lock()
	pm.pruneLocked(now)
	list := make([]PublicAgentPairing, 0, len(pm.records))
	for _, rec := range pm.records {
		if !canUserManagePairing(rec, u) {
			continue
		}
		list = append(list, clonePublicPairing(rec))
	}
	pm.mu.Unlock()

	sort.Slice(list, func(i, j int) bool {
		if list[i].Status == "pending" && list[j].Status != "pending" {
			return true
		}
		if list[i].Status != "pending" && list[j].Status == "pending" {
			return false
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	pendingCount := 0
	for _, item := range list {
		if item.Status == "pending" {
			pendingCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"requests":      list,
		"pending_count": pendingCount,
	})
}

func (s *Server) handleApproveAgentPairing(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	var in ApprovePairingInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	now := time.Now()
	pm := s.getAgentPairing()
	pm.mu.Lock()
	pm.pruneLocked(now)
	rec, ok := pm.records[id]
	if !ok {
		pm.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接入申请不存在"})
		return
	}
	if !canUserManagePairing(rec, u) {
		pm.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "无权审批归属其他用户的接入申请"})
		return
	}
	if rec.Status != "pending" {
		pm.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该申请已处理或已过期 (" + rec.Status + ")"})
		return
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = rec.Name
	}

	owner := strings.TrimSpace(in.OwnerUsername)
	if u.Role != "admin" {
		if owner != "" && owner != u.Username {
			pm.mu.Unlock()
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅管理员可指定其他归属用户"})
			return
		}
		owner = u.Username
	} else if owner == "" {
		if rec.TargetOwner != "" {
			if _, err := s.auth.ValidateExistingUsers([]string{rec.TargetOwner}); err == nil {
				owner = rec.TargetOwner
			}
		}
		if owner == "" {
			owner = u.Username
		}
	}

	// Safety baseline: delete is never auto-approved from requester proposals unless caller explicitly includes it.
	var scopes []string
	if in.Scopes != nil {
		scopes = append([]string(nil), (*in.Scopes)...)
	} else {
		scopes = stripDeleteScope(rec.RequestedScopes)
	}

	readFolders := rec.RequestedReadFolders
	if in.ReadFolders != nil {
		readFolders = append([]string(nil), (*in.ReadFolders)...)
	}
	writeFolders := rec.RequestedWriteFolders
	if in.WriteFolders != nil {
		writeFolders = append([]string(nil), (*in.WriteFolders)...)
	}
	if len(readFolders) == 0 && len(writeFolders) == 0 {
		readFolders = []string{"*"}
	}

	var defaultAllowed []string
	if in.DefaultAllowedUsers != nil {
		var err error
		defaultAllowed, err = s.auth.ValidateExistingUsers(*in.DefaultAllowedUsers)
		if err != nil {
			pm.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	allowVis := false
	if in.AllowSetVisibility != nil {
		allowVis = *in.AllowSetVisibility
	}
	rateLimit := 180
	if in.RateLimitPerMin != nil && *in.RateLimitPerMin > 0 {
		rateLimit = *in.RateLimitPerMin
	}
	var expiresAt *time.Time
	if in.ExpiresHours > 0 {
		t := now.Add(time.Duration(in.ExpiresHours) * time.Hour)
		expiresAt = &t
	}
	deviceAlias := strings.TrimSpace(in.DeviceAlias)
	if deviceAlias == "" {
		deviceAlias = rec.Hostname
	}
	deviceKind := auth.NormalizeDeviceKind(in.DeviceKind)
	if deviceKind == "" {
		deviceKind = rec.DeviceKind
	}
	avatarID := strings.TrimSpace(in.AvatarID)
	displayAlias := strings.TrimSpace(in.DisplayAlias)
	pm.mu.Unlock()

	plainToken, pubKey, err := s.auth.CreateAgentKey(auth.CreateAgentKeyInput{
		Name:                name,
		OwnerUsername:       owner,
		Scopes:              scopes,
		ReadFolders:         readFolders,
		WriteFolders:        writeFolders,
		DefaultAllowedUsers: defaultAllowed,
		AllowSetVisibility:  allowVis,
		RateLimitPerMin:     rateLimit,
		AvatarID:            avatarID,
		DisplayAlias:        displayAlias,
		DeviceAlias:         deviceAlias,
		DeviceKind:          deviceKind,
		ExpiresAt:           expiresAt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	pm.mu.Lock()
	rec, ok = pm.records[id]
	if !ok || rec.Status != "pending" {
		pm.mu.Unlock()
		_ = s.auth.DeleteAgentKey(pubKey.ID, owner, "admin")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该申请已被并发处理或过期"})
		return
	}
	resolvedAt := time.Now()
	rec.Status = "approved"
	rec.ResolvedAt = &resolvedAt
	rec.ApprovedBy = u.Username
	rec.ApprovedKeyID = pubKey.ID
	rec.ApprovedScopes = append([]string(nil), pubKey.Scopes...)
	rec.ApprovedReadFolders = append([]string(nil), pubKey.ReadFolders...)
	rec.ApprovedWriteFolders = append([]string(nil), pubKey.WriteFolders...)
	rec.PlainToken = plainToken
	rec.TokenExpiresAt = resolvedAt.Add(agentPairingClaimTTL)
	keyCopy := pubKey
	rec.ApprovedKey = &keyCopy
	pub := clonePublicPairing(rec)
	pm.mu.Unlock()

	s.logAudit(r, u.Username, "agent_pairing_approve", pubKey.Name, "ok", fmt.Sprintf("pair=%s key=%s owner=%s", pub.ID, pubKey.ID, owner))
	if s.events != nil {
		s.PublishEvent(FileChangeEvent{
			Kind:         "agent_pairing",
			Uploader:     owner,
			ActorName:    pubKey.Name,
			NodeName:     "approved",
			AllowedUsers: []string{owner},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"request": pub,
		"key":     pubKey,
	})
}

func (s *Server) handleRejectAgentPairing(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	now := time.Now()
	pm := s.getAgentPairing()
	pm.mu.Lock()
	pm.pruneLocked(now)
	rec, ok := pm.records[id]
	if !ok {
		pm.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接入申请不存在"})
		return
	}
	if !canUserManagePairing(rec, u) {
		pm.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "无权拒绝归属其他用户的接入申请"})
		return
	}
	if rec.Status != "pending" {
		pm.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该申请已处理 (" + rec.Status + ")"})
		return
	}
	rec.Status = "rejected"
	rec.PlainToken = ""
	rec.ApprovedBy = u.Username
	rec.ResolvedAt = &now
	pub := clonePublicPairing(rec)
	pm.mu.Unlock()

	s.logAudit(r, u.Username, "agent_pairing_reject", pub.Name, "ok", "pair="+pub.ID)
	if s.events != nil {
		var allowed []string
		if pub.TargetOwner != "" {
			allowed = []string{pub.TargetOwner}
		}
		s.PublishEvent(FileChangeEvent{
			Kind:         "agent_pairing",
			Uploader:     u.Username,
			ActorName:    pub.Name,
			NodeName:     "rejected",
			AllowedUsers: allowed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"request": pub,
	})
}

func statusRank(status string) int {
	switch status {
	case "working":
		return 4
	case "watching":
		return 3
	case "online":
		return 2
	default:
		return 1
	}
}

func (s *Server) handleListAgentSessions(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	ownerFilter := ""
	if u.Role != "admin" {
		ownerFilter = u.Username
	}
	keys := s.auth.ListAgentKeys(ownerFilter)
	allSessions := s.getMCPServer().ListSessions()

	visibleSessions := make([]mcp.SessionSnapshot, 0, len(allSessions))
	byKey := make(map[string][]mcp.SessionSnapshot)
	for _, sess := range allSessions {
		if u.Role != "admin" && !strings.EqualFold(sess.Username, u.Username) {
			continue
		}
		visibleSessions = append(visibleSessions, sess)
		if sess.AgentKeyID != "" {
			byKey[sess.AgentKeyID] = append(byKey[sess.AgentKeyID], sess)
		}
	}

	leasesByKey := make(map[string][]AgentLeaseView)
	if s.store != nil {
		for _, lease := range s.store.ListActiveLeases() {
			view := AgentLeaseView{
				FileID:    lease.FileID,
				ExpiresAt: lease.ExpiresAt,
			}
			if rec, _, ok := s.store.Get(lease.FileID); ok {
				view.FileName = rec.Name
				view.FolderPath = rec.FolderPath
				view.Path = storage.LogicalPathOf(rec.FolderPath, rec.Name)
			}
			leasesByKey[lease.HolderKeyID] = append(leasesByKey[lease.HolderKeyID], view)
		}
	}

	agents := make([]AgentPresenceItem, 0, len(keys))
	summary := AgentPresenceSummary{
		TotalAgents:     len(keys),
		ActiveSessions:  len(visibleSessions),
		PendingPairings: s.pendingPairingCountForUser(u),
	}

	for _, k := range keys {
		item := AgentPresenceItem{
			Key:          k,
			Status:       "offline",
			ActiveLeases: leasesByKey[k.ID],
			LastSeen:     k.LastUsedAt,
		}
		sessList := byKey[k.ID]
		for _, sess := range sessList {
			item.SessionIDs = append(item.SessionIDs, sess.SessionID)
			item.ToolCalls += sess.ToolCalls
			if sess.SSEConnected {
				item.SSEConnected = true
			}
			if statusRank(sess.Status) > statusRank(item.Status) || (item.LastSeen == nil || sess.LastSeen.After(*item.LastSeen)) {
				if statusRank(sess.Status) >= statusRank(item.Status) {
					item.Status = sess.Status
				}
			}
			if item.LastSeen == nil || sess.LastSeen.After(*item.LastSeen) {
				ts := sess.LastSeen
				item.LastSeen = &ts
				if sess.ClientName != "" {
					item.ClientName = sess.ClientName
					item.ClientVersion = sess.ClientVersion
				}
				if sess.ClientIP != "" {
					item.ClientIP = sess.ClientIP
				}
				if sess.ProtocolVersion != "" {
					item.ProtocolVersion = sess.ProtocolVersion
				}
				if sess.LastTool != "" {
					item.LastTool = sess.LastTool
					item.LastTarget = sess.LastTarget
					item.LastToolOK = sess.LastToolOK
				}
			}
			if sess.ActiveTool != "" {
				item.ActiveTool = sess.ActiveTool
				item.ActiveTarget = sess.ActiveTarget
				item.ActiveSince = sess.ActiveSince
			}
			if sess.ReportedAt != nil && (item.ReportedAt == nil || sess.ReportedAt.After(*item.ReportedAt)) {
				item.ReportedState = sess.ReportedState
				item.ReportedTask = sess.ReportedTask
				item.ReportedProgress = sess.ReportedProgress
				item.ReportedAt = sess.ReportedAt
			}
		}

		if len(item.ActiveLeases) > 0 && item.Status == "offline" && !k.Disabled {
			item.Status = "working"
		}
		if k.Disabled {
			item.Status = "offline"
			item.Online = false
		} else {
			item.Online = len(sessList) > 0 && item.Status != "idle"
		}
		if len(sessList) > 0 && item.Status == "idle" {
			item.Status = "offline"
		}

		if item.Online {
			summary.OnlineAgents++
		}
		switch item.Status {
		case "working":
			summary.WorkingAgents++
		case "watching":
			summary.WatchingAgents++
		}
		agents = append(agents, item)
	}

	sort.Slice(agents, func(i, j int) bool {
		ri, rj := statusRank(agents[i].Status), statusRank(agents[j].Status)
		if ri != rj {
			return ri > rj
		}
		ti := agents[i].Key.CreatedAt
		if agents[i].LastSeen != nil {
			ti = *agents[i].LastSeen
		}
		tj := agents[j].Key.CreatedAt
		if agents[j].LastSeen != nil {
			tj = *agents[j].LastSeen
		}
		return ti.After(tj)
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"summary":  summary,
		"agents":   agents,
		"sessions": visibleSessions,
	})
}

func (s *Server) handleDisconnectAgentSession(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 sessionID"})
		return
	}
	mcpSrv := s.getMCPServer()
	for _, snap := range mcpSrv.ListSessions() {
		if snap.SessionID == sessionID {
			if u.Role != "admin" && !strings.EqualFold(snap.Username, u.Username) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "无权断开其他用户的 Agent 会话"})
				return
			}
			mcpSrv.DisconnectSession(sessionID)
			s.logAudit(r, u.Username, "agent_session_disconnect", snap.AgentName, "ok", "session="+sessionID)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "MCP 会话不存在或已断开"})
}
