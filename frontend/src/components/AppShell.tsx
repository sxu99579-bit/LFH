import React, { useState } from 'react'
import {
  Activity,
  AlertTriangle,
  ArrowUpDown,
  Clipboard,
  Copy,
  Cpu,
  FilePlus2,
  Files,
  FolderUp,
  HardDrive,
  Laptop,
  LogOut,
  Menu,
  Moon,
  Monitor,
  Plus,
  RefreshCw,
  Search,
  Settings,
  Smartphone,
  Sun,
  Upload,
  X,
} from 'lucide-react'
import type { NavTab, PoolSummary, ServerInfo, ThemeMode, TransferTask, User } from '@/types'
import { cn, formatQuotaDisplay } from '@/lib/utils'
import { Badge, Button, Sheet, useToast } from '@/components/ui'

interface AppShellProps {
  user: User
  info: ServerInfo | null
  pool: PoolSummary | null
  activeTab: NavTab
  onTabChange: (tab: NavTab) => void
  theme: ThemeMode
  onToggleTheme: () => void
  isMobile: boolean
  tasks: TransferTask[]
  onToggleTransferDrawer: () => void
  onOpenCommandPalette: () => void
  onQuickUploadFiles: () => void
  onQuickCreateTxt: () => void
  onQuickLocalImport: () => void
  onReconcileClusterVersion: () => Promise<void>
  onLogout: () => void
  children: React.ReactNode
}

