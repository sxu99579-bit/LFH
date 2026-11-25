import React, { useMemo, useRef, useState } from 'react'
import {
  Archive,
  Bot,
  Calendar,
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
  FolderInput,
  FolderPlus,
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
  X,
} from 'lucide-react'
import { api } from '@/lib/api'
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
import { Badge, Button, Card, Dialog, Input, Select, useToast } from '@/components/ui'
import type { AppleIconSize } from '@/components/AppleFileIcons'
import { AppleFilesGrid } from '@/components/AppleFilesGrid'

interface FilesPageProps {
  user: User
  files: FileRecord[]
  folders: string[]
  currentFolder: string
  onCurrentFolderChange: (folder: string) => void
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
  dateFilter: string
  dateFromFilter: string
  dateToFilter: string
  nodeFilter: string
  deviceFilter: string
  onFilterChange: (patch: {
    searchQuery?: string
    categoryFilter?: string
    monthFilter?: string
    dateFilter?: string
    dateFromFilter?: string
    dateToFilter?: string
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

function shortMobileCategoryLabel(rawLabel: string): string {
  const clean = String(rawLabel || '')
    .replace(/\s*[（(][^）)]*[）)]\s*/g, '')
    .trim()
  return clean || rawLabel
}

function toLocalDateKey(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function shiftDateKey(offsetDays: number): string {
  const d = new Date()
  d.setDate(d.getDate() + offsetDays)
  return toLocalDateKey(d)
}

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
  folders,
  currentFolder,
  onCurrentFolderChange,
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
  dateFilter,
  dateFromFilter,
  dateToFilter,
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
  const canUpload = isAdmin || (user.role !== 'readonly' && user.can_upload !== false)
  const canManageFolders = isAdmin || (user.role !== 'readonly' && user.can_manage_folders !== false)
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

  const [targetNode, setTargetNode] = useState('auto')
  const [allowedUsers, setAllowedUsers] = useState<string[]>([])
  const [showVisibilityPicker, setShowVisibilityPicker] = useState(false)
  const hasActiveFilter = Boolean(
    searchQuery.trim() ||
      categoryFilter ||
      monthFilter ||
      dateFilter ||
      dateFromFilter ||
      dateToFilter ||
      nodeFilter ||
      deviceFilter
  )
  const hasExtraFilters = Boolean(
    monthFilter ||
      dateFilter ||
      dateFromFilter ||
      dateToFilter ||
      nodeFilter ||
      deviceFilter ||
      allowedUsers.length > 0 ||
      targetNode !== 'auto'
  )
  const [showFilters, setShowFilters] = useState(false)
  const [dragging, setDragging] = useState(false)
  const dragDepthRef = useRef(0)

  // Folder management & file move modals
  const [createFolderModal, setCreateFolderModal] = useState<{ parent: string; name: string } | null>(null)
  const [renameFolderModal, setRenameFolderModal] = useState<{ oldPath: string; newPath: string } | null>(null)
  const [deleteFolderModal, setDeleteFolderModal] = useState<{
    path: string
    mode: 'move_to_parent' | 'delete_files'
  } | null>(null)
  const [moveFileModal, setMoveFileModal] = useState<{ rec: FileRecord; folderPath: string } | null>(null)
  const [folderBusy, setFolderBusy] = useState(false)

  const onlineNodes = useMemo(() => nodes.filter((n) => n.online), [nodes])

  const uploadPrefix = !hasActiveFilter && groupByFolder && currentFolder ? `${currentFolder}/` : ''

  const handleFilesSelected = (list: FileList | null) => {
    if (!canUpload) {
      toast('当前账号为只读权限或已被管理员禁止上传写入', 'error')
      return
    }
    if (!list || list.length === 0) return
    const items = Array.from(list).map((f) => {
      const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name
      return {
        file: f,
        relativePath: uploadPrefix ? `${uploadPrefix}${rel}` : rel,
      }
    })
    onUploadFiles(items, targetNode, allowedUsers)
  }

  const handleDrop = async (e: React.DragEvent) => {
    if (e.dataTransfer?.types?.includes('text/lfh-file-id')) return
    e.preventDefault()
    e.stopPropagation()
    dragDepthRef.current = 0
    setDragging(false)
    if (!canUpload) {
      toast('当前账号为只读权限或已被管理员禁止上传写入', 'error')
      return
    }
    if (!e.dataTransfer) return
    const items = await readAllEntriesFromDrop(e.dataTransfer)
    if (items.length === 0) {
      toast('未检测到可上传的文件', 'info')
      return
    }
    const prefixed = uploadPrefix
      ? items.map((it) => ({ ...it, relativePath: `${uploadPrefix}${it.relativePath}` }))
      : items
    onUploadFiles(prefixed, targetNode, allowedUsers)
  }

  const handleMoveFile = async (rec: FileRecord, explicitTargetFolder?: string) => {
    if (explicitTargetFolder !== undefined) {
      const cleanTarget = explicitTargetFolder.replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
      const cleanCur = String(rec.folder_path || '').replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
      if (cleanTarget === cleanCur) return
      try {
        await api.moveFileToFolder(rec.id, cleanTarget)
        toast(cleanTarget ? `已移动到 /${cleanTarget}` : '已移动到根目录', 'success')
        onRefresh()
      } catch (err) {
        toast(err instanceof Error ? err.message : '移动文件失败', 'error')
      }
      return
    }
    setMoveFileModal({
      rec,
      folderPath: String(rec.folder_path || '').replace(/\\/g, '/').replace(/^\/+|\/+$/g, ''),
    })
  }

  const submitCreateFolder = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!createFolderModal) return
    const cleanName = createFolderModal.name.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
    if (!cleanName) {
      toast('请输入文件夹名称', 'error')
      return
    }
    const fullPath = createFolderModal.parent ? `${createFolderModal.parent}/${cleanName}` : cleanName
    setFolderBusy(true)
    try {
      const res = await api.createFolder(fullPath)
      toast(`已创建文件夹 /${res.path}`, 'success')
      setCreateFolderModal(null)
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '创建文件夹失败', 'error')
    } finally {
      setFolderBusy(false)
    }
  }

  const submitRenameFolder = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!renameFolderModal) return
    const oldP = renameFolderModal.oldPath.trim()
    const newP = renameFolderModal.newPath.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
    if (!newP) {
      toast('请输入新的文件夹路径', 'error')
      return
    }
    setFolderBusy(true)
    try {
      const res = await api.renameFolder(oldP, newP)
      toast(`已重命名为 /${res.path}（同步迁移 ${res.moved} 个文件）`, 'success')
      if (currentFolder === oldP || currentFolder.startsWith(`${oldP}/`)) {
        onCurrentFolderChange(`${newP}${currentFolder.slice(oldP.length)}`)
      }
      setRenameFolderModal(null)
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '重命名文件夹失败', 'error')
    } finally {
      setFolderBusy(false)
    }
  }

  const submitDeleteFolder = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!deleteFolderModal) return
    setFolderBusy(true)
    try {
      const res = await api.deleteFolder(deleteFolderModal.path, deleteFolderModal.mode)
      toast(
        deleteFolderModal.mode === 'delete_files'
          ? `已删除文件夹及 ${res.affected} 个文件`
          : `已移除文件夹，${res.affected} 个文件已移出目录`,
        'success'
      )
      if (currentFolder === deleteFolderModal.path || currentFolder.startsWith(`${deleteFolderModal.path}/`)) {
        const parts = deleteFolderModal.path.split('/')
        parts.pop()
        onCurrentFolderChange(parts.join('/'))
      }
      setDeleteFolderModal(null)
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除文件夹失败', 'error')
    } finally {
      setFolderBusy(false)
    }
  }

  const submitMoveFile = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!moveFileModal) return
    const cleanTarget = moveFileModal.folderPath.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
    setFolderBusy(true)
    try {
      await api.moveFileToFolder(moveFileModal.rec.id, cleanTarget)
      toast(cleanTarget ? `已将「${moveFileModal.rec.name}」移至 /${cleanTarget}` : `已移至根目录`, 'success')
      setMoveFileModal(null)
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '移动文件失败', 'error')
    } finally {
      setFolderBusy(false)
    }
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
        if (e.dataTransfer?.types?.includes('text/lfh-file-id')) return
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
          {canUpload ? (
            <>
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
            </>
          ) : (
            <Badge variant="warning" className="gap-1 px-2.5 py-1">
              <Lock className="h-3 w-3" />
              <span>只读 / 禁止上传</span>
            </Badge>
          )}
          {canManageFolders && (
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                setCreateFolderModal({
                  parent: !hasActiveFilter && groupByFolder ? currentFolder : '',
                  name: '',
                })
              }
            >
              <FolderPlus className="h-3.5 w-3.5 text-[#4aa1ec]" />
              <span>新建文件夹</span>
            </Button>
          )}
          {isAdmin && (
            <Button size="sm" variant="secondary" onClick={onOpenLocalImport}>
              <HardDrive className="h-3.5 w-3.5" />
              <span>零拷贝导入/挂载</span>
            </Button>
          )}
        </div>
      </div>

      {/* Upload Routing & Search / Filter Toolbar */}
      <Card
        className={cn(
          isMobile
            ? 'rounded-2xl p-2.5 space-y-2.5 border-border/75 shadow-2xs'
            : 'p-3 sm:p-4 space-y-3'
        )}
      >
        {isMobile ? (
          <div className="space-y-2">
            {/* Mobile Row 1: Full-width iOS pill search bar */}
            <div className="relative w-full">
              <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input
                value={searchQuery}
                onChange={(e) => onFilterChange({ searchQuery: e.target.value })}
                placeholder="搜索文件名、目录或上传者"
                className="h-9 w-full rounded-xl border-transparent bg-muted/65 pl-8.5 pr-8 text-[13px] shadow-none focus-visible:border-primary/40 focus-visible:bg-background"
              />
              {searchQuery && (
                <button
                  type="button"
                  aria-label="清空搜索"
                  onClick={() => onFilterChange({ searchQuery: '' })}
                  className="absolute right-2.5 top-2.5 flex h-4 w-4 items-center justify-center rounded-full bg-muted-foreground/20 text-foreground hover:bg-muted-foreground/30"
                >
                  <X className="h-3 w-3" />
                </button>
              )}
            </div>

            {/* Mobile Row 2: Single balanced horizontal control strip */}
            <div className="flex items-center justify-between gap-1.5">
              <div className="flex items-center gap-1.5 min-w-0">
                <button
                  type="button"
                  onClick={() => setGroupByFolder(!groupByFolder)}
                  disabled={hasActiveFilter}
                  className={cn(
                    'inline-flex h-7.5 items-center gap-1 rounded-lg border px-2.5 text-xs font-medium transition-colors cursor-pointer disabled:opacity-50',
                    groupByFolder && !hasActiveFilter
                      ? 'border-border/80 bg-secondary text-secondary-foreground'
                      : 'border-border/80 bg-background text-muted-foreground'
                  )}
                >
                  <Folder className="h-3.5 w-3.5 text-[#4aa1ec]" />
                  <span>{groupByFolder && !hasActiveFilter ? '按目录' : '平铺'}</span>
                </button>

                <div className="flex h-7.5 items-center rounded-lg border border-border/80 bg-muted/35 p-0.5 text-[11px]">
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
                        'rounded-md px-2 py-0.5 cursor-pointer transition-colors',
                        iconSize === opt.id || (iconSize === 'sm' && opt.id === 'md')
                          ? 'bg-background text-foreground font-medium shadow-2xs'
                          : 'text-muted-foreground'
                      )}
                      title={`切换为每行 ${opt.label}`}
                    >
                      {opt.label}
                    </button>
                  ))}
                </div>
              </div>

              <div className="flex items-center gap-1.5 shrink-0">
                <button
                  type="button"
                  onClick={() => setShowFilters((v) => !v)}
                  className={cn(
                    'relative inline-flex h-7.5 items-center gap-1 rounded-lg border px-2.5 text-xs font-medium transition-colors cursor-pointer',
                    showFilters || hasExtraFilters
                      ? 'border-primary/40 bg-primary/10 text-primary'
                      : 'border-border/80 bg-background text-foreground'
                  )}
                >
                  <SlidersHorizontal className="h-3.5 w-3.5" />
                  <span>筛选</span>
                  {hasExtraFilters && !showFilters && (
                    <span className="h-1.5 w-1.5 rounded-full bg-primary" />
                  )}
                </button>

                <button
                  type="button"
                  onClick={onRefresh}
                  disabled={loading}
                  aria-label="刷新列表"
                  title="刷新列表"
                  className="inline-flex h-7.5 w-7.5 items-center justify-center rounded-lg border border-border/80 bg-background text-muted-foreground hover:text-foreground disabled:opacity-50 cursor-pointer"
                >
                  <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-full md:flex-1 min-w-[200px]">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input
                value={searchQuery}
                onChange={(e) => onFilterChange({ searchQuery: e.target.value })}
                placeholder="搜索文件名、目录路径或上传者..."
                className="pl-8 pr-8 h-9"
              />
              {searchQuery && (
                <button
                  type="button"
                  aria-label="清空搜索"
                  onClick={() => onFilterChange({ searchQuery: '' })}
                  className="absolute right-2.5 top-2.5 flex h-4 w-4 items-center justify-center rounded-full bg-muted text-muted-foreground hover:text-foreground cursor-pointer"
                >
                  <X className="h-3 w-3" />
                </button>
              )}
            </div>

            <Button
              size="sm"
              variant={showFilters || hasExtraFilters ? 'secondary' : 'outline'}
              onClick={() => setShowFilters((v) => !v)}
              className="relative"
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              <span>筛选与路由</span>
              {hasExtraFilters && !showFilters && (
                <span className="h-1.5 w-1.5 rounded-full bg-primary" />
              )}
            </Button>

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

            {viewMode === 'grid' && (
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
              </>
            )}

            <Button size="sm" variant="ghost" onClick={onRefresh} disabled={loading} title="刷新列表">
              <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
            </Button>
          </div>
        )}

        {/* Category Quick Pills (hidden scrollbar + right fade on mobile) */}
        <div className="relative">
          <div className="no-scrollbar flex items-center gap-1.5 overflow-x-auto pr-5">
            <button
              type="button"
              onClick={() => onFilterChange({ categoryFilter: '' })}
              className={cn(
                'shrink-0 rounded-full border border-transparent px-2.5 py-1 text-[11px] sm:text-xs transition-colors cursor-pointer tabular-nums',
                !categoryFilter
                  ? 'bg-primary text-primary-foreground font-medium shadow-2xs'
                  : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'
              )}
            >
              全部 ({summary?.total_count ?? files.length})
            </button>
            {categoryBuckets.map((b) => {
              const rawLabel = b.label || categoryNames[b.key] || b.key
              const label = isMobile ? shortMobileCategoryLabel(rawLabel) : rawLabel
              return (
                <button
                  key={b.key}
                  type="button"
                  onClick={() => onFilterChange({ categoryFilter: categoryFilter === b.key ? '' : b.key })}
                  className={cn(
                    'shrink-0 rounded-full border border-transparent px-2.5 py-1 text-[11px] sm:text-xs transition-colors cursor-pointer tabular-nums',
                    categoryFilter === b.key
                      ? 'bg-primary text-primary-foreground font-medium shadow-2xs'
                      : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'
                  )}
                >
                  {label} ({b.count})
                </button>
              )
            })}
          </div>
          <div className="pointer-events-none absolute inset-y-0 right-0 w-6 bg-gradient-to-l from-card to-transparent" />
        </div>

        {/* Active Date / Range Summary Strip */}
        {(dateFilter || dateFromFilter || dateToFilter || monthFilter) && (
          <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
            <Badge variant="default" className="gap-1.5 py-0.5">
              <Calendar className="h-3 w-3" />
              <span>
                {dateFilter
                  ? `精确日期：${dateFilter}`
                  : dateFromFilter || dateToFilter
                  ? `日期区间：${dateFromFilter || '...'} 至 ${dateToFilter || '...'}`
                  : `归档月份：${monthFilter}`}
              </span>
              <button
                type="button"
                aria-label="清除日期筛选"
                onClick={() =>
                  onFilterChange({
                    monthFilter: '',
                    dateFilter: '',
                    dateFromFilter: '',
                    dateToFilter: '',
                  })
                }
                className="hover:opacity-75 cursor-pointer"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          </div>
        )}

        {/* Collapsible Filter + Upload Target Bar */}
        {showFilters && (
          <div className="grid gap-2.5 border-t border-border pt-3 sm:grid-cols-2 lg:grid-cols-5 text-xs">
            {/* Exact Day & Date Range Picker Block */}
            <div className="sm:col-span-2 lg:col-span-5 rounded-xl border border-border/80 bg-muted/25 p-2.5 space-y-2.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-1.5 text-[11px] font-medium text-foreground">
                  <Calendar className="h-3.5 w-3.5 text-primary" />
                  <span>按日期筛选（支持精确到日或起止范围）</span>
                </div>
                <div className="flex flex-wrap items-center gap-1">
                  {[
                    {
                      label: '全部日期',
                      active: !dateFilter && !dateFromFilter && !dateToFilter && !monthFilter,
                      onClick: () =>
                        onFilterChange({
                          monthFilter: '',
                          dateFilter: '',
                          dateFromFilter: '',
                          dateToFilter: '',
                        }),
                    },
                    {
                      label: '今天',
                      active: dateFilter === shiftDateKey(0),
                      onClick: () =>
                        onFilterChange({
                          monthFilter: '',
                          dateFilter: shiftDateKey(0),
                          dateFromFilter: '',
                          dateToFilter: '',
                        }),
                    },
                    {
                      label: '昨天',
                      active: dateFilter === shiftDateKey(-1),
                      onClick: () =>
                        onFilterChange({
                          monthFilter: '',
                          dateFilter: shiftDateKey(-1),
                          dateFromFilter: '',
                          dateToFilter: '',
                        }),
                    },
                    {
                      label: '近 7 天',
                      active:
                        dateFromFilter === shiftDateKey(-6) && dateToFilter === shiftDateKey(0),
                      onClick: () =>
                        onFilterChange({
                          monthFilter: '',
                          dateFilter: '',
                          dateFromFilter: shiftDateKey(-6),
                          dateToFilter: shiftDateKey(0),
                        }),
                    },
                    {
                      label: '近 30 天',
                      active:
                        dateFromFilter === shiftDateKey(-29) && dateToFilter === shiftDateKey(0),
                      onClick: () =>
                        onFilterChange({
                          monthFilter: '',
                          dateFilter: '',
                          dateFromFilter: shiftDateKey(-29),
                          dateToFilter: shiftDateKey(0),
                        }),
                    },
                  ].map((preset) => (
                    <button
                      key={preset.label}
                      type="button"
                      onClick={preset.onClick}
                      className={cn(
                        'rounded-md border px-2 py-0.5 text-[11px] cursor-pointer transition-colors',
                        preset.active
                          ? 'border-primary bg-primary text-primary-foreground font-medium'
                          : 'border-border bg-background text-muted-foreground hover:text-foreground'
                      )}
                    >
                      {preset.label}
                    </button>
                  ))}
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-3">
                <div className="space-y-1">
                  <div className="text-[11px] text-muted-foreground">精确某一天</div>
                  <Input
                    type="date"
                    value={dateFilter}
                    onChange={(e) =>
                      onFilterChange({
                        monthFilter: '',
                        dateFilter: e.target.value,
                        dateFromFilter: '',
                        dateToFilter: '',
                      })
                    }
                    className="h-8 text-xs"
                  />
                </div>
                <div className="space-y-1">
                  <div className="text-[11px] text-muted-foreground">开始日期（含）</div>
                  <Input
                    type="date"
                    value={dateFromFilter}
                    onChange={(e) =>
                      onFilterChange({
                        monthFilter: '',
                        dateFilter: '',
                        dateFromFilter: e.target.value,
                      })
                    }
                    className="h-8 text-xs"
                  />
                </div>
                <div className="space-y-1">
                  <div className="text-[11px] text-muted-foreground">结束日期（含）</div>
                  <Input
                    type="date"
                    value={dateToFilter}
                    onChange={(e) =>
                      onFilterChange({
                        monthFilter: '',
                        dateFilter: '',
                        dateToFilter: e.target.value,
                      })
                    }
                    className="h-8 text-xs"
                  />
                </div>
              </div>
            </div>

            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">按归档月份筛选</div>
              <Select
                value={monthFilter}
                onChange={(e) =>
                  onFilterChange({
                    monthFilter: e.target.value,
                    dateFilter: '',
                    dateFromFilter: '',
                    dateToFilter: '',
                  })
                }
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
      {files.length === 0 && (!groupByFolder || hasActiveFilter || (folders.length === 0 && !currentFolder)) ? (
        <Card className="p-10 text-center space-y-2">
          <div className="text-sm font-medium">暂无匹配文件</div>
          <p className="text-xs text-muted-foreground">
            可以直接将本地文件或文件夹拖拽到此处上传，或点击上方「上传文件 / 新建文件夹」。
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
                          {(user.role === 'admin' || rec.uploader === user.username) && !rec.mount_id && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => void handleMoveFile(rec)}
                              title="移动到文件夹"
                            >
                              <FolderInput className="h-3.5 w-3.5" />
                            </Button>
                          )}
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
          folders={folders}
          currentFolder={currentFolder}
          onCurrentFolderChange={onCurrentFolderChange}
          user={user}
          capabilities={capabilities}
          writableMountIds={writableMountIds}
          canManageFolders={canManageFolders}
          isMobile={isMobile}
          groupByFolder={groupByFolder}
          iconSize={iconSize}
          hasActiveFilter={hasActiveFilter}
          onInspectFile={onInspectFile}
          onPreviewFile={onPreviewFile}
          onEditFile={onEditFile}
          onDownloadFile={onDownloadFile}
          onCreateFolder={(parent) => setCreateFolderModal({ parent, name: '' })}
          onMoveFile={(rec, target) => void handleMoveFile(rec, target)}
          onRenameFolder={(fp) => setRenameFolderModal({ oldPath: fp, newPath: fp })}
          onDeleteFolder={(fp) => setDeleteFolderModal({ path: fp, mode: 'move_to_parent' })}
          onRefresh={onRefresh}
        />
      )}

      {/* Create Folder Dialog */}
      <Dialog
        open={Boolean(createFolderModal)}
        onClose={() => setCreateFolderModal(null)}
        size="sm"
        title="新建文件夹"
        description={
          createFolderModal?.parent
            ? `将在 /${createFolderModal.parent} 下创建子目录（支持用 / 一次创建多级）`
            : '支持输入多级路径，例如：考研数学/线性代数'
        }
      >
        {createFolderModal && (
          <form onSubmit={submitCreateFolder} className="space-y-3 text-xs">
            <div className="space-y-1">
              <label className="font-medium">文件夹名称</label>
              <Input
                value={createFolderModal.name}
                onChange={(e) => setCreateFolderModal({ ...createFolderModal, name: e.target.value })}
                placeholder="例如：项目归档"
                autoFocus
              />
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-border">
              <Button type="button" size="sm" variant="outline" onClick={() => setCreateFolderModal(null)}>
                取消
              </Button>
              <Button type="submit" size="sm" disabled={folderBusy}>
                {folderBusy ? '创建中...' : '确定创建'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* Rename / Move Folder Dialog */}
      <Dialog
        open={Boolean(renameFolderModal)}
        onClose={() => setRenameFolderModal(null)}
        size="sm"
        title="重命名 / 移动文件夹"
        description="修改目录路径将自动同步更新其下所有文件与子目录归属"
      >
        {renameFolderModal && (
          <form onSubmit={submitRenameFolder} className="space-y-3 text-xs">
            <div className="space-y-1">
              <label className="font-medium">当前路径</label>
              <Input value={renameFolderModal.oldPath} disabled className="font-mono" />
            </div>
            <div className="space-y-1">
              <label className="font-medium">新路径</label>
              <Input
                value={renameFolderModal.newPath}
                onChange={(e) => setRenameFolderModal({ ...renameFolderModal, newPath: e.target.value })}
                className="font-mono"
                autoFocus
              />
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-border">
              <Button type="button" size="sm" variant="outline" onClick={() => setRenameFolderModal(null)}>
                取消
              </Button>
              <Button type="submit" size="sm" disabled={folderBusy}>
                {folderBusy ? '保存中...' : '确认重命名'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* Delete Folder Confirm Dialog */}
      <Dialog
        open={Boolean(deleteFolderModal)}
        onClose={() => setDeleteFolderModal(null)}
        size="sm"
        title={`删除文件夹 — /${deleteFolderModal?.path || ''}`}
        description="请选择如何处理该文件夹内的文件"
      >
        {deleteFolderModal && (
          <form onSubmit={submitDeleteFolder} className="space-y-3 text-xs">
            <div className="space-y-2">
              <label className="flex items-start gap-2 rounded-lg border border-border p-2.5 cursor-pointer hover:bg-muted/40">
                <input
                  type="radio"
                  name="del-folder-mode"
                  checked={deleteFolderModal.mode === 'move_to_parent'}
                  onChange={() => setDeleteFolderModal({ ...deleteFolderModal, mode: 'move_to_parent' })}
                  className="mt-0.5"
                />
                <div>
                  <div className="font-medium">仅移除文件夹（推荐）</div>
                  <div className="text-[11px] text-muted-foreground">
                    目录内文件自动移回上级目录或根目录，不会丢失任何数据。
                  </div>
                </div>
              </label>

              <label className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/5 p-2.5 cursor-pointer hover:bg-destructive/10">
                <input
                  type="radio"
                  name="del-folder-mode"
                  checked={deleteFolderModal.mode === 'delete_files'}
                  onChange={() => setDeleteFolderModal({ ...deleteFolderModal, mode: 'delete_files' })}
                  className="mt-0.5"
                />
                <div>
                  <div className="font-medium text-destructive">彻底删除文件夹及其中所有文件</div>
                  <div className="text-[11px] text-muted-foreground">
                    不可撤销！该目录及全部子目录内的文件都会被永久清除。
                  </div>
                </div>
              </label>
            </div>

            <div className="flex justify-end gap-2 pt-2 border-t border-border">
              <Button type="button" size="sm" variant="outline" onClick={() => setDeleteFolderModal(null)}>
                取消
              </Button>
              <Button
                type="submit"
                size="sm"
                variant={deleteFolderModal.mode === 'delete_files' ? 'destructive' : 'default'}
                disabled={folderBusy}
              >
                {folderBusy ? '处理中...' : '确认执行'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* Move File to Folder Dialog */}
      <Dialog
        open={Boolean(moveFileModal)}
        onClose={() => setMoveFileModal(null)}
        size="sm"
        title={`移动文件 — ${moveFileModal?.rec.name || ''}`}
        description="选择已有文件夹或输入新目录路径（留空表示移回根目录）"
      >
        {moveFileModal && (
          <form onSubmit={submitMoveFile} className="space-y-3 text-xs">
            <div className="space-y-1">
              <label className="font-medium">快速选择已有目录</label>
              <Select
                value={folders.includes(moveFileModal.folderPath) ? moveFileModal.folderPath : '__custom__'}
                onChange={(e) => {
                  if (e.target.value !== '__custom__') {
                    setMoveFileModal({ ...moveFileModal, folderPath: e.target.value })
                  }
                }}
                className="w-full text-xs"
              >
                <option value="">/（根目录）</option>
                {folders.map((fp) => (
                  <option key={fp} value={fp}>
                    /{fp}
                  </option>
                ))}
                {moveFileModal.folderPath !== '' && !folders.includes(moveFileModal.folderPath) && (
                  <option value="__custom__">自定义：/{moveFileModal.folderPath}</option>
                )}
              </Select>
            </div>

            <div className="space-y-1">
              <label className="font-medium">或直接输入目标路径</label>
              <Input
                value={moveFileModal.folderPath}
                onChange={(e) => setMoveFileModal({ ...moveFileModal, folderPath: e.target.value })}
                placeholder="留空表示根目录，多级用 / 分隔"
                className="font-mono"
              />
            </div>

            <div className="flex justify-end gap-2 pt-2 border-t border-border">
              <Button type="button" size="sm" variant="outline" onClick={() => setMoveFileModal(null)}>
                取消
              </Button>
              <Button type="submit" size="sm" disabled={folderBusy}>
                {folderBusy ? '移动中...' : '确认移动'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>
    </div>
  )
}
