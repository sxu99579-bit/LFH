import type {
  AgentAvatarId,
  AgentDeviceKind,
  AgentKeyItem,
  AgentPairingItem,
  AgentPresenceItem,
  AgentPresenceSummary,
  AgentScope,
  AgentTaskComment,
  AgentTaskItem,
  AgentTaskPriority,
  AgentTaskStatus,
  AgentTaskSummary,
  AuditEntry,
  BandwidthProbeResult,
  ClusterCapabilityView,
  ClusterResponse,
  DiscoveredNode,
  FileLease,
  FileRecord,
  FileVersion,
  IntegrityStatus,
  InviteCode,
  MarketplaceItem,
  MCPSessionSnapshot,
  MountItem,
  PluginJob,
  PluginRecommendation,
  PoolSummary,
  ReleaseSummary,
  ServerInfo,
  SummaryStats,
  SystemLogEntry,
  TransferTicketResponse,
  UpdateStatus,
  UploadPolicy,
  User,
  VerifyPreset,
  VerifyResult,
  WebhookRuleItem,
  WebhookTestResult,
} from '@/types'
import { getDeviceMeta } from '@/lib/utils'

function safeSetHeader(headers: Headers, key: string, val?: string) {
  const clean = String(val || '').trim()
  if (!clean) return
  try {
    headers.set(key, encodeURIComponent(clean))
  } catch {
    // ignore unsupported non-ASCII header fallback
  }
}

export async function apiFetch<T = Record<string, unknown>>(
  url: string,
  options: RequestInit = {}
): Promise<T> {
  const headers = new Headers(options.headers || {})
  const dev = getDeviceMeta()
  if (!headers.has('X-Device-FP')) safeSetHeader(headers, 'X-Device-FP', dev.fp)
  if (!headers.has('X-Device-Name')) safeSetHeader(headers, 'X-Device-Name', dev.name)
  if (!headers.has('X-Device-Detail')) safeSetHeader(headers, 'X-Device-Detail', dev.detail)

  const res = await fetch(url, { ...options, headers })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error((data as { error?: string }).error || `请求失败 (${res.status})`)
  }
  return data as T
}