export function AppShell({
  user,
  info,
  pool,
  activeTab,
  onTabChange,
  theme,
  onToggleTheme,
  isMobile,
  tasks,
  onToggleTransferDrawer,
  onOpenCommandPalette,
  onQuickUploadFiles,
  onQuickCreateTxt,
  onQuickLocalImport,
  onReconcileClusterVersion,
  onLogout,
  children,
}: AppShellProps) {
  const { toast } = useToast()
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false)
  const [reconciling, setReconciling] = useState(false)

  const isAdmin = user.role === 'admin'
  const activeTransfers = tasks.filter((t) => t.status === 'running' || t.status === 'queued').length
  const totalUsed = pool?.total_used_bytes ?? 0
  const totalQuota = pool?.total_quota_bytes ?? 0
  const quotaPct = totalQuota > 0 ? Math.min(100, Math.round((totalUsed * 100) / totalQuota)) : 0
  const entryUrls =
    info?.entry_addrs && info.entry_addrs.length > 0
      ? info.entry_addrs.map((a) => ({ label: `${a.tag ? `${a.tag} · ` : ''}${a.url}`, url: a.url }))
      : info?.entry_urls && info.entry_urls.length > 0
      ? info.entry_urls.map((u) => ({ label: u, url: u }))
      : info?.entry_url
      ? [{ label: info.entry_url, url: info.entry_url }]
      : [{ label: window.location.origin, url: window.location.origin }]
  const [selectedEntryUrl, setSelectedEntryUrl] = useState('')
  const displayEntryUrl = selectedEntryUrl || entryUrls[0]?.url || window.location.origin

  const allNavItems: Array<{ id: NavTab; label: string; shortLabel: string; icon: React.ComponentType<{ className?: string }>; adminOnly?: boolean }> = [
    { id: 'files', label: '文件中心', shortLabel: '文件', icon: Files },
    { id: 'clipboard', label: '超级剪贴板', shortLabel: '剪贴板', icon: Clipboard },
    { id: 'plugins', label: '算力与插件', shortLabel: '算力', icon: Cpu, adminOnly: true },
    { id: 'health', label: '节点健康', shortLabel: '监控', icon: Activity },
    { id: 'settings', label: '系统设置', shortLabel: '设置', icon: Settings },
  ]
  const navItems = allNavItems.filter((item) => !item.adminOnly || isAdmin)

  const copyEntryUrl = async () => {
    try {
      await navigator.clipboard.writeText(displayEntryUrl)
      toast(`已复制入口地址：${displayEntryUrl}`, 'success')
    } catch {
      toast(displayEntryUrl, 'info')
    }
  }

  const handleVersionReconcile = async () => {
    setReconciling(true)
    try {
      await onReconcileClusterVersion()
    } finally {
      setReconciling(false)
    }
  }

  const versionMismatch = Boolean(pool && pool.version_consistent === false)

  const SidebarContent = (
    <div className="flex h-full flex-col bg-sidebar text-sidebar-foreground">
      {/* Brand header */}
      <div className="flex h-14 items-center gap-2.5 border-b border-sidebar-border px-4">
        <img src="/logo.png" alt="LFH" className="h-8 w-8 rounded-lg object-cover shadow-xs" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold tracking-tight">LFH</div>
          <div className="truncate text-[11px] text-muted-foreground">
            {info?.version || 'V0.11.22.4'} · 分布式共享池
          </div>
        </div>
        {sidebarOpen && (
          <Button variant="ghost" size="icon" className="lg:hidden" onClick={() => setSidebarOpen(false)}>
            <X className="h-4 w-4" />
          </Button>
        )}
      </div>

      {/* Navigation */}
      <div className="flex-1 overflow-y-auto px-3 py-3 space-y-5">
        <div>
          <div className="px-2.5 pb-1.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
            工作台
          </div>
          <nav className="space-y-1">
            {navItems.map((item) => {
              const Icon = item.icon
              const active = activeTab === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => {
                    onTabChange(item.id)
                    setSidebarOpen(false)
                  }}
                  className={cn(
                    'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm font-medium transition-colors cursor-pointer',
                    active
                      ? 'bg-sidebar-accent text-sidebar-accent-foreground shadow-2xs'
                      : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground'
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  <span className="truncate">{item.label}</span>
                  {item.id === 'health' && pool && (
                    <span className="ml-auto rounded-md bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400 tabular-nums">
                      {pool.online_nodes}/{pool.total_nodes}
                    </span>
                  )}
                </button>
              )
            })}
          </nav>
        </div>

        {/* Storage Pool Card */}
        <div className="rounded-xl border border-sidebar-border bg-background/60 p-3 space-y-2.5">
          <div className="flex items-center justify-between text-xs">
            <span className="inline-flex items-center gap-1.5 font-medium">
              <HardDrive className="h-3.5 w-3.5 text-primary" />
              全集群存储池
            </span>
            <span className="text-[11px] text-muted-foreground tabular-nums">{quotaPct}%</span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
            <div
              className={cn(
                'h-full rounded-full transition-all',
                quotaPct >= 90 ? 'bg-destructive' : quotaPct >= 75 ? 'bg-amber-500' : 'bg-primary'
              )}
              style={{ width: `${quotaPct}%` }}
            />
          </div>
          <div className="flex items-center justify-between text-[11px] text-muted-foreground tabular-nums">
            <span>已用 {formatQuotaDisplay(totalUsed)}</span>
            <span>配额 {totalQuota > 0 ? formatQuotaDisplay(totalQuota) : '-'}</span>
          </div>
          <div className="flex items-center justify-between border-t border-sidebar-border pt-2 text-[11px] text-muted-foreground">
            <span>在线节点 {pool?.online_nodes ?? 1}/{pool?.total_nodes ?? 1}</span>
            <span className="tabular-nums">{pool?.total_file_count ?? 0} 个文件</span>
          </div>
        </div>
      </div>

      {/* Footer: View Switch + User */}
      <div className="border-t border-sidebar-border p-3 space-y-2.5">
        <a
          href="/?view=mobile"
          className="flex items-center justify-between rounded-lg border border-sidebar-border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground transition-colors"
        >
          <span className="inline-flex items-center gap-1.5">
            <Smartphone className="h-3.5 w-3.5" />
            预览手机端视图
          </span>
          <span className="text-[10px]">Mobile</span>
        </a>
        <div className="flex items-center justify-between gap-2 rounded-lg bg-background/60 px-2.5 py-2">
          <div className="min-w-0">
            <div className="truncate text-xs font-medium">{user.username}</div>
            <div className="text-[11px] text-muted-foreground">
              {isAdmin ? '集群管理员' : '普通成员'}
            </div>
          </div>
          <Button variant="ghost" size="icon" onClick={onLogout} title="退出登录" className="h-7 w-7 shrink-0">
            <LogOut className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>
  )

  /* ==================== Mobile Layout ==================== */
  if (isMobile) {
    return (
      <div className="min-h-screen bg-background text-foreground flex flex-col pb-[76px]">
        {/* Mobile Header */}
        <header className="sticky top-0 z-30 flex h-13 items-center justify-between gap-2 border-b border-border bg-card/90 px-3 backdrop-blur-md">
          <div className="flex items-center gap-2 min-w-0">
            <img src="/logo.png" alt="LFH" className="h-7 w-7 rounded-lg object-cover shrink-0" />
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">LFH 局域网中枢</div>
              <div className="truncate text-[10px] text-muted-foreground">
                {user.username} · {pool?.online_nodes ?? 1}/{pool?.total_nodes ?? 1} 节点在线
              </div>
            </div>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <Button variant="ghost" size="icon" onClick={onOpenCommandPalette} title="快速搜索">
              <Search className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" onClick={onToggleTransferDrawer} title="传输队列" className="relative">
              <ArrowUpDown className="h-4 w-4" />
              {activeTransfers > 0 && (
                <span className="absolute right-1 top-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-primary px-1 text-[9px] font-bold text-primary-foreground">
                  {activeTransfers}
                </span>
              )}
            </Button>
            <Button variant="ghost" size="icon" onClick={onToggleTheme} title="切换主题">
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
          </div>
        </header>

        {/* Version Mismatch Banner */}
        {versionMismatch && (
          <div
            id="cluster-version-alert"
            className="mx-3 mt-3 flex flex-col gap-2 rounded-xl border border-amber-500/40 bg-amber-500/10 p-3 text-xs"
          >
            <div className="flex items-start gap-2 text-amber-700 dark:text-amber-300">
              <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5" />
              <div>
                <div className="font-semibold">集群节点版本不一致</div>
                <div className="text-[11px] opacity-90">
                  异常节点：{pool?.mismatched_nodes?.join(', ') || '部分节点'}。请保持集群全节点同版本运行。
                </div>
              </div>
            </div>
            {isAdmin && (
              <div className="flex items-center gap-2">
                <Button size="xs" disabled={reconciling} onClick={handleVersionReconcile}>
                  <RefreshCw className={cn('h-3 w-3', reconciling && 'animate-spin')} />
                  <span>{reconciling ? '正在全网同步...' : '一键同步对齐版本'}</span>
                </Button>
                <Button size="xs" variant="outline" onClick={() => onTabChange('settings')}>
                  前往版本管理
                </Button>
              </div>
            )}
          </div>
        )}

        {/* Mobile Body */}
        <main className="flex-1 px-3 py-3">{children}</main>

        {/* Mobile Bottom Tab Bar with center + action button */}
        <nav className="fixed bottom-0 inset-x-0 z-40 border-t border-border bg-card/95 backdrop-blur-md pb-safe">
          <div className="grid grid-cols-5 items-center h-15 px-1">
            {[
              { id: 'files' as NavTab, label: '文件', icon: Files },
              { id: 'clipboard' as NavTab, label: '剪贴板', icon: Clipboard },
            ].map((item) => {
              const Icon = item.icon
              const active = activeTab === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onTabChange(item.id)}
                  className={cn(
                    'flex flex-col items-center justify-center gap-0.5 py-1 text-[11px] cursor-pointer',
                    active ? 'text-primary font-semibold' : 'text-muted-foreground'
                  )}
                >
                  <Icon className="h-4.5 w-4.5" />
                  <span>{item.label}</span>
                </button>
              )
            })}

            {/* Center Quick Action Button */}
            <div className="flex items-center justify-center">
              <button
                type="button"
                onClick={() => setMobileMenuOpen(true)}
                className="flex h-11 w-11 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-md active:scale-95 transition-transform cursor-pointer"
                title="快捷操作"
              >
                <Plus className="h-5 w-5" />
              </button>
            </div>

            {[
              { id: (isAdmin && activeTab === 'plugins' ? 'plugins' : 'health') as NavTab, label: '监控', icon: Activity },
              { id: 'settings' as NavTab, label: '设置', icon: Settings },
            ].map((item) => {
              const Icon = item.icon
              const active = activeTab === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onTabChange(item.id)}
                  className={cn(
                    'flex flex-col items-center justify-center gap-0.5 py-1 text-[11px] cursor-pointer',
                    active ? 'text-primary font-semibold' : 'text-muted-foreground'
                  )}
                >
                  <Icon className="h-4.5 w-4.5" />
                  <span>{item.label}</span>
                </button>
              )
            })}
          </div>
        </nav>

        {/* Mobile Action Sheet */}
        <Sheet
          open={mobileMenuOpen}
          onClose={() => setMobileMenuOpen(false)}
          side="bottom"
          title="快捷操作与入口切换"
          subtitle={`当前版本 ${info?.version || 'V0.11.22.4'}`}
        >
          <div className="grid grid-cols-2 gap-2.5">
            <button
              type="button"
              onClick={() => {
                setMobileMenuOpen(false)
                onTabChange('files')
                onQuickUploadFiles()
              }}
              className="flex flex-col items-start gap-1.5 rounded-xl border border-border bg-muted/30 p-3 text-left hover:bg-accent"
            >
              <Upload className="h-4.5 w-4.5 text-primary" />
              <div className="text-sm font-medium">上传文件/照片</div>
              <div className="text-[11px] text-muted-foreground">支持断点续传</div>
            </button>
            <button
              type="button"
              onClick={() => {
                setMobileMenuOpen(false)
                onQuickCreateTxt()
              }}
              className="flex flex-col items-start gap-1.5 rounded-xl border border-border bg-muted/30 p-3 text-left hover:bg-accent"
            >
              <FilePlus2 className="h-4.5 w-4.5 text-primary" />
              <div className="text-sm font-medium">新建 TXT 文本</div>
              <div className="text-[11px] text-muted-foreground">快速记录备忘/代码</div>
            </button>
            {isAdmin && (
              <button
                type="button"
                onClick={() => {
                  setMobileMenuOpen(false)
                  onTabChange('plugins')
                }}
                className="flex flex-col items-start gap-1.5 rounded-xl border border-border bg-muted/30 p-3 text-left hover:bg-accent"
              >
                <Cpu className="h-4.5 w-4.5 text-primary" />
                <div className="text-sm font-medium">算力与插件</div>
                <div className="text-[11px] text-muted-foreground">智能推荐与调度</div>
              </button>
            )}
            {isAdmin && (
              <button
                type="button"
                onClick={() => {
                  setMobileMenuOpen(false)
                  onQuickLocalImport()
                }}
                className="flex flex-col items-start gap-1.5 rounded-xl border border-border bg-muted/30 p-3 text-left hover:bg-accent"
              >
                <FolderUp className="h-4.5 w-4.5 text-primary" />
                <div className="text-sm font-medium">零拷贝导入/挂载</div>
                <div className="text-[11px] text-muted-foreground">同盘硬链秒入库</div>
              </button>
            )}
          </div>
          <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
            <a
              href="/?view=desktop"
              className="inline-flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs hover:bg-accent"
            >
              <Monitor className="h-3.5 w-3.5" />
              <span>切换完整桌面版</span>
            </a>
            <Button variant="outline" size="xs" onClick={copyEntryUrl}>
              <Copy className="h-3 w-3" />
              <span>复制入口地址</span>
            </Button>
          </div>
        </Sheet>
      </div>
    )
  }

  /* ==================== Desktop Layout (shadcn-admin shell) ==================== */
  return (
    <div className="flex min-h-screen bg-background text-foreground">
      {/* Desktop Fixed Sidebar */}
      <aside className="hidden lg:block w-64 shrink-0 border-r border-sidebar-border sticky top-0 h-screen">
        {SidebarContent}
      </aside>

      {/* Mobile-width Drawer fallback when viewport shrinks on desktop entry */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-50 flex lg:hidden">
          <div className="fixed inset-0 bg-black/50 backdrop-blur-xs" onClick={() => setSidebarOpen(false)} />
          <div className="relative z-10 w-64 max-w-[80vw] h-full border-r border-sidebar-border shadow-2xl">
            {SidebarContent}
          </div>
        </div>
      )}

      {/* Main Content Area */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Top Bar */}
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-3 border-b border-border bg-background/85 px-4 sm:px-6 backdrop-blur-md">
          <div className="flex items-center gap-2.5 min-w-0">
            <Button
              variant="ghost"
              size="icon"
              className="lg:hidden"
              onClick={() => setSidebarOpen(true)}
              title="打开菜单"
            >
              <Menu className="h-4.5 w-4.5" />
            </Button>
            <button
              type="button"
              onClick={onOpenCommandPalette}
              className="inline-flex h-8 w-52 sm:w-72 items-center justify-between rounded-md border border-input bg-muted/40 px-2.5 text-xs text-muted-foreground hover:bg-accent transition-colors cursor-pointer"
            >
              <span className="inline-flex items-center gap-1.5 truncate">
                <Search className="h-3.5 w-3.5 shrink-0" />
                <span className="truncate">全局搜索文件、跳转页面或命令...</span>
              </span>
              <kbd className="hidden sm:inline-block rounded border border-border bg-background px-1.5 py-0.5 text-[10px] font-mono">
                Ctrl K
              </kbd>
            </button>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            {/* Multi-NIC Entry Address Switcher */}
            <div className="hidden md:flex items-center gap-1 rounded-md border border-border bg-muted/30 pl-2 pr-1 py-0.5 text-xs">
              <Laptop className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
              <select
                value={displayEntryUrl}
                onChange={(e) => setSelectedEntryUrl(e.target.value)}
                className="max-w-[200px] truncate bg-transparent text-xs outline-none cursor-pointer"
                title="入口访问地址"
              >
                {entryUrls.map((item) => (
                  <option key={item.url} value={item.url} className="bg-card text-foreground">
                    {item.label}
                  </option>
                ))}
              </select>
              <Button variant="ghost" size="icon" className="h-6 w-6" onClick={copyEntryUrl} title="复制入口地址">
                <Copy className="h-3 w-3" />
              </Button>
            </div>

            <Badge variant="success" className="hidden sm:inline-flex tabular-nums">
              {pool?.online_nodes ?? 1}/{pool?.total_nodes ?? 1} 节点在线
            </Badge>

            <Button variant="outline" size="xs" onClick={onToggleTransferDrawer} className="gap-1.5">
              <ArrowUpDown className="h-3.5 w-3.5" />
              <span className="hidden sm:inline">传输队列</span>
              {activeTransfers > 0 && (
                <Badge variant="default" className="px-1.5 py-0 text-[10px] tabular-nums">
                  {activeTransfers}
                </Badge>
              )}
            </Button>

            <Button variant="ghost" size="icon" onClick={onToggleTheme} title="切换浅色/深色主题">
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
          </div>
        </header>

        {/* Global Cluster Version Consistency Warning Banner */}
        {versionMismatch && (
          <div
            id="cluster-version-alert"
            className="mx-4 mt-4 sm:mx-6 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-xs"
          >
            <div className="flex items-start gap-2.5 text-amber-700 dark:text-amber-300">
              <AlertTriangle className="h-4.5 w-4.5 shrink-0 mt-0.5" />
              <div>
                <div className="font-semibold text-sm">检测到集群节点版本不同步</div>
                <div className="opacity-90 mt-0.5">
                  当前入口版本 {pool?.entry_version || info?.version}，全网最高版本 {pool?.highest_node_version || '-'}，差异节点：{pool?.mismatched_nodes?.join(', ') || '部分节点'}。为保证功能完整，严禁不同版本混用。
                </div>
              </div>
            </div>
            {isAdmin && (
              <div className="flex items-center gap-2 shrink-0">
                <Button size="xs" disabled={reconciling} onClick={handleVersionReconcile}>
                  <RefreshCw className={cn('h-3.5 w-3.5', reconciling && 'animate-spin')} />
                  <span>{reconciling ? '正在同步全集群...' : '自动同步到最新节点 / GitHub 最新版'}</span>
                </Button>
                <Button size="xs" variant="outline" onClick={() => onTabChange('settings')}>
                  选择指定版本退回
                </Button>
              </div>
            )}
          </div>
        )}

        {/* Quick Action Floating Context Strip on non-file pages is unnecessary; pages own their actions */}
        <main className="flex-1 px-4 py-5 sm:px-6 max-w-[1600px] w-full mx-auto">
          {children}
        </main>
      </div>
    </div>
  )
}
