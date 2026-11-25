import React, { useEffect, useMemo, useState } from 'react'
import {
  ChevronLeft,
  ChevronRight,
  Download,
  Edit3,
  Eye,
  Folder,
  FolderInput,
  FolderOpen,
  FolderPlus,
  Info,
  MoreHorizontal,
  Pencil,
  Trash2,
} from 'lucide-react'
import { api } from '@/lib/api'
import {
  AppleFileThumbnail,
  AppleFolderIcon,
  type AppleIconSize,
} from '@/components/AppleFileIcons'
import { Button, useToast } from '@/components/ui'
import {
  canPreviewFile,
  cn,
  formatAppleDate,
  formatSize,
  getMatchingCapabilitiesForFile,
  isEditableTextFile,
  middleTruncateFileName,
} from '@/lib/utils'
import type { ClusterCapabilityView, FileRecord, User } from '@/types'

interface AppleFolderEntry {
  name: string
  fullPath: string
  itemCount: number
  totalSize: number
}

interface AppleFilesGridProps {
  files: FileRecord[]
  folders: string[]
  currentFolder: string
  onCurrentFolderChange: (folder: string) => void
  user: User
  capabilities: ClusterCapabilityView[]
  writableMountIds?: Set<string>
  canManageFolders?: boolean
  isMobile: boolean
  groupByFolder: boolean
  iconSize: AppleIconSize
  hasActiveFilter: boolean
  onInspectFile: (rec: FileRecord) => void
  onPreviewFile: (rec: FileRecord) => void
  onEditFile: (rec: FileRecord) => void
  onDownloadFile: (rec: FileRecord) => void
  onCreateFolder: (parentFolder: string) => void
  onMoveFile: (rec: FileRecord, targetFolder?: string) => void
  onRenameFolder: (folderPath: string) => void
  onDeleteFolder: (folderPath: string) => void
  onRefresh: () => void
}

function gridColumnClasses(size: AppleIconSize, isMobile: boolean): string {
  if (isMobile) {
    if (size === 'sm') return 'grid-cols-4 gap-x-1.5 gap-y-3.5'
    if (size === 'lg') return 'grid-cols-2 gap-x-3 gap-y-5'
    return 'grid-cols-3 gap-x-2 gap-y-4'
  }
  switch (size) {
    case 'sm':
      return 'grid-cols-3 sm:grid-cols-5 md:grid-cols-6 lg:grid-cols-7 xl:grid-cols-8 gap-x-3 gap-y-6'
    case 'lg':
      return 'grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-x-5 gap-y-7'
    default:
      return 'grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-7 gap-x-4 gap-y-6'
  }
}

function folderSvgSizeClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'w-[66px] h-[54px] shrink-0'
    case 'lg':
      return 'w-[102px] h-[84px] sm:w-[114px] sm:h-[94px] shrink-0'
    default:
      return 'w-[82px] h-[68px] sm:w-[92px] sm:h-[76px] shrink-0'
  }
}

function folderSlotHeightClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'h-[80px]'
    case 'lg':
      return 'h-[126px] sm:h-[144px]'
    default:
      return 'h-[100px] sm:h-[112px]'
  }
}

function maxNameCharsForSize(size: AppleIconSize, isMobile: boolean): number {
  if (isMobile) return size === 'lg' ? 26 : 18
  if (size === 'sm') return 18
  if (size === 'lg') return 30
  return 24
}

