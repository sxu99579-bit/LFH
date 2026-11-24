export type ThemeMode = 'dark' | 'light'

export type NavTab = 'files' | 'tasks' | 'clipboard' | 'plugins' | 'health' | 'settings'

export interface User {
  id: string
  username: string
  role: 'admin' | 'user'
  status?: string
  created_at?: string
}

export interface NetworkAddr {
  url: string
  ip: string
  iface?: string
  tag?: string
  primary?: boolean
  enabled?: boolean
}

export interface NICAdapterInfo {
  ip: string
  iface: string
  tag: string
  url: string
  enabled: boolean
  primary: boolean
}

export interface DriveInfo {
  path: string
  label: string
  default_dir: string
  total_bytes: number
  free_bytes: number
}

export interface CapabilitySummary {
  plugin_id: string
  name: string
  version: string
  capabilities: string[]
  supported_exts: string[]
  enabled: boolean
  max_concurrency: number
  busy_jobs: number
  has_gpu?: boolean
}

export interface NodeStatus {
  node_id: string
  node_name: string
  role: 'entry' | 'worker' | string
  os: string
  arch: string
  version?: string
  http_url: string
  quota_gb: number
  quota_bytes: number
  used_bytes: number
  free_bytes: number
  file_count: number
  storage_dir?: string
  drives?: DriveInfo[]
  cpu_percent: number
  cpu_cores?: number
  cpu_model?: string
  gpu_name?: string
  has_gpu?: boolean
  lan_link_mbps?: number
  lan_bandwidth_mbps?: number
  bandwidth_probed_at?: string
  mem_percent: number
  mem_used_bytes: number
  mem_total_bytes: number
  disk_total_bytes: number
  disk_used_bytes: number
  disk_free_bytes: number
  disk_percent: number
  net_latency_ms: number
  heartbeat_latency_ms: number
  upload_bps: number
  download_bps: number
  total_upload_bytes?: number
  total_download_bytes?: number
  last_sync_at: string
  io_read_errors: number
  io_write_errors: number
  io_total_errors: number
  zero_copy_count?: number
  zero_copy_bytes?: number
  mount_count?: number
  clipboard_enabled: boolean
  plugins?: CapabilitySummary[]
  online: boolean
  is_self: boolean
  last_seen: string
}

export interface PoolSummary {
  entry_url: string
  entry_version?: string
  auto_sync_workers: boolean
  primary_node_id?: string
  version_consistent?: boolean
  mismatched_nodes?: string[]
  highest_node_version?: string
  highest_node_id?: string
  total_nodes: number
  online_nodes: number
  total_quota_bytes: number
  total_used_bytes: number
  total_free_bytes: number
  total_file_count: number
  nodes: NodeStatus[]
}

export interface ServerInfo {
  app_name: string
  version: string
  role: string
  local_url: string
  entry_url: string
  entry_urls?: string[]
  entry_addrs?: NetworkAddr[]
  invite_required: boolean
  is_mobile_ua?: boolean
  upload_policy?: UploadPolicy
  pool?: PoolSummary
}

export interface UploadPolicy {
  max_file_size_mb: number
  allowed_exts?: string[]
  blocked_exts?: string[]
}

export interface FileVersion {
  version: number
  size: number
  sha256?: string
  uploaded_at: string
  uploader: string
  device_fp?: string
  device_name?: string
  note?: string
  update_method?: string
}

export interface FileRecord {
  id: string
  name: string
  ext: string
  category: string
  size: number
  sha256?: string
  uploaded_at: string
  uploader: string
  folder_path?: string
  allowed_users?: string[]
  storage_dir?: string
  storage_mode?: '' | 'copy' | 'hardlink' | 'external_ref' | 'mount' | string
  external_path?: string
  source_missing?: boolean
  mount_id?: string
  device_fp?: string
  device_name?: string
  device_detail?: string
  device_ip?: string
  node_id?: string
  node_name?: string
  node_os?: string
  node_online?: boolean
  version?: number
  version_count?: number
  version_note?: string
  update_method?: string
  versions?: FileVersion[]
}

