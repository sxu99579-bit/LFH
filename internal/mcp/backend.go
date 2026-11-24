package mcp

import (
	"context"
	"net/http"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/storage"
)

type VerifyPresetView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	WorkSubDir string `json:"work_sub_dir,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	LogRelPath string `json:"log_rel_path,omitempty"`
}

type MountView struct {
	ID            string             `json:"id"`
	Alias         string             `json:"alias"`
	Writable      bool               `json:"writable"`
	ShadowBackup  bool               `json:"shadow_backup"`
	FileCount     int                `json:"file_count"`
	TotalBytes    int64              `json:"total_bytes"`
	NodeID        string             `json:"node_id"`
	NodeName      string             `json:"node_name"`
	IsSelf        bool               `json:"is_self"`
	Status        string             `json:"status"`
	VerifyPresets []VerifyPresetView `json:"verify_presets,omitempty"`
}

type WhoAmIResult struct {
	Username            string      `json:"username"`
	Role                string      `json:"role"`
	IsAgent             bool        `json:"is_agent"`
	ActorLabel          string      `json:"actor_label,omitempty"`
	AgentKeyID          string      `json:"agent_key_id,omitempty"`
	AgentName           string      `json:"agent_name,omitempty"`
	AvatarID            string      `json:"avatar_id,omitempty"`
	DisplayAlias        string      `json:"display_alias,omitempty"`
	DeviceAlias         string      `json:"device_alias,omitempty"`
	DeviceKind          string      `json:"device_kind,omitempty"`
	Scopes              []string    `json:"scopes"`
	ReadFolders         []string    `json:"read_folders,omitempty"`
	WriteFolders        []string    `json:"write_folders,omitempty"`
	AllowSetVisibility  bool        `json:"allow_set_visibility"`
	DefaultAllowedUsers []string    `json:"default_allowed_users,omitempty"`
	NodeID              string      `json:"node_id"`
	NodeName            string      `json:"node_name"`
	ClusterFreeBytes    int64       `json:"cluster_free_bytes"`
	Mounts              []MountView `json:"mounts"`
}

type SelectableUser struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type ListTreeInput struct {
	FolderPath string `json:"folder_path,omitempty"`
	Depth      int    `json:"depth,omitempty"`
	Glob       string `json:"glob,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type TreeEntry struct {
	FileID        string     `json:"file_id"`
	Path          string     `json:"path"`
	Name          string     `json:"name"`
	FolderPath    string     `json:"folder_path,omitempty"`
	Size          int64      `json:"size"`
	Version       int        `json:"version"`
	UpdatedBy     string     `json:"updated_by,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
	StorageMode   string     `json:"storage_mode,omitempty"`
	NodeID        string     `json:"node_id,omitempty"`
	NodeName      string     `json:"node_name,omitempty"`
	LockedBy      string     `json:"locked_by,omitempty"`
	LockExpiresAt *time.Time `json:"lock_expires_at,omitempty"`
}

type ListTreeResult struct {
	FolderPath   string      `json:"folder_path,omitempty"`
	TotalMatched int         `json:"total_matched"`
	Truncated    bool        `json:"truncated,omitempty"`
	Entries      []TreeEntry `json:"entries"`
}

type SearchFilesInput struct {
	FolderPath    string `json:"folder_path,omitempty"`
	NameQuery     string `json:"name_query,omitempty"`
	ContentQuery  string `json:"content_query,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type SearchLineMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type SearchFileMatch struct {
	FileID  string            `json:"file_id"`
	Path    string            `json:"path"`
	Size    int64             `json:"size"`
	Version int               `json:"version"`
	Matches []SearchLineMatch `json:"matches,omitempty"`
}

type SearchFilesResult struct {
	TotalMatched        int               `json:"total_matched"`
	ScannedContentFiles int               `json:"scanned_content_files"`
	Truncated           bool              `json:"truncated,omitempty"`
	Results             []SearchFileMatch `json:"results"`
}

type FileLocatorInput struct {
	FileID string `json:"file_id,omitempty"`
	Path   string `json:"path,omitempty"`
}

type ReadFileInput struct {
	FileID      string `json:"file_id,omitempty"`
	Path        string `json:"path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	IfNoneMatch string `json:"if_none_match,omitempty"`
}

type ReadFileResult struct {
	FileID      string             `json:"file_id"`
	Path        string             `json:"path"`
	Version     int                `json:"version"`
	SHA256      string             `json:"sha256,omitempty"`
	Size        int64              `json:"size"`
	NotModified bool               `json:"not_modified,omitempty"`
	Content     string             `json:"content,omitempty"`
	TotalLines  int                `json:"total_lines,omitempty"`
	StartLine   int                `json:"start_line,omitempty"`
	EndLine     int                `json:"end_line,omitempty"`
	Truncated   bool               `json:"truncated,omitempty"`
	UpdatedBy   string             `json:"updated_by,omitempty"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Lease       *storage.FileLease `json:"lease,omitempty"`
}

