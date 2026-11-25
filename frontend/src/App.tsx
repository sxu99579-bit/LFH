import { useCallback, useEffect, useRef, useState } from 'react'
import { KeyRound, LogOut } from 'lucide-react'
import { api } from '@/lib/api'
import { useTransferQueue } from '@/lib/transfers'
import { isMobileLayout } from '@/lib/utils'
import type {
  ClusterCapabilityView,
  ClusterResponse,
  FileRecord,
  NavTab,
  ServerInfo,
  SummaryStats,
  ThemeMode,
  User,
} from '@/types'
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  ToastProvider,
  useToast,
} from '@/components/ui'
import { AuthView } from '@/components/AuthView'
import { AppShell } from '@/components/AppShell'
import { CommandPalette } from '@/components/CommandPalette'
import { TransferDrawer } from '@/components/TransferDrawer'
import { FileInspector } from '@/components/FileInspector'
import {
  CreateTxtModal,
  LocalImportModal,
  PreviewModal,
  TextEditorModal,
} from '@/components/FileModals'
import { FilesPage } from '@/pages/FilesPage'
import { AgentTasksPage } from '@/pages/AgentTasksPage'
import { ClipboardPage } from '@/pages/ClipboardPage'
import { PluginsPage } from '@/pages/PluginsPage'
import { HealthPage } from '@/pages/HealthPage'
import { SettingsPage } from '@/pages/SettingsPage'
import type { SettingsTab } from '@/pages/SettingsPage'