export interface SummaryBucket {
  key: string
  label: string
  sub?: string
  count: number
  size: number
}

export interface SummaryStats {
  total_count: number
  total_size: number
  by_time: SummaryBucket[]
  by_category: SummaryBucket[]
  by_device: SummaryBucket[]
}

export interface ClusterResponse {
  pool: PoolSummary
  cluster_secret?: string
  port?: string
  expose_ip?: string
  expose_ips?: string[]
  disabled_ips?: string[]
  available_addrs?: NICAdapterInfo[]
  entry_addrs?: NetworkAddr[]
  events_version?: number
}

export interface DiscoveredNode {
  node_id: string
  node_name: string
  role?: string
  os?: string
  arch?: string
  version?: string
  http_url: string
  entry_url?: string
  already_in_pool?: boolean
  last_seen?: string
}

export interface VerifyPreset {
  id: string
  label: string
  work_sub_dir?: string
  command: string
  args?: string[]
  timeout_sec?: number
  log_rel_path?: string
}

export interface VerifyResult {
  ok: boolean
  mount_id: string
  mount_alias: string
  preset_id: string
  label: string
  exit_code: number
  timed_out?: boolean
  duration_ms: number
  tail_output: string
  truncated?: boolean
  log_path?: string
  coalesced?: boolean
  finished_at: string
}

export type WebhookEventType =
  | 'file.uploaded'
  | 'file.updated'
  | 'file.batch_commit'
  | 'file.deleted'
  | 'verify.completed'
  | '*'

export interface WebhookRuleItem {
  id: string
  name: string
  folder_prefix: string
  events: string[]
  target_url: string
  has_secret: boolean
  enabled: boolean
  include_download_ticket?: boolean
  created_by?: string
  created_at: string
  updated_at: string
  last_status?: string
  last_error?: string
  last_sent_at?: string | null
}

export interface WebhookTestResult {
  ok: boolean
  status_code?: number
  duration_ms: number
  error?: string
}

export interface TransferTicketResponse {
  ok: boolean
  action: 'up' | 'down'
  node_id: string
  node_name: string
  direct_url?: string
  entry_url: string
  recommended_url: string
  expires_at: string
  max_bytes?: number
  single_use: boolean
  method: string
}

export interface MountItem {
  id: string
  alias: string
  dir_path: string
  node_id: string
  node_name?: string
  is_self?: boolean
  status?: string
  file_count?: number
  total_bytes?: number
  last_sync_at?: string
  last_error?: string
  allowed_users?: string[]
  writable?: boolean
  shadow_backup?: boolean
  ignore_patterns?: string[]
  max_file_bytes?: number
  verify_presets?: VerifyPreset[]
}

export type AgentScope = 'read' | 'write' | 'share' | 'verify' | 'delete'

export type AgentAvatarId =
  | 'shiba'
  | 'bluecat'
  | 'panda'
  | 'owl'
  | 'bunny'
  | 'fox'
  | 'penguin'
  | 'mecha'

export type AgentDeviceKind = 'laptop' | 'desktop' | 'mac' | 'server'

export interface AgentKeyItem {
  id: string
  name: string
  owner_username: string
  token_prefix: string
  avatar_id?: AgentAvatarId
  display_alias?: string
  device_alias?: string
  device_kind?: AgentDeviceKind
  scopes: AgentScope[]
  read_folders?: string[]
  write_folders?: string[]
  default_allowed_users?: string[]
  allow_set_visibility: boolean
  rate_limit_per_min: number
  disabled?: boolean
  expires_at?: string | null
  created_at: string
  last_used_at?: string | null
  last_used_ip?: string
}

