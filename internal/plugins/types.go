package plugins

import "time"

const (
	RuntimeBuiltin = "builtin"
	RuntimeExec    = "exec"

	PluginOCRVision     = "ocr-vision"
	PluginAutoOrganizer = "auto-organizer"
	PluginPDFToWord     = "pdf-to-word"

	CapOCRImage     = "ocr.image"
	CapAutoClassify = "org.auto_classify"
	CapPDFToDocx    = "doc.pdf2docx"
)

// Manifest defines plugin metadata persisted as <dataDir>/plugins/<id>/plugin.json.
type Manifest struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	Author         string    `json:"author,omitempty"`
	Description    string    `json:"description,omitempty"`
	Capabilities   []string  `json:"capabilities"`
	SupportedExts  []string  `json:"supported_exts"`
	TriggerModes   []string  `json:"trigger_modes,omitempty"` // manual, auto_upload, schedule
	Runtime        string    `json:"runtime"`                 // builtin, exec
	Command        string    `json:"command,omitempty"`
	Args           []string  `json:"args,omitempty"`
	MaxConcurrency int       `json:"max_concurrency"`
	Enabled        bool      `json:"enabled"`
	HasGPU         bool      `json:"has_gpu,omitempty"`
	InstalledAt    time.Time `json:"installed_at"`
}

// CapabilitySummary is advertised by each node in cluster heartbeats.
type CapabilitySummary struct {
	PluginID       string   `json:"plugin_id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Capabilities   []string `json:"capabilities"`
	SupportedExts  []string `json:"supported_exts"`
	TriggerModes   []string `json:"trigger_modes,omitempty"`
	Enabled        bool     `json:"enabled"`
	MaxConcurrency int      `json:"max_concurrency"`
	BusyJobs       int      `json:"busy_jobs"`
	HasGPU         bool     `json:"has_gpu,omitempty"`
}

// MarketplaceItem describes a plugin catalog entry installable onto any cluster node.
type MarketplaceItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Author         string   `json:"author"`
	Description    string   `json:"description"`
	Category       string   `json:"category"`
	Capabilities   []string `json:"capabilities"`
	SupportedExts  []string `json:"supported_exts"`
	TriggerModes   []string `json:"trigger_modes"`
	Runtime        string   `json:"runtime"`
	Requirements   string   `json:"requirements,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	MaxConcurrency int      `json:"max_concurrency"`
}

// ExecRequest describes a single file compute task handed to a plugin runner.
type ExecRequest struct {
	PluginID   string `json:"plugin_id"`
	Capability string `json:"capability"`
	FileName   string `json:"file_name"`
	InputPath  string `json:"input_path"`
	WorkDir    string `json:"work_dir"`
}

// ExecResponse reports the generated output artifact and summary from a plugin execution.
type ExecResponse struct {
	OutputName string         `json:"output_name"`
	OutputPath string         `json:"output_path"`
	Summary    string         `json:"summary"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PluginJob records a distributed plugin compute execution initiated through the Entry scheduler.
type PluginJob struct {
	ID              string    `json:"id"`
	PluginID        string    `json:"plugin_id"`
	Capability      string    `json:"capability"`
	SourceFileID    string    `json:"source_file_id"`
	SourceFileName  string    `json:"source_file_name"`
	SourceNodeID    string    `json:"source_node_id,omitempty"`
	ComputeNodeID   string    `json:"compute_node_id"`
	ComputeNodeName string    `json:"compute_node_name"`
	OutputFileID    string    `json:"output_file_id,omitempty"`
	OutputFileName  string    `json:"output_file_name,omitempty"`
	Status          string    `json:"status"` // done, error
	Summary         string    `json:"summary,omitempty"`
	Error           string    `json:"error,omitempty"`
	DurationMs      int64     `json:"duration_ms"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}