type WriteFileInput struct {
	Path            string   `json:"path"`
	Content         string   `json:"content"`
	ExpectedVersion *int     `json:"expected_version,omitempty"`
	AllowedUsers    []string `json:"allowed_users,omitempty"`
	Note            string   `json:"note,omitempty"`
}

type PatchFileInput struct {
	FileID          string              `json:"file_id,omitempty"`
	Path            string              `json:"path,omitempty"`
	Mode            string              `json:"mode,omitempty"`
	Hunks           []storage.PatchHunk `json:"hunks,omitempty"`
	UnifiedDiff     string              `json:"unified_diff,omitempty"`
	ExpectedVersion *int                `json:"expected_version,omitempty"`
	Note            string              `json:"note,omitempty"`
}

type BatchCommitInput struct {
	Note       string            `json:"note,omitempty"`
	Operations []storage.BatchOp `json:"operations"`
}

type DeleteFileInput struct {
	FileID          string `json:"file_id,omitempty"`
	Path            string `json:"path,omitempty"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
}

type DeleteFileResult struct {
	OK     bool   `json:"ok"`
	FileID string `json:"file_id"`
	Path   string `json:"path"`
}

type SetVisibilityInput struct {
	FileID       string   `json:"file_id,omitempty"`
	Path         string   `json:"path,omitempty"`
	AllowedUsers []string `json:"allowed_users"`
}

type AcquireLeaseInput struct {
	FileID     string `json:"file_id,omitempty"`
	Path       string `json:"path,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type ReleaseLeaseInput struct {
	FileID string `json:"file_id,omitempty"`
	Path   string `json:"path,omitempty"`
}

type RestoreVersionInput struct {
	FileID          string `json:"file_id,omitempty"`
	Path            string `json:"path,omitempty"`
	Version         int    `json:"version"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
	Note            string `json:"note,omitempty"`
}

type FileSummary struct {
	ID           string             `json:"id"`
	Path         string             `json:"path"`
	Name         string             `json:"name"`
	FolderPath   string             `json:"folder_path,omitempty"`
	Size         int64              `json:"size"`
	Version      int                `json:"version"`
	VersionCount int                `json:"version_count"`
	SHA256       string             `json:"sha256,omitempty"`
	UpdateMethod string             `json:"update_method,omitempty"`
	VersionNote  string             `json:"version_note,omitempty"`
	UpdatedBy    string             `json:"updated_by,omitempty"`
	UpdatedAt    time.Time          `json:"updated_at"`
	AllowedUsers []string           `json:"allowed_users,omitempty"`
	StorageMode  string             `json:"storage_mode,omitempty"`
	MountID      string             `json:"mount_id,omitempty"`
	NodeID       string             `json:"node_id,omitempty"`
	NodeName     string             `json:"node_name,omitempty"`
	Lease        *storage.FileLease `json:"lease,omitempty"`
}

type VersionInfo struct {
	Version      int       `json:"version"`
	Size         int64     `json:"size"`
	Uploader     string    `json:"uploader"`
	UploadedAt   time.Time `json:"uploaded_at"`
	DeviceName   string    `json:"device_name,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	Note         string    `json:"note,omitempty"`
	UpdateMethod string    `json:"update_method,omitempty"`
}

type FileVersionsResult struct {
	FileID         string        `json:"file_id"`
	Path           string        `json:"path"`
	CurrentVersion int           `json:"current_version"`
	VersionCount   int           `json:"version_count"`
	Versions       []VersionInfo `json:"versions"`
}

type WaitChangesInput struct {
	FolderPrefix string `json:"folder_prefix,omitempty"`
	SinceVersion uint64 `json:"since_version,omitempty"`
	TimeoutSec   int    `json:"timeout_sec,omitempty"`
}

type ChangeEvent struct {
	Version     uint64   `json:"version"`
	Kind        string   `json:"kind"`
	Uploader    string   `json:"uploader,omitempty"`
	ActorName   string   `json:"actor_name,omitempty"`
	Count       int      `json:"count,omitempty"`
	FileNames   []string `json:"file_names,omitempty"`
	FolderPaths []string `json:"folder_paths,omitempty"`
	BatchID     string   `json:"batch_id,omitempty"`
	NodeName    string   `json:"node_name,omitempty"`
	Timestamp   int64    `json:"timestamp"`
}

type WaitChangesResult struct {
	CurrentVersion uint64        `json:"current_version"`
	TimedOut       bool          `json:"timed_out,omitempty"`
	Gap            bool          `json:"gap,omitempty"`
	Events         []ChangeEvent `json:"events"`
}