export const api = {
  getInfo: () => apiFetch<ServerInfo>('/api/info'),
  getMe: () => apiFetch<{ user: User }>('/api/me'),
  login: (username: string, password: string) =>
    apiFetch<{ user: User }>('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    }),
  register: (username: string, password: string, invite_code: string) =>
    apiFetch<{ user?: User; pending?: boolean; message?: string }>('/api/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password, invite_code }),
    }),
  logout: () => apiFetch('/api/logout', { method: 'POST' }),
  changePassword: (old_password: string, new_password: string) =>
    apiFetch('/api/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_password, new_password }),
    }),
  getToken: () => apiFetch<{ token: string }>('/api/token'),
  regenerateToken: () => apiFetch<{ token: string }>('/api/token', { method: 'POST' }),
  revokeToken: () => apiFetch<{ ok: boolean }>('/api/token', { method: 'DELETE' }),

  getCluster: () => apiFetch<ClusterResponse>('/api/cluster'),
  getSummary: (params?: URLSearchParams) =>
    apiFetch<SummaryStats>(`/api/summary${params && params.toString() ? `?${params.toString()}` : ''}`),
  getFiles: (params: URLSearchParams) =>
    apiFetch<{ files: FileRecord[] }>(`/api/files${params.toString() ? `?${params.toString()}` : ''}`),
  getSelectableUsers: () => apiFetch<{ users: Array<{ username: string; role: string }> }>('/api/users/selectable'),

  createTxtFile: (payload: {
    name: string
    folder_path: string
    content: string
    allowed_users: string[]
    target_node: string
  }) =>
    apiFetch<{ file: FileRecord }>('/api/files/create-txt', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),

  getFileMeta: (id: string, includePath = true) =>
    apiFetch<{ file: FileRecord; disk_path?: string; source_path?: string; storage_mode?: string; source_missing?: boolean; integrity_status?: string; mime?: string }>(
      `/api/files/${encodeURIComponent(id)}/meta?include_path=${includePath ? '1' : '0'}`
    ),
  previewFile: (id: string) => apiFetch<Record<string, unknown>>(`/api/files/${encodeURIComponent(id)}/preview`),
  previewFileSnippet: (id: string) =>
    apiFetch<{ type?: string; content?: string; lines?: string[]; snippet?: boolean; truncated?: boolean }>(
      `/api/files/${encodeURIComponent(id)}/preview?snippet=1`
    ),
  previewArchiveEntry: (id: string, entryPath: string) =>
    apiFetch<Record<string, unknown>>(`/api/files/${encodeURIComponent(id)}/archive-entry?path=${encodeURIComponent(entryPath)}&mode=preview`),
  extractArchive: (id: string, targetNode: string) => {
    const dev = getDeviceMeta()
    const qs = new URLSearchParams({
      target_node: targetNode,
      device_fp: dev.fp,
      device_name: dev.name,
      device_detail: dev.detail,
    })
    return apiFetch<{ extracted: FileRecord[]; skipped?: string[] }>(`/api/files/${encodeURIComponent(id)}/extract?${qs.toString()}`, {
      method: 'POST',
    })
  },
  editFile: (id: string, content: string, note: string) =>
    apiFetch<{ file: FileRecord }>(`/api/files/${encodeURIComponent(id)}/edit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content, note }),
    }),
  updateFileAccess: (id: string, allowed_users: string[]) =>
    apiFetch(`/api/files/${encodeURIComponent(id)}/access`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ allowed_users }),
    }),
  moveFileToFolder: (id: string, folder_path: string) =>
    apiFetch<{ file: FileRecord }>(`/api/files/${encodeURIComponent(id)}/folder`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ folder_path }),
    }),
  listFolders: () =>
    apiFetch<{ ok: boolean; folders: string[] }>('/api/folders'),
  createFolder: (path: string) =>
    apiFetch<{ ok: boolean; path: string; folders: string[] }>('/api/folders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    }),
  renameFolder: (old_path: string, new_path: string) =>
    apiFetch<{ ok: boolean; path: string; moved: number; folders: string[] }>('/api/folders', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_path, new_path }),
    }),
  deleteFolder: (path: string, mode: 'empty_only' | 'move_to_parent' | 'delete_files' = 'move_to_parent') =>
    apiFetch<{ ok: boolean; affected: number; folders: string[] }>('/api/folders', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, mode }),
    }),
  deleteFile: (id: string) => apiFetch(`/api/files/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  getFileVersions: (id: string) =>
    apiFetch<{ file: FileRecord; current_version: number; versions: FileVersion[] }>(
      `/api/files/${encodeURIComponent(id)}/versions`
    ),
  restoreFileVersion: (id: string, ver: number) =>
    apiFetch(`/api/files/${encodeURIComponent(id)}/versions/${ver}/restore`, { method: 'POST' }),
  deleteFileVersion: (id: string, ver: number) =>
    apiFetch(`/api/files/${encodeURIComponent(id)}/versions/${ver}`, { method: 'DELETE' }),

  // Plugins
  getPluginCapabilities: () => apiFetch<{ capabilities: ClusterCapabilityView[] }>('/api/plugins/capabilities'),
  getPluginJobs: () => apiFetch<{ jobs: PluginJob[] }>('/api/plugins/jobs'),
  runPluginJob: (file_id: string, capability: string, plugin_id: string) =>
    apiFetch<{ ok: boolean; job: PluginJob; output_file?: FileRecord }>('/api/plugins/jobs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file_id, capability, plugin_id }),
    }),
  getAdminMarketplace: () =>
    apiFetch<{
      marketplace: MarketplaceItem[]
      recommendations: PluginRecommendation[]
      capabilities: ClusterCapabilityView[]
      nodes: PoolSummary['nodes']
    }>('/api/admin/plugins/marketplace'),
  installPlugin: (node_id: string, plugin_id: string) =>
    apiFetch<{ ok: boolean; capabilities: ClusterCapabilityView[]; pool: PoolSummary }>('/api/admin/plugins/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id, plugin_id }),
    }),
  togglePlugin: (node_id: string, plugin_id: string, enabled: boolean) =>
    apiFetch<{ ok: boolean; capabilities: ClusterCapabilityView[]; pool: PoolSummary }>('/api/admin/plugins/toggle', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id, plugin_id, enabled }),
    }),
  uninstallPlugin: (nodeID: string, pluginID: string) =>
    apiFetch<{ ok: boolean; capabilities: ClusterCapabilityView[]; pool: PoolSummary }>(
      `/api/admin/plugins/${encodeURIComponent(nodeID)}/${encodeURIComponent(pluginID)}`,
      { method: 'DELETE' }
    ),

  // Admin Settings, Users, Invites, Audit, Integrity
  getAdminSettings: () =>
    apiFetch<{ settings: UploadPolicy & { invite_required: boolean }; pending_count?: number }>('/api/admin/settings'),
  saveAdminSettings: (payload: Record<string, unknown>) =>
    apiFetch<{ settings: UploadPolicy & { invite_required: boolean } }>('/api/admin/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  getUsers: () => apiFetch<{ users: Array<User & { status: string }>; pending_count: number }>('/api/admin/users'),
  approveUser: (username: string) =>
    apiFetch(`/api/admin/users/${encodeURIComponent(username)}/approve`, { method: 'POST' }),
  setUserRole: (username: string, role: 'admin' | 'user') =>
    apiFetch(`/api/admin/users/${encodeURIComponent(username)}/role`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ role }),
    }),
  banUser: (username: string) => apiFetch(`/api/admin/users/${encodeURIComponent(username)}/ban`, { method: 'POST' }),
  unbanUser: (username: string) =>
    apiFetch(`/api/admin/users/${encodeURIComponent(username)}/unban`, { method: 'POST' }),
  deleteUser: (username: string) =>
    apiFetch(`/api/admin/users/${encodeURIComponent(username)}`, { method: 'DELETE' }),

  getInvites: () => apiFetch<{ invites: InviteCode[] }>('/api/admin/invites'),
  createInvite: (payload: { note: string; code: string; max_uses: number; expire_hours: number }) =>
    apiFetch<{ invite: InviteCode }>('/api/admin/invites', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  deleteInvite: (code: string) => apiFetch(`/api/admin/invites/${encodeURIComponent(code)}`, { method: 'DELETE' }),

  getIntegrityStatus: () => apiFetch<IntegrityStatus>('/api/admin/integrity/status'),
  runIntegrityScrub: () =>
    apiFetch<{ result: IntegrityStatus['last_scrub']; corrupted_count: number }>('/api/admin/integrity/scrub', {
      method: 'POST',
    }),
  getAuditLogs: (limit = 100) => apiFetch<{ entries: AuditEntry[] }>(`/api/admin/audit?limit=${limit}`),
  getSystemLogs: (params?: { limit?: number; level?: string; q?: string }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.level && params.level !== 'all') qs.set('level', params.level)
    if (params?.q?.trim()) qs.set('q', params.q.trim())
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return apiFetch<{
      entries: SystemLogEntry[]
      total: number
      counts: Record<string, number>
    }>(`/api/admin/logs${suffix}`)
  },
  clearSystemLogs: () => apiFetch<{ ok: boolean }>('/api/admin/logs/clear', { method: 'POST' }),

  // Cluster governance, zero-copy mounts & bandwidth
  probeBandwidth: () =>
    apiFetch<{ ok: boolean; results: BandwidthProbeResult[]; pool: PoolSummary }>('/api/admin/cluster/probe-bandwidth', {
      method: 'POST',
    }),
  getDiscoveredNodes: () => apiFetch<{ discovered: DiscoveredNode[] }>('/api/admin/cluster/discovered'),
  scanDiscoveredNodes: (subnet = '') =>
    apiFetch<{ discovered: DiscoveredNode[] }>('/api/admin/cluster/discover', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ subnet }),
    }),
  adoptPeer: (payload: {
    address: string
    target_admin_user: string
    target_admin_pass: string
    target_auth_secret: string
    quota_text: string
  }) =>
    apiFetch<{ node: { node_name: string }; pool: PoolSummary }>('/api/admin/cluster/adopt-peer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  joinCluster: (payload: { entry_url: string; cluster_secret: string; quota_text: string }) =>
    apiFetch<{ entry_url: string }>('/api/admin/cluster/join', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  promoteNode: (target_node_id: string) =>
    apiFetch<{ pool: PoolSummary; new_entry_url?: string }>('/api/admin/cluster/promote', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_node_id }),
    }),
  leaveCluster: () => apiFetch('/api/admin/cluster/leave', { method: 'POST' }),
  removeNode: (nodeID: string) =>
    apiFetch<{ pool: PoolSummary }>(`/api/admin/cluster/nodes/${encodeURIComponent(nodeID)}`, { method: 'DELETE' }),
  setNodeQuota: (node_id: string, quota_text: string) =>
    apiFetch<{ pool: PoolSummary }>('/api/admin/cluster/quota', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id, quota_text }),
    }),
  setNodeStorageDir: (node_id: string, storage_dir: string, migrate_existing: boolean) =>
    apiFetch<{ pool: PoolSummary; moved_count?: number }>('/api/admin/cluster/storage-dir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id, storage_dir, migrate_existing }),
    }),
  toggleNodeClipboard: (node_id: string, enabled: boolean) =>
    apiFetch<{ pool: PoolSummary }>('/api/admin/cluster/clipboard', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id, enabled }),
    }),
  saveEndpoint: (payload: { expose_ip: string; expose_ips: string[]; disabled_ips: string[]; port: string }) =>
    apiFetch<ClusterResponse & { entry_url: string; port_changed?: boolean }>('/api/admin/cluster/endpoint', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  upgradeNodes: (payload: { node_id?: string; auto_sync_workers?: boolean }) =>
    apiFetch<{ ok: boolean; pool: PoolSummary; total_targets?: number; synced?: number; results?: unknown[] }>(
      '/api/admin/cluster/upgrade-nodes',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }
    ),
  getMounts: () => apiFetch<{ mounts: MountItem[] }>('/api/admin/mounts'),
  addMount: (payload: {
    node_id: string
    dir_path: string
    alias: string
    allowed_users: string[]
    writable?: boolean
    shadow_backup?: boolean
    ignore_patterns?: string[]
    max_file_bytes?: number
    verify_presets?: VerifyPreset[]
  }) =>
    apiFetch<{ mounts: MountItem[]; pool?: PoolSummary }>('/api/admin/mounts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  updateMount: (
    nodeID: string,
    mountID: string,
    payload: {
      alias?: string
      allowed_users?: string[]
      writable?: boolean
      shadow_backup?: boolean
      ignore_patterns?: string[]
      max_file_bytes?: number
      verify_presets?: VerifyPreset[]
    }
  ) =>
    apiFetch<{ ok: boolean; mount: MountItem; mounts: MountItem[]; pool?: PoolSummary }>(
      `/api/admin/mounts/${encodeURIComponent(nodeID)}/${encodeURIComponent(mountID)}`,
      {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }
    ),
  syncMount: (nodeID: string, mountID: string) =>
    apiFetch<{ mounts: MountItem[]; pool?: PoolSummary; result?: { added?: number; updated?: number; removed?: number } }>(
      `/api/admin/mounts/${encodeURIComponent(nodeID)}/${encodeURIComponent(mountID)}/sync`,
      { method: 'POST' }
    ),
  removeMount: (nodeID: string, mountID: string) =>
    apiFetch<{ mounts: MountItem[]; pool?: PoolSummary; removed?: number }>(
      `/api/admin/mounts/${encodeURIComponent(nodeID)}/${encodeURIComponent(mountID)}`,
      { method: 'DELETE' }
    ),

  // Agent Keys & Soft Leases
  getAgentKeys: () => apiFetch<{ keys: AgentKeyItem[] }>('/api/agent-keys'),
  createAgentKey: (payload: {
    name: string
    avatar_id?: AgentAvatarId
    display_alias?: string
    device_alias?: string
    device_kind?: AgentDeviceKind
    scopes: AgentScope[]
    read_folders: string[]
    write_folders: string[]
    default_allowed_users: string[]
    allow_set_visibility: boolean
    rate_limit_per_min: number
    expires_hours?: number
  }) =>
    apiFetch<{ ok: boolean; key: AgentKeyItem; token: string }>('/api/agent-keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  updateAgentKey: (
    id: string,
    payload: {
      name?: string
      avatar_id?: AgentAvatarId
      display_alias?: string
      device_alias?: string
      device_kind?: AgentDeviceKind
      scopes?: AgentScope[]
      read_folders?: string[]
      write_folders?: string[]
      default_allowed_users?: string[]
      allow_set_visibility?: boolean
      rate_limit_per_min?: number
      disabled?: boolean
      clear_expires_at?: boolean
    }
  ) =>
    apiFetch<{ ok: boolean; key: AgentKeyItem }>(`/api/agent-keys/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  toggleAgentKey: (id: string, disabled?: boolean) =>
    apiFetch<{ ok: boolean; key: AgentKeyItem }>(`/api/agent-keys/${encodeURIComponent(id)}/toggle`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(disabled === undefined ? {} : { disabled }),
    }),
  deleteAgentKey: (id: string) =>
    apiFetch<{ ok: boolean }>(`/api/agent-keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  getAgentPairings: () =>
    apiFetch<{ ok: boolean; requests: AgentPairingItem[]; pending_count: number }>('/api/agent-pairing'),
  approveAgentPairing: (
    id: string,
    payload: {
      name?: string
      owner_username?: string
      avatar_id?: AgentAvatarId
      display_alias?: string
      device_alias?: string
      device_kind?: AgentDeviceKind
      scopes?: AgentScope[]
      read_folders?: string[]
      write_folders?: string[]
      default_allowed_users?: string[]
      allow_set_visibility?: boolean
      rate_limit_per_min?: number
      expires_hours?: number
    } = {}
  ) =>
    apiFetch<{ ok: boolean; request: AgentPairingItem; key: AgentKeyItem }>(
      `/api/agent-pairing/${encodeURIComponent(id)}/approve`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }
    ),
  rejectAgentPairing: (id: string) =>
    apiFetch<{ ok: boolean; request: AgentPairingItem }>(
      `/api/agent-pairing/${encodeURIComponent(id)}/reject`,
      { method: 'POST' }
    ),
  getAgentSessions: () =>
    apiFetch<{
      ok: boolean
      summary: AgentPresenceSummary
      agents: AgentPresenceItem[]
      sessions: MCPSessionSnapshot[]
    }>('/api/agent-sessions'),
  disconnectAgentSession: (sessionID: string) =>
    apiFetch<{ ok: boolean }>(`/api/agent-sessions/${encodeURIComponent(sessionID)}`, {
      method: 'DELETE',
    }),

  listAgentTasks: (params?: {
    status?: AgentTaskStatus | ''
    assigned_agent?: string
    folder_prefix?: string
    claimable_only?: boolean
    limit?: number
  }) => {
    const qs = new URLSearchParams()
    if (params?.status) qs.set('status', params.status)
    if (params?.assigned_agent) qs.set('assigned_agent', params.assigned_agent)
    if (params?.folder_prefix) qs.set('folder_prefix', params.folder_prefix)
    if (params?.claimable_only) qs.set('claimable_only', '1')
    if (params?.limit) qs.set('limit', String(params.limit))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return apiFetch<{
      ok: boolean
      summary: AgentTaskSummary
      tasks: AgentTaskItem[]
      server_time: string
    }>(`/api/agent-tasks${suffix}`)
  },
  createAgentTask: (payload: {
    title: string
    description?: string
    priority?: AgentTaskPriority
    assigned_agent?: string
    assigned_agent_label?: string
    folder_prefix?: string
    allow_split?: boolean
    scheduled_at?: string
    max_comment_rounds?: number
  }) =>
    apiFetch<{ ok: boolean; task: AgentTaskItem }>('/api/agent-tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  updateAgentTask: (
    id: string,
    payload: {
      title?: string
      description?: string
      priority?: AgentTaskPriority
      assigned_agent?: string
      assigned_agent_label?: string
      folder_prefix?: string
      allow_split?: boolean
      scheduled_at?: string
      clear_schedule?: boolean
      status?: AgentTaskStatus
      max_comment_rounds?: number
    }
  ) =>
    apiFetch<{ ok: boolean; task: AgentTaskItem }>(`/api/agent-tasks/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  deleteAgentTask: (id: string) =>
    apiFetch<{ ok: boolean }>(`/api/agent-tasks/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  activateAgentTask: (id: string) =>
    apiFetch<{ ok: boolean; task: AgentTaskItem }>(`/api/agent-tasks/${encodeURIComponent(id)}/activate`, {
      method: 'POST',
    }),
  reopenAgentTask: (id: string) =>
    apiFetch<{ ok: boolean; task: AgentTaskItem }>(`/api/agent-tasks/${encodeURIComponent(id)}/reopen`, {
      method: 'POST',
    }),
  postAgentTaskComment: (id: string, body: string) =>
    apiFetch<{ ok: boolean; task: AgentTaskItem; comment: AgentTaskComment }>(
      `/api/agent-tasks/${encodeURIComponent(id)}/comments`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ body }),
      }
    ),

  getFileLease: (id: string) =>
    apiFetch<{ locked: boolean; lease: FileLease | null }>(`/api/files/${encodeURIComponent(id)}/lease`),
  releaseFileLease: (id: string, force = false) =>
    apiFetch<{ ok: boolean }>(`/api/files/${encodeURIComponent(id)}/lease`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ force }),
    }),
  issueTicket: (payload: {
    action: 'up' | 'down'
    file_id?: string
    path?: string
    folder_path?: string
    file_name?: string
    allowed_users?: string[]
    max_bytes?: number
    target_node?: string
    ttl_seconds?: number
    prefer_direct?: boolean
  }) =>
    apiFetch<TransferTicketResponse>('/api/tickets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  triggerMountVerify: (mountID: string, presetID: string) =>
    apiFetch<VerifyResult>(
      `/api/mounts/${encodeURIComponent(mountID)}/verify/${encodeURIComponent(presetID)}`,
      { method: 'POST' }
    ),
  getWebhooks: () =>
    apiFetch<{ ok: boolean; webhooks: WebhookRuleItem[] }>('/api/admin/webhooks'),
  createWebhook: (payload: {
    name: string
    folder_prefix: string
    events: string[]
    target_url: string
    secret: string
    enabled?: boolean
    include_download_ticket?: boolean
  }) =>
    apiFetch<{ ok: boolean; webhook: WebhookRuleItem }>('/api/admin/webhooks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  updateWebhook: (
    id: string,
    payload: {
      name?: string
      folder_prefix?: string
      events?: string[]
      target_url?: string
      secret?: string
      enabled?: boolean
      include_download_ticket?: boolean
    }
  ) =>
    apiFetch<{ ok: boolean; webhook: WebhookRuleItem }>(
      `/api/admin/webhooks/${encodeURIComponent(id)}`,
      {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }
    ),
  deleteWebhook: (id: string) =>
    apiFetch<{ ok: boolean }>(`/api/admin/webhooks/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  testWebhook: (id: string) =>
    apiFetch<{ ok: boolean; result: WebhookTestResult }>(
      `/api/admin/webhooks/${encodeURIComponent(id)}/test`,
      { method: 'POST' }
    ),
  importLocal: (payload: { node_id: string; source_path: string; folder_path: string; allowed_users: string[] }) =>
    apiFetch<{ pool?: PoolSummary; imported_count?: number }>('/api/admin/files/import-local', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),

  // Update, Rollback & Two-Tier Reconcile
  getUpdateStatus: () => apiFetch<{ status: UpdateStatus }>('/api/admin/update'),
  checkUpdate: () => apiFetch<{ status: UpdateStatus }>('/api/admin/update/check', { method: 'POST' }),
  applyUpdate: () => apiFetch<{ status: UpdateStatus; pool?: PoolSummary }>('/api/admin/update/apply', { method: 'POST' }),
  saveUpdateConfig: (payload: { repo: string; proxy_url: string; auto_check: boolean }) =>
    apiFetch<{ status: UpdateStatus }>('/api/admin/update/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  getReleases: () =>
    apiFetch<{ releases: ReleaseSummary[]; status: UpdateStatus; pool: PoolSummary }>('/api/admin/update/releases'),
  switchVersion: (target_version: string, sync_cluster = true) =>
    apiFetch<{ ok: boolean; target_version: string; status: UpdateStatus; pool: PoolSummary }>('/api/admin/update/switch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_version, sync_cluster }),
    }),
  reconcileVersions: (strategy: 'auto' | 'highest_node' | 'github_latest') =>
    apiFetch<{ ok: boolean; strategy_used?: string; target_version?: string; message?: string; status?: UpdateStatus; pool: PoolSummary }>(
      '/api/admin/cluster/reconcile-versions',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ strategy }),
      }
    ),
}
