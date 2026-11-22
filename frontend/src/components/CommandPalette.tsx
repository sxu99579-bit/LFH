import { useEffect, useMemo, useState } from 'react'
import {
  Search,
  Files,
  Clipboard,
  Cpu,
  Activity,
  Settings,
  FilePlus2,
  Gauge,
  FileText,
  X,
} from 'lucide-react'
import type { FileRecord, NavTab } from '@/types'
import { formatSize } from '@/lib/utils'

export function CommandPalette({
  open,
  onClose,
  files,
  isAdmin,
  onNavigate,
  onSelectFile,
  onCreateTxt,
  onProbeBandwidth,
}: {
  open: boolean
  onClose: () => void
  files: FileRecord[]
  isAdmin: boolean
  onNavigate: (tab: NavTab) => void
  onSelectFile: (rec: FileRecord) => void
  onCreateTxt: () => void
  onProbeBandwidth: () => void
}) {
  const [query, setQuery] = useState('')

  useEffect(() => {
    if (!open) setQuery('')
  }, [open])

  const matchedFiles = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return files.slice(0, 6)
    return files
      .filter(
        (f) =>
          f.name.toLowerCase().includes(q) ||
          (f.folder_path || '').toLowerCase().includes(q) ||
          (f.uploader || '').toLowerCase().includes(q)
      )
      .slice(0, 8)
  }, [files, query])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[80] flex items-start justify-center pt-[12vh] px-4">
      <div className="fixed inset-0 bg-black/55 backdrop-blur-xs" onClick={onClose} />
      <div className="relative z-10 w-full max-w-xl overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border px-3.5 py-2.5">
          <Search className="h-4 w-4 text-muted-foreground shrink-0" />
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索文件名、目录、上传者，或选择快捷操作..."
            className="w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground cursor-pointer"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-[60vh] overflow-y-auto p-2 space-y-3">
          <div>
            <div className="px-2 py-1 text-[11px] font-medium text-muted-foreground">快捷导航与动作</div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-1">
              {[
                { label: '打开文件中心', icon: Files, fn: () => onNavigate('files') },
                { label: '打开超级剪贴板', icon: Clipboard, fn: () => onNavigate('clipboard') },
                ...(isAdmin ? [{ label: '打开算力与插件市场', icon: Cpu, fn: () => onNavigate('plugins') }] : []),
                { label: '节点健康与内网带宽', icon: Activity, fn: () => onNavigate('health') },
                { label: '系统设置与版本控制', icon: Settings, fn: () => onNavigate('settings') },
                { label: '新建 TXT 文本文件', icon: FilePlus2, fn: onCreateTxt },
                ...(isAdmin ? [{ label: '探测集群内网带宽', icon: Gauge, fn: onProbeBandwidth }] : []),
              ].map((item) => {
                const Icon = item.icon
                return (
                  <button
                    key={item.label}
                    type="button"
                    onClick={() => {
                      item.fn()
                      onClose()
                    }}
                    className="flex items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs hover:bg-accent cursor-pointer"
                  >
                    <Icon className="h-3.5 w-3.5 text-primary shrink-0" />
                    <span>{item.label}</span>
                  </button>
                )
              })}
            </div>
          </div>

          {matchedFiles.length > 0 && (
            <div>
              <div className="px-2 py-1 text-[11px] font-medium text-muted-foreground">匹配文件</div>
              <div className="space-y-0.5">
                {matchedFiles.map((rec) => (
                  <button
                    key={rec.id}
                    type="button"
                    onClick={() => {
                      onSelectFile(rec)
                      onClose()
                    }}
                    className="flex w-full items-center justify-between gap-3 rounded-md px-2.5 py-2 text-left text-xs hover:bg-accent cursor-pointer"
                  >
                    <span className="flex items-center gap-2 min-w-0">
                      <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                      <span className="truncate font-medium">{rec.name}</span>
                      {rec.folder_path && (
                        <span className="truncate text-[11px] text-muted-foreground">/{rec.folder_path}</span>
                      )}
                    </span>
                    <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                      {formatSize(rec.size)} · {rec.node_name || '本机'}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
