import React, { useMemo, useRef, useState } from 'react'
import {
  Archive,
  Bot,
  Code2,
  Cpu,
  Download,
  Edit3,
  Eye,
  File,
  FileAudio,
  FileImage,
  FilePlus2,
  FileText,
  FileVideo,
  Folder,
  FolderUp,
  HardDrive,
  Info,
  LayoutGrid,
  List,
  Lock,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Upload,
  Users,
} from 'lucide-react'
import type {
  ClusterCapabilityView,
  FileRecord,
  NodeStatus,
  SummaryStats,
  User,
} from '@/types'
import {
  canPreviewFile,
  categoryNames,
  cn,
  extractAgentLabel,
  formatSize,
  formatStorageModeLabel,
  formatTime,
  getFileExt,
  getMatchingCapabilitiesForFile,
  isAgentDeviceName,
  isEditableTextFile,
} from '@/lib/utils'
import { Badge, Button, Card, Input, Select, useToast } from '@/components/ui'
import type { AppleIconSize } from '@/components/AppleFileIcons'
import { AppleFilesGrid } from '@/components/AppleFilesGrid'

interface FilesPageProps {
  user: User
  files: FileRecord[]
  summary: SummaryStats | null
  nodes: NodeStatus[]
  capabilities: ClusterCapabilityView[]
  selectableUsers: Array<{ username: string; role: string }>
  writableMountIds?: Set<string>
  isMobile: boolean
  loading: boolean
  searchQuery: string
  categoryFilter: string
  monthFilter: string
  nodeFilter: string
  deviceFilter: string
  onFilterChange: (patch: {
    searchQuery?: string
    categoryFilter?: string
    monthFilter?: string
    nodeFilter?: string
    deviceFilter?: string
  }) => void
  onRefresh: () => void
  onUploadFiles: (
    items: Array<{ file: File; relativePath: string }>,
    targetNode: string,
    allowedUsers: string[]
  ) => void
  onInspectFile: (rec: FileRecord) => void
  onPreviewFile: (rec: FileRecord) => void
  onEditFile: (rec: FileRecord) => void
  onDownloadFile: (rec: FileRecord) => void
  onOpenCreateTxt: () => void
  onOpenLocalImport: () => void
  fileInputRef: React.RefObject<HTMLInputElement | null>
  folderInputRef: React.RefObject<HTMLInputElement | null>
}

function CategoryIcon({ category, className = 'h-4 w-4' }: { category: string; className?: string }) {
  switch (category) {
    case 'video':
      return <FileVideo className={cn(className, 'text-purple-500')} />
    case 'image':
      return <FileImage className={cn(className, 'text-sky-500')} />
    case 'audio':
      return <FileAudio className={cn(className, 'text-pink-500')} />
    case 'doc':
      return <FileText className={cn(className, 'text-amber-500')} />
    case 'archive':
      return <Archive className={cn(className, 'text-orange-500')} />
    case 'code':
      return <Code2 className={cn(className, 'text-emerald-500')} />
    default:
      return <File className={cn(className, 'text-muted-foreground')} />
  }
}

const IMAGE_THUMB_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'])

async function readAllEntriesFromDrop(dt: DataTransfer): Promise<Array<{ file: File; relativePath: string }>> {
  const items = Array.from(dt.items || [])
  const out: Array<{ file: File; relativePath: string }> = []

  const walkEntry = async (entry: FileSystemEntry, prefix = ''): Promise<void> => {
    if (entry.isFile) {
      await new Promise<void>((resolve) => {
        ;(entry as FileSystemFileEntry).file(
          (f) => {
            const rel = prefix ? `${prefix}/${f.name}` : f.name
            out.push({ file: f, relativePath: rel })
            resolve()
          },
          () => resolve()
        )
      })
      return
    }
    if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader()
      const nextPrefix = prefix ? `${prefix}/${entry.name}` : entry.name
      await new Promise<void>((resolve) => {
        const readBatch = () => {
          reader.readEntries(
            async (entries) => {
              if (!entries.length) {
                resolve()
                return
              }
              for (const child of entries) {
                await walkEntry(child, nextPrefix)
              }
              readBatch()
            },
            () => resolve()
          )
        }
        readBatch()
      })
    }
  }

  let usedEntries = false
  for (const item of items) {
    const entry = item.webkitGetAsEntry?.()
    if (entry) {
      usedEntries = true
      await walkEntry(entry, '')
    }
  }

  if (!usedEntries && dt.files) {
    for (const f of Array.from(dt.files)) {
      out.push({ file: f, relativePath: f.name })
    }
  }
  return out
}

