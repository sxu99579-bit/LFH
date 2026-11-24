import { useCallback, useEffect, useRef, useState } from 'react'
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
import { ToastProvider, useToast } from '@/components/ui'
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

  // File filters
  const [searchQuery, setSearchQuery] = useState('')
  const [categoryFilter, setCategoryFilter] = useState('')
  const [monthFilter, setMonthFilter] = useState('')
  const [nodeFilter, setNodeFilter] = useState('')
  const [deviceFilter, setDeviceFilter] = useState('')

  // Overlay & Modal states
  const [cmdOpen, setCmdOpen] = useState(false)
  const [inspectedFile, setInspectedFile] = useState<FileRecord | null>(null)
  const [previewFile, setPreviewFile] = useState<FileRecord | null>(null)
  const [editingFile, setEditingFile] = useState<FileRecord | null>(null)
  const [createTxtOpen, setCreateTxtOpen] = useState(false)
  const [localImportMode, setLocalImportMode] = useState<'import' | 'mount' | null>(null)

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
  }, [categoryFilter, deviceFilter, monthFilter, nodeFilter, searchQuery, toast])

  const loadAuxData = useCallback(async () => {
    try {
      const [capRes, selRes, taskRes] = await Promise.all([
        api.getPluginCapabilities(),
        api.getSelectableUsers(),
        api.listAgentTasks({ limit: 1 }).catch(() => null),
      ])
      setCapabilities(capRes.capabilities || [])
      setSelectableUsers(selRes.users || [])
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
          }
          return
        }
      } catch {
        // ignore parse error
      }
      handleEvent()
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
  }, [user, loadCluster, loadFilesAndSummary, toast])

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
          alt="LFH"
          className="h-14 w-14 object-contain animate-pulse"
        />
        <div className="text-xs text-muted-foreground">正在连接 LFH 分布式中枢...</div>
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
        onQuickUploadFiles={() => fileInputRef.current?.click()}
        onQuickCreateTxt={() => setCreateTxtOpen(true)}
        onQuickLocalImport={() => setLocalImportMode('import')}
        onReconcileClusterVersion={handleReconcileClusterVersion}
        onLogout={handleLogout}
      >
        {activeTab === 'files' && (
          <FilesPage
            user={user}
            files={files}
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
            nodeFilter={nodeFilter}
            deviceFilter={deviceFilter}
            onFilterChange={(patch) => {
              if (patch.searchQuery !== undefined) setSearchQuery(patch.searchQuery)
              if (patch.categoryFilter !== undefined) setCategoryFilter(patch.categoryFilter)
              if (patch.monthFilter !== undefined) setMonthFilter(patch.monthFilter)
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
            isMobile={isMobile}
            onSummaryChange={(s) => setPendingTasksCount((s.pending || 0) + (s.paused || 0))}
            onOpenAgentSettings={() => {
              setSettingsFocusTab({ tab: 'agent', seq: Date.now() })
              setActiveTab('settings')
            }}
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
        onCreateTxt={() => setCreateTxtOpen(true)}
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
