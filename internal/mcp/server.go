package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
)

const (
	maxSessions          = 512
	sessionIdleTTL       = 2 * time.Hour
	onlineWindow         = 5 * time.Minute
	maxMCPBodyBytes      = 24 << 20
	sseKeepaliveEvery    = 20 * time.Second
	presenceFlipThrottle = 400 * time.Millisecond
)

// SessionSnapshot describes an active or recently seen MCP session and its live work status.
type SessionSnapshot struct {
	SessionID        string     `json:"session_id"`
	Owner            string     `json:"owner"`
	Username         string     `json:"username"`
	IsAgent          bool       `json:"is_agent"`
	AgentKeyID       string     `json:"agent_key_id,omitempty"`
	AgentName        string     `json:"agent_name,omitempty"`
	ClientName       string     `json:"client_name,omitempty"`
	ClientVersion    string     `json:"client_version,omitempty"`
	ClientIP         string     `json:"client_ip,omitempty"`
	ProtocolVersion  string     `json:"protocol_version,omitempty"`
	Status           string     `json:"status"` // "working" | "watching" | "online" | "idle"
	SSEConnected     bool       `json:"sse_connected"`
	ActiveTool       string     `json:"active_tool,omitempty"`
	ActiveTarget     string     `json:"active_target,omitempty"`
	ActiveSince      *time.Time `json:"active_since,omitempty"`
	ReportedState    string     `json:"reported_state,omitempty"`
	ReportedTask     string     `json:"reported_task,omitempty"`
	ReportedProgress string     `json:"reported_progress,omitempty"`
	ReportedAt       *time.Time `json:"reported_at,omitempty"`
	LastTool         string     `json:"last_tool,omitempty"`
	LastTarget       string     `json:"last_target,omitempty"`
	LastToolOK       bool       `json:"last_tool_ok"`
	ToolCalls        int64      `json:"tool_calls"`
	CreatedAt        time.Time  `json:"created_at"`
	LastSeen         time.Time  `json:"last_seen"`
}

type mcpSession struct {
	id               string
	owner            string
	username         string
	isAgent          bool
	agentKeyID       string
	agentName        string
	clientName       string
	clientVersion    string
	clientIP         string
	protocolVersion  string
	sseConns         int
	inFlight         int
	watching         int
	activeTool       string
	activeTarget     string
	activeSince      time.Time
	reportedState    string
	reportedTask     string
	reportedProgress string
	reportedAt       time.Time
	lastTool         string
	lastTarget       string
	lastToolOK       bool
	toolCalls        int64
	createdAt        time.Time
	lastSeen         time.Time
	lastNotifyAt     time.Time
	lastNotifyStatus string
	cancelCh         chan struct{}
	cancelOnce       sync.Once
}

func (sess *mcpSession) closeCancel() {
	if sess == nil || sess.cancelCh == nil {
		return
	}
	sess.cancelOnce.Do(func() {
		close(sess.cancelCh)
	})
}

func (sess *mcpSession) derivedStatus(now time.Time) string {
	if sess.inFlight > 0 {
		return "working"
	}
	if sess.reportedState == "working" && !sess.reportedAt.IsZero() && now.Sub(sess.reportedAt) <= onlineWindow {
		return "working"
	}
	if sess.watching > 0 || sess.sseConns > 0 {
		return "watching"
	}
	if now.Sub(sess.lastSeen) <= onlineWindow {
		return "online"
	}
	return "idle"
}

func (sess *mcpSession) snapshot(now time.Time) SessionSnapshot {
	snap := SessionSnapshot{
		SessionID:        sess.id,
		Owner:            sess.owner,
		Username:         sess.username,
		IsAgent:          sess.isAgent,
		AgentKeyID:       sess.agentKeyID,
		AgentName:        sess.agentName,
		ClientName:       sess.clientName,
		ClientVersion:    sess.clientVersion,
		ClientIP:         sess.clientIP,
		ProtocolVersion:  sess.protocolVersion,
		Status:           sess.derivedStatus(now),
		SSEConnected:     sess.sseConns > 0,
		ActiveTool:       sess.activeTool,
		ActiveTarget:     sess.activeTarget,
		ReportedState:    sess.reportedState,
		ReportedTask:     sess.reportedTask,
		ReportedProgress: sess.reportedProgress,
		LastTool:         sess.lastTool,
		LastTarget:       sess.lastTarget,
		LastToolOK:       sess.lastToolOK,
		ToolCalls:        sess.toolCalls,
		CreatedAt:        sess.createdAt,
		LastSeen:         sess.lastSeen,
	}
	if sess.inFlight > 0 && !sess.activeSince.IsZero() {
		t := sess.activeSince
		snap.ActiveSince = &t
	}
	if !sess.reportedAt.IsZero() {
		t := sess.reportedAt
		snap.ReportedAt = &t
	}
	return snap
}

