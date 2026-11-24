import { useEffect, useState } from 'react'
import { Cpu, Laptop, Monitor, Server } from 'lucide-react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { AgentAvatarId, AgentDeviceKind, AgentKeyItem } from '@/types'
import { Badge, Button, Dialog, Input, useToast } from '@/components/ui'

export interface AgentAvatarMeta {
  id: AgentAvatarId
  label: string
  roleHint: string
  bgHex: string
}

export const AGENT_AVATARS: AgentAvatarMeta[] = [
  { id: 'shiba', label: '柴犬架构师', roleHint: '后端与核心重构', bgHex: '#fef3c7' },
  { id: 'bluecat', label: '蓝猫像素匠', roleHint: '前端与交互设计', bgHex: '#dbeafe' },
  { id: 'panda', label: '熊猫质检官', roleHint: '单测与边界校验', bgHex: '#f1f5f9' },
  { id: 'owl', label: '猫头鹰夜巡', roleHint: '日志分析与定时值守', bgHex: '#ede9fe' },
  { id: 'bunny', label: '兔兔文档官', roleHint: '文档汇总与周报撰写', bgHex: '#fce7f3' },
  { id: 'fox', label: '狐狸调优师', roleHint: '算法优化与压测排查', bgHex: '#ffedd5' },
  { id: 'penguin', label: '企鹅运维官', roleHint: 'Linux 部署与脚本构建', bgHex: '#cffafe' },
  { id: 'mecha', label: '机甲总控官', roleHint: '多阶段拆分与协调', bgHex: '#e0e7ff' },
]

export const DEVICE_KIND_OPTIONS: Array<{
  id: AgentDeviceKind
  label: string
  shortLabel: string
}> = [
  { id: 'laptop', label: '笔记本电脑', shortLabel: '笔记本' },
  { id: 'desktop', label: '台式工作站', shortLabel: '台式机' },
  { id: 'mac', label: 'Mac / 苹果电脑', shortLabel: 'Mac' },
  { id: 'server', label: '服务器 / NAS', shortLabel: '服务器' },
]

const VALID_AVATAR_SET = new Set<string>(AGENT_AVATARS.map((a) => a.id))

const OWNER_RING_PALETTE = [
  '#2563eb',
  '#10b981',
  '#f59e0b',
  '#8b5cf6',
  '#ec4899',
  '#06b6d4',
  '#ef4444',
  '#84cc16',
]

