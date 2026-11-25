import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Bot,
  Check,
  CheckCircle2,
  Clock,
  Copy,
  FolderLock,
  HardDrive,
  History,
  KeyRound,
  Lock,
  LogOut,
  Plus,
  RefreshCw,
  Search,
  Shield,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  Unlock,
  UserCheck,
  UserCog,
  UserPlus,
  Users,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, copyTextToClipboard, formatSize, formatTime } from '@/lib/utils'
import type {
  AdminUserItem,
  AuditEntry,
  AgentKeyItem,
  User,
  UserDetailResponse,
  UserGovernanceSummary,
  UserRole,
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
  Sheet,
  Switch,
  Textarea,
} from '@/components/ui'

export interface UserGovernanceTabProps {
  currentUser: User
  folders: string[]
  toast: (message: string, variant?: 'success' | 'error' | 'info') => void
  onUsersChanged?: () => void
}

const GB_BYTES = 1024 * 1024 * 1024

function bytesToGbInput(bytes?: number): string {
  if (!bytes || bytes <= 0) return '0'
  const gb = bytes / GB_BYTES
  if (Math.abs(gb - Math.round(gb)) < 0.01) {
    return String(Math.round(gb))
  }
  return gb.toFixed(2)
}

function gbInputToBytes(val: string): number {
  const num = Number(val.trim())
  if (!Number.isFinite(num) || num <= 0) return 0
  return Math.round(num * GB_BYTES)
}

function parseAllowedFoldersText(raw: string): string[] {
  const items = raw
    .split(/[\n,]+/)
    .map((s) => s.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, ''))
    .filter(Boolean)
  return Array.from(new Set(items))
}

function toggleFolderInText(raw: string, folder: string): string {
  const clean = folder.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  if (!clean) return raw
  const list = parseAllowedFoldersText(raw)
  const exists = list.includes(clean)
  const next = exists ? list.filter((x) => x !== clean) : [...list, clean]
  return next.join(', ')
}

function roleLabel(role: UserRole | string): string {
  if (role === 'admin') return '管理员'
  if (role === 'readonly') return '只读访客'
  return '普通成员'
}

function roleBadgeVariant(role: UserRole | string): 'default' | 'secondary' | 'outline' {
  if (role === 'admin') return 'default'
  if (role === 'readonly') return 'outline'
  return 'secondary'
}

function statusMeta(u: Pick<AdminUserItem, 'status' | 'expired'>): {
  label: string
  variant: 'success' | 'warning' | 'destructive' | 'secondary'
  key: 'active' | 'pending' | 'banned' | 'expired'
} {
  if (u.status === 'pending') return { label: '待审核', variant: 'warning', key: 'pending' }
  if (u.status === 'banned') return { label: '已封禁', variant: 'destructive', key: 'banned' }
  if (u.expired) return { label: '已过期', variant: 'destructive', key: 'expired' }
  return { label: '正常', variant: 'success', key: 'active' }
}

function formatExpiryShort(expiresAt?: string | null, expired?: boolean): string {
  if (!expiresAt) return '永久有效'
  const ts = Date.parse(expiresAt)
  if (!Number.isFinite(ts)) return expiresAt
  if (expired || ts <= Date.now()) {
    return `已于 ${formatTime(expiresAt)} 过期`
  }
  const diffMs = ts - Date.now()
  const hours = Math.floor(diffMs / 3600000)
  if (hours < 24) {
    return `剩余约 ${Math.max(1, hours)} 小时（${formatTime(expiresAt)}）`
  }
  const days = Math.floor(hours / 24)
  return `剩余约 ${days} 天（${formatTime(expiresAt)}）`
}