export function FilesPage({
  user,
  files,
  summary,
  nodes,
  capabilities,
  selectableUsers,
  writableMountIds,
  isMobile,
  loading,
  searchQuery,
  categoryFilter,
  monthFilter,
  nodeFilter,
  deviceFilter,
  onFilterChange,
  onRefresh,
  onUploadFiles,
  onInspectFile,
  onPreviewFile,
  onEditFile,
  onDownloadFile,
  onOpenCreateTxt,
  onOpenLocalImport,
  fileInputRef,
  folderInputRef,
}: FilesPageProps) {
  const { toast } = useToast()
  const isAdmin = user.role === 'admin'
  const [viewMode, setViewModeState] = useState<'table' | 'grid'>(() => {
    if (isMobile) return 'grid'
    try {
      const saved = localStorage.getItem('lfh_files_view_mode')
      if (saved === 'table' || saved === 'grid') return saved
    } catch {
      // ignore
    }
    return 'grid'
  })
  const [groupByFolder, setGroupByFolderState] = useState<boolean>(() => {
    try {
      return localStorage.getItem('lfh_files_group_folders') !== '0'
    } catch {
      return true
    }
  })
  const [iconSize, setIconSizeState] = useState<AppleIconSize>(() => {
    try {
      const saved = localStorage.getItem('lfh_files_icon_size')
      if (saved === 'sm' || saved === 'md' || saved === 'lg') return saved
    } catch {
      // ignore
    }
    return 'md'
  })

  const setViewMode = (mode: 'table' | 'grid') => {
    setViewModeState(mode)
    try {
      localStorage.setItem('lfh_files_view_mode', mode)
    } catch {
      // ignore
    }
  }
  const setGroupByFolder = (val: boolean) => {
    setGroupByFolderState(val)
    try {
      localStorage.setItem('lfh_files_group_folders', val ? '1' : '0')
    } catch {
      // ignore
    }
  }
  const setIconSize = (sz: AppleIconSize) => {
    setIconSizeState(sz)
    try {
      localStorage.setItem('lfh_files_icon_size', sz)
    } catch {
      // ignore
    }
  }

  const hasActiveFilter = Boolean(
    searchQuery.trim() || categoryFilter || monthFilter || nodeFilter || deviceFilter
  )
  const [targetNode, setTargetNode] = useState('auto')
  const [allowedUsers, setAllowedUsers] = useState<string[]>([])
  const [showVisibilityPicker, setShowVisibilityPicker] = useState(false)
  const [showFilters, setShowFilters] = useState(false)
  const [dragging, setDragging] = useState(false)
  const dragDepthRef = useRef(0)

  const onlineNodes = useMemo(() => nodes.filter((n) => n.online), [nodes])

  const handleFilesSelected = (list: FileList | null) => {
    if (!list || list.length === 0) return
    const items = Array.from(list).map((f) => ({
      file: f,
      relativePath: (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name,
    }))
    onUploadFiles(items, targetNode, allowedUsers)
  }

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragDepthRef.current = 0
    setDragging(false)
    if (!e.dataTransfer) return
    const items = await readAllEntriesFromDrop(e.dataTransfer)
    if (items.length === 0) {
      toast('未检测到可上传的文件', 'info')
      return
    }
    onUploadFiles(items, targetNode, allowedUsers)
  }

  const toggleAllowedUser = (username: string) => {
    setAllowedUsers((prev) =>
      prev.includes(username) ? prev.filter((x) => x !== username) : [...prev, username]
    )
  }

  const categoryBuckets = summary?.by_category || []
  const monthBuckets = summary?.by_time || []
  const deviceBuckets = summary?.by_device || []

  return (
    <div
      className="relative space-y-4"
      onDragEnter={(e) => {
        e.preventDefault()
        dragDepthRef.current++
        setDragging(true)
      }}
      onDragOver={(e) => {
        e.preventDefault()
      }}
      onDragLeave={(e) => {
        e.preventDefault()
        dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
        if (dragDepthRef.current === 0) setDragging(false)
      }}
      onDrop={handleDrop}
    >
      {/* Drag Overlay */}
      {dragging && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm p-6 pointer-events-none">
          <div className="flex max-w-md flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-primary bg-card p-8 text-center shadow-2xl">
            <Upload className="h-10 w-10 text-primary animate-bounce" />
            <div className="text-base font-semibold">松开即可批量入库</div>
            <div className="text-xs text-muted-foreground">
              支持拖入多个文件或完整文件夹，自动保留相对路径，4MB 分片断点续传
            </div>
          </div>
        </div>
      )}

      {/* Hidden File Inputs */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          handleFilesSelected(e.target.files)
          e.target.value = ''
        }}
      />
      <input
        ref={folderInputRef}
        type="file"
        multiple
        className="hidden"
        {...({ webkitdirectory: '', directory: '' } as React.InputHTMLAttributes<HTMLInputElement>)}
        onChange={(e) => {
          handleFilesSelected(e.target.files)
          e.target.value = ''
        }}
      />

      {/* Page Title & Action Strip */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg sm:text-xl font-semibold tracking-tight">文件中心</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            全集群统一命名空间 · 共 <span className="tabular-nums font-medium text-foreground">{files.length}</span> 项
            {summary && (
              <>
                {' '}
                · 总量 <span className="tabular-nums">{formatSize(summary.total_size)}</span>
              </>
            )}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => fileInputRef.current?.click()}>
            <Upload className="h-3.5 w-3.5" />
            <span>上传文件</span>
          </Button>
          {!isMobile && (
            <Button size="sm" variant="outline" onClick={() => folderInputRef.current?.click()}>
              <FolderUp className="h-3.5 w-3.5" />
              <span>上传目录</span>
            </Button>
          )}
          <Button size="sm" variant="outline" onClick={onOpenCreateTxt}>
            <FilePlus2 className="h-3.5 w-3.5" />
            <span>新建 TXT</span>
          </Button>
          {isAdmin && (
            <Button size="sm" variant="secondary" onClick={onOpenLocalImport}>
              <HardDrive className="h-3.5 w-3.5" />
              <span>零拷贝导入/挂载</span>
            </Button>
          )}
        </div>
      </div>

      {/* Upload Routing & Search / Filter Toolbar */}
      <Card className="p-3 sm:p-4 space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative flex-1 min-w-[200px]">
            <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input
              value={searchQuery}
              onChange={(e) => onFilterChange({ searchQuery: e.target.value })}
              placeholder="搜索文件名、目录路径或上传者..."
              className="pl-8 h-9"
            />
          </div>

          <Button
            size="sm"
            variant={showFilters ? 'secondary' : 'outline'}
            onClick={() => setShowFilters((v) => !v)}
          >
            <SlidersHorizontal className="h-3.5 w-3.5" />
            <span>筛选与路由</span>
          </Button>

          {!isMobile && (
            <div className="flex items-center rounded-md border border-border p-0.5">
              <button
                type="button"
                onClick={() => setViewMode('table')}
                className={cn(
                  'rounded px-2 py-1 text-xs cursor-pointer',
                  viewMode === 'table' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground'
                )}
                title="列表视图"
              >
                <List className="h-3.5 w-3.5" />
              </button>
              <button
                type="button"
                onClick={() => setViewMode('grid')}
                className={cn(
                  'rounded px-2 py-1 text-xs cursor-pointer',
                  viewMode === 'grid' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground'
                )}
                title="图标视图 (Apple Finder 风格)"
              >
                <LayoutGrid className="h-3.5 w-3.5" />
              </button>
            </div>
          )}

          {(viewMode === 'grid' || isMobile) && (
            <>
              <Button
                size="sm"
                variant={groupByFolder && !hasActiveFilter ? 'secondary' : 'outline'}
                onClick={() => setGroupByFolder(!groupByFolder)}
                disabled={hasActiveFilter}
                title={
                  hasActiveFilter
                    ? '筛选或搜索时自动平铺显示匹配文件'
                    : groupByFolder
                    ? '按目录层级聚合（点击切换平铺）'
                    : '全部平铺（点击按目录聚合）'
                }
              >
                <Folder className="h-3.5 w-3.5 text-[#4aa1ec]" />
                <span>{groupByFolder && !hasActiveFilter ? '按目录' : '平铺'}</span>
              </Button>

              {isMobile ? (
                <div className="flex items-center rounded-md border border-border p-0.5 text-[11px]">
                  {(
                    [
                      { id: 'md', label: '3列' },
                      { id: 'lg', label: '2列' },
                    ] as Array<{ id: AppleIconSize; label: string }>
                  ).map((opt) => (
                    <button
                      key={opt.id}
                      type="button"
                      onClick={() => setIconSize(opt.id)}
                      className={cn(
                        'rounded px-2 py-1 cursor-pointer transition-colors',
                        (iconSize === opt.id || (iconSize === 'sm' && opt.id === 'md'))
                          ? 'bg-accent text-accent-foreground font-medium'
                          : 'text-muted-foreground hover:text-foreground'
                      )}
                      title={`切换为每行 ${opt.label}`}
                    >
                      {opt.label}
                    </button>
                  ))}
                </div>
              ) : (
                <div className="flex items-center rounded-md border border-border p-0.5 text-[11px]">
                  {(
                    [
                      { id: 'sm', label: '小' },
                      { id: 'md', label: '中' },
                      { id: 'lg', label: '大' },
                    ] as Array<{ id: AppleIconSize; label: string }>
                  ).map((opt) => (
                    <button
                      key={opt.id}
                      type="button"
                      onClick={() => setIconSize(opt.id)}
                      className={cn(
                        'rounded px-2 py-1 cursor-pointer transition-colors',
                        iconSize === opt.id
                          ? 'bg-accent text-accent-foreground font-medium'
                          : 'text-muted-foreground hover:text-foreground'
                      )}
                      title={`图标尺寸：${opt.label}`}
                    >
                      {opt.label}
                    </button>
                  ))}
                </div>
              )}
            </>
          )}

          <Button size="sm" variant="ghost" onClick={onRefresh} disabled={loading} title="刷新列表">
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          </Button>
        </div>

        {/* Category Quick Pills */}
        <div className="flex items-center gap-1.5 overflow-x-auto pb-0.5">
          <button
            type="button"
            onClick={() => onFilterChange({ categoryFilter: '' })}
            className={cn(
              'shrink-0 rounded-full border px-2.5 py-1 text-xs transition-colors cursor-pointer',
              !categoryFilter
                ? 'border-primary bg-primary text-primary-foreground'
                : 'border-border bg-background text-muted-foreground hover:text-foreground'
            )}
          >
            全部 ({summary?.total_count ?? files.length})
          </button>
          {categoryBuckets.map((b) => (
            <button
              key={b.key}
              type="button"
              onClick={() => onFilterChange({ categoryFilter: categoryFilter === b.key ? '' : b.key })}
              className={cn(
                'shrink-0 rounded-full border px-2.5 py-1 text-xs transition-colors cursor-pointer tabular-nums',
                categoryFilter === b.key
                  ? 'border-primary bg-primary text-primary-foreground'
                  : 'border-border bg-background text-muted-foreground hover:text-foreground'
              )}
            >
              {b.label || categoryNames[b.key] || b.key} ({b.count})
            </button>
          ))}
        </div>

        {/* Collapsible Filter + Upload Target Bar */}
        {showFilters && (
          <div className="grid gap-2.5 border-t border-border pt-3 sm:grid-cols-2 lg:grid-cols-5 text-xs">
            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">按归档月份筛选</div>
              <Select
                value={monthFilter}
                onChange={(e) => onFilterChange({ monthFilter: e.target.value })}
                className="w-full h-8 text-xs"
              >
                <option value="">全部月份</option>
                {monthBuckets.map((m) => (
                  <option key={m.key} value={m.key}>
                    {m.label} ({m.count})
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">按所在存储节点筛选</div>
              <Select
                value={nodeFilter}
                onChange={(e) => onFilterChange({ nodeFilter: e.target.value })}
                className="w-full h-8 text-xs"
              >
                <option value="">全部节点</option>
                {nodes.map((n) => (
                  <option key={n.node_id} value={n.node_id}>
                    {n.node_name} {n.online ? '(在线)' : '(离线)'}
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">按上传设备筛选</div>
              <Select
                value={deviceFilter}
                onChange={(e) => onFilterChange({ deviceFilter: e.target.value })}
                className="w-full h-8 text-xs"
              >
                <option value="">全部设备</option>
                {deviceBuckets.map((d) => (
                  <option key={d.key} value={d.key}>
                    {d.label} ({d.count})
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">上传目标存储节点</div>
              <Select
                value={targetNode}
                onChange={(e) => setTargetNode(e.target.value)}
                className="w-full h-8 text-xs"
              >
                <option value="auto">智能调度（推荐）</option>
                {onlineNodes.map((n) => (
                  <option key={n.node_id} value={n.node_id}>
                    {n.node_name}（余 {formatSize(n.free_bytes)}）
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">新上传默认可见范围</div>
              <Button
                variant="outline"
                size="sm"
                className="w-full justify-between h-8 text-xs font-normal"
                onClick={() => setShowVisibilityPicker((v) => !v)}
              >
                <span className="inline-flex items-center gap-1.5 truncate">
                  <Users className="h-3.5 w-3.5 text-muted-foreground" />
                  {allowedUsers.length === 0 ? '全员可见' : `指定 ${allowedUsers.length} 人可见`}
                </span>
                <span className="text-[10px] text-muted-foreground">设置</span>
              </Button>
            </div>

            {showVisibilityPicker && selectableUsers.length > 0 && (
              <div className="sm:col-span-2 lg:col-span-5 rounded-lg border border-border bg-muted/30 p-2.5 flex flex-wrap items-center gap-1.5">
                <span className="text-[11px] text-muted-foreground mr-1">
                  指定可见成员（未勾选任何成员时默认全员可见）：
                </span>
                {selectableUsers.map((u) => {
                  const checked = allowedUsers.includes(u.username)
                  return (
                    <button
                      key={u.username}
                      type="button"
                      onClick={() => toggleAllowedUser(u.username)}
                      className={cn(
                        'rounded-md border px-2 py-0.5 text-[11px] cursor-pointer transition-colors',
                        checked
                          ? 'border-primary bg-primary/15 text-primary font-medium'
                          : 'border-border bg-background text-muted-foreground hover:text-foreground'
                      )}
                    >
                      {u.username}
                    </button>
                  )
                })}
                {allowedUsers.length > 0 && (
                  <Button size="xs" variant="ghost" onClick={() => setAllowedUsers([])}>
                    重置为全员可见
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </Card>

      {/* File List / Grid */}
      {files.length === 0 ? (
        <Card className="p-10 text-center space-y-2">
          <div className="text-sm font-medium">暂无匹配文件</div>
          <p className="text-xs text-muted-foreground">
            可以直接将本地文件或文件夹拖拽到此处上传，或点击上方按钮入库。
          </p>
        </Card>
      ) : viewMode === 'table' && !isMobile ? (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-border bg-muted/40 text-muted-foreground">
                <tr>
                  <th className="px-4 py-2.5 font-medium">文件名与目录</th>
                  <th className="px-3 py-2.5 font-medium whitespace-nowrap">大小</th>
                  <th className="px-3 py-2.5 font-medium whitespace-nowrap">存储节点</th>
                  <th className="px-3 py-2.5 font-medium whitespace-nowrap">版本/模式</th>
                  <th className="px-3 py-2.5 font-medium whitespace-nowrap">上传者 / 设备</th>
                  <th className="px-3 py-2.5 font-medium whitespace-nowrap">时间</th>
                  <th className="px-4 py-2.5 font-medium text-right whitespace-nowrap">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {files.map((rec) => {
                  const previewable = canPreviewFile(rec)
                  const editable = isEditableTextFile(rec, writableMountIds)
                  const matchingCaps = getMatchingCapabilitiesForFile(capabilities, rec.name)
                  const storageLabel = formatStorageModeLabel(rec.storage_mode)
                  const restricted = rec.allowed_users && rec.allowed_users.length > 0
                  const fromAgent = isAgentDeviceName(rec.device_name)
                  return (
                    <tr
                      key={rec.id}
                      onClick={() => onInspectFile(rec)}
                      className="hover:bg-muted/35 transition-colors cursor-pointer"
                    >
                      <td className="px-4 py-2.5 max-w-[340px]">
                        <div className="flex items-center gap-2.5 min-w-0">
                          {IMAGE_THUMB_EXTS.has(getFileExt(rec)) && !rec.source_missing && rec.node_online !== false ? (
                            <img
                              src={`/api/files/${encodeURIComponent(rec.id)}/download`}
                              alt={rec.name}
                              loading="lazy"
                              decoding="async"
                              className="h-8 w-8 rounded-md border border-border object-cover shrink-0 bg-muted"
                            />
                          ) : (
                            <div className="flex h-8 w-8 items-center justify-center rounded-md border border-border bg-muted/40 shrink-0">
                              <CategoryIcon category={rec.category} />
                            </div>
                          )}
                          <div className="min-w-0">
                            <div className="flex items-center gap-1.5 min-w-0">
                              <span className="truncate font-medium text-foreground">{rec.name}</span>
                              {restricted && (
                                <span title="仅指定成员可见">
                                  <Lock className="h-3 w-3 text-amber-500 shrink-0" />
                                </span>
                              )}
                              {rec.source_missing && (
                                <Badge variant="destructive">源文件丢失</Badge>
                              )}
                            </div>
                            {rec.folder_path && (
                              <div className="truncate text-[11px] text-muted-foreground mt-0.5">
                                /{rec.folder_path}
                              </div>
                            )}
                          </div>
                        </div>
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap tabular-nums text-muted-foreground">
                        {formatSize(rec.size)}
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap">
                        <Badge variant={rec.node_online === false ? 'warning' : 'secondary'}>
                          {rec.node_name || '本机节点'}
                        </Badge>
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap">
                        <div className="flex items-center gap-1">
                          <Badge variant="outline" className="tabular-nums">
                            v{rec.version || 1}
                          </Badge>
                          {storageLabel && <Badge variant="default">{storageLabel}</Badge>}
                        </div>
                      </td>
                      <td className="px-3 py-2.5 max-w-[180px]">
                        <div className="truncate text-foreground">{rec.uploader}</div>
                        {fromAgent ? (
                          <Badge variant="default" className="mt-0.5 max-w-full">
                            <Bot className="h-3 w-3 shrink-0" />
                            <span className="truncate">{extractAgentLabel(rec.device_name)}</span>
                          </Badge>
                        ) : (
                          rec.device_name && (
                            <div className="truncate text-[11px] text-muted-foreground">{rec.device_name}</div>
                          )
                        )}
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap text-muted-foreground tabular-nums">
                        {formatTime(rec.uploaded_at)}
                      </td>
                      <td
                        className="px-4 py-2.5 text-right whitespace-nowrap"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <div className="inline-flex items-center justify-end gap-1">
                          {previewable && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => onPreviewFile(rec)}
                              title="在线预览"
                            >
                              <Eye className="h-3.5 w-3.5" />
                            </Button>
                          )}
                          {editable && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => onEditFile(rec)}
                              title="在线编辑"
                            >
                              <Edit3 className="h-3.5 w-3.5" />
                            </Button>
                          )}
                          {matchingCaps.length > 0 && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-primary"
                              onClick={() => onInspectFile(rec)}
                              title={`可用算力插件: ${matchingCaps.map((c) => c.name).join(', ')}`}
                            >
                              <Cpu className="h-3.5 w-3.5" />
                            </Button>
                          )}
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7"
                            onClick={() => onDownloadFile(rec)}
                            title="下载"
                          >
                            <Download className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7"
                            onClick={() => onInspectFile(rec)}
                            title="详情、版本与插件"
                          >
                            <Info className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </Card>
      ) : (
        /* Apple Finder / iPadOS Files style borderless icon canvas */
        <AppleFilesGrid
          files={files}
          user={user}
          capabilities={capabilities}
          writableMountIds={writableMountIds}
          isMobile={isMobile}
          groupByFolder={groupByFolder}
          iconSize={iconSize}
          hasActiveFilter={hasActiveFilter}
          onInspectFile={onInspectFile}
          onPreviewFile={onPreviewFile}
          onEditFile={onEditFile}
          onDownloadFile={onDownloadFile}
          onRefresh={onRefresh}
        />
      )}
    </div>
  )
}
