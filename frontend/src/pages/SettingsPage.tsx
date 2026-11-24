import { useCallback, useEffect, useState } from 'react'
import {
  AlertCircle,
  CheckCircle2,
  Copy,
  Download,
  HardDrive,
  History,
  KeyRound,
  Network,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  Smartphone,
  Terminal,
  Trash2,
  UserCheck,
  Users,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, copyTextToClipboard, formatQuotaDisplay, formatSize, formatTime } from '@/lib/utils'
import type {
  AuditEntry,
  ClusterResponse,
  DiscoveredNode,
  IntegrityStatus,
  InviteCode,
  MountItem,
  NodeStatus,
  ReleaseSummary,
  ServerInfo,
  SystemLogEntry,
  ThemeMode,
  UpdateStatus,
  User,
  VerifyPreset,
  VerifyResult,
} from '@/types'
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
import { AgentBridgeTab } from '@/components/AgentBridgeTab'
import { VerifyPresetsEditor } from '@/components/VerifyPresetsEditor'

interface SettingsPageProps {
  user: User
  info: ServerInfo | null
  cluster: ClusterResponse | null
  theme: ThemeMode
  onToggleTheme: () => void
  onRefreshAll: () => Promise<void> | void
  onOpenMountModal: () => void
}

type SettingsTab = 'general' | 'agent' | 'security' | 'users' | 'cluster' | 'update' | 'logs'

