import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  CheckCircle2,
  Cpu,
  Download,
  Gauge,
  Play,
  Power,
  RefreshCw,
  Sparkles,
  Trash2,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn, formatSize, formatTime } from '@/lib/utils'
import type {
  ClusterCapabilityView,
  MarketplaceItem,
  NodeStatus,
  PluginJob,
  PluginRecommendation,
} from '@/types'
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Select,
  useToast,
} from '@/components/ui'

interface PluginsPageProps {
  onPoolUpdated: () => void
  onDownloadOutput: (fileId: string, fileName: string) => void
}

function fitLevelMeta(level: string) {
  switch (level) {
    case 'recommended':
      return { label: '性能充沛 · 推荐部署', variant: 'success' as const }
    case 'compatible':
      return { label: '硬件满足 · 可运行', variant: 'default' as const }
    default:
      return { label: '资源较紧张 · 谨慎部署', variant: 'warning' as const }
  }
}

export function PluginsPage({ onPoolUpdated, onDownloadOutput }: PluginsPageProps) {
  const { toast } = useToast()
  const [marketplace, setMarketplace] = useState<MarketplaceItem[]>([])
  const [recommendations, setRecommendations] = useState<PluginRecommendation[]>([])
  const [capabilities, setCapabilities] = useState<ClusterCapabilityView[]>([])
  const [nodes, setNodes] = useState<NodeStatus[]>([])
  const [jobs, setJobs] = useState<PluginJob[]>([])
  const [loading, setLoading] = useState(true)
  const [busyKey, setBusyKey] = useState('')
  const [selectedTargets, setSelectedTargets] = useState<Record<string, string>>({})

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [mktRes, jobsRes] = await Promise.all([api.getAdminMarketplace(), api.getPluginJobs()])
      setMarketplace(mktRes.marketplace || [])
      setRecommendations(mktRes.recommendations || [])
      setCapabilities(mktRes.capabilities || [])
      setNodes(mktRes.nodes || [])
      setJobs(jobsRes.jobs || [])
    } catch (err) {
      toast(err instanceof Error ? err.message : '加载算力市场数据失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast])

  useEffect(() => {
    void loadData()
  }, [loadData])

  const onlineNodes = useMemo(() => nodes.filter((n) => n.online), [nodes])

  const recMap = useMemo(() => {
    const m = new Map<string, PluginRecommendation>()
    for (const r of recommendations) m.set(r.plugin_id, r)
    return m
  }, [recommendations])

  const handleInstall = async (nodeId: string, pluginId: string, nodeName?: string) => {
    const key = `install:${nodeId}:${pluginId}`
    setBusyKey(key)
    try {
      await api.installPlugin(nodeId, pluginId)
      toast(`已在 ${nodeName || nodeId} 安装并启用 ${pluginId}`, 'success')
      await loadData()
      onPoolUpdated()
    } catch (err) {
      toast(err instanceof Error ? err.message : '安装失败', 'error')
    } finally {
      setBusyKey('')
    }
  }

  const handleToggle = async (nodeId: string, pluginId: string, enabled: boolean) => {
    const key = `toggle:${nodeId}:${pluginId}`
    setBusyKey(key)
    try {
      await api.togglePlugin(nodeId, pluginId, enabled)
      toast(`${pluginId} 已${enabled ? '启用' : '停用'}`, 'success')
      await loadData()
      onPoolUpdated()
    } catch (err) {
      toast(err instanceof Error ? err.message : '操作失败', 'error')
    } finally {
      setBusyKey('')
    }
  }

  const handleUninstall = async (nodeId: string, pluginId: string) => {
    const key = `del:${nodeId}:${pluginId}`
    setBusyKey(key)
    try {
      await api.uninstallPlugin(nodeId, pluginId)
      toast(`已从节点卸载 ${pluginId}`, 'success')
      await loadData()
      onPoolUpdated()
    } catch (err) {
      toast(err instanceof Error ? err.message : '卸载失败', 'error')
    } finally {
      setBusyKey('')
    }
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg sm:text-xl font-semibold tracking-tight">分布式算力与插件中心</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            自动评估全集群节点 CPU、可用内存、GPU 与磁盘空间，智能推荐最佳部署节点并统一调度任务
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={loadData} disabled={loading}>
          <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          <span>刷新硬件与插件状态</span>
        </Button>
      </div>

      {/* Node Hardware Overview Strip */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {nodes.map((n) => (
          <Card key={n.node_id} className="p-3.5 space-y-2">
            <div className="flex items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">
                  {n.node_name} {n.is_self && <span className="text-primary text-xs">(本机)</span>}
                </div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {n.cpu_model || `${n.os}/${n.arch}`} · {n.cpu_cores || 1} 核
                </div>
              </div>
              <Badge variant={n.online ? 'success' : 'secondary'}>
                {n.online ? (n.role === 'entry' ? '主节点在线' : '工作节点在线') : '离线'}
              </Badge>
            </div>
            <div className="grid grid-cols-3 gap-2 rounded-lg bg-muted/40 p-2 text-[11px]">
              <div>
                <div className="text-muted-foreground">CPU 负载</div>
                <div className="font-semibold tabular-nums">{Math.round(n.cpu_percent || 0)}%</div>
              </div>
              <div>
                <div className="text-muted-foreground">剩余内存</div>
                <div className="font-semibold tabular-nums">
                  {formatSize(Math.max(0, (n.mem_total_bytes || 0) - (n.mem_used_bytes || 0)))}
                </div>
              </div>
              <div>
                <div className="text-muted-foreground">内网速率</div>
                <div className="font-semibold tabular-nums text-primary">
                  {n.lan_bandwidth_mbps && n.lan_bandwidth_mbps > 0
                    ? `${n.lan_bandwidth_mbps.toFixed(0)} Mbps`
                    : n.lan_link_mbps && n.lan_link_mbps > 0
                    ? `${n.lan_link_mbps}M 网卡`
                    : '待探测'}
                </div>
              </div>
            </div>
            {n.gpu_name && (
              <div className="text-[11px] text-muted-foreground truncate">
                GPU 加速：{n.gpu_name}
              </div>
            )}
          </Card>
        ))}
      </div>

      {/* Marketplace + Smart Recommendations */}
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <Sparkles className="h-4 w-4 text-primary" />
          <h2 className="text-sm font-semibold">插件市场与硬件智能推荐</h2>
        </div>

        <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
          {marketplace.map((item) => {
            const rec = recMap.get(item.id)
            const fits = rec?.node_fits || []
            const bestFit = fits.find((f) => f.node_id === rec?.best_node_id) || fits[0]
            const defaultTarget = selectedTargets[item.id] || rec?.best_node_id || onlineNodes[0]?.node_id || ''
            const levelMeta = fitLevelMeta(rec?.best_level || bestFit?.level || 'compatible')

            return (
              <Card key={item.id} className="flex flex-col justify-between">
                <CardHeader className="pb-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <CardTitle className="text-base truncate">{item.name}</CardTitle>
                      <div className="text-[11px] text-muted-foreground mt-1 font-mono">
                        {item.id} · v{item.version} · {item.category}
                      </div>
                    </div>
                    <Badge variant="outline">{item.runtime}</Badge>
                  </div>
                  <CardDescription className="pt-1.5">{item.description}</CardDescription>
                </CardHeader>

                <CardContent className="space-y-3">
                  {/* Capabilities & Supported Exts */}
                  <div className="flex flex-wrap gap-1">
                    {item.capabilities.map((cap) => (
                      <Badge key={cap} variant="default" className="font-mono">
                        {cap}
                      </Badge>
                    ))}
                    {item.supported_exts.map((ext) => (
                      <Badge key={ext} variant="outline" className="font-mono">
                        {ext}
                      </Badge>
                    ))}
                  </div>

                  {/* Requirements */}
                  <div className="text-[11px] text-muted-foreground leading-relaxed">
                    依赖与资源：{item.requirements}
                  </div>

                  {/* Smart Recommendation Box */}
                  {rec && (
                    <div className="rounded-lg border border-primary/25 bg-primary/5 p-3 space-y-2 text-xs">
                      <div className="flex items-center justify-between gap-2">
                        <span className="inline-flex items-center gap-1.5 font-semibold text-primary">
                          <Sparkles className="h-3.5 w-3.5" />
                          智能推荐节点：{rec.best_node_name || '暂无在线节点'}
                        </span>
                        <Badge variant={levelMeta.variant} className="tabular-nums">
                          {rec.best_score} 分
                        </Badge>
                      </div>
                      <div className="text-[11px] text-muted-foreground leading-snug">
                        {rec.best_reason}
                      </div>

                      {/* Per-node hardware fit status + installed toggle */}
                      <div className="space-y-1.5 pt-1">
                        {fits.map((fit) => (
                          <div
                            key={fit.node_id}
                            className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-card px-2.5 py-1.5 text-[11px]"
                          >
                            <div className="min-w-0">
                              <div className="flex items-center gap-1.5 font-medium">
                                <span className="truncate">{fit.node_name}</span>
                                <span className="text-muted-foreground tabular-nums">
                                  ({fit.score}分 · {fit.cpu_cores}C / 余{formatSize(fit.mem_free_bytes)}
                                  {fit.has_gpu ? ' / GPU' : ''})
                                </span>
                              </div>
                            </div>
                            <div className="flex items-center gap-1 shrink-0">
                              {fit.installed ? (
                                <>
                                  <Button
                                    size="xs"
                                    variant={fit.enabled ? 'secondary' : 'outline'}
                                    disabled={busyKey === `toggle:${fit.node_id}:${item.id}`}
                                    onClick={() => handleToggle(fit.node_id, item.id, !fit.enabled)}
                                  >
                                    <Power className="h-3 w-3" />
                                    <span>{fit.enabled ? '已启用' : '已停用'}</span>
                                  </Button>
                                  <Button
                                    size="xs"
                                    variant="ghost"
                                    className="text-destructive"
                                    disabled={busyKey === `del:${fit.node_id}:${item.id}`}
                                    onClick={() => handleUninstall(fit.node_id, item.id)}
                                    title="卸载"
                                  >
                                    <Trash2 className="h-3 w-3" />
                                  </Button>
                                </>
                              ) : (
                                <Button
                                  size="xs"
                                  variant={fit.node_id === rec.best_node_id ? 'default' : 'outline'}
                                  disabled={!fit.online || !fit.can_install || busyKey === `install:${fit.node_id}:${item.id}`}
                                  onClick={() => handleInstall(fit.node_id, item.id, fit.node_name)}
                                >
                                  <Play className="h-3 w-3" />
                                  <span>{fit.online ? '安装' : '离线'}</span>
                                </Button>
                              )}
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Quick Target Selector Fallback */}
                  {onlineNodes.length > 0 && defaultTarget && (
                    <div className="flex items-center gap-2 pt-1 border-t border-border">
                      <Select
                        value={defaultTarget}
                        onChange={(e) =>
                          setSelectedTargets((prev) => ({ ...prev, [item.id]: e.target.value }))
                        }
                        className="h-8 flex-1 text-xs"
                      >
                        {onlineNodes.map((n) => (
                          <option key={n.node_id} value={n.node_id}>
                            部署到 {n.node_name}
                          </option>
                        ))}
                      </Select>
                      <Button
                        size="xs"
                        disabled={busyKey === `install:${defaultTarget}:${item.id}`}
                        onClick={() => {
                          const targetNodeObj = onlineNodes.find((n) => n.node_id === defaultTarget)
                          void handleInstall(defaultTarget, item.id, targetNodeObj?.node_name)
                        }}
                      >
                        {busyKey === `install:${defaultTarget}:${item.id}` ? '部署中...' : '一键安装'}
                      </Button>
                    </div>
                  )}
                </CardContent>
              </Card>
            )
          })}
        </div>
      </div>

      {/* Cluster Capability Matrix */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Cpu className="h-4 w-4 text-primary" />
            <span>全集群算力能力矩阵</span>
          </CardTitle>
          <CardDescription>实时汇总在线节点已注册且启用的能力，任务优先调度到本地有数据的空闲节点</CardDescription>
        </CardHeader>
        <CardContent>
          {capabilities.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
              暂无已启用的集群能力，请在上方市场选择硬件契合度最高的节点安装。
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {capabilities.map((cap) => (
                <div key={`${cap.plugin_id}:${cap.capability}`} className="rounded-lg border border-border p-3 space-y-2 text-xs">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-semibold">{cap.name}</span>
                    <Badge variant="default" className="font-mono">
                      {cap.capability}
                    </Badge>
                  </div>
                  <div className="space-y-1">
                    {cap.providers.map((p) => (
                      <div key={p.node_id} className="flex items-center justify-between rounded bg-muted/40 px-2 py-1 text-[11px]">
                        <span className="truncate font-medium">
                          {p.node_name} {p.is_self && '(本机)'}
                        </span>
                        <span className="text-muted-foreground tabular-nums">
                          并发 {p.busy_jobs}/{p.max_concurrency} · CPU {Math.round(p.cpu_percent)}%
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Compute Jobs Table */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Gauge className="h-4 w-4 text-primary" />
            <span>分布式算力任务记录 ({jobs.length})</span>
          </CardTitle>
          <CardDescription>查看从文件详情面板触发的插件执行节点、耗时与生成结果</CardDescription>
        </CardHeader>
        <CardContent>
          {jobs.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
              暂无任务记录。可在“文件中心”点击任意支持格式（如图片/PDF）右侧详情面板触发算力处理。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-border text-muted-foreground">
                  <tr>
                    <th className="py-2 pr-3 font-medium">源文件</th>
                    <th className="py-2 pr-3 font-medium">能力 / 插件</th>
                    <th className="py-2 pr-3 font-medium">执行节点</th>
                    <th className="py-2 pr-3 font-medium">状态</th>
                    <th className="py-2 pr-3 font-medium">结果摘要</th>
                    <th className="py-2 pr-3 font-medium">耗时</th>
                    <th className="py-2 text-right font-medium">产物</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {jobs.map((job) => (
                    <tr key={job.id}>
                      <td className="py-2.5 pr-3 max-w-[180px] truncate font-medium">{job.source_file_name}</td>
                      <td className="py-2.5 pr-3 font-mono text-[11px]">{job.capability}</td>
                      <td className="py-2.5 pr-3">{job.compute_node_name || job.compute_node_id}</td>
                      <td className="py-2.5 pr-3">
                        <Badge
                          variant={
                            job.status === 'done'
                              ? 'success'
                              : job.status === 'error'
                              ? 'destructive'
                              : 'warning'
                          }
                        >
                          {job.status === 'done' && <CheckCircle2 className="h-3 w-3" />}
                          {job.status}
                        </Badge>
                      </td>
                      <td className="py-2.5 pr-3 max-w-[240px] truncate text-muted-foreground" title={job.summary || job.error}>
                        {job.error || job.summary || '-'}
                      </td>
                      <td className="py-2.5 pr-3 tabular-nums text-muted-foreground">{job.duration_ms} ms</td>
                      <td className="py-2.5 text-right whitespace-nowrap">
                        {job.output_file_id ? (
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={() =>
                              onDownloadOutput(job.output_file_id!, job.output_file_name || 'output')
                            }
                          >
                            <Download className="h-3 w-3" />
                            <span>{job.output_file_name || '下载产物'}</span>
                          </Button>
                        ) : (
                          <span className="text-muted-foreground">{formatTime(job.created_at)}</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
