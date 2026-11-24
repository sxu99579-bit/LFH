import { useCallback, useEffect, useState } from 'react'
import {
  AlertCircle,
  CheckCircle2,
  KeyRound,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Send,
  Trash2,
  Webhook,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, formatRelativeTime } from '@/lib/utils'
import type { MountItem, WebhookRuleItem } from '@/types'
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  Input,
  Switch,
  useToast,
} from '@/components/ui'

interface WebhookManagerCardProps {
  mounts: MountItem[]
}

const EVENT_OPTIONS: Array<{ value: string; label: string; desc: string }> = [
  { value: 'file.uploaded', label: '文件上传 (file.uploaded)', desc: '新上传或直传短链写入文件' },
  { value: 'file.updated', label: '文件编辑 (file.updated)', desc: '文本在线编辑或补丁修改' },
  { value: 'file.batch_commit', label: '事务提交 (file.batch_commit)', desc: '多文件 batch_commit 原子落盘' },
  { value: 'file.deleted', label: '文件删除 (file.deleted)', desc: '文件被移除或清理' },
  { value: 'verify.completed', label: '校验完成 (verify.completed)', desc: '工作区构建或测试执行结束' },
  { value: '*', label: '全部事件 (*)', desc: '订阅上述所有目录事件' },
]

const EVENT_SHORT: Record<string, string> = {
  'file.uploaded': '上传',
  'file.updated': '编辑',
  'file.batch_commit': '事务提交',
  'file.deleted': '删除',
  'verify.completed': '校验完成',
  '*': '全部事件',
}

function generateRandomWebhookSecret(): string {
  const buf = new Uint8Array(16)
  if (typeof window !== 'undefined' && window.crypto?.getRandomValues) {
    window.crypto.getRandomValues(buf)
  } else {
    for (let i = 0; i < buf.length; i++) {
      buf[i] = Math.floor(Math.random() * 256)
    }
  }
  const hex = Array.from(buf)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
  return `whsec_${hex}`
}

