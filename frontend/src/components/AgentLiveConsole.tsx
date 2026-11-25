import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity,
  Bot,
  Check,
  Clock,
  Copy,
  Lock,
  Paintbrush,
  Pencil,
  PlugZap,
  Plus,
  Power,
  Radio,
  RefreshCw,
  ShieldAlert,
  Terminal,
  Trash2,
  Unplug,
  X,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, copyTextToClipboard, formatRelativeTime } from '@/lib/utils'
import type {
  AgentKeyItem,
  AgentPairingItem,
  AgentPresenceItem,
  AgentPresenceSummary,
  AgentScope,
  MountItem,
  User,
} from '@/types'
import {
  AgentAvatarBadge,
  AgentProfileModal,
  DeviceMiniIcon,
  agentDeviceLabel,
  agentDisplayName,
  deviceKindShortLabel,
} from '@/components/AgentAvatar'
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  Input,
  Select,
  Switch,
  Textarea,
  useToast,
} from '@/components/ui'

interface AgentLiveConsoleProps {
  user: User
  mounts: MountItem[]
  selectableUsers: Array<{ username: string; role: string }>
  effectiveBaseUrl: string
  addressOptions: Array<{ label: string; url: string }>
  selectedBaseUrl: string
  customBaseUrl: string
  onChangeSelectedBaseUrl: (url: string) => void
  onChangeCustomBaseUrl: (url: string) => void
  onOpenCreateKey: () => void
  onEditKey: (key: AgentKeyItem) => void
  onToggleKey: (key: AgentKeyItem) => Promise<void>
  onDeleteKey: (key: AgentKeyItem) => Promise<void>
  onSelectKeyForConfig: (keyId: string) => void
  onKeysChanged: () => Promise<void>
  busyKeyId: string
}

type ConnectTab = 'ps1' | 'sh' | 'oauth'
type AgentFilter = 'all' | 'online' | 'working' | 'offline'

const SCOPE_OPTIONS: Array<{ value: AgentScope; label: string; desc: string }> = [
  { value: 'read', label: '读 (read)', desc: '列目录、搜索、读取与监听变更' },
  { value: 'write', label: '写 (write)', desc: '写入、补丁、batch_commit、租约与回滚' },
  { value: 'share', label: '分享 (share)', desc: '调整 allowed_users 可见成员' },
  { value: 'verify', label: '校验 (verify)', desc: '触发预置工作区构建或测试' },
  { value: 'delete', label: '删 (delete)', desc: '删除文件与历史版本（高危）' },
]

const SCOPE_SHORT: Record<AgentScope, string> = {
  read: '读',
  write: '写',
  share: '分享',
  verify: '校验',
  delete: '删',
}

function parseFolderRules(raw: string): string[] {
  return Array.from(
    new Set(
      raw
        .split(/[\n,;]+/)
        .map((s) => s.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, ''))
        .filter(Boolean)
    )
  )
}

function appendFolderRule(raw: string, item: string): string {
  const clean = item.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  if (!clean) return raw
  const list = parseFolderRules(raw)
  if (!list.includes(clean)) list.push(clean)
  return list.join(', ')
}

function statusBadgeInfo(status: string): {
  label: string
  variant: 'success' | 'default' | 'secondary' | 'outline'
  dotClass: string
} {
  switch (status) {
    case 'working':
      return { label: '工作中', variant: 'success', dotClass: 'bg-emerald-500 animate-pulse' }
    case 'watching':
      return { label: '监听中', variant: 'default', dotClass: 'bg-cyan-500' }
    case 'online':
      return { label: '在线空闲', variant: 'secondary', dotClass: 'bg-sky-500' }
    default:
      return { label: '离线', variant: 'outline', dotClass: 'bg-muted-foreground/40' }
  }
}

