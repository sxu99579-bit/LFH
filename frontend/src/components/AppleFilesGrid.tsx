import React, { useEffect, useMemo, useState } from 'react'
import {
  ChevronLeft,
  ChevronRight,
  Download,
  Edit3,
  Eye,
  Folder,
  Info,
  MoreHorizontal,
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
  user: User
  capabilities: ClusterCapabilityView[]
  writableMountIds?: Set<string>
  isMobile: boolean
  groupByFolder: boolean
  iconSize: AppleIconSize
  hasActiveFilter: boolean
  onInspectFile: (rec: FileRecord) => void
  onPreviewFile: (rec: FileRecord) => void
  onEditFile: (rec: FileRecord) => void
  onDownloadFile: (rec: FileRecord) => void
  onRefresh: () => void
}

function gridColumnClasses(size: AppleIconSize, isMobile: boolean): string {
  if (isMobile) {
    if (size === 'sm') return 'grid-cols-4 gap-x-2 gap-y-4'
    if (size === 'lg') return 'grid-cols-2 gap-x-4 gap-y-5'
    return 'grid-cols-3 gap-x-3 gap-y-5'
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
      return 'h-16 w-20'
    case 'lg':
      return 'h-24 w-28 sm:h-28 sm:w-32'
    default:
      return 'h-20 w-24 sm:h-22 sm:w-26'
  }
}

function folderSlotHeightClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'h-20'
    case 'lg':
      return 'h-32 sm:h-36'
    default:
      return 'h-24 sm:h-28'
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
  user,
  capabilities,
  writableMountIds,
  isMobile,
  groupByFolder,
  iconSize,
  hasActiveFilter,
  onInspectFile,
  onPreviewFile,
  onEditFile,
  onDownloadFile,
  onRefresh,
}: AppleFilesGridProps) {
  const { toast } = useToast()
  const [currentFolder, setCurrentFolder] = useState('')
  const [selectedId, setSelectedId] = useState('')
  const [contextMenu, setContextMenu] = useState<{
    rec: FileRecord
    x: number
    y: number
  } | null>(null)
  const [deletingId, setDeletingId] = useState('')

  const effectiveGrouping = groupByFolder && !hasActiveFilter

  const normalizeFolder = (raw?: string) =>
    String(raw || '')
      .replace(/\\/g, '/')
      .replace(/^\/+|\/+$/g, '')

  // Reset currentFolder if no files belong under it anymore (e.g. deleted or unmounted).
  useEffect(() => {
    if (!effectiveGrouping || !currentFolder) return
    const prefix = `${currentFolder}/`
    const stillExists = files.some((rec) => {
      const fp = normalizeFolder(rec.folder_path)
      return fp === currentFolder || fp.startsWith(prefix)
    })
    if (!stillExists) {
      setCurrentFolder('')
    }
  }, [currentFolder, effectiveGrouping, files])

  useEffect(() => {
    if (!effectiveGrouping) {
      setCurrentFolder('')
    }
  }, [effectiveGrouping])

  const { childFolders, visibleFiles } = useMemo(() => {
    if (!effectiveGrouping) {
      return { childFolders: [] as AppleFolderEntry[], visibleFiles: files }
    }
    const folderMap = new Map<string, AppleFolderEntry>()
    const direct: FileRecord[] = []
    const prefix = currentFolder ? `${currentFolder}/` : ''

    for (const rec of files) {
      const fp = normalizeFolder(rec.folder_path)
      if (!currentFolder) {
        if (!fp) {
          direct.push(rec)
          continue
        }
        const seg = fp.split('/')[0]
        const prev = folderMap.get(seg)
        if (prev) {
          prev.itemCount += 1
          prev.totalSize += rec.size || 0
        } else {
          folderMap.set(seg, {
            name: seg,
            fullPath: seg,
            itemCount: 1,
            totalSize: rec.size || 0,
          })
        }
      } else if (fp === currentFolder) {
        direct.push(rec)
      } else if (fp.startsWith(prefix)) {
        const rest = fp.slice(prefix.length)
        const seg = rest.split('/')[0]
        if (!seg) continue
        const fullPath = `${prefix}${seg}`
        const prev = folderMap.get(fullPath)
        if (prev) {
          prev.itemCount += 1
          prev.totalSize += rec.size || 0
        } else {
          folderMap.set(fullPath, {
            name: seg,
            fullPath,
            itemCount: 1,
            totalSize: rec.size || 0,
          })
        }
      }
    }

    const sortedFolders = Array.from(folderMap.values()).sort((a, b) =>
      a.name.localeCompare(b.name, 'zh-Hans-CN')
    )
    return { childFolders: sortedFolders, visibleFiles: direct }
  }, [currentFolder, effectiveGrouping, files])

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

  // Dismiss context menu on outside click or Escape
  useEffect(() => {
    if (!contextMenu) return
    const close = () => setContextMenu(null)
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
  }, [contextMenu])

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
    const menuW = 216
    const menuH = 260
    const x = Math.max(8, Math.min(e.clientX, window.innerWidth - menuW - 8))
    const y = Math.max(8, Math.min(e.clientY, window.innerHeight - menuH - 8))
    setContextMenu({ rec, x, y })
  }

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
      }}
    >
      {/* Finder Breadcrumb Strip when inside subfolder */}
      {effectiveGrouping && currentFolder !== '' && (
        <div
          className="flex flex-wrap items-center gap-1.5 rounded-xl border border-border/70 bg-card/60 px-3 py-2 text-xs backdrop-blur-xs"
          onClick={(e) => e.stopPropagation()}
        >
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => {
              const parts = currentFolder.split('/')
              parts.pop()
              setCurrentFolder(parts.join('/'))
              setSelectedId('')
            }}
          >
            <ChevronLeft className="h-3.5 w-3.5" />
            <span>返回上级</span>
          </Button>
          <span className="h-3.5 w-px bg-border mx-0.5" />
          <button
            type="button"
            onClick={() => {
              setCurrentFolder('')
              setSelectedId('')
            }}
            className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer"
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
                  onClick={() => {
                    setCurrentFolder(bc.path)
                    setSelectedId('')
                  }}
                  className={cn(
                    'rounded px-1.5 py-0.5 cursor-pointer truncate max-w-[180px]',
                    isLast
                      ? 'font-semibold text-foreground'
                      : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                  )}
                >
                  {bc.label}
                </button>
              </React.Fragment>
            )
          })}
          <span className="ml-auto text-[11px] text-muted-foreground tabular-nums">
            {childFolders.length + visibleFiles.length} 项
          </span>
        </div>
      )}

      {/* Borderless Apple Files Icon Canvas */}
      <div className={cn('grid items-start', gridColumnClasses(iconSize, isMobile))}>
        {/* Virtual / Directory Folders */}
        {childFolders.map((folder) => (
          <button
            key={`folder:${folder.fullPath}`}
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              setCurrentFolder(folder.fullPath)
              setSelectedId('')
              setContextMenu(null)
            }}
            className="group flex flex-col items-center rounded-2xl px-2 py-2.5 text-center transition-colors hover:bg-accent/45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF] cursor-pointer"
            title={`${folder.fullPath} (${folder.itemCount} 项 · ${formatSize(folder.totalSize)})`}
          >
            <div className={cn('flex w-full items-end justify-center', folderSlotHeightClasses(iconSize))}>
              <AppleFolderIcon className={folderSvgSizeClasses(iconSize)} />
            </div>
            <div className="mt-2 w-full space-y-0.5">
              <div className="line-clamp-2 break-words text-[12px] sm:text-[13px] font-medium leading-[1.3] text-foreground px-1">
                {middleTruncateFileName(folder.name, maxChars)}
              </div>
              <div className="text-[11px] text-muted-foreground tabular-nums">
                {folder.itemCount} 项
              </div>
            </div>
          </button>
        ))}

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
              onClick={(e) => {
                e.stopPropagation()
                setContextMenu(null)
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
                'group relative flex flex-col items-center rounded-2xl px-2 py-2.5 text-center transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF] cursor-pointer',
                isSelected ? 'bg-[#007AFF]/8 dark:bg-[#007AFF]/15' : 'hover:bg-accent/45',
                deletingId === rec.id && 'opacity-50 pointer-events-none'
              )}
              title={`${rec.name}\n${rec.folder_path ? `/${rec.folder_path} · ` : ''}${formatSize(rec.size)} · v${rec.version || 1} · ${rec.node_name || '本机'}`}
            >
              {/* Thumbnail Slot + Hover Frosted Glass Action Pill */}
              <div className="relative flex w-full items-end justify-center">
                <div
                  className={cn(
                    'rounded-[8px] p-1 transition-all',
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
                    className="absolute right-0 bottom-0 flex h-6 w-6 items-center justify-center rounded-full border border-border/80 bg-card/90 text-muted-foreground shadow-xs backdrop-blur-xs"
                  >
                    <MoreHorizontal className="h-3.5 w-3.5" />
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
                      'inline-block max-w-full break-words rounded-[5px] px-1.5 py-[1px] text-[12px] sm:text-[13px] font-medium leading-[1.3] transition-colors',
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
    </div>
  )
}
