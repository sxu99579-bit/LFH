package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lfh/internal/storage"
)

const DefaultProtocolVersion = "2025-03-26"

var supportedProtocolVersions = map[string]bool{
	"2025-03-26": true,
	"2024-11-05": true,
}

const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r JSONRPCRequest) isNotification() bool {
	trimmed := strings.TrimSpace(string(r.ID))
	return trimmed == "" || trimmed == "null"
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    map[string]any  `json:"capabilities,omitempty"`
	ClientInfo      *Implementation `json:"clientInfo,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type CallToolResult struct {
	Content []TextContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolError maps domain/auth/validation failures into structured, actionable MCP tool errors.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Detail  any    `json:"detail,omitempty"`
}

func (e *ToolError) Error() string {
	return e.Message
}

func NewToolError(code, message, hint string, detail any) *ToolError {
	return &ToolError{
		Code:    code,
		Message: message,
		Hint:    hint,
		Detail:  detail,
	}
}

func newToolJSONResult(payload any) (CallToolResult, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return CallToolResult{}, err
	}
	return CallToolResult{
		Content: []TextContent{{Type: "text", Text: string(b)}},
	}, nil
}

func newToolErrorResult(err error) CallToolResult {
	var te *ToolError
	if errors.As(err, &te) {
		b, _ := json.Marshal(te)
		return CallToolResult{
			Content: []TextContent{{Type: "text", Text: string(b)}},
			IsError: true,
		}
	}

	var conflict *storage.ConflictError
	if errors.As(err, &conflict) {
		payload := ToolError{
			Code:    "version_conflict",
			Message: conflict.Error(),
			Hint:    fmt.Sprintf("Call read_file on %s to inspect latest v%d, merge changes, and retry with expected_version=%d.", conflict.Path, conflict.CurrentVersion, conflict.CurrentVersion),
			Detail:  conflict,
		}
		b, _ := json.Marshal(payload)
		return CallToolResult{
			Content: []TextContent{{Type: "text", Text: string(b)}},
			IsError: true,
		}
	}

	var leaseErr *storage.LeaseLockedError
	if errors.As(err, &leaseErr) {
		payload := ToolError{
			Code:    "lease_locked",
			Message: leaseErr.Error(),
			Hint:    fmt.Sprintf("File %s is currently soft-locked by %s (%d seconds remaining). Wait or coordinate before retrying.", leaseErr.FileID, leaseErr.HolderName, leaseErr.RemainingSec),
			Detail:  leaseErr,
		}
		b, _ := json.Marshal(payload)
		return CallToolResult{
			Content: []TextContent{{Type: "text", Text: string(b)}},
			IsError: true,
		}
	}

	var patchErr *storage.PatchError
	if errors.As(err, &patchErr) {
		payload := ToolError{
			Code:    "patch_failed",
			Message: patchErr.Error(),
			Hint:    "Call read_file to inspect exact lines/context and retry patch_file (or write_file if rewriting the whole file).",
			Detail:  patchErr,
		}
		b, _ := json.Marshal(payload)
		return CallToolResult{
			Content: []TextContent{{Type: "text", Text: string(b)}},
			IsError: true,
		}
	}

	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	code := "tool_error"
	switch {
	case strings.Contains(msg, "无权") || strings.Contains(msg, "禁止") || strings.Contains(msg, "缺少权限") || strings.Contains(msg, "只读"):
		code = "forbidden"
	case strings.Contains(msg, "不存在") || strings.Contains(msg, "未找到"):
		code = "not_found"
	case strings.Contains(msg, "离线"):
		code = "node_offline"
	}
	payload := ToolError{
		Code:    code,
		Message: msg,
	}
	b, _ := json.Marshal(payload)
	return CallToolResult{
		Content: []TextContent{{Type: "text", Text: string(b)}},
		IsError: true,
	}
}