// Server exposes a pure Go Streamable HTTP MCP endpoint at /mcp.
type Server struct {
	backend          Backend
	serverVersion    string
	mu               sync.Mutex
	sessions         map[string]*mcpSession
	onPresenceChange func(event string, snap SessionSnapshot)
}

func NewServer(backend Backend, serverVersion string) *Server {
	return &Server{
		backend:       backend,
		serverVersion: strings.TrimSpace(serverVersion),
		sessions:      make(map[string]*mcpSession),
	}
}

// SetOnPresenceChange registers an optional callback notified when Agent session state changes.
func (s *Server) SetOnPresenceChange(fn func(event string, snap SessionSnapshot)) {
	s.mu.Lock()
	s.onPresenceChange = fn
	s.mu.Unlock()
}

func principalSessionOwner(p auth.Principal) string {
	if p.IsAgent && p.AgentKey != nil {
		return "agent:" + p.AgentKey.ID
	}
	return "user:" + p.User.Username
}

func newSessionID() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return "mcp_" + hex.EncodeToString(buf)
}

func extractRequestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func resolveWWWAuthenticateHeader(r *http.Request) string {
	if r == nil || strings.TrimSpace(r.Host) == "" {
		return `Bearer realm="LFH MCP"`
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	return fmt.Sprintf(`Bearer realm="LFH MCP", resource_metadata="%s://%s/.well-known/oauth-protected-resource"`, scheme, strings.TrimSpace(r.Host))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) pruneExpiredLocked(now time.Time) {
	for id, sess := range s.sessions {
		if sess.sseConns == 0 && sess.inFlight == 0 && sess.watching == 0 && now.Sub(sess.lastSeen) > sessionIdleTTL {
			sess.closeCancel()
			delete(s.sessions, id)
		}
	}
	if len(s.sessions) < maxSessions {
		return
	}
	var oldestID string
	var oldestTime time.Time
	for id, sess := range s.sessions {
		if oldestID == "" || sess.lastSeen.Before(oldestTime) {
			oldestID = id
			oldestTime = sess.lastSeen
		}
	}
	if oldestID != "" {
		if old := s.sessions[oldestID]; old != nil {
			old.closeCancel()
		}
		delete(s.sessions, oldestID)
	}
}

func applyPrincipalToSessionLocked(sess *mcpSession, p auth.Principal, r *http.Request) {
	sess.username = p.User.Username
	sess.isAgent = p.IsAgent
	if p.IsAgent && p.AgentKey != nil {
		sess.agentKeyID = p.AgentKey.ID
		sess.agentName = p.AgentKey.Name
	} else {
		sess.agentName = p.User.Username
	}
	if ip := extractRequestClientIP(r); ip != "" {
		sess.clientIP = ip
	}
}

func (s *Server) createSession(owner, protocolVersion string, p auth.Principal, clientInfo *Implementation, r *http.Request) *mcpSession {
	now := time.Now()
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]*mcpSession)
	}
	s.pruneExpiredLocked(now)
	sess := &mcpSession{
		id:              newSessionID(),
		owner:           owner,
		protocolVersion: protocolVersion,
		createdAt:       now,
		lastSeen:        now,
		cancelCh:        make(chan struct{}),
		lastNotifyAt:    now,
		lastNotifyStatus: "online",
	}
	applyPrincipalToSessionLocked(sess, p, r)
	if clientInfo != nil {
		sess.clientName = strings.TrimSpace(clientInfo.Name)
		sess.clientVersion = strings.TrimSpace(clientInfo.Version)
	}
	s.sessions[sess.id] = sess
	cb := s.onPresenceChange
	snap := sess.snapshot(now)
	s.mu.Unlock()

	if cb != nil {
		cb("connected", snap)
	}
	return sess
}

