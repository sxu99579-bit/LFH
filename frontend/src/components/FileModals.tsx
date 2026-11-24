import { useEffect, useState } from 'react'
import { ArrowLeft, Download, Edit3, Folder, FileArchive, Search } from 'lucide-react'
import { api } from '@/lib/api'
import { formatSize, getFileExt, isEditableTextFile } from '@/lib/utils'
import type { FileRecord, NodeStatus } from '@/types'
import { Badge, Button, Dialog, Input, Select, Switch, Textarea, useToast } from '@/components/ui'

interface DocxBlock {
  kind: 'heading' | 'table' | string
  level?: number
  text?: string
  rows?: string[][]
}

interface XlsxSheet {
  name: string
  rows: string[][]
}

interface PptxSlide {
  title: string
  lines: string[]
}

interface ArchiveEntry {
  path: string
  name: string
  ext?: string
  size: number
  is_dir?: boolean
  can_preview?: boolean
}

function PreviewRenderer({
  data,
  rawUrl,
  name,
  size,
  onSelectArchiveEntry,
  onExtractAll,
  extracting,
}: {
  data: Record<string, unknown> | null
  rawUrl: string
  name: string
  size: number
  onSelectArchiveEntry?: (entry: ArchiveEntry) => void
  onExtractAll?: () => void
  extracting?: boolean
}) {
  const [sheetIdx, setSheetIdx] = useState(0)
  const [slideIdx, setSlideIdx] = useState(0)
  const [archFilter, setArchFilter] = useState('')

  const ext = getFileExt({ ext: '', name })

  if (['mp4', 'webm', 'mov', 'm4v', 'ogv'].includes(ext)) {
    return <video className="max-h-[70vh] w-full rounded-lg bg-black" controls autoPlay src={rawUrl} />
  }
  if (['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a'].includes(ext)) {
    return (
      <div className="rounded-xl border border-border bg-muted/40 p-6 text-center space-y-3">
        <div className="text-sm font-semibold">{name}</div>
        <div className="text-xs text-muted-foreground">{formatSize(size)}</div>
        <audio controls autoPlay src={rawUrl} className="mx-auto w-full max-w-md" />
      </div>
    )
  }
  if (ext === 'pdf') {
    return <iframe src={rawUrl} title={name} className="h-[72vh] w-full rounded-lg border border-border bg-white" />
  }
  if (['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'].includes(ext)) {
    return <img src={rawUrl} alt={name} className="mx-auto max-h-[72vh] max-w-full rounded-lg object-contain" />
  }

  if (!data) {
    return <div className="py-10 text-center text-xs text-muted-foreground">正在加载预览内容...</div>
  }

  if (data.type === 'archive') {
    const entries = (data.entries as ArchiveEntry[]) || []
    const q = archFilter.trim().toLowerCase()
    const filtered = q
      ? entries.filter((e) => (e.path || '').toLowerCase().includes(q) || (e.name || '').toLowerCase().includes(q))
      : entries

    return (
      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant="default">{String(data.format || 'archive').toUpperCase()}</Badge>
            <span>
              共 {Number(data.total_files || 0)} 个文件 · 解压总计 {formatSize(Number(data.total_size || 0))}
            </span>
          </div>
          <div className="flex items-center gap-2 w-full sm:w-auto">
            <div className="relative flex-1 sm:w-56">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                value={archFilter}
                onChange={(e) => setArchFilter(e.target.value)}
                placeholder="搜索包内文件..."
                className="h-8 pl-8 text-xs"
              />
            </div>
            {onExtractAll && (
              <Button size="sm" disabled={extracting} onClick={onExtractAll}>
                <FileArchive className="h-3.5 w-3.5" />
                <span>{extracting ? '解压中...' : '全部解压入库'}</span>
              </Button>
            )}
          </div>
        </div>

        <div className="max-h-[55vh] overflow-y-auto divide-y divide-border rounded-lg border border-border">
          {filtered.length === 0 ? (
            <div className="py-8 text-center text-xs text-muted-foreground">没有匹配的压缩包条目</div>
          ) : (
            filtered.map((entry) => (
              <div key={entry.path} className="flex items-center justify-between gap-2 px-3 py-2 text-xs hover:bg-muted/30">
                <div className="flex items-center gap-2 min-w-0">
                  {entry.is_dir ? (
                    <Folder className="h-3.5 w-3.5 text-amber-500 shrink-0" />
                  ) : (
                    <Badge variant="outline" className="text-[10px] shrink-0">
                      {(entry.ext || 'FILE').toUpperCase()}
                    </Badge>
                  )}
                  <span className="truncate font-mono" title={entry.path}>{entry.path}</span>
                </div>
                {!entry.is_dir && (
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-muted-foreground tabular-nums">{formatSize(entry.size)}</span>
                    {entry.can_preview && onSelectArchiveEntry && (
                      <Button size="xs" onClick={() => onSelectArchiveEntry(entry)}>预览</Button>
                    )}
                    <a
                      href={`/api/files/${encodeURIComponent(String(data.file_id || ''))}/archive-entry?path=${encodeURIComponent(entry.path)}&mode=download`}
                      className="text-primary hover:underline px-1"
                    >
                      下载
                    </a>
                  </div>
                )}
              </div>
            ))
          )}
        </div>
      </div>
    )
  }

  if (data.type === 'docx') {
    const blocks = (data.blocks as DocxBlock[]) || []
    if (blocks.length === 0) return <div className="py-8 text-center text-xs text-muted-foreground">文档内容为空</div>
    return (
      <div className="max-h-[70vh] overflow-y-auto rounded-lg border border-border bg-background p-4 sm:p-6 space-y-3 text-sm leading-relaxed">
        {blocks.map((b, i) => {
          if (b.kind === 'heading') {
            return <h3 key={i} className="text-base font-bold pt-2">{b.text}</h3>
          }
          if (b.kind === 'table' && b.rows) {
            return (
              <div key={i} className="overflow-x-auto rounded border border-border">
                <table className="w-full border-collapse text-xs">
                  <tbody>
                    {b.rows.map((row, ri) => (
                      <tr key={ri} className="border-b border-border last:border-0">
                        {row.map((cell, ci) => (
                          <td key={ci} className={`px-2.5 py-1.5 border-r border-border last:border-0 ${ri === 0 ? 'bg-muted/50 font-semibold' : ''}`}>
                            {cell}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
          return <p key={i} className="whitespace-pre-wrap">{b.text}</p>
        })}
      </div>
    )
  }

  if (data.type === 'xlsx') {
    const sheets = (data.sheets as XlsxSheet[]) || []
    if (sheets.length === 0) return <div className="py-8 text-center text-xs text-muted-foreground">表格为空</div>
    const active = sheets[Math.min(sheetIdx, sheets.length - 1)]
    return (
      <div className="space-y-3">
        <div className="flex flex-wrap gap-1.5">
          {sheets.map((s, idx) => (
            <Button
              key={s.name}
              size="xs"
              variant={idx === sheetIdx ? 'default' : 'outline'}
              onClick={() => setSheetIdx(idx)}
            >
              {s.name}
            </Button>
          ))}
        </div>
        <div className="max-h-[62vh] overflow-auto rounded-lg border border-border">
          <table className="w-full border-collapse text-xs">
            <tbody>
              {(active.rows || []).map((r, ri) => (
                <tr key={ri} className="border-b border-border last:border-0">
                  {r.map((c, ci) => (
                    <td key={ci} className={`px-2.5 py-1.5 border-r border-border last:border-0 ${ri === 0 ? 'bg-muted/50 font-semibold' : ''}`}>
                      {c}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    )
  }

  if (data.type === 'pptx') {
    const slides = (data.slides as PptxSlide[]) || []
    if (slides.length === 0) return <div className="py-8 text-center text-xs text-muted-foreground">未解析到幻灯片内容</div>
    const cur = slides[Math.min(slideIdx, slides.length - 1)]
    return (
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <Button size="xs" variant="outline" disabled={slideIdx === 0} onClick={() => setSlideIdx((s) => Math.max(0, s - 1))}>
            上一页
          </Button>
          <span className="text-xs text-muted-foreground tabular-nums">
            第 {slideIdx + 1} / {slides.length} 页
          </span>
          <Button
            size="xs"
            variant="outline"
            disabled={slideIdx >= slides.length - 1}
            onClick={() => setSlideIdx((s) => Math.min(slides.length - 1, s + 1))}
          >
            下一页
          </Button>
        </div>
        <div className="min-h-[260px] rounded-xl border border-border bg-muted/20 p-6 space-y-3">
          <h3 className="text-lg font-bold">{cur.title}</h3>
          <ul className="list-disc pl-5 space-y-1.5 text-sm">
            {(cur.lines || []).map((line, i) => (
              <li key={i}>{line}</li>
            ))}
          </ul>
        </div>
      </div>
    )
  }

  return (
    <pre className="max-h-[70vh] overflow-auto rounded-lg border border-border bg-background p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words">
      {String(data.content || '')}
    </pre>
  )
}

export function PreviewModal({
  file,
  writableMountIds,
  onClose,
  onOpenEditor,
  targetNode,
  onRefreshAll,
}: {
  file: FileRecord | null
  writableMountIds?: Set<string>
  onClose: () => void
  onOpenEditor: (rec: FileRecord) => void
  targetNode: string
  onRefreshAll: () => void
}) {
  const { toast } = useToast()
  const [data, setData] = useState<Record<string, unknown> | null>(null)
  const [error, setError] = useState('')
  const [activeEntry, setActiveEntry] = useState<ArchiveEntry | null>(null)
  const [entryData, setEntryData] = useState<Record<string, unknown> | null>(null)
  const [extracting, setExtracting] = useState(false)

  useEffect(() => {
    if (!file) return
    setData(null)
    setError('')
    setActiveEntry(null)
    setEntryData(null)
    const ext = getFileExt(file)
    if (['mp4', 'webm', 'mov', 'm4v', 'ogv', 'mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'pdf', 'jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'].includes(ext)) {
      return
    }
    api
      .previewFile(file.id)
      .then((res) => setData({ ...res, file_id: file.id }))
      .catch((err) => setError(err instanceof Error ? err.message : '加载失败'))
  }, [file])

  useEffect(() => {
    if (!file || !activeEntry) return
    setEntryData(null)
    const ext = (activeEntry.ext || '').toLowerCase().replace(/^\./, '')
    if (['mp4', 'webm', 'mov', 'm4v', 'ogv', 'mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'pdf', 'jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'].includes(ext)) {
      return
    }
    api
      .previewArchiveEntry(file.id, activeEntry.path)
      .then((res) => setEntryData(res))
      .catch((err) => setError(err instanceof Error ? err.message : '解压预览失败'))
  }, [activeEntry, file])

  if (!file) return null

  const rawUrl = activeEntry
    ? `/api/files/${encodeURIComponent(file.id)}/archive-entry?path=${encodeURIComponent(activeEntry.path)}&mode=raw`
    : `/api/files/${encodeURIComponent(file.id)}/raw`

  const handleExtractAll = async () => {
    setExtracting(true)
    try {
      const res = await api.extractArchive(file.id, targetNode || 'auto')
      toast(`成功解压 ${(res.extracted || []).length} 个文件到文件库`, 'success')
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '解压失败', 'error')
    } finally {
      setExtracting(false)
    }
  }

  return (
    <Dialog
      open={Boolean(file)}
      onClose={onClose}
      size="xl"
      title={activeEntry ? activeEntry.path : file.name}
      description={`${formatSize(activeEntry ? activeEntry.size : file.size)} · 节点 ${file.node_name || '本机'}`}
      headerExtra={
        <div className="flex items-center gap-1.5">
          {activeEntry && (
            <Button size="xs" variant="outline" onClick={() => setActiveEntry(null)}>
              <ArrowLeft className="h-3.5 w-3.5" />
              <span>返回压缩包</span>
            </Button>
          )}
          {!activeEntry && isEditableTextFile(file, writableMountIds) && (
            <Button
              size="xs"
              variant="outline"
              onClick={() => {
                onClose()
                onOpenEditor(file)
              }}
            >
              <Edit3 className="h-3.5 w-3.5" />
              <span>在线编辑</span>
            </Button>
          )}
          <a
            href={
              activeEntry
                ? `/api/files/${encodeURIComponent(file.id)}/archive-entry?path=${encodeURIComponent(activeEntry.path)}&mode=download`
                : `/api/files/${encodeURIComponent(file.id)}/download`
            }
            className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2.5 text-xs font-medium hover:bg-accent"
          >
            <Download className="h-3.5 w-3.5" />
            <span>下载</span>
          </a>
        </div>
      }
    >
      {error ? (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-4 text-xs text-destructive">{error}</div>
      ) : activeEntry ? (
        <PreviewRenderer
          data={entryData}
          rawUrl={rawUrl}
          name={activeEntry.name}
          size={activeEntry.size}
        />
      ) : (
        <PreviewRenderer
          data={data}
          rawUrl={rawUrl}
          name={file.name}
          size={file.size}
          onSelectArchiveEntry={setActiveEntry}
          onExtractAll={handleExtractAll}
          extracting={extracting}
        />
      )}
    </Dialog>
  )
}

export function TextEditorModal({
  file,
  onClose,
  onSaved,
}: {
  file: FileRecord | null
  onClose: () => void
  onSaved: () => void
}) {
  const { toast } = useToast()
  const [content, setContent] = useState('')
  const [note, setNote] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!file) return
    setLoading(true)
    setNote('')
    api
      .previewFile(file.id)
      .then((res) => setContent(String(res.content || '')))
      .catch((err) => toast(err instanceof Error ? err.message : '加载文本失败', 'error'))
      .finally(() => setLoading(false))
  }, [file, toast])

  if (!file) return null

  const save = async () => {
    setSaving(true)
    try {
      const res = await api.editFile(file.id, content, note.trim())
      toast(`已保存新版本 v${res.file?.version || (file.version || 1) + 1}`, 'success')
      onClose()
      onSaved()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存失败', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={Boolean(file)}
      onClose={onClose}
      size="xl"
      title={`在线编辑 — ${file.name}`}
      description={`当前 v${file.version || 1} · 保存将自动生成新版本并保留历史版本`}
      headerExtra={
        <Button size="xs" disabled={saving || loading} onClick={save}>
          {saving ? '保存中...' : '保存新版本'}
        </Button>
      }
    >
      <div className="space-y-3">
        <Input
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="可选：填写版本更新备注（如：修正配置参数）"
        />
        <Textarea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          disabled={loading}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
              e.preventDefault()
              void save()
            }
          }}
          className="min-h-[55vh] font-mono text-xs leading-relaxed"
          placeholder={loading ? '正在加载文件内容...' : '在此输入或编辑文本内容...'}
        />
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span>提示：支持 Ctrl+S 快捷键直接保存</span>
          <span className="tabular-nums">{content.length} 字符</span>
        </div>
      </div>
    </Dialog>
  )
}

export function CreateTxtModal({
  open,
  onClose,
  isAdmin,
  nodes,
  defaultNode,
  allowedUsers,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  isAdmin: boolean
  nodes: NodeStatus[]
  defaultNode: string
  allowedUsers: string[]
  onCreated: () => void
}) {
  const { toast } = useToast()
  const [name, setName] = useState('')
  const [folder, setFolder] = useState('')
  const [content, setContent] = useState('')
  const [targetNode, setTargetNode] = useState(defaultNode || 'auto')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setName('')
      setFolder('')
      setContent('')
      setTargetNode(defaultNode || 'auto')
    }
  }, [defaultNode, open])

  const submit = async () => {
    if (!name.trim()) {
      toast('请输入文件名', 'error')
      return
    }
    setSaving(true)
    try {
      const res = await api.createTxtFile({
        name: name.trim(),
        folder_path: folder.trim(),
        content,
        allowed_users: allowedUsers,
        target_node: isAdmin ? targetNode : 'auto',
      })
      toast(`已创建并保存：${res.file?.name || name}`, 'success')
      onClose()
      onCreated()
    } catch (err) {
      toast(err instanceof Error ? err.message : '创建失败', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onClose={onClose} size="md" title="新建 TXT 文本文件" description="直接编写纯文本笔记或代码片段并立即入库">
      <div className="space-y-3 text-xs">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="space-y-1">
            <label className="font-medium">文件名（自动补全 .txt）</label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="如：备忘录.txt" />
          </div>
          <div className="space-y-1">
            <label className="font-medium">目标文件夹（可选）</label>
            <Input value={folder} onChange={(e) => setFolder(e.target.value)} placeholder="留空存入根目录" />
          </div>
        </div>
        {isAdmin && (
          <div className="space-y-1">
            <label className="font-medium">存储节点</label>
            <Select value={targetNode} onChange={(e) => setTargetNode(e.target.value)} className="w-full">
              <option value="auto">自动分配（剩余空间最大节点）</option>
              {nodes.filter((n) => n.online).map((n) => (
                <option key={n.node_id} value={n.node_id}>
                  {n.node_name} (剩 {formatSize(n.free_bytes)})
                </option>
              ))}
            </Select>
          </div>
        )}
        <div className="space-y-1">
          <label className="font-medium">正文内容</label>
          <Textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            className="min-h-[220px] font-mono text-xs"
            placeholder="输入文本内容..."
          />
        </div>
        <div className="flex items-center justify-between pt-1">
          <span className="text-[11px] text-muted-foreground tabular-nums">{content.length} 字符</span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={onClose}>取消</Button>
            <Button size="sm" disabled={saving} onClick={submit}>
              {saving ? '创建中...' : '创建并入库'}
            </Button>
          </div>
        </div>
      </div>
    </Dialog>
  )
}

const DEFAULT_MOUNT_IGNORES =
  'node_modules, .git, dist, build, .next, .nuxt, target, vendor, __pycache__, .venv, .idea, .vscode, .DS_Store, *.log'

export function LocalImportModal({
  open,
  initialMode,
  onClose,
  nodes,
  allowedUsers,
  onSuccess,
}: {
  open: boolean
  initialMode: 'import' | 'mount'
  onClose: () => void
  nodes: NodeStatus[]
  allowedUsers: string[]
  onSuccess: () => void
}) {
  const { toast } = useToast()
  const [mode, setMode] = useState<'import' | 'mount'>(initialMode)
  const [nodeId, setNodeId] = useState('')
  const [pathVal, setPathVal] = useState('')
  const [folderVal, setFolderVal] = useState('')
  const [writable, setWritable] = useState(false)
  const [shadowBackup, setShadowBackup] = useState(true)
  const [ignoreText, setIgnoreText] = useState(DEFAULT_MOUNT_IGNORES)
  const [maxFileMB, setMaxFileMB] = useState('50')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) {
      setMode(initialMode)
      setPathVal('')
      setFolderVal('')
      setWritable(false)
      setShadowBackup(true)
      setIgnoreText(DEFAULT_MOUNT_IGNORES)
      setMaxFileMB('50')
      const online = nodes.filter((n) => n.online)
      const def = online.find((n) => n.is_self) || online[0]
      setNodeId(def?.node_id || '')
    }
  }, [initialMode, nodes, open])

  const submit = async () => {
    if (!nodeId || !pathVal.trim()) {
      toast('请选择节点并填写本机绝对路径', 'error')
      return
    }
    setBusy(true)
    try {
      if (mode === 'mount') {
        const parsedIgnores = Array.from(
          new Set(
            ignoreText
              .split(/[\n,;]+/)
              .map((s) => s.trim())
              .filter(Boolean)
          )
        )
        const maxFileBytes = Math.max(1, Number(maxFileMB || 50)) * 1024 * 1024
        await api.addMount({
          node_id: nodeId,
          dir_path: pathVal.trim(),
          alias: folderVal.trim(),
          allowed_users: allowedUsers,
          writable,
          shadow_backup: writable ? shadowBackup : false,
          ignore_patterns: parsedIgnores,
          max_file_bytes: maxFileBytes,
        })
        toast(writable ? '已创建可写项目工作区挂载并开始同步索引' : '已成功挂载本机虚拟目录并开始同步索引', 'success')
      } else {
        const res = await api.importLocal({
          node_id: nodeId,
          source_path: pathVal.trim(),
          folder_path: folderVal.trim(),
          allowed_users: allowedUsers,
        })
        toast(`零拷贝导入完成（共 ${res.imported_count || 1} 项）`, 'success')
      }
      onClose()
      onSuccess()
    } catch (err) {
      toast(err instanceof Error ? err.message : '操作失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="md"
      title="本机零拷贝导入与虚拟目录挂载"
      description="仅管理员可用 · 不重复复制本机已有数据，省出磁盘空间"
    >
      <div className="space-y-3 text-xs">
        <div className="grid grid-cols-2 gap-2">
          <button
            type="button"
            onClick={() => setMode('import')}
            className={`rounded-lg border p-3 text-left cursor-pointer ${
              mode === 'import' ? 'border-primary bg-primary/10' : 'border-border'
            }`}
          >
            <div className="font-semibold">零拷贝导入</div>
            <div className="mt-1 text-[11px] text-muted-foreground">同盘硬链接防误删，跨盘只读外链引用</div>
          </button>
          <button
            type="button"
            onClick={() => setMode('mount')}
            className={`rounded-lg border p-3 text-left cursor-pointer ${
              mode === 'mount' ? 'border-primary bg-primary/10' : 'border-border'
            }`}
          >
            <div className="font-semibold">挂载本机目录</div>
            <div className="mt-1 text-[11px] text-muted-foreground">映射海量存量目录为虚拟库，不占配额</div>
          </button>
        </div>

        <div className="space-y-1">
          <label className="font-medium">目标执行节点</label>
          <Select value={nodeId} onChange={(e) => setNodeId(e.target.value)} className="w-full">
            {nodes.filter((n) => n.online).map((n) => (
              <option key={n.node_id} value={n.node_id}>
                {n.node_name}{n.is_self ? ' (主本机)' : ''}
              </option>
            ))}
          </Select>
        </div>

        <div className="space-y-1">
          <label className="font-medium">节点绝对路径</label>
          <Input
            value={pathVal}
            onChange={(e) => setPathVal(e.target.value)}
            placeholder="例如：E:\Movies 或 /mnt/media"
          />
        </div>

        <div className="space-y-1">
          <label className="font-medium">{mode === 'mount' ? '虚拟库别名（可选）' : '目标虚拟目录（可选）'}</label>
          <Input
            value={folderVal}
            onChange={(e) => setFolderVal(e.target.value)}
            placeholder={mode === 'mount' ? '例如：shopapp 或 高清影视库' : '留空存入根目录'}
          />
        </div>

        {mode === 'mount' && (
          <div className="space-y-2.5 rounded-lg border border-border bg-muted/20 p-3">
            <div className="flex items-center justify-between gap-2">
              <div>
                <div className="font-medium">启用可写项目工作区（允许授权 Agent 双向写穿）</div>
                <div className="text-[11px] text-muted-foreground">
                  默认关闭以保持只读安全；开启后支持多 Agent 写穿源码及网页在线编辑
                </div>
              </div>
              <Switch checked={writable} onCheckedChange={setWritable} />
            </div>

            {writable && (
              <>
                <div className="flex items-center justify-between gap-2 border-t border-border pt-2">
                  <div>
                    <div className="font-medium">开启 Shadow 版本自动备份</div>
                    <div className="text-[11px] text-muted-foreground">
                      Agent 修改覆盖前自动将旧版本保留至 uploads/，随时一键还原写回磁盘
                    </div>
                  </div>
                  <Switch checked={shadowBackup} onCheckedChange={setShadowBackup} />
                </div>

                <div className="grid gap-2 sm:grid-cols-3 border-t border-border pt-2">
                  <div className="sm:col-span-2 space-y-1">
                    <label className="font-medium block">忽略目录 / 文件模板</label>
                    <Textarea
                      value={ignoreText}
                      onChange={(e) => setIgnoreText(e.target.value)}
                      className="min-h-[58px] font-mono text-[11px]"
                    />
                  </div>
                  <div className="space-y-1">
                    <label className="font-medium block">单文件上限 (MB)</label>
                    <Input
                      type="number"
                      min={1}
                      max={1024}
                      value={maxFileMB}
                      onChange={(e) => setMaxFileMB(e.target.value)}
                    />
                  </div>
                </div>

                <div className="flex flex-wrap gap-1">
                  {[
                    { label: '全栈默认', val: DEFAULT_MOUNT_IGNORES },
                    { label: 'Node/前端', val: 'node_modules, dist, .next, .nuxt, coverage, *.log' },
                    { label: 'Go 工程', val: 'bin, dist, vendor, *.exe, *.test' },
                    { label: 'Python', val: '__pycache__, .venv, venv, .pytest_cache, *.pyc' },
                  ].map((tpl) => (
                    <Button
                      key={tpl.label}
                      type="button"
                      size="xs"
                      variant="outline"
                      onClick={() => setIgnoreText(tpl.val)}
                    >
                      {tpl.label}
                    </Button>
                  ))}
                </div>
                <div className="text-[11px] text-muted-foreground">
                  服务端硬编码保护：`.git`、`.ssh`、`.env`、私钥后缀及符号链接逃逸始终禁止读写。
                </div>
              </>
            )}
          </div>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <Button variant="outline" size="sm" onClick={onClose}>取消</Button>
          <Button size="sm" disabled={busy} onClick={submit}>
            {busy ? '正在处理...' : mode === 'mount' ? '立即挂载目录' : '立即零拷贝导入'}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
