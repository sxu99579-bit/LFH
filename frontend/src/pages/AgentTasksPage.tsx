import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertTriangle,
  AtSign,
  Bot,
  CalendarClock,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Circle,
  Clock,
  Copy,
  Download,
  Eye,
  FileText,
  Filter,
  Folder,
  GitBranch,
  Info,
  ListTodo,
  Loader2,
  MessageSquare,
  Paintbrush,
  PauseCircle,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Send,
  Terminal,
  Trash2,
  User as UserIcon,
  X,
} from 'lucide-react'
import { api } from '@/lib/api'
import { canPreviewFile, cn, copyTextToClipboard, formatRelativeTime, formatSize } from '@/lib/utils'
import type {
  AgentKeyItem,
  AgentPresenceItem,
  AgentTaskComment,
  AgentTaskItem,
  AgentTaskPriority,
  AgentTaskStatus,
  AgentTaskSummary,
  FileRecord,
  ServerInfo,
  User,
} from '@/types'
import {
  AgentAvatarBadge,
  AgentProfileModal,
  DeviceMiniIcon,
  agentDeviceLabel,
  agentDisplayName,
  deviceKindShortLabel,
  ownerRingColor,
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

interface AgentTasksPageProps {
  user: User
  info: ServerInfo | null
  files: FileRecord[]
  folders: string[]
  isMobile: boolean
  onSummaryChange?: (summary: AgentTaskSummary) => void
  onOpenAgentSettings?: () => void
  onPreviewFile: (rec: FileRecord) => void
  onDownloadFile: (rec: FileRecord) => void
  onInspectFile: (rec: FileRecord) => void
}

type StatusFilter = 'all' | 'pending' | 'in_progress' | 'scheduled' | 'paused' | 'done'
type SchedulePreset = 'now' | '5m' | '30m' | '1h' | 'custom'
type SetupTab = 'ps1' | 'sh' | 'schedule'

function formatLocalDateTime(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  return `${mm}-${dd} ${hh}:${mi}`
}

function formatScheduledCountdown(iso?: string | null): string {
  if (!iso) return ''
  const target = new Date(iso).getTime()
  if (Number.isNaN(target)) return ''
  const diffSec = Math.round((target - Date.now()) / 1000)
  if (diffSec <= 0) return '即将激活'
  if (diffSec < 60) return `${diffSec} 秒后激活`
  if (diffSec < 3600) return `${Math.ceil(diffSec / 60)} 分钟后激活`
  const h = Math.floor(diffSec / 3600)
  const m = Math.ceil((diffSec % 3600) / 60)
  return m > 0 ? `${h}小时${m}分后激活` : `${h} 小时后激活`
}

function statusBadgeInfo(status: AgentTaskStatus): {
  label: string
  variant: 'default' | 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'
} {
  switch (status) {
    case 'scheduled':
      return { label: '定时等待', variant: 'secondary' }
    case 'pending':
      return { label: '待抢单', variant: 'warning' }
    case 'in_progress':
      return { label: '协同执行中', variant: 'default' }
    case 'paused':
      return { label: '已暂停 · 需确认', variant: 'destructive' }
    case 'done':
      return { label: '已完成', variant: 'success' }
    case 'cancelled':
      return { label: '已取消', variant: 'outline' }
    default:
      return { label: status, variant: 'secondary' }
  }
}

function priorityBadgeInfo(priority: AgentTaskPriority): {
  label: string
  variant: 'warning' | 'destructive'
} | null {
  if (priority === 'urgent') return { label: '紧急', variant: 'destructive' }
  if (priority === 'high') return { label: '高优', variant: 'warning' }
  return null
}

function presenceLabel(status: string): { label: string; badgeVariant: 'success' | 'default' | 'secondary' | 'outline' } {
  if (status === 'working') return { label: '干活中', badgeVariant: 'success' }
  if (status === 'watching') return { label: '值守待命', badgeVariant: 'default' }
  if (status === 'online') return { label: '在线', badgeVariant: 'success' }
  return { label: '离线', badgeVariant: 'outline' }
}

function renderCommentBody(body: string) {
  const parts = body.split(/(@[^\s@，,。.；;！!？?]+)/g)
  return parts.map((part, idx) => {
    if (part.startsWith('@') && part.length > 1) {
      return (
        <span
          key={idx}
          className="inline-block rounded bg-primary/15 px-1 py-0.5 font-medium text-primary"
        >
          {part}
        </span>
      )
    }
    return <span key={idx}>{part}</span>
  })
}

export function AgentTasksPage({
  user,
  info,
  files,
  folders,
  isMobile,
  onSummaryChange,
  onOpenAgentSettings,
  onPreviewFile,
  onDownloadFile,
  onInspectFile,
}: AgentTasksPageProps) {
  const { toast } = useToast()

  const [tasks, setTasks] = useState<AgentTaskItem[]>([])
  const [summary, setSummary] = useState<AgentTaskSummary>({
    total: 0,
    scheduled: 0,
    pending: 0,
    in_progress: 0,
    paused: 0,
    done: 0,
    claimable_for_me: 0,
    mentioned_for_me: 0,
  })
  const [agents, setAgents] = useState<AgentPresenceItem[]>([])
  const [loading, setLoading] = useState(true)
  const [busyTaskId, setBusyTaskId] = useState('')

  // Filters
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [agentFilterId, setAgentFilterId] = useState('')
  const [searchQuery, setSearchQuery] = useState('')

  // Composer state
  const [composerExpanded, setComposerExpanded] = useState(true)
  const [newTitle, setNewTitle] = useState('')
  const [newDesc, setNewDesc] = useState('')
  const [newPriority, setNewPriority] = useState<AgentTaskPriority>('normal')
  const [newAssignedId, setNewAssignedId] = useState('')
  const [newFolderPrefix, setNewFolderPrefix] = useState('')
  const [newAllowSplit, setNewAllowSplit] = useState(true)
  const [schedulePreset, setSchedulePreset] = useState<SchedulePreset>('now')
  const [customScheduleAt, setCustomScheduleAt] = useState('')
  const [creatingTask, setCreatingTask] = useState(false)
  const [composerMentionQuery, setComposerMentionQuery] = useState<string | null>(null)
  const focusTitleInput = () => {
    window.setTimeout(() => {
      document.getElementById('lfh-task-title-input')?.focus()
    }, 0)
  }

  // Task card comment inputs & expanded comment threads
  const [commentDrafts, setCommentDrafts] = useState<Record<string, string>>({})
  const [commentMentionTarget, setCommentMentionTarget] = useState<{ taskId: string; query: string } | null>(null)
  const [submittingCommentTaskId, setSubmittingCommentTaskId] = useState('')
  const [expandedComments, setExpandedComments] = useState<Record<string, boolean>>({})

  // Profile customization modal
  const [profileEditingKey, setProfileEditingKey] = useState<AgentKeyItem | null>(null)

  // Worker / Schedule onboarding modal
  const [setupOpen, setSetupOpen] = useState(false)
  const [setupTab, setSetupTab] = useState<SetupTab>(() => {
    if (typeof navigator !== 'undefined' && /win/i.test(navigator.userAgent)) return 'ps1'
    return 'sh'
  })
  const [setupIntervalSec, setSetupIntervalSec] = useState('60')
  const [setupCustomPrompt, setSetupCustomPrompt] = useState('')

  const addressOptions = useMemo(() => {
    const out: Array<{ label: string; url: string }> = []
    const seen = new Set<string>()
    const add = (label: string, raw: string) => {
      const clean = String(raw || '').trim().replace(/\/+$/, '')
      if (!clean || seen.has(clean)) return
      seen.add(clean)
      out.push({ label, url: clean })
    }
    if (typeof window !== 'undefined' && window.location?.origin) {
      add('当前浏览器地址', window.location.origin)
    }
    if (info?.entry_url) add('主节点默认地址', info.entry_url)
    for (const u of info?.entry_urls || []) {
      add(`节点地址 (${u})`, u)
    }
    return out
  }, [info])

  const [selectedBaseUrl, setSelectedBaseUrl] = useState('')
  useEffect(() => {
    if (!selectedBaseUrl && addressOptions.length > 0) {
      setSelectedBaseUrl(addressOptions[0].url)
    }
  }, [addressOptions, selectedBaseUrl])

  const effectiveBaseUrl = (selectedBaseUrl || 'http://127.0.0.1:7070').replace(/\/+$/, '')

  const loadBoardData = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const [taskRes, sessRes] = await Promise.all([
          api.listAgentTasks({ limit: 200 }),
          api.getAgentSessions().catch(() => null),
        ])
        setTasks(taskRes.tasks || [])
        setSummary(taskRes.summary)
        onSummaryChange?.(taskRes.summary)
        if (sessRes?.agents) {
          setAgents(sessRes.agents)
        }
      } catch (err) {
        if (!silent) {
          toast(err instanceof Error ? err.message : '加载 AI 任务板失败', 'error')
        }
      } finally {
        if (!silent) setLoading(false)
      }
    },
    [onSummaryChange, toast]
  )

  useEffect(() => {
    void loadBoardData(false)
  }, [loadBoardData])

  // Real-time SSE updates dispatched by App.tsx
  useEffect(() => {
    const onAgentEvent = (ev: Event) => {
      const detail = (ev as CustomEvent).detail as { kind?: string } | undefined
      if (
        detail?.kind === 'agent_task' ||
        detail?.kind === 'agent_profile' ||
        detail?.kind === 'agent_status'
      ) {
        void loadBoardData(true)
      }
    }
    window.addEventListener('lfh:agent-event', onAgentEvent)
    return () => window.removeEventListener('lfh:agent-event', onAgentEvent)
  }, [loadBoardData])

  // Tick every 20s only when scheduled tasks exist so countdown labels stay fresh
  const hasScheduledTasks = useMemo(() => tasks.some((t) => t.status === 'scheduled'), [tasks])
  const [, setClockTick] = useState(0)
  useEffect(() => {
    if (!hasScheduledTasks) return
    const timer = window.setInterval(() => setClockTick((n) => n + 1), 20000)
    return () => window.clearInterval(timer)
  }, [hasScheduledTasks])

  // Build quick lookup tables for agent badges & @matching
  const agentLookup = useMemo(() => {
    const byId = new Map<string, AgentPresenceItem>()
    const byRef = new Map<string, AgentPresenceItem>()
    for (const item of agents) {
      byId.set(item.key.id, item)
      byRef.set(item.key.id.toLowerCase(), item)
      byRef.set(item.key.name.toLowerCase(), item)
      if (item.key.display_alias?.trim()) {
        byRef.set(item.key.display_alias.trim().toLowerCase(), item)
      }
      byRef.set(`${item.key.owner_username}-${item.key.name}`.toLowerCase(), item)
    }
    return { byId, byRef }
  }, [agents])

  const findAgent = useCallback(
    (ref?: string): AgentPresenceItem | undefined => {
      const clean = String(ref || '').trim().replace(/^@+/, '').toLowerCase()
      if (!clean) return undefined
      return agentLookup.byRef.get(clean)
    },
    [agentLookup]
  )

  const sortedAgents = useMemo(() => {
    const score = (a: AgentPresenceItem) => {
      if (a.key.disabled) return 0
      if (a.status === 'working') return 4
      if (a.status === 'watching') return 3
      if (a.online) return 2
      return 1
    }
    return [...agents].sort((a, b) => score(b) - score(a))
  }, [agents])

  // Owner color legend built from existing agents + current user
  const ownerLegend = useMemo(() => {
    const names = new Set<string>()
    if (user.username) names.add(user.username)
    for (const a of agents) {
      if (a.key.owner_username) names.add(a.key.owner_username)
    }
    return Array.from(names).map((name) => ({
      username: name,
      color: ownerRingColor(name),
    }))
  }, [agents, user.username])

  const selectedAssignedAgent = useMemo(
    () => (newAssignedId ? agentLookup.byId.get(newAssignedId) : undefined),
    [agentLookup.byId, newAssignedId]
  )

  const filteredAgentsForComposer = useMemo(() => {
    if (composerMentionQuery === null) return []
    const q = composerMentionQuery.trim().toLowerCase()
    return sortedAgents.filter((item) => {
      if (!q) return true
      const hay = [
        item.key.name,
        item.key.display_alias || '',
        item.key.owner_username,
        item.key.device_alias || '',
      ]
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [composerMentionQuery, sortedAgents])

  const handleTitleChange = (val: string) => {
    setNewTitle(val)
    const match = val.match(/(?:^|\s)@([^\s@]*)$/)
    if (match) {
      setComposerMentionQuery(match[1])
    } else {
      setComposerMentionQuery(null)
    }
  }

  const pickComposerMentionAgent = (item: AgentPresenceItem) => {
    const label = agentDisplayName(item.key)
    setNewAssignedId(item.key.id)
    if (/@[^\s@]*$/.test(newTitle)) {
      setNewTitle((prev) => prev.replace(/@[^\s@]*$/, `@${label} `))
    }
    setComposerMentionQuery(null)
    focusTitleInput()
  }

  const computeScheduledAtISO = (): string | undefined => {
    if (schedulePreset === 'now') return undefined
    if (schedulePreset === '5m') return new Date(Date.now() + 5 * 60 * 1000).toISOString()
    if (schedulePreset === '30m') return new Date(Date.now() + 30 * 60 * 1000).toISOString()
    if (schedulePreset === '1h') return new Date(Date.now() + 60 * 60 * 1000).toISOString()
    if (schedulePreset === 'custom' && customScheduleAt) {
      const d = new Date(customScheduleAt)
      if (!Number.isNaN(d.getTime())) return d.toISOString()
    }
    return undefined
  }

  const handleCreateTask = async (e: React.FormEvent) => {
    e.preventDefault()
    const cleanTitle = newTitle.trim()
    if (!cleanTitle) {
      toast('请输入任务标题', 'error')
      return
    }
    const scheduledISO = computeScheduledAtISO()
    if (schedulePreset === 'custom' && !scheduledISO) {
      toast('请选择有效的定时触发时间', 'error')
      return
    }

    setCreatingTask(true)
    try {
      const targetAgent = newAssignedId ? agentLookup.byId.get(newAssignedId) : undefined
      await api.createAgentTask({
        title: cleanTitle,
        description: newDesc.trim() || undefined,
        priority: newPriority,
        assigned_agent: targetAgent?.key.id || undefined,
        assigned_agent_label: targetAgent ? agentDisplayName(targetAgent.key) : undefined,
        folder_prefix: newFolderPrefix.trim() || undefined,
        allow_split: newAllowSplit,
        scheduled_at: scheduledISO,
      })
      toast(scheduledISO ? '已加入定时任务队列，到期后自动通知 Agent 抢单' : '任务已发布到协同留言板', 'success')
      setNewTitle('')
      setNewDesc('')
      setNewAssignedId('')
      setNewPriority('normal')
      setSchedulePreset('now')
      setCustomScheduleAt('')
      setComposerMentionQuery(null)
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '发布任务失败', 'error')
    } finally {
      setCreatingTask(false)
    }
  }

  const handleActivateTask = async (task: AgentTaskItem) => {
    setBusyTaskId(task.id)
    try {
      await api.activateAgentTask(task.id)
      toast(`已立即激活「${task.title}」`, 'success')
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '激活失败', 'error')
    } finally {
      setBusyTaskId('')
    }
  }

  const handleReopenTask = async (task: AgentTaskItem) => {
    setBusyTaskId(task.id)
    try {
      await api.reopenAgentTask(task.id)
      toast(`已重新激活「${task.title}」`, 'success')
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '重新激活失败', 'error')
    } finally {
      setBusyTaskId('')
    }
  }

  const handleSetTaskStatus = async (task: AgentTaskItem, status: 'done' | 'cancelled') => {
    setBusyTaskId(task.id)
    try {
      await api.updateAgentTask(task.id, { status })
      toast(status === 'done' ? '已手动标记任务完成' : '已取消该任务', 'success')
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '状态更新失败', 'error')
    } finally {
      setBusyTaskId('')
    }
  }

  const handleDeleteTask = async (task: AgentTaskItem) => {
    if (!window.confirm(`确定删除任务「${task.title}」？相关子任务与讨论楼将一并清除。`)) return
    setBusyTaskId(task.id)
    try {
      await api.deleteAgentTask(task.id)
      toast('任务已删除', 'info')
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除失败', 'error')
    } finally {
      setBusyTaskId('')
    }
  }

  const handleCommentInputChange = (taskId: string, val: string) => {
    setCommentDrafts((prev) => ({ ...prev, [taskId]: val }))
    const match = val.match(/(?:^|\s)@([^\s@]*)$/)
    if (match) {
      setCommentMentionTarget({ taskId, query: match[1] })
    } else if (commentMentionTarget?.taskId === taskId) {
      setCommentMentionTarget(null)
    }
  }

  const pickCommentMention = (taskId: string, item: AgentPresenceItem) => {
    const label = agentDisplayName(item.key)
    setCommentDrafts((prev) => {
      const current = prev[taskId] || ''
      const replaced = /@[^\s@]*$/.test(current)
        ? current.replace(/@[^\s@]*$/, `@${label} `)
        : `${current}${current && !current.endsWith(' ') ? ' ' : ''}@${label} `
      return { ...prev, [taskId]: replaced }
    })
    setCommentMentionTarget(null)
  }

  const handlePostComment = async (taskId: string) => {
    const body = (commentDrafts[taskId] || '').trim()
    if (!body) return
    setSubmittingCommentTaskId(taskId)
    try {
      await api.postAgentTaskComment(taskId, body)
      setCommentDrafts((prev) => ({ ...prev, [taskId]: '' }))
      setCommentMentionTarget(null)
      setExpandedComments((prev) => ({ ...prev, [taskId]: true }))
      await loadBoardData(true)
    } catch (err) {
      toast(err instanceof Error ? err.message : '发送留言失败', 'error')
    } finally {
      setSubmittingCommentTaskId('')
    }
  }

  const filteredTasks = useMemo(() => {
    const q = searchQuery.trim().toLowerCase()
    const filterAgent = agentFilterId ? agentLookup.byId.get(agentFilterId) : undefined
    const filterAliases = filterAgent
      ? new Set(
          [
            filterAgent.key.id,
            filterAgent.key.name,
            filterAgent.key.display_alias || '',
            `${filterAgent.key.owner_username}-${filterAgent.key.name}`,
          ]
            .map((s) => s.trim().toLowerCase())
            .filter(Boolean)
        )
      : null

    return tasks.filter((t) => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false
      if (filterAliases) {
        const hitAssigned = filterAliases.has((t.assigned_agent || '').toLowerCase())
        const hitClaimer = filterAliases.has((t.claimed_by_id || '').toLowerCase())
        const hitSub = (t.subtasks || []).some(
          (st) =>
            filterAliases.has((st.assigned_agent || '').toLowerCase()) ||
            filterAliases.has((st.claimed_by_id || '').toLowerCase())
        )
        const hitMention = (t.comments || []).some((c) =>
          (c.mentions || []).some((m) => filterAliases.has(m.replace(/^@/, '').toLowerCase()))
        )
        if (!hitAssigned && !hitClaimer && !hitSub && !hitMention) return false
      }
      if (q) {
        const hay = [
          t.title,
          t.description || '',
          t.folder_prefix || '',
          t.assigned_agent_label || '',
          t.claimed_by_name || '',
          ...(t.subtasks || []).map((st) => st.title),
        ]
          .join(' ')
          .toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [agentFilterId, agentLookup.byId, searchQuery, statusFilter, tasks])

  const workerSnippet = useMemo(() => {
    const interval = Math.max(10, Number(setupIntervalSec || 60))
    const promptPart = setupCustomPrompt.trim()
      ? `&prompt=${encodeURIComponent(setupCustomPrompt.trim())}`
      : ''
    if (setupTab === 'ps1') {
      return `irm "${effectiveBaseUrl}/agent/worker.ps1?interval=${interval}${promptPart}" | iex`
    }
    if (setupTab === 'sh') {
      return `curl -fsSL "${effectiveBaseUrl}/agent/worker.sh?interval=${interval}${promptPart}" | sh`
    }
    return `/schedule 请连接 LFH MCP 服务（list_tasks）检查「${effectiveBaseUrl}」上的 AI 任务板：
1. 若有指派给我或公开待抢单（claimable_for_me > 0）或 @提及我的任务，立即用 claim_task 接单并按要求执行；
2. 若任务复杂（allow_split=true），用 split_task 拆分子任务并与其它 Agent 通过 post_task_comment @接力协同；
3. 完成后提交成果文件并调用 complete_task 自动勾选完成；若无待办则保持静默等待下次巡检。`
  }, [effectiveBaseUrl, setupCustomPrompt, setupIntervalSec, setupTab])

  const activeFilterAgent = agentFilterId ? agentLookup.byId.get(agentFilterId) : undefined

  const resolveOutputFile = useCallback(
    (refPath: string): FileRecord | undefined => {
      const clean = String(refPath || '').trim().replace(/\\/g, '/').replace(/^\/+/, '')
      if (!clean) return undefined
      const byId = files.find((f) => f.id === clean)
      if (byId) return byId
      const byExactPath = files.find((f) => {
        const fp = String(f.folder_path || '').replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
        const full = fp ? `${fp}/${f.name}` : f.name
        return full === clean
      })
      if (byExactPath) return byExactPath
      const byName = files.find((f) => f.name === clean)
      if (byName) return byName
      const base = clean.split('/').pop() || clean
      return files.find((f) => f.name === base)
    },
    [files]
  )

  const renderOutputFileChip = (refPath: string, keyVal: string | number) => {
    const matched = resolveOutputFile(refPath)
    if (!matched) {
      return (
        <span
          key={keyVal}
          className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-500/30 bg-background px-2.5 py-1 text-[11px] font-mono text-foreground shadow-2xs"
        >
          <FileText className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400 shrink-0" />
          <span className="break-all">{refPath}</span>
          <button
            type="button"
            onClick={async () => {
              const ok = await copyTextToClipboard(refPath)
              toast(ok ? '已复制文件路径' : '复制失败', ok ? 'success' : 'error')
            }}
            className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer"
            title="复制路径"
          >
            <Copy className="h-3 w-3" />
          </button>
        </span>
      )
    }

    const previewable = canPreviewFile(matched)
    return (
      <span
        key={keyVal}
        className="inline-flex flex-wrap items-center gap-1.5 rounded-lg border border-emerald-500/35 bg-background px-2.5 py-1 text-[11px] text-foreground shadow-2xs"
      >
        <FileText className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400 shrink-0" />
        <button
          type="button"
          onClick={() => (previewable ? onPreviewFile(matched) : onInspectFile(matched))}
          className="font-medium hover:text-primary hover:underline break-all text-left cursor-pointer"
          title="点击直接打开"
        >
          {matched.folder_path ? `${matched.folder_path}/${matched.name}` : matched.name}
        </button>
        <span className="text-[10px] text-muted-foreground tabular-nums">
          ({formatSize(matched.size)})
        </span>
        <span className="inline-flex items-center gap-0.5 ml-0.5 border-l border-border/70 pl-1.5">
          {previewable && (
            <button
              type="button"
              onClick={() => onPreviewFile(matched)}
              className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-primary hover:bg-primary/10 cursor-pointer"
              title="预览文件"
            >
              <Eye className="h-3 w-3" />
              <span>预览</span>
            </button>
          )}
          <button
            type="button"
            onClick={() => onDownloadFile(matched)}
            className="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium hover:bg-accent cursor-pointer"
            title="下载文件"
          >
            <Download className="h-3 w-3" />
            <span>下载</span>
          </button>
          <button
            type="button"
            onClick={() => onInspectFile(matched)}
            className="inline-flex items-center rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer"
            title="查看文件详情与版本"
          >
            <Info className="h-3 w-3" />
          </button>
        </span>
      </span>
    )
  }

  return (
    <div className="space-y-5">
      {/* Page Header */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary">
              <ListTodo className="h-4.5 w-4.5" />
            </div>
            <h1 className="text-lg sm:text-xl font-bold tracking-tight">AI 协同任务板</h1>
            <Badge variant="outline" className="hidden sm:inline-flex">
              MCP 25 Tools + Zero-Token Worker
            </Badge>
          </div>
          <p className="text-xs text-muted-foreground">
            发布即时或定时任务，支持 @Agent 卡通工牌定向指派、多电脑 AI 自由抢单与拆块接力协同
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setSetupOpen(true)}>
            <Terminal className="h-3.5 w-3.5 text-primary" />
            <span>零消耗值守 / /schedule</span>
          </Button>
          {onOpenAgentSettings && (
            <Button size="sm" variant="outline" onClick={onOpenAgentSettings}>
              <Bot className="h-3.5 w-3.5" />
              <span>Agent 接入中心</span>
            </Button>
          )}
          <Button size="sm" variant="outline" onClick={() => void loadBoardData(false)} disabled={loading}>
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
            <span>刷新</span>
          </Button>
        </div>
      </div>

      {/* KPI Strip */}
      <div className="grid grid-cols-2 sm:grid-cols-5 gap-2.5">
        <div className="rounded-xl border border-amber-500/30 bg-amber-500/8 px-3.5 py-2.5">
          <div className="text-[11px] text-amber-600 dark:text-amber-400">待认领 / 抢单池</div>
          <div className="mt-0.5 text-xl font-bold tabular-nums">{summary.pending}</div>
        </div>
        <div className="rounded-xl border border-primary/30 bg-primary/8 px-3.5 py-2.5">
          <div className="text-[11px] text-primary">协同执行中</div>
          <div className="mt-0.5 text-xl font-bold tabular-nums">{summary.in_progress}</div>
        </div>
        <div className="rounded-xl border border-violet-500/30 bg-violet-500/8 px-3.5 py-2.5">
          <div className="text-[11px] text-violet-600 dark:text-violet-400">定时等待触发</div>
          <div className="mt-0.5 text-xl font-bold tabular-nums">{summary.scheduled}</div>
        </div>
        <div className="rounded-xl border border-rose-500/30 bg-rose-500/8 px-3.5 py-2.5">
          <div className="text-[11px] text-rose-600 dark:text-rose-400">暂停待确认</div>
          <div className="mt-0.5 text-xl font-bold tabular-nums">{summary.paused}</div>
        </div>
        <div className="col-span-2 sm:col-span-1 rounded-xl border border-emerald-500/30 bg-emerald-500/8 px-3.5 py-2.5">
          <div className="text-[11px] text-emerald-600 dark:text-emerald-400">已完成打勾</div>
          <div className="mt-0.5 text-xl font-bold tabular-nums">{summary.done}</div>
        </div>
      </div>

      {/* Two-Column Workspace */}
      <div className={cn('grid gap-5 items-start', !isMobile && 'lg:grid-cols-[minmax(0,1fr)_320px]')}>
        {/* Left Main Column: Composer + Filters + Task Feed */}
        <div className="space-y-4 min-w-0">
          {/* Task Composer */}
          <Card>
            <CardHeader className="pb-2 flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <Plus className="h-4 w-4 text-primary" />
                  <span>发布待办任务</span>
                </CardTitle>
                <CardDescription>
                  输入标题时输入 <code className="font-mono">@</code> 或点选下方工牌，可直接定向指派到某台电脑的 Agent
                </CardDescription>
              </div>
              <Button
                type="button"
                size="xs"
                variant="ghost"
                onClick={() => setComposerExpanded((v) => !v)}
              >
                {composerExpanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
              </Button>
            </CardHeader>

            {composerExpanded && (
              <CardContent>
                <form onSubmit={handleCreateTask} className="space-y-3 text-xs">
                  {/* Title input + @ mention autocomplete + quick agent pills */}
                  <div className="relative space-y-2">
                    <div className="flex flex-wrap gap-2">
                      <div className="relative flex-1 min-w-[220px]">
                        <Input
                          id="lfh-task-title-input"
                          value={newTitle}
                          onChange={(e) => handleTitleChange(e.target.value)}
                          placeholder="输入任务标题，例如：整理项目周报汇总并输出 Markdown，支持 @ 选 Agent"
                          className="h-9 pr-16 text-sm"
                        />
                        <button
                          type="button"
                          onClick={() => {
                            setComposerMentionQuery((prev) => (prev === null ? '' : null))
                            focusTitleInput()
                          }}
                          className="absolute right-1.5 top-1.5 inline-flex h-6 items-center gap-0.5 rounded-md border border-border bg-muted/70 px-1.5 text-[11px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer"
                          title="按 Agent 选人"
                        >
                          <AtSign className="h-3 w-3 text-primary" />
                          <span>选人</span>
                        </button>
                        {composerMentionQuery !== null && filteredAgentsForComposer.length > 0 && (
                          <div className="absolute left-0 right-0 top-10 z-30 max-h-56 overflow-y-auto rounded-lg border border-border bg-card p-1.5 shadow-xl space-y-1">
                            <div className="px-2 py-0.5 text-[10px] text-muted-foreground">
                              点击选择定向指派对象（按别名 / 密钥名 / 成员 / 电脑匹配）
                            </div>
                            {filteredAgentsForComposer.map((item) => (
                              <button
                                key={item.key.id}
                                type="button"
                                onClick={() => pickComposerMentionAgent(item)}
                                className="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-accent cursor-pointer"
                              >
                                <AgentAvatarBadge
                                  agentKey={item.key}
                                  onlineStatus={item.status}
                                  size="xs"
                                />
                                <div className="min-w-0 flex-1">
                                  <div className="font-medium truncate">
                                    @{agentDisplayName(item.key)}
                                  </div>
                                  <div className="text-[10px] text-muted-foreground truncate">
                                    归属 {item.key.owner_username} ·{' '}
                                    {agentDeviceLabel(item.key) || deviceKindShortLabel(item.key.device_kind)}
                                  </div>
                                </div>
                                <Badge variant={presenceLabel(item.status).badgeVariant}>
                                  {presenceLabel(item.status).label}
                                </Badge>
                              </button>
                            ))}
                          </div>
                        )}
                      </div>

                      <Select
                        value={newAssignedId}
                        onChange={(e) => setNewAssignedId(e.target.value)}
                        className="h-9 text-xs w-full sm:w-52"
                        title="定向指派对象"
                      >
                        <option value="">全员自由抢单（不指定）</option>
                        {sortedAgents.map((item) => (
                          <option key={item.key.id} value={item.key.id}>
                            @{agentDisplayName(item.key)} ({item.key.owner_username})
                          </option>
                        ))}
                      </Select>
                    </div>

                    {sortedAgents.length > 0 && (
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-[11px] text-muted-foreground mr-0.5">快捷指派：</span>
                        {sortedAgents.slice(0, 6).map((item) => {
                          const picked = newAssignedId === item.key.id
                          return (
                            <button
                              key={`quick-${item.key.id}`}
                              type="button"
                              onClick={() => setNewAssignedId((prev) => (prev === item.key.id ? '' : item.key.id))}
                              className={cn(
                                'inline-flex items-center gap-1.5 rounded-full border pl-1 pr-2 py-0.5 text-[11px] transition-colors cursor-pointer',
                                picked
                                  ? 'border-primary bg-primary/12 text-primary font-medium'
                                  : 'border-border bg-background/80 text-muted-foreground hover:bg-accent hover:text-foreground'
                              )}
                            >
                              <AgentAvatarBadge
                                agentKey={item.key}
                                onlineStatus={item.status}
                                size="xs"
                                showDeviceCorner={false}
                              />
                              <span>@{agentDisplayName(item.key)}</span>
                            </button>
                          )
                        })}
                      </div>
                    )}

                    {selectedAssignedAgent && (
                      <div className="flex items-center gap-2 rounded-lg border border-primary/30 bg-primary/8 px-2.5 py-1.5">
                        <AgentAvatarBadge
                          agentKey={selectedAssignedAgent.key}
                          onlineStatus={selectedAssignedAgent.status}
                          size="xs"
                        />
                        <span className="font-medium text-primary">
                          已定向指派给 @{agentDisplayName(selectedAssignedAgent.key)}
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          ({selectedAssignedAgent.key.owner_username} ·{' '}
                          {agentDeviceLabel(selectedAssignedAgent.key) ||
                            deviceKindShortLabel(selectedAssignedAgent.key.device_kind)}
                          )
                        </span>
                        <button
                          type="button"
                          onClick={() => setNewAssignedId('')}
                          className="ml-auto text-muted-foreground hover:text-foreground cursor-pointer"
                          title="改为全员自由抢单"
                        >
                          <X className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    )}
                  </div>

                  <Textarea
                    value={newDesc}
                    onChange={(e) => setNewDesc(e.target.value)}
                    placeholder="补充具体要求、期望产出文件名、验收标准或拆块分工建议（可选）..."
                    className="min-h-[60px] text-xs"
                  />

                  {/* Unified Composer Options Toolbar */}
                  <div className="flex flex-wrap items-center gap-2 rounded-xl border border-border/70 bg-muted/35 p-2">
                    <div className="flex items-center gap-1.5">
                      <Clock className="h-3.5 w-3.5 text-muted-foreground shrink-0 ml-1" />
                      <Select
                        value={schedulePreset}
                        onChange={(e) => setSchedulePreset(e.target.value as SchedulePreset)}
                        className="h-8 text-xs bg-background"
                      >
                        <option value="now">立即发布</option>
                        <option value="5m">5 分钟后激活</option>
                        <option value="30m">30 分钟后激活</option>
                        <option value="1h">1 小时后激活</option>
                        <option value="custom">自定义定时</option>
                      </Select>
                    </div>

                    {schedulePreset === 'custom' && (
                      <Input
                        type="datetime-local"
                        value={customScheduleAt}
                        onChange={(e) => setCustomScheduleAt(e.target.value)}
                        className="h-8 w-48 bg-background text-xs"
                      />
                    )}

                    <Select
                      value={newPriority}
                      onChange={(e) => setNewPriority(e.target.value as AgentTaskPriority)}
                      className="h-8 bg-background text-xs"
                      title="任务优先级"
                    >
                      <option value="normal">普通优先级</option>
                      <option value="high">高优先级</option>
                      <option value="urgent">紧急插队</option>
                    </Select>

                    <div className="relative flex items-center flex-1 sm:flex-initial min-w-[160px]">
                      <Folder className="absolute left-2.5 h-3.5 w-3.5 text-[#4aa1ec] pointer-events-none" />
                      <Input
                        list="lfh-task-folder-list"
                        value={newFolderPrefix}
                        onChange={(e) => setNewFolderPrefix(e.target.value)}
                        placeholder="选择或输入目录前缀（可选）"
                        className="h-8 w-full sm:w-48 bg-background pl-8 text-xs font-mono"
                      />
                      <datalist id="lfh-task-folder-list">
                        {folders.map((fp) => (
                          <option key={fp} value={fp} />
                        ))}
                      </datalist>
                    </div>

                    <div className="flex h-8 items-center gap-2 rounded-md border border-border bg-background px-2.5">
                      <GitBranch className="h-3.5 w-3.5 text-primary" />
                      <span>允许拆块协同</span>
                      <Switch checked={newAllowSplit} onCheckedChange={setNewAllowSplit} />
                    </div>

                    <Button type="submit" size="sm" disabled={creatingTask} className="h-8 w-full sm:w-auto sm:ml-auto">
                      {creatingTask ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : schedulePreset !== 'now' ? (
                        <CalendarClock className="h-3.5 w-3.5" />
                      ) : (
                        <Send className="h-3.5 w-3.5" />
                      )}
                      <span>
                        {creatingTask
                          ? '发布中...'
                          : schedulePreset !== 'now'
                          ? '预约定时任务'
                          : '发布待办'}
                      </span>
                    </Button>
                  </div>
                </form>
              </CardContent>
            )}
          </Card>

          {/* Filter Bar */}
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border bg-card p-2.5">
            <div className="flex flex-wrap items-center gap-1">
              {(
                [
                  { id: 'all', label: `全部 (${summary.total})` },
                  { id: 'pending', label: `待抢单 (${summary.pending})` },
                  { id: 'in_progress', label: `进行中 (${summary.in_progress})` },
                  { id: 'scheduled', label: `定时 (${summary.scheduled})` },
                  { id: 'paused', label: `暂停 (${summary.paused})` },
                  { id: 'done', label: `已完成 (${summary.done})` },
                ] as Array<{ id: StatusFilter; label: string }>
              ).map((tab) => (
                <Button
                  key={tab.id}
                  size="xs"
                  variant={statusFilter === tab.id ? 'default' : 'ghost'}
                  onClick={() => setStatusFilter(tab.id)}
                >
                  {tab.label}
                </Button>
              ))}
            </div>

            <div className="flex items-center gap-2 flex-1 sm:flex-initial justify-end">
              {activeFilterAgent && (
                <Badge variant="default" className="gap-1.5 py-1">
                  <Filter className="h-3 w-3" />
                  <span>@{agentDisplayName(activeFilterAgent.key)}</span>
                  <button
                    type="button"
                    onClick={() => setAgentFilterId('')}
                    className="hover:opacity-80 cursor-pointer"
                    title="清除 Agent 筛选"
                  >
                    <X className="h-3 w-3" />
                  </button>
                </Badge>
              )}
              <div className="relative w-44 sm:w-52">
                <Search className="absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  placeholder="搜索标题、子任务、目录..."
                  className="h-7.5 pl-8 text-xs"
                />
              </div>
            </div>
          </div>

          {/* Task List */}
          {filteredTasks.length === 0 ? (
            <Card>
              <CardContent className="p-8 text-center text-xs text-muted-foreground space-y-2">
                <div className="text-sm font-medium text-foreground">
                  {tasks.length === 0 ? '暂无任何 AI 协同任务' : '当前筛选条件下没有匹配的任务'}
                </div>
                <div>
                  在上方输入任务标题（支持 <code className="font-mono">@Agent</code> 定向指派与定时发布），在线或定时巡检的 Agent 会自动认领并打勾完成。
                </div>
              </CardContent>
            </Card>
          ) : (
            <div className="space-y-3">
              {filteredTasks.map((task) => {
                const stBadge = statusBadgeInfo(task.status)
                const priBadge = priorityBadgeInfo(task.priority)
                const assignedItem = findAgent(task.assigned_agent)
                const claimerItem = findAgent(task.claimed_by_id || task.claimed_by_name)
                const subtasks = task.subtasks || []
                const doneSubtasks = subtasks.filter((s) => s.status === 'done').length
                const comments: AgentTaskComment[] = task.comments || []
                const isExpanded = Boolean(expandedComments[task.id])
                const visibleComments = isExpanded || comments.length <= 3 ? comments : comments.slice(-3)
                const hiddenCount = comments.length - visibleComments.length
                const draft = commentDrafts[task.id] || ''
                const mentionSuggestions =
                  commentMentionTarget?.taskId === task.id
                    ? sortedAgents.filter((a) => {
                        const q = commentMentionTarget.query.toLowerCase()
                        if (!q) return true
                        return [a.key.name, a.key.display_alias || '', a.key.owner_username]
                          .join(' ')
                          .toLowerCase()
                          .includes(q)
                      })
                    : []
                const busy = busyTaskId === task.id

                return (
                  <Card
                    key={task.id}
                    className={cn(
                      'transition-colors',
                      task.status === 'done' && 'border-emerald-500/35',
                      task.status === 'paused' && 'border-amber-500/50'
                    )}
                  >
                    <CardContent className="p-4 space-y-3 text-xs">
                      {/* Card Top Meta + Actions */}
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge variant={stBadge.variant}>
                            {task.status === 'done' && <CheckCircle2 className="h-3 w-3" />}
                            {task.status === 'in_progress' && <Loader2 className="h-3 w-3 animate-spin" />}
                            {task.status === 'scheduled' && <Clock className="h-3 w-3" />}
                            {task.status === 'paused' && <PauseCircle className="h-3 w-3" />}
                            <span>{stBadge.label}</span>
                          </Badge>

                          {priBadge && <Badge variant={priBadge.variant}>{priBadge.label}</Badge>}

                          {task.assigned_agent ? (
                            <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted/40 pl-1 pr-2 py-0.5 text-[11px]">
                              {assignedItem ? (
                                <AgentAvatarBadge
                                  agentKey={assignedItem.key}
                                  onlineStatus={assignedItem.status}
                                  size="xs"
                                  showDeviceCorner={false}
                                />
                              ) : (
                                <AtSign className="h-3 w-3 text-primary ml-1" />
                              )}
                              <span className="font-medium">
                                @{task.assigned_agent_label || (assignedItem ? agentDisplayName(assignedItem.key) : task.assigned_agent)}
                              </span>
                            </span>
                          ) : (
                            <Badge variant="outline">全员抢单池</Badge>
                          )}

                          {task.claimed_by_name && (
                            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/30 bg-emerald-500/10 pl-1 pr-2 py-0.5 text-[11px] text-emerald-700 dark:text-emerald-300">
                              {claimerItem && (
                                <AgentAvatarBadge
                                  agentKey={claimerItem.key}
                                  onlineStatus={claimerItem.status}
                                  size="xs"
                                  showDeviceCorner={false}
                                />
                              )}
                              <span>认领：{claimerItem ? agentDisplayName(claimerItem.key) : task.claimed_by_name}</span>
                            </span>
                          )}

                          {task.scheduled_at && (
                            <Badge variant="outline" className="tabular-nums">
                              <Clock className="h-3 w-3" />
                              <span>
                                {task.status === 'scheduled'
                                  ? `${formatLocalDateTime(task.scheduled_at)} · ${formatScheduledCountdown(task.scheduled_at)}`
                                  : `定时 ${formatLocalDateTime(task.scheduled_at)}`}
                              </span>
                            </Badge>
                          )}

                          {task.folder_prefix && (
                            <Badge variant="outline" className="font-mono">
                              <Folder className="h-3 w-3" />
                              <span>{task.folder_prefix}</span>
                            </Badge>
                          )}

                          {task.allow_split && (
                            <Badge variant="outline">
                              <GitBranch className="h-3 w-3" />
                              <span>可拆块</span>
                            </Badge>
                          )}
                        </div>

                        <div className="flex flex-wrap items-center gap-1 shrink-0">
                          {task.status === 'scheduled' && (
                            <Button
                              size="xs"
                              variant="outline"
                              disabled={busy}
                              onClick={() => void handleActivateTask(task)}
                            >
                              <Play className="h-3 w-3 text-emerald-500" />
                              <span>立即激活</span>
                            </Button>
                          )}
                          {(task.status === 'paused' || task.status === 'done' || task.status === 'cancelled') && (
                            <Button
                              size="xs"
                              variant="outline"
                              disabled={busy}
                              onClick={() => void handleReopenTask(task)}
                            >
                              <RotateCcw className="h-3 w-3 text-primary" />
                              <span>{task.status === 'paused' ? '确认并继续' : '重新激活'}</span>
                            </Button>
                          )}
                          {(task.status === 'pending' || task.status === 'in_progress' || task.status === 'paused') && (
                            <Button
                              size="xs"
                              variant="outline"
                              disabled={busy}
                              onClick={() => void handleSetTaskStatus(task, 'done')}
                              title="手动打勾完成"
                            >
                              <Check className="h-3 w-3 text-emerald-500" />
                              <span>打勾完成</span>
                            </Button>
                          )}
                          {task.status !== 'done' && task.status !== 'cancelled' && (
                            <Button
                              size="xs"
                              variant="ghost"
                              disabled={busy}
                              onClick={() => void handleSetTaskStatus(task, 'cancelled')}
                              title="取消任务"
                            >
                              <X className="h-3.5 w-3.5" />
                            </Button>
                          )}
                          <Button
                            size="xs"
                            variant="ghost"
                            className="text-destructive"
                            disabled={busy}
                            onClick={() => void handleDeleteTask(task)}
                            title="删除任务"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>

                      {/* Title & Description */}
                      <div className="space-y-1">
                        <div className="flex items-start gap-2">
                          <span
                            className={cn(
                              'mt-0.5 flex h-4.5 w-4.5 shrink-0 items-center justify-center rounded border',
                              task.status === 'done'
                                ? 'border-emerald-500 bg-emerald-500 text-white'
                                : 'border-border bg-background'
                            )}
                          >
                            {task.status === 'done' && <Check className="h-3 w-3 stroke-[3]" />}
                          </span>
                          <div className="min-w-0 flex-1">
                            <h3
                              className={cn(
                                'text-sm font-semibold leading-snug break-words',
                                task.status === 'done' && 'line-through text-muted-foreground'
                              )}
                            >
                              {task.title}
                            </h3>
                            {task.description && (
                              <p className="mt-1 whitespace-pre-wrap text-xs text-muted-foreground leading-relaxed break-words">
                                {task.description}
                              </p>
                            )}
                          </div>
                        </div>

                        <div className="flex flex-wrap items-center gap-3 pl-6 text-[11px] text-muted-foreground">
                          <span>发布人：{task.created_by}</span>
                          <span>创建于 {formatRelativeTime(task.created_at)}</span>
                          {task.completed_at && (
                            <span className="text-emerald-600 dark:text-emerald-400">
                              完成于 {formatRelativeTime(task.completed_at)}
                            </span>
                          )}
                        </div>
                      </div>

                      {/* Pause Warning Banner */}
                      {task.status === 'paused' && (
                        <div className="flex flex-wrap items-start justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-2.5 text-amber-800 dark:text-amber-200">
                          <div className="flex items-start gap-2 min-w-0">
                            <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5 text-amber-500" />
                            <div>
                              <div className="font-semibold">多 Agent 对话达到轮次保护，已自动暂停</div>
                              <div className="opacity-90 mt-0.5">
                                {task.pause_reason || '请在下方留言指示决策，然后点击「确认并继续」唤醒协同流程。'}
                              </div>
                            </div>
                          </div>
                          <Button
                            size="xs"
                            variant="outline"
                            disabled={busy}
                            onClick={() => void handleReopenTask(task)}
                          >
                            确认并继续
                          </Button>
                        </div>
                      )}

                      {/* Completion Result & Output Files */}
                      {(task.result_summary || (task.output_files && task.output_files.length > 0)) && (
                        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/8 p-2.5 space-y-1.5">
                          {task.result_summary && (
                            <div className="text-foreground leading-relaxed whitespace-pre-wrap break-words">
                              <span className="font-semibold text-emerald-700 dark:text-emerald-400">成果摘要：</span>
                              {task.result_summary}
                            </div>
                          )}
                          {task.output_files && task.output_files.length > 0 && (
                            <div className="space-y-1.5 pt-0.5">
                              <div className="text-[11px] font-medium text-emerald-700 dark:text-emerald-400">
                                产出文件（点击可直接预览 / 下载）：
                              </div>
                              <div className="flex flex-wrap items-center gap-2">
                                {task.output_files.map((fp, idx) => renderOutputFileChip(fp, idx))}
                              </div>
                            </div>
                          )}
                        </div>
                      )}

                      {/* Subtasks Checklist (Split Collaboration) */}
                      {subtasks.length > 0 && (
                        <div className="rounded-lg border border-border bg-muted/25 p-3 space-y-2">
                          <div className="flex items-center justify-between gap-2">
                            <span className="font-semibold flex items-center gap-1.5">
                              <GitBranch className="h-3.5 w-3.5 text-primary" />
                              <span>拆块接力清单 ({doneSubtasks}/{subtasks.length})</span>
                            </span>
                            <div className="h-1.5 w-28 overflow-hidden rounded-full bg-muted">
                              <div
                                className="h-full bg-emerald-500 transition-all"
                                style={{
                                  width: `${Math.round((doneSubtasks / subtasks.length) * 100)}%`,
                                }}
                              />
                            </div>
                          </div>

                          <div className="space-y-1.5">
                            {subtasks.map((st) => {
                              const stActor = findAgent(st.claimed_by_id || st.assigned_agent)
                              return (
                                <div
                                  key={st.id}
                                  className="flex flex-wrap items-start justify-between gap-2 rounded-md border border-border/70 bg-background/70 px-2.5 py-2"
                                >
                                  <div className="flex items-start gap-2 min-w-0 flex-1">
                                    {st.status === 'done' ? (
                                      <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500 mt-0.5" />
                                    ) : st.status === 'claimed' ? (
                                      <Loader2 className="h-4 w-4 shrink-0 text-primary animate-spin mt-0.5" />
                                    ) : (
                                      <Circle className="h-4 w-4 shrink-0 text-muted-foreground mt-0.5" />
                                    )}
                                    <div className="min-w-0 flex-1">
                                      <div
                                        className={cn(
                                          'font-medium leading-snug',
                                          st.status === 'done' && 'line-through text-muted-foreground'
                                        )}
                                      >
                                        #{st.seq} {st.title}
                                      </div>
                                      {st.result_summary && (
                                        <div className="text-[11px] text-emerald-600 dark:text-emerald-400 mt-0.5">
                                          {st.result_summary}
                                        </div>
                                      )}
                                      {st.output_file && (
                                        <div className="mt-1">
                                          {renderOutputFileChip(st.output_file, `sub-${st.id}`)}
                                        </div>
                                      )}
                                    </div>
                                  </div>

                                  <div className="flex flex-wrap items-center gap-1.5 shrink-0">
                                    {st.depends_on && st.depends_on.length > 0 && st.status === 'pending' && (
                                      <Badge variant="outline" className="text-[10px]">
                                        等待前序块
                                      </Badge>
                                    )}
                                    {(st.claimed_by_name || st.assigned_agent_label || st.assigned_agent) && (
                                      <span className="inline-flex items-center gap-1 rounded-full border border-border bg-card px-1.5 py-0.5 text-[10px]">
                                        {stActor && (
                                          <AgentAvatarBadge
                                            agentKey={stActor.key}
                                            onlineStatus={stActor.status}
                                            size="xs"
                                            showDeviceCorner={false}
                                          />
                                        )}
                                        <span>
                                          {st.claimed_by_name
                                            ? st.claimed_by_name
                                            : `@${st.assigned_agent_label || st.assigned_agent}`}
                                        </span>
                                      </span>
                                    )}
                                  </div>
                                </div>
                              )
                            })}
                          </div>
                        </div>
                      )}

                      {/* Multi-Agent Collaboration Comments Thread */}
                      <div className="space-y-2 border-t border-border/70 pt-2.5">
                        <div className="flex items-center justify-between">
                          <span className="inline-flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
                            <MessageSquare className="h-3.5 w-3.5" />
                            <span>协同群聊 / @接力记录 ({comments.length})</span>
                          </span>
                          {hiddenCount > 0 && (
                            <Button
                              type="button"
                              size="xs"
                              variant="ghost"
                              onClick={() =>
                                setExpandedComments((prev) => ({ ...prev, [task.id]: true }))
                              }
                            >
                              展开更早 {hiddenCount} 条对话
                            </Button>
                          )}
                          {isExpanded && comments.length > 3 && (
                            <Button
                              type="button"
                              size="xs"
                              variant="ghost"
                              onClick={() =>
                                setExpandedComments((prev) => ({ ...prev, [task.id]: false }))
                              }
                            >
                              收起
                            </Button>
                          )}
                        </div>

                        {visibleComments.length > 0 && (
                          <div className="space-y-1.5">
                            {visibleComments.map((c) => {
                              const actor =
                                c.author_role === 'agent'
                                  ? findAgent(c.author_id || c.author_name)
                                  : undefined
                              return (
                                <div
                                  key={c.id}
                                  className={cn(
                                    'flex items-start gap-2 rounded-lg px-2.5 py-1.5',
                                    c.author_role === 'system'
                                      ? 'bg-muted/35 text-muted-foreground'
                                      : c.author_role === 'user'
                                      ? 'bg-primary/6 border border-primary/20'
                                      : 'bg-muted/40 border border-border/60'
                                  )}
                                >
                                  {c.author_role === 'agent' ? (
                                    <AgentAvatarBadge
                                      agentKey={
                                        actor?.key || {
                                          id: c.author_id || c.author_name,
                                          name: c.author_name,
                                          owner_username: task.owner_username,
                                        }
                                      }
                                      onlineStatus={actor?.status}
                                      size="xs"
                                      showDeviceCorner={false}
                                      className="mt-0.5"
                                    />
                                  ) : c.author_role === 'user' ? (
                                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/15 text-primary">
                                      <UserIcon className="h-3.5 w-3.5" />
                                    </span>
                                  ) : (
                                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                                      <Bot className="h-3.5 w-3.5" />
                                    </span>
                                  )}

                                  <div className="min-w-0 flex-1">
                                    <div className="flex flex-wrap items-center gap-1.5">
                                      <span className="font-semibold text-[11px]">
                                        {actor ? agentDisplayName(actor.key) : c.author_name}
                                      </span>
                                      {c.author_role === 'user' && (
                                        <Badge variant="outline" className="px-1 py-0 text-[9px]">
                                          人工指示
                                        </Badge>
                                      )}
                                      <span className="text-[10px] text-muted-foreground">
                                        {formatRelativeTime(c.created_at)}
                                      </span>
                                    </div>
                                    <div className="mt-0.5 whitespace-pre-wrap text-xs leading-relaxed break-words">
                                      {renderCommentBody(c.body)}
                                    </div>
                                  </div>
                                </div>
                              )
                            })}
                          </div>
                        )}

                        {/* Quick @Reply Bar */}
                        <div className="relative flex items-center gap-1.5 pt-1">
                          <Input
                            value={draft}
                            onChange={(e) => handleCommentInputChange(task.id, e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' && !e.shiftKey) {
                                e.preventDefault()
                                void handlePostComment(task.id)
                              }
                            }}
                            placeholder="回复指示或输入 @ 呼叫指定 Agent 接手..."
                            className="h-8 text-xs"
                          />
                          <Button
                            type="button"
                            size="xs"
                            variant="outline"
                            disabled={!draft.trim() || submittingCommentTaskId === task.id}
                            onClick={() => void handlePostComment(task.id)}
                          >
                            {submittingCommentTaskId === task.id ? (
                              <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                              <Send className="h-3.5 w-3.5" />
                            )}
                            <span>发送</span>
                          </Button>

                          {mentionSuggestions.length > 0 && (
                            <div className="absolute left-0 bottom-9 z-30 w-72 max-h-48 overflow-y-auto rounded-lg border border-border bg-card p-1.5 shadow-xl space-y-1">
                              {mentionSuggestions.map((item) => (
                                <button
                                  key={item.key.id}
                                  type="button"
                                  onClick={() => pickCommentMention(task.id, item)}
                                  className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-accent cursor-pointer"
                                >
                                  <AgentAvatarBadge
                                    agentKey={item.key}
                                    onlineStatus={item.status}
                                    size="xs"
                                    showDeviceCorner={false}
                                  />
                                  <div className="min-w-0 flex-1">
                                    <div className="font-medium truncate">
                                      @{agentDisplayName(item.key)}
                                    </div>
                                    <div className="text-[10px] text-muted-foreground truncate">
                                      {item.key.owner_username} ·{' '}
                                      {agentDeviceLabel(item.key) || deviceKindShortLabel(item.key.device_kind)}
                                    </div>
                                  </div>
                                </button>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </div>

        {/* Right Column: AI Employee Badge Wall (3-Layer Cartoon Identity) */}
        <aside className="space-y-4">
          <Card>
            <CardHeader className="pb-2.5">
              <CardTitle className="flex items-center justify-between text-sm">
                <span className="flex items-center gap-1.5">
                  <Bot className="h-4 w-4 text-primary" />
                  <span>AI 员工工牌墙 ({sortedAgents.length})</span>
                </span>
                {agentFilterId && (
                  <Button size="xs" variant="ghost" onClick={() => setAgentFilterId('')}>
                    看全部
                  </Button>
                )}
              </CardTitle>
              <CardDescription>
                卡通形象区分角色，外圈色环区分归属用户，右下角标区分运行电脑。点击卡片可按 Agent 筛选看板。
              </CardDescription>
            </CardHeader>

            <CardContent className="space-y-3 text-xs">
              {/* Owner Ring Legend */}
              {ownerLegend.length > 0 && (
                <div className="rounded-lg border border-border bg-muted/30 p-2.5 space-y-1.5">
                  <div className="text-[10px] font-medium text-muted-foreground">
                    外圈色环 · 成员归属图例
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {ownerLegend.map((entry) => (
                      <span
                        key={entry.username}
                        className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-2 py-0.5 text-[11px]"
                      >
                        <span
                          className="h-2.5 w-2.5 rounded-full shrink-0"
                          style={{ backgroundColor: entry.color }}
                        />
                        <span>{entry.username}</span>
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {/* Agent Roster Cards */}
              {sortedAgents.length === 0 ? (
                <div className="rounded-lg border border-dashed border-border p-4 text-center text-muted-foreground space-y-2">
                  <div>暂未绑定任何 Agent 密钥</div>
                  {onOpenAgentSettings && (
                    <Button size="xs" variant="outline" onClick={onOpenAgentSettings}>
                      前往 Agent 接入中心创建
                    </Button>
                  )}
                </div>
              ) : (
                <div className="space-y-2">
                  {sortedAgents.map((item) => {
                    const k = item.key
                    const selected = agentFilterId === k.id
                    const st = presenceLabel(k.disabled ? 'offline' : item.status)
                    const deviceText =
                      agentDeviceLabel(k) || deviceKindShortLabel(k.device_kind)

                    return (
                      <div
                        key={k.id}
                        role="button"
                        tabIndex={0}
                        onClick={() => setAgentFilterId((prev) => (prev === k.id ? '' : k.id))}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault()
                            setAgentFilterId((prev) => (prev === k.id ? '' : k.id))
                          }
                        }}
                        className={cn(
                          'rounded-xl border p-3 transition-all cursor-pointer space-y-2',
                          selected
                            ? 'border-primary bg-primary/8 shadow-xs'
                            : 'border-border hover:bg-muted/40'
                        )}
                      >
                        <div className="flex items-start gap-2.5">
                          <AgentAvatarBadge
                            agentKey={k}
                            onlineStatus={k.disabled ? 'offline' : item.status}
                            size="md"
                          />
                          <div className="min-w-0 flex-1 space-y-0.5">
                            <div className="flex items-center justify-between gap-1.5">
                              <span className="font-semibold truncate">{agentDisplayName(k)}</span>
                              <Badge variant={st.badgeVariant} className="shrink-0 text-[10px]">
                                {st.label}
                              </Badge>
                            </div>
                            <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                              <span>@{k.owner_username}</span>
                              <span>·</span>
                              <span className="inline-flex items-center gap-1">
                                <DeviceMiniIcon kind={k.device_kind} className="h-3 w-3" />
                                <span className="truncate max-w-[110px]">{deviceText}</span>
                              </span>
                            </div>
                          </div>
                        </div>

                        {(item.reported_task || item.active_tool) && (
                          <div className="rounded bg-muted/50 px-2 py-1 text-[10px] text-muted-foreground truncate">
                            {item.reported_task || `正在调用：${item.active_tool}`}
                          </div>
                        )}

                        <div className="flex items-center justify-between gap-1.5 pt-1 border-t border-border/60">
                          <Button
                            type="button"
                            size="xs"
                            variant="ghost"
                            onClick={(e) => {
                              e.stopPropagation()
                              setProfileEditingKey(k)
                            }}
                          >
                            <Paintbrush className="h-3 w-3" />
                            <span>换装</span>
                          </Button>

                          <Button
                            type="button"
                            size="xs"
                            variant="outline"
                            onClick={(e) => {
                              e.stopPropagation()
                              setNewAssignedId(k.id)
                              setComposerExpanded(true)
                              focusTitleInput()
                              toast(`已在发布栏选定 @${agentDisplayName(k)}`, 'info')
                            }}
                          >
                            <AtSign className="h-3 w-3 text-primary" />
                            <span>派单给他</span>
                          </Button>
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>
        </aside>
      </div>

      {/* Zero-Token Worker & /schedule Setup Dialog */}
      <Dialog
        open={setupOpen}
        onClose={() => setSetupOpen(false)}
        size="lg"
        title="配置零消耗后台值守 Worker / /schedule 巡检"
        description="空闲轮询仅调用轻量化 /api/agent-tasks/poll，0 LLM Token 消耗；仅在有待办或 @提及时唤醒 Agent 执行"
      >
        <div className="space-y-4 text-xs">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <label className="font-medium">目标节点访问地址</label>
              <Select
                value={effectiveBaseUrl}
                onChange={(e) => setSelectedBaseUrl(e.target.value)}
                className="w-full text-xs"
              >
                {addressOptions.map((opt) => (
                  <option key={opt.url} value={opt.url}>
                    {opt.label} ({opt.url})
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-1">
              <label className="font-medium">巡检间隔（秒，支持秒级长轮询）</label>
              <Select
                value={setupIntervalSec}
                onChange={(e) => setSetupIntervalSec(e.target.value)}
                className="w-full text-xs"
              >
                <option value="30">每 30 秒检查一次（准实时响应）</option>
                <option value="60">每 1 分钟检查一次（推荐）</option>
                <option value="300">每 5 分钟检查一次</option>
                <option value="1800">每 30 分钟检查一次</option>
              </Select>
            </div>
          </div>

          <div className="space-y-1.5">
            <div className="flex flex-wrap gap-1.5">
              {(
                [
                  { id: 'ps1', label: 'Windows PowerShell 零消耗值守' },
                  { id: 'sh', label: 'macOS / Linux Shell 零消耗值守' },
                  { id: 'schedule', label: 'Antigravity /schedule 提示词' },
                ] as Array<{ id: SetupTab; label: string }>
              ).map((t) => (
                <Button
                  key={t.id}
                  type="button"
                  size="xs"
                  variant={setupTab === t.id ? 'default' : 'outline'}
                  onClick={() => setSetupTab(t.id)}
                >
                  {t.label}
                </Button>
              ))}
            </div>

            {setupTab !== 'schedule' && (
              <div className="space-y-1 pt-1">
                <label className="font-medium">附加全局值守指令（可选，会通过环境变量注入唤醒会话）</label>
                <Input
                  value={setupCustomPrompt}
                  onChange={(e) => setSetupCustomPrompt(e.target.value)}
                  placeholder="例如：优先认领 backend/ 目录任务，产出提交到 workspace/reports"
                />
              </div>
            )}

            <div className="rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all">
              {workerSnippet}
            </div>

            <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
              <div className="text-[11px] text-muted-foreground">
                {setupTab === 'schedule'
                  ? '在 Antigravity 对话中粘贴 /schedule 提示词即可让本地 Agent 定时主动看看板。'
                  : '脚本会自动读取本机 mcp_config.json 中的 lfh_ag_ 令牌（或环境变量 LFH_TOKEN），无需明文粘贴密钥。'}
              </div>
              <Button
                type="button"
                size="sm"
                onClick={async () => {
                  const ok = await copyTextToClipboard(workerSnippet)
                  toast(ok ? '已复制到剪贴板' : '复制失败，请手动复制', ok ? 'success' : 'error')
                }}
              >
                <Copy className="h-3.5 w-3.5" />
                <span>复制内容</span>
              </Button>
            </div>
          </div>
        </div>
      </Dialog>

      {/* Cartoon Badge Customizer Modal */}
      <AgentProfileModal
        open={Boolean(profileEditingKey)}
        agentKey={profileEditingKey}
        onClose={() => setProfileEditingKey(null)}
        onSaved={() => {
          void loadBoardData(true)
        }}
      />
    </div>
  )
}