func (s *Server) ensureStatelessSessionLocked(owner string, p auth.Principal, r *http.Request, now time.Time) *mcpSession {
	if s.sessions == nil {
		s.sessions = make(map[string]*mcpSession)
	}
	key := "stateless:" + owner
	if sess, ok := s.sessions[key]; ok {
		sess.lastSeen = now
		applyPrincipalToSessionLocked(sess, p, r)
		return sess
	}
	s.pruneExpiredLocked(now)
	sess := &mcpSession{
		id:        key,
		owner:     owner,
		createdAt: now,
		lastSeen:  now,
		cancelCh:  make(chan struct{}),
	}
	applyPrincipalToSessionLocked(sess, p, r)
	s.sessions[key] = sess
	return sess
}

func (s *Server) validateSession(sessionID, owner string) (int, string) {
	id := strings.TrimSpace(sessionID)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return http.StatusNotFound, "MCP 会话不存在或已过期"
	}
	if sess.sseConns == 0 && sess.inFlight == 0 && sess.watching == 0 && now.Sub(sess.lastSeen) > sessionIdleTTL {
		sess.closeCancel()
		delete(s.sessions, id)
		return http.StatusNotFound, "MCP 会话已过期"
	}
	if sess.owner != owner {
		return http.StatusForbidden, "无权使用其他主体的 MCP 会话"
	}
	sess.lastSeen = now
	return http.StatusOK, ""
}

func (s *Server) deleteSession(sessionID, owner string) (int, string) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return http.StatusBadRequest, "缺少 Mcp-Session-Id 请求头"
	}
	now := time.Now()
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return http.StatusNotFound, "MCP 会话不存在"
	}
	if sess.sseConns == 0 && sess.inFlight == 0 && sess.watching == 0 && now.Sub(sess.lastSeen) > sessionIdleTTL {
		sess.closeCancel()
		delete(s.sessions, id)
		s.mu.Unlock()
		return http.StatusNotFound, "MCP 会话已过期"
	}
	if sess.owner != owner {
		s.mu.Unlock()
		return http.StatusForbidden, "无权注销其他主体的 MCP 会话"
	}
	snap := sess.snapshot(now)
	sess.closeCancel()
	delete(s.sessions, id)
	cb := s.onPresenceChange
	s.mu.Unlock()

	if cb != nil {
		cb("disconnected", snap)
	}
	return http.StatusNoContent, ""
}

// DisconnectSession forcibly closes an active MCP session by ID (invoked by admin/owner).
func (s *Server) DisconnectSession(sessionID string) (SessionSnapshot, bool) {
	id := strings.TrimSpace(sessionID)
	now := time.Now()
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return SessionSnapshot{}, false
	}
	snap := sess.snapshot(now)
	sess.closeCancel()
	delete(s.sessions, id)
	cb := s.onPresenceChange
	s.mu.Unlock()

	if cb != nil {
		cb("disconnected", snap)
	}
	return snap, true
}

// DisconnectByAgentKey forcibly terminates every MCP session belonging to keyID.
func (s *Server) DisconnectByAgentKey(keyID string) int {
	target := strings.TrimSpace(keyID)
	if target == "" {
		return 0
	}
	now := time.Now()
	var disconnected []SessionSnapshot

	s.mu.Lock()
	for id, sess := range s.sessions {
		if sess.agentKeyID == target {
			disconnected = append(disconnected, sess.snapshot(now))
			sess.closeCancel()
			delete(s.sessions, id)
		}
	}
	cb := s.onPresenceChange
	s.mu.Unlock()

	if cb != nil {
		for _, snap := range disconnected {
			cb("disconnected", snap)
		}
	}
	return len(disconnected)
}

// ListSessions returns snapshots of all live/non-expired MCP sessions sorted by recent activity.
func (s *Server) ListSessions() []SessionSnapshot {
	now := time.Now()
	s.mu.Lock()
	s.pruneExpiredLocked(now)
	out := make([]SessionSnapshot, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess.snapshot(now))
	}
	s.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

func (s *Server) openSSESession(sid, owner string, p auth.Principal, r *http.Request) (sessionKey string, cancelCh <-chan struct{}) {
	now := time.Now()
	s.mu.Lock()
	var sess *mcpSession
	if sid != "" {
		sess = s.sessions[sid]
	}
	if sess == nil {
		sess = s.ensureStatelessSessionLocked(owner, p, r, now)
	} else {
		applyPrincipalToSessionLocked(sess, p, r)
	}
	sess.sseConns++
	sess.lastSeen = now
	cancelCh = sess.cancelCh
	sessionKey = sess.id
	status := sess.derivedStatus(now)
	shouldNotify := status != sess.lastNotifyStatus && now.Sub(sess.lastNotifyAt) >= presenceFlipThrottle
	var snap SessionSnapshot
	if shouldNotify {
		sess.lastNotifyStatus = status
		sess.lastNotifyAt = now
		snap = sess.snapshot(now)
	}
	cb := s.onPresenceChange
	s.mu.Unlock()

	if shouldNotify && cb != nil {
		cb("status_changed", snap)
	}
	return sessionKey, cancelCh
}