export function WebhookManagerCard({ mounts }: WebhookManagerCardProps) {
  const { toast } = useToast()

  const [webhooks, setWebhooks] = useState<WebhookRuleItem[]>([])
  const [loading, setLoading] = useState(false)
  const [busyId, setBusyId] = useState('')
  const [testingId, setTestingId] = useState('')

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<WebhookRuleItem | null>(null)
  const [formName, setFormName] = useState('')
  const [formPrefix, setFormPrefix] = useState('*')
  const [formTargetURL, setFormTargetURL] = useState('')
  const [formSecret, setFormSecret] = useState('')
  const [formEvents, setFormEvents] = useState<string[]>(['file.uploaded', 'file.batch_commit', 'verify.completed'])
  const [formEnabled, setFormEnabled] = useState(true)
  const [formIncludeTicket, setFormIncludeTicket] = useState(false)
  const [saving, setSaving] = useState(false)

  const loadWebhooks = useCallback(async () => {
    setLoading(true)
    try {
      const res = await api.getWebhooks()
      setWebhooks(res.webhooks || [])
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载 Webhook 规则失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast])

  useEffect(() => {
    void loadWebhooks()
  }, [loadWebhooks])

  const openCreate = () => {
    setEditingRule(null)
    setFormName('')
    setFormPrefix(mounts[0]?.alias || '*')
    setFormTargetURL('')
    setFormSecret(generateRandomWebhookSecret())
    setFormEvents(['file.uploaded', 'file.batch_commit', 'verify.completed'])
    setFormEnabled(true)
    setFormIncludeTicket(false)
    setDialogOpen(true)
  }

  const openEdit = (rule: WebhookRuleItem) => {
    setEditingRule(rule)
    setFormName(rule.name)
    setFormPrefix(rule.folder_prefix || '*')
    setFormTargetURL(rule.target_url)
    setFormSecret('')
    setFormEvents(rule.events && rule.events.length > 0 ? [...rule.events] : ['file.uploaded'])
    setFormEnabled(Boolean(rule.enabled))
    setFormIncludeTicket(Boolean(rule.include_download_ticket))
    setDialogOpen(true)
  }

  const toggleEvent = (ev: string) => {
    setFormEvents((prev) => {
      if (ev === '*') {
        return prev.includes('*') ? ['file.uploaded'] : ['*']
      }
      const next = prev.filter((item) => item !== '*')
      return next.includes(ev) ? next.filter((item) => item !== ev) : [...next, ev]
    })
  }

  const submitForm = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!formName.trim()) {
      toast('请填写 Webhook 规则名称', 'error')
      return
    }
    if (!formTargetURL.trim()) {
      toast('请填写目标回调地址 (http:// 或 https://)', 'error')
      return
    }
    if (formEvents.length === 0) {
      toast('请至少选择一个订阅事件', 'error')
      return
    }

    setSaving(true)
    try {
      const cleanPrefix = formPrefix.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
      if (editingRule) {
        await api.updateWebhook(editingRule.id, {
          name: formName.trim(),
          folder_prefix: cleanPrefix,
          events: formEvents,
          target_url: formTargetURL.trim(),
          enabled: formEnabled,
          include_download_ticket: formIncludeTicket,
          ...(formSecret.trim() ? { secret: formSecret.trim() } : {}),
        })
        toast(`已更新 Webhook「${formName.trim()}」`, 'success')
      } else {
        await api.createWebhook({
          name: formName.trim(),
          folder_prefix: cleanPrefix,
          events: formEvents,
          target_url: formTargetURL.trim(),
          secret: formSecret.trim(),
          enabled: formEnabled,
          include_download_ticket: formIncludeTicket,
        })
        toast(`已创建 Webhook「${formName.trim()}」`, 'success')
      }
      setDialogOpen(false)
      await loadWebhooks()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存 Webhook 失败', 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleToggle = async (rule: WebhookRuleItem) => {
    setBusyId(rule.id)
    try {
      await api.updateWebhook(rule.id, { enabled: !rule.enabled })
      toast(rule.enabled ? `已停用「${rule.name}」` : `已启用「${rule.name}」`, 'success')
      await loadWebhooks()
    } catch (err) {
      toast(err instanceof Error ? err.message : '切换 Webhook 状态失败', 'error')
    } finally {
      setBusyId('')
    }
  }

  const handleTest = async (rule: WebhookRuleItem) => {
    setTestingId(rule.id)
    try {
      const res = await api.testWebhook(rule.id)
      if (res.result.ok) {
        toast(
          `测试投递成功：HTTP ${res.result.status_code ?? 200} (${res.result.duration_ms}ms)`,
          'success'
        )
      } else {
        toast(
          `测试投递失败：${res.result.error || `HTTP ${res.result.status_code || '-'}`}`,
          'error'
        )
      }
      await loadWebhooks()
    } catch (err) {
      toast(err instanceof Error ? err.message : '测试投递失败', 'error')
    } finally {
      setTestingId('')
    }
  }

  const handleDelete = async (rule: WebhookRuleItem) => {
    if (!window.confirm(`确定删除 Webhook 规则「${rule.name}」吗？`)) {
      return
    }
    setBusyId(rule.id)
    try {
      await api.deleteWebhook(rule.id)
      toast(`已删除 Webhook「${rule.name}」`, 'success')
      await loadWebhooks()
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除 Webhook 失败', 'error')
    } finally {
      setBusyId('')
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <div>
          <CardTitle className="flex items-center gap-2">
            <Webhook className="h-4 w-4 text-primary" />
            <span>Webhook 事件回调自动化 ({webhooks.length})</span>
          </CardTitle>
          <CardDescription>
            文件变更与构建校验完成后自动异步推送 HMAC 签名回调（X-LFH-Signature-256），支持退避重试（1s / 4s / 12s）
          </CardDescription>
        </div>
        <div className="flex items-center gap-1.5">
          <Button size="xs" variant="outline" onClick={() => void loadWebhooks()} disabled={loading}>
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
            <span>刷新</span>
          </Button>
          <Button size="xs" onClick={openCreate}>
            <Plus className="h-3.5 w-3.5" />
            <span>新建 Webhook</span>
          </Button>
        </div>
      </CardHeader>

      <CardContent className="space-y-2.5 text-xs">
        {webhooks.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border p-5 text-center text-muted-foreground space-y-1.5">
            <div>尚未配置 Webhook 自动化回调规则。</div>
            <div>创建后可将 `file.uploaded`、`verify.completed` 等事件自动通知另一台机器或 CI 流水线。</div>
          </div>
        ) : (
          webhooks.map((rule) => (
            <div key={rule.id} className="rounded-lg border border-border p-3.5 space-y-2">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-semibold text-sm">{rule.name}</span>
                    {rule.enabled ? (
                      <Badge variant="success">启用中</Badge>
                    ) : (
                      <Badge variant="outline">已停用</Badge>
                    )}
                    <Badge variant="secondary" className="font-mono text-[11px]">
                      {rule.folder_prefix ? `${rule.folder_prefix}/*` : '*'}
                    </Badge>
                    {rule.has_secret && (
                      <Badge variant="outline">HMAC 签名</Badge>
                    )}
                    {rule.include_download_ticket && (
                      <Badge variant="outline">附带下载短链</Badge>
                    )}
                  </div>

                  <div className="font-mono text-[11px] text-muted-foreground break-all">
                    {rule.target_url}
                  </div>

                  <div className="flex flex-wrap items-center gap-1">
                    {(rule.events || []).map((ev) => (
                      <Badge key={ev} variant="default">
                        {EVENT_SHORT[ev] || ev}
                      </Badge>
                    ))}
                  </div>
                </div>

                <div className="flex flex-wrap items-center gap-1 shrink-0">
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={testingId === rule.id}
                    onClick={() => void handleTest(rule)}
                  >
                    <Send className={cn('h-3 w-3', testingId === rule.id && 'animate-pulse')} />
                    <span>{testingId === rule.id ? '发送中...' : '测试投递'}</span>
                  </Button>
                  <Button size="xs" variant="outline" onClick={() => openEdit(rule)}>
                    <Pencil className="h-3 w-3" />
                    <span>编辑</span>
                  </Button>
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={busyId === rule.id}
                    onClick={() => void handleToggle(rule)}
                  >
                    <Power className="h-3 w-3" />
                    <span>{rule.enabled ? '停用' : '启用'}</span>
                  </Button>
                  <Button
                    size="xs"
                    variant="ghost"
                    className="text-destructive"
                    disabled={busyId === rule.id}
                    onClick={() => void handleDelete(rule)}
                  >
                    <Trash2 className="h-3 w-3" />
                    <span>删除</span>
                  </Button>
                </div>
              </div>

              {(rule.last_status || rule.last_error || rule.last_sent_at) && (
                <div className="flex flex-wrap items-center gap-2 border-t border-border/60 pt-1.5 text-[11px] text-muted-foreground">
                  {rule.last_error ? (
                    <span className="inline-flex items-center gap-1 text-destructive">
                      <AlertCircle className="h-3 w-3 shrink-0" />
                      <span>
                        最近投递：{rule.last_status || 'ERROR'} · {rule.last_error}
                      </span>
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
                      <CheckCircle2 className="h-3 w-3 shrink-0" />
                      <span>最近投递：{rule.last_status || '200 OK'}</span>
                    </span>
                  )}
                  {rule.last_sent_at && <span>· {formatRelativeTime(rule.last_sent_at)}</span>}
                </div>
              )}
            </div>
          ))
        )}
      </CardContent>

      <Dialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        size="lg"
        title={editingRule ? `编辑 Webhook — ${editingRule.name}` : '新建 Webhook 自动化规则'}
        description="按目录前缀与事件类型过滤投递，采用有界后台队列与 HMAC-SHA256 验签"
      >
        <form onSubmit={submitForm} className="space-y-4 text-xs">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <label className="font-medium">规则名称 *</label>
              <Input
                value={formName}
                onChange={(e) => setFormName(e.target.value)}
                placeholder="如：构建通知机器人的 CI Hook"
              />
            </div>
            <div className="space-y-1">
              <label className="font-medium">目标 URL *</label>
              <Input
                value={formTargetURL}
                onChange={(e) => setFormTargetURL(e.target.value)}
                placeholder="http://192.168.1.20:9000/webhook"
                className="font-mono text-xs"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">监听目录前缀（* 或留空匹配全部目录）</label>
            <Input
              value={formPrefix}
              onChange={(e) => setFormPrefix(e.target.value)}
              placeholder="如：shopapp 或 dropzone（* 为全部）"
              className="font-mono text-xs"
            />
            <div className="flex flex-wrap gap-1.5">
              <Button type="button" size="xs" variant="outline" onClick={() => setFormPrefix('*')}>
                全部目录 (*)
              </Button>
              {mounts.map((m) => (
                <Button
                  key={`wh-${m.node_id}-${m.id}`}
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => setFormPrefix(m.alias)}
                >
                  {m.alias}
                </Button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">触发事件</label>
            <div className="grid gap-2 sm:grid-cols-2">
              {EVENT_OPTIONS.map((item) => {
                const active = formEvents.includes(item.value)
                return (
                  <button
                    key={item.value}
                    type="button"
                    onClick={() => toggleEvent(item.value)}
                    className={cn(
                      'rounded-lg border p-2.5 text-left transition-colors cursor-pointer',
                      active ? 'border-primary bg-primary/10' : 'border-border hover:bg-muted/40'
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium">{item.label}</span>
                      {active && <Badge variant="default">已选</Badge>}
                    </div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{item.desc}</div>
                  </button>
                )
              })}
            </div>
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between gap-2">
              <label className="font-medium">
                HMAC 签名密钥（用于生成 X-LFH-Signature-256 请求头）
              </label>
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() => setFormSecret(generateRandomWebhookSecret())}
              >
                <KeyRound className="h-3 w-3" />
                <span>随机生成</span>
              </Button>
            </div>
            <Input
              value={formSecret}
              onChange={(e) => setFormSecret(e.target.value)}
              placeholder={
                editingRule?.has_secret
                  ? '已配置密钥；留空则保留原密钥不变，输入新值则覆盖'
                  : '可选，建议填写或点击随机生成'
              }
              className="font-mono text-xs"
            />
          </div>

          <div className="grid gap-2.5 sm:grid-cols-2">
            <div className="flex items-center justify-between rounded-lg border border-border p-3">
              <div>
                <div className="font-medium">启用该 Webhook 规则</div>
                <div className="text-[11px] text-muted-foreground">关闭后仅保留配置，不发送回调</div>
              </div>
              <Switch checked={formEnabled} onCheckedChange={setFormEnabled} />
            </div>

            <div className="flex items-center justify-between rounded-lg border border-border p-3">
              <div>
                <div className="font-medium">随事件附带一次性下载短链</div>
                <div className="text-[11px] text-muted-foreground">
                  仅对勾选规则附带单文件 `/s/ticket` 链接
                </div>
              </div>
              <Switch checked={formIncludeTicket} onCheckedChange={setFormIncludeTicket} />
            </div>
          </div>

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <Button type="button" size="sm" variant="outline" onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button type="submit" size="sm" disabled={saving}>
              {saving ? '保存中...' : editingRule ? '保存修改' : '创建 Webhook'}
            </Button>
          </div>
        </form>
      </Dialog>
    </Card>
  )
}
