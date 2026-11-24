import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Check,
  Copy,
  KeyRound,
  ShieldAlert,
  Wrench,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, copyTextToClipboard, formatRelativeTime } from '@/lib/utils'
import type { AgentKeyItem, AgentScope, MountItem, ServerInfo, User } from '@/types'
import { AgentLiveConsole } from '@/components/AgentLiveConsole'
import { WebhookManagerCard } from '@/components/WebhookManagerCard'
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
  Select,
  Switch,
  Textarea,
  useToast,
} from '@/components/ui'

interface AgentBridgeTabProps {
  user: User
  info: ServerInfo | null
  mounts: MountItem[]
}

type ConfigFormat = 'cursor' | 'claude_cli' | 'generic' | 'curl'

const SCOPE_OPTIONS: Array<{ value: AgentScope; label: string; desc: string }> = [
  { value: 'read', label: '读 (read)', desc: '列目录、搜索、读取与监听变更' },
  { value: 'write', label: '写 (write)', desc: '写入、补丁、batch_commit、租约与回滚' },
  { value: 'share', label: '分享 (share)', desc: '调整 allowed_users 可见成员' },
  { value: 'verify', label: '校验 (verify)', desc: '触发预置工作区构建或测试' },
  { value: 'delete', label: '删 (delete)', desc: '删除文件与历史版本（高危，默认关闭）' },
]

function parseFolderList(raw: string): string[] {
  return Array.from(
    new Set(
      raw
        .split(/[\n,;]+/)
        .map((s) => s.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, ''))
        .filter(Boolean)
    )
  )
}

function appendFolderItem(raw: string, item: string): string {
  const clean = item.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  if (!clean) return raw
  const list = parseFolderList(raw)
  if (!list.includes(clean)) list.push(clean)
  return list.join(', ')
}

function toSafeServerKey(raw: string): string {
  const s = String(raw || 'lfh-workspace')
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return s ? `lfh-${s}` : 'lfh-workspace'
}

