package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
)

const (
	maxSessions       = 512
	sessionIdleTTL    = 2 * time.Hour
	maxMCPBodyBytes   = 24 << 20
	sseKeepaliveEvery = 20 * time.Second
)

type mcpSession struct {
	id              string
	owner           string
	protocolVersion string
	createdAt       time.Time
	lastSeen        time.Time
}

// Server exposes a pure Go Streamable HTTP MCP endpoint at /mcp.
type Server struct {
	backend       Backend
	serverVersion string
	mu            sync.Mutex
	sessions      map[string]*mcpSession
}

func NewServer(backend Backend, serverVersion string) *Server {
	return &Server{
		backend:       backend,
		serverVersion: strings.TrimSpace(serverVersion),
		sessions:      make(map[string]*mcpSession),
	}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) createSession(owner, protocolVersion string) *mcpSession {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*mcpSession)
	}
	for id, sess := range s.sessions {
		if now.Sub(sess.lastSeen) > sessionIdleTTL {
			delete(s.sessions, id)
		}
	}
	if len(s.sessions) >= maxSessions {
		var oldestID string
		var oldestTime time.Time
		for id, sess := range s.sessions {
			if oldestID == "" || sess.lastSeen.Before(oldestTime) {
				oldestID = id
				oldestTime = sess.lastSeen
			}
		}
		if oldestID != "" {
			delete(s.sessions, oldestID)
		}
	}
	sess := &mcpSession{
		id:              newSessionID(),
		owner:           owner,
		protocolVersion: protocolVersion,
		createdAt:       now,
		lastSeen:        now,
	}
	s.sessions[sess.id] = sess
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
	if now.Sub(sess.lastSeen) > sessionIdleTTL {
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
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return http.StatusNotFound, "MCP 会话不存在"
	}
	if now.Sub(sess.lastSeen) > sessionIdleTTL {
		delete(s.sessions, id)
		return http.StatusNotFound, "MCP 会话已过期"
	}
	if sess.owner != owner {
		return http.StatusForbidden, "无权注销其他主体的 MCP 会话"
	}
	delete(s.sessions, id)
	return http.StatusNoContent, ""
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, err := s.backend.Authenticate(r)
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="LFH MCP"`)
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
		if sid := strings.TrimSpace(r.Header.Get("Mcp-Session-Id")); sid != "" {
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

		resp, newSID, hasResp := s.handleJSONRPC(r, p, owner, req)
		if newSID != "" {
			createdSessionID = newSID
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

func (s *Server) handleJSONRPC(r *http.Request, p auth.Principal, owner string, req JSONRPCRequest) (JSONRPCResponse, string, bool) {
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
		sess := s.createSession(owner, chosenVer)
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
			Instructions: "Use whoami and list_tree to explore accessible workspaces. Pass expected_version on write_file and patch_file when coordinating concurrent edits.",
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
		toolRes, known := s.dispatchTool(r.Context(), r, p, params.Name, params.Arguments)
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
