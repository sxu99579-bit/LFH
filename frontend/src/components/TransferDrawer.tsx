import { useState } from 'react'
import { ArrowDownToLine, ArrowUpFromLine, Pause, Play, X, Trash2 } from 'lucide-react'
import type { TransferTask } from '@/types'
import { formatSize } from '@/lib/utils'
import { Badge, Button } from '@/components/ui'

export function TransferDrawer({
  tasks,
  open,
  onClose,
  onPause,
  onResume,
  onCancel,
  onClearDone,
}: {
  tasks: TransferTask[]
  open: boolean
  onClose: () => void
  onPause: (id: string) => void
  onResume: (id: string) => void
  onCancel: (id: string) => void
  onClearDone: () => void
}) {
  const [tab, setTab] = useState<'all' | 'upload' | 'download' | 'done'>('all')

  if (!open) return null

  const filtered = tasks.filter((t) => {
    if (tab === 'upload') return t.kind === 'upload' && t.status !== 'done'
    if (tab === 'download') return t.kind === 'download' && t.status !== 'done'
    if (tab === 'done') return t.status === 'done'
    return true
  })

  return (
    <div className="fixed bottom-16 right-3 z-[60] w-[calc(100vw-24px)] sm:bottom-4 sm:right-4 sm:w-[400px] overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-2xl">
      <div className="flex items-center justify-between border-b border-border px-3.5 py-2.5">
        <div className="flex items-center gap-2">
          <span className="text-xs font-semibold">传输队列（4MB 分片断点续传）</span>
          <Badge variant="secondary" className="tabular-nums">{tasks.length}</Badge>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="xs" onClick={onClearDone}>
            <Trash2 className="h-3 w-3" />
            <span>清空完成</span>
          </Button>
          <Button variant="ghost" size="icon" onClick={onClose} className="h-7 w-7">
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      <div className="flex gap-1 border-b border-border px-3 py-1.5 text-xs">
        {[
          { key: 'all', label: '全部' },
          { key: 'upload', label: '上传' },
          { key: 'download', label: '下载' },
          { key: 'done', label: '已完成' },
        ].map((item) => (
          <button
            key={item.key}
            type="button"
            onClick={() => setTab(item.key as typeof tab)}
            className={`rounded px-2.5 py-1 font-medium cursor-pointer transition-colors ${
              tab === item.key ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="max-h-72 overflow-y-auto p-2.5 space-y-2">
        {filtered.length === 0 ? (
          <div className="py-8 text-center text-xs text-muted-foreground">当前没有传输任务</div>
        ) : (
          filtered.map((t) => (
            <div key={t.id} className="rounded-lg border border-border bg-background/60 p-2.5 space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-1.5 min-w-0">
                  {t.kind === 'upload' ? (
                    <ArrowUpFromLine className="h-3.5 w-3.5 text-primary shrink-0" />
                  ) : (
                    <ArrowDownToLine className="h-3.5 w-3.5 text-emerald-500 shrink-0" />
                  )}
                  <span className="truncate text-xs font-medium" title={t.name}>{t.name}</span>
                </div>
                <span className="shrink-0 text-[11px] font-medium tabular-nums text-muted-foreground">
                  {t.progress}%
                </span>
              </div>

              <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className={`h-full transition-all ${
                    t.status === 'done'
                      ? 'bg-emerald-500'
                      : t.status === 'error'
                      ? 'bg-destructive'
                      : 'bg-primary'
                  }`}
                  style={{ width: `${t.progress}%` }}
                />
              </div>

              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <span className="truncate">
                  {t.status === 'error'
                    ? t.error || '失败'
                    : t.status === 'paused'
                    ? '已暂停，可断点续传'
                    : t.status === 'done'
                    ? `已完成 (${formatSize(t.size)})`
                    : `${formatSize(t.transferred)} / ${formatSize(t.size)}`}
                </span>
                <div className="flex items-center gap-1 shrink-0">
                  {t.status === 'running' && (
                    <Button variant="ghost" size="xs" onClick={() => onPause(t.id)} title="暂停">
                      <Pause className="h-3 w-3" />
                    </Button>
                  )}
                  {(t.status === 'paused' || t.status === 'error') && (
                    <Button variant="ghost" size="xs" onClick={() => onResume(t.id)} title="继续">
                      <Play className="h-3 w-3" />
                    </Button>
                  )}
                  {t.status !== 'done' && (
                    <Button variant="ghost" size="xs" onClick={() => onCancel(t.id)} title="取消">
                      <X className="h-3 w-3" />
                    </Button>
                  )}
                </div>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
