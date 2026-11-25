import { useMemo, useState } from 'react'
import {
  Clipboard,
  Copy,
  Download,
  Eye,
  Info,
  RefreshCw,
  Send,
  Trash2,
} from 'lucide-react'
import { api } from '@/lib/api'
import { canPreviewFile, copyTextToClipboard, formatSize, formatTime } from '@/lib/utils'
import type { FileRecord, NodeStatus, User } from '@/types'
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Switch, Textarea, useToast } from '@/components/ui'

interface ClipboardPageProps {
  user: User
  files: FileRecord[]
  nodes: NodeStatus[]
  onRefresh: () => void
  onPreviewFile: (rec: FileRecord) => void
  onInspectFile: (rec: FileRecord) => void
  onDownloadFile: (rec: FileRecord) => void
}

export function ClipboardPage({
  user,
  files,
  nodes,
  onRefresh,
  onPreviewFile,
  onInspectFile,
  onDownloadFile,
}: ClipboardPageProps) {
  const { toast } = useToast()
  const isAdmin = user.role === 'admin'
  const [noteTitle, setNoteTitle] = useState('')
  const [content, setContent] = useState('')
  const [sending, setSending] = useState(false)
  const [copyingId, setCopyingId] = useState('')
  const [togglingNode, setTogglingNode] = useState('')

  const clipboardFiles = useMemo(() => {
    return files
      .filter((f) => (f.folder_path || '').includes('超级剪贴板') || (f.name || '').startsWith('剪贴板'))
      .sort((a, b) => new Date(b.uploaded_at).getTime() - new Date(a.uploaded_at).getTime())
  }, [files])

  const sendTextToClipboard = async () => {
    if (!content.trim()) {
      toast('请先输入要同步到局域网剪贴板的文本内容', 'error')
      return
    }
    setSending(true)
    try {
      const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
      const baseName = noteTitle.trim()
        ? noteTitle.trim().replace(/\.txt$/i, '')
        : `剪贴板-${ts}`
      await api.createTxtFile({
        name: `${baseName}.txt`,
        folder_path: '超级剪贴板',
        content,
        allowed_users: [],
        target_node: 'auto',
      })
      toast('已推送到超级剪贴板，所有节点可即时读取', 'success')
      setNoteTitle('')
      setContent('')
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '推送失败', 'error')
    } finally {
      setSending(false)
    }
  }

  const copyFileText = async (rec: FileRecord) => {
    setCopyingId(rec.id)
    try {
      const res = await api.previewFile(rec.id)
      const text = typeof res.content === 'string' ? res.content : ''
      if (!text) {
        onPreviewFile(rec)
        return
      }
      const ok = await copyTextToClipboard(text)
      if (!ok) {
        onPreviewFile(rec)
        return
      }
      toast(`已复制 ${rec.name} 的文本内容`, 'success')
    } catch (err) {
      toast(err instanceof Error ? err.message : '复制文本失败，请点预览查看', 'error')
    } finally {
      setCopyingId('')
    }
  }

  const handleDelete = async (rec: FileRecord) => {
    try {
      await api.deleteFile(rec.id)
      toast(`已移除 ${rec.name}`, 'success')
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除失败', 'error')
    }
  }

  const handleToggleNodeClipboard = async (node: NodeStatus) => {
    setTogglingNode(node.node_id)
    try {
      await api.toggleNodeClipboard(node.node_id, !node.clipboard_enabled)
      toast(
        `${node.node_name} 剪贴板监听已${!node.clipboard_enabled ? '开启' : '关闭'}`,
        'success'
      )
      onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '切换节点剪贴板开关失败', 'error')
    } finally {
      setTogglingNode('')
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg sm:text-xl font-semibold tracking-tight">超级剪贴板</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            跨设备局域网剪贴板中枢 · 节点按 Ctrl+C 复制的文本/截图与网页端推送内容自动汇入此处
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={onRefresh}>
          <RefreshCw className="h-3.5 w-3.5" />
          <span>刷新记录</span>
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        {/* Left: Quick Publish + Node Controls */}
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>快速发送文本到剪贴板</CardTitle>
              <CardDescription>手机、平板或其他电脑打开网页即可一键复制，或触发对应节点系统通知</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <Input
                value={noteTitle}
                onChange={(e) => setNoteTitle(e.target.value)}
                placeholder="可选标题（留空则按时间戳自动命名）"
              />
              <Textarea
                value={content}
                onChange={(e) => setContent(e.target.value)}
                rows={5}
                placeholder="在此粘贴链接、验证码、代码片段或跨设备文本..."
              />
              <Button className="w-full" disabled={sending} onClick={sendTextToClipboard}>
                <Send className="h-3.5 w-3.5" />
                <span>{sending ? '正在同步...' : '同步到全集群超级剪贴板'}</span>
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>各节点本地监听开关</CardTitle>
              <CardDescription>仅在线桌面节点支持监听系统原生剪贴板自动捕获图片与文本</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2.5">
              {nodes.length === 0 ? (
                <div className="text-xs text-muted-foreground">暂无节点信息</div>
              ) : (
                nodes.map((n) => (
                  <div
                    key={n.node_id}
                    className="flex items-center justify-between gap-2 rounded-lg border border-border px-3 py-2 text-xs"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-medium">
                        {n.node_name} {n.is_self && <span className="text-primary">(本机)</span>}
                      </div>
                      <div className="text-[11px] text-muted-foreground">
                        {n.os} · {n.online ? '在线' : '离线'}
                      </div>
                    </div>
                    <Switch
                      checked={Boolean(n.clipboard_enabled)}
                      disabled={!isAdmin || !n.online || togglingNode === n.node_id}
                      onCheckedChange={() => handleToggleNodeClipboard(n)}
                    />
                  </div>
                ))
              )}
            </CardContent>
          </Card>
        </div>

        {/* Right: Clipboard Timeline Feed */}
        <div className="lg:col-span-2 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-sm font-semibold">最近同步记录 ({clipboardFiles.length})</span>
            <span className="text-xs text-muted-foreground">归档目录：/超级剪贴板</span>
          </div>

          {clipboardFiles.length === 0 ? (
            <Card className="p-10 text-center space-y-2">
              <Clipboard className="h-8 w-8 mx-auto text-muted-foreground/60" />
              <div className="text-sm font-medium">暂无剪贴板记录</div>
              <p className="text-xs text-muted-foreground">
                可在左侧直接发送文本，或在已开启监听的 Windows 节点复制内容测试。
              </p>
            </Card>
          ) : (
            <div className="space-y-2.5">
              {clipboardFiles.map((rec) => {
                const isTextLike = rec.category === 'doc' || rec.category === 'code' || rec.ext === 'txt' || rec.ext === 'md'
                const uploaderText = String(rec.uploader || '').trim()
                const devText = String(rec.device_name || '').trim()
                const showDev = Boolean(
                  devText &&
                  !uploaderText.toLowerCase().includes(devText.toLowerCase()) &&
                  (!rec.node_name || !devText.toLowerCase().includes(rec.node_name.toLowerCase()))
                )
                return (
                  <Card key={rec.id} className="p-3.5 hover:border-primary/40 transition-colors">
                    <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
                      <div className="min-w-0 flex-1 space-y-1.5">
                        <div className="text-sm font-medium leading-snug break-all sm:truncate" title={rec.name}>
                          {rec.name}
                        </div>
                        <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                          <Badge variant="outline" className="tabular-nums whitespace-nowrap shrink-0">
                            {formatSize(rec.size)}
                          </Badge>
                          <Badge variant="secondary" className="whitespace-nowrap shrink-0">
                            {rec.node_name || '本机'}
                          </Badge>
                          <span className="truncate">
                            来自 {uploaderText}
                            {showDev ? ` (${devText})` : ''}
                          </span>
                          <span className="tabular-nums whitespace-nowrap">· {formatTime(rec.uploaded_at)}</span>
                        </div>
                      </div>

                      <div className="flex flex-wrap items-center justify-end gap-1.5 border-t border-border/60 pt-2 sm:border-t-0 sm:pt-0 shrink-0">
                        {isTextLike && (
                          <Button
                            size="xs"
                            variant="secondary"
                            className="whitespace-nowrap shrink-0"
                            disabled={copyingId === rec.id}
                            onClick={() => copyFileText(rec)}
                          >
                            <Copy className="h-3 w-3" />
                            <span>{copyingId === rec.id ? '读取中...' : '复制正文'}</span>
                          </Button>
                        )}
                        {canPreviewFile(rec) && (
                          <Button size="xs" variant="outline" className="whitespace-nowrap shrink-0" onClick={() => onPreviewFile(rec)}>
                            <Eye className="h-3 w-3" />
                            <span>预览</span>
                          </Button>
                        )}
                        <Button size="xs" variant="ghost" className="shrink-0" onClick={() => onDownloadFile(rec)} title="下载">
                          <Download className="h-3.5 w-3.5" />
                        </Button>
                        <Button size="xs" variant="ghost" className="shrink-0" onClick={() => onInspectFile(rec)} title="详情">
                          <Info className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="xs"
                          variant="ghost"
                          className="shrink-0 text-destructive hover:text-destructive"
                          onClick={() => handleDelete(rec)}
                          title="删除"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  </Card>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