type IssueTicketInput struct {
	Action       string   `json:"action"`
	FileID       string   `json:"file_id,omitempty"`
	Path         string   `json:"path,omitempty"`
	FolderPath   string   `json:"folder_path,omitempty"`
	FileName     string   `json:"file_name,omitempty"`
	AllowedUsers []string `json:"allowed_users,omitempty"`
	MaxBytes     int64    `json:"max_bytes,omitempty"`
	TargetNode   string   `json:"target_node,omitempty"`
	TTLSeconds   int      `json:"ttl_seconds,omitempty"`
	PreferDirect *bool    `json:"prefer_direct,omitempty"`
}

type IssueTicketResult struct {
	Action         string    `json:"action"`
	NodeID         string    `json:"node_id"`
	NodeName       string    `json:"node_name"`
	DirectURL      string    `json:"direct_url,omitempty"`
	EntryURL       string    `json:"entry_url"`
	RecommendedURL string    `json:"recommended_url"`
	ExpiresAt      time.Time `json:"expires_at"`
	MaxBytes       int64     `json:"max_bytes,omitempty"`
	SingleUse      bool      `json:"single_use"`
	Method         string    `json:"method"`
}

type TriggerVerifyInput struct {
	MountID  string `json:"mount_id"`
	PresetID string `json:"preset_id"`
}

type HeartbeatTaskInput struct {
	TaskID       string `json:"task_id"`
	SubtaskID    string `json:"subtask_id,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type TaskCommentResult struct {
	Task    bridge.AgentTask   `json:"task"`
	Comment bridge.TaskComment `json:"comment"`
}

// Backend bridges MCP tool invocations cleanly into LFH auth, storage, cluster, and event services.
type Backend interface {
	Authenticate(r *http.Request) (auth.Principal, error)
	EventsDone() <-chan struct{}

	WhoAmI(ctx context.Context, p auth.Principal) (WhoAmIResult, error)
	ListUsers(ctx context.Context, p auth.Principal) ([]SelectableUser, error)
	ListTree(ctx context.Context, p auth.Principal, in ListTreeInput) (ListTreeResult, error)
	SearchFiles(ctx context.Context, p auth.Principal, in SearchFilesInput) (SearchFilesResult, error)
	ReadFile(ctx context.Context, p auth.Principal, in ReadFileInput) (ReadFileResult, error)
	WriteFile(ctx context.Context, r *http.Request, p auth.Principal, in WriteFileInput) (FileSummary, error)
	PatchFile(ctx context.Context, r *http.Request, p auth.Principal, in PatchFileInput) (FileSummary, error)
	BatchCommit(ctx context.Context, r *http.Request, p auth.Principal, in BatchCommitInput) (storage.BatchCommitResult, error)
	DeleteFile(ctx context.Context, r *http.Request, p auth.Principal, in DeleteFileInput) (DeleteFileResult, error)
	SetFileVisibility(ctx context.Context, r *http.Request, p auth.Principal, in SetVisibilityInput) (FileSummary, error)
	AcquireLease(ctx context.Context, p auth.Principal, in AcquireLeaseInput) (storage.FileLease, error)
	ReleaseLease(ctx context.Context, p auth.Principal, in ReleaseLeaseInput) error
	GetFileVersions(ctx context.Context, p auth.Principal, in FileLocatorInput) (FileVersionsResult, error)
	RestoreFileVersion(ctx context.Context, r *http.Request, p auth.Principal, in RestoreVersionInput) (FileSummary, error)
	WaitForChanges(ctx context.Context, p auth.Principal, in WaitChangesInput) (WaitChangesResult, error)
	IssueTransferTicket(ctx context.Context, r *http.Request, p auth.Principal, in IssueTicketInput) (IssueTicketResult, error)
	TriggerWorkspaceVerify(ctx context.Context, p auth.Principal, in TriggerVerifyInput) (bridge.VerifyResult, error)

	ListTasks(ctx context.Context, p auth.Principal, in bridge.TaskListFilter) (bridge.TaskListResult, error)
	ClaimTask(ctx context.Context, p auth.Principal, in bridge.ClaimTaskInput) (bridge.AgentTask, error)
	HeartbeatTask(ctx context.Context, p auth.Principal, in HeartbeatTaskInput) (bridge.AgentTask, error)
	SplitTask(ctx context.Context, p auth.Principal, in bridge.SplitTaskInput) (bridge.AgentTask, error)
	PostTaskComment(ctx context.Context, p auth.Principal, in bridge.PostCommentInput) (TaskCommentResult, error)
	CompleteTask(ctx context.Context, p auth.Principal, in bridge.CompleteTaskInput) (bridge.AgentTask, error)
	ReleaseTask(ctx context.Context, p auth.Principal, in bridge.ReleaseTaskInput) (bridge.AgentTask, error)
}