function WorkspaceApp() {
  const { toast } = useToast()
  const [isMobile] = useState(() => isMobileLayout())
  const [theme, setTheme] = useState<ThemeMode>(() => {
    try {
      const saved = localStorage.getItem('lan_filehub_theme')
      return saved === 'light' ? 'light' : 'dark'
    } catch {
      return 'dark'
    }
  })

  const [booting, setBooting] = useState(true)
  const [info, setInfo] = useState<ServerInfo | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [activeTab, setActiveTab] = useState<NavTab>('files')
  const [pendingTasksCount, setPendingTasksCount] = useState(0)
  const [settingsFocusTab, setSettingsFocusTab] = useState<{ tab: SettingsTab; seq: number } | null>(null)

  const [cluster, setCluster] = useState<ClusterResponse | null>(null)
  const [summary, setSummary] = useState<SummaryStats | null>(null)
  const [files, setFiles] = useState<FileRecord[]>([])
  const [filesLoading, setFilesLoading] = useState(false)
  const [capabilities, setCapabilities] = useState<ClusterCapabilityView[]>([])
  const [selectableUsers, setSelectableUsers] = useState<Array<{ username: string; role: string }>>([])
  const [writableMountIds, setWritableMountIds] = useState<Set<string>>(() => new Set())
  const [folders, setFolders] = useState<string[]>([])
  const [currentFolder, setCurrentFolder] = useState('')

  // File filters
  const [searchQuery, setSearchQuery] = useState('')
  const [categoryFilter, setCategoryFilter] = useState('')
  const [monthFilter, setMonthFilter] = useState('')
  const [dateFilter, setDateFilter] = useState('')
  const [dateFromFilter, setDateFromFilter] = useState('')
  const [dateToFilter, setDateToFilter] = useState('')
  const [nodeFilter, setNodeFilter] = useState('')
  const [deviceFilter, setDeviceFilter] = useState('')

  // Overlay & Modal states
  const [cmdOpen, setCmdOpen] = useState(false)
  const [inspectedFile, setInspectedFile] = useState<FileRecord | null>(null)
  const [previewFile, setPreviewFile] = useState<FileRecord | null>(null)
  const [editingFile, setEditingFile] = useState<FileRecord | null>(null)
  const [createTxtOpen, setCreateTxtOpen] = useState(false)
  const [localImportMode, setLocalImportMode] = useState<'import' | 'mount' | null>(null)
  const [forceOldPwd, setForceOldPwd] = useState('')
  const [forceNewPwd, setForceNewPwd] = useState('')
  const [forceConfirmPwd, setForceConfirmPwd] = useState('')
  const [forcePwdBusy, setForcePwdBusy] = useState(false)

  const canUpload = Boolean(user && (user.role === 'admin' || (user.role !== 'readonly' && user.can_upload !== false)))

  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const folderInputRef = useRef<HTMLInputElement | null>(null)

  // Sync Tailwind dark mode class
  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    try {
      localStorage.setItem('lan_filehub_theme', theme)
    } catch {
      // ignore storage quota errors
    }
  }, [theme])

  const toggleTheme = useCallback(() => {
    setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))
  }, [])

  const loadCluster = useCallback(async () => {
    try {
      const [clRes, infoRes] = await Promise.all([api.getCluster(), api.getInfo()])
      setCluster(clRes)
      setInfo(infoRes)
    } catch {
      // ignore background refresh error
    }
  }, [])

  const loadFilesAndSummary = useCallback(async () => {
    setFilesLoading(true)
    try {
      const sumParams = new URLSearchParams()
      if (categoryFilter) sumParams.set('category', categoryFilter)
      if (monthFilter) sumParams.set('month', monthFilter)
      if (dateFilter) sumParams.set('date', dateFilter)
      if (dateFromFilter) sumParams.set('date_from', dateFromFilter)
      if (dateToFilter) sumParams.set('date_to', dateToFilter)
      if (nodeFilter) sumParams.set('node', nodeFilter)
      if (deviceFilter) sumParams.set('device', deviceFilter)

      const fileParams = new URLSearchParams(sumParams)
      if (searchQuery.trim()) fileParams.set('q', searchQuery.trim())

      const [sumRes, filesRes] = await Promise.all([
        api.getSummary(sumParams),
        api.getFiles(fileParams),
      ])
      setSummary(sumRes)
      setFiles(filesRes.files || [])
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载文件列表失败', 'error')
    } finally {
      setFilesLoading(false)
    }
  }, [categoryFilter, dateFilter, dateFromFilter, dateToFilter, deviceFilter, monthFilter, nodeFilter, searchQuery, toast])

  const loadAuxData = useCallback(async () => {
    try {
      const [capRes, selRes, taskRes, folderRes] = await Promise.all([
        api.getPluginCapabilities(),
        api.getSelectableUsers(),
        api.listAgentTasks({ limit: 1 }).catch(() => null),
        api.listFolders().catch(() => ({ folders: [] })),
      ])
      setCapabilities(capRes.capabilities || [])
      setSelectableUsers(selRes.users || [])
      setFolders(folderRes.folders || [])
      if (taskRes?.summary) {
        setPendingTasksCount((taskRes.summary.pending || 0) + (taskRes.summary.paused || 0))
      }
      if (user?.role === 'admin') {
        const mntRes = await api.getMounts().catch(() => ({ mounts: [] }))
        const nextSet = new Set<string>()
        for (const m of mntRes.mounts || []) {
          if (m.writable) nextSet.add(m.id)
        }
        setWritableMountIds(nextSet)
      } else {
        setWritableMountIds(new Set())
      }
    } catch {
      // non-fatal
    }
  }, [user?.role])

  const refreshAll = useCallback(async () => {
    await Promise.all([loadCluster(), loadFilesAndSummary(), loadAuxData()])
  }, [loadAuxData, loadCluster, loadFilesAndSummary])

  const transfers = useTransferQueue(() => {
    void refreshAll()
  })

  // Initial auth bootstrap
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const serverInfo = await api.getInfo()
        if (!cancelled) setInfo(serverInfo)
        const me = await api.getMe()
        if (!cancelled) setUser(me.user)
      } catch {
        if (!cancelled) setUser(null)
      } finally {
        if (!cancelled) setBooting(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Load workspace data once authenticated
  useEffect(() => {
    if (!user) return
    void refreshAll()
  }, [user, refreshAll])

  // Real-time SSE events
  useEffect(() => {
    if (!user) return
    const es = new EventSource('/api/events')
    const handleEvent = () => {
      void loadFilesAndSummary()
      void loadCluster()
    }
    es.addEventListener('change', (ev: MessageEvent) => {
      try {
        const data = JSON.parse(ev.data)
        if (data.kind === 'entry_switched' && data.entry_url) {
          toast(`主入口节点已切换至：${data.entry_url}`, 'info')
          void loadCluster()
          return
        }
        if (data.kind === 'agent_pairing') {
          window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: data }))
          const code = String(data.node_name || '').trim()
          if (code && code !== 'approved' && code !== 'rejected' && code !== 'oauth_approved') {
            toast(`新 Agent「${data.actor_name || 'Agent'}」请求接入（确认码 ${code}）`, 'info')
          }
          return
        }
        if (data.kind === 'agent_status') {
          window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: data }))
          return
        }
        if (data.kind === 'agent_task' || data.kind === 'agent_profile') {
          window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: data }))
          void api
            .listAgentTasks({ limit: 1 })
            .then((res) => {
              if (res?.summary) {
                setPendingTasksCount((res.summary.pending || 0) + (res.summary.paused || 0))
              }
            })
            .catch(() => {})
          if (data.kind === 'agent_task' && data.node_name === 'completed') {
            void loadFilesAndSummary()
            void loadAuxData()
          }
          return
        }
        if (data.kind === 'folder') {
          void loadFilesAndSummary()
          void loadAuxData()
          return
        }
      } catch {
        // ignore parse error
      }
      handleEvent()
      void loadAuxData()
    })
    es.addEventListener('upload', handleEvent)
    es.addEventListener('extract', handleEvent)
    es.addEventListener('delete', handleEvent)
    es.addEventListener('cluster_sync', handleEvent)
    es.addEventListener('entry_switched', (ev: MessageEvent) => {
      try {
        const data = JSON.parse(ev.data)
        if (data.entry_url) {
          toast(`主入口节点已切换至：${data.entry_url}`, 'info')
        }
      } catch {
        // ignore parse error
      }
      void loadCluster()
    })
    return () => {
      es.close()
    }
  }, [user, loadAuxData, loadCluster, loadFilesAndSummary, toast])

  // 3s telemetry polling while viewing health or cluster/plugins
  useEffect(() => {
    if (!user) return
    if (activeTab !== 'health' && activeTab !== 'plugins' && activeTab !== 'settings') return
    const timer = window.setInterval(() => {
      void loadCluster()
    }, 3000)
    return () => window.clearInterval(timer)
  }, [activeTab, loadCluster, user])

  // Global Ctrl+K shortcut for Command Palette
  useEffect(() => {
    if (!user) return
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setCmdOpen((v) => !v)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [user])

  const handleLogout = async () => {
    await api.logout().catch(() => {})
    setUser(null)
    setInspectedFile(null)
    setPreviewFile(null)
    setEditingFile(null)
  }

  const handleReconcileClusterVersion = async () => {
    try {
      const res = await api.reconcileVersions('auto')
      toast(res.message || `已对齐版本到 ${res.target_version || ''}`, 'success')
      await loadCluster()
    } catch (err) {
      toast(err instanceof Error ? err.message : '集群版本对齐失败', 'error')
    }
  }

  const handleProbeBandwidth = async () => {
    if (!user || user.role !== 'admin') return
    setActiveTab('health')
    try {
      await api.probeBandwidth()
      toast('已完成全集群内网带宽测速', 'success')
      await loadCluster()
    } catch (err) {
      toast(err instanceof Error ? err.message : '测速失败', 'error')
    }
  }

  const handleDownloadOutput = (fileId: string, fileName: string) => {
    const found = files.find((f) => f.id === fileId)
    if (found) {
      transfers.enqueueDownload(found)
      transfers.setOpen(true)
      return
    }
    const a = document.createElement('a')
    a.href = `/api/files/${encodeURIComponent(fileId)}/download?download=1`
    a.download = fileName
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  if (booting) {
    return (
      <div className="min-h-screen flex flex-col items-center justify-center gap-3 bg-background text-foreground">
        <img
          src={theme === 'dark' ? '/logo-dark.png' : '/logo-light.png'}
          alt="QuailNest"
          className="h-14 w-14 object-contain animate-pulse"
        />
        <div className="text-xs text-muted-foreground">正在连接 QuailNest 鹌鹑窝中枢...</div>
      </div>
    )
  }

  if (!user) {
    return (
      <AuthView
        serverInfo={info}
        theme={theme}
        onToggleTheme={toggleTheme}
        onAuthenticated={(u) => setUser(u)}
      />
    )
  }

  const pool = cluster?.pool || info?.pool || null
  const nodes = pool?.nodes || []

  return (
    <>
      <AppShell
        user={user}
        info={info}
        pool={pool}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        theme={theme}
        onToggleTheme={toggleTheme}
        isMobile={isMobile}
        tasks={transfers.tasks}
        pendingTasksCount={pendingTasksCount}
        onToggleTransferDrawer={() => transfers.setOpen(!transfers.open)}
        onOpenCommandPalette={() => setCmdOpen(true)}
        onQuickUploadFiles={() => {
          if (!canUpload) {
            toast('当前账号为只读权限或已被管理员禁止上传写入', 'error')
            return
          }
          fileInputRef.current?.click()
        }}
        onQuickCreateTxt={() => {
          if (!canUpload) {
            toast('当前账号为只读权限或已被管理员禁止上传写入', 'error')
            return
          }
          setCreateTxtOpen(true)
        }}
        onQuickLocalImport={() => setLocalImportMode('import')}
        onReconcileClusterVersion={handleReconcileClusterVersion}
        onLogout={handleLogout}
      >
        {activeTab === 'files' && (
          <FilesPage
            user={user}
            files={files}
            folders={folders}
            currentFolder={currentFolder}
            onCurrentFolderChange={setCurrentFolder}
            summary={summary}
            nodes={nodes}
            capabilities={capabilities}
            selectableUsers={selectableUsers}
            writableMountIds={writableMountIds}
            isMobile={isMobile}
            loading={filesLoading}
            searchQuery={searchQuery}
            categoryFilter={categoryFilter}
            monthFilter={monthFilter}
            dateFilter={dateFilter}
            dateFromFilter={dateFromFilter}
            dateToFilter={dateToFilter}
            nodeFilter={nodeFilter}
            deviceFilter={deviceFilter}
            onFilterChange={(patch) => {
              if (patch.searchQuery !== undefined) setSearchQuery(patch.searchQuery)
              if (patch.categoryFilter !== undefined) setCategoryFilter(patch.categoryFilter)
              if (patch.monthFilter !== undefined) setMonthFilter(patch.monthFilter)
              if (patch.dateFilter !== undefined) setDateFilter(patch.dateFilter)
              if (patch.dateFromFilter !== undefined) setDateFromFilter(patch.dateFromFilter)
              if (patch.dateToFilter !== undefined) setDateToFilter(patch.dateToFilter)
              if (patch.nodeFilter !== undefined) setNodeFilter(patch.nodeFilter)
              if (patch.deviceFilter !== undefined) setDeviceFilter(patch.deviceFilter)
            }}
            onRefresh={() => void refreshAll()}
            onUploadFiles={(items, targetNode, allowed) => {
              transfers.enqueueUploads(items, targetNode, allowed)
              transfers.setOpen(true)
            }}
            onInspectFile={(rec) => setInspectedFile(rec)}
            onPreviewFile={(rec) => setPreviewFile(rec)}
            onEditFile={(rec) => setEditingFile(rec)}
            onDownloadFile={(rec) => {
              transfers.enqueueDownload(rec)
              transfers.setOpen(true)
            }}
            onOpenCreateTxt={() => setCreateTxtOpen(true)}
            onOpenLocalImport={() => setLocalImportMode('import')}
            fileInputRef={fileInputRef}
            folderInputRef={folderInputRef}
          />
        )}

        {activeTab === 'tasks' && (
          <AgentTasksPage
            user={user}
            info={info}
            files={files}
            folders={folders}
            isMobile={isMobile}
            onSummaryChange={(s) => setPendingTasksCount((s.pending || 0) + (s.paused || 0))}
            onOpenAgentSettings={() => {
              setSettingsFocusTab({ tab: 'agent', seq: Date.now() })
              setActiveTab('settings')
            }}
            onPreviewFile={(rec) => setPreviewFile(rec)}
            onDownloadFile={(rec) => {
              transfers.enqueueDownload(rec)
              transfers.setOpen(true)
            }}
            onInspectFile={(rec) => setInspectedFile(rec)}
          />
        )}

        {activeTab === 'clipboard' && (
          <ClipboardPage
            user={user}
            files={files}
            nodes={nodes}
            onRefresh={() => void refreshAll()}
            onPreviewFile={(rec) => setPreviewFile(rec)}
            onInspectFile={(rec) => setInspectedFile(rec)}
            onDownloadFile={(rec) => {
              transfers.enqueueDownload(rec)
              transfers.setOpen(true)
            }}
          />
        )}

        {activeTab === 'plugins' && user.role === 'admin' && (
          <PluginsPage
            onPoolUpdated={() => void refreshAll()}
            onDownloadOutput={handleDownloadOutput}
          />
        )}

        {activeTab === 'health' && (
          <HealthPage
            user={user}
            pool={pool}
            onRefresh={loadCluster}
          />
        )}

        {activeTab === 'settings' && (
          <SettingsPage
            user={user}
            info={info}
            cluster={cluster}
            theme={theme}
            onToggleTheme={toggleTheme}
            onRefreshAll={refreshAll}
            onOpenMountModal={() => setLocalImportMode('mount')}
            folders={folders}
            focusTab={settingsFocusTab}
          />
        )}
      </AppShell>

      <CommandPalette
        open={cmdOpen}
        onClose={() => setCmdOpen(false)}
        files={files}
        isAdmin={user.role === 'admin'}
        onNavigate={(tab) => setActiveTab(tab)}
        onSelectFile={(rec) => setInspectedFile(rec)}
        onCreateTxt={() => {
          if (!canUpload) {
            toast('当前账号为只读权限或已被管理员禁止上传写入', 'error')
            return
          }
          setCreateTxtOpen(true)
        }}
        onProbeBandwidth={() => void handleProbeBandwidth()}
      />

      <TransferDrawer
        tasks={transfers.tasks}
        open={transfers.open}
        onClose={() => transfers.setOpen(false)}
        onPause={transfers.pauseTask}
        onResume={transfers.resumeTask}
        onCancel={transfers.cancelTask}
        onClearDone={transfers.clearDone}
      />

      <FileInspector
        file={inspectedFile}
        user={user}
        capabilities={capabilities}
        writableMountIds={writableMountIds}
        onClose={() => setInspectedFile(null)}
        onOpenPreview={(rec) => setPreviewFile(rec)}
        onOpenEditor={(rec) => setEditingFile(rec)}
        onQueueDownload={(rec) => {
          transfers.enqueueDownload(rec)
          transfers.setOpen(true)
        }}
        onRefreshAll={() => {
          setInspectedFile(null)
          void refreshAll()
        }}
      />

      <PreviewModal
        file={previewFile}
        writableMountIds={writableMountIds}
        onClose={() => setPreviewFile(null)}
        onOpenEditor={(rec) => setEditingFile(rec)}
        targetNode="auto"
        onRefreshAll={() => void refreshAll()}
      />

      <TextEditorModal
        file={editingFile}
        onClose={() => setEditingFile(null)}
        onSaved={() => void refreshAll()}
      />

      <CreateTxtModal
        open={createTxtOpen}
        onClose={() => setCreateTxtOpen(false)}
        isAdmin={user.role === 'admin'}
        nodes={nodes}
        defaultNode="auto"
        defaultFolder={currentFolder}
        folders={folders}
        allowedUsers={[]}
        onCreated={() => void refreshAll()}
      />

      <LocalImportModal
        open={localImportMode !== null}
        initialMode={localImportMode || 'import'}
        onClose={() => setLocalImportMode(null)}
        nodes={nodes}
        allowedUsers={[]}
        onSuccess={() => void refreshAll()}
      />

      {user.must_change_password && (
        <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/65 backdrop-blur-xs p-4">
          <Card className="w-full max-w-md shadow-2xl">
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <KeyRound className="h-4 w-4 text-primary" />
                <span>请修改初始或临时密码</span>
              </CardTitle>
              <CardDescription>
                管理员已为账号「{user.username}」开启首次/重置登录强制改密要求，完成修改后即可进入工作区。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form
                onSubmit={async (e) => {
                  e.preventDefault()
                  if (!forceOldPwd) {
                    toast('请输入当前/临时密码', 'error')
                    return
                  }
                  if (forceNewPwd.length < 6) {
                    toast('新密码长度至少为 6 位', 'error')
                    return
                  }
                  if (forceNewPwd !== forceConfirmPwd) {
                    toast('两次输入的新密码不一致', 'error')
                    return
                  }
                  setForcePwdBusy(true)
                  try {
                    const res = await api.changePassword(forceOldPwd, forceNewPwd)
                    setForceOldPwd('')
                    setForceNewPwd('')
                    setForceConfirmPwd('')
                    if (res.user) {
                      setUser(res.user)
                    } else {
                      setUser((prev) => (prev ? { ...prev, must_change_password: false } : prev))
                    }
                    toast('密码修改成功，欢迎进入 QuailNest 工作区', 'success')
                  } catch (err) {
                    toast(err instanceof Error ? err.message : '修改密码失败', 'error')
                  } finally {
                    setForcePwdBusy(false)
                  }
                }}
                className="space-y-3 text-xs"
              >
                <div className="space-y-1">
                  <label className="font-medium">当前 / 临时密码</label>
                  <Input
                    type="password"
                    value={forceOldPwd}
                    onChange={(e) => setForceOldPwd(e.target.value)}
                    placeholder="输入管理员分配的初始或临时密码"
                    autoFocus
                  />
                </div>
                <div className="space-y-1">
                  <label className="font-medium">设置新密码（至少 6 位）</label>
                  <Input
                    type="password"
                    value={forceNewPwd}
                    onChange={(e) => setForceNewPwd(e.target.value)}
                    placeholder="请输入新登录密码"
                  />
                </div>
                <div className="space-y-1">
                  <label className="font-medium">确认新密码</label>
                  <Input
                    type="password"
                    value={forceConfirmPwd}
                    onChange={(e) => setForceConfirmPwd(e.target.value)}
                    placeholder="再次输入新登录密码"
                  />
                </div>
                <div className="flex items-center justify-between gap-2 pt-2">
                  <Button type="button" size="sm" variant="outline" onClick={() => void handleLogout()}>
                    <LogOut className="h-3.5 w-3.5" />
                    <span>退出登录</span>
                  </Button>
                  <Button type="submit" size="sm" disabled={forcePwdBusy}>
                    {forcePwdBusy ? '保存中...' : '确认修改并进入'}
                  </Button>
                </div>
              </form>
            </CardContent>
          </Card>
        </div>
      )}
    </>
  )
}

export function App() {
  return (
    <ToastProvider>
      <WorkspaceApp />
    </ToastProvider>
  )
}

export default App