export function UserGovernanceTab({
  currentUser,
  folders,
  toast,
  onUsersChanged,
}: UserGovernanceTabProps) {
  const [users, setUsers] = useState<AdminUserItem[]>([])
  const [summary, setSummary] = useState<UserGovernanceSummary>({
    total: 0,
    active: 0,
    pending: 0,
    banned: 0,
    expired: 0,
    admins: 0,
    readonly: 0,
  })
  const [loading, setLoading] = useState(false)

  // Filters & multi-select
  const [search, setSearch] = useState('')
  const [roleFilter, setRoleFilter] = useState<'all' | UserRole>('all')
  const [statusFilter, setStatusFilter] = useState<'all' | 'active' | 'pending' | 'banned' | 'expired'>('all')
  const [selectedNames, setSelectedNames] = useState<string[]>([])
  const [batchBusy, setBatchBusy] = useState(false)
  const [batchQuotaModalOpen, setBatchQuotaModalOpen] = useState(false)
  const [batchQuotaGb, setBatchQuotaGb] = useState('5')

  // Create Member Dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [cUsername, setCUsername] = useState('')
  const [cDisplayName, setCDisplayName] = useState('')
  const [cPassword, setCPassword] = useState('')
  const [cRole, setCRole] = useState<UserRole>('user')
  const [cQuotaGb, setCQuotaGb] = useState('0')
  const [cExpirePreset, setCExpirePreset] = useState<'0' | '24' | '168' | '720' | 'custom'>('0')
  const [cExpireCustomHours, setCExpireCustomHours] = useState('72')
  const [cCanUpload, setCCanUpload] = useState(true)
  const [cCanManageFolders, setCCanManageFolders] = useState(true)
  const [cCanUseAgents, setCCanUseAgents] = useState(true)
  const [cMustChangePwd, setCMustChangePwd] = useState(true)
  const [cAllowedFolders, setCAllowedFolders] = useState('')
  const [cNote, setCNote] = useState('')
  const [createdResult, setCreatedResult] = useState<{
    username: string
    password: string
    autoGenerated: boolean
  } | null>(null)

  // Reset Password Dialog
  const [resetModalUser, setResetModalUser] = useState<AdminUserItem | null>(null)
  const [resetNewPwd, setResetNewPwd] = useState('')
  const [resetMustChange, setResetMustChange] = useState(true)
  const [resetBusy, setResetBusy] = useState(false)
  const [resetResultPwd, setResetResultPwd] = useState('')

  // Delete Member Dialog
  const [deleteModalUser, setDeleteModalUser] = useState<AdminUserItem | null>(null)
  const [deleteFileAction, setDeleteFileAction] = useState<'keep' | 'transfer' | 'delete'>('transfer')
  const [deleteBusy, setDeleteBusy] = useState(false)

  // Detail Drawer (Sheet)
  const [drawerUsername, setDrawerUsername] = useState<string | null>(null)
  const [drawerDetail, setDrawerDetail] = useState<UserDetailResponse | null>(null)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [drawerSaving, setDrawerSaving] = useState(false)
  const [agentFreezeBusy, setAgentFreezeBusy] = useState(false)

  // Drawer Edit Draft
  const [eDisplayName, setEDisplayName] = useState('')
  const [eNote, setENote] = useState('')
  const [eRole, setERole] = useState<UserRole>('user')
  const [eQuotaGb, setEQuotaGb] = useState('0')
  const [eCanUpload, setECanUpload] = useState(true)
  const [eCanManageFolders, setECanManageFolders] = useState(true)
  const [eCanUseAgents, setECanUseAgents] = useState(true)
  const [eMustChangePwd, setEMustChangePwd] = useState(false)
  const [eAllowedFolders, setEAllowedFolders] = useState('')
  const [eExpireAction, setEExpireAction] = useState<'keep' | 'clear' | 'hours'>('keep')
  const [eExpireHours, setEExpireHours] = useState('168')

  const loadUsers = useCallback(async () => {
    setLoading(true)
    try {
      const res = await api.getUsers()
      setUsers(res.users || [])
      if (res.summary) {
        setSummary(res.summary)
      }
      setSelectedNames((prev) =>
        prev.filter((name) => (res.users || []).some((u) => u.username === name && u.username !== currentUser.username))
      )
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载成员列表失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [currentUser.username, toast])

  useEffect(() => {
    void loadUsers()
  }, [loadUsers])

  const syncDraftFromUser = useCallback((u: AdminUserItem) => {
    setEDisplayName(u.display_name || '')
    setENote(u.note || '')
    setERole(u.role)
    setEQuotaGb(bytesToGbInput(u.quota_bytes))
    setECanUpload(u.can_upload !== false)
    setECanManageFolders(u.can_manage_folders !== false)
    setECanUseAgents(u.can_use_agents !== false)
    setEMustChangePwd(Boolean(u.must_change_password))
    setEAllowedFolders((u.allowed_folders || []).join(', '))
    setEExpireAction('keep')
    setEExpireHours('168')
  }, [])

  const openDetailDrawer = useCallback(
    async (username: string) => {
      setDrawerUsername(username)
      setDrawerLoading(true)
      try {
        const res = await api.getUserDetail(username)
        setDrawerDetail(res)
        syncDraftFromUser(res.user)
      } catch (err) {
        toast(err instanceof Error ? err.message : '加载成员详情失败', 'error')
        setDrawerUsername(null)
      } finally {
        setDrawerLoading(false)
      }
    },
    [syncDraftFromUser, toast]
  )

  const filteredUsers = useMemo(() => {
    const q = search.trim().toLowerCase()
    return users.filter((u) => {
      if (roleFilter !== 'all' && u.role !== roleFilter) return false
      const st = statusMeta(u)
      if (statusFilter !== 'all' && st.key !== statusFilter) return false
      if (q) {
        const hay = [
          u.username,
          u.display_name || '',
          u.note || '',
          u.last_login_ip || '',
          ...(u.allowed_folders || []),
        ]
          .join(' ')
          .toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [roleFilter, search, statusFilter, users])

  const selectableFilteredNames = useMemo(
    () => filteredUsers.filter((u) => u.username !== currentUser.username).map((u) => u.username),
    [currentUser.username, filteredUsers]
  )

  const allFilteredSelected =
    selectableFilteredNames.length > 0 && selectableFilteredNames.every((name) => selectedNames.includes(name))

  const toggleSelectAll = () => {
    if (allFilteredSelected) {
      setSelectedNames((prev) => prev.filter((name) => !selectableFilteredNames.includes(name)))
    } else {
      setSelectedNames((prev) => Array.from(new Set([...prev, ...selectableFilteredNames])))
    }
  }

  const toggleSelectOne = (username: string) => {
    if (username === currentUser.username) return
    setSelectedNames((prev) => (prev.includes(username) ? prev.filter((x) => x !== username) : [...prev, username]))
  }

  const totalUsedBytes = useMemo(() => users.reduce((acc, u) => acc + (u.used_bytes || 0), 0), [users])
  const totalActiveAgents = useMemo(
    () => users.reduce((acc, u) => acc + (u.active_agent_key_count || 0), 0),
    [users]
  )
  const totalAgents = useMemo(() => users.reduce((acc, u) => acc + (u.agent_key_count || 0), 0), [users])

  const notifyMutation = useCallback(async () => {
    await loadUsers()
    onUsersChanged?.()
  }, [loadUsers, onUsersChanged])

  // Batch actions
  const runBatchAction = async (
    action: 'approve' | 'ban' | 'unban' | 'force_logout' | 'set_quota',
    quotaBytes?: number
  ) => {
    if (selectedNames.length === 0) return
    if (
      action === 'ban' &&
      !window.confirm(`确定批量封禁选中的 ${selectedNames.length} 个成员并断开其 AI Agent 连接吗？`)
    ) {
      return
    }
    if (
      action === 'force_logout' &&
      !window.confirm(`确定让选中的 ${selectedNames.length} 个成员立即下线并清空其 API Token 吗？`)
    ) {
      return
    }
    setBatchBusy(true)
    try {
      const res = await api.batchUsersAction({
        usernames: selectedNames,
        action,
        quota_bytes: quotaBytes,
      })
      setUsers(res.users || [])
      if (res.summary) setSummary(res.summary)
      toast(
        `批量操作已完成：成功处理 ${res.result.updated.length} 项${
          res.result.skipped?.length ? `，跳过 ${res.result.skipped.length} 项` : ''
        }`,
        'success'
      )
      setSelectedNames([])
      setBatchQuotaModalOpen(false)
      onUsersChanged?.()
      if (drawerUsername && selectedNames.includes(drawerUsername)) {
        void openDetailDrawer(drawerUsername)
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : '批量操作失败', 'error')
    } finally {
      setBatchBusy(false)
    }
  }

  // Create Member
  const resetCreateForm = () => {
    setCUsername('')
    setCDisplayName('')
    setCPassword('')
    setCRole('user')
    setCQuotaGb('0')
    setCExpirePreset('0')
    setCExpireCustomHours('72')
    setCCanUpload(true)
    setCCanManageFolders(true)
    setCCanUseAgents(true)
    setCMustChangePwd(true)
    setCAllowedFolders('')
    setCNote('')
    setCreatedResult(null)
  }

  const handleRoleChangeInCreate = (nextRole: UserRole) => {
    setCRole(nextRole)
    if (nextRole === 'readonly') {
      setCCanUpload(false)
      setCCanManageFolders(false)
    } else if (cRole === 'readonly') {
      setCCanUpload(true)
      setCCanManageFolders(true)
    }
  }

  const submitCreateUser = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!cUsername.trim()) {
      toast('请输入用户名', 'error')
      return
    }
    let expireHours = 0
    if (cExpirePreset === 'custom') {
      const h = Math.round(Number(cExpireCustomHours))
      if (!Number.isFinite(h) || h <= 0) {
        toast('请输入有效的临时账号有效小时数', 'error')
        return
      }
      expireHours = h
    } else if (cExpirePreset !== '0') {
      expireHours = Number(cExpirePreset)
    }

    setCreateBusy(true)
    try {
      const res = await api.createUser({
        username: cUsername.trim(),
        password: cPassword.trim() || undefined,
        role: cRole,
        display_name: cDisplayName.trim(),
        note: cNote.trim(),
        quota_bytes: gbInputToBytes(cQuotaGb),
        allowed_folders: parseAllowedFoldersText(cAllowedFolders),
        can_upload: cCanUpload,
        can_manage_folders: cCanManageFolders,
        can_use_agents: cCanUseAgents,
        must_change_password: cMustChangePwd,
        expire_hours: expireHours,
      })
      setUsers(res.users || [])
      if (res.summary) setSummary(res.summary)
      setCreatedResult({
        username: res.user.username,
        password: res.generated_password || cPassword.trim(),
        autoGenerated: Boolean(res.generated_password),
      })
      toast(`已创建成员 ${res.user.username}`, 'success')
      onUsersChanged?.()
    } catch (err) {
      toast(err instanceof Error ? err.message : '创建成员失败', 'error')
    } finally {
      setCreateBusy(false)
    }
  }

  // Reset Password
  const openResetPasswordModal = (u: AdminUserItem) => {
    setResetModalUser(u)
    setResetNewPwd('')
    setResetMustChange(true)
    setResetResultPwd('')
  }

  const submitResetPassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!resetModalUser) return
    setResetBusy(true)
    try {
      const res = await api.resetUserPassword(resetModalUser.username, {
        new_password: resetNewPwd.trim() || undefined,
        must_change_password: resetMustChange,
      })
      setResetResultPwd(res.temporary_password)
      toast(`已重置 ${resetModalUser.username} 的登录密码并使其旧会话失效`, 'success')
      await notifyMutation()
      if (drawerUsername === resetModalUser.username) {
        void openDetailDrawer(resetModalUser.username)
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : '重置密码失败', 'error')
    } finally {
      setResetBusy(false)
    }
  }

  // Force Logout
  const handleForceLogout = async (u: AdminUserItem) => {
    if (!window.confirm(`确定强制 ${u.username} 立即下线吗？此操作会使其全部浏览器会话失效并清除个人 API Token。`)) {
      return
    }
    try {
      await api.forceLogoutUser(u.username)
      toast(`已强制 ${u.username} 下线`, 'success')
      await notifyMutation()
      if (drawerUsername === u.username) {
        void openDetailDrawer(u.username)
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : '强制下线失败', 'error')
    }
  }

  // Delete Member
  const openDeleteModal = (u: AdminUserItem) => {
    setDeleteModalUser(u)
    setDeleteFileAction(u.file_count > 0 ? 'transfer' : 'keep')
  }

  const submitDeleteUser = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!deleteModalUser) return
    setDeleteBusy(true)
    try {
      const targetName = deleteModalUser.username
      const res = await api.deleteUser(targetName, deleteFileAction)
      toast(
        res.affected_files && res.affected_files > 0
          ? `已删除成员 ${targetName}，并处理关联文件 ${res.affected_files} 个`
          : `已彻底删除成员 ${targetName}`,
        'success'
      )
      setDeleteModalUser(null)
      if (drawerUsername === targetName) {
        setDrawerUsername(null)
        setDrawerDetail(null)
      }
      await notifyMutation()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除成员失败', 'error')
    } finally {
      setDeleteBusy(false)
    }
  }

  // Drawer Save Profile / Permissions / Quota / Expiry
  const submitDrawerSave = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!drawerDetail) return
    const target = drawerDetail.user
    const isSelf = target.username === currentUser.username
    setDrawerSaving(true)
    try {
      const payload: Parameters<typeof api.updateUser>[1] = {
        display_name: eDisplayName.trim(),
        note: eNote.trim(),
        quota_bytes: gbInputToBytes(eQuotaGb),
        allowed_folders: parseAllowedFoldersText(eAllowedFolders),
        can_upload: eCanUpload,
        can_manage_folders: eCanManageFolders,
        can_use_agents: eCanUseAgents,
        must_change_password: eMustChangePwd,
      }
      if (!isSelf) {
        payload.role = eRole
      }
      if (eExpireAction === 'clear') {
        payload.clear_expires_at = true
      } else if (eExpireAction === 'hours') {
        const hrs = Math.round(Number(eExpireHours))
        if (!Number.isFinite(hrs) || hrs <= 0) {
          toast('请输入有效的有效期小时数', 'error')
          setDrawerSaving(false)
          return
        }
        payload.expire_hours = hrs
      }
      await api.updateUser(target.username, payload)
      toast(`已保存 ${target.username} 的治理配置`, 'success')
      await notifyMutation()
      await openDetailDrawer(target.username)
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存成员配置失败', 'error')
    } finally {
      setDrawerSaving(false)
    }
  }

  const handleFreezeAgents = async (disabled: boolean) => {
    if (!drawerDetail) return
    setAgentFreezeBusy(true)
    try {
      const res = await api.freezeUserAgents(drawerDetail.user.username, disabled)
      setDrawerDetail((prev) => (prev ? { ...prev, agent_keys: res.keys || [] } : prev))
      toast(
        disabled
          ? `已冻结 ${drawerDetail.user.username} 名下 ${res.affected} 个 AI Agent 凭证`
          : `已恢复 ${drawerDetail.user.username} 名下 ${res.affected} 个 AI Agent 凭证`,
        'success'
      )
      await notifyMutation()
    } catch (err) {
      toast(err instanceof Error ? err.message : '操作 Agent 凭证失败', 'error')
    } finally {
      setAgentFreezeBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      {/* KPI Strip */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>成员总数</span>
            <Users className="h-3.5 w-3.5 text-primary" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">{summary.total}</span>
            <span className="text-[11px] text-muted-foreground">
              管理 {summary.admins} · 只读 {summary.readonly}
            </span>
          </div>
        </Card>

        <Card className="p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>活跃可用账号</span>
            <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">{summary.active}</span>
            <span className="text-[11px] text-muted-foreground">正常可访问命名空间</span>
          </div>
        </Card>

        <Card className="p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>待审核 / 限制账号</span>
            <ShieldAlert className="h-3.5 w-3.5 text-amber-500" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">{summary.pending}</span>
            <span className="text-[11px] text-muted-foreground">
              已封禁 {summary.banned} · 已过期 {summary.expired}
            </span>
          </div>
        </Card>

        <Card className="p-3.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>成员空间与 Agent</span>
            <HardDrive className="h-3.5 w-3.5 text-primary" />
          </div>
          <div className="mt-1.5 flex items-baseline gap-2">
            <span className="text-xl font-semibold tabular-nums">{formatSize(totalUsedBytes)}</span>
            <span className="text-[11px] text-muted-foreground">
              Agent {totalActiveAgents}/{totalAgents} 启用
            </span>
          </div>
        </Card>
      </div>

      {/* Main Governance Card */}
      <Card>
        <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle className="flex items-center gap-2">
              <UserCog className="h-4 w-4 text-primary" />
              <span>成员治理中心</span>
            </CardTitle>
            <CardDescription>
              直接开户、重置密码、配额限制、目录隔离、只读角色、临时有效期与登录审计控制
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              onClick={() => {
                resetCreateForm()
                setCreateOpen(true)
              }}
            >
              <UserPlus className="h-3.5 w-3.5" />
              <span>新建成员</span>
            </Button>
            <Button size="sm" variant="outline" onClick={() => void loadUsers()} disabled={loading}>
              <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
              <span>刷新</span>
            </Button>
          </div>
        </CardHeader>

        <CardContent className="space-y-3">
          {/* Search & Filters */}
          <div className="flex flex-col gap-2 md:flex-row md:items-center">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索用户名、显示名、备注、允许目录或登录 IP..."
                className="pl-8 text-xs"
              />
            </div>
            <div className="flex items-center gap-2">
              <Select
                value={roleFilter}
                onChange={(e) => setRoleFilter(e.target.value as 'all' | UserRole)}
                className="h-9 text-xs min-w-[116px]"
              >
                <option value="all">全部角色</option>
                <option value="admin">管理员</option>
                <option value="user">普通成员</option>
                <option value="readonly">只读访客</option>
              </Select>
              <Select
                value={statusFilter}
                onChange={(e) =>
                  setStatusFilter(e.target.value as 'all' | 'active' | 'pending' | 'banned' | 'expired')
                }
                className="h-9 text-xs min-w-[116px]"
              >
                <option value="all">全部状态</option>
                <option value="active">正常</option>
                <option value="pending">待审核</option>
                <option value="banned">已封禁</option>
                <option value="expired">已过期</option>
              </Select>
            </div>
          </div>

          {/* Multi-Select Batch Action Strip */}
          {selectedNames.length > 0 && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2 text-xs">
              <div className="font-medium text-foreground">
                已选择 <span className="tabular-nums text-primary">{selectedNames.length}</span> 名成员
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <Button size="xs" variant="outline" disabled={batchBusy} onClick={() => void runBatchAction('approve')}>
                  <UserCheck className="h-3 w-3" />
                  <span>批量批准</span>
                </Button>
                <Button size="xs" variant="outline" disabled={batchBusy} onClick={() => void runBatchAction('unban')}>
                  <Unlock className="h-3 w-3" />
                  <span>批量解封</span>
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={batchBusy}
                  onClick={() => void runBatchAction('force_logout')}
                >
                  <LogOut className="h-3 w-3" />
                  <span>批量强制下线</span>
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={batchBusy}
                  onClick={() => setBatchQuotaModalOpen(true)}
                >
                  <HardDrive className="h-3 w-3" />
                  <span>批量设配额</span>
                </Button>
                <Button
                  size="xs"
                  variant="destructive"
                  disabled={batchBusy}
                  onClick={() => void runBatchAction('ban')}
                >
                  <Lock className="h-3 w-3" />
                  <span>批量封禁</span>
                </Button>
                <Button size="xs" variant="ghost" onClick={() => setSelectedNames([])}>
                  清空选择
                </Button>
              </div>
            </div>
          )}

          {/* Member Table */}
          <div className="overflow-x-auto rounded-lg border border-border">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-border bg-muted/40 text-muted-foreground">
                <tr>
                  <th className="w-9 px-3 py-2.5">
                    <input
                      type="checkbox"
                      checked={allFilteredSelected}
                      onChange={toggleSelectAll}
                      disabled={selectableFilteredNames.length === 0}
                      aria-label="全选当前筛选成员"
                      className="h-3.5 w-3.5 rounded border-border accent-primary cursor-pointer"
                    />
                  </th>
                  <th className="py-2.5 pr-3 font-medium">成员与权限标签</th>
                  <th className="py-2.5 pr-3 font-medium whitespace-nowrap">角色</th>
                  <th className="py-2.5 pr-3 font-medium whitespace-nowrap">状态与有效期</th>
                  <th className="py-2.5 pr-3 font-medium min-w-[155px]">配额与已用空间</th>
                  <th className="py-2.5 pr-3 font-medium whitespace-nowrap">最近登录审计</th>
                  <th className="py-2.5 pr-3 font-medium whitespace-nowrap">Agent 凭证</th>
                  <th className="px-3 py-2.5 text-right font-medium whitespace-nowrap">治理操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {filteredUsers.length === 0 ? (
                  <tr>
                    <td colSpan={8} className="px-4 py-8 text-center text-muted-foreground">
                      未找到符合筛选条件的成员账号
                    </td>
                  </tr>
                ) : (
                  filteredUsers.map((u) => {
                    const isSelf = u.username === currentUser.username
                    const st = statusMeta(u)
                    const checked = selectedNames.includes(u.username)
                    const quotaBytes = u.quota_bytes || 0
                    const pct =
                      quotaBytes > 0 ? Math.min(100, Math.round(((u.used_bytes || 0) / quotaBytes) * 100)) : 0
                    const deniedUpload = u.role === 'readonly' || u.can_upload === false
                    const deniedFolders = u.role === 'readonly' || u.can_manage_folders === false
                    const deniedAgents = u.can_use_agents === false

                    return (
                      <tr
                        key={u.id || u.username}
                        className={cn(
                          'transition-colors hover:bg-muted/30',
                          checked && 'bg-primary/5'
                        )}
                      >
                        <td className="px-3 py-2.5 align-top">
                          <input
                            type="checkbox"
                            checked={checked}
                            disabled={isSelf}
                            onChange={() => toggleSelectOne(u.username)}
                            aria-label={`选择成员 ${u.username}`}
                            className="mt-0.5 h-3.5 w-3.5 rounded border-border accent-primary cursor-pointer disabled:opacity-40"
                          />
                        </td>
                        <td className="py-2.5 pr-3 align-top">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <button
                              type="button"
                              onClick={() => void openDetailDrawer(u.username)}
                              className="font-medium text-foreground hover:text-primary hover:underline cursor-pointer text-left"
                            >
                              {u.username}
                            </button>
                            {u.display_name && (
                              <span className="text-[11px] text-muted-foreground">({u.display_name})</span>
                            )}
                            {isSelf && <Badge variant="outline">当前账号</Badge>}
                            {u.must_change_password && <Badge variant="warning">待改密</Badge>}
                            {u.has_api_token && <Badge variant="secondary">Token</Badge>}
                          </div>
                          {u.note && (
                            <div className="mt-0.5 text-[11px] text-muted-foreground line-clamp-1">{u.note}</div>
                          )}
                          <div className="mt-1 flex flex-wrap items-center gap-1">
                            {u.allowed_folders && u.allowed_folders.length > 0 && (
                              <Badge variant="outline" className="text-[10px] py-0 px-1.5 gap-1">
                                <FolderLock className="h-2.5 w-2.5 text-primary" />
                                <span>仅 {u.allowed_folders.length} 个目录</span>
                              </Badge>
                            )}
                            {deniedUpload && u.role !== 'admin' && (
                              <Badge variant="outline" className="text-[10px] py-0 px-1.5 text-amber-600 dark:text-amber-400">
                                禁上传
                              </Badge>
                            )}
                            {deniedFolders && u.role !== 'admin' && (
                              <Badge variant="outline" className="text-[10px] py-0 px-1.5 text-muted-foreground">
                                禁管目录
                              </Badge>
                            )}
                            {deniedAgents && u.role !== 'admin' && (
                              <Badge variant="outline" className="text-[10px] py-0 px-1.5 text-destructive">
                                禁Agent
                              </Badge>
                            )}
                          </div>
                        </td>
                        <td className="py-2.5 pr-3 align-top whitespace-nowrap">
                          <Badge variant={roleBadgeVariant(u.role)}>{roleLabel(u.role)}</Badge>
                        </td>
                        <td className="py-2.5 pr-3 align-top whitespace-nowrap">
                          <div>
                            <Badge variant={st.variant}>{st.label}</Badge>
                          </div>
                          {u.expires_at ? (
                            <div className="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground">
                              <Clock className="h-2.5 w-2.5 shrink-0" />
                              <span>{formatTime(u.expires_at)}</span>
                            </div>
                          ) : (
                            <div className="mt-1 text-[10px] text-muted-foreground">永久有效</div>
                          )}
                        </td>
                        <td className="py-2.5 pr-3 align-top">
                          <div className="flex items-center justify-between text-[11px] tabular-nums">
                            <span className="font-medium text-foreground">{formatSize(u.used_bytes || 0)}</span>
                            <span className="text-muted-foreground">
                              / {quotaBytes > 0 ? formatSize(quotaBytes) : '不限'}
                            </span>
                          </div>
                          {quotaBytes > 0 && (
                            <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                              <div
                                className={cn(
                                  'h-full rounded-full transition-all',
                                  pct >= 90 ? 'bg-destructive' : pct >= 75 ? 'bg-amber-500' : 'bg-primary'
                                )}
                                style={{ width: `${pct}%` }}
                              />
                            </div>
                          )}
                          <div className="mt-0.5 text-[10px] text-muted-foreground tabular-nums">
                            {u.file_count || 0} 个文件
                          </div>
                        </td>
                        <td className="py-2.5 pr-3 align-top whitespace-nowrap">
                          {u.last_login_at ? (
                            <>
                              <div className="text-[11px] text-foreground">{formatTime(u.last_login_at)}</div>
                              <div className="text-[10px] font-mono text-muted-foreground">
                                {u.last_login_ip || '-'}
                              </div>
                            </>
                          ) : (
                            <span className="text-[11px] text-muted-foreground">从未登录</span>
                          )}
                        </td>
                        <td className="py-2.5 pr-3 align-top whitespace-nowrap tabular-nums">
                          <div className="inline-flex items-center gap-1 text-[11px]">
                            <Bot className="h-3.5 w-3.5 text-muted-foreground" />
                            <span>
                              {u.active_agent_key_count}/{u.agent_key_count}
                            </span>
                          </div>
                        </td>
                        <td className="px-3 py-2.5 text-right align-top whitespace-nowrap">
                          <div className="inline-flex items-center gap-1">
                            {u.status === 'pending' && (
                              <Button
                                size="xs"
                                onClick={async () => {
                                  await api.approveUser(u.username)
                                  toast(`已批准成员 ${u.username}`, 'success')
                                  await notifyMutation()
                                }}
                              >
                                <UserCheck className="h-3 w-3" />
                                <span>批准</span>
                              </Button>
                            )}
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => void openDetailDrawer(u.username)}
                            >
                              <SlidersHorizontal className="h-3 w-3" />
                              <span>详情与权限</span>
                            </Button>
                            {!isSelf && (
                              <>
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  title="重置密码"
                                  onClick={() => openResetPasswordModal(u)}
                                >
                                  <KeyRound className="h-3.5 w-3.5" />
                                </Button>
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  title="强制下线（使会话与 API Token 失效）"
                                  onClick={() => void handleForceLogout(u)}
                                >
                                  <LogOut className="h-3.5 w-3.5" />
                                </Button>
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  className="text-destructive"
                                  title="删除成员"
                                  onClick={() => openDeleteModal(u)}
                                >
                                  <Trash2 className="h-3.5 w-3.5" />
                                </Button>
                              </>
                            )}
                          </div>
                        </td>
                      </tr>
                    )
                  })
                )}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {/* ==================== Create Member Dialog ==================== */}
      <Dialog
        open={createOpen}
        onClose={() => {
          setCreateOpen(false)
          setCreatedResult(null)
        }}
        size="md"
        title="新建成员账号"
        description="管理员直接开户并预配置角色、存储配额、目录白名单与有效期"
      >
        {createdResult ? (
          <div className="space-y-4 text-xs">
            <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-4 space-y-2">
              <div className="flex items-center gap-2 font-semibold text-emerald-600 dark:text-emerald-400">
                <CheckCircle2 className="h-4 w-4" />
                <span>成员账号「{createdResult.username}」已创建并激活</span>
              </div>
              <p className="text-muted-foreground">
                {createdResult.autoGenerated
                  ? '系统已为该成员生成强随机初始密码，请立即复制并线下转交成员：'
                  : '请妥善保管并告知成员以下初始登录凭据：'}
              </p>
              <div className="rounded-md border border-border bg-card p-3 font-mono text-xs space-y-1">
                <div>用户名：{createdResult.username}</div>
                <div>初始密码：{createdResult.password}</div>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={async () => {
                  const text = `QuailNest 登录账号：${createdResult.username}\n初始密码：${createdResult.password}`
                  const ok = await copyTextToClipboard(text)
                  toast(ok ? '已复制账号密码到剪贴板' : '复制失败，请手动选中复制', ok ? 'success' : 'error')
                }}
              >
                <Copy className="h-3.5 w-3.5" />
                <span>复制账号密码</span>
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  setCreateOpen(false)
                  setCreatedResult(null)
                }}
              >
                完成
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={submitCreateUser} className="space-y-4 text-xs">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <label className="font-medium">用户名 *</label>
                <Input
                  value={cUsername}
                  onChange={(e) => setCUsername(e.target.value)}
                  placeholder="2-32 位字母/数字/中文（无空格或斜杠）"
                  required
                  autoFocus
                />
              </div>
              <div className="space-y-1">
                <label className="font-medium">显示名称 / 昵称（可选）</label>
                <Input
                  value={cDisplayName}
                  onChange={(e) => setCDisplayName(e.target.value)}
                  placeholder="例如：张工 / 财务组"
                />
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <label className="font-medium">初始密码（留空则自动生成强密码）</label>
                <Input
                  value={cPassword}
                  onChange={(e) => setCPassword(e.target.value)}
                  placeholder="至少 6 位，留空自动生成 12 位随机密码"
                />
              </div>
              <div className="space-y-1">
                <label className="font-medium">角色身份</label>
                <Select value={cRole} onChange={(e) => handleRoleChangeInCreate(e.target.value as UserRole)}>
                  <option value="user">普通成员（读写所属权限文件）</option>
                  <option value="readonly">只读访客（仅预览与下载）</option>
                  <option value="admin">管理员（全平台治理权限）</option>
                </Select>
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <label className="font-medium">个人空间配额（GB，0 表示不限）</label>
                <Input
                  type="number"
                  min="0"
                  step="0.5"
                  value={cQuotaGb}
                  onChange={(e) => setCQuotaGb(e.target.value)}
                />
                <div className="flex flex-wrap gap-1">
                  {['0', '1', '5', '10', '50'].map((preset) => (
                    <button
                      key={preset}
                      type="button"
                      onClick={() => setCQuotaGb(preset)}
                      className={cn(
                        'rounded border px-2 py-0.5 text-[11px] cursor-pointer',
                        cQuotaGb === preset
                          ? 'border-primary bg-primary text-primary-foreground'
                          : 'border-border bg-muted/40 hover:bg-muted'
                      )}
                    >
                      {preset === '0' ? '不限' : `${preset} GB`}
                    </button>
                  ))}
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="font-medium">账号有效期</label>
                <Select
                  value={cExpirePreset}
                  onChange={(e) => setCExpirePreset(e.target.value as '0' | '24' | '168' | '720' | 'custom')}
                >
                  <option value="0">永久有效</option>
                  <option value="24">临时 24 小时后自动失效</option>
                  <option value="168">临时 7 天后自动失效</option>
                  <option value="720">临时 30 天后自动失效</option>
                  <option value="custom">自定义小时数...</option>
                </Select>
                {cExpirePreset === 'custom' && (
                  <Input
                    type="number"
                    min="1"
                    value={cExpireCustomHours}
                    onChange={(e) => setCExpireCustomHours(e.target.value)}
                    placeholder="输入有效小时数"
                  />
                )}
              </div>
            </div>

            <div className="grid gap-2 rounded-lg border border-border bg-muted/20 p-3 sm:grid-cols-2">
              <div className="flex items-center justify-between gap-2">
                <span>允许上传与修改文件</span>
                <Switch checked={cCanUpload} onCheckedChange={setCCanUpload} />
              </div>
              <div className="flex items-center justify-between gap-2">
                <span>允许创建/重命名目录</span>
                <Switch checked={cCanManageFolders} onCheckedChange={setCCanManageFolders} />
              </div>
              <div className="flex items-center justify-between gap-2">
                <span>允许管理名下 AI Agent</span>
                <Switch checked={cCanUseAgents} onCheckedChange={setCCanUseAgents} />
              </div>
              <div className="flex items-center justify-between gap-2">
                <span>首次登录强制改密</span>
                <Switch checked={cMustChangePwd} onCheckedChange={setCMustChangePwd} />
              </div>
            </div>

            <div className="space-y-1.5">
              <label className="font-medium">允许访问目录白名单（留空代表不限制目录）</label>
              <Textarea
                rows={2}
                value={cAllowedFolders}
                onChange={(e) => setCAllowedFolders(e.target.value)}
                placeholder="例如：项目归档, 公开资料/设计稿（多个用逗号或换行分隔，含子目录继承）"
              />
              {folders.length > 0 && (
                <div className="flex flex-wrap items-center gap-1">
                  <span className="text-[11px] text-muted-foreground mr-1">快捷点选现有目录:</span>
                  {folders.slice(0, 14).map((fp) => {
                    const selected = parseAllowedFoldersText(cAllowedFolders).includes(fp)
                    return (
                      <button
                        key={fp}
                        type="button"
                        onClick={() => setCAllowedFolders((prev) => toggleFolderInText(prev, fp))}
                        className={cn(
                          'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] cursor-pointer',
                          selected
                            ? 'border-primary bg-primary/15 text-primary font-medium'
                            : 'border-border bg-muted/30 text-muted-foreground hover:text-foreground'
                        )}
                      >
                        {selected && <Check className="h-2.5 w-2.5" />}
                        <span>/{fp}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>

            <div className="space-y-1">
              <label className="font-medium">治理备注（仅管理员可见）</label>
              <Input
                value={cNote}
                onChange={(e) => setCNote(e.target.value)}
                placeholder="例如：外包设计实习生账号、财务对账只读专用"
              />
            </div>

            <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
              <Button type="button" size="sm" variant="outline" onClick={() => setCreateOpen(false)}>
                取消
              </Button>
              <Button type="submit" size="sm" disabled={createBusy}>
                <Plus className="h-3.5 w-3.5" />
                <span>{createBusy ? '创建中...' : '确认创建并激活'}</span>
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* ==================== Reset Password Dialog ==================== */}
      <Dialog
        open={Boolean(resetModalUser)}
        onClose={() => {
          setResetModalUser(null)
          setResetResultPwd('')
        }}
        size="sm"
        title={`重置登录密码 — ${resetModalUser?.username || ''}`}
        description="重置后该成员当前浏览器会话与 API Token 将立即失效"
      >
        {resetResultPwd ? (
          <div className="space-y-4 text-xs">
            <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3.5 space-y-2">
              <div className="font-medium text-emerald-600 dark:text-emerald-400">
                已生成新临时密码并强制旧登录态失效
              </div>
              <div className="rounded border border-border bg-card px-3 py-2 font-mono text-sm select-all">
                {resetResultPwd}
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={async () => {
                  const ok = await copyTextToClipboard(resetResultPwd)
                  toast(ok ? '已复制新密码' : '复制失败', ok ? 'success' : 'error')
                }}
              >
                <Copy className="h-3.5 w-3.5" />
                <span>复制密码</span>
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  setResetModalUser(null)
                  setResetResultPwd('')
                }}
              >
                关闭
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={submitResetPassword} className="space-y-3 text-xs">
            <div className="space-y-1">
              <label className="font-medium">指定新密码（留空则自动生成强随机密码）</label>
              <Input
                value={resetNewPwd}
                onChange={(e) => setResetNewPwd(e.target.value)}
                placeholder="至少 6 位，留空自动生成"
                autoFocus
              />
            </div>
            <div className="flex items-center justify-between rounded-lg border border-border bg-muted/20 p-2.5">
              <span>要求成员下次登录后立即修改密码</span>
              <Switch checked={resetMustChange} onCheckedChange={setResetMustChange} />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button type="button" size="sm" variant="outline" onClick={() => setResetModalUser(null)}>
                取消
              </Button>
              <Button type="submit" size="sm" disabled={resetBusy}>
                {resetBusy ? '重置中...' : '确认重置并下线'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* ==================== Batch Quota Dialog ==================== */}
      <Dialog
        open={batchQuotaModalOpen}
        onClose={() => setBatchQuotaModalOpen(false)}
        size="sm"
        title="批量设置个人空间配额"
        description={`将为选中的 ${selectedNames.length} 个成员统一调整空间上限`}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void runBatchAction('set_quota', gbInputToBytes(batchQuotaGb))
          }}
          className="space-y-3 text-xs"
        >
          <div className="space-y-1.5">
            <label className="font-medium">空间上限（GB，输入 0 代表不限制）</label>
            <Input
              type="number"
              min="0"
              step="0.5"
              value={batchQuotaGb}
              onChange={(e) => setBatchQuotaGb(e.target.value)}
              autoFocus
            />
            <div className="flex flex-wrap gap-1.5">
              {['0', '1', '5', '10', '50'].map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => setBatchQuotaGb(p)}
                  className={cn(
                    'rounded border px-2 py-0.5 text-[11px] cursor-pointer',
                    batchQuotaGb === p
                      ? 'border-primary bg-primary text-primary-foreground'
                      : 'border-border bg-muted/40 hover:bg-muted'
                  )}
                >
                  {p === '0' ? '不限' : `${p} GB`}
                </button>
              ))}
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" size="sm" variant="outline" onClick={() => setBatchQuotaModalOpen(false)}>
              取消
            </Button>
            <Button type="submit" size="sm" disabled={batchBusy}>
              {batchBusy ? '应用中...' : '确认应用配额'}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* ==================== Delete Member Dialog ==================== */}
      <Dialog
        open={Boolean(deleteModalUser)}
        onClose={() => setDeleteModalUser(null)}
        size="sm"
        title={`删除成员 — ${deleteModalUser?.username || ''}`}
        description="请选择名下已上传文件的资产处置方式"
      >
        {deleteModalUser && (
          <form onSubmit={submitDeleteUser} className="space-y-3 text-xs">
            <div className="rounded-lg border border-border bg-muted/30 p-2.5 text-muted-foreground">
              该成员名下当前有{' '}
              <span className="font-semibold text-foreground">{deleteModalUser.file_count}</span> 个文件，共占用{' '}
              <span className="font-semibold text-foreground">{formatSize(deleteModalUser.used_bytes)}</span>；
              其名下 Agent 凭证也将一并撤销并断开在线连接。
            </div>

            <div className="space-y-2">
              <label className="flex items-start gap-2.5 rounded-lg border border-border p-2.5 cursor-pointer hover:bg-muted/30">
                <input
                  type="radio"
                  name="delete_file_action"
                  checked={deleteFileAction === 'transfer'}
                  onChange={() => setDeleteFileAction('transfer')}
                  className="mt-0.5 accent-primary"
                />
                <div>
                  <div className="font-medium text-foreground">过户文件给管理员 ({currentUser.username})（推荐）</div>
                  <div className="text-[11px] text-muted-foreground">
                    将名下全部文件的上传归属安全移交给当前管理员，不丢失任何文件数据。
                  </div>
                </div>
              </label>

              <label className="flex items-start gap-2.5 rounded-lg border border-border p-2.5 cursor-pointer hover:bg-muted/30">
                <input
                  type="radio"
                  name="delete_file_action"
                  checked={deleteFileAction === 'keep'}
                  onChange={() => setDeleteFileAction('keep')}
                  className="mt-0.5 accent-primary"
                />
                <div>
                  <div className="font-medium text-foreground">仅删除账号（保留文件原上传署名）</div>
                  <div className="text-[11px] text-muted-foreground">
                    保留文件实体与原 uploader 字段，仅吊销账号登录权。
                  </div>
                </div>
              </label>

              <label className="flex items-start gap-2.5 rounded-lg border border-destructive/40 bg-destructive/5 p-2.5 cursor-pointer hover:bg-destructive/10">
                <input
                  type="radio"
                  name="delete_file_action"
                  checked={deleteFileAction === 'delete'}
                  onChange={() => setDeleteFileAction('delete')}
                  className="mt-0.5 accent-destructive"
                />
                <div>
                  <div className="font-medium text-destructive">彻底删除该成员上传的所有文件</div>
                  <div className="text-[11px] text-muted-foreground">
                    不可恢复清理该成员在集群上传的文件及历史版本（不影响只读挂载目录）。
                  </div>
                </div>
              </label>
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <Button type="button" size="sm" variant="outline" onClick={() => setDeleteModalUser(null)}>
                取消
              </Button>
              <Button type="submit" size="sm" variant="destructive" disabled={deleteBusy}>
                {deleteBusy ? '执行中...' : '确认删除成员'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* ==================== Slide-Over Member Detail Drawer ==================== */}
      <Sheet
        open={Boolean(drawerUsername)}
        onClose={() => {
          setDrawerUsername(null)
          setDrawerDetail(null)
        }}
        side="right"
        title={
          <span className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-primary" />
            <span>成员档案与治理 — {drawerUsername}</span>
          </span>
        }
        subtitle={
          drawerDetail
            ? `已用 ${formatSize(drawerDetail.user.used_bytes)} · ${drawerDetail.user.file_count} 文件 · 会话版本 v${
                drawerDetail.user.session_version || 1
              }`
            : '正在读取详情...'
        }
      >
        {drawerLoading || !drawerDetail ? (
          <div className="py-12 text-center text-xs text-muted-foreground">正在加载成员详细信息...</div>
        ) : (
          <div className="space-y-5 text-xs pb-6">
            {/* Summary Pills */}
            <div className="grid grid-cols-2 gap-2">
              <div className="rounded-lg border border-border bg-muted/25 p-2.5">
                <div className="text-[11px] text-muted-foreground">最近登录</div>
                <div className="mt-0.5 font-medium">
                  {drawerDetail.user.last_login_at ? formatTime(drawerDetail.user.last_login_at) : '从未登录'}
                </div>
                <div className="text-[10px] font-mono text-muted-foreground">
                  {drawerDetail.user.last_login_ip || '-'}
                </div>
              </div>
              <div className="rounded-lg border border-border bg-muted/25 p-2.5">
                <div className="text-[11px] text-muted-foreground">有效期状态</div>
                <div className="mt-0.5 font-medium">
                  {formatExpiryShort(drawerDetail.user.expires_at, drawerDetail.user.expired)}
                </div>
                <div className="text-[10px] text-muted-foreground">
                  创建于 {formatTime(drawerDetail.user.created_at)}
                </div>
              </div>
            </div>

            {/* Edit Form */}
            <form onSubmit={submitDrawerSave} className="space-y-3 rounded-lg border border-border p-3">
              <div className="flex items-center justify-between">
                <span className="font-semibold flex items-center gap-1.5">
                  <Shield className="h-3.5 w-3.5 text-primary" />
                  <span>资料、角色、配额与权限范围</span>
                </span>
                <Badge variant={statusMeta(drawerDetail.user).variant}>
                  {statusMeta(drawerDetail.user).label}
                </Badge>
              </div>

              <div className="grid gap-2.5 sm:grid-cols-2">
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">显示名称</label>
                  <Input
                    value={eDisplayName}
                    onChange={(e) => setEDisplayName(e.target.value)}
                    placeholder="成员显示名"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">角色身份</label>
                  <Select
                    value={eRole}
                    disabled={drawerDetail.user.username === currentUser.username}
                    onChange={(e) => {
                      const nextRole = e.target.value as UserRole
                      setERole(nextRole)
                      if (nextRole === 'readonly') {
                        setECanUpload(false)
                        setECanManageFolders(false)
                      }
                    }}
                  >
                    <option value="admin">管理员</option>
                    <option value="user">普通成员</option>
                    <option value="readonly">只读访客</option>
                  </Select>
                </div>
              </div>

              <div className="space-y-1">
                <label className="text-[11px] text-muted-foreground">治理备注</label>
                <Input value={eNote} onChange={(e) => setENote(e.target.value)} placeholder="备注用途或部门" />
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] text-muted-foreground">个人空间配额（GB，0 = 不限）</label>
                <Input
                  type="number"
                  min="0"
                  step="0.5"
                  value={eQuotaGb}
                  onChange={(e) => setEQuotaGb(e.target.value)}
                />
                <div className="flex flex-wrap gap-1">
                  {['0', '1', '5', '10', '50'].map((p) => (
                    <button
                      key={p}
                      type="button"
                      onClick={() => setEQuotaGb(p)}
                      className={cn(
                        'rounded border px-2 py-0.5 text-[11px] cursor-pointer',
                        eQuotaGb === p
                          ? 'border-primary bg-primary text-primary-foreground'
                          : 'border-border bg-muted/40 hover:bg-muted'
                      )}
                    >
                      {p === '0' ? '不限' : `${p} GB`}
                    </button>
                  ))}
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] text-muted-foreground">调整账号有效期</label>
                <div className="flex flex-wrap items-center gap-1.5">
                  <Select
                    value={eExpireAction}
                    onChange={(e) => setEExpireAction(e.target.value as 'keep' | 'clear' | 'hours')}
                    className="flex-1 min-w-[140px]"
                  >
                    <option value="keep">保持当前有效期不变</option>
                    <option value="clear">设为永久有效</option>
                    <option value="hours">从现在起重新设定小时数...</option>
                  </Select>
                  {eExpireAction === 'hours' && (
                    <Input
                      type="number"
                      min="1"
                      value={eExpireHours}
                      onChange={(e) => setEExpireHours(e.target.value)}
                      className="w-24"
                      placeholder="小时"
                    />
                  )}
                </div>
                {eExpireAction === 'hours' && (
                  <div className="flex flex-wrap gap-1">
                    {[
                      { label: '24 小时', h: '24' },
                      { label: '7 天', h: '168' },
                      { label: '30 天', h: '720' },
                    ].map((x) => (
                      <button
                        key={x.h}
                        type="button"
                        onClick={() => setEExpireHours(x.h)}
                        className="rounded border border-border bg-muted/30 px-2 py-0.5 text-[11px] hover:bg-muted cursor-pointer"
                      >
                        {x.label}
                      </button>
                    ))}
                  </div>
                )}
              </div>

              <div className="space-y-2 rounded-lg border border-border bg-muted/20 p-2.5">
                <div className="flex items-center justify-between gap-2">
                  <span>允许上传与编辑文件</span>
                  <Switch checked={eCanUpload} onCheckedChange={setECanUpload} />
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span>允许创建/重命名/删除文件夹</span>
                  <Switch checked={eCanManageFolders} onCheckedChange={setECanManageFolders} />
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span>允许使用 AI Agent 凭证与 MCP</span>
                  <Switch checked={eCanUseAgents} onCheckedChange={setECanUseAgents} />
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span>强制成员下次登录改密</span>
                  <Switch checked={eMustChangePwd} onCheckedChange={setEMustChangePwd} />
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] text-muted-foreground">
                  目录白名单（留空代表允许访问全量可见目录）
                </label>
                <Textarea
                  rows={2}
                  value={eAllowedFolders}
                  onChange={(e) => setEAllowedFolders(e.target.value)}
                  placeholder="逗号或换行分隔，例如：项目归档, 设计部/终稿"
                />
                {folders.length > 0 && (
                  <div className="flex flex-wrap gap-1">
                    {folders.slice(0, 12).map((fp) => {
                      const active = parseAllowedFoldersText(eAllowedFolders).includes(fp)
                      return (
                        <button
                          key={fp}
                          type="button"
                          onClick={() => setEAllowedFolders((prev) => toggleFolderInText(prev, fp))}
                          className={cn(
                            'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] cursor-pointer',
                            active
                              ? 'border-primary bg-primary/15 text-primary font-medium'
                              : 'border-border bg-muted/30 text-muted-foreground'
                          )}
                        >
                          {active && <Check className="h-2.5 w-2.5" />}
                          <span>/{fp}</span>
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>

              <Button type="submit" size="sm" className="w-full" disabled={drawerSaving}>
                {drawerSaving ? '保存中...' : '保存成员配置'}
              </Button>
            </form>

            {/* Quick Security Controls */}
            {drawerDetail.user.username !== currentUser.username && (
              <div className="rounded-lg border border-border p-3 space-y-2">
                <div className="font-semibold">安全控制与状态切换</div>
                <div className="flex flex-wrap gap-1.5">
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => openResetPasswordModal(drawerDetail.user)}
                  >
                    <KeyRound className="h-3 w-3" />
                    <span>重置密码</span>
                  </Button>
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => void handleForceLogout(drawerDetail.user)}
                  >
                    <LogOut className="h-3 w-3" />
                    <span>强制下线</span>
                  </Button>
                  <Button
                    size="xs"
                    variant={drawerDetail.user.status === 'banned' ? 'outline' : 'destructive'}
                    onClick={async () => {
                      if (drawerDetail.user.status === 'banned') {
                        await api.unbanUser(drawerDetail.user.username)
                        toast(`已解封 ${drawerDetail.user.username}`, 'success')
                      } else {
                        await api.banUser(drawerDetail.user.username)
                        toast(`已封禁 ${drawerDetail.user.username}`, 'success')
                      }
                      await notifyMutation()
                      await openDetailDrawer(drawerDetail.user.username)
                    }}
                  >
                    {drawerDetail.user.status === 'banned' ? (
                      <>
                        <Unlock className="h-3 w-3" />
                        <span>解除封禁</span>
                      </>
                    ) : (
                      <>
                        <Lock className="h-3 w-3" />
                        <span>封禁账号</span>
                      </>
                    )}
                  </Button>
                  <Button
                    size="xs"
                    variant="ghost"
                    className="text-destructive"
                    onClick={() => openDeleteModal(drawerDetail.user)}
                  >
                    <Trash2 className="h-3 w-3" />
                    <span>删除成员</span>
                  </Button>
                </div>
              </div>
            )}

            {/* Owned AI Agent Keys */}
            <div className="rounded-lg border border-border p-3 space-y-2.5">
              <div className="flex items-center justify-between gap-2">
                <span className="font-semibold flex items-center gap-1.5">
                  <Bot className="h-3.5 w-3.5 text-primary" />
                  <span>名下 AI Agent 凭证 ({drawerDetail.agent_keys.length})</span>
                </span>
                {drawerDetail.agent_keys.length > 0 && (
                  <div className="flex items-center gap-1">
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={agentFreezeBusy}
                      onClick={() => void handleFreezeAgents(true)}
                    >
                      冻结全部
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={agentFreezeBusy}
                      onClick={() => void handleFreezeAgents(false)}
                    >
                      恢复全部
                    </Button>
                  </div>
                )}
              </div>
              {drawerDetail.agent_keys.length === 0 ? (
                <div className="text-[11px] text-muted-foreground">该成员暂未签发任何 AI Agent Key</div>
              ) : (
                <div className="space-y-1.5">
                  {drawerDetail.agent_keys.map((ak: AgentKeyItem) => (
                    <div
                      key={ak.id}
                      className="rounded-md border border-border bg-muted/25 px-2.5 py-2 flex items-start justify-between gap-2"
                    >
                      <div className="min-w-0">
                        <div className="flex items-center gap-1.5">
                          <span className="font-medium truncate">{ak.display_alias || ak.name}</span>
                          <Badge variant={ak.disabled ? 'destructive' : 'success'}>
                            {ak.disabled ? '已冻结' : '已启用'}
                          </Badge>
                        </div>
                        <div className="mt-0.5 text-[10px] text-muted-foreground font-mono">
                          {ak.token_prefix}... · {ak.scopes.join('/')}
                        </div>
                        {ak.last_used_at && (
                          <div className="text-[10px] text-muted-foreground">
                            最近使用 {formatTime(ak.last_used_at)} ({ak.last_used_ip || '-'})
                          </div>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Member Audit Timeline */}
            <div className="rounded-lg border border-border p-3 space-y-2">
              <div className="font-semibold flex items-center gap-1.5">
                <History className="h-3.5 w-3.5 text-primary" />
                <span>个人行为审计时间线 ({drawerDetail.audit_entries.length})</span>
              </div>
              {drawerDetail.audit_entries.length === 0 ? (
                <div className="text-[11px] text-muted-foreground">暂无相关审计日志</div>
              ) : (
                <div className="space-y-1.5 max-h-64 overflow-y-auto pr-1">
                  {drawerDetail.audit_entries.map((ev: AuditEntry, idx: number) => (
                    <div
                      key={ev.id || `${ev.action}-${idx}`}
                      className="rounded-md border border-border/70 bg-muted/20 px-2.5 py-1.5 space-y-0.5"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-medium text-foreground">{ev.action}</span>
                        <Badge variant={ev.status === 'ok' ? 'success' : 'destructive'}>{ev.status}</Badge>
                      </div>
                      {ev.target && <div className="text-[10px] text-muted-foreground truncate">目标: {ev.target}</div>}
                      {ev.detail && <div className="text-[10px] text-muted-foreground truncate">{ev.detail}</div>}
                      <div className="text-[10px] text-muted-foreground">
                        {formatTime(ev.timestamp || ev.time || ev.created_at)} · {ev.ip || '-'}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}
      </Sheet>
    </div>
  )
}