export interface AgentPairingItem {
  id: string
  confirm_code: string
  name: string
  client_type: string
  client_version?: string
  hostname?: string
  os?: string
  device_kind?: AgentDeviceKind
  source_ip?: string
  target_owner?: string
  requested_scopes: AgentScope[]
  requested_read_folders?: string[]
  requested_write_folders?: string[]
  approved_scopes?: AgentScope[]
  approved_read_folders?: string[]
  approved_write_folders?: string[]
  note?: string
  status: 'pending' | 'approved' | 'claimed' | 'rejected' | 'expired'
  created_at: string
  expires_at: string
  resolved_at?: string | null
  approved_by?: string
  approved_key_id?: string
}

export interface AgentLeaseView {
  file_id: string
  file_name?: string
  path?: string
  folder_path?: string
  expires_at: string
}

export interface MCPSessionSnapshot {
  session_id: string
  owner: string
  username: string
  is_agent: boolean
  agent_key_id?: string
  agent_name?: string
  client_name?: string
  client_version?: string
  client_ip?: string
  protocol_version?: string
  status: 'working' | 'watching' | 'online' | 'idle' | string
  sse_connected: boolean
  active_tool?: string
  active_target?: string
  active_since?: string | null
  reported_state?: string
  reported_task?: string
  reported_progress?: string
  reported_at?: string | null
  last_tool?: string
  last_target?: string
  last_tool_ok: boolean
  tool_calls: number
  created_at: string
  last_seen: string
}

export interface AgentPresenceItem {
  key: AgentKeyItem
  online: boolean
  status: 'working' | 'watching' | 'online' | 'offline' | string
  client_name?: string
  client_version?: string
  client_ip?: string
  protocol_version?: string
  sse_connected: boolean
  active_tool?: string
  active_target?: string
  active_since?: string | null
  reported_state?: string
  reported_task?: string
  reported_progress?: string
  reported_at?: string | null
  last_tool?: string
  last_target?: string
  last_tool_ok: boolean
  tool_calls: number
  session_ids?: string[]
  active_leases?: AgentLeaseView[]
  last_seen?: string | null
}

export interface AgentPresenceSummary {
  total_agents: number
  online_agents: number
  working_agents: number
  watching_agents: number
  active_sessions: number
  pending_pairings: number
}

export interface FileLease {
  file_id: string
  holder_key_id: string
  holder_name: string
  acquired_at: string
  expires_at: string
}

export interface MarketplaceItem {
  id: string
  name: string
  version: string
  author: string
  description: string
  category: string
  capabilities: string[]
  supported_exts: string[]
  trigger_modes?: string[]
  runtime: string
  requirements: string
  tags?: string[]
  max_concurrency: number
}

export interface NodePluginFit {
  node_id: string
  node_name: string
  online: boolean
  is_self: boolean
  installed: boolean
  enabled: boolean
  score: number
  level: 'recommended' | 'compatible' | 'limited' | string
  can_install: boolean
  cpu_cores: number
  cpu_model?: string
  cpu_percent: number
  mem_total_bytes: number
  mem_free_bytes: number
  mem_percent: number
  disk_free_bytes: number
  gpu_name?: string
  has_gpu: boolean
  lan_link_mbps?: number
  lan_bandwidth_mbps?: number
  reasons: string[]
}

export interface PluginRecommendation {
  plugin_id: string
  best_node_id?: string
  best_node_name?: string
  best_score: number
  best_level: string
  best_reason: string
  node_fits: NodePluginFit[]
}

export interface ClusterCapabilityProvider {
  node_id: string
  node_name: string
  is_self: boolean
  busy_jobs: number
  max_concurrency: number
  cpu_percent: number
  has_gpu: boolean
}

export interface ClusterCapabilityView {
  capability: string
  plugin_id: string
  name: string
  version: string
  supported_exts: string[]
  trigger_modes: string[]
  providers: ClusterCapabilityProvider[]
}

export interface PluginJob {
  id: string
  plugin_id: string
  capability: string
  source_file_id: string
  source_file_name: string
  source_node_id?: string
  compute_node_id: string
  compute_node_name: string
  output_file_id?: string
  output_file_name?: string
  status: string
  summary?: string
  error?: string
  duration_ms: number
  created_by: string
  created_at: string
}

