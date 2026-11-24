package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"lfh/internal/auth"
)

func toolCatalog() []Tool {
	return []Tool{
		{
			Name:        "whoami",
			Description: "Inspect caller identity, granted scopes, allowed read/write folders, cluster free space, and accessible workspace mounts.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "list_users",
			Description: "List active team usernames available for allowed_users visibility assignments.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "list_tree",
			Description: "List lightweight workspace files and code tree metadata with version and lease status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"folder_path": map[string]any{"type": "string", "description": "Optional directory prefix filter (e.g. shopapp/backend)."},
					"depth":       map[string]any{"type": "integer", "description": "Optional max folder depth relative to folder_path."},
					"glob":        map[string]any{"type": "string", "description": "Optional filename glob filter (e.g. *.go)."},
					"limit":       map[string]any{"type": "integer", "description": "Max entries returned (default 200, max 1000)."},
				},
			},
		},
		{
			Name:        "search_files",
			Description: "Search workspace files by filename or text content within readable folders.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"folder_path":    map[string]any{"type": "string", "description": "Optional directory prefix filter."},
					"name_query":     map[string]any{"type": "string", "description": "Substring query matched against file path and name."},
					"content_query":  map[string]any{"type": "string", "description": "Text substring matched inside readable files <= 2MB."},
					"case_sensitive": map[string]any{"type": "boolean", "description": "Whether search is case-sensitive (default false)."},
					"limit":          map[string]any{"type": "integer", "description": "Max files returned (default 50, max 200)."},
				},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a text file with optional line range slicing, ETag hash skip (if_none_match), version metadata, and active lease info.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":          map[string]any{"type": "string", "description": "Logical file path (e.g. shopapp/backend/main.go)."},
					"file_id":       map[string]any{"type": "string", "description": "Stable LFH file ID."},
					"start_line":    map[string]any{"type": "integer", "description": "1-based inclusive start line."},
					"end_line":      map[string]any{"type": "integer", "description": "1-based inclusive end line."},
					"if_none_match": map[string]any{"type": "string", "description": "Skip content transfer when current SHA-256 matches."},
				},
			},
		},
		{
			Name:        "write_file",
			Description: "Create or overwrite a file (writing atomically through writable workspace mounts with Shadow backup and optional optimistic lock expected_version).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"path", "content"},
				"properties": map[string]any{
					"path":             map[string]any{"type": "string", "description": "Logical target file path."},
					"content":          map[string]any{"type": "string", "description": "Full text content to write."},
					"expected_version": map[string]any{"type": "integer", "description": "0 requires creating a new file; N > 0 requires current version == N."},
					"allowed_users":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional restricted team member usernames."},
					"note":             map[string]any{"type": "string", "description": "Optional change note recorded in version history."},
				},
			},
		},
		{
			Name:        "patch_file",
			Description: "Apply targeted search_replace hunks or a unified_diff patch while preserving line endings and checking expected_version / soft lease.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Logical target file path."},
					"file_id": map[string]any{"type": "string", "description": "Stable LFH file ID."},
					"mode":    map[string]any{"type": "string", "enum": []string{"search_replace", "unified_diff"}, "description": "Patch mode (default search_replace)."},
					"hunks": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"old_text", "new_text"},
							"properties": map[string]any{
								"old_text":    map[string]any{"type": "string"},
								"new_text":    map[string]any{"type": "string"},
								"replace_all": map[string]any{"type": "boolean"},
							},
						},
					},
					"unified_diff":     map[string]any{"type": "string", "description": "Unified diff text starting with @@ hunk headers."},
					"expected_version": map[string]any{"type": "integer", "description": "Optimistic concurrency version assertion."},
					"note":             map[string]any{"type": "string", "description": "Optional patch summary note."},
				},
			},
		},
		{
			Name:        "batch_commit",
			Description: "Atomically validate, stage, and commit up to 50 coordinated file write/patch/delete operations without intermediate torn states.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"operations"},
				"properties": map[string]any{
					"note": map[string]any{"type": "string", "description": "Commit message attached to every affected version."},
					"operations": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"action", "path"},
							"properties": map[string]any{
								"action":           map[string]any{"type": "string", "enum": []string{"write", "patch", "delete"}},
								"path":             map[string]any{"type": "string"},
								"content":          map[string]any{"type": "string"},
								"mode":             map[string]any{"type": "string", "enum": []string{"search_replace", "unified_diff"}},
								"hunks":            map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
								"unified_diff":     map[string]any{"type": "string"},
								"expected_version": map[string]any{"type": "integer"},
								"allowed_users":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
						},
					},
				},
			},
		},
		{
			Name:        "delete_file",
			Description: "Delete an accessible file (requires explicit delete scope; removes writable mount file and Shadow backups).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":             map[string]any{"type": "string"},
					"file_id":          map[string]any{"type": "string"},
					"expected_version": map[string]any{"type": "integer"},
				},
			},
		},
		{
			Name:        "set_file_visibility",
			Description: "Update allowed_users visibility restriction on a file (requires share scope and allow_set_visibility).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"allowed_users"},
				"properties": map[string]any{
					"path":          map[string]any{"type": "string"},
					"file_id":       map[string]any{"type": "string"},
					"allowed_users": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
		},
		{
			Name:        "acquire_lease",
			Description: "Acquire or renew an expiring soft edit lease lock on a file (default 60s, max 300s).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string"},
					"file_id":     map[string]any{"type": "string"},
					"ttl_seconds": map[string]any{"type": "integer"},
				},
			},
		},
		{
			Name:        "release_lease",
			Description: "Release an active soft edit lease held by the caller.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"file_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "get_file_versions",
			Description: "List version history of a file including version numbers, authors, update methods, and notes.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"file_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "restore_file_version",
			Description: "Restore a historical file version (including Shadow backups for writable workspace mounts).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"version"},
				"properties": map[string]any{
					"path":             map[string]any{"type": "string"},
					"file_id":          map[string]any{"type": "string"},
					"version":          map[string]any{"type": "integer"},
					"expected_version": map[string]any{"type": "integer"},
					"note":             map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "wait_for_changes",
			Description: "Long-poll up to 30 seconds for readable workspace file changes after since_version.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"folder_prefix": map[string]any{"type": "string", "description": "Optional readable folder prefix filter."},
					"since_version": map[string]any{"type": "integer", "description": "Last seen EventHub version sequence."},
					"timeout_sec":   map[string]any{"type": "integer", "description": "Max wait seconds (default 15, max 30)."},
				},
			},
		},
		{
			Name:        "issue_transfer_ticket",
			Description: "Issue an HMAC-signed presigned URL (/s/ticket) for out-of-band large binary upload (PUT) or download (GET), with Direct-to-Worker LAN/Tailscale acceleration when eligible.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"action"},
				"properties": map[string]any{
					"action":        map[string]any{"type": "string", "enum": []string{"up", "down"}, "description": "up for single-use upload ticket, down for download ticket."},
					"file_id":       map[string]any{"type": "string", "description": "Required for action=down unless path is given."},
					"path":          map[string]any{"type": "string", "description": "Logical file path (e.g. build/app.tar.gz)."},
					"folder_path":   map[string]any{"type": "string", "description": "Target folder for action=up when path is not given."},
					"file_name":     map[string]any{"type": "string", "description": "Target filename for action=up when path is not given."},
					"allowed_users": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"max_bytes":     map[string]any{"type": "integer", "description": "Upload byte limit for action=up (default 2GB)."},
					"target_node":   map[string]any{"type": "string", "description": "Optional preferred target node ID for non-mount upload."},
					"ttl_seconds":   map[string]any{"type": "integer", "description": "Ticket lifetime in seconds (10..86400, default 600)."},
					"prefer_direct": map[string]any{"type": "boolean", "description": "Prefer direct worker LAN/Tailscale URL when reachable (default true)."},
				},
			},
		},
		{
			Name:        "trigger_workspace_verify",
			Description: "Run an allowlisted build/test verify preset on a readable workspace mount (single-flight coalesced, captures final 128KB output and .lfh-logs/<preset>.log).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"mount_id", "preset_id"},
				"properties": map[string]any{
					"mount_id":  map[string]any{"type": "string", "description": "Mount ID returned by whoami."},
					"preset_id": map[string]any{"type": "string", "description": "Verify preset ID configured on that mount."},
				},
			},
		},
	}
}

func decodeToolArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func (s *Server) dispatchTool(ctx context.Context, r *http.Request, p auth.Principal, name string, rawArgs json.RawMessage) (CallToolResult, bool) {
	requireScope := func(scope string) *CallToolResult {
		if scope != "" && !p.HasScope(scope) {
			res := newToolErrorResult(NewToolError("forbidden", "当前 Agent 密钥缺少权限: "+scope, "Ask the administrator to grant scope '"+scope+"' on this AgentKey.", nil))
			return &res
		}
		return nil
	}

	switch name {
	case "whoami":
		res, err := s.backend.WhoAmI(ctx, p)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "list_users":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		users, err := s.backend.ListUsers(ctx, p)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(map[string]any{"users": users})
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "list_tree":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		var in ListTreeInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		res, err := s.backend.ListTree(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "search_files":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		var in SearchFilesInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		res, err := s.backend.SearchFiles(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "read_file":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		var in ReadFileInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		res, err := s.backend.ReadFile(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "write_file":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in WriteFileInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		res, err := s.backend.WriteFile(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "patch_file":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in PatchFileInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		res, err := s.backend.PatchFile(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "batch_commit":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in BatchCommitInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		for _, op := range in.Operations {
			if op.Action == "delete" {
				if denied := requireScope(auth.ScopeDelete); denied != nil {
					return *denied, true
				}
				break
			}
		}
		res, err := s.backend.BatchCommit(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "delete_file":
		if denied := requireScope(auth.ScopeDelete); denied != nil {
			return *denied, true
		}
		var in DeleteFileInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		res, err := s.backend.DeleteFile(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "set_file_visibility":
		if denied := requireScope(auth.ScopeShare); denied != nil {
			return *denied, true
		}
		var in SetVisibilityInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		res, err := s.backend.SetFileVisibility(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "acquire_lease":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in AcquireLeaseInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		lease, err := s.backend.AcquireLease(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(map[string]any{"ok": true, "lease": lease})
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "release_lease":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in ReleaseLeaseInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		if err := s.backend.ReleaseLease(ctx, p, in); err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(map[string]any{"ok": true})
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "get_file_versions":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		var in FileLocatorInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		res, err := s.backend.GetFileVersions(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "restore_file_version":
		if denied := requireScope(auth.ScopeWrite); denied != nil {
			return *denied, true
		}
		var in RestoreVersionInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if in.FileID == "" && in.Path == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须提供 path 或 file_id", "", nil)), true
		}
		if in.Version <= 0 {
			return newToolErrorResult(NewToolError("invalid_params", "version 必须为正整数", "", nil)), true
		}
		res, err := s.backend.RestoreFileVersion(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "wait_for_changes":
		if denied := requireScope(auth.ScopeRead); denied != nil {
			return *denied, true
		}
		var in WaitChangesInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		res, err := s.backend.WaitForChanges(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "issue_transfer_ticket":
		var in IssueTicketInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		action := strings.ToLower(strings.TrimSpace(in.Action))
		switch action {
		case "down":
			if denied := requireScope(auth.ScopeRead); denied != nil {
				return *denied, true
			}
		case "up":
			if denied := requireScope(auth.ScopeWrite); denied != nil {
				return *denied, true
			}
		default:
			return newToolErrorResult(NewToolError("invalid_params", "action 必须为 up 或 down", "", nil)), true
		}
		in.Action = action
		res, err := s.backend.IssueTransferTicket(ctx, r, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	case "trigger_workspace_verify":
		if denied := requireScope(auth.ScopeVerify); denied != nil {
			return *denied, true
		}
		var in TriggerVerifyInput
		if err := decodeToolArgs(rawArgs, &in); err != nil {
			return newToolErrorResult(NewToolError("invalid_params", "参数解析失败: "+err.Error(), "", nil)), true
		}
		if strings.TrimSpace(in.MountID) == "" || strings.TrimSpace(in.PresetID) == "" {
			return newToolErrorResult(NewToolError("invalid_params", "必须指定 mount_id 和 preset_id", "", nil)), true
		}
		res, err := s.backend.TriggerWorkspaceVerify(ctx, p, in)
		if err != nil {
			return newToolErrorResult(err), true
		}
		out, err := newToolJSONResult(res)
		if err != nil {
			return newToolErrorResult(err), true
		}
		return out, true

	default:
		return CallToolResult{}, false
	}
}