export function AgentBridgeTab({ user, info, mounts }: AgentBridgeTabProps) {
  const { toast } = useToast()

  const [keys, setKeys] = useState<AgentKeyItem[]>([])
  const [selectableUsers, setSelectableUsers] = useState<Array<{ username: string; role: string }>>([])
  const [busyId, setBusyId] = useState('')

  // Plaintext token shown once right after key creation (and reused optionally in snippet preview)
  const [newlyCreated, setNewlyCreated] = useState<{ key: AgentKeyItem; token: string } | null>(null)

  // Create / Edit dialog state
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingKey, setEditingKey] = useState<AgentKeyItem | null>(null)
  const [formName, setFormName] = useState('')
  const [formScopes, setFormScopes] = useState<AgentScope[]>(['read', 'write', 'share'])
  const [formReadFolders, setFormReadFolders] = useState('')
  const [formWriteFolders, setFormWriteFolders] = useState('')
  const [formAllowVis, setFormAllowVis] = useState(false)
  const [formDefaultUsers, setFormDefaultUsers] = useState<string[]>([])
  const [formRateLimit, setFormRateLimit] = useState('180')
  const [formExpireHours, setFormExpireHours] = useState('0')
  const [formClearExpire, setFormClearExpire] = useState(false)
  const [savingForm, setSavingForm] = useState(false)

  // MCP config generator state
  const addressOptions = useMemo(() => {
    const out: Array<{ label: string; url: string }> = []
    const seen = new Set<string>()
    for (const addr of info?.entry_addrs || []) {
      const u = String(addr.url || '').trim().replace(/\/+$/, '')
      if (u && !seen.has(u)) {
        seen.add(u)
        out.push({ label: `${addr.tag ? `${addr.tag} · ` : ''}${u}`, url: u })
      }
    }
    const fallback = String(
      info?.entry_url || (typeof window !== 'undefined' ? window.location.origin : '')
    )
      .trim()
      .replace(/\/+$/, '')
    if (fallback && !seen.has(fallback)) {
      seen.add(fallback)
      out.push({ label: fallback, url: fallback })
    }
    return out
  }, [info])

  const [selectedBaseUrl, setSelectedBaseUrl] = useState('')
  const [customBaseUrl, setCustomBaseUrl] = useState('')
  const [selectedKeyId, setSelectedKeyId] = useState('')
  const [manualTokenOverride, setManualTokenOverride] = useState('')
  const [configFormat, setConfigFormat] = useState<ConfigFormat>('cursor')

  useEffect(() => {
    if (!selectedBaseUrl && addressOptions.length > 0) {
      setSelectedBaseUrl(addressOptions[0].url)
    }
  }, [addressOptions, selectedBaseUrl])

  const loadData = useCallback(async () => {
    try {
      const [keyRes, userRes] = await Promise.all([
        api.getAgentKeys(),
        api.getSelectableUsers().catch(() => ({ users: [] })),
      ])
      const list = keyRes.keys || []
      setKeys(list)
      setSelectableUsers(userRes.users || [])
      if (list.length > 0 && !selectedKeyId) {
        setSelectedKeyId(list[0].id)
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载 Agent 密钥列表失败', 'error')
    }
  }, [selectedKeyId, toast])

  useEffect(() => {
    void loadData()
  }, [loadData])

  const openCreateDialog = () => {
    setEditingKey(null)
    setFormName('')
    setFormScopes(['read', 'write', 'share'])
    setFormReadFolders(mounts[0]?.alias || '')
    setFormWriteFolders(mounts.find((m) => m.writable)?.alias || '')
    setFormAllowVis(false)
    setFormDefaultUsers([])
    setFormRateLimit('180')
    setFormExpireHours('0')
    setFormClearExpire(false)
    setDialogOpen(true)
  }

  const openEditDialog = (k: AgentKeyItem) => {
    setEditingKey(k)
    setFormName(k.name)
    setFormScopes(k.scopes && k.scopes.length > 0 ? [...k.scopes] : ['read'])
    setFormReadFolders((k.read_folders || []).join(', '))
    setFormWriteFolders((k.write_folders || []).join(', '))
    setFormAllowVis(Boolean(k.allow_set_visibility))
    setFormDefaultUsers(k.default_allowed_users ? [...k.default_allowed_users] : [])
    setFormRateLimit(String(k.rate_limit_per_min || 180))
    setFormExpireHours('0')
    setFormClearExpire(false)
    setDialogOpen(true)
  }

  const applyRolePreset = (preset: 'reader' | 'developer' | 'publisher') => {
    const firstAlias = mounts[0]?.alias || 'workspace'
    if (preset === 'reader') {
      setFormScopes(['read'])
      setFormAllowVis(false)
      setFormWriteFolders('')
      if (!formReadFolders.trim()) setFormReadFolders(firstAlias)
      if (!formName.trim()) setFormName('只读审阅 Agent')
    } else if (preset === 'developer') {
      setFormScopes(['read', 'write', 'verify'])
      setFormAllowVis(false)
      if (!formReadFolders.trim()) setFormReadFolders(firstAlias)
      if (!formWriteFolders.trim()) setFormWriteFolders(`${firstAlias}/backend`)
      if (!formName.trim()) setFormName('开发协作 Agent')
    } else {
      setFormScopes(['read', 'write', 'share'])
      setFormAllowVis(true)
      if (!formReadFolders.trim()) setFormReadFolders('docs')
      if (!formWriteFolders.trim()) setFormWriteFolders('docs')
      if (!formName.trim()) setFormName('文档投递 Agent')
    }
  }

  const toggleScope = (scope: AgentScope) => {
    setFormScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]
    )
  }

  const submitForm = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!formName.trim()) {
      toast('请填写 Agent 密钥名称', 'error')
      return
    }
    if (formScopes.length === 0) {
      toast('请至少选择一项作用域权限', 'error')
      return
    }
    const parsedRate = Math.max(1, Math.min(6000, Number(formRateLimit || 180)))
    const readFolders = parseFolderList(formReadFolders)
    const writeFolders = parseFolderList(formWriteFolders)

    setSavingForm(true)
    try {
      if (editingKey) {
        await api.updateAgentKey(editingKey.id, {
          name: formName.trim(),
          scopes: formScopes,
          read_folders: readFolders,
          write_folders: writeFolders,
          default_allowed_users: formDefaultUsers,
          allow_set_visibility: formAllowVis,
          rate_limit_per_min: parsedRate,
          clear_expires_at: formClearExpire,
        })
        toast(`已更新 Agent 密钥「${formName.trim()}」`, 'success')
      } else {
        const res = await api.createAgentKey({
          name: formName.trim(),
          scopes: formScopes,
          read_folders: readFolders,
          write_folders: writeFolders,
          default_allowed_users: formDefaultUsers,
          allow_set_visibility: formAllowVis,
          rate_limit_per_min: parsedRate,
          expires_hours: Number(formExpireHours || 0) || undefined,
        })
        setNewlyCreated({ key: res.key, token: res.token })
        setSelectedKeyId(res.key.id)
        toast('Agent 密钥已创建，请立即复制保存明文密钥', 'success')
      }
      setDialogOpen(false)
      await loadData()
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_status' } }))
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存 Agent 密钥失败', 'error')
    } finally {
      setSavingForm(false)
    }
  }

  const handleToggleKey = async (k: AgentKeyItem) => {
    setBusyId(k.id)
    try {
      await api.toggleAgentKey(k.id, !k.disabled)
      toast(k.disabled ? `已启用「${k.name}」` : `已停用「${k.name}」`, 'success')
      await loadData()
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_status' } }))
    } catch (err) {
      toast(err instanceof Error ? err.message : '切换状态失败', 'error')
    } finally {
      setBusyId('')
    }
  }

  const handleDeleteKey = async (k: AgentKeyItem) => {
    if (!window.confirm(`确定永久吊销并删除 Agent 密钥「${k.name}」？使用该密钥的终端将立即失效。`)) {
      return
    }
    setBusyId(k.id)
    try {
      await api.deleteAgentKey(k.id)
      if (newlyCreated?.key.id === k.id) setNewlyCreated(null)
      if (selectedKeyId === k.id) setSelectedKeyId('')
      toast(`已删除密钥「${k.name}」`, 'success')
      await loadData()
      window.dispatchEvent(new CustomEvent('lfh:agent-event', { detail: { kind: 'agent_status' } }))
    } catch (err) {
      toast(err instanceof Error ? err.message : '删除失败', 'error')
    } finally {
      setBusyId('')
    }
  }

  const selectedKey = keys.find((k) => k.id === selectedKeyId) || keys[0] || null
  const effectiveBaseUrl = (customBaseUrl.trim() || selectedBaseUrl || addressOptions[0]?.url || 'http://127.0.0.1:8080').replace(/\/+$/, '')
  const mcpUrl = `${effectiveBaseUrl}/mcp`
  const tokenForSnippet =
    manualTokenOverride.trim() ||
    (newlyCreated && selectedKey && newlyCreated.key.id === selectedKey.id ? newlyCreated.token : '') ||
    (selectedKey ? `${selectedKey.token_prefix}...(请替换为完整密钥)` : 'lfh_ag_xxxxxxxxxxxxxxxxxxxxxxxx')

  const serverAlias = toSafeServerKey(selectedKey?.name || 'lfh-workspace')

  const configSnippet = useMemo(() => {
    switch (configFormat) {
      case 'cursor':
        return JSON.stringify(
          {
            mcpServers: {
              [serverAlias]: {
                type: 'http',
                url: mcpUrl,
                headers: {
                  Authorization: `Bearer ${tokenForSnippet}`,
                },
              },
            },
          },
          null,
          2
        )
      case 'claude_cli':
        return `claude mcp add --transport http ${serverAlias} "${mcpUrl}" --header "Authorization: Bearer ${tokenForSnippet}"`
      case 'generic':
        return JSON.stringify(
          {
            name: serverAlias,
            transport: 'streamable-http',
            url: mcpUrl,
            headers: {
              Authorization: `Bearer ${tokenForSnippet}`,
            },
          },
          null,
          2
        )
      case 'curl':
        return `# 1. 初始化 MCP 会话\ncurl -i -X POST "${mcpUrl}" \\\n  -H "Authorization: Bearer ${tokenForSnippet}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"curl-check","version":"1.0"}}}'\n\n# 2. 调用 whoami 核验权限与挂载工作区（含 verify_presets）\ncurl -s -X POST "${mcpUrl}" \\\n  -H "Authorization: Bearer ${tokenForSnippet}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'\n\n# 3. 签发带外大文件直传短链 (Direct-to-Worker PUT /s/ticket)\ncurl -s -X POST "${effectiveBaseUrl}/api/tickets" \\\n  -H "Authorization: Bearer ${tokenForSnippet}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"action":"up","path":"workspace/artifact.bin","ttl_seconds":600}'`
    }
  }, [configFormat, effectiveBaseUrl, mcpUrl, serverAlias, tokenForSnippet])

  return (
    <div className="space-y-5 min-w-0">
      {/* One-time created token alert banner */}
      {newlyCreated && (
        <Card className="border-emerald-500/40 bg-emerald-500/5">
          <CardHeader className="pb-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <CardTitle className="flex items-center gap-2 text-emerald-600 dark:text-emerald-400">
                <Check className="h-4 w-4 shrink-0" />
                <span>密钥「{newlyCreated.key.name}」已创建 — 请立即保存明文密钥</span>
              </CardTitle>
              <Button size="xs" variant="ghost" onClick={() => setNewlyCreated(null)}>
                收起提示
              </Button>
            </div>
            <CardDescription>
              出于零信任安全设计，服务端磁盘仅存储 SHA-256 哈希。关闭或刷新页面后无法再次查看完整明文。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-xs">
            <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
              <Input readOnly value={newlyCreated.token} className="font-mono text-xs bg-background" />
              <div className="flex items-center gap-1.5 shrink-0">
                <Button
                  size="sm"
                  onClick={async () => {
                    const ok = await copyTextToClipboard(newlyCreated.token)
                    toast(ok ? '已复制完整 Agent 密钥' : '复制失败，请手动复制输入框内容', ok ? 'success' : 'error')
                  }}
                >
                  <Copy className="h-3.5 w-3.5" />
                  <span>复制密钥</span>
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setSelectedKeyId(newlyCreated.key.id)
                    setManualTokenOverride(newlyCreated.token)
                    toast('已自动填入下方 MCP 配置生成器', 'success')
                  }}
                >
                  填入配置
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Block A: Live Agent Presence, One-Click Onboarding & Pairing Approval */}
      <AgentLiveConsole
        user={user}
        mounts={mounts}
        selectableUsers={selectableUsers}
        effectiveBaseUrl={effectiveBaseUrl}
        addressOptions={addressOptions}
        selectedBaseUrl={selectedBaseUrl}
        customBaseUrl={customBaseUrl}
        onChangeSelectedBaseUrl={setSelectedBaseUrl}
        onChangeCustomBaseUrl={setCustomBaseUrl}
        onOpenCreateKey={openCreateDialog}
        onEditKey={openEditDialog}
        onToggleKey={handleToggleKey}
        onDeleteKey={handleDeleteKey}
        onSelectKeyForConfig={(keyId) => {
          setSelectedKeyId(keyId)
          document.getElementById('lfh-manual-mcp-card')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
        }}
        onKeysChanged={loadData}
        busyKeyId={busyId}
      />

      {/* Block B: Manual MCP Client Config Generator */}
      <Card id="lfh-manual-mcp-card">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-primary" />
            <span>一键生成 MCP 客户端接入配置</span>
          </CardTitle>
          <CardDescription>
            选择入口网络地址与 Agent 密钥，直接复制到另一台电脑的 Cursor、Antigravity、Claude Desktop 或 Claude Code CLI 使用
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4 text-xs">
          <div className="grid gap-3 md:grid-cols-3 min-w-0">
            <div className="space-y-1 min-w-0">
              <label className="font-medium block">入口地址（局域网 / Tailscale / 自定义）</label>
              <Select
                value={selectedBaseUrl}
                onChange={(e) => setSelectedBaseUrl(e.target.value)}
                className="w-full h-9 text-xs"
              >
                {addressOptions.map((opt) => (
                  <option key={opt.url} value={opt.url}>
                    {opt.label}
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1 min-w-0">
              <label className="font-medium block">自定义公网 / 穿透地址（选填，覆盖左侧）</label>
              <Input
                value={customBaseUrl}
                onChange={(e) => setCustomBaseUrl(e.target.value)}
                placeholder="如 https://lfh.example.com"
                className="font-mono text-xs"
              />
            </div>

            <div className="space-y-1 min-w-0">
              <label className="font-medium block">目标 Agent 密钥</label>
              <Select
                value={selectedKey?.id || ''}
                onChange={(e) => setSelectedKeyId(e.target.value)}
                className="w-full h-9 text-xs"
              >
                {keys.length === 0 ? (
                  <option value="">示例占位密钥（请先新建密钥）</option>
                ) : (
                  keys.map((k) => (
                    <option key={k.id} value={k.id}>
                      {k.name} ({k.token_prefix})
                    </option>
                  ))
                )}
              </Select>
            </div>
          </div>

          <div className="space-y-1 min-w-0">
            <label className="font-medium block text-[11px] text-muted-foreground">
              可选：仅在本地浏览器内存中填入完整密钥生成即用配置（不会上传或存储到服务端）
            </label>
            <Input
              value={manualTokenOverride}
              onChange={(e) => setManualTokenOverride(e.target.value)}
              placeholder="粘贴以 lfh_ag_ 开头的完整密钥，留空则显示前缀占位符"
              className="font-mono text-xs"
            />
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border pb-2">
            <div className="flex flex-wrap items-center gap-1.5">
              {(
                [
                  { id: 'cursor', label: 'Cursor / Antigravity / Desktop' },
                  { id: 'claude_cli', label: 'Claude Code CLI' },
                  { id: 'generic', label: '通用 Streamable HTTP' },
                  { id: 'curl', label: 'cURL 联调测试' },
                ] as Array<{ id: ConfigFormat; label: string }>
              ).map((fmt) => (
                <Button
                  key={fmt.id}
                  size="xs"
                  variant={configFormat === fmt.id ? 'default' : 'outline'}
                  onClick={() => setConfigFormat(fmt.id)}
                >
                  {fmt.label}
                </Button>
              ))}
            </div>
            <Button
              size="xs"
              onClick={async () => {
                const ok = await copyTextToClipboard(configSnippet)
                toast(ok ? '已复制 MCP 接入配置' : '复制失败，请手动选中复制', ok ? 'success' : 'error')
              }}
            >
              <Copy className="h-3.5 w-3.5" />
              <span>复制配置</span>
            </Button>
          </div>

          <pre className="max-h-72 overflow-x-auto overflow-y-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed whitespace-pre">
            {configSnippet}
          </pre>
        </CardContent>
      </Card>

      {user.role === 'admin' && <WebhookManagerCard mounts={mounts} />}

      {/* Block C: Endpoint & Tool Capability Reference */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Wrench className="h-4 w-4 text-primary" />
            <span>内置 MCP 服务端点与 25 项协同工具</span>
          </CardTitle>
          <CardDescription>
            纯 Go 原生 Streamable HTTP 服务端点：<code className="font-mono">{mcpUrl}</code>（同步开放{' '}
            <code className="font-mono">/agent/connect.ps1</code>、
            <code className="font-mono">/agent/connect.sh</code>、
            <code className="font-mono">/agent/worker.ps1</code>、
            <code className="font-mono">/agent/worker.sh</code>、
            <code className="font-mono">/api/agent-tasks</code>、
            <code className="font-mono">/api/agent-pairing/*</code>、
            <code className="font-mono">/api/agent-sessions</code>、
            <code className="font-mono">/oauth/*</code>、
            <code className="font-mono">{'/api/files/{id}/patch'}</code>、
            <code className="font-mono">/api/files/batch-commit</code>、
            <code className="font-mono">/api/tickets</code>、
            <code className="font-mono">/s/ticket</code>、
            <code className="font-mono">{'/api/mounts/{mountID}/verify/{presetID}'}</code> 与{' '}
            <code className="font-mono">/api/admin/webhooks</code>）
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 md:grid-cols-2 lg:grid-cols-4 text-[11px]">
          <div className="rounded-lg border border-border p-3 space-y-1">
            <div className="font-semibold text-xs">探索、读写与大文件短链</div>
            <div className="text-muted-foreground leading-relaxed font-mono">
              whoami, list_users, list_tree, search_files, read_file, write_file, issue_transfer_ticket
            </div>
          </div>
          <div className="rounded-lg border border-border p-3 space-y-1">
            <div className="font-semibold text-xs">精准补丁、事务与受控构建校验</div>
            <div className="text-muted-foreground leading-relaxed font-mono">
              patch_file, batch_commit, get_file_versions, restore_file_version, trigger_workspace_verify
            </div>
          </div>
          <div className="rounded-lg border border-border p-3 space-y-1">
            <div className="font-semibold text-xs">软租约、状态上报与接力监听</div>
            <div className="text-muted-foreground leading-relaxed font-mono">
              acquire_lease, release_lease, set_file_visibility, delete_file, wait_for_changes, report_agent_status
            </div>
          </div>
          <div className="rounded-lg border border-border p-3 space-y-1">
            <div className="font-semibold text-xs">AI 任务板抢单与拆块协同</div>
            <div className="text-muted-foreground leading-relaxed font-mono">
              list_tasks, claim_task, heartbeat_task, split_task, post_task_comment, complete_task, release_task
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Create / Edit Agent Key Dialog */}
      <Dialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        size="lg"
        title={editingKey ? `编辑 Agent 密钥 — ${editingKey.name}` : '新建零信任 Agent 密钥'}
        description="最小权限原则：默认仅开放安全读写与分享，删除权限与管理员后台隔离受保护"
      >
        <form onSubmit={submitForm} className="space-y-4 text-xs">
          {!editingKey && (
            <div className="space-y-1.5">
              <div className="font-medium">快速角色模板（可选）</div>
              <div className="flex flex-wrap gap-2">
                <Button type="button" size="xs" variant="outline" onClick={() => applyRolePreset('reader')}>
                  只读审阅 Agent
                </Button>
                <Button type="button" size="xs" variant="outline" onClick={() => applyRolePreset('developer')}>
                  开发协作 Agent (可读写+校验)
                </Button>
                <Button type="button" size="xs" variant="outline" onClick={() => applyRolePreset('publisher')}>
                  文档投递 Agent (可设可见人)
                </Button>
              </div>
            </div>
          )}

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <label className="font-medium">密钥名称 *</label>
              <Input
                value={formName}
                onChange={(e) => setFormName(e.target.value)}
                placeholder="如：B电脑-后端Agent"
              />
            </div>
            <div className="space-y-1">
              <label className="font-medium">限流阈值（次/分钟）</label>
              <Input
                type="number"
                min={1}
                max={6000}
                value={formRateLimit}
                onChange={(e) => setFormRateLimit(e.target.value)}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">作用域权限 (Scopes)</label>
            <div className="grid gap-2 sm:grid-cols-2">
              {SCOPE_OPTIONS.map((item) => {
                const active = formScopes.includes(item.value)
                return (
                  <button
                    key={item.value}
                    type="button"
                    onClick={() => toggleScope(item.value)}
                    className={cn(
                      'rounded-lg border p-2.5 text-left transition-colors cursor-pointer',
                      active
                        ? item.value === 'delete'
                          ? 'border-amber-500/60 bg-amber-500/10'
                          : 'border-primary bg-primary/10'
                        : 'border-border hover:bg-muted/40'
                    )}
                  >
                    <div className="flex items-center justify-between">
                      <span className="font-medium">{item.label}</span>
                      {active && (
                        <Badge variant={item.value === 'delete' ? 'warning' : 'default'}>已选</Badge>
                      )}
                    </div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{item.desc}</div>
                  </button>
                )
              })}
            </div>
            {formScopes.includes('delete') && (
              <div className="flex items-center gap-1.5 rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
                <ShieldAlert className="h-3.5 w-3.5 shrink-0" />
                <span>已勾选 delete 权限，请配合下方可写目录白名单严格限制影响范围。</span>
              </div>
            )}
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">
              可读目录白名单（逗号或换行分隔，支持 * 或 目录前缀 如 shopapp/docs）
            </label>
            <Textarea
              value={formReadFolders}
              onChange={(e) => setFormReadFolders(e.target.value)}
              className="min-h-[64px] font-mono text-xs"
              placeholder="例如：shopapp/docs, shopapp/db（留空则仅能读下方可写目录）"
            />
            <div className="flex flex-wrap gap-1.5">
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() => setFormReadFolders((prev) => appendFolderItem(prev, '*'))}
              >
                + 全部目录 (*)
              </Button>
              {mounts.map((m) => (
                <Button
                  key={`rd-${m.node_id}-${m.id}`}
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => setFormReadFolders((prev) => appendFolderItem(prev, m.alias))}
                >
                  + {m.alias}
                </Button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="font-medium block">
              可写目录白名单（逗号或换行分隔，必须显式授权目录才可写入/补丁/建锁）
            </label>
            <Textarea
              value={formWriteFolders}
              onChange={(e) => setFormWriteFolders(e.target.value)}
              className="min-h-[64px] font-mono text-xs"
              placeholder="例如：shopapp/backend（留空代表绝对禁止写入任何文件）"
            />
            {mounts.filter((m) => m.writable).length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {mounts
                  .filter((m) => m.writable)
                  .map((m) => (
                    <Button
                      key={`wr-${m.node_id}-${m.id}`}
                      type="button"
                      size="xs"
                      variant="outline"
                      onClick={() => setFormWriteFolders((prev) => appendFolderItem(prev, m.alias))}
                    >
                      + {m.alias} (可写挂载)
                    </Button>
                  ))}
              </div>
            )}
          </div>

          <div className="rounded-lg border border-border p-3 space-y-3">
            <div className="flex items-center justify-between gap-2">
              <div>
                <div className="font-medium">允许 Agent 自主指定文件可见人 (allow_set_visibility)</div>
                <div className="text-[11px] text-muted-foreground">
                  关闭时强制使用下方预设默认可见成员；开启后 Agent 可指定团队接收人
                </div>
              </div>
              <Switch checked={formAllowVis} onCheckedChange={setFormAllowVis} />
            </div>

            {selectableUsers.length > 0 && (
              <div className="space-y-1.5 border-t border-border pt-2">
                <div className="text-[11px] text-muted-foreground">
                  默认可见成员兜底（未选代表全员可见；一旦指定则自动保留密钥归属者本人访问权限）：
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {selectableUsers.map((su) => {
                    const checked = formDefaultUsers.includes(su.username)
                    return (
                      <button
                        key={su.username}
                        type="button"
                        onClick={() =>
                          setFormDefaultUsers((prev) =>
                            checked ? prev.filter((x) => x !== su.username) : [...prev, su.username]
                          )
                        }
                        className={cn(
                          'rounded-md border px-2 py-1 text-[11px] cursor-pointer',
                          checked ? 'border-primary bg-primary/15 text-primary' : 'border-border text-muted-foreground'
                        )}
                      >
                        {su.username}
                      </button>
                    )
                  })}
                </div>
              </div>
            )}
          </div>

          {!editingKey ? (
            <div className="space-y-1">
              <label className="font-medium">密钥有效期</label>
              <Select
                value={formExpireHours}
                onChange={(e) => setFormExpireHours(e.target.value)}
                className="w-full h-9 text-xs"
              >
                <option value="0">永不过期（可随时手动停用或删除）</option>
                <option value="24">24 小时临时任务密钥</option>
                <option value="168">7 天</option>
                <option value="720">30 天</option>
              </Select>
            </div>
          ) : (
            editingKey.expires_at && (
              <div className="flex items-center justify-between rounded-lg border border-border p-3">
                <div>
                  <div className="font-medium">清除到期时间限制（设为长期有效）</div>
                  <div className="text-[11px] text-muted-foreground">
                    当前到期：{formatRelativeTime(editingKey.expires_at)}
                  </div>
                </div>
                <Switch checked={formClearExpire} onCheckedChange={setFormClearExpire} />
              </div>
            )
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <Button type="button" variant="outline" size="sm" onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button type="submit" size="sm" disabled={savingForm}>
              {savingForm ? '保存中...' : editingKey ? '保存修改' : '生成 Agent 密钥'}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  )
}
