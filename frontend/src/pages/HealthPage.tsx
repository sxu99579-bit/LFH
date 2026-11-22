import { useState } from 'react'
import {
  Activity,
  Cpu,
  Files,
  Gauge,
  HardDrive,
  Network,
  RefreshCw,
  Server,
  ShieldAlert,
  ShieldCheck,
  Wifi,
} from 'lucide-react'
import { api } from '@/lib/api'
import {
  cn,
  formatQuotaDisplay,
  formatRate,
  formatRelativeTime,
  formatSize,
} from '@/lib/utils'
import type { BandwidthProbeResult, PoolSummary, User } from '@/types'
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, useToast } from '@/components/ui'

interface HealthPageProps {
  user: User
  pool: PoolSummary | null
  onRefresh: () => Promise<void> | void
}

function MeterBar({ label, percent, detail, tone = 'primary' }: { label: string; percent: number; detail: string; tone?: 'primary' | 'warn' | 'auto' }) {
  const pct = Math.max(0, Math.min(100, Math.round(percent || 0)))
  const barColor =
    tone === 'warn' || (tone === 'auto' && pct >= 90)
      ? 'bg-destructive'
      : tone === 'auto' && pct >= 75
      ? 'bg-amber-500'
      : 'bg-primary'
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-[11px]">
        <span className="text-muted-foreground">{label}</span>
        <span className="tabular-nums font-medium">{detail} ({pct}%)</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div className={cn('h-full rounded-full transition-all', barColor)} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

export function HealthPage({ user, pool, onRefresh }: HealthPageProps) {
  const { toast } = useToast()
  const isAdmin = user.role === 'admin'
  const [probing, setProbing] = useState(false)
  const [syncingAll, setSyncingAll] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [lastProbeResults, setLastProbeResults] = useState<BandwidthProbeResult[]>([])

  const nodes = pool?.nodes || []
  const usedBytes = pool?.total_used_bytes || 0
  const quotaBytes = pool?.total_quota_bytes || 0
  const poolPct = quotaBytes > 0 ? Math.min(100, Math.round((usedBytes * 100) / quotaBytes)) : 0

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await onRefresh()
    } finally {
      setRefreshing(false)
    }
  }

  const handleProbeBandwidth = async () => {
    setProbing(true)
    try {
      const res = await api.probeBandwidth()
      setLastProbeResults(res.results || [])
      toast('已完成全集群内网带宽探测', 'success')
      await onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '内网带宽探测失败', 'error')
    } finally {
      setProbing(false)
    }
  }

  const handleSyncClusterVersions = async () => {
    setSyncingAll(true)
    try {
      const res = await api.upgradeNodes({ node_id: 'all' })
      toast(`已向工作节点下发同步指令（成功 ${res.synced ?? 0}/${res.total_targets ?? 0}）`, 'success')
      await onRefresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '同步版本失败', 'error')
    } finally {
      setSyncingAll(false)
    }
  }

  return (
    <div className="space-y-6">
      {/* Header & Controls */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg sm:text-xl font-semibold tracking-tight">节点健康与内网带宽监控</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            每 3 秒自动刷新全集群资源遥测、物理网卡协商速率、主动测速吞吐、硬件配置与磁盘 I/O 健康度
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {isAdmin && (
            <Button size="sm" onClick={handleProbeBandwidth} disabled={probing}>
              <Gauge className={cn('h-3.5 w-3.5', probing && 'animate-spin')} />
              <span>{probing ? '正在测速...' : '探测内网带宽'}</span>
            </Button>
          )}
          {isAdmin && (
            <Button size="sm" variant="outline" onClick={handleSyncClusterVersions} disabled={syncingAll}>
              <RefreshCw className={cn('h-3.5 w-3.5', syncingAll && 'animate-spin')} />
              <span>{syncingAll ? '同步中...' : '全集群版本同步'}</span>
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={handleRefresh} disabled={refreshing}>
            <RefreshCw className={cn('h-3.5 w-3.5', refreshing && 'animate-spin')} />
            <span>立即刷新</span>
          </Button>
        </div>
      </div>

      {/* KPI Strip */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="p-4 space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>在线节点状态</span>
            <Server className="h-4 w-4 text-primary" />
          </div>
          <div className="text-2xl font-bold tabular-nums">
            {pool?.online_nodes ?? 0} / {pool?.total_nodes ?? 0}
          </div>
          <div className="text-[11px] text-muted-foreground">
            入口地址：{pool?.entry_url || '-'}
          </div>
        </Card>

        <Card className="p-4 space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>集群存储配额占用</span>
            <HardDrive className="h-4 w-4 text-primary" />
          </div>
          <div className="text-2xl font-bold tabular-nums">
            {formatQuotaDisplay(usedBytes)}
            <span className="text-sm font-normal text-muted-foreground">
              {' '}
              / {quotaBytes > 0 ? formatQuotaDisplay(quotaBytes) : '-'}
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary" style={{ width: `${poolPct}%` }} />
          </div>
        </Card>

        <Card className="p-4 space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>集群托管文件总量</span>
            <Files className="h-4 w-4 text-primary" />
          </div>
          <div className="text-2xl font-bold tabular-nums">{pool?.total_file_count ?? 0}</div>
          <div className="text-[11px] text-muted-foreground">
            剩余可用配额：{formatQuotaDisplay(pool?.total_free_bytes ?? 0)}
          </div>
        </Card>

        <Card className="p-4 space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>全网版本一致性</span>
            {pool?.version_consistent === false ? (
              <ShieldAlert className="h-4 w-4 text-amber-500" />
            ) : (
              <ShieldCheck className="h-4 w-4 text-emerald-500" />
            )}
          </div>
          <div className="flex items-center gap-2 pt-0.5">
            <Badge variant={pool?.version_consistent === false ? 'warning' : 'success'}>
              {pool?.version_consistent === false ? '检测到版本差异' : '全集群版本一致'}
            </Badge>
            <span className="text-xs font-mono text-muted-foreground">{pool?.entry_version || '-'}</span>
          </div>
          <div className="text-[11px] text-muted-foreground truncate">
            {pool?.version_consistent === false
              ? `差异节点：${pool?.mismatched_nodes?.join(', ') || '-'}`
              : `全网最高版本：${pool?.highest_node_version || pool?.entry_version || '-'}`}
          </div>
        </Card>
      </div>

      {/* Latest Probe Quick Summary */}
      {lastProbeResults.length > 0 && (
        <Card className="p-4 border-primary/30 bg-primary/5">
          <div className="flex items-center gap-2 text-xs font-semibold text-primary mb-2">
            <Wifi className="h-4 w-4" />
            <span>本次主动内网带宽探测结果</span>
          </div>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 text-xs">
            {lastProbeResults.map((r) => (
              <div key={r.node_id} className="flex items-center justify-between rounded-lg border border-border bg-card px-3 py-2">
                <div>
                  <div className="font-medium">{r.node_name}</div>
                  <div className="text-[11px] text-muted-foreground">
                    {r.ok ? `${formatSize(r.bytes)} · ${r.duration_ms}ms` : r.error || '探测失败'}
                  </div>
                </div>
                <div className="text-right tabular-nums">
                  <div className="font-semibold text-primary">
                    {r.ok ? `${r.bandwidth_mbps.toFixed(1)} Mbps` : '-'}
                  </div>
                  <div className="text-[10px] text-muted-foreground">
                    {r.ok ? `${(r.bandwidth_mbps / 8).toFixed(1)} MB/s` : ''}
                  </div>
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Per-Node Detailed Telemetry Cards */}
      <div className="grid gap-4 lg:grid-cols-2">
        {nodes.map((node) => {
          const quotaPct = node.quota_bytes > 0 ? Math.min(100, (node.used_bytes * 100) / node.quota_bytes) : 0
          const hasProbedBw = (node.lan_bandwidth_mbps || 0) > 0
          const hasLinkMbps = (node.lan_link_mbps || 0) > 0
          const mbPerSec = hasProbedBw ? (node.lan_bandwidth_mbps! / 8).toFixed(1) : ''
          const enabledPlugins = (node.plugins || []).filter((p) => p.enabled)

          return (
            <Card key={node.node_id} className={cn(!node.online && 'opacity-75')}>
              <CardHeader className="pb-3">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div className="min-w-0">
                    <CardTitle className="flex items-center gap-2 text-base">
                      <span className="truncate">{node.node_name}</span>
                      {node.is_self && <Badge variant="default">本机</Badge>}
                      <Badge variant={node.role === 'entry' ? 'default' : 'outline'}>
                        {node.role === 'entry' ? '入口节点' : '工作节点'}
                      </Badge>
                    </CardTitle>
                    <CardDescription className="mt-1 font-mono text-[11px]">
                      {node.os}/{node.arch} · {node.version || '-'} · {node.http_url}
                    </CardDescription>
                  </div>
                  <Badge variant={node.online ? 'success' : 'destructive'}>
                    {node.online ? '在线' : `离线 (${formatRelativeTime(node.last_seen)})`}
                  </Badge>
                </div>
              </CardHeader>

              <CardContent className="space-y-4">
                {/* Highlighted LAN Bandwidth Telemetry Box */}
                <div className="rounded-xl border border-primary/25 bg-primary/5 p-3 flex flex-wrap items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <div className="rounded-lg bg-primary/15 p-2 text-primary">
                      <Network className="h-4.5 w-4.5" />
                    </div>
                    <div>
                      <div className="text-xs font-semibold">内网通信带宽与网卡速率</div>
                      <div className="text-[11px] text-muted-foreground">
                        {node.bandwidth_probed_at
                          ? `最近探测：${formatRelativeTime(node.bandwidth_probed_at)}`
                          : '点击右上角“探测内网带宽”执行 HTTP 吞吐实测'}
                      </div>
                    </div>
                  </div>
                  <div className="text-right tabular-nums">
                    <div className="text-base font-bold text-primary">
                      {hasProbedBw ? `${node.lan_bandwidth_mbps!.toFixed(1)} Mbps` : hasLinkMbps ? `${node.lan_link_mbps} Mbps 网卡` : '未测速'}
                    </div>
                    <div className="text-[11px] text-muted-foreground">
                      {hasProbedBw ? `约 ${mbPerSec} MB/s` : ''}
                      {hasProbedBw && hasLinkMbps ? ' · ' : ''}
                      {hasLinkMbps ? `物理链路 ${node.lan_link_mbps} Mbps` : ''}
                    </div>
                  </div>
                </div>

                {/* Resource Meters */}
                <div className="grid gap-3 sm:grid-cols-2">
                  <MeterBar
                    label={`CPU (${node.cpu_cores || 1} 核)`}
                    percent={node.cpu_percent}
                    detail={`${Math.round(node.cpu_percent || 0)}%`}
                    tone="auto"
                  />
                  <MeterBar
                    label="内存占用"
                    percent={node.mem_percent}
                    detail={`${formatSize(node.mem_used_bytes)} / ${formatSize(node.mem_total_bytes)}`}
                    tone="auto"
                  />
                  <MeterBar
                    label="所在磁盘占用"
                    percent={node.disk_percent}
                    detail={`${formatSize(node.disk_used_bytes)} / ${formatSize(node.disk_total_bytes)}`}
                    tone="auto"
                  />
                  <MeterBar
                    label={`共享池配额 (${node.file_count} 文件)`}
                    percent={quotaPct}
                    detail={`${formatQuotaDisplay(node.used_bytes)} / ${formatQuotaDisplay(node.quota_bytes)}`}
                    tone="auto"
                  />
                </div>

                {/* Hardware & Traffic Details Grid */}
                <div className="grid gap-2 rounded-lg border border-border bg-muted/25 p-3 text-[11px] sm:grid-cols-2">
                  <div className="flex items-center gap-1.5 truncate">
                    <Cpu className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                    <span className="truncate" title={node.cpu_model}>
                      CPU：{node.cpu_model || `${node.os}/${node.arch}`} ({node.cpu_cores || 1}C)
                    </span>
                  </div>
                  <div className="flex items-center gap-1.5 truncate">
                    <Activity className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                    <span className="truncate" title={node.gpu_name}>
                      GPU：{node.gpu_name || (node.has_gpu ? '可用' : '未检测到独立显卡')}
                    </span>
                  </div>
                  <div>
                    实时速率：上传 {formatRate(node.upload_bps)} / 下载 {formatRate(node.download_bps)}
                  </div>
                  <div>
                    延迟：网络 {node.net_latency_ms}ms · 心跳 {node.heartbeat_latency_ms}ms
                  </div>
                  <div>
                    磁盘 I/O 错误：读 {node.io_read_errors} / 写 {node.io_write_errors}
                  </div>
                  <div>
                    零拷贝：{node.zero_copy_count ?? 0} 项 ({formatSize(node.zero_copy_bytes)}) · 挂载 {node.mount_count ?? 0} 个目录
                  </div>
                </div>

                {/* Enabled Plugins Pills */}
                <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                  <span className="text-muted-foreground">已启用算力插件：</span>
                  {enabledPlugins.length === 0 ? (
                    <span className="text-muted-foreground">无</span>
                  ) : (
                    enabledPlugins.map((p) => (
                      <Badge key={p.plugin_id} variant="secondary">
                        {p.name} ({p.busy_jobs}/{p.max_concurrency})
                      </Badge>
                    ))
                  )}
                </div>
              </CardContent>
            </Card>
          )
        })}
      </div>
    </div>
  )
}
