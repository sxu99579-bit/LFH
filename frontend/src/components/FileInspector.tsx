import { useEffect, useState } from 'react'
import {
  Bot,
  Cpu,
  Download,
  Edit3,
  Eye,
  History,
  Link2,
  ListChecks,
  Lock,
  RotateCcw,
  ShieldCheck,
  Trash2,
  Unlock,
} from 'lucide-react'
import { api } from '@/lib/api'
import {
  canPreviewFile,
  copyTextToClipboard,
  extractAgentLabel,
  formatSize,
  formatStorageModeLabel,
  formatTime,
  formatUpdateMethodLabel,
  getMatchingCapabilitiesForFile,
  isAgentDeviceName,
  isEditableTextFile,
  isMobileLayout,
} from '@/lib/utils'
import type { ClusterCapabilityView, FileLease, FileRecord, FileVersion, PluginJob, User } from '@/types'
import { Badge, Button, Sheet, useToast } from '@/components/ui'

export function FileInspector({
  file,
  user,
  capabilities,
  writableMountIds,
  onClose,
  onOpenPreview,
  onOpenEditor,
  onQueueDownload,
  onRefreshAll,
}: {
  file: FileRecord | null
  user: User
  capabilities: ClusterCapabilityView[]
  writableMountIds?: Set<string>
  onClose: () => void
  onOpenPreview: (rec: FileRecord) => void
  onOpenEditor: (rec: FileRecord) => void
  onQueueDownload: (rec: FileRecord) => void
  onRefreshAll: () => void
}) {
  const { toast } = useToast()
  const [meta, setMeta] = useState<{
    disk_path?: string
    source_path?: string
    integrity_status?: string
    mime?: string
  } | null>(null)
  const [lease, setLease] = useState<FileLease | null>(null)
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [releasingLease, setReleasingLease] = useState(false)
  const [issuingTicket, setIssuingTicket] = useState(false)
  const [versions, setVersions] = useState<FileVersion[]>([])
  const [curVersion, setCurVersion] = useState<number>(1)
  const [runningPlugin, setRunningPlugin] = useState<string>('')
  const [jobResult, setJobResult] = useState<{ job: PluginJob; outputFile?: FileRecord } | null>(null)
  const [selectableUsers, setSelectableUsers] = useState<Array<{ username: string; role: string }>>([])
  const [allowedDraft, setAllowedDraft] = useState<string[]>([])
  const [editingAccess, setEditingAccess] = useState(false)

  useEffect(() => {
    if (!file) return
    setMeta(null)
    setLease(null)
    setNowMs(Date.now())
    setJobResult(null)
    setEditingAccess(false)
    setAllowedDraft(Array.isArray(file.allowed_users) ? file.allowed_users : [])

    if (file.node_online !== false && !file.source_missing) {
      api
        .getFileMeta(file.id, user.role === 'admin')
        .then((res) => setMeta(res))
        .catch(() => setMeta(null))
      api
        .getFileVersions(file.id)
        .then((res) => {
          setVersions(res.versions || [])
          setCurVersion(res.current_version || file.version || 1)
        })
        .catch(() => setVersions([]))
      api
        .getFileLease(file.id)
        .then((res) => setLease(res.locked && res.lease ? res.lease : null))
        .catch(() => setLease(null))
    }
  }, [file, user.role])

  useEffect(() => {
    if (!lease) return
    const timer = window.setInterval(() => {
      setNowMs(Date.now())
    }, 1000)
    return () => window.clearInterval(timer)
  }, [lease])

  if (!file) return null

  const online = file.node_online !== false
  const canManage = user.role === 'admin' || user.username === file.uploader
  const isMount = file.storage_mode === 'mount'
  const isWritableMount = isMount && Boolean(file.mount_id && writableMountIds?.has(file.mount_id))
  const matches = online && !file.source_missing ? getMatchingCapabilitiesForFile(capabilities, file.name) : []
  const storageTag = formatStorageModeLabel(file.storage_mode)
  const fromAgent = isAgentDeviceName(file.device_name)
  const leaseRemainingSec = lease
    ? Math.max(0, Math.ceil((new Date(lease.expires_at).getTime() - nowMs) / 1000))
    : 0
  const activeLease = lease && leaseRemainingSec > 0 ? lease : null

  const handleForceReleaseLease = async () => {
    setReleasingLease(true)
    try {
      await api.releaseFileLease(file.id, true)
      setLease(null)
      toast('已强制解除文件软租约锁定', 'success')
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '解除软租约失败', 'error')
    } finally {
      setReleasingLease(false)
    }
  }

  const handleCopyTransferTicket = async () => {
    setIssuingTicket(true)
    try {
      const res = await api.issueTicket({
        action: 'down',
        file_id: file.id,
        ttl_seconds: 600,
        prefer_direct: true,
      })
      const ok = await copyTextToClipboard(res.recommended_url)
      if (ok) {
        const isDirect = Boolean(res.direct_url && res.recommended_url === res.direct_url)
        toast(
          `已复制 10 分钟临时下载短链（${isDirect ? `直连节点 ${res.node_name}` : '入口签名'}）`,
          'success'
        )
      } else {
        toast('复制下载短链失败，请检查剪贴板权限', 'error')
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : '签发临时下载短链失败', 'error')
    } finally {
      setIssuingTicket(false)
    }
  }

  const runCompute = async (cap: ClusterCapabilityView) => {
    setRunningPlugin(cap.plugin_id)
    setJobResult(null)
    try {
      const res = await api.runPluginJob(file.id, cap.capability, cap.plugin_id)
      setJobResult({ job: res.job, outputFile: res.output_file })
      toast(`算力任务完成：已调度至 ${res.job.compute_node_name}`, 'success')
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '算力任务执行失败', 'error')
    } finally {
      setRunningPlugin('')
    }
  }

  const openAccessEditor = async () => {
    if (!editingAccess && selectableUsers.length === 0) {
      try {
        const res = await api.getSelectableUsers()
        setSelectableUsers(res.users || [])
      } catch {
        // ignore
      }
    }
    setEditingAccess((v) => !v)
  }

  const saveAccess = async () => {
    try {
      await api.updateFileAccess(file.id, allowedDraft)
      toast('已更新文件访问权限', 'success')
      setEditingAccess(false)
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存权限失败', 'error')
    }
  }

  const handleRestoreVersion = async (ver: number) => {
    if (!confirm(`确定将「${file.name}」恢复到 v${ver} 吗？`)) return
    try {
      await api.restoreFileVersion(file.id, ver)
      toast(`已回退并生效 v${ver}`, 'success')
      const res = await api.getFileVersions(file.id)
      setVersions(res.versions || [])
      setCurVersion(res.current_version || ver)
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '恢复版本失败', 'error')
    }
  }

  const handleDeleteVersion = async (ver: number) => {
    if (!confirm(`确定删除历史版本 v${ver} 吗？`)) return
    try {
      await api.deleteFileVersion(file.id, ver)
      toast(`已删除历史版本 v${ver}`, 'success')
      const res = await api.getFileVersions(file.id)
      setVersions(res.versions || [])
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除版本失败', 'error')
    }
  }

  const handleDeleteFile = async () => {
    if (!confirm(`确定删除文件「${file.name}」吗？`)) return
    try {
      await api.deleteFile(file.id)
      toast('文件已删除', 'success')
      onClose()
      onRefreshAll()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除失败', 'error')
    }
  }

  return (
    <Sheet
      open={Boolean(file)}
      onClose={onClose}
      side={isMobileLayout() ? 'bottom' : 'right'}
      title={file.name}
      subtitle={`${formatSize(file.size)} · 节点 ${file.node_name || '本机'} · v${file.version || 1}`}
    >
      <div className="space-y-5 text-xs">
        {/* Quick actions */}
        <div className="flex flex-wrap gap-2">
          {canPreviewFile(file) && (
            <Button
              size="sm"
              disabled={!online || Boolean(file.source_missing)}
              onClick={() => onOpenPreview(file)}
            >
              <Eye className="h-3.5 w-3.5" />
              <span>在线预览</span>
            </Button>
          )}
          {isEditableTextFile(file, writableMountIds) && (
            <Button
              variant="outline"
              size="sm"
              disabled={!online || Boolean(file.source_missing)}
              onClick={() => onOpenEditor(file)}
            >
              <Edit3 className="h-3.5 w-3.5" />
              <span>在线编辑</span>
            </Button>
          )}
          {online && !file.source_missing && (
            <>
              <a
                href={`/api/files/${encodeURIComponent(file.id)}/download`}
                className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-xs font-medium hover:bg-accent"
              >
                <Download className="h-3.5 w-3.5" />
                <span>下载</span>
              </a>
              <Button variant="outline" size="sm" onClick={() => onQueueDownload(file)}>
                <ListChecks className="h-3.5 w-3.5" />
                <span>队列续传</span>
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={issuingTicket}
                onClick={() => void handleCopyTransferTicket()}
              >
                <Link2 className="h-3.5 w-3.5" />
                <span>{issuingTicket ? '签发中...' : '临时下载短链'}</span>
              </Button>
            </>
          )}
          {canManage && (!isMount || isWritableMount) && (
            <Button variant="destructive" size="sm" disabled={!online} onClick={handleDeleteFile}>
              <Trash2 className="h-3.5 w-3.5" />
              <span>删除</span>
            </Button>
          )}
        </div>

        {/* Active Soft Lease Banner */}
        {activeLease && (
          <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 flex flex-wrap items-center justify-between gap-2">
            <div className="min-w-0 space-y-0.5">
              <div className="flex items-center gap-1.5 font-semibold text-amber-600 dark:text-amber-400">
                <Lock className="h-3.5 w-3.5 shrink-0" />
                <span>当前正被软租约锁定编辑（剩余 {leaseRemainingSec} 秒）</span>
              </div>
              <div className="text-[11px] text-muted-foreground truncate">
                持锁方：{activeLease.holder_name || activeLease.holder_key_id}
              </div>
            </div>
            {user.role === 'admin' && (
              <Button size="xs" variant="outline" disabled={releasingLease} onClick={() => void handleForceReleaseLease()}>
                <Unlock className="h-3 w-3" />
                <span>{releasingLease ? '解锁中...' : '强制解锁'}</span>
              </Button>
            )}
          </div>
        )}

        {/* Distributed compute plugins */}
        {matches.length > 0 && (
          <div className="rounded-lg border border-border bg-muted/40 p-3 space-y-2.5">
            <div className="flex items-center gap-1.5 font-semibold text-foreground">
              <Cpu className="h-4 w-4 text-primary" />
              <span>可用分布式算力插件</span>
            </div>
            <div className="space-y-2">
              {matches.map((cap) => (
                <div
                  key={cap.plugin_id}
                  className="flex items-center justify-between gap-2 rounded-md border border-border bg-card p-2.5"
                >
                  <div className="min-w-0">
                    <div className="font-medium truncate">{cap.name || cap.capability}</div>
                    <div className="text-[11px] text-muted-foreground">
                      {cap.providers.length} 个算力节点在线 ·{' '}
                      {cap.providers.map((p) => `${p.node_name}${p.has_gpu ? '(GPU)' : ''}`).join('、')}
                    </div>
                  </div>
                  <Button
                    size="xs"
                    disabled={Boolean(runningPlugin)}
                    onClick={() => runCompute(cap)}
                  >
                    {runningPlugin === cap.plugin_id ? '执行中...' : '调度执行'}
                  </Button>
                </div>
              ))}
            </div>
            {jobResult && (
              <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 p-2.5 space-y-1.5">
                <div className="font-medium text-emerald-600 dark:text-emerald-400">
                  已由 {jobResult.job.compute_node_name} 执行完成（{jobResult.job.duration_ms} ms）
                </div>
                <div className="text-foreground">{jobResult.job.summary}</div>
                {jobResult.outputFile && (
                  <a
                    href={`/api/files/${encodeURIComponent(jobResult.outputFile.id)}/download`}
                    className="inline-flex items-center gap-1 font-medium text-primary underline"
                  >
                    下载产物：{jobResult.outputFile.name}
                  </a>
                )}
              </div>
            )}
          </div>
        )}

        {/* Properties */}
        <div className="rounded-lg border border-border p-3 space-y-2">
          <div className="flex items-center justify-between">
            <span className="font-semibold">文件详情与完整性</span>
            <div className="flex items-center gap-1">
              {storageTag && <Badge variant="secondary">{storageTag}</Badge>}
              {meta?.integrity_status === 'ok' && (
                <Badge variant="success">
                  <ShieldCheck className="h-3 w-3" />
                  SHA-256 正常
                </Badge>
              )}
              {(file.source_missing || meta?.integrity_status === 'corrupted') && (
                <Badge variant="destructive">源文件异常/缺失</Badge>
              )}
            </div>
          </div>

          <div className="grid grid-cols-3 gap-y-1.5 text-[11px]">
            <span className="text-muted-foreground">上传者</span>
            <span className="col-span-2">{file.uploader}</span>
            <span className="text-muted-foreground">目录位置</span>
            <span className="col-span-2 break-all">{file.folder_path || '/'}</span>
            <span className="text-muted-foreground">上传时间</span>
            <span className="col-span-2">{formatTime(file.uploaded_at)}</span>
            <span className="text-muted-foreground">更新方式</span>
            <span className="col-span-2">{formatUpdateMethodLabel(file.update_method, file.version)}</span>
            <span className="text-muted-foreground">来源设备</span>
            <span className="col-span-2 flex flex-wrap items-center gap-1">
              {fromAgent ? (
                <Badge variant="default">
                  <Bot className="h-3 w-3" />
                  <span>{extractAgentLabel(file.device_name)}</span>
                </Badge>
              ) : (
                <span>
                  {file.device_name || '-'} ({file.device_fp || '-'})
                </span>
              )}
            </span>
            <span className="text-muted-foreground">SHA-256</span>
            <span className="col-span-2 break-all font-mono text-[10px]">{file.sha256 || '-'}</span>
            {user.role === 'admin' && (meta?.disk_path || file.storage_dir) && (
              <>
                <span className="text-muted-foreground">物理路径</span>
                <span className="col-span-2 break-all font-mono text-[10px]">{meta?.disk_path || file.storage_dir}</span>
              </>
            )}
          </div>
        </div>

        {/* Access control */}
        {canManage && !isMount && (
          <div className="rounded-lg border border-border p-3 space-y-2">
            <div className="flex items-center justify-between">
              <span className="inline-flex items-center gap-1.5 font-semibold">
                <Lock className="h-3.5 w-3.5 text-muted-foreground" />
                可见与下载权限
              </span>
              <Button variant="ghost" size="xs" onClick={openAccessEditor}>
                {editingAccess ? '收起' : '修改权限'}
              </Button>
            </div>
            <div className="text-[11px] text-muted-foreground">
              {allowedDraft.length === 0
                ? '当前为全员可见并可下载'
                : `仅限指定用户访问：${allowedDraft.join('、')}`}
            </div>
            {editingAccess && (
              <div className="space-y-2 border-t border-border pt-2">
                <div className="flex flex-wrap gap-1.5">
                  {selectableUsers.map((u) => {
                    const checked = allowedDraft.includes(u.username)
                    return (
                      <button
                        key={u.username}
                        type="button"
                        onClick={() =>
                          setAllowedDraft((prev) =>
                            checked ? prev.filter((x) => x !== u.username) : [...prev, u.username]
                          )
                        }
                        className={`rounded-md border px-2 py-1 text-[11px] cursor-pointer ${
                          checked ? 'border-primary bg-primary/15 text-primary' : 'border-border text-muted-foreground'
                        }`}
                      >
                        {u.username}
                      </button>
                    )
                  })}
                </div>
                <div className="flex gap-2">
                  <Button size="xs" onClick={saveAccess}>保存权限</Button>
                  <Button variant="outline" size="xs" onClick={() => setAllowedDraft([])}>设为全员可见</Button>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Version timeline */}
        <div className="rounded-lg border border-border p-3 space-y-2.5">
          <div className="flex items-center justify-between">
            <span className="inline-flex items-center gap-1.5 font-semibold">
              <History className="h-3.5 w-3.5 text-primary" />
              版本历史（共 {versions.length} 版）
            </span>
          </div>
          {versions.length === 0 ? (
            <div className="text-[11px] text-muted-foreground">仅 v1 初始版本</div>
          ) : (
            <div className="space-y-2">
              {versions.map((v) => {
                const isCur = v.version === curVersion
                const verFromAgent = isAgentDeviceName(v.device_name)
                return (
                  <div
                    key={v.version}
                    className={`rounded-md border p-2 space-y-1 ${isCur ? 'border-primary/40 bg-primary/5' : 'border-border'}`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <Badge variant={isCur ? 'default' : 'outline'}>v{v.version}</Badge>
                        <span>{formatUpdateMethodLabel(v.update_method, v.version)}</span>
                        <span className="text-muted-foreground">· {v.uploader}</span>
                        {verFromAgent && (
                          <Badge variant="secondary">
                            <Bot className="h-3 w-3" />
                            <span>{extractAgentLabel(v.device_name)}</span>
                          </Badge>
                        )}
                      </div>
                      <span className="text-[11px] text-muted-foreground tabular-nums">{formatSize(v.size)}</span>
                    </div>
                    {v.note && (
                      <div className="text-[11px] text-foreground/85 break-words">{v.note}</div>
                    )}
                    <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                      <span>{v.device_name || v.device_fp || '-'} · {formatTime(v.uploaded_at)}</span>
                      <div className="flex items-center gap-1">
                        <a
                          href={`/api/files/${encodeURIComponent(file.id)}/versions/${v.version}/download`}
                          className="text-primary hover:underline px-1"
                        >
                          下载
                        </a>
                        {canManage && !isCur && (
                          <>
                            <button
                              type="button"
                              onClick={() => handleRestoreVersion(v.version)}
                              className="inline-flex items-center gap-0.5 text-primary hover:underline px-1 cursor-pointer"
                            >
                              <RotateCcw className="h-3 w-3" /> 还原
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDeleteVersion(v.version)}
                              className="text-destructive hover:underline px-1 cursor-pointer"
                            >
                              删除
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </Sheet>
  )
}