function hashString(raw: string): number {
  let h = 2166136261
  for (let i = 0; i < raw.length; i += 1) {
    h ^= raw.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

export function resolveAvatarId(key?: { id?: string; avatar_id?: string | null }): AgentAvatarId {
  const raw = String(key?.avatar_id || '').trim().toLowerCase()
  if (VALID_AVATAR_SET.has(raw)) {
    return raw as AgentAvatarId
  }
  const seed = String(key?.id || 'default')
  return AGENT_AVATARS[hashString(seed) % AGENT_AVATARS.length].id
}

export function ownerRingColor(ownerUsername?: string): string {
  const clean = String(ownerUsername || 'default').trim().toLowerCase() || 'default'
  return OWNER_RING_PALETTE[hashString(clean) % OWNER_RING_PALETTE.length]
}

export function agentDisplayName(key?: { name?: string; display_alias?: string }): string {
  const alias = String(key?.display_alias || '').trim()
  if (alias) return alias
  return String(key?.name || 'Agent')
}

export function agentDeviceLabel(key?: {
  device_alias?: string
  last_used_ip?: string
}): string {
  const alias = String(key?.device_alias || '').trim()
  if (alias) return alias
  return String(key?.last_used_ip || '').trim()
}

export function deviceKindShortLabel(kind?: AgentDeviceKind | string): string {
  const found = DEVICE_KIND_OPTIONS.find((d) => d.id === kind)
  return found ? found.shortLabel : '电脑'
}

export function DeviceMiniIcon({
  kind,
  className = 'h-3 w-3',
}: {
  kind?: AgentDeviceKind | string
  className?: string
}) {
  if (kind === 'desktop') return <Monitor className={className} />
  if (kind === 'server') return <Server className={className} />
  if (kind === 'mac') return <Cpu className={className} />
  return <Laptop className={className} />
}

export function AgentMascotSvg({
  avatarId,
  className = 'h-full w-full',
}: {
  avatarId: AgentAvatarId
  className?: string
}) {
  const meta = AGENT_AVATARS.find((a) => a.id === avatarId) || AGENT_AVATARS[0]
  return (
    <svg viewBox="0 0 48 48" className={className} aria-hidden="true">
      <circle cx="24" cy="24" r="23" fill={meta.bgHex} />

      {avatarId === 'shiba' && (
        <g>
          <polygon points="10,20 14,7 22,15" fill="#d97706" />
          <polygon points="38,20 34,7 26,15" fill="#d97706" />
          <polygon points="12,18 15,10 19,15" fill="#fde68a" />
          <polygon points="36,18 33,10 29,15" fill="#fde68a" />
          <ellipse cx="24" cy="26" rx="14" ry="12.5" fill="#f59e0b" />
          <ellipse cx="24" cy="30" rx="10.5" ry="7.5" fill="#fffbeb" />
          <circle cx="18.5" cy="23.5" r="1.9" fill="#1e293b" />
          <circle cx="29.5" cy="23.5" r="1.9" fill="#1e293b" />
          <circle cx="19" cy="22.8" r="0.6" fill="#ffffff" />
          <circle cx="30" cy="22.8" r="0.6" fill="#ffffff" />
          <ellipse cx="24" cy="27.5" rx="2.3" ry="1.6" fill="#1e293b" />
          <path d="M21.5 30.8 Q24 33 26.5 30.8" fill="none" stroke="#1e293b" strokeWidth="1.5" strokeLinecap="round" />
          <circle cx="14.5" cy="27.5" r="2.2" fill="#fca5a5" opacity="0.65" />
          <circle cx="33.5" cy="27.5" r="2.2" fill="#fca5a5" opacity="0.65" />
        </g>
      )}

      {avatarId === 'bluecat' && (
        <g>
          <polygon points="11,22 12,8 21,15" fill="#3b82f6" />
          <polygon points="37,22 36,8 27,15" fill="#3b82f6" />
          <polygon points="13,19 14,11 19,15" fill="#93c5fd" />
          <polygon points="35,19 34,11 29,15" fill="#93c5fd" />
          <ellipse cx="24" cy="26.5" rx="14" ry="12" fill="#60a5fa" />
          <ellipse cx="24" cy="30.5" rx="8.5" ry="6" fill="#eff6ff" />
          <circle cx="18" cy="24" r="2.2" fill="#0f172a" />
          <circle cx="30" cy="24" r="2.2" fill="#0f172a" />
          <circle cx="18.7" cy="23.2" r="0.7" fill="#ffffff" />
          <circle cx="30.7" cy="23.2" r="0.7" fill="#ffffff" />
          <polygon points="22.4,27.2 25.6,27.2 24,29" fill="#f43f5e" />
          <path d="M21 30.8 Q24 32.5 27 30.8" fill="none" stroke="#1e293b" strokeWidth="1.3" strokeLinecap="round" />
          <line x1="9" y1="26" x2="14" y2="27" stroke="#1d4ed8" strokeWidth="1.2" strokeLinecap="round" />
          <line x1="9" y1="29" x2="14" y2="29" stroke="#1d4ed8" strokeWidth="1.2" strokeLinecap="round" />
          <line x1="39" y1="26" x2="34" y2="27" stroke="#1d4ed8" strokeWidth="1.2" strokeLinecap="round" />
          <line x1="39" y1="29" x2="34" y2="29" stroke="#1d4ed8" strokeWidth="1.2" strokeLinecap="round" />
        </g>
      )}

      {avatarId === 'panda' && (
        <g>
          <circle cx="13" cy="14" r="5" fill="#1e293b" />
          <circle cx="35" cy="14" r="5" fill="#1e293b" />
          <ellipse cx="24" cy="26" rx="14" ry="12.5" fill="#ffffff" stroke="#cbd5e1" strokeWidth="1" />
          <ellipse cx="18" cy="24" rx="4.2" ry="5" transform="rotate(-15 18 24)" fill="#1e293b" />
          <ellipse cx="30" cy="24" rx="4.2" ry="5" transform="rotate(15 30 24)" fill="#1e293b" />
          <circle cx="18.3" cy="23.8" r="1.4" fill="#ffffff" />
          <circle cx="29.7" cy="23.8" r="1.4" fill="#ffffff" />
          <ellipse cx="24" cy="28.5" rx="2.3" ry="1.5" fill="#1e293b" />
          <path d="M21.5 31.5 Q24 33.4 26.5 31.5" fill="none" stroke="#1e293b" strokeWidth="1.4" strokeLinecap="round" />
          <circle cx="13.5" cy="28.5" r="2.2" fill="#fda4af" opacity="0.6" />
          <circle cx="34.5" cy="28.5" r="2.2" fill="#fda4af" opacity="0.6" />
        </g>
      )}

      {avatarId === 'owl' && (
        <g>
          <polygon points="11,15 15,8 20,14" fill="#6d28d9" />
          <polygon points="37,15 33,8 28,14" fill="#6d28d9" />
          <ellipse cx="24" cy="26" rx="13.5" ry="13" fill="#7c3aed" />
          <circle cx="18.2" cy="24" r="5.5" fill="#f5f3ff" />
          <circle cx="29.8" cy="24" r="5.5" fill="#f5f3ff" />
          <circle cx="18.2" cy="24" r="2.4" fill="#1e1b4b" />
          <circle cx="29.8" cy="24" r="2.4" fill="#1e1b4b" />
          <circle cx="19" cy="23" r="0.8" fill="#ffffff" />
          <circle cx="30.6" cy="23" r="0.8" fill="#ffffff" />
          <polygon points="22,27.5 26,27.5 24,31.5" fill="#f59e0b" />
          <path d="M18 34.5 Q24 37.5 30 34.5" fill="none" stroke="#ddd6fe" strokeWidth="1.6" strokeLinecap="round" />
        </g>
      )}

      {avatarId === 'bunny' && (
        <g>
          <ellipse cx="17" cy="12" rx="3.8" ry="9" fill="#f9a8d4" />
          <ellipse cx="31" cy="12" rx="3.8" ry="9" fill="#f9a8d4" />
          <ellipse cx="17" cy="12.5" rx="2" ry="6" fill="#fbcfe8" />
          <ellipse cx="31" cy="12.5" rx="2" ry="6" fill="#fbcfe8" />
          <ellipse cx="24" cy="28" rx="13.5" ry="11.5" fill="#fff1f2" stroke="#f9a8d4" strokeWidth="1.2" />
          <circle cx="18.5" cy="26" r="2" fill="#1e293b" />
          <circle cx="29.5" cy="26" r="2" fill="#1e293b" />
          <circle cx="19.1" cy="25.3" r="0.7" fill="#ffffff" />
          <circle cx="30.1" cy="25.3" r="0.7" fill="#ffffff" />
          <ellipse cx="24" cy="29" rx="2" ry="1.4" fill="#fb7185" />
          <path d="M22 31.5 Q24 33.2 26 31.5" fill="none" stroke="#be123c" strokeWidth="1.3" strokeLinecap="round" />
          <circle cx="14.5" cy="29.5" r="2.2" fill="#fda4af" opacity="0.6" />
          <circle cx="33.5" cy="29.5" r="2.2" fill="#fda4af" opacity="0.6" />
        </g>
      )}

      {avatarId === 'fox' && (
        <g>
          <polygon points="10,20 12,7 22,15" fill="#ea580c" />
          <polygon points="38,20 36,7 26,15" fill="#ea580c" />
          <polygon points="12,17 14,10 19,15" fill="#1e293b" />
          <polygon points="36,17 34,10 29,15" fill="#1e293b" />
          <path d="M10 24 Q24 12 38 24 Q36 37 24 39 Q12 37 10 24 Z" fill="#f97316" />
          <path d="M11 26 Q18 28 24 38.5 Q30 28 37 26 Q35 37 24 39 Q13 37 11 26 Z" fill="#fff7ed" />
          <circle cx="18.5" cy="24.5" r="1.9" fill="#1e293b" />
          <circle cx="29.5" cy="24.5" r="1.9" fill="#1e293b" />
          <circle cx="19" cy="23.9" r="0.6" fill="#ffffff" />
          <circle cx="30" cy="23.9" r="0.6" fill="#ffffff" />
          <circle cx="24" cy="33" r="2" fill="#1e293b" />
        </g>
      )}

      {avatarId === 'penguin' && (
        <g>
          <ellipse cx="24" cy="26" rx="13.5" ry="13.5" fill="#0f172a" />
          <path d="M14 25 Q18 18 24 22 Q30 18 34 25 Q35 38 24 39 Q13 38 14 25 Z" fill="#f8fafc" />
          <circle cx="18.5" cy="24" r="1.9" fill="#0f172a" />
          <circle cx="29.5" cy="24" r="1.9" fill="#0f172a" />
          <circle cx="19" cy="23.3" r="0.6" fill="#ffffff" />
          <circle cx="30" cy="23.3" r="0.6" fill="#ffffff" />
          <polygon points="21.5,27 26.5,27 24,30.5" fill="#f59e0b" />
          <circle cx="14.5" cy="28" r="2" fill="#fda4af" opacity="0.65" />
          <circle cx="33.5" cy="28" r="2" fill="#fda4af" opacity="0.65" />
        </g>
      )}

      {avatarId === 'mecha' && (
        <g>
          <line x1="24" y1="6" x2="24" y2="12" stroke="#4f46e5" strokeWidth="2.5" strokeLinecap="round" />
          <circle cx="24" cy="6" r="2.2" fill="#06b6d4" />
          <rect x="11" y="12" width="26" height="24" rx="7" fill="#4338ca" />
          <rect x="8" y="20" width="3.5" height="9" rx="1.5" fill="#6366f1" />
          <rect x="36.5" y="20" width="3.5" height="9" rx="1.5" fill="#6366f1" />
          <rect x="14.5" y="17.5" width="19" height="9.5" rx="3.5" fill="#0f172a" />
          <circle cx="19.5" cy="22.2" r="2.1" fill="#22d3ee" />
          <circle cx="28.5" cy="22.2" r="2.1" fill="#22d3ee" />
          <rect x="17" y="30" width="14" height="2.6" rx="1.3" fill="#a5b4fc" />
        </g>
      )}
    </svg>
  )
}

export function AgentAvatarBadge({
  agentKey,
  avatarId,
  ownerUsername,
  deviceKind,
  onlineStatus,
  size = 'md',
  showStatusDot = true,
  showDeviceCorner = true,
  className,
}: {
  agentKey?: Partial<AgentKeyItem>
  avatarId?: AgentAvatarId
  ownerUsername?: string
  deviceKind?: AgentDeviceKind
  onlineStatus?: string
  size?: 'xs' | 'sm' | 'md' | 'lg'
  showStatusDot?: boolean
  showDeviceCorner?: boolean
  className?: string
}) {
  const resolvedAvatar = avatarId || resolveAvatarId(agentKey)
  const resolvedOwner = ownerUsername || agentKey?.owner_username || 'admin'
  const resolvedDevice = deviceKind || agentKey?.device_kind || 'laptop'
  const ringHex = ownerRingColor(resolvedOwner)

  const boxSize =
    size === 'xs'
      ? 'h-6 w-6'
      : size === 'sm'
      ? 'h-8 w-8'
      : size === 'lg'
      ? 'h-12 w-12'
      : 'h-10 w-10'

  const cornerSize =
    size === 'lg' ? 'h-5 w-5 -bottom-0.5 -right-0.5' : 'h-4 w-4 -bottom-0.5 -right-0.5'

  const dotColor =
    onlineStatus === 'working'
      ? 'bg-emerald-500 animate-pulse'
      : onlineStatus === 'watching'
      ? 'bg-cyan-500'
      : onlineStatus === 'online'
      ? 'bg-emerald-500'
      : 'bg-slate-400'

  return (
    <div
      className={cn('relative inline-flex shrink-0 items-center justify-center', boxSize, className)}
      title={`归属：${resolvedOwner} · 设备：${deviceKindShortLabel(resolvedDevice)}`}
    >
      <div
        className="h-full w-full overflow-hidden rounded-full bg-card p-0.5 shadow-xs"
        style={{
          boxShadow: `0 0 0 2.5px ${ringHex}`,
        }}
      >
        <AgentMascotSvg avatarId={resolvedAvatar} className="h-full w-full rounded-full" />
      </div>

      {showDeviceCorner && size !== 'xs' && (
        <span
          className={cn(
            'absolute flex items-center justify-center rounded-full border border-border bg-card text-foreground shadow-xs',
            cornerSize
          )}
        >
          <DeviceMiniIcon kind={resolvedDevice} className="h-2.5 w-2.5" />
        </span>
      )}

      {showStatusDot && onlineStatus && (
        <span
          className={cn(
            'absolute -bottom-0.5 -left-0.5 h-2.5 w-2.5 rounded-full border-2 border-card',
            dotColor
          )}
        />
      )}
    </div>
  )
}

export function AgentProfileModal({
  open,
  agentKey,
  onClose,
  onSaved,
}: {
  open: boolean
  agentKey: AgentKeyItem | null
  onClose: () => void
  onSaved: (updated: AgentKeyItem) => void
}) {
  const { toast } = useToast()
  const [avatarId, setAvatarId] = useState<AgentAvatarId>('shiba')
  const [displayAlias, setDisplayAlias] = useState('')
  const [deviceAlias, setDeviceAlias] = useState('')
  const [deviceKind, setDeviceKind] = useState<AgentDeviceKind>('laptop')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!agentKey) return
    setAvatarId(resolveAvatarId(agentKey))
    setDisplayAlias(agentKey.display_alias || '')
    setDeviceAlias(agentKey.device_alias || '')
    setDeviceKind((agentKey.device_kind as AgentDeviceKind) || 'laptop')
  }, [agentKey])

  if (!agentKey) return null

  const ownerColor = ownerRingColor(agentKey.owner_username)

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      const res = await api.updateAgentKey(agentKey.id, {
        avatar_id: avatarId,
        display_alias: displayAlias.trim(),
        device_alias: deviceAlias.trim(),
        device_kind: deviceKind,
      })
      toast('Agent 卡通工牌已保存并广播同步', 'success')
      window.dispatchEvent(
        new CustomEvent('lfh:agent-event', {
          detail: { kind: 'agent_profile', actor_name: res.key.name, node_name: res.key.id },
        })
      )
      onSaved(res.key)
      onClose()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存工牌失败', 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="md"
      title={`定制 AI 卡通工牌 — ${agentDisplayName(agentKey)}`}
      description="外圈颜色固定绑定归属成员，右下角徽章代表运行设备类型，看板中支持 @别名 直派"
    >
      <form onSubmit={handleSave} className="space-y-4 text-xs">
        <div className="flex items-center gap-4 rounded-xl border border-border bg-muted/30 p-3.5">
          <AgentAvatarBadge
            agentKey={agentKey}
            avatarId={avatarId}
            deviceKind={deviceKind}
            size="lg"
            onlineStatus="online"
          />
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-sm font-semibold">
                {displayAlias.trim() || agentKey.name}
              </span>
              <Badge variant="outline">归属：{agentKey.owner_username}</Badge>
              <span
                className="inline-flex items-center gap-1 rounded-md border border-border px-1.5 py-0.5 text-[10px]"
                style={{ color: ownerColor }}
              >
                <span className="h-2 w-2 rounded-full" style={{ backgroundColor: ownerColor }} />
                <span>成员身份环</span>
              </span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              原始密钥：{agentKey.name} · 设备标签：{deviceAlias.trim() || deviceKindShortLabel(deviceKind)}
            </div>
          </div>
        </div>

        <div className="space-y-1.5">
          <div className="font-medium">选择 8 款内置卡通形象</div>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
            {AGENT_AVATARS.map((item) => {
              const selected = avatarId === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => setAvatarId(item.id)}
                  className={cn(
                    'flex items-center gap-2 rounded-lg border p-2 text-left transition-colors cursor-pointer',
                    selected
                      ? 'border-primary bg-primary/10'
                      : 'border-border hover:bg-muted/40'
                  )}
                >
                  <AgentMascotSvg avatarId={item.id} className="h-8 w-8 shrink-0 rounded-full" />
                  <div className="min-w-0">
                    <div className="font-medium truncate">{item.label}</div>
                    <div className="text-[10px] text-muted-foreground truncate">{item.roleHint}</div>
                  </div>
                </button>
              )
            })}
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <label className="font-medium">工牌昵称（用于 @ 指派）</label>
            <Input
              value={displayAlias}
              onChange={(e) => setDisplayAlias(e.target.value)}
              placeholder={`留空则显示 ${agentKey.name}`}
            />
          </div>
          <div className="space-y-1">
            <label className="font-medium">所在电脑标签</label>
            <Input
              value={deviceAlias}
              onChange={(e) => setDeviceAlias(e.target.value)}
              placeholder="如：主控台式机 / 出差 Mac"
            />
          </div>
        </div>

        <div className="space-y-1.5">
          <div className="font-medium">设备角标类型</div>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
            {DEVICE_KIND_OPTIONS.map((opt) => {
              const active = deviceKind === opt.id
              return (
                <button
                  key={opt.id}
                  type="button"
                  onClick={() => setDeviceKind(opt.id)}
                  className={cn(
                    'flex items-center gap-2 rounded-lg border px-2.5 py-2 text-left transition-colors cursor-pointer',
                    active ? 'border-primary bg-primary/10' : 'border-border hover:bg-muted/40'
                  )}
                >
                  <DeviceMiniIcon kind={opt.id} className="h-3.5 w-3.5 shrink-0 text-primary" />
                  <span className="truncate font-medium">{opt.label}</span>
                </button>
              )
            })}
          </div>
        </div>

        <div className="flex justify-end gap-2 pt-2 border-t border-border">
          <Button type="button" size="sm" variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" size="sm" disabled={saving}>
            {saving ? '保存中...' : '保存卡通工牌'}
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