func (s *Server) closeSSESession(sessionKey string) {
	now := time.Now()
	s.mu.Lock()
	sess, ok := s.sessions[sessionKey]
	if !ok {
		s.mu.Unlock()
		return
	}
	if sess.sseConns > 0 {
		sess.sseConns--
	}
	sess.lastSeen = now
	status := sess.derivedStatus(now)
	shouldNotify := status != sess.lastNotifyStatus && now.Sub(sess.lastNotifyAt) >= presenceFlipThrottle
	var snap SessionSnapshot
	if shouldNotify {
		sess.lastNotifyStatus = status
		sess.lastNotifyAt = now
		snap = sess.snapshot(now)
	}
	cb := s.onPresenceChange
	s.mu.Unlock()

	if shouldNotify && cb != nil {
		cb("status_changed", snap)
	}
}

func truncateText(raw string, maxRunes int) string {
	trimmed := strings.TrimSpace(raw)
	if maxRunes <= 0 || trimmed == "" {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes])
}

func summarizeToolTarget(toolName string, rawArgs json.RawMessage) string {
	if len(rawArgs) == 0 || string(rawArgs) == "null" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(rawArgs, &m); err != nil || len(m) == 0 {
		return ""
	}
	str := func(k string) string {
		if v, ok := m[k]; ok {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}
	switch toolName {
	case "trigger_workspace_verify":
		mid := str("mount_id")
		pid := str("preset_id")
		if mid != "" && pid != "" {
			return truncateText(mid+"/"+pid, 96)
		}
		return truncateText(pid+mid, 96)
	case "batch_commit":
		if ops, ok := m["operations"].([]any); ok {
			return fmt.Sprintf("%d ops", len(ops))
		}
	case "report_agent_status":
		return truncateText(str("task"), 96)
	}
	for _, key := range []string{"task_id", "path", "file_id", "folder_prefix", "folder_path", "content_query", "name_query"} {
		if val := str(key); val != "" && val != "<nil>" {
			return truncateText(val, 96)
		}
	}
	return ""
}

func (s *Server) beginToolCall(sessionKey, owner string, p auth.Principal, r *http.Request, toolName, target string) (resolvedKey string, isWatching bool) {
	now := time.Now()
	isWatching = toolName == "wait_for_changes"

	s.mu.Lock()
	var sess *mcpSession
	if sessionKey != "" {
		sess = s.sessions[sessionKey]
	}
	if sess == nil {
		sess = s.ensureStatelessSessionLocked(owner, p, r, now)
	} else {
		applyPrincipalToSessionLocked(sess, p, r)
	}
	resolvedKey = sess.id
	sess.lastSeen = now
	if isWatching {
		sess.watching++
	} else {
		sess.inFlight++
		sess.activeTool = toolName
		sess.activeTarget = target
		sess.activeSince = now
	}
	status := sess.derivedStatus(now)
	shouldNotify := status != sess.lastNotifyStatus && now.Sub(sess.lastNotifyAt) >= presenceFlipThrottle
	var snap SessionSnapshot
	if shouldNotify {
		sess.lastNotifyStatus = status
		sess.lastNotifyAt = now
		snap = sess.snapshot(now)
	}
	cb := s.onPresenceChange
	s.mu.Unlock()

	if shouldNotify && cb != nil {
		cb("status_changed", snap)
	}
	return resolvedKey, isWatching
}

func (s *Server) endToolCall(resolvedKey, toolName, target string, ok bool, isWatching bool) {
	now := time.Now()
	s.mu.Lock()
	sess, exists := s.sessions[resolvedKey]
	if !exists {
		s.mu.Unlock()
		return
	}
	if isWatching {
		if sess.watching > 0 {
			sess.watching--
		}
	} else {
		if sess.inFlight > 0 {
			sess.inFlight--
		}
		if sess.inFlight == 0 {
			sess.activeTool = ""
			sess.activeTarget = ""
			sess.activeSince = time.Time{}
		}
	}
	sess.toolCalls++
	sess.lastTool = toolName
	sess.lastTarget = target
	sess.lastToolOK = ok
	sess.lastSeen = now

	status := sess.derivedStatus(now)
	shouldNotify := status != sess.lastNotifyStatus && now.Sub(sess.lastNotifyAt) >= presenceFlipThrottle
	var snap SessionSnapshot
	if shouldNotify {
		sess.lastNotifyStatus = status
		sess.lastNotifyAt = now
		snap = sess.snapshot(now)
	}
	cb := s.onPresenceChange
	s.mu.Unlock()

	if shouldNotify && cb != nil {
		cb("status_changed", snap)
	}
}

func (s *Server) updateReportedStatus(sessionKey, state, task, progress string) (SessionSnapshot, error) {
	cleanState := strings.ToLower(strings.TrimSpace(state))
	switch cleanState {
	case "working", "idle", "waiting", "done", "error":
	default:
		return SessionSnapshot{}, NewToolError("invalid_params", "state 必须为 working, idle, waiting, done 或 error", "", nil)
	}
	cleanTask := truncateText(task, 200)
	cleanProg := truncateText(progress, 64)
	now := time.Now()

	s.mu.Lock()
	sess, ok := s.sessions[sessionKey]
	if !ok {
		s.mu.Unlock()
		return SessionSnapshot{}, NewToolError("not_found", "MCP 会话不存在", "", nil)
	}
	sess.reportedState = cleanState
	sess.reportedTask = cleanTask
	sess.reportedProgress = cleanProg
	sess.reportedAt = now
	sess.lastSeen = now
	snap := sess.snapshot(now)
	sess.lastNotifyStatus = snap.Status
	sess.lastNotifyAt = now
	cb := s.onPresenceChange
	s.mu.Unlock()

	if cb != nil {
		cb("reported_status", snap)
	}
	return snap, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, err := s.backend.Authenticate(r)
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("WWW-Authenticate", resolveWWWAuthenticateHeader(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先提供有效的 Agent Key 或访问令牌"})
		return
	}

	owner := principalSessionOwner(p)

	switch r.Method {
	case http.MethodDelete:
		status, msg := s.deleteSession(r.Header.Get("Mcp-Session-Id"), owner)
		if status == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, status, map[string]string{"error": msg})
		return

	case http.MethodGet:
		if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			w.Header().Set("Allow", "POST, GET, DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET /mcp 仅支持 text/event-stream SSE 流"})
			return
		}
		sid := strings.TrimSpace(r.Header.Get("Mcp-Session-Id"))
		if sid != "" {
			if status, msg := s.validateSession(sid, owner); status != http.StatusOK {
				writeJSON(w, status, map[string]string{"error": msg})
				return
			}
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前连接不支持 SSE 流"})
			return
		}
		sessKey, sessCancel := s.openSSESession(sid, owner, p, r)
		defer s.closeSSESession(sessKey)

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ticker := time.NewTicker(sseKeepaliveEvery)
		defer ticker.Stop()
		doneCh := s.backend.EventsDone()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-doneCh:
				return
			case <-sessCancel:
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}

	case http.MethodPost:
		s.handlePost(w, r, p, owner)
		return

	default:
		w.Header().Set("Allow", "POST, GET, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "不支持的 HTTP 方法"})
	}
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request, p auth.Principal, owner string) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBodyBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &JSONRPCError{Code: ErrCodeParse, Message: "读取请求体失败"},
		})
		return
	}
	if len(rawBody) > maxMCPBodyBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &JSONRPCError{Code: ErrCodeInvalidRequest, Message: "MCP 请求体超过 24MB 上限"},
		})
		return
	}

	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 {
		writeJSON(w, http.StatusBadRequest, JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &JSONRPCError{Code: ErrCodeInvalidRequest, Message: "空 JSON-RPC 请求体"},
		})
		return
	}

	isBatch := trimmed[0] == '['
	var requests []JSONRPCRequest
	if isBatch {
		if err := json.Unmarshal(trimmed, &requests); err != nil || len(requests) == 0 {
			writeJSON(w, http.StatusBadRequest, JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &JSONRPCError{Code: ErrCodeParse, Message: "无效的 JSON-RPC 批量请求"},
			})
			return
		}
	} else {
		var single JSONRPCRequest
		if err := json.Unmarshal(trimmed, &single); err != nil {
			writeJSON(w, http.StatusBadRequest, JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &JSONRPCError{Code: ErrCodeParse, Message: "JSON-RPC 解析失败"},
			})
			return
		}
		requests = []JSONRPCRequest{single}
	}

	sessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id"))
	hasInitialize := false
	for _, req := range requests {
		if req.Method == "initialize" {
			hasInitialize = true
			break
		}
	}
	if sessionID != "" && !hasInitialize {
		if status, msg := s.validateSession(sessionID, owner); status != http.StatusOK {
			writeJSON(w, status, map[string]string{"error": msg})
			return
		}
	}

	var responses []JSONRPCResponse
	var createdSessionID string
	activeSessionID := sessionID

	for _, req := range requests {
		if req.Method == "" || (req.JSONRPC != "" && req.JSONRPC != "2.0") {
			if !req.isNotification() {
				id := req.ID
				if len(id) == 0 {
					id = json.RawMessage("null")
				}
				responses = append(responses, JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      id,
					Error:   &JSONRPCError{Code: ErrCodeInvalidRequest, Message: "无效的 JSON-RPC 2.0 请求"},
				})
			}
			continue
		}

		resp, newSID, hasResp := s.handleJSONRPC(r, p, owner, activeSessionID, req)
		if newSID != "" {
			createdSessionID = newSID
			activeSessionID = newSID
		}
		if hasResp {
			responses = append(responses, resp)
		}
	}

	if createdSessionID != "" {
		w.Header().Set("Mcp-Session-Id", createdSessionID)
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if isBatch {
		writeJSON(w, http.StatusOK, responses)
		return
	}
	writeJSON(w, http.StatusOK, responses[0])
}