export interface UpdateStatus {
  current_version: string
  latest_version: string
  has_update: boolean
  repo: string
  proxy_url: string
  auto_check: boolean
  release_url?: string
  release_notes?: string
  published_at?: string
  asset_name?: string
  asset_size?: number
  asset_url?: string
  checked_at?: string
  state: string
  progress: number
  message?: string
  last_error?: string
  os: string
  arch: string
}

export interface ReleaseSummary {
  tag_name: string
  name: string
  published_at?: string
  release_url?: string
  release_notes?: string
  prerelease: boolean
  is_current: boolean
  relation: 'current' | 'newer' | 'older' | string
  has_asset: boolean
  asset_name?: string
  asset_size?: number
}

export interface InviteCode {
  code: string
  note?: string
  max_uses: number
  used_count: number
  expires_at?: string
  created_by?: string
  created_at: string
  used_by?: string
  used_at?: string
  disabled?: boolean
}

export interface AuditEntry {
  id?: string
  timestamp?: string
  time?: string
  created_at?: string
  actor: string
  action: string
  target?: string
  ip?: string
  status: string
  detail?: string
}

export interface SystemLogEntry {
  seq: number
  timestamp: string
  level: 'INFO' | 'WARN' | 'ERROR' | string
  source: string
  message: string
}

export interface IntegrityStatus {
  corrupted_count: number
  corrupted_ids?: string[]
  last_scrub?: {
    scanned: number
    verified: number
    corrupted: number
    missing: number
    started_at?: string
    finished_at?: string
    duration_ms?: number
  }
}

export interface BandwidthProbeResult {
  node_id: string
  node_name: string
  bandwidth_mbps: number
  link_mbps?: number
  bytes: number
  duration_ms: number
  ok: boolean
  error?: string
}

export interface TransferTask {
  id: string
  kind: 'upload' | 'download'
  name: string
  size: number
  transferred: number
  progress: number
  status: 'queued' | 'running' | 'paused' | 'done' | 'error' | 'cancelled'
  error?: string
  fileId?: string
}

export type AgentTaskStatus = 'scheduled' | 'pending' | 'in_progress' | 'paused' | 'done' | 'cancelled'
export type AgentTaskPriority = 'normal' | 'high' | 'urgent'

export interface AgentSubtaskItem {
  id: string
  seq: number
  title: string
  description?: string
  assigned_agent?: string
  assigned_agent_label?: string
  status: 'pending' | 'claimed' | 'done'
  depends_on?: string[]
  claimed_by_id?: string
  claimed_by_name?: string
  claimed_at?: string | null
  lease_expires_at?: string | null
  completed_at?: string | null
  output_file?: string
  result_summary?: string
}

export interface AgentTaskComment {
  id: string
  author_id?: string
  author_name: string
  author_role: 'agent' | 'user' | 'system'
  mentions?: string[]
  body: string
  created_at: string
}

export interface AgentTaskItem {
  id: string
  title: string
  description?: string
  status: AgentTaskStatus
  priority: AgentTaskPriority
  owner_username: string
  created_by: string
  assigned_agent?: string
  assigned_agent_label?: string
  folder_prefix?: string
  allow_split: boolean
  scheduled_at?: string | null
  claimed_by_id?: string
  claimed_by_name?: string
  claimed_at?: string | null
  lease_expires_at?: string | null
  completed_at?: string | null
  output_files?: string[]
  result_summary?: string
  subtasks?: AgentSubtaskItem[]
  comments?: AgentTaskComment[]
  max_comment_rounds: number
  pause_reason?: string
  created_at: string
  updated_at: string
}

export interface AgentTaskSummary {
  total: number
  scheduled: number
  pending: number
  in_progress: number
  paused: number
  done: number
  claimable_for_me: number
  mentioned_for_me: number
}