export function AgentLiveConsole({
  user,
  mounts,
  selectableUsers,
  effectiveBaseUrl,
  addressOptions,
  selectedBaseUrl,
  customBaseUrl,
  onChangeSelectedBaseUrl,
  onChangeCustomBaseUrl,
  onOpenCreateKey,
  onEditKey,
  onToggleKey,
  onDeleteKey,
  onSelectKeyForConfig,
  onKeysChanged,
  busyKeyId,
}: AgentLiveConsoleProps) {
  const { toast } = useToast()

  const [loading, setLoading] = useState(false)
  const [summary, setSummary] = useState<AgentPresenceSummary>({
    total_agents: 0,
    online_agents: 0,
    working_agents: 0,
    watching_agents: 0,
    active_sessions: 0,
    pending_pairings: 0,
  })
  const [agents, setAgents] = useState<AgentPresenceItem[]>([])
  const [pairings, setPairings] = useState<AgentPairingItem[]>([])
  const [statusFilter, setStatusFilter] = useState<AgentFilter>('all')
  const [busyActionId, setBusyActionId] = useState('')

  // One-click onboarding tab state
  const [connectTab, setConnectTab] = useState<ConnectTab>(() => {
    if (typeof navigator !== 'undefined' && /win/i.test(navigator.userAgent)) {
      return 'ps1'
    }
    return 'sh'
  })
  const [targetClient, setTargetClient] = useState<'auto' | 'cursor' | 'antigravity' | 'claude_code' | 'claude_desktop'>('auto')

  // Custom pairing approval dialog state
  const [editingPairing, setEditingPairing] = useState<AgentPairingItem | null>(null)
  const [appName, setAppName] = useState('')
  const [appScopes, setAppScopes] = useState<AgentScope[]>(['read', 'write'])
  const [appReadFolders, setAppReadFolders] = useState('*')
  const [appWriteFolders, setAppWriteFolders] = useState('')
  const [appAllowVis, setAppAllowVis] = useState(false)
  const [appDefaultUsers, setAppDefaultUsers] = useState<string[]>([])
  const [appRateLimit, setAppRateLimit] = useState('180')
  const [appExpireHours, setAppExpireHours] = useState('0')
  const [submittingApprove, setSubmittingApprove] = useState(false)
  const [profileKey, setProfileKey] = useState<AgentKeyItem | null>(null)

  const loadLiveState = useCallback(async () => {
    setLoading(true)
    try {
      const [sessRes, pairRes] = await Promise.all([
        api.getAgentSessions(),
        api.getAgentPairings(),
      ])
      setSummary(sessRes.summary)
      setAgents(sessRes.agents || [])
      setPairings(pairRes.requests || [])
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载 Agent 在线状态失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast])

  useEffect(() => {
    void loadLiveState()
    const onAgentEvent = () => {
      void loadLiveState()
    }
    window.addEventListener('lfh:agent-event', onAgentEvent)
    const timer = window.setInterval(() => {
      void loadLiveState()
    }, 5000)
    return () => {
      window.removeEventListener('lfh:agent-event', onAgentEvent)
      window.clearInterval(timer)
    }
  }, [loadLiveState])

  const pendingPairings = useMemo(
    () => pairings.filter((p) => p.status === 'pending'),
    [pairings]
  )

  const filteredAgents = useMemo(() => {
    switch (statusFilter) {
      case 'online':
        return agents.filter((a) => a.online)
      case 'working':
        return agents.filter((a) => a.status === 'working' || a.status === 'watching')
      case 'offline':
        return agents.filter((a) => !a.online)
      default:
        return agents
    }
  }, [agents, statusFilter])

  const scriptQuery = useMemo(() => {
    const qs = new URLSearchParams()
    if (targetClient !== 'auto') qs.set('client', targetClient)
    if (user.username) qs.set('owner', user.username)
    const s = qs.toString()
    return s ? `?${s}` : ''
  }, [targetClient, user.username])

  const connectCommand = useMemo(() => {
    switch (connectTab) {
      case 'ps1':
        return `irm "${effectiveBaseUrl}/agent/connect.ps1${scriptQuery}" | iex`
      case 'sh':
        return `curl -fsSL "${effectiveBaseUrl}/agent/connect.sh${scriptQuery}" | sh`
      case 'oauth':
        return `${effectiveBaseUrl}/mcp`
    }
  }, [connectTab, effectiveBaseUrl, scriptQuery])

  const handleQuickApprove = async (item: AgentPairingItem) => {
    setBusyActionId(item.id)
    try {
      const safeScopes = (item.requested_scopes || ['read', 'write']).filter((s) => s !== 'delete')
      await api.approveAgentPairing(item.id, {
        scopes: safeScopes.length > 0 ? safeScopes : ['read'],
        read_folders:
          item.requested_read_folders && item.requested_read_folders.length > 0
            ? item.requested_read_folders
            : ['*'],
        write_folders: item.requested_write_folders || [],
      })
      toast(`已同意「${item.name}」接入平台`, 'success')
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_pairing', node_name: 'approved' } }))
      await Promise.all([loadLiveState(), onKeysChanged()])
    } catch (err) {
      toast(err instanceof Error ? err.message : '审批同意失败', 'error')
    } finally {
      setBusyActionId('')
    }
  }

  const handleReject = async (item: AgentPairingItem) => {
    setBusyActionId(item.id)
    try {
      await api.rejectAgentPairing(item.id)
      toast(`已拒绝「${item.name}」的接入申请`, 'info')
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_pairing', node_name: 'rejected' } }))
      await loadLiveState()
    } catch (err) {
      toast(err instanceof Error ? err.message : '拒绝申请失败', 'error')
    } finally {
      setBusyActionId('')
    }
  }

  const openApproveDialog = (item: AgentPairingItem) => {
    setEditingPairing(item)
    setAppName(item.name)
    const safe = (item.requested_scopes || ['read', 'write']).filter((s) => s !== 'delete')
    setAppScopes(safe.length > 0 ? safe : ['read'])
    setAppReadFolders(
      item.requested_read_folders && item.requested_read_folders.length > 0
        ? item.requested_read_folders.join(', ')
        : '*'
    )
    setAppWriteFolders((item.requested_write_folders || []).join(', '))
    setAppAllowVis(false)
    setAppDefaultUsers([])
    setAppRateLimit('180')
    setAppExpireHours('0')
  }

  const toggleAppScope = (scope: AgentScope) => {
    setAppScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]
    )
  }

  const submitApproveDialog = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!editingPairing) return
    if (!appName.trim()) {
      toast('请填写 Agent 名称', 'error')
      return
    }
    if (appScopes.length === 0) {
      toast('请至少勾选一项权限作用域', 'error')
      return
    }
    setSubmittingApprove(true)
    try {
      await api.approveAgentPairing(editingPairing.id, {
        name: appName.trim(),
        scopes: appScopes,
        read_folders: parseFolderRules(appReadFolders),
        write_folders: parseFolderRules(appWriteFolders),
        default_allowed_users: appDefaultUsers,
        allow_set_visibility: appAllowVis,
        rate_limit_per_min: Math.max(1, Math.min(6000, Number(appRateLimit || 180))),
        expires_hours: Number(appExpireHours || 0) || undefined,
      })
      toast(`已审批授权「${appName.trim()}」`, 'success')
      setEditingPairing(null)
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_pairing', node_name: 'approved' } }))
      await Promise.all([loadLiveState(), onKeysChanged()])
    } catch (err) {
      toast(err instanceof Error ? err.message : '审批保存失败', 'error')
    } finally {
      setSubmittingApprove(false)
    }
  }

  const handleDisconnectAgent = async (item: AgentPresenceItem) => {
    const sids = item.session_ids || []
    if (sids.length === 0) return
    setBusyActionId(item.key.id)
    try {
      await Promise.all(sids.map((sid) => api.disconnectAgentSession(sid)))
      toast(`已强制断开「${item.key.name}」的 ${sids.length} 个 MCP 会话`, 'success')
      await loadLiveState()
    } catch (err) {
      toast(err instanceof Error ? err.message : '强制断开会话失败', 'error')
    } finally {
      setBusyActionId('')
    }
  }

  return (
    <div className="space-y-5 min-w-0">
      {/* KPI Summary Strip */}
      <div className="grid gap-3 grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-border bg-card p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>待审批接入申请</span>
            <Clock className={cn('h-4 w-4', summary.pending_pairings > 0 ? 'text-amber-500' : 'text-muted-foreground')} />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className={cn('text-2xl font-semibold tabular-nums', summary.pending_pairings > 0 && 'text-amber-500')}>
              {summary.pending_pairings}
            </span>
            <span className="text-[11px] text-muted-foreground">条待你确认</span>
          </div>
        </div>

        <div className="rounded-xl border border-border bg-card p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>在线 Agent 数量</span>
            <Radio className="h-4 w-4 text-primary" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">
              {summary.online_agents}
            </span>
            <span className="text-[11px] text-muted-foreground">/ {summary.total_agents} 已授权密钥</span>
          </div>
        </div>

        <div className="rounded-xl border border-border bg-card p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>正在执行 / 监听</span>
            <Activity className="h-4 w-4 text-emerald-500" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums text-emerald-600 dark:text-emerald-400">
              {summary.working_agents}
            </span>
            <span className="text-[11px] text-muted-foreground">工作中 · {summary.watching_agents} 监听中</span>
          </div>
        </div>

        <div className="rounded-xl border border-border bg-card p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>活跃 MCP 会话</span>
            <Terminal className="h-4 w-4 text-primary" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">{summary.active_sessions}</span>
            <span className="text-[11px] text-muted-foreground">实时连接会话</span>
          </div>
        </div>
      </div>

      {/* Zero-Config One-Click Pairing Card */}
      <Card className="border-primary/30 bg-primary/5">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <PlugZap className="h-4 w-4 text-primary" />
            <span>一行命令零配置接入（自动申请密钥 + 写入客户端）</span>
          </CardTitle>
          <CardDescription>
            在目标电脑终端运行一行命令，脚本将自动识别本机 Cursor / Antigravity / Claude Code 并在此页面弹出申请；你点一次「同意」后立即自动写好配置，全程无需复制密钥
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-xs">
          <div className="grid gap-3 md:grid-cols-3 min-w-0">
            <div className="space-y-1 min-w-0">
              <label className="font-medium block">接入入口地址（局域网 / Tailscale）</label>
              <Select
                value={selectedBaseUrl}
                onChange={(e) => onChangeSelectedBaseUrl(e.target.value)}
                className="w-full h-9 text-xs bg-background"
              >
                {addressOptions.map((opt) => (
                  <option key={opt.url} value={opt.url}>
                    {opt.label}
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-1 min-w-0">
              <label className="font-medium block">自定义域名 / 穿透地址（选填覆盖）</label>
              <Input
                value={customBaseUrl}
                onChange={(e) => onChangeCustomBaseUrl(e.target.value)}
                placeholder="如 https://lfh.example.com"
                className="font-mono text-xs bg-background"
              />
            </div>
            <div className="space-y-1 min-w-0">
              <label className="font-medium block">目标客户端类型</label>
              <Select
                value={targetClient}
                onChange={(e) => setTargetClient(e.target.value as typeof targetClient)}
                className="w-full h-9 text-xs bg-background"
              >
                <option value="auto">自动探测全部已装客户端 (推荐)</option>
                <option value="cursor">仅写入 Cursor</option>
                <option value="antigravity">仅写入 Antigravity</option>
                <option value="claude_code">仅写入 Claude Code CLI</option>
                <option value="claude_desktop">仅写入 Claude Desktop</option>
              </Select>
            </div>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-1.5">
              {(
                [
                  { id: 'ps1', label: 'Windows PowerShell 一行命令' },
                  { id: 'sh', label: 'Linux / macOS 一行命令' },
                  { id: 'oauth', label: '原生 MCP OAuth 2.1 (仅填 URL)' },
                ] as Array<{ id: ConnectTab; label: string }>
              ).map((t) => (
                <Button
                  key={t.id}
                  size="xs"
                  variant={connectTab === t.id ? 'default' : 'outline'}
                  onClick={() => setConnectTab(t.id)}
                >
                  {t.label}
                </Button>
              ))}
            </div>
            <Button
              size="xs"
              onClick={async () => {
                const ok = await copyTextToClipboard(connectCommand)
                toast(ok ? '已复制一键接入指令' : '复制失败，请手动复制', ok ? 'success' : 'error')
              }}
            >
              <Copy className="h-3.5 w-3.5" />
              <span>{connectTab === 'oauth' ? '复制 MCP 地址' : '复制一键命令'}</span>
            </Button>
          </div>

          <div className="rounded-lg border border-border bg-background p-3 font-mono text-xs break-all select-all">
            {connectCommand}
          </div>
          {connectTab === 'oauth' && (
            <div className="text-[11px] text-muted-foreground">
              支持标准 MCP OAuth 2.1 的客户端仅需添加上述 URL（无需 Header 或令牌），连接时会自动唤起浏览器打开 QuailNest 授权页由你点击同意。
            </div>
          )}
        </CardContent>
      </Card>

      {/* Pending Agent Pairing Approval Queue */}
      {pendingPairings.length > 0 && (
        <Card className="border-amber-500/50 bg-amber-500/5">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-amber-600 dark:text-amber-400">
              <ShieldAlert className="h-4 w-4 shrink-0" />
              <span>待审批接入申请 ({pendingPairings.length})</span>
            </CardTitle>
            <CardDescription>
              请核对终端显示的 6 位确认码、来源主机与申请权限；默认不会自动授予删除 (`delete`) 权限
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2.5 text-xs">
            {pendingPairings.map((item) => {
              const wantsDelete = (item.requested_scopes || []).includes('delete')
              return (
                <div
                  key={item.id}
                  className="rounded-lg border border-amber-500/40 bg-background p-3.5 space-y-2"
                >
                  <div className="flex flex-wrap items-start justify-between gap-2">
                    <div className="space-y-1 min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant="warning" className="font-mono text-xs tracking-wider">
                          确认码 {item.confirm_code}
                        </Badge>
                        <span className="font-semibold text-sm">{item.name}</span>
                        <Badge variant="secondary">{item.client_type}</Badge>
                        {item.hostname && <Badge variant="outline">{item.hostname}</Badge>}
                        {item.os && <Badge variant="outline">{item.os}</Badge>}
                        {item.source_ip && (
                          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                            {item.source_ip}
                          </code>
                        )}
                      </div>

                      <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span>申请权限：</span>
                        {(item.requested_scopes || []).map((sc) => (
                          <Badge key={sc} variant={sc === 'delete' ? 'destructive' : 'outline'}>
                            {SCOPE_SHORT[sc] || sc}
                          </Badge>
                        ))}
                        <span>· 申请读目录：</span>
                        <span className="font-mono text-foreground">
                          {(item.requested_read_folders || []).join(', ') || '*'}
                        </span>
                        {(item.requested_write_folders || []).length > 0 && (
                          <>
                            <span>· 写目录：</span>
                            <span className="font-mono text-foreground">
                              {item.requested_write_folders!.join(', ')}
                            </span>
                          </>
                        )}
                      </div>
                      {item.note && (
                        <div className="text-[11px] text-muted-foreground">备注：{item.note}</div>
                      )}
                      {wantsDelete && (
                        <div className="text-[11px] text-amber-600 dark:text-amber-400">
                          安全提示：对方请求包含高危 `delete` 权限，直接点「一键同意」时将自动剥离该权限。
                        </div>
                      )}
                    </div>

                    <div className="flex flex-wrap items-center gap-1.5 shrink-0">
                      <Button
                        size="xs"
                        disabled={busyActionId === item.id}
                        onClick={() => void handleQuickApprove(item)}
                      >
                        <Check className="h-3.5 w-3.5" />
                        <span>一键同意</span>
                      </Button>
                      <Button
                        size="xs"
                        variant="outline"
                        disabled={busyActionId === item.id}
                        onClick={() => openApproveDialog(item)}
                      >
                        <Pencil className="h-3 w-3" />
                        <span>调整权限</span>
                      </Button>
                      <Button
                        size="xs"
                        variant="ghost"
                        className="text-destructive"
                        disabled={busyActionId === item.id}
                        onClick={() => void handleReject(item)}
                      >
                        <X className="h-3.5 w-3.5" />
                        <span>拒绝</span>
                      </Button>
                    </div>
                  </div>
                </div>
              )
            })}
          </CardContent>
        </Card>
      )}

      {/* Connected & Live Agent Status Table */}
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Bot className="h-4 w-4 text-primary" />
              <span>已接入与在线 Agent 实时状态 ({agents.length})</span>
            </CardTitle>
            <CardDescription>
              实时展示每个已授权 Agent 的客户端信息、在线灯、当前正在执行的工具/任务、软租约与调用次数
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {(
              [
                { id: 'all', label: '全部' },
                { id: 'online', label: '在线' },
                { id: 'working', label: '工作中' },
                { id: 'offline', label: '离线' },
              ] as Array<{ id: AgentFilter; label: string }>
            ).map((f) => (
              <Button
                key={f.id}
                size="xs"
                variant={statusFilter === f.id ? 'default' : 'outline'}
                onClick={() => setStatusFilter(f.id)}
              >
                {f.label}
              </Button>
            ))}
            <Button size="xs" variant="outline" onClick={() => void loadLiveState()} disabled={loading}>
              <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
              <span>刷新</span>
            </Button>
            <Button size="xs" onClick={onOpenCreateKey}>
              <Plus className="h-3.5 w-3.5" />
              <span>手动新建密钥</span>
            </Button>
          </div>
        </CardHeader>

        <CardContent className="space-y-2.5 text-xs">
          {filteredAgents.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border p-6 text-center text-muted-foreground space-y-1.5">
              <div>{agents.length === 0 ? '尚未授权任何 Agent 密钥。' : '当前筛选条件下无匹配 Agent。'}</div>
              <div>可直接使用上方「一行命令零配置接入」，或点击「手动新建密钥」。</div>
            </div>
          ) : (
            filteredAgents.map((item) => {
              const k = item.key
              const expired = Boolean(k.expires_at && new Date(k.expires_at).getTime() <= Date.now())
              const stInfo = statusBadgeInfo(k.disabled ? 'offline' : item.status)
              const activeSids = item.session_ids || []
              const busy = busyKeyId === k.id || busyActionId === k.id

              return (
                <div key={k.id} className="rounded-lg border border-border p-3.5 space-y-2">
                  <div className="flex flex-wrap items-start justify-between gap-2">
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <AgentAvatarBadge
                          agentKey={k}
                          onlineStatus={k.disabled ? 'offline' : item.status}
                          size="sm"
                        />
                        <span className="font-semibold text-sm">{agentDisplayName(k)}</span>
                        {k.display_alias?.trim() && (
                          <Badge variant="outline" className="text-[10px]">
                            {k.name}
                          </Badge>
                        )}
                        <Badge variant={stInfo.variant}>{stInfo.label}</Badge>
                        {k.disabled && <Badge variant="outline">密钥已停用</Badge>}
                        {expired && <Badge variant="destructive">已过期</Badge>}
                        <Badge variant="outline" className="gap-1">
                          <DeviceMiniIcon kind={k.device_kind} className="h-3 w-3" />
                          <span>{agentDeviceLabel(k) || deviceKindShortLabel(k.device_kind)}</span>
                        </Badge>
                        {item.client_name && (
                          <Badge variant="secondary">
                            {item.client_name}
                            {item.client_version ? ` ${item.client_version}` : ''}
                          </Badge>
                        )}
                        {item.sse_connected && <Badge variant="outline">SSE 长连</Badge>}
                        {user.role === 'admin' && k.owner_username !== user.username && (
                          <Badge variant="secondary">归属：{k.owner_username}</Badge>
                        )}
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                          {k.token_prefix}
                        </code>
                        {item.client_ip && (
                          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                            {item.client_ip}
                          </code>
                        )}
                      </div>

                      <div className="flex flex-wrap items-center gap-1">
                        {(k.scopes || []).map((sc) => (
                          <Badge key={sc} variant={sc === 'delete' ? 'warning' : 'default'}>
                            {SCOPE_SHORT[sc] || sc}
                          </Badge>
                        ))}
                        {k.allow_set_visibility && <Badge variant="secondary">允许设可见人</Badge>}
                        <Badge variant="outline" className="tabular-nums">
                          {k.rate_limit_per_min || 180}/分
                        </Badge>
                      </div>
                    </div>

                    <div className="flex flex-wrap items-center gap-1 shrink-0">
                      {activeSids.length > 0 && (
                        <Button
                          size="xs"
                          variant="outline"
                          disabled={busy}
                          onClick={() => void handleDisconnectAgent(item)}
                        >
                          <Unplug className="h-3 w-3 text-amber-500" />
                          <span>断开 ({activeSids.length})</span>
                        </Button>
                      )}
                      <Button size="xs" variant="outline" onClick={() => setProfileKey(k)}>
                        <Paintbrush className="h-3 w-3" />
                        <span>换装</span>
                      </Button>
                      <Button size="xs" variant="outline" onClick={() => onSelectKeyForConfig(k.id)}>
                        <Terminal className="h-3 w-3" />
                        <span>配置</span>
                      </Button>
                      <Button size="xs" variant="outline" onClick={() => onEditKey(k)}>
                        <Pencil className="h-3 w-3" />
                        <span>编辑</span>
                      </Button>
                      <Button
                        size="xs"
                        variant="outline"
                        disabled={busy}
                        onClick={() => void onToggleKey(k)}
                      >
                        <Power className="h-3 w-3" />
                        <span>{k.disabled ? '启用' : '停用'}</span>
                      </Button>
                      <Button
                        size="xs"
                        variant="ghost"
                        className="text-destructive"
                        disabled={busy}
                        onClick={() => void onDeleteKey(k)}
                      >
                        <Trash2 className="h-3 w-3" />
                        <span>删除</span>
                      </Button>
                    </div>
                  </div>

                  {/* Live Work Status, Task Report & Soft Leases Strip */}
                  {(item.active_tool ||
                    item.reported_task ||
                    item.last_tool ||
                    (item.active_leases && item.active_leases.length > 0)) && (
                    <div className="rounded-md bg-muted/40 px-2.5 py-2 space-y-1 text-[11px]">
                      {item.active_tool && (
                        <div className="flex flex-wrap items-center gap-1.5 text-emerald-600 dark:text-emerald-400 font-medium">
                          <Activity className="h-3 w-3 shrink-0" />
                          <span>
                            正在执行：{item.active_tool}
                            {item.active_target ? ` -> ${item.active_target}` : ''}
                          </span>
                        </div>
                      )}
                      {item.reported_task && (
                        <div className="flex flex-wrap items-center gap-1.5 text-foreground">
                          <Bot className="h-3 w-3 text-primary shrink-0" />
                          <span>
                            主动汇报任务：{item.reported_task}
                            {item.reported_progress ? ` (${item.reported_progress})` : ''}
                            {item.reported_state ? ` · [${item.reported_state}]` : ''}
                          </span>
                        </div>
                      )}
                      {item.active_leases && item.active_leases.length > 0 && (
                        <div className="flex flex-wrap items-center gap-1.5 text-amber-600 dark:text-amber-400">
                          <Lock className="h-3 w-3 shrink-0" />
                          <span>
                            持有软租约：
                            {item.active_leases
                              .map((l) => l.path || l.file_name || l.file_id)
                              .join('、')}
                          </span>
                        </div>
                      )}
                      {(item.last_tool || item.tool_calls > 0) && (
                        <div className="text-muted-foreground">
                          最近工具：{item.last_tool || '-'}
                          {item.last_target ? ` (${item.last_target})` : ''} · 本期累计调用 {item.tool_calls} 次
                        </div>
                      )}
                    </div>
                  )}

                  <div className="grid gap-1 sm:grid-cols-2 text-[11px] text-muted-foreground pt-1 border-t border-border/60">
                    <div className="truncate">
                      可读目录：
                      <span className="font-mono text-foreground">
                        {(k.read_folders || []).length > 0 ? k.read_folders!.join(', ') : '(仅继承可写目录)'}
                      </span>
                    </div>
                    <div className="truncate">
                      可写目录：
                      <span className="font-mono text-foreground">
                        {(k.write_folders || []).length > 0 ? k.write_folders!.join(', ') : '(只读不可写)'}
                      </span>
                    </div>
                    <div>
                      默认可见成员：
                      {(k.default_allowed_users || []).length > 0
                        ? k.default_allowed_users!.join('、')
                        : '全员可见'}
                    </div>
                    <div className="tabular-nums">
                      最近活跃：
                      {item.last_seen
                        ? `${formatRelativeTime(item.last_seen)}${item.client_ip || k.last_used_ip ? ` (${item.client_ip || k.last_used_ip})` : ''}`
                        : '尚未使用'}
                    </div>
                  </div>
                </div>
              )
            })
          )}
        </CardContent>
      </Card>

      {/* Pairing Approval Permission Editor Dialog */}
      <Dialog
        open={Boolean(editingPairing)}
        onClose={() => setEditingPairing(null)}
        size="lg"
        title={`审批 Agent 接入 — ${editingPairing?.name || ''}`}
        description={
          editingPairing
            ? `确认码 ${editingPairing.confirm_code} · 客户端 ${editingPairing.client_type} · 来源 ${editingPairing.source_ip || '-'}`
            : ''
        }
      >
        <form onSubmit={submitApproveDialog} className="space-y-4 text-xs">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <label className="font-medium">Agent 展示名称 *</label>
              <Input value={appName} onChange={(e) => setAppName(e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="font-medium">限流（次/分）</label>
                <Input
                  type="number"
                  min={1}
                  max={6000}
                  value={appRateLimit}
                  onChange={(e) => setAppRateLimit(e.target.value)}
                />
              </div>
              <div className="space-y-1">
                <label className="font-medium">有效期</label>
                <Select value={appExpireHours} onChange={(e) => setAppExpireHours(e.target.value)} className="h-9 text-xs">
                  <option value="0">永久有效</option>
                  <option value="24">24 小时</option>
                  <option value="168">7 天</option>
                  <option value="720">30 天</option>
                </Select>
              </div>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">授予作用域权限 (Scopes)</label>
            <div className="grid gap-2 sm:grid-cols-2">
              {SCOPE_OPTIONS.map((sc) => {
                const active = appScopes.includes(sc.value)
                return (
                  <button
                    key={sc.value}
                    type="button"
                    onClick={() => toggleAppScope(sc.value)}
                    className={cn(
                      'rounded-lg border p-2.5 text-left transition-colors cursor-pointer',
                      active
                        ? sc.value === 'delete'
                          ? 'border-amber-500/60 bg-amber-500/10'
                          : 'border-primary bg-primary/10'
                        : 'border-border hover:bg-muted/40'
                    )}
                  >
                    <div className="flex items-center justify-between">
                      <span className="font-medium">{sc.label}</span>
                      {active && <Badge variant={sc.value === 'delete' ? 'warning' : 'default'}>已选</Badge>}
                    </div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{sc.desc}</div>
                  </button>
                )
              })}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">可读目录白名单</label>
            <Textarea
              value={appReadFolders}
              onChange={(e) => setAppReadFolders(e.target.value)}
              className="min-h-[56px] font-mono text-xs"
            />
            <div className="flex flex-wrap gap-1.5">
              <Button type="button" size="xs" variant="outline" onClick={() => setAppReadFolders((p) => appendFolderRule(p, '*'))}>
                + 全部目录 (*)
              </Button>
              {mounts.map((m) => (
                <Button
                  key={`ap-rd-${m.node_id}-${m.id}`}
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => setAppReadFolders((p) => appendFolderRule(p, m.alias))}
                >
                  + {m.alias}
                </Button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">可写目录白名单（留空则禁止写入）</label>
            {appScopes.includes('write') && parseFolderRules(appWriteFolders).length === 0 && (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-2.5 py-2 text-[11px] text-amber-700 dark:text-amber-300">
                <span>已勾选写权限 (write)，但可写目录为空，该 Agent 将无法写入任何文件。</span>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => setAppWriteFolders((p) => appendFolderRule(p, '*'))}
                >
                  + 一键允许全部目录 (*)
                </Button>
              </div>
            )}
            <Textarea
              value={appWriteFolders}
              onChange={(e) => setAppWriteFolders(e.target.value)}
              className="min-h-[56px] font-mono text-xs"
            />
            <div className="flex flex-wrap gap-1.5">
              <Button type="button" size="xs" variant="outline" onClick={() => setAppWriteFolders((p) => appendFolderRule(p, '*'))}>
                + 全部目录 (*)
              </Button>
              {mounts
                .filter((m) => m.writable)
                .map((m) => (
                  <Button
                    key={`ap-wr-${m.node_id}-${m.id}`}
                    type="button"
                    size="xs"
                    variant="outline"
                    onClick={() => setAppWriteFolders((p) => appendFolderRule(p, m.alias))}
                  >
                    + {m.alias}
                  </Button>
                ))}
            </div>
          </div>

          <div className="flex items-center justify-between rounded-lg border border-border p-3">
            <div>
              <div className="font-medium">允许该 Agent 自主调整文件可见成员</div>
              <div className="text-[11px] text-muted-foreground">关闭时写入与分享严格锁定为默认成员列表</div>
            </div>
            <Switch checked={appAllowVis} onCheckedChange={setAppAllowVis} />
          </div>

          {selectableUsers.length > 0 && (
            <div className="space-y-1.5">
              <label className="font-medium block">默认可见成员（全不选表示全员可见）</label>
              <div className="flex flex-wrap gap-1.5">
                {selectableUsers.map((u) => {
                  const checked = appDefaultUsers.includes(u.username)
                  return (
                    <Button
                      key={u.username}
                      type="button"
                      size="xs"
                      variant={checked ? 'default' : 'outline'}
                      onClick={() =>
                        setAppDefaultUsers((prev) =>
                          prev.includes(u.username) ? prev.filter((x) => x !== u.username) : [...prev, u.username]
                        )
                      }
                    >
                      {u.username}
                    </Button>
                  )
                })}
              </div>
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <Button type="button" size="sm" variant="outline" onClick={() => setEditingPairing(null)}>
              取消
            </Button>
            <Button type="submit" size="sm" disabled={submittingApprove}>
              {submittingApprove ? '授权中...' : '同意并签发密钥'}
            </Button>
          </div>
        </form>
      </Dialog>

      <AgentProfileModal
        open={Boolean(profileKey)}
        agentKey={profileKey}
        onClose={() => setProfileKey(null)}
        onSaved={() => {
          void loadLiveState()
          void onKeysChanged()
        }}
      />
    </div>
  )
}