func (s *Server) handleJSONRPC(r *http.Request, p auth.Principal, owner, sessionID string, req JSONRPCRequest) (JSONRPCResponse, string, bool) {
	if req.isNotification() {
		switch req.Method {
		case "notifications/initialized", "notifications/cancelled":
			return JSONRPCResponse{}, "", false
		default:
			if strings.HasPrefix(req.Method, "notifications/") {
				return JSONRPCResponse{}, "", false
			}
		}
	}

	id := req.ID
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}

	switch req.Method {
	case "initialize":
		var params InitializeParams
		if len(req.Params) > 0 && string(req.Params) != "null" {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      id,
					Error:   &JSONRPCError{Code: ErrCodeInvalidParams, Message: "initialize 参数格式错误"},
				}, "", true
			}
		}
		chosenVer := DefaultProtocolVersion
		if supportedProtocolVersions[strings.TrimSpace(params.ProtocolVersion)] {
			chosenVer = strings.TrimSpace(params.ProtocolVersion)
		}
		sess := s.createSession(owner, chosenVer, p, params.ClientInfo, r)
		ver := s.serverVersion
		if ver == "" {
			ver = "dev"
		}
		res := InitializeResult{
			ProtocolVersion: chosenVer,
			Capabilities: ServerCapabilities{
				Tools: &ToolsCapability{ListChanged: false},
			},
			ServerInfo: Implementation{
				Name:    "lan-filehub-mcp",
				Version: ver,
			},
			Instructions: "Use whoami and list_tree to explore accessible workspaces. Pass expected_version on write_file and patch_file when coordinating concurrent edits. Optionally call report_agent_status to share your current task progress.",
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: res}, sess.id, true

	case "ping":
		return JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{}}, "", true

	case "tools/list":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  map[string]any{"tools": toolCatalog()},
		}, "", true

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &JSONRPCError{Code: ErrCodeInvalidParams, Message: "tools/call 缺少有效工具名或参数"},
			}, "", true
		}
		target := summarizeToolTarget(params.Name, params.Arguments)
		resolvedKey, isWatching := s.beginToolCall(sessionID, owner, p, r, params.Name, target)
		toolRes, known := s.dispatchTool(r.Context(), r, p, resolvedKey, params.Name, params.Arguments)
		s.endToolCall(resolvedKey, params.Name, target, known && !toolRes.IsError, isWatching)
		if !known {
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &JSONRPCError{Code: ErrCodeMethodNotFound, Message: "未找到 MCP 工具: " + params.Name},
			}, "", true
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: toolRes}, "", true

	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &JSONRPCError{Code: ErrCodeMethodNotFound, Message: "不支持的 MCP 方法: " + req.Method},
		}, "", true
	}
}