function isEditableElementFocused(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

export function AppleFilesGrid({
  files,
  folders,
  currentFolder,
  onCurrentFolderChange,
  user,
  capabilities,
  writableMountIds,
  canManageFolders = true,
  isMobile,
  groupByFolder,
  iconSize,
  hasActiveFilter,
  onInspectFile,
  onPreviewFile,
  onEditFile,
  onDownloadFile,
  onCreateFolder,
  onMoveFile,
  onRenameFolder,
  onDeleteFolder,
  onRefresh,
}: AppleFilesGridProps) {
  const { toast } = useToast()
  const [selectedId, setSelectedId] = useState('')
  const [dropTargetFolder, setDropTargetFolder] = useState<string | null>(null)
  const [contextMenu, setContextMenu] = useState<{
    rec: FileRecord
    x: number
    y: number
  } | null>(null)
  const [folderMenu, setFolderMenu] = useState<{
    folder: AppleFolderEntry
    x: number
    y: number
  } | null>(null)
  const [deletingId, setDeletingId] = useState('')

  const effectiveGrouping = groupByFolder && !hasActiveFilter

  const normalizeFolder = (raw?: string) =>
    String(raw || '')
      .replace(/\\/g, '/')
      .replace(/^\/+|\/+$/g, '')

  // Reset currentFolder if no files and no explicit folders match it anymore.
  useEffect(() => {
    if (!effectiveGrouping || !currentFolder) return
    const prefix = `${currentFolder}/`
    const fileMatch = files.some((rec) => {
      const fp = normalizeFolder(rec.folder_path)
      return fp === currentFolder || fp.startsWith(prefix)
    })
    const folderMatch = folders.some((raw) => {
      const fp = normalizeFolder(raw)
      return fp === currentFolder || fp.startsWith(prefix)
    })
    if (!fileMatch && !folderMatch) {
      onCurrentFolderChange('')
    }
  }, [currentFolder, effectiveGrouping, files, folders, onCurrentFolderChange])

  useEffect(() => {
    if (!effectiveGrouping && currentFolder) {
      onCurrentFolderChange('')
    }
  }, [currentFolder, effectiveGrouping, onCurrentFolderChange])

  const { childFolders, visibleFiles } = useMemo(() => {
    if (!effectiveGrouping) {
      return { childFolders: [] as AppleFolderEntry[], visibleFiles: files }
    }
    const folderMap = new Map<string, AppleFolderEntry>()
    const direct: FileRecord[] = []
    const prefix = currentFolder ? `${currentFolder}/` : ''

    const ensureChildFolder = (seg: string, fullPath: string) => {
      if (!seg) return null
      let entry = folderMap.get(fullPath)
      if (!entry) {
        entry = { name: seg, fullPath, itemCount: 0, totalSize: 0 }
        folderMap.set(fullPath, entry)
      }
      return entry
    }

    // Seed explicit folders (including empty ones)
    for (const raw of folders) {
      const fp = normalizeFolder(raw)
      if (!fp) continue
      if (!currentFolder) {
        const seg = fp.split('/')[0]
        ensureChildFolder(seg, seg)
      } else if (fp.startsWith(prefix)) {
        const rest = fp.slice(prefix.length)
        const seg = rest.split('/')[0]
        ensureChildFolder(seg, `${prefix}${seg}`)
      }
    }

    // Aggregate files into direct entries or child folder stats
    for (const rec of files) {
      const fp = normalizeFolder(rec.folder_path)
      if (!currentFolder) {
        if (!fp) {
          direct.push(rec)
          continue
        }
        const seg = fp.split('/')[0]
        const entry = ensureChildFolder(seg, seg)
        if (entry) {
          entry.itemCount += 1
          entry.totalSize += rec.size || 0
        }
      } else if (fp === currentFolder) {
        direct.push(rec)
      } else if (fp.startsWith(prefix)) {
        const rest = fp.slice(prefix.length)
        const seg = rest.split('/')[0]
        const entry = ensureChildFolder(seg, `${prefix}${seg}`)
        if (entry) {
          entry.itemCount += 1
          entry.totalSize += rec.size || 0
        }
      }
    }

    const sortedFolders = Array.from(folderMap.values()).sort((a, b) =>
      a.name.localeCompare(b.name, 'zh-Hans-CN')
    )
    return { childFolders: sortedFolders, visibleFiles: direct }
  }, [currentFolder, effectiveGrouping, files, folders])

  const selectedFile = useMemo(
    () => visibleFiles.find((f) => f.id === selectedId) || null,
    [selectedId, visibleFiles]
  )

  // Keyboard Space -> QuickLook preview on desktop
  useEffect(() => {
    if (isMobile) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code !== 'Space' || !selectedFile) return
      if (isEditableElementFocused(e.target)) return
      // Avoid hijacking Space if a modal/dialog overlay is already open.
      if (document.querySelector('[class*="z-[70]"], [class*="z-[65]"]')) return
      e.preventDefault()
      setContextMenu(null)
      if (canPreviewFile(selectedFile)) {
        onPreviewFile(selectedFile)
      } else {
        onInspectFile(selectedFile)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [isMobile, onInspectFile, onPreviewFile, selectedFile])

  // Dismiss context menus on outside click or Escape
  useEffect(() => {
    if (!contextMenu && !folderMenu) return
    const close = () => {
      setContextMenu(null)
      setFolderMenu(null)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    window.addEventListener('click', close)
    window.addEventListener('scroll', close, true)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('keydown', onKey)
    }
  }, [contextMenu, folderMenu])

  const breadcrumbs = useMemo(() => {
    if (!currentFolder) return [] as Array<{ label: string; path: string }>
    const parts = currentFolder.split('/')
    return parts.map((seg, i) => ({
      label: seg,
      path: parts.slice(0, i + 1).join('/'),
    }))
  }, [currentFolder])

  const openContextMenuForFile = (e: React.MouseEvent, rec: FileRecord) => {
    e.preventDefault()
    e.stopPropagation()
    setSelectedId(rec.id)
    setFolderMenu(null)
    const menuW = 224
    const menuH = 300
    const x = Math.max(8, Math.min(e.clientX, window.innerWidth - menuW - 8))
    const y = Math.max(8, Math.min(e.clientY, window.innerHeight - menuH - 8))
    setContextMenu({ rec, x, y })
  }

  const openContextMenuForFolder = (e: React.MouseEvent, folder: AppleFolderEntry) => {
    e.preventDefault()
    e.stopPropagation()
    setContextMenu(null)
    const menuW = 216
    const menuH = 220
    const x = Math.max(8, Math.min(e.clientX, window.innerWidth - menuW - 8))
    const y = Math.max(8, Math.min(e.clientY, window.innerHeight - menuH - 8))
    setFolderMenu({ folder, x, y })
  }

  const folderDragProps = (targetPath: string) => ({
    onDragOver: (e: React.DragEvent) => {
      if (!e.dataTransfer.types.includes('text/lfh-file-id')) return
      e.preventDefault()
      e.stopPropagation()
      e.dataTransfer.dropEffect = 'move'
      if (dropTargetFolder !== targetPath) setDropTargetFolder(targetPath)
    },
    onDragLeave: (e: React.DragEvent) => {
      if (!e.dataTransfer.types.includes('text/lfh-file-id')) return
      e.stopPropagation()
      if (dropTargetFolder === targetPath) setDropTargetFolder(null)
    },
    onDrop: (e: React.DragEvent) => {
      if (!e.dataTransfer.types.includes('text/lfh-file-id')) return
      e.preventDefault()
      e.stopPropagation()
      setDropTargetFolder(null)
      const fileId = e.dataTransfer.getData('text/lfh-file-id')
      const dragged = files.find((f) => f.id === fileId)
      if (dragged) onMoveFile(dragged, targetPath)
    },
  })

  const handleDeleteFromMenu = async (rec: FileRecord) => {
    setContextMenu(null)
    if (!window.confirm(`确定删除文件「${rec.name}」？此操作不可撤销。`)) return
    setDeletingId(rec.id)
    try {
      await api.deleteFile(rec.id)
      toast(`已删除「${rec.name}」`, 'success')
      if (selectedId === rec.id) setSelectedId('')
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除文件失败', 'error')
    } finally {
      setDeletingId('')
    }
  }

  const maxChars = maxNameCharsForSize(iconSize, isMobile)

  return (
    <div
      className="space-y-4"
      onClick={() => {
        setSelectedId('')
        setContextMenu(null)
        setFolderMenu(null)
      }}
    >
      {/* Finder Breadcrumb Strip + Folder Management Actions when inside subfolder */}
      {effectiveGrouping && currentFolder !== '' && (
        <div
          className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border/70 bg-card/60 px-3 py-2 text-xs backdrop-blur-xs"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex flex-wrap items-center gap-1 min-w-0">
            <Button
              type="button"
              size="xs"
              variant="ghost"
              onClick={() => {
                const parts = currentFolder.split('/')
                parts.pop()
                onCurrentFolderChange(parts.join('/'))
                setSelectedId('')
              }}
            >
              <ChevronLeft className="h-3.5 w-3.5" />
              <span>返回上级</span>
            </Button>
            <span className="h-3.5 w-px bg-border mx-0.5" />
            <button
              type="button"
              {...folderDragProps('')}
              onClick={() => {
                onCurrentFolderChange('')
                setSelectedId('')
              }}
              className={cn(
                'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer transition-colors',
                dropTargetFolder === '' && 'ring-2 ring-[#007AFF] bg-[#007AFF]/10 text-foreground'
              )}
              title="回到根目录（支持将文件拖放到此）"
            >
              <Folder className="h-3 w-3 text-[#4aa1ec]" />
              <span>全部文件</span>
            </button>
            {breadcrumbs.map((bc, idx) => {
              const isLast = idx === breadcrumbs.length - 1
              return (
                <React.Fragment key={bc.path}>
                  <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                  <button
                    type="button"
                    {...folderDragProps(bc.path)}
                    onClick={() => {
                      onCurrentFolderChange(bc.path)
                      setSelectedId('')
                    }}
                    className={cn(
                      'rounded px-1.5 py-0.5 cursor-pointer truncate max-w-[160px] transition-colors',
                      isLast
                        ? 'font-semibold text-foreground'
                        : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                      dropTargetFolder === bc.path && 'ring-2 ring-[#007AFF] bg-[#007AFF]/10 text-foreground'
                    )}
                  >
                    {bc.label}
                  </button>
                </React.Fragment>
              )
            })}
          </div>

          <div className="flex flex-wrap items-center gap-1 shrink-0">
            <span className="text-[11px] text-muted-foreground tabular-nums mr-1">
              {childFolders.length + visibleFiles.length} 项
            </span>
            {canManageFolders && (
              <>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => onCreateFolder(currentFolder)}
                  title="在当前目录下新建子文件夹"
                >
                  <FolderPlus className="h-3 w-3 text-[#4aa1ec]" />
                  <span>新建子目录</span>
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  onClick={() => onRenameFolder(currentFolder)}
                  title="重命名或移动当前文件夹"
                >
                  <Pencil className="h-3 w-3" />
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  className="text-destructive hover:text-destructive"
                  onClick={() => onDeleteFolder(currentFolder)}
                  title="删除当前文件夹"
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </>
            )}
          </div>
        </div>
      )}

      {/* Empty Folder State when inside an explicit empty directory */}
      {effectiveGrouping && childFolders.length === 0 && visibleFiles.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-border bg-card/40 p-8 text-center text-xs text-muted-foreground space-y-2">
          <div className="text-sm font-medium text-foreground">
            {currentFolder ? `目录「${currentFolder}」暂无文件` : '暂无文件'}
          </div>
          <div>可直接将文件拖拽到此处上传，或点击上方「上传文件 / 新建 TXT / 新建文件夹」。</div>
        </div>
      ) : (
        /* Borderless Apple Files Icon Canvas */
        <div className={cn('grid items-start', gridColumnClasses(iconSize, isMobile))}>
          {/* Virtual / Directory Folders */}
          {childFolders.map((folder) => {
            const isDropOver = dropTargetFolder === folder.fullPath
            return (
              <div
                key={`folder:${folder.fullPath}`}
                role="button"
                tabIndex={0}
                {...folderDragProps(folder.fullPath)}
                onClick={(e) => {
                  e.stopPropagation()
                  onCurrentFolderChange(folder.fullPath)
                  setSelectedId('')
                  setContextMenu(null)
                  setFolderMenu(null)
                }}
                onContextMenu={(e) => openContextMenuForFolder(e, folder)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    onCurrentFolderChange(folder.fullPath)
                  }
                }}
                className={cn(
                  'group relative min-w-0 w-full flex flex-col items-center rounded-2xl px-1 py-1.5 sm:px-2 sm:py-2.5 text-center transition-colors hover:bg-accent/45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF] cursor-pointer',
                  isDropOver && 'ring-2 ring-[#007AFF] bg-[#007AFF]/12'
                )}
                title={`${folder.fullPath} (${folder.itemCount} 项 · ${formatSize(folder.totalSize)}) · 右键可重命名/删除`}
              >
                <div className={cn('relative flex w-full items-end justify-center', folderSlotHeightClasses(iconSize))}>
                  <AppleFolderIcon className={folderSvgSizeClasses(iconSize)} />
                  <button
                    type="button"
                    aria-label="文件夹操作"
                    onClick={(e) => {
                      e.stopPropagation()
                      openContextMenuForFolder(e, folder)
                    }}
                    className={cn(
                      'absolute right-0.5 bottom-0.5 flex h-5.5 w-5.5 items-center justify-center rounded-full border border-border/80 bg-card/90 text-muted-foreground shadow-xs backdrop-blur-xs transition-opacity',
                      isMobile ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
                    )}
                    title="文件夹菜单"
                  >
                    <MoreHorizontal className="h-3 w-3" />
                  </button>
                </div>
                <div className="mt-2 w-full space-y-0.5">
                  <div className="line-clamp-2 break-all sm:break-words text-[12px] sm:text-[13px] font-medium leading-[1.3] text-foreground px-0.5">
                    {middleTruncateFileName(folder.name, maxChars)}
                  </div>
                  <div className="text-[11px] text-muted-foreground tabular-nums">
                    {folder.itemCount} 项
                  </div>
                </div>
              </div>
            )
          })}

          {/* Individual Files */}
          {visibleFiles.map((rec) => {
            const isSelected = selectedId === rec.id
            const previewable = canPreviewFile(rec)
            const editable = isEditableTextFile(rec, writableMountIds)
            const displayName = middleTruncateFileName(rec.name, maxChars)

            return (
              <div
                key={rec.id}
                role="button"
                tabIndex={0}
                draggable={!isMobile}
                onDragStart={(e) => {
                  e.dataTransfer.setData('text/lfh-file-id', rec.id)
                  e.dataTransfer.effectAllowed = 'move'
                }}
                onDragEnd={() => setDropTargetFolder(null)}
                onClick={(e) => {
                  e.stopPropagation()
                  setContextMenu(null)
                  setFolderMenu(null)
                  if (isMobile) {
                    if (previewable) onPreviewFile(rec)
                    else onInspectFile(rec)
                    return
                  }
                  setSelectedId(rec.id)
                }}
              onDoubleClick={(e) => {
                e.stopPropagation()
                setContextMenu(null)
                if (previewable) onPreviewFile(rec)
                else onInspectFile(rec)
              }}
              onContextMenu={(e) => openContextMenuForFile(e, rec)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  if (previewable) onPreviewFile(rec)
                  else onInspectFile(rec)
                }
              }}
              className={cn(
                'group relative min-w-0 w-full flex flex-col items-center rounded-2xl px-1 py-1.5 sm:px-2 sm:py-2.5 text-center transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF] cursor-pointer',
                isSelected ? 'bg-[#007AFF]/8 dark:bg-[#007AFF]/15' : 'hover:bg-accent/45',
                deletingId === rec.id && 'opacity-50 pointer-events-none'
              )}
              title={`${rec.name}\n${rec.folder_path ? `/${rec.folder_path} · ` : ''}${formatSize(rec.size)} · v${rec.version || 1} · ${rec.node_name || '本机'}`}
            >
              {/* Thumbnail Slot + Hover Frosted Glass Action Pill */}
              <div className="relative flex w-full items-end justify-center">
                <div
                  className={cn(
                    'w-full flex items-end justify-center rounded-[10px] p-1 transition-all',
                    isSelected && 'bg-black/6 dark:bg-white/10'
                  )}
                >
                  <AppleFileThumbnail rec={rec} size={iconSize} />
                </div>

                {/* Mobile quick menu trigger */}
                {isMobile && (
                  <button
                    type="button"
                    aria-label="更多操作"
                    onClick={(e) => {
                      e.stopPropagation()
                      onInspectFile(rec)
                    }}
                    className="absolute right-0.5 bottom-0.5 flex h-5.5 w-5.5 items-center justify-center rounded-full border border-border/80 bg-card/90 text-muted-foreground shadow-xs backdrop-blur-xs"
                  >
                    <MoreHorizontal className="h-3 w-3" />
                  </button>
                )}

                {/* Desktop hover frosted pill */}
                {!isMobile && (
                  <div
                    onClick={(e) => e.stopPropagation()}
                    className="pointer-events-none absolute -bottom-1 left-1/2 flex -translate-x-1/2 items-center gap-0.5 rounded-full border border-border/80 bg-card/90 px-1.5 py-0.5 opacity-0 shadow-lg backdrop-blur-md transition-opacity duration-150 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    {previewable && (
                      <button
                        type="button"
                        title="快速预览 (Space)"
                        onClick={() => onPreviewFile(rec)}
                        className="flex h-6 w-6 items-center justify-center rounded-full text-foreground/80 hover:bg-accent hover:text-foreground cursor-pointer"
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </button>
                    )}
                    {editable && (
                      <button
                        type="button"
                        title="在线编辑"
                        onClick={() => onEditFile(rec)}
                        className="flex h-6 w-6 items-center justify-center rounded-full text-foreground/80 hover:bg-accent hover:text-foreground cursor-pointer"
                      >
                        <Edit3 className="h-3.5 w-3.5" />
                      </button>
                    )}
                    <button
                      type="button"
                      title="下载"
                      onClick={() => onDownloadFile(rec)}
                      className="flex h-6 w-6 items-center justify-center rounded-full text-foreground/80 hover:bg-accent hover:text-foreground cursor-pointer"
                    >
                      <Download className="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      title={`详情 / 版本 v${rec.version || 1}`}
                      onClick={() => onInspectFile(rec)}
                      className="flex h-6 w-6 items-center justify-center rounded-full text-foreground/80 hover:bg-accent hover:text-foreground cursor-pointer"
                    >
                      <Info className="h-3.5 w-3.5" />
                    </button>
                  </div>
                )}
              </div>

              {/* Minimalist 3-Line Apple Typography Below Thumbnail */}
              <div className="mt-2 w-full space-y-0.5 px-0.5">
                <div className="flex justify-center">
                  <span
                    className={cn(
                      'inline-block max-w-full line-clamp-2 break-all sm:break-words rounded-[5px] px-1.5 py-[1px] text-[12px] sm:text-[13px] font-medium leading-[1.3] transition-colors',
                      isSelected
                        ? 'bg-[#007AFF] text-white shadow-xs'
                        : 'text-foreground'
                    )}
                  >
                    {displayName}
                  </span>
                </div>
                {!effectiveGrouping && rec.folder_path && (
                  <div className="truncate text-[10px] text-muted-foreground/80">
                    /{normalizeFolder(rec.folder_path)}
                  </div>
                )}
                <div className="text-[11px] text-muted-foreground tabular-nums leading-tight">
                  {formatAppleDate(rec.uploaded_at)}
                </div>
                <div className="text-[11px] text-muted-foreground/90 tabular-nums leading-tight">
                  {formatSize(rec.size)}
                </div>
              </div>
            </div>
          )
        })}
      </div>
      )}

      {/* Desktop Apple-Style Frosted Right-Click Context Menu */}
      {contextMenu && (
        <div
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onClick={(e) => e.stopPropagation()}
          className="fixed z-[80] w-54 rounded-xl border border-border/80 bg-card/95 p-1.5 text-xs shadow-2xl backdrop-blur-xl"
        >
          <div className="px-2.5 py-1.5 border-b border-border/60 mb-1">
            <div className="font-semibold truncate text-foreground">{contextMenu.rec.name}</div>
            <div className="text-[10px] text-muted-foreground truncate">
              v{contextMenu.rec.version || 1} · {contextMenu.rec.node_name || '本机'} ·{' '}
              {formatSize(contextMenu.rec.size)}
            </div>
          </div>

          {canPreviewFile(contextMenu.rec) && (
            <button
              type="button"
              onClick={() => {
                const target = contextMenu.rec
                setContextMenu(null)
                onPreviewFile(target)
              }}
              className="flex w-full items-center justify-between rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
            >
              <span className="inline-flex items-center gap-2">
                <Eye className="h-3.5 w-3.5" />
                <span>快速预览</span>
              </span>
              <span className="text-[10px] opacity-70">Space</span>
            </button>
          )}

          {isEditableTextFile(contextMenu.rec, writableMountIds) && (
            <button
              type="button"
              onClick={() => {
                const target = contextMenu.rec
                setContextMenu(null)
                onEditFile(target)
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
            >
              <Edit3 className="h-3.5 w-3.5" />
              <span>在线文本编辑</span>
            </button>
          )}

          <button
            type="button"
            onClick={() => {
              const target = contextMenu.rec
              setContextMenu(null)
              onDownloadFile(target)
            }}
            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
          >
            <Download className="h-3.5 w-3.5" />
            <span>下载文件</span>
          </button>

          <button
            type="button"
            onClick={() => {
              const target = contextMenu.rec
              setContextMenu(null)
              onInspectFile(target)
            }}
            className="flex w-full items-center justify-between rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
          >
            <span className="inline-flex items-center gap-2">
              <Info className="h-3.5 w-3.5" />
              <span>简介、版本与插件</span>
            </span>
            {getMatchingCapabilitiesForFile(capabilities, contextMenu.rec.name).length > 0 && (
              <span className="rounded bg-primary/15 px-1 text-[10px] text-primary">
                可调
              </span>
            )}
          </button>

          {(user.role === 'admin' || contextMenu.rec.uploader === user.username) && !contextMenu.rec.mount_id && (
            <button
              type="button"
              onClick={() => {
                const target = contextMenu.rec
                setContextMenu(null)
                onMoveFile(target)
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
            >
              <FolderInput className="h-3.5 w-3.5" />
              <span>移动到文件夹...</span>
            </button>
          )}

          {(user.role === 'admin' || contextMenu.rec.uploader === user.username) && (
            <>
              <div className="my-1 border-t border-border/60" />
              <button
                type="button"
                onClick={() => void handleDeleteFromMenu(contextMenu.rec)}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-destructive hover:bg-destructive hover:text-destructive-foreground cursor-pointer"
              >
                <Trash2 className="h-3.5 w-3.5" />
                <span>删除文件</span>
              </button>
            </>
          )}
        </div>
      )}

      {/* Folder Context Menu */}
      {folderMenu && (
        <div
          style={{ left: folderMenu.x, top: folderMenu.y }}
          onClick={(e) => e.stopPropagation()}
          className="fixed z-[80] w-52 rounded-xl border border-border/80 bg-card/95 p-1.5 text-xs shadow-2xl backdrop-blur-xl"
        >
          <div className="px-2.5 py-1.5 border-b border-border/60 mb-1">
            <div className="font-semibold truncate text-foreground">{folderMenu.folder.name}</div>
            <div className="text-[10px] text-muted-foreground truncate">
              /{folderMenu.folder.fullPath} · {folderMenu.folder.itemCount} 项
            </div>
          </div>

          <button
            type="button"
            onClick={() => {
              const target = folderMenu.folder.fullPath
              setFolderMenu(null)
              onCurrentFolderChange(target)
            }}
            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
          >
            <FolderOpen className="h-3.5 w-3.5" />
            <span>打开文件夹</span>
          </button>

          {canManageFolders && (
            <>
              <button
                type="button"
                onClick={() => {
                  const target = folderMenu.folder.fullPath
                  setFolderMenu(null)
                  onCreateFolder(target)
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
              >
                <FolderPlus className="h-3.5 w-3.5" />
                <span>新建子文件夹</span>
              </button>

              <button
                type="button"
                onClick={() => {
                  const target = folderMenu.folder.fullPath
                  setFolderMenu(null)
                  onRenameFolder(target)
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[#007AFF] hover:text-white cursor-pointer"
              >
                <Pencil className="h-3.5 w-3.5" />
                <span>重命名 / 移动目录</span>
              </button>

              <div className="my-1 border-t border-border/60" />
              <button
                type="button"
                onClick={() => {
                  const target = folderMenu.folder.fullPath
                  setFolderMenu(null)
                  onDeleteFolder(target)
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-destructive hover:bg-destructive hover:text-destructive-foreground cursor-pointer"
              >
                <Trash2 className="h-3.5 w-3.5" />
                <span>删除文件夹...</span>
              </button>
            </>
          )}
        </div>
      )}
    </div>
  )
}