export function SettingsPage({
  user,
  info,
  cluster,
  theme,
  onToggleTheme,
  onRefreshAll,
  onOpenMountModal,
}: SettingsPageProps) {
  const { toast } = useToast()
  const isAdmin = user.role === 'admin'
  const [tab, setTab] = useState<SettingsTab>('general')

  // General state
  const [deviceName, setDeviceName] = useState(() => {
    try {
      return localStorage.getItem('lan_filehub_device_name') || ''
    } catch {
      return ''
    }
  })
  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [changingPwd, setChangingPwd] = useState(false)

  // Security & Audit state
  const [inviteRequired, setInviteRequired] = useState(false)
  const [allowedExtsInput, setAllowedExtsInput] = useState('')
  const [blockedExtsInput, setBlockedExtsInput] = useState('')
  const [invites, setInvites] = useState<InviteCode[]>([])
  const [inviteNote, setInviteNote] = useState('')
  const [inviteCustomCode, setInviteCustomCode] = useState('')
  const [inviteMaxUses, setInviteMaxUses] = useState('0')
  const [inviteExpireHours, setInviteExpireHours] = useState('0')
  const [integrity, setIntegrity] = useState<IntegrityStatus | null>(null)
  const [scrubbing, setScrubbing] = useState(false)
  const [auditLogs, setAuditLogs] = useState<AuditEntry[]>([])

  // Users state
  const [users, setUsers] = useState<Array<User & { status: string }>>([])

  // Cluster & NIC & Mounts state
  const [exposeIps, setExposeIps] = useState<string[]>([])
  const [primaryIp, setPrimaryIp] = useState('')
  const [portInput, setPortInput] = useState('8080')
  const [discovered, setDiscovered] = useState<DiscoveredNode[]>([])
  const [scanningLan, setScanningLan] = useState(false)
  const [adoptTarget, setAdoptTarget] = useState<DiscoveredNode | null>(null)
  const [adoptUser, setAdoptUser] = useState('admin')
  const [adoptPass, setAdoptPass] = useState('')
  const [adoptSecret, setAdoptSecret] = useState('')
  const [adoptQuota, setAdoptQuota] = useState('50GB')
  const [joinUrl, setJoinUrl] = useState('')
  const [joinSecret, setJoinSecret] = useState('')
  const [joinQuota, setJoinQuota] = useState('50GB')
  const [mounts, setMounts] = useState<MountItem[]>([])
  const [editingMount, setEditingMount] = useState<MountItem | null>(null)
  const [editMountAlias, setEditMountAlias] = useState('')
  const [editMountWritable, setEditMountWritable] = useState(false)
  const [editMountShadow, setEditMountShadow] = useState(true)
  const [editMountIgnores, setEditMountIgnores] = useState('')
  const [editMountMaxMB, setEditMountMaxMB] = useState('50')
  const [editMountPresets, setEditMountPresets] = useState<VerifyPreset[]>([])
  const [savingMount, setSavingMount] = useState(false)
  const [verifyDialog, setVerifyDialog] = useState<{
    mount: MountItem
    preset: VerifyPreset
    running: boolean
    result: VerifyResult | null
    error: string
  } | null>(null)
  const [quotaDrafts, setQuotaDrafts] = useState<Record<string, string>>({})
  const [storageDrafts, setStorageDrafts] = useState<Record<string, string>>({})
  const [migrateFlags, setMigrateFlags] = useState<Record<string, boolean>>({})

  // Update & Version Rollback state
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null)
  const [releases, setReleases] = useState<ReleaseSummary[]>([])
  const [selectedVersion, setSelectedVersion] = useState('')
  const [repoInput, setRepoInput] = useState('XSJ02022/lan-filehub')
  const [proxyInput, setProxyInput] = useState('')
  const [autoCheck, setAutoCheck] = useState(true)
  const [busyAction, setBusyAction] = useState('')

  // System Logs state
  const [logsSubTab, setLogsSubTab] = useState<'runtime' | 'audit'>('runtime')
  const [runtimeLogs, setRuntimeLogs] = useState<SystemLogEntry[]>([])
  const [logsTotal, setLogsTotal] = useState(0)
  const [logsCounts, setLogsCounts] = useState<Record<string, number>>({})
  const [logLevel, setLogLevel] = useState<'all' | 'INFO' | 'WARN' | 'ERROR'>('all')
  const [logQuery, setLogQuery] = useState('')
  const [logsAutoRefresh, setLogsAutoRefresh] = useState(true)
  const [auditQuery, setAuditQuery] = useState('')

  // Personal API Token & iOS Shortcuts state
  const [apiToken, setApiToken] = useState('')
  const [shortcutBaseUrl, setShortcutBaseUrl] = useState('')

  useEffect(() => {
    void api
      .getToken()
      .then((res) => setApiToken(res.token || ''))
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (!shortcutBaseUrl) {
      const def =
        info?.entry_addrs?.[0]?.url ||
        info?.entry_url ||
        (typeof window !== 'undefined' ? window.location.origin : '')
      if (def) setShortcutBaseUrl(def)
    }
  }, [info, shortcutBaseUrl])

  const pool = cluster?.pool || info?.pool || null
  const nodes = pool?.nodes || []

  const syncClusterDrafts = useCallback((cl: ClusterResponse | null) => {
    if (!cl) return
    const enabledIps =
      cl.expose_ips && cl.expose_ips.length > 0
        ? cl.expose_ips
        : (cl.available_addrs || []).filter((a) => a.enabled).map((a) => a.ip)
    setExposeIps(enabledIps)
    setPrimaryIp(cl.expose_ip || enabledIps[0] || '')
    setPortInput(cl.port || '8080')
    const qMap: Record<string, string> = {}
    const sMap: Record<string, string> = {}
    for (const n of cl.pool?.nodes || []) {
      qMap[n.node_id] = String(n.quota_gb || Math.max(1, Math.round((n.quota_bytes || 0) / (1024 * 1024 * 1024))))
      sMap[n.node_id] = n.storage_dir || ''
    }
    setQuotaDrafts(qMap)
    setStorageDrafts(sMap)
  }, [])

  useEffect(() => {
    syncClusterDrafts(cluster)
  }, [cluster, syncClusterDrafts])

  const loadAdminTab = useCallback(
    async (targetTab: SettingsTab) => {
      if (!isAdmin) return
      try {
        if (targetTab === 'agent') {
          const mntRes = await api.getMounts().catch(() => ({ mounts: [] }))
          setMounts(mntRes.mounts || [])
        } else if (targetTab === 'security') {
          const [st, inv, intg, aud] = await Promise.all([
            api.getAdminSettings(),
            api.getInvites(),
            api.getIntegrityStatus(),
            api.getAuditLogs(100),
          ])
          setInviteRequired(Boolean(st.settings?.invite_required))
          setAllowedExtsInput((st.settings?.allowed_exts || []).join(', '))
          setBlockedExtsInput((st.settings?.blocked_exts || []).join(', '))
          setInvites(inv.invites || [])
          setIntegrity(intg)
          setAuditLogs(aud.entries || [])
        } else if (targetTab === 'users') {
          const res = await api.getUsers()
          setUsers(res.users || [])
        } else if (targetTab === 'cluster') {
          const [discRes, mntRes] = await Promise.all([api.getDiscoveredNodes(), api.getMounts()])
          setDiscovered(discRes.discovered || [])
          setMounts(mntRes.mounts || [])
        } else if (targetTab === 'update') {
          const [updRes, relRes] = await Promise.all([api.getUpdateStatus(), api.getReleases().catch(() => null)])
          setUpdateStatus(updRes.status)
          setRepoInput(updRes.status.repo || 'XSJ02022/lan-filehub')
          setProxyInput(updRes.status.proxy_url || '')
          setAutoCheck(Boolean(updRes.status.auto_check))
          if (relRes && Array.isArray(relRes.releases)) {
            setReleases(relRes.releases)
            if (relRes.releases.length > 0 && !selectedVersion) {
              setSelectedVersion(relRes.releases[0].tag_name)
            }
          }
        } else if (targetTab === 'logs') {
          const [logRes, audRes] = await Promise.all([
            api.getSystemLogs({ limit: 300, level: logLevel, q: logQuery }),
            api.getAuditLogs(200),
          ])
          setRuntimeLogs(logRes.entries || [])
          setLogsTotal(logRes.total || 0)
          setLogsCounts(logRes.counts || {})
          setAuditLogs(audRes.entries || [])
        }
      } catch (err) {
        toast(err instanceof Error ? err.message : '加载配置失败', 'error')
      }
    },
    [isAdmin, logLevel, logQuery, selectedVersion, toast]
  )

  useEffect(() => {
    if (tab !== 'general') {
      void loadAdminTab(tab)
    }
  }, [tab, loadAdminTab])

  useEffect(() => {
    if (!isAdmin || tab !== 'logs' || logsSubTab !== 'runtime' || !logsAutoRefresh) return
    const timer = window.setInterval(() => {
      void api
        .getSystemLogs({ limit: 300, level: logLevel, q: logQuery })
        .then((res) => {
          setRuntimeLogs(res.entries || [])
          setLogsTotal(res.total || 0)
          setLogsCounts(res.counts || {})
        })
        .catch(() => {})
    }, 3000)
    return () => window.clearInterval(timer)
  }, [isAdmin, logLevel, logQuery, logsAutoRefresh, logsSubTab, tab])

  const saveDeviceName = () => {
    try {
      if (deviceName.trim()) localStorage.setItem('lan_filehub_device_name', deviceName.trim())
      else localStorage.removeItem('lan_filehub_device_name')
      toast('设备名称已保存，下次请求自动更新设备标识', 'success')
    } catch {
      toast('本地存储不可用', 'error')
    }
  }

  const handleChangePassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!oldPwd || !newPwd) {
      toast('请填写旧密码和新密码', 'error')
      return
    }
    setChangingPwd(true)
    try {
      await api.changePassword(oldPwd, newPwd)
      toast('密码已修改', 'success')
      setOldPwd('')
      setNewPwd('')
    } catch (err) {
      toast(err instanceof Error ? err.message : '修改密码失败', 'error')
    } finally {
      setChangingPwd(false)
    }
  }

  const parseExtList = (raw: string) =>
    Array.from(
      new Set(
        raw
          .split(/[\s,;]+/)
          .map((s) => s.trim().toLowerCase().replace(/^\.+/, ''))
          .filter(Boolean)
      )
    )

  const handleSaveUploadPolicy = async () => {
    try {
      await api.saveAdminSettings({
        allowed_exts: parseExtList(allowedExtsInput),
        blocked_exts: parseExtList(blockedExtsInput),
      })
      toast('上传后缀策略已保存并生效', 'success')
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存上传策略失败', 'error')
    }
  }

  const handleToggleInviteRequired = async (nextVal: boolean) => {
    setInviteRequired(nextVal)
    try {
      await api.saveAdminSettings({ invite_required: nextVal })
      toast(nextVal ? '已开启强制邀请码注册' : '已关闭强制邀请码（新注册将待审核）', 'success')
      await onRefreshAll()
    } catch (err) {
      setInviteRequired(!nextVal)
      toast(err instanceof Error ? err.message : '保存失败', 'error')
    }
  }

  const handleCreateInvite = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const res = await api.createInvite({
        note: inviteNote.trim(),
        code: inviteCustomCode.trim(),
        max_uses: Number(inviteMaxUses || 0),
        expire_hours: Number(inviteExpireHours || 0),
      })
      toast(`已生成邀请码：${res.invite.code}`, 'success')
      setInviteNote('')
      setInviteCustomCode('')
      await loadAdminTab('security')
    } catch (err) {
      toast(err instanceof Error ? err.message : '创建邀请码失败', 'error')
    }
  }

  const handleRunScrub = async () => {
    setScrubbing(true)
    try {
      const res = await api.runIntegrityScrub()
      toast(
        `巡检完成：验证 ${res.result?.verified ?? 0}，损坏 ${res.corrupted_count ?? 0}`,
        res.corrupted_count > 0 ? 'error' : 'success'
      )
      await loadAdminTab('security')
    } catch (err) {
      toast(err instanceof Error ? err.message : '执行完整性巡检失败', 'error')
    } finally {
      setScrubbing(false)
    }
  }

  const handleSaveEndpoint = async () => {
    const addrs = cluster?.available_addrs || []
    const disabledIps = addrs.map((a) => a.ip).filter((ip) => !exposeIps.includes(ip))
    const finalPrimary = exposeIps.includes(primaryIp) ? primaryIp : exposeIps[0] || ''
    setBusyAction('endpoint')
    try {
      const res = await api.saveEndpoint({
        expose_ip: finalPrimary,
        expose_ips: exposeIps,
        disabled_ips: disabledIps,
        port: portInput.trim() || '8080',
      })
      toast(
        res.port_changed
          ? `端口配置已保存，请访问新入口：${res.entry_url}`
          : `入口配置已更新：${res.entry_url}`,
        'success'
      )
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存入口配置失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleScanLan = async () => {
    setScanningLan(true)
    try {
      const res = await api.scanDiscoveredNodes('')
      setDiscovered(res.discovered || [])
      toast(`扫描完成，发现 ${res.discovered?.length ?? 0} 个局域网节点`, 'success')
    } catch (err) {
      toast(err instanceof Error ? err.message : '扫描失败', 'error')
    } finally {
      setScanningLan(false)
    }
  }

  const handleAdoptSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!adoptTarget) return
    setBusyAction('adopt')
    try {
      const res = await api.adoptPeer({
        address: adoptTarget.http_url,
        target_admin_user: adoptUser.trim() || 'admin',
        target_admin_pass: adoptPass,
        target_auth_secret: adoptSecret.trim(),
        quota_text: adoptQuota.trim() || '50GB',
      })
      toast(`已纳管工作节点：${res.node?.node_name || adoptTarget.node_name}`, 'success')
      setAdoptTarget(null)
      setAdoptPass('')
      setAdoptSecret('')
      await onRefreshAll()
      await loadAdminTab('cluster')
    } catch (err) {
      toast(err instanceof Error ? err.message : '纳管节点失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleJoinCluster = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!joinUrl.trim() || !joinSecret.trim()) {
      toast('请填写入口节点地址与集群密钥', 'error')
      return
    }
    setBusyAction('join')
    try {
      const res = await api.joinCluster({
        entry_url: joinUrl.trim(),
        cluster_secret: joinSecret.trim(),
        quota_text: joinQuota.trim() || '50GB',
      })
      toast(`已加入外部集群：${res.entry_url}`, 'success')
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '加入集群失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleSaveQuota = async (node: NodeStatus) => {
    const gb = quotaDrafts[node.node_id]
    if (!gb || Number(gb) <= 0) {
      toast('请输入大于 0 的 GB 配额', 'error')
      return
    }
    try {
      await api.setNodeQuota(node.node_id, `${gb}GB`)
      toast(`已更新 ${node.node_name} 配额为 ${gb} GB`, 'success')
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '更新配额失败', 'error')
    }
  }

  const handleSaveStorageDir = async (node: NodeStatus) => {
    const dir = (storageDrafts[node.node_id] || '').trim()
    if (!dir) {
      toast('请输入完整存储路径', 'error')
      return
    }
    const migrate = Boolean(migrateFlags[node.node_id])
    if (migrate && !window.confirm(`确定切换 ${node.node_name} 存储路径至 ${dir} 并迁移已有文件吗？`)) return
    setBusyAction(`storage:${node.node_id}`)
    try {
      const res = await api.setNodeStorageDir(node.node_id, dir, migrate)
      toast(
        migrate ? `存储路径已切换并迁移 ${res.moved_count ?? 0} 个文件` : `新上传文件将写入 ${dir}`,
        'success'
      )
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '修改存储路径失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleCheckUpdate = async () => {
    setBusyAction('checkUpdate')
    try {
      const res = await api.checkUpdate()
      setUpdateStatus(res.status)
      const relRes = await api.getReleases().catch(() => null)
      if (relRes?.releases) setReleases(relRes.releases)
      toast(
        res.status.has_update ? `发现新版本 ${res.status.latest_version}` : '当前已是最新版本',
        'success'
      )
    } catch (err) {
      toast(err instanceof Error ? err.message : '检查更新失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleApplyUpdate = async () => {
    if (!window.confirm('确认下载并覆盖更新主节点与在线工作节点程序吗？更新后服务将短暂重启。')) return
    setBusyAction('applyUpdate')
    try {
      const res = await api.applyUpdate()
      setUpdateStatus(res.status)
      toast('已触发系统更新，正在下载并重启集群...', 'success')
    } catch (err) {
      toast(err instanceof Error ? err.message : '应用更新失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleSwitchVersion = async () => {
    if (!selectedVersion) {
      toast('请先加载并选择一个目标发行版标签', 'error')
      return
    }
    if (
      !window.confirm(
        `强约束同步警告：确定将主节点及所有关联工作节点统一切换（升级或退回）到 ${selectedVersion} 吗？`
      )
    ) {
      return
    }
    setBusyAction('switchVersion')
    try {
      const res = await api.switchVersion(selectedVersion, true)
      setUpdateStatus(res.status)
      toast(`已下发全集群切换至 ${res.target_version}，节点即将重启`, 'success')
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '版本切换失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const handleReconcile = async (strategy: 'highest_node' | 'github_latest') => {
    setBusyAction(`reconcile:${strategy}`)
    try {
      const res = await api.reconcileVersions(strategy)
      toast(res.message || `已触发版本同步（目标 ${res.target_version || '-'}）`, 'success')
      await onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '同步集群版本失败', 'error')
    } finally {
      setBusyAction('')
    }
  }

  const allTabs: Array<{ id: SettingsTab; label: string; adminOnly?: boolean }> = [
    { id: 'general', label: '外观与账号' },
    { id: 'agent', label: 'AI 桥梁与 MCP' },
    { id: 'security', label: '安全与邀请', adminOnly: true },
    { id: 'users', label: '用户治理', adminOnly: true },
    { id: 'cluster', label: '集群、多网卡与挂载', adminOnly: true },
    { id: 'update', label: '版本升级与退回', adminOnly: true },
    { id: 'logs', label: '系统日志', adminOnly: true },
  ]
  const tabs = allTabs.filter((t) => !t.adminOnly || isAdmin)

  return (
    <div className="min-w-0 w-full space-y-5">
      <div>
        <h1 className="text-lg sm:text-xl font-semibold tracking-tight">系统设置与集群治理</h1>
        <p className="text-xs text-muted-foreground mt-0.5 break-words">
          管理个人偏好、安全上传策略、分布式多节点拓扑、虚拟挂载及全集群一致版本控制
        </p>
      </div>

      {/* Horizontal Settings Tabs */}
      <div className="flex items-center gap-1.5 overflow-x-auto border-b border-border pb-2">
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setTab(item.id)}
            className={cn(
              'shrink-0 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors cursor-pointer',
              tab === item.id
                ? 'bg-primary text-primary-foreground shadow-xs'
                : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      {/* ==================== TAB: AI BRIDGE & MCP ==================== */}
      {tab === 'agent' && <AgentBridgeTab user={user} info={info} mounts={mounts} />}

      {/* ==================== TAB: GENERAL ==================== */}
      {tab === 'general' && (
        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>界面主题与本机设备名</CardTitle>
              <CardDescription>自定义当前浏览器端界面外观及上传文件时展示的设备标识</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-center justify-between rounded-lg border border-border p-3 text-xs">
                <div>
                  <div className="font-medium">深色模式 (Dark Mode)</div>
                  <div className="text-muted-foreground mt-0.5">当前：{theme === 'dark' ? '深色' : '浅色'}</div>
                </div>
                <Switch checked={theme === 'dark'} onCheckedChange={onToggleTheme} />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-medium">自定义设备别名</label>
                <div className="flex gap-2">
                  <Input
                    value={deviceName}
                    onChange={(e) => setDeviceName(e.target.value)}
                    placeholder="例如：我的主力笔记本 / iPhone 16 Pro"
                  />
                  <Button size="sm" variant="outline" onClick={saveDeviceName}>
                    保存
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <KeyRound className="h-4 w-4 text-primary" />
                <span>修改登录密码</span>
              </CardTitle>
              <CardDescription>当前账号：{user.username}</CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={handleChangePassword} className="space-y-3">
                <Input
                  type="password"
                  value={oldPwd}
                  onChange={(e) => setOldPwd(e.target.value)}
                  placeholder="当前密码"
                />
                <Input
                  type="password"
                  value={newPwd}
                  onChange={(e) => setNewPwd(e.target.value)}
                  placeholder="新密码（至少 6 位）"
                />
                <Button type="submit" size="sm" disabled={changingPwd}>
                  {changingPwd ? '保存中...' : '更新密码'}
                </Button>
              </form>
            </CardContent>
          </Card>

          <Card className="lg:col-span-2 min-w-0 overflow-hidden">
            <CardHeader>
              <CardTitle className="flex flex-wrap items-center gap-2 leading-snug">
                <Smartphone className="h-4 w-4 text-primary shrink-0" />
                <span>iPhone 快捷指令 (Shortcuts) 一键上传 &amp; 主屏幕应用</span>
              </CardTitle>
              <CardDescription className="break-words">
                无需填写任何请求头或表单字段，生成专属令牌链接后在 iPhone 自带「快捷指令」配两步即可从微信/QQ/相册一键直传
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4 text-xs">
              <div className="grid gap-3 md:grid-cols-2 min-w-0">
                <div className="space-y-1.5 min-w-0">
                  <label className="font-medium block">个人访问令牌 (API Token)</label>
                  <div className="flex flex-col sm:flex-row gap-2 min-w-0">
                    <Input
                      readOnly
                      value={apiToken}
                      placeholder="尚未生成，点击下方生成专属令牌"
                      className="min-w-0 w-full flex-1 font-mono text-xs"
                    />
                    <div className="flex flex-wrap items-center gap-1.5 shrink-0">
                      <Button
                        size="sm"
                        disabled={busyAction === 'token'}
                        onClick={async () => {
                          setBusyAction('token')
                          try {
                            const res = await api.regenerateToken()
                            setApiToken(res.token || '')
                            toast('已生成新的个人访问令牌', 'success')
                          } catch (err) {
                            toast(err instanceof Error ? err.message : '生成令牌失败', 'error')
                          } finally {
                            setBusyAction('')
                          }
                        }}
                      >
                        {apiToken ? '重置令牌' : '生成令牌'}
                      </Button>
                      {apiToken && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={async () => {
                            const ok = await copyTextToClipboard(apiToken)
                            toast(ok ? '已复制个人访问令牌' : '复制失败，请长按输入框复制', ok ? 'success' : 'error')
                          }}
                        >
                          <Copy className="h-3.5 w-3.5" />
                          <span>复制</span>
                        </Button>
                      )}
                      {apiToken && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive"
                          onClick={async () => {
                            if (!window.confirm('确定吊销当前令牌？吊销后 iPhone 快捷指令需更换新令牌。')) return
                            await api.revokeToken()
                            setApiToken('')
                            toast('已吊销个人令牌', 'success')
                          }}
                        >
                          吊销
                        </Button>
                      )}
                    </div>
                  </div>
                </div>

                <div className="space-y-1.5 min-w-0">
                  <label className="font-medium block">选择入口网络地址（局域网或 Tailscale）</label>
                  <Select
                    value={shortcutBaseUrl}
                    onChange={(e) => setShortcutBaseUrl(e.target.value)}
                    className="w-full min-w-0 max-w-full h-9 text-xs"
                  >
                    {(info?.entry_addrs && info.entry_addrs.length > 0
                      ? info.entry_addrs.map((a) => ({ label: `${a.tag ? `${a.tag} · ` : ''}${a.url}`, url: a.url }))
                      : [{ label: info?.entry_url || window.location.origin, url: info?.entry_url || window.location.origin }]
                    ).map((opt) => (
                      <option key={opt.url} value={opt.url}>
                        {opt.label}
                      </option>
                    ))}
                  </Select>
                </div>
              </div>

              {apiToken && shortcutBaseUrl && (
                <div className="grid gap-3 md:grid-cols-2 min-w-0">
                  {[
                    {
                      label: '微信 / QQ / 相册通用上传专属 URL',
                      url: `${shortcutBaseUrl.replace(/\/$/, '')}/api/shortcut/upload?token=${encodeURIComponent(apiToken)}`,
                    },
                    {
                      label: '一键同步到「超级剪贴板」专属 URL',
                      url: `${shortcutBaseUrl.replace(/\/$/, '')}/api/shortcut/upload?folder=${encodeURIComponent('超级剪贴板')}&token=${encodeURIComponent(apiToken)}`,
                    },
                  ].map((item) => (
                    <div key={item.label} className="min-w-0 overflow-hidden rounded-lg border border-border bg-muted/30 p-3 space-y-2">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="font-medium leading-snug">{item.label}</span>
                        <Button
                          size="xs"
                          variant="outline"
                          className="shrink-0"
                          onClick={async () => {
                            const ok = await copyTextToClipboard(item.url)
                            toast(
                              ok ? `已复制：${item.label}` : '浏览器限制自动复制，请长按下方链接全选复制',
                              ok ? 'success' : 'error'
                            )
                          }}
                        >
                          <Copy className="h-3 w-3" />
                          <span>复制链接</span>
                        </Button>
                      </div>
                      <div className="rounded-md bg-background/70 px-2.5 py-2 font-mono text-[11px] leading-relaxed text-muted-foreground break-all select-all">
                        {item.url}
                      </div>
                    </div>
                  ))}
                </div>
              )}

              <div className="rounded-lg border border-border bg-muted/20 p-3 space-y-1.5 text-[11px] text-muted-foreground leading-relaxed break-words">
                <div className="font-semibold text-foreground">iPhone 极简配置（仅需 2 步，无需填任何 Header）：</div>
                <div>1. 打开 iPhone「快捷指令」App 新建指令，底部点 i 打开「在共享表单中显示」。</div>
                <div>2. 添加「重复每一项(快捷指令输入)」-&gt; 循环内添加「获取 URL 内容」-&gt; URL 粘贴上方复制的专属链接 -&gt; 方法选 <code className="font-mono text-foreground">POST</code> -&gt; 请求体选 <code className="font-mono text-foreground">文件</code> 并绑定「重复项目」即可。</div>
                <div>3. Safari 打开当前页面点底部分享 -&gt;「添加到主屏幕」，即可全屏启动 PWA 手机端应用。</div>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {/* ==================== TAB: SECURITY & AUDIT ==================== */}
      {tab === 'security' && isAdmin && (
        <div className="space-y-5">
          <div className="grid gap-4 lg:grid-cols-2">
            {/* Registration + Upload Policy */}
            <Card>
              <CardHeader>
                <CardTitle>注册与上传安全策略</CardTitle>
                <CardDescription>控制新用户注册门槛及全集群允许/禁止上传的扩展名白名单/黑名单</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex items-center justify-between rounded-lg border border-border p-3 text-xs">
                  <div>
                    <div className="font-medium">开启邀请码注册</div>
                    <div className="text-muted-foreground mt-0.5">关闭时新注册用户需经管理员审核同意</div>
                  </div>
                  <Switch checked={inviteRequired} onCheckedChange={handleToggleInviteRequired} />
                </div>

                <div className="space-y-2 border-t border-border pt-3 text-xs">
                  <div className="flex flex-wrap gap-1.5">
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => {
                        setAllowedExtsInput('')
                        setBlockedExtsInput('')
                      }}
                    >
                      不限制后缀
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => {
                        setAllowedExtsInput('')
                        setBlockedExtsInput('exe, msi, bat, cmd, com, scr, vbs, ps1, dll, sh')
                      }}
                    >
                      屏蔽可执行脚本
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => {
                        setAllowedExtsInput('pdf, docx, xlsx, pptx, md, txt, jpg, jpeg, png, gif, webp, mp4, zip')
                        setBlockedExtsInput('')
                      }}
                    >
                      仅限办公/媒体
                    </Button>
                  </div>
                  <div>
                    <label className="text-[11px] text-muted-foreground">允许后缀（白名单，逗号分隔）</label>
                    <Input
                      value={allowedExtsInput}
                      onChange={(e) => setAllowedExtsInput(e.target.value)}
                      placeholder="留空代表不设白名单限制"
                      className="mt-1"
                    />
                  </div>
                  <div>
                    <label className="text-[11px] text-muted-foreground">禁止后缀（黑名单，逗号分隔）</label>
                    <Input
                      value={blockedExtsInput}
                      onChange={(e) => setBlockedExtsInput(e.target.value)}
                      placeholder="如：exe, bat, ps1"
                      className="mt-1"
                    />
                  </div>
                  <Button size="sm" onClick={handleSaveUploadPolicy}>
                    保存上传规则
                  </Button>
                </div>
              </CardContent>
            </Card>

            {/* SHA-256 Integrity Scrub */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <ShieldCheck className="h-4 w-4 text-primary" />
                  <span>SHA-256 存储完整性巡检</span>
                </CardTitle>
                <CardDescription>扫描全库托管文件的磁盘实体校验和，及时发现坏道或误删</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid grid-cols-2 gap-2.5 text-xs">
                  <div className="rounded-lg bg-muted/40 p-3">
                    <div className="text-muted-foreground">当前异常文件数</div>
                    <div className="text-xl font-bold tabular-nums mt-0.5">
                      {integrity?.corrupted_count ?? 0}
                    </div>
                  </div>
                  <div className="rounded-lg bg-muted/40 p-3">
                    <div className="text-muted-foreground">最近巡检结果</div>
                    <div className="text-xs font-medium mt-1">
                      {integrity?.last_scrub
                        ? `校验 ${integrity.last_scrub.verified}/${integrity.last_scrub.scanned}`
                        : '尚未巡检'}
                    </div>
                    {integrity?.last_scrub?.finished_at && (
                      <div className="text-[11px] text-muted-foreground mt-0.5">
                        {formatTime(integrity.last_scrub.finished_at)}
                      </div>
                    )}
                  </div>
                </div>
                <Button size="sm" onClick={handleRunScrub} disabled={scrubbing}>
                  <RefreshCw className={cn('h-3.5 w-3.5', scrubbing && 'animate-spin')} />
                  <span>{scrubbing ? '全库校验中...' : '立即执行全盘 SHA-256 巡检'}</span>
                </Button>
              </CardContent>
            </Card>
          </div>

          {/* Invite Codes */}
          <Card>
            <CardHeader>
              <CardTitle>邀请码管理</CardTitle>
              <CardDescription>生成多用途或单次邀请码分配给局域网成员</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <form onSubmit={handleCreateInvite} className="grid gap-2 sm:grid-cols-5">
                <Input
                  value={inviteNote}
                  onChange={(e) => setInviteNote(e.target.value)}
                  placeholder="备注（如：设计部同事）"
                />
                <Input
                  value={inviteCustomCode}
                  onChange={(e) => setInviteCustomCode(e.target.value)}
                  placeholder="自定义邀请码（可留空）"
                />
                <Input
                  type="number"
                  min={0}
                  value={inviteMaxUses}
                  onChange={(e) => setInviteMaxUses(e.target.value)}
                  placeholder="最大次数 (0无限)"
                />
                <Input
                  type="number"
                  min={0}
                  value={inviteExpireHours}
                  onChange={(e) => setInviteExpireHours(e.target.value)}
                  placeholder="有效期小时 (0永久)"
                />
                <Button type="submit" size="sm">
                  <Plus className="h-3.5 w-3.5" />
                  <span>生成邀请码</span>
                </Button>
              </form>

              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="border-b border-border text-muted-foreground">
                    <tr>
                      <th className="py-2 pr-3 font-medium">邀请码</th>
                      <th className="py-2 pr-3 font-medium">备注</th>
                      <th className="py-2 pr-3 font-medium">使用次数</th>
                      <th className="py-2 pr-3 font-medium">过期时间</th>
                      <th className="py-2 text-right font-medium">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {invites.map((inv) => (
                      <tr key={inv.code}>
                        <td className="py-2 pr-3 font-mono font-medium">
                          <span className="mr-2">{inv.code}</span>
                          <Button
                            size="xs"
                            variant="ghost"
                            onClick={async () => {
                              const ok = await copyTextToClipboard(inv.code)
                              toast(ok ? `已复制邀请码 ${inv.code}` : '复制失败', ok ? 'success' : 'error')
                            }}
                          >
                            <Copy className="h-3 w-3" />
                          </Button>
                        </td>
                        <td className="py-2 pr-3 text-muted-foreground">{inv.note || '-'}</td>
                        <td className="py-2 pr-3 tabular-nums">
                          {inv.used_count} / {inv.max_uses === 0 ? '不限' : inv.max_uses}
                        </td>
                        <td className="py-2 pr-3 text-muted-foreground">
                          {inv.expires_at ? formatTime(inv.expires_at) : '永不过期'}
                        </td>
                        <td className="py-2 text-right">
                          <Button
                            size="xs"
                            variant="ghost"
                            className="text-destructive"
                            onClick={async () => {
                              await api.deleteInvite(inv.code)
                              await loadAdminTab('security')
                            }}
                          >
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>

          {/* Security Audit Logs */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle>安全审计日志 (最近 100 条)</CardTitle>
                <CardDescription>记录登录、上传、权限调整、节点与版本变更行为</CardDescription>
              </div>
              <Button size="xs" variant="outline" onClick={() => loadAdminTab('security')}>
                <RefreshCw className="h-3 w-3" />
                <span>刷新</span>
              </Button>
            </CardHeader>
            <CardContent>
              <div className="max-h-72 overflow-auto">
                <table className="w-full text-left text-xs">
                  <thead className="border-b border-border text-muted-foreground sticky top-0 bg-card">
                    <tr>
                      <th className="py-2 pr-3 font-medium">时间</th>
                      <th className="py-2 pr-3 font-medium">操作人</th>
                      <th className="py-2 pr-3 font-medium">动作</th>
                      <th className="py-2 pr-3 font-medium">目标</th>
                      <th className="py-2 pr-3 font-medium">IP</th>
                      <th className="py-2 pr-3 font-medium">状态</th>
                      <th className="py-2 font-medium">详情</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {auditLogs.map((entry, idx) => (
                      <tr key={entry.id || idx}>
                        <td className="py-2 pr-3 whitespace-nowrap text-muted-foreground tabular-nums">
                          {formatTime(entry.timestamp || entry.time || entry.created_at)}
                        </td>
                        <td className="py-2 pr-3">{entry.actor || '-'}</td>
                        <td className="py-2 pr-3 font-mono text-[11px]">{entry.action}</td>
                        <td className="py-2 pr-3 max-w-[140px] truncate">{entry.target || '-'}</td>
                        <td className="py-2 pr-3 font-mono text-[11px]">{entry.ip || '-'}</td>
                        <td className="py-2 pr-3">
                          <Badge variant={entry.status === 'error' || entry.status === 'denied' ? 'destructive' : 'success'}>
                            {entry.status || 'ok'}
                          </Badge>
                        </td>
                        <td className="py-2 max-w-[200px] truncate text-muted-foreground">{entry.detail || '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {/* ==================== TAB: USERS ==================== */}
      {tab === 'users' && isAdmin && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Users className="h-4 w-4 text-primary" />
                <span>成员账号与审核管理</span>
              </CardTitle>
              <CardDescription>审批待审核账号、调整管理员角色或封禁异常用户</CardDescription>
            </div>
            <Button size="xs" variant="outline" onClick={() => loadAdminTab('users')}>
              <RefreshCw className="h-3 w-3" />
              <span>刷新</span>
            </Button>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-border text-muted-foreground">
                  <tr>
                    <th className="py-2 pr-3 font-medium">用户名</th>
                    <th className="py-2 pr-3 font-medium">角色</th>
                    <th className="py-2 pr-3 font-medium">状态</th>
                    <th className="py-2 pr-3 font-medium">创建时间</th>
                    <th className="py-2 text-right font-medium">管理操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {users.map((u) => {
                    const isSelf = u.username === user.username
                    return (
                      <tr key={u.id || u.username}>
                        <td className="py-2.5 pr-3 font-medium">{u.username}</td>
                        <td className="py-2.5 pr-3">
                          <Badge variant={u.role === 'admin' ? 'default' : 'secondary'}>
                            {u.role === 'admin' ? '管理员' : '普通用户'}
                          </Badge>
                        </td>
                        <td className="py-2.5 pr-3">
                          <Badge
                            variant={
                              u.status === 'pending'
                                ? 'warning'
                                : u.status === 'banned'
                                ? 'destructive'
                                : 'success'
                            }
                          >
                            {u.status === 'pending' ? '待审核' : u.status === 'banned' ? '已封禁' : '正常'}
                          </Badge>
                        </td>
                        <td className="py-2.5 pr-3 text-muted-foreground">{formatTime(u.created_at)}</td>
                        <td className="py-2.5 text-right whitespace-nowrap">
                          <div className="inline-flex items-center gap-1">
                            {u.status === 'pending' && (
                              <Button
                                size="xs"
                                onClick={async () => {
                                  await api.approveUser(u.username)
                                  toast(`已同意 ${u.username} 注册`, 'success')
                                  await loadAdminTab('users')
                                }}
                              >
                                <UserCheck className="h-3 w-3" />
                                <span>批准</span>
                              </Button>
                            )}
                            {!isSelf && (
                              <>
                                <Button
                                  size="xs"
                                  variant="outline"
                                  onClick={async () => {
                                    await api.setUserRole(u.username, u.role === 'admin' ? 'user' : 'admin')
                                    await loadAdminTab('users')
                                  }}
                                >
                                  {u.role === 'admin' ? '设为普通' : '设为管理'}
                                </Button>
                                <Button
                                  size="xs"
                                  variant="outline"
                                  onClick={async () => {
                                    if (u.status === 'banned') await api.unbanUser(u.username)
                                    else await api.banUser(u.username)
                                    await loadAdminTab('users')
                                  }}
                                >
                                  {u.status === 'banned' ? '解封' : '封禁'}
                                </Button>
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  className="text-destructive"
                                  onClick={async () => {
                                    if (!window.confirm(`确定彻底删除用户 ${u.username} 吗？`)) return
                                    await api.deleteUser(u.username)
                                    await loadAdminTab('users')
                                  }}
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </>
                            )}
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      )}

      {/* ==================== TAB: CLUSTER, NIC & MOUNTS ==================== */}
      {tab === 'cluster' && isAdmin && (
        <div className="space-y-5">
          <div className="grid gap-4 lg:grid-cols-2">
            {/* Multi-NIC Endpoint Config */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <Network className="h-4 w-4 text-primary" />
                  <span>多网卡入口地址与集群密钥</span>
                </CardTitle>
                <CardDescription>勾选需要对外广播/开放的局域网网卡 IP 并设置监听端口</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4 text-xs">
                <div className="space-y-2">
                  {(cluster?.available_addrs || []).map((nic) => {
                    const checked = exposeIps.includes(nic.ip)
                    return (
                      <div
                        key={nic.ip}
                        className="flex items-center justify-between gap-2 rounded-lg border border-border px-3 py-2"
                      >
                        <label className="flex items-center gap-2 cursor-pointer min-w-0">
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={(e) => {
                              if (e.target.checked) setExposeIps((prev) => Array.from(new Set([...prev, nic.ip])))
                              else setExposeIps((prev) => prev.filter((x) => x !== nic.ip))
                            }}
                          />
                          <div className="min-w-0">
                            <div className="font-mono font-medium truncate">
                              {nic.ip} <span className="text-muted-foreground">({nic.tag || nic.iface})</span>
                            </div>
                          </div>
                        </label>
                        {checked && (
                          <button
                            type="button"
                            onClick={() => setPrimaryIp(nic.ip)}
                            className={cn(
                              'rounded px-2 py-0.5 text-[11px] border cursor-pointer shrink-0',
                              primaryIp === nic.ip
                                ? 'border-primary bg-primary/15 text-primary font-medium'
                                : 'border-border text-muted-foreground'
                            )}
                          >
                            {primaryIp === nic.ip ? '默认主地址' : '设为主地址'}
                          </button>
                        )}
                      </div>
                    )
                  })}
                </div>

                <div className="flex items-end gap-2">
                  <div className="flex-1 space-y-1">
                    <label className="text-[11px] text-muted-foreground">服务监听端口</label>
                    <Input value={portInput} onChange={(e) => setPortInput(e.target.value)} placeholder="8080" />
                  </div>
                  <Button size="sm" disabled={busyAction === 'endpoint'} onClick={handleSaveEndpoint}>
                    保存网卡与端口
                  </Button>
                </div>

                {cluster?.cluster_secret && (
                  <div className="flex items-center justify-between rounded-lg bg-muted/50 px-3 py-2">
                    <div className="min-w-0 truncate">
                      <span className="text-muted-foreground mr-2">集群密钥：</span>
                      <code className="font-mono">{cluster.cluster_secret}</code>
                    </div>
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={async () => {
                        const ok = await copyTextToClipboard(cluster.cluster_secret!)
                        toast(ok ? '已复制集群密钥' : '复制失败', ok ? 'success' : 'error')
                      }}
                    >
                      <Copy className="h-3 w-3" />
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>

            {/* LAN Auto Discovery & Join External Cluster */}
            <Card>
              <CardHeader className="flex flex-row items-center justify-between gap-2">
                <div>
                  <CardTitle className="flex items-center gap-2">
                    <Server className="h-4 w-4 text-primary" />
                    <span>局域网发现与主动纳管</span>
                  </CardTitle>
                  <CardDescription>自动扫描同网段 LFH 实例一键纳管，或接入外部主节点</CardDescription>
                </div>
                <Button size="xs" variant="outline" onClick={handleScanLan} disabled={scanningLan}>
                  <Search className={cn('h-3 w-3', scanningLan && 'animate-spin')} />
                  <span>{scanningLan ? '扫描中...' : '扫描局域网'}</span>
                </Button>
              </CardHeader>
              <CardContent className="space-y-4 text-xs">
                {discovered.length > 0 && (
                  <div className="space-y-2 max-h-44 overflow-y-auto">
                    {discovered.map((d) => (
                      <div
                        key={d.node_id || d.http_url}
                        className="flex items-center justify-between gap-2 rounded-lg border border-border p-2.5"
                      >
                        <div className="min-w-0">
                          <div className="font-medium truncate">{d.node_name}</div>
                          <div className="text-[11px] font-mono text-muted-foreground truncate">
                            {d.http_url} · {d.version || '-'}
                          </div>
                        </div>
                        {d.already_in_pool ? (
                          <Badge variant="success">已在集群中</Badge>
                        ) : (
                          <Button size="xs" onClick={() => setAdoptTarget(d)}>
                            一键纳管
                          </Button>
                        )}
                      </div>
                    ))}
                  </div>
                )}

                <form onSubmit={handleJoinCluster} className="space-y-2 border-t border-border pt-3">
                  <div className="font-medium">或主动接入指定入口节点：</div>
                  <div className="grid gap-2 sm:grid-cols-3">
                    <Input
                      value={joinUrl}
                      onChange={(e) => setJoinUrl(e.target.value)}
                      placeholder="入口 http://IP:8080"
                    />
                    <Input
                      value={joinSecret}
                      onChange={(e) => setJoinSecret(e.target.value)}
                      placeholder="集群密钥"
                    />
                    <Input
                      value={joinQuota}
                      onChange={(e) => setJoinQuota(e.target.value)}
                      placeholder="贡献配额 (50GB)"
                    />
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button type="submit" size="xs" disabled={busyAction === 'join'}>
                      加入外部集群
                    </Button>
                    {info?.role === 'worker' && (
                      <Button
                        size="xs"
                        variant="destructive"
                        onClick={async () => {
                          if (!window.confirm('确定脱离当前主集群并恢复独立主节点身份吗？')) return
                          await api.leaveCluster()
                          await onRefreshAll()
                        }}
                      >
                        脱离集群恢复独立节点
                      </Button>
                    )}
                  </div>
                </form>
              </CardContent>
            </Card>
          </div>

          {/* Cluster Nodes Management */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle>存储节点配额、路径与主从调度</CardTitle>
                <CardDescription>动态调整每个节点贡献空间、真实存储路径及自动版本追平策略</CardDescription>
              </div>
              <div className="flex items-center gap-2 text-xs">
                <span className="text-muted-foreground">新节点接入自动追平主节点版本</span>
                <Switch
                  checked={Boolean(pool?.auto_sync_workers)}
                  onCheckedChange={async (checked) => {
                    await api.upgradeNodes({ auto_sync_workers: checked })
                    toast(checked ? '已开启从节点自动版本追平' : '已关闭自动版本追平', 'success')
                    await onRefreshAll()
                  }}
                />
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              {nodes.map((n) => (
                <div key={n.node_id} className="rounded-xl border border-border p-3.5 space-y-3 text-xs">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold">{n.node_name}</span>
                      {n.is_self && <Badge variant="default">本机</Badge>}
                      <Badge variant={n.role === 'entry' ? 'default' : 'outline'}>
                        {n.role === 'entry' ? '入口主节点' : '工作节点'}
                      </Badge>
                      <Badge variant={n.online ? 'success' : 'secondary'}>{n.online ? '在线' : '离线'}</Badge>
                      <span className="font-mono text-[11px] text-muted-foreground">{n.version}</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      {!n.is_self && n.role !== 'entry' && (
                        <Button
                          size="xs"
                          variant="outline"
                          onClick={async () => {
                            if (!window.confirm(`确定将 ${n.node_name} 升格为新入口主节点吗？`)) return
                            const res = await api.promoteNode(n.node_id)
                            toast(`已切换入口至 ${res.new_entry_url || n.http_url}`, 'success')
                            await onRefreshAll()
                          }}
                        >
                          升格为主节点
                        </Button>
                      )}
                      {!n.is_self && (
                        <Button
                          size="xs"
                          variant="ghost"
                          className="text-destructive"
                          onClick={async () => {
                            if (!window.confirm(`确定移除节点 ${n.node_name} 吗？`)) return
                            await api.removeNode(n.node_id)
                            await onRefreshAll()
                          }}
                        >
                          移除节点
                        </Button>
                      )}
                    </div>
                  </div>

                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="flex items-end gap-2">
                      <div className="flex-1 space-y-1">
                        <div className="text-[11px] text-muted-foreground">
                          共享空间配额 (GB) · 当前已用 {formatQuotaDisplay(n.used_bytes)}
                        </div>
                        <Input
                          type="number"
                          min={1}
                          value={quotaDrafts[n.node_id] ?? ''}
                          onChange={(e) =>
                            setQuotaDrafts((prev) => ({ ...prev, [n.node_id]: e.target.value }))
                          }
                        />
                      </div>
                      <Button size="sm" variant="outline" onClick={() => handleSaveQuota(n)}>
                        更新配额
                      </Button>
                    </div>

                    <div className="space-y-1">
                      <div className="text-[11px] text-muted-foreground">存储目录路径</div>
                      <div className="flex gap-2">
                        <Input
                          value={storageDrafts[n.node_id] ?? ''}
                          onChange={(e) =>
                            setStorageDrafts((prev) => ({ ...prev, [n.node_id]: e.target.value }))
                          }
                          placeholder="如：E:\LFH-Storage"
                        />
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busyAction === `storage:${n.node_id}`}
                          onClick={() => handleSaveStorageDir(n)}
                        >
                          应用
                        </Button>
                      </div>
                      <label className="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground pt-1 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={Boolean(migrateFlags[n.node_id])}
                          onChange={(e) =>
                            setMigrateFlags((prev) => ({ ...prev, [n.node_id]: e.target.checked }))
                          }
                        />
                        <span>同时迁移该节点已有托管文件</span>
                      </label>
                    </div>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          {/* Virtual Directory Mounts */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle className="flex items-center gap-2">
                  <HardDrive className="h-4 w-4 text-primary" />
                  <span>虚拟目录挂载 ({mounts.length})</span>
                </CardTitle>
                <CardDescription>不复制文件、不占配额，实时索引节点磁盘现有目录并安全共享</CardDescription>
              </div>
              <Button size="xs" onClick={onOpenMountModal}>
                <Plus className="h-3 w-3" />
                <span>新建挂载</span>
              </Button>
            </CardHeader>
            <CardContent>
              {mounts.length === 0 ? (
                <div className="text-xs text-muted-foreground">暂无虚拟挂载目录</div>
              ) : (
                <div className="space-y-2 text-xs">
                  {mounts.map((m) => (
                    <div
                      key={`${m.node_id}:${m.id}`}
                      className="rounded-lg border border-border p-3 space-y-2"
                    >
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-1.5 font-medium">
                            <span>{m.alias}</span>
                            <Badge variant={m.writable ? 'success' : 'secondary'}>
                              {m.writable ? '可写工作区' : '只读挂载'}
                            </Badge>
                            {m.writable && m.shadow_backup && <Badge variant="outline">Shadow 备份</Badge>}
                            {m.ignore_patterns && m.ignore_patterns.length > 0 && (
                              <Badge variant="outline">{m.ignore_patterns.length} 项忽略</Badge>
                            )}
                            {m.verify_presets && m.verify_presets.length > 0 && (
                              <Badge variant="default">{m.verify_presets.length} 项校验预设</Badge>
                            )}
                          </div>
                          <div className="text-[11px] font-mono text-muted-foreground mt-0.5 break-all">
                            {m.dir_path}
                          </div>
                          <div className="text-[11px] text-muted-foreground mt-0.5">
                            节点：{m.node_name || m.node_id} · {m.file_count ?? 0} 个文件 ({formatSize(m.total_bytes)})
                            {m.writable && m.max_file_bytes ? ` · 单文件上限 ${Math.round(m.max_file_bytes / (1024 * 1024))} MB` : ''}
                          </div>
                        </div>
                        <div className="flex items-center gap-1.5">
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={() => {
                              setEditingMount(m)
                              setEditMountAlias(m.alias || '')
                              setEditMountWritable(Boolean(m.writable))
                              setEditMountShadow(m.writable ? Boolean(m.shadow_backup ?? true) : true)
                              setEditMountIgnores(
                                m.ignore_patterns && m.ignore_patterns.length > 0
                                  ? m.ignore_patterns.join(', ')
                                  : 'node_modules, .git, dist, build, .next, .nuxt, target, vendor, __pycache__, .venv, .idea, .vscode, .DS_Store, *.log'
                              )
                              setEditMountMaxMB(
                                String(m.max_file_bytes ? Math.max(1, Math.round(m.max_file_bytes / (1024 * 1024))) : 50)
                              )
                              setEditMountPresets(
                                m.verify_presets
                                  ? m.verify_presets.map((p) => ({
                                      ...p,
                                      args: p.args ? [...p.args] : [],
                                    }))
                                  : []
                              )
                            }}
                          >
                            <Pencil className="h-3 w-3" />
                            <span>配置</span>
                          </Button>
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={async () => {
                              const res = await api.syncMount(m.node_id, m.id)
                              toast(
                                `扫描完成：新增 ${res.result?.added ?? 0}，更新 ${res.result?.updated ?? 0}`,
                                'success'
                              )
                              await loadAdminTab('cluster')
                              await onRefreshAll()
                            }}
                          >
                            立即扫描
                          </Button>
                          <Button
                            size="xs"
                            variant="ghost"
                            className="text-destructive"
                            onClick={async () => {
                              if (!window.confirm(`确定取消挂载 ${m.alias} 吗？（原磁盘文件完整保留）`)) return
                              await api.removeMount(m.node_id, m.id)
                              await loadAdminTab('cluster')
                              await onRefreshAll()
                            }}
                          >
                            取消挂载
                          </Button>
                        </div>
                      </div>
                      {m.verify_presets && m.verify_presets.length > 0 && (
                        <div className="flex flex-wrap items-center gap-1.5 border-t border-border/60 pt-2">
                          <span className="text-[11px] text-muted-foreground">快捷构建校验：</span>
                          {m.verify_presets.map((preset) => (
                            <Button
                              key={preset.id}
                              size="xs"
                              variant="outline"
                              disabled={Boolean(verifyDialog?.running)}
                              onClick={async () => {
                                setVerifyDialog({
                                  mount: m,
                                  preset,
                                  running: true,
                                  result: null,
                                  error: '',
                                })
                                try {
                                  const res = await api.triggerMountVerify(m.id, preset.id)
                                  setVerifyDialog({
                                    mount: m,
                                    preset,
                                    running: false,
                                    result: res,
                                    error: '',
                                  })
                                  toast(
                                    res.ok
                                      ? `校验通过 (${preset.label || preset.id}, ${res.duration_ms}ms)`
                                      : `校验失败 (exit=${res.exit_code}, ${res.duration_ms}ms)`,
                                    res.ok ? 'success' : 'error'
                                  )
                                  void onRefreshAll()
                                } catch (err) {
                                  const msg = err instanceof Error ? err.message : '执行构建校验失败'
                                  setVerifyDialog({
                                    mount: m,
                                    preset,
                                    running: false,
                                    result: null,
                                    error: msg,
                                  })
                                  toast(msg, 'error')
                                }
                              }}
                            >
                              <Play className="h-3 w-3 text-primary" />
                              <span>{preset.label || preset.id}</span>
                            </Button>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* ==================== TAB: UPDATE & VERSION ROLLBACK ==================== */}
      {tab === 'update' && isAdmin && (
        <div className="space-y-5">
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>在线检查与升级</CardTitle>
                <CardDescription>
                  当前运行版本：<code className="font-mono">{updateStatus?.current_version || info?.version}</code> · 平台{' '}
                  {updateStatus?.os}/{updateStatus?.arch}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <Button size="sm" onClick={handleCheckUpdate} disabled={busyAction === 'checkUpdate'}>
                    <RefreshCw className={cn('h-3.5 w-3.5', busyAction === 'checkUpdate' && 'animate-spin')} />
                    <span>检查新版本</span>
                  </Button>
                  {updateStatus?.has_update && (
                    <Button size="sm" variant="secondary" onClick={handleApplyUpdate} disabled={busyAction === 'applyUpdate'}>
                      升级全集群至 {updateStatus.latest_version}
                    </Button>
                  )}
                </div>
                {updateStatus?.latest_version && (
                  <div className="rounded-lg border border-border bg-muted/30 p-3 space-y-1">
                    <div className="flex items-center gap-2 font-medium">
                      <CheckCircle2 className="h-3.5 w-3.5 text-primary" />
                      <span>GitHub 最新发行版：{updateStatus.latest_version}</span>
                    </div>
                    {updateStatus.release_notes && (
                      <div className="text-[11px] text-muted-foreground whitespace-pre-wrap max-h-28 overflow-auto">
                        {updateStatus.release_notes}
                      </div>
                    )}
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Strong-Consistency Version Rollback & Reconcile */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <History className="h-4 w-4 text-primary" />
                  <span>强约束版本指定、退回与集群对齐</span>
                </CardTitle>
                <CardDescription>
                  选择任意历史发行版，主节点与所有关联工作节点将强制同步升级或降级，杜绝混用
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3 text-xs">
                <div className="flex items-center gap-2">
                  <Select
                    value={selectedVersion}
                    onChange={(e) => setSelectedVersion(e.target.value)}
                    className="flex-1 h-9 text-xs"
                  >
                    {releases.length === 0 && <option value="">请先检查或刷新发行版列表</option>}
                    {releases.map((r) => (
                      <option key={r.tag_name} value={r.tag_name}>
                        {r.tag_name} {r.is_current ? '(当前)' : r.relation === 'newer' ? '(较新)' : '(可退回)'}
                      </option>
                    ))}
                  </Select>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={handleSwitchVersion}
                    disabled={!selectedVersion || busyAction === 'switchVersion'}
                  >
                    全集群统一切换
                  </Button>
                </div>

                <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                  <Button
                    size="xs"
                    variant="secondary"
                    disabled={busyAction === 'reconcile:highest_node'}
                    onClick={() => handleReconcile('highest_node')}
                  >
                    同步优先级 1：跟随全网最高节点
                  </Button>
                  <Button
                    size="xs"
                    variant="secondary"
                    disabled={busyAction === 'reconcile:github_latest'}
                    onClick={() => handleReconcile('github_latest')}
                  >
                    同步优先级 2：全网拉取 GitHub 最新版
                  </Button>
                </div>
              </CardContent>
            </Card>
          </div>

          {/* GitHub Repo & Proxy Config */}
          <Card>
            <CardHeader>
              <CardTitle>更新源与镜像代理配置</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-3 text-xs">
              <div className="space-y-1">
                <label className="text-[11px] text-muted-foreground">GitHub 仓库</label>
                <Input value={repoInput} onChange={(e) => setRepoInput(e.target.value)} />
              </div>
              <div className="space-y-1">
                <label className="text-[11px] text-muted-foreground">镜像加速前缀（可选）</label>
                <Input
                  value={proxyInput}
                  onChange={(e) => setProxyInput(e.target.value)}
                  placeholder="例如：https://ghfast.top/"
                />
              </div>
              <div className="flex items-end justify-between gap-2">
                <label className="inline-flex items-center gap-2 cursor-pointer">
                  <Switch checked={autoCheck} onCheckedChange={setAutoCheck} />
                  <span>启动自动检查</span>
                </label>
                <Button
                  size="sm"
                  onClick={async () => {
                    const res = await api.saveUpdateConfig({
                      repo: repoInput.trim(),
                      proxy_url: proxyInput.trim(),
                      auto_check: autoCheck,
                    })
                    setUpdateStatus(res.status)
                    toast('更新源配置已保存', 'success')
                  }}
                >
                  保存配置
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {/* ==================== TAB: SYSTEM LOGS ==================== */}
      {tab === 'logs' && isAdmin && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 rounded-lg border border-border bg-muted/30 p-1 text-xs">
              <button
                type="button"
                onClick={() => setLogsSubTab('runtime')}
                className={cn(
                  'rounded-md px-3 py-1.5 font-medium transition-colors cursor-pointer',
                  logsSubTab === 'runtime' ? 'bg-card text-foreground shadow-2xs' : 'text-muted-foreground'
                )}
              >
                服务运行日志 ({logsTotal})
              </button>
              <button
                type="button"
                onClick={() => setLogsSubTab('audit')}
                className={cn(
                  'rounded-md px-3 py-1.5 font-medium transition-colors cursor-pointer',
                  logsSubTab === 'audit' ? 'bg-card text-foreground shadow-2xs' : 'text-muted-foreground'
                )}
              >
                安全审计日志 ({auditLogs.length})
              </button>
            </div>

            <div className="flex flex-wrap items-center gap-2 text-xs">
              {logsSubTab === 'runtime' && (
                <label className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 cursor-pointer">
                  <Switch checked={logsAutoRefresh} onCheckedChange={setLogsAutoRefresh} />
                  <span>3秒自动刷新</span>
                </label>
              )}
              <Button size="xs" variant="outline" onClick={() => void loadAdminTab('logs')}>
                <RefreshCw className="h-3 w-3" />
                <span>刷新</span>
              </Button>
              <Button
                size="xs"
                variant="outline"
                onClick={() => {
                  const text =
                    logsSubTab === 'runtime'
                      ? runtimeLogs
                          .map((l) => `${formatTime(l.timestamp)} [${l.level}] [${l.source}] ${l.message}`)
                          .join('\n')
                      : JSON.stringify(auditLogs, null, 2)
                  const blob = new Blob([text], {
                    type: logsSubTab === 'runtime' ? 'text/plain;charset=utf-8' : 'application/json;charset=utf-8',
                  })
                  const url = URL.createObjectURL(blob)
                  const a = document.createElement('a')
                  a.href = url
                  a.download = logsSubTab === 'runtime' ? 'lfh-runtime.log' : 'lfh-audit.json'
                  document.body.appendChild(a)
                  a.click()
                  a.remove()
                  URL.revokeObjectURL(url)
                }}
              >
                <Download className="h-3 w-3" />
                <span>导出文件</span>
              </Button>
              {logsSubTab === 'runtime' && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="text-destructive"
                  onClick={async () => {
                    if (!window.confirm('确定清空当前内存运行日志缓冲吗？')) return
                    await api.clearSystemLogs()
                    toast('已清空运行日志', 'success')
                    await loadAdminTab('logs')
                  }}
                >
                  <Trash2 className="h-3 w-3" />
                  <span>清空</span>
                </Button>
              )}
            </div>
          </div>

          {logsSubTab === 'runtime' ? (
            <Card>
              <CardHeader className="pb-3">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <CardTitle className="flex items-center gap-2">
                      <Terminal className="h-4 w-4 text-primary" />
                      <span>节点运行日志流</span>
                    </CardTitle>
                    <CardDescription>实时保留当前节点最近 1000 条 HTTP、集群通信、升级与系统事件日志</CardDescription>
                  </div>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {(['all', 'INFO', 'WARN', 'ERROR'] as const).map((lvl) => (
                      <button
                        key={lvl}
                        type="button"
                        onClick={() => setLogLevel(lvl)}
                        className={cn(
                          'rounded-full border px-2.5 py-0.5 text-[11px] cursor-pointer tabular-nums',
                          logLevel === lvl
                            ? 'border-primary bg-primary text-primary-foreground'
                            : 'border-border text-muted-foreground hover:text-foreground'
                        )}
                      >
                        {lvl === 'all' ? `全部 (${logsTotal})` : `${lvl} (${logsCounts[lvl] || 0})`}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="pt-2">
                  <Input
                    value={logQuery}
                    onChange={(e) => setLogQuery(e.target.value)}
                    placeholder="按关键词或模块筛选（如：http、cluster、error、updater）..."
                    className="h-8 text-xs"
                  />
                </div>
              </CardHeader>
              <CardContent>
                {runtimeLogs.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-border p-8 text-center text-xs text-muted-foreground">
                    当前筛选条件下暂无运行日志记录
                  </div>
                ) : (
                  <div className="max-h-[55vh] overflow-auto rounded-lg border border-border bg-muted/20 divide-y divide-border font-mono text-[11px]">
                    {runtimeLogs.map((entry) => (
                      <div key={entry.seq} className="flex flex-wrap sm:flex-nowrap items-start gap-2 px-3 py-2 hover:bg-muted/35">
                        <span className="shrink-0 text-muted-foreground tabular-nums">
                          {formatTime(entry.timestamp)}
                        </span>
                        <Badge
                          variant={
                            entry.level === 'ERROR'
                              ? 'destructive'
                              : entry.level === 'WARN'
                              ? 'warning'
                              : 'secondary'
                          }
                          className="shrink-0 font-mono text-[10px]"
                        >
                          {entry.level}
                        </Badge>
                        <Badge variant="outline" className="shrink-0 font-mono text-[10px]">
                          {entry.source}
                        </Badge>
                        <span className="min-w-0 break-all text-foreground">{entry.message}</span>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          ) : (
            <Card>
              <CardHeader className="pb-3">
                <CardTitle>全量安全审计日志</CardTitle>
                <CardDescription>记录账号登录、文件上传、版本回退、权限调整与系统配置操作</CardDescription>
                <div className="pt-2">
                  <Input
                    value={auditQuery}
                    onChange={(e) => setAuditQuery(e.target.value)}
                    placeholder="筛选操作人、动作、目标文件或 IP..."
                    className="h-8 text-xs"
                  />
                </div>
              </CardHeader>
              <CardContent>
                <div className="max-h-[55vh] overflow-auto">
                  <table className="w-full text-left text-xs">
                    <thead className="border-b border-border text-muted-foreground sticky top-0 bg-card">
                      <tr>
                        <th className="py-2 pr-3 font-medium">时间</th>
                        <th className="py-2 pr-3 font-medium">操作人</th>
                        <th className="py-2 pr-3 font-medium">动作</th>
                        <th className="py-2 pr-3 font-medium">目标</th>
                        <th className="py-2 pr-3 font-medium">来源 IP</th>
                        <th className="py-2 pr-3 font-medium">状态</th>
                        <th className="py-2 font-medium">详情</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                      {auditLogs
                        .filter((e) => {
                          const q = auditQuery.trim().toLowerCase()
                          if (!q) return true
                          return `${e.actor} ${e.action} ${e.target || ''} ${e.ip || ''} ${e.detail || ''}`
                            .toLowerCase()
                            .includes(q)
                        })
                        .map((entry, idx) => (
                          <tr key={entry.id || idx}>
                            <td className="py-2 pr-3 whitespace-nowrap text-muted-foreground tabular-nums">
                              {formatTime(entry.timestamp || entry.time || entry.created_at)}
                            </td>
                            <td className="py-2 pr-3">{entry.actor || '-'}</td>
                            <td className="py-2 pr-3 font-mono text-[11px]">{entry.action}</td>
                            <td className="py-2 pr-3 max-w-[140px] truncate">{entry.target || '-'}</td>
                            <td className="py-2 pr-3 font-mono text-[11px]">{entry.ip || '-'}</td>
                            <td className="py-2 pr-3">
                              <Badge variant={entry.status === 'error' || entry.status === 'denied' ? 'destructive' : 'success'}>
                                {entry.status || 'ok'}
                              </Badge>
                            </td>
                            <td className="py-2 max-w-[240px] truncate text-muted-foreground">{entry.detail || '-'}</td>
                          </tr>
                        ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          )}
        </div>
      )}

      {/* Adopt Peer Modal */}
      <Dialog
        open={Boolean(adoptTarget)}
        onClose={() => setAdoptTarget(null)}
        size="sm"
        title={`纳管节点 — ${adoptTarget?.node_name || ''}`}
        description={adoptTarget?.http_url}
      >
        <form onSubmit={handleAdoptSubmit} className="space-y-3 text-xs">
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground">目标节点管理员账号</label>
            <Input value={adoptUser} onChange={(e) => setAdoptUser(e.target.value)} />
          </div>
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground">目标节点管理员密码（或留空填写密钥）</label>
            <Input type="password" value={adoptPass} onChange={(e) => setAdoptPass(e.target.value)} />
          </div>
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground">目标节点现有集群密钥（选填）</label>
            <Input value={adoptSecret} onChange={(e) => setAdoptSecret(e.target.value)} />
          </div>
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground">贡献给池的存储配额</label>
            <Input value={adoptQuota} onChange={(e) => setAdoptQuota(e.target.value)} placeholder="50GB" />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={() => setAdoptTarget(null)}>
              取消
            </Button>
            <Button type="submit" disabled={busyAction === 'adopt'}>
              {busyAction === 'adopt' ? '纳管中...' : '确认纳管并接入'}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Edit Mount Config Modal */}
      <Dialog
        open={Boolean(editingMount)}
        onClose={() => setEditingMount(null)}
        size="lg"
        title={`挂载工作区配置 — ${editingMount?.alias || ''}`}
        description={editingMount?.dir_path}
      >
        <div className="space-y-3 text-xs">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <label className="font-medium">虚拟目录别名</label>
              <Input value={editMountAlias} onChange={(e) => setEditMountAlias(e.target.value)} />
            </div>
            <div className="space-y-1">
              <label className="font-medium">单文件写穿上限 (MB)</label>
              <Input
                type="number"
                min={1}
                max={1024}
                value={editMountMaxMB}
                onChange={(e) => setEditMountMaxMB(e.target.value)}
              />
            </div>
          </div>

          <div className="flex items-center justify-between rounded-lg border border-border p-3">
            <div>
              <div className="font-medium">启用可写项目工作区 (Writable)</div>
              <div className="text-[11px] text-muted-foreground">
                开启后允许有写权限的 Agent / 网页在线编辑原子写穿该目录源码
              </div>
            </div>
            <Switch checked={editMountWritable} onCheckedChange={setEditMountWritable} />
          </div>

          {editMountWritable && (
            <div className="flex items-center justify-between rounded-lg border border-border p-3">
              <div>
                <div className="font-medium">Shadow 版本自动备份</div>
                <div className="text-[11px] text-muted-foreground">
                  每次写入或补丁覆盖前保留旧版本快照至 uploads/，支持一键回滚写回宿主机
                </div>
              </div>
              <Switch checked={editMountShadow} onCheckedChange={setEditMountShadow} />
            </div>
          )}

          <div className="space-y-1.5">
            <label className="font-medium block">忽略目录/文件规则（逗号或换行分隔）</label>
            <Textarea
              value={editMountIgnores}
              onChange={(e) => setEditMountIgnores(e.target.value)}
              className="min-h-[72px] font-mono text-xs"
            />
            <div className="flex flex-wrap gap-1.5">
              {[
                { label: '全栈默认', list: 'node_modules, .git, dist, build, .next, .nuxt, target, vendor, __pycache__, .venv, .idea, .vscode, .DS_Store, *.log' },
                { label: 'Node / 前端', list: 'node_modules, dist, .next, .nuxt, coverage, *.log' },
                { label: 'Go 工程', list: 'bin, dist, vendor, *.exe, *.test' },
                { label: 'Python / 数据', list: '__pycache__, .venv, venv, .pytest_cache, *.pyc' },
                { label: 'Java / Rust', list: 'target, build, .gradle, *.class' },
              ].map((tpl) => (
                <Button
                  key={tpl.label}
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => setEditMountIgnores(tpl.list)}
                >
                  套用：{tpl.label}
                </Button>
              ))}
            </div>
            <div className="text-[11px] text-muted-foreground">
              安全底线：服务端硬编码禁止写入 `.git`、`.ssh`、`.env`（允许 `.env.example`）、私钥后缀及符号链接越界路径。
            </div>
          </div>

          <VerifyPresetsEditor value={editMountPresets} onChange={setEditMountPresets} />

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <Button variant="outline" size="sm" onClick={() => setEditingMount(null)}>
              取消
            </Button>
            <Button
              size="sm"
              disabled={savingMount}
              onClick={async () => {
                if (!editingMount) return
                const cleanedPresets: VerifyPreset[] = []
                for (const p of editMountPresets) {
                  const id = p.id.trim()
                  const cmd = p.command.trim()
                  if (!id && !cmd) continue
                  if (!id || !cmd) {
                    toast('每个校验预设都必须填写 ID 与可执行命令', 'error')
                    return
                  }
                  cleanedPresets.push({
                    id,
                    label: (p.label || id).trim(),
                    work_sub_dir: p.work_sub_dir?.trim() || undefined,
                    command: cmd,
                    args: (p.args || []).map((a) => a.trim()).filter(Boolean),
                    timeout_sec: p.timeout_sec ? Math.max(1, Math.min(300, Number(p.timeout_sec))) : 60,
                    log_rel_path: p.log_rel_path?.trim() || undefined,
                  })
                }
                setSavingMount(true)
                try {
                  const ignorePatterns = Array.from(
                    new Set(
                      editMountIgnores
                        .split(/[\n,;]+/)
                        .map((s) => s.trim())
                        .filter(Boolean)
                    )
                  )
                  const maxBytes = Math.max(1, Number(editMountMaxMB || 50)) * 1024 * 1024
                  await api.updateMount(editingMount.node_id, editingMount.id, {
                    alias: editMountAlias.trim() || undefined,
                    writable: editMountWritable,
                    shadow_backup: editMountShadow,
                    ignore_patterns: ignorePatterns,
                    max_file_bytes: maxBytes,
                    verify_presets: cleanedPresets,
                  })
                  toast('挂载工作区配置已更新并触发扫描', 'success')
                  setEditingMount(null)
                  await loadAdminTab('cluster')
                  await onRefreshAll()
                } catch (err) {
                  toast(err instanceof Error ? err.message : '更新挂载配置失败', 'error')
                } finally {
                  setSavingMount(false)
                }
              }}
            >
              {savingMount ? '保存中...' : '保存配置'}
            </Button>
          </div>
        </div>
      </Dialog>

      {/* Live Mount Verify Output Dialog */}
      <Dialog
        open={Boolean(verifyDialog)}
        onClose={() => {
          if (!verifyDialog?.running) setVerifyDialog(null)
        }}
        size="lg"
        title={`工作区构建校验 — ${verifyDialog?.preset.label || verifyDialog?.preset.id || ''}`}
        description={
          verifyDialog
            ? `${verifyDialog.mount.alias} · ${verifyDialog.preset.command} ${(verifyDialog.preset.args || []).join(' ')}`
            : ''
        }
      >
        {verifyDialog && (
          <div className="space-y-3 text-xs">
            {verifyDialog.running ? (
              <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/30 p-4">
                <RefreshCw className="h-4 w-4 animate-spin text-primary" />
                <span>正在节点 {verifyDialog.mount.node_name || verifyDialog.mount.node_id} 上执行受控构建校验...</span>
              </div>
            ) : verifyDialog.error ? (
              <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-destructive">
                <AlertCircle className="h-4 w-4 shrink-0 mt-0.5" />
                <span className="break-all">{verifyDialog.error}</span>
              </div>
            ) : verifyDialog.result ? (
              <div className="space-y-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5">
                    {verifyDialog.result.ok ? (
                      <Badge variant="success" className="inline-flex items-center gap-1">
                        <CheckCircle2 className="h-3 w-3" />
                        <span>校验通过</span>
                      </Badge>
                    ) : verifyDialog.result.timed_out ? (
                      <Badge variant="warning">执行超时终止</Badge>
                    ) : (
                      <Badge variant="destructive">校验失败 (exit={verifyDialog.result.exit_code})</Badge>
                    )}
                    <Badge variant="outline" className="tabular-nums">
                      {verifyDialog.result.duration_ms} ms
                    </Badge>
                    {verifyDialog.result.coalesced && <Badge variant="secondary">单飞合流复用</Badge>}
                    {verifyDialog.result.truncated && <Badge variant="outline">已截断至末尾 128KB</Badge>}
                    {verifyDialog.result.log_path && (
                      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">
                        {verifyDialog.result.log_path}
                      </code>
                    )}
                  </div>
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={async () => {
                      const ok = await copyTextToClipboard(verifyDialog.result?.tail_output || '')
                      toast(ok ? '已复制构建输出' : '复制失败', ok ? 'success' : 'error')
                    }}
                  >
                    <Copy className="h-3 w-3" />
                    <span>复制输出</span>
                  </Button>
                </div>

                <pre className="max-h-96 overflow-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all">
                  {verifyDialog.result.tail_output || '(无标准输出或错误输出)'}
                </pre>
              </div>
            ) : null}

            <div className="flex justify-end pt-2 border-t border-border">
              <Button size="sm" variant="outline" disabled={verifyDialog.running} onClick={() => setVerifyDialog(null)}>
                关闭
              </Button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}
