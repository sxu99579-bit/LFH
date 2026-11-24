import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
import type { ClusterCapabilityView, FileRecord } from '@/types'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatSize(bytes?: number): string {
  const b = Number(bytes || 0)
  if (!b || b <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let idx = 0
  let v = b
  while (v >= 1024 && idx < units.length - 1) {
    v /= 1024
    idx++
  }
  return `${v >= 100 || idx === 0 ? v.toFixed(idx === 0 ? 0 : 1) : v.toFixed(2)} ${units[idx]}`
}

export function formatQuotaDisplay(bytes?: number): string {
  const b = Number(bytes || 0)
  if (b <= 0) return '0 B'
  const gb = b / (1024 * 1024 * 1024)
  if (gb >= 1) return `${Number(gb.toFixed(2))} GB`
  return formatSize(b)
}

export function formatRate(bps?: number): string {
  const v = Number(bps || 0)
  if (v <= 0) return '0 B/s'
  return `${formatSize(v)}/s`
}

export function formatTime(iso?: string): string {
  if (!iso || iso.startsWith('0001-')) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function formatRelativeTime(iso?: string): string {
  if (!iso || iso.startsWith('0001-')) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '-'
  const diffSec = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000))
  if (diffSec < 10) return '刚刚'
  if (diffSec < 60) return `${diffSec} 秒前`
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)} 分钟前`
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)} 小时前`
  return `${Math.floor(diffSec / 86400)} 天前`
}

export function compareVersions(a?: string, b?: string): number {
  const parse = (s?: string) =>
    String(s || '')
      .trim()
      .replace(/^[vV]/, '')
      .split('.')
      .map((x) => parseInt(x, 10) || 0)
  const pa = parse(a)
  const pb = parse(b)
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const va = pa[i] || 0
    const vb = pb[i] || 0
    if (va < vb) return -1
    if (va > vb) return 1
  }
  return 0
}

export function formatUpdateMethodLabel(method?: string, ver?: number): string {
  switch (method) {
    case 'agent_mcp_write':
      return 'Agent 写入'
    case 'agent_mcp_patch':
      return 'Agent 补丁'
    case 'patch':
      return '文本补丁'
    case 'batch_commit':
      return '原子批量提交'
    case 'mount_sync':
      return '挂载同步'
    case 'version_upload':
      return '上传新版本'
    case 'local_import':
      return '本机导入'
    case 'online_edit':
      return '在线编辑'
    case 'version_restore':
      return '版本回退'
    case 'upload_overwrite':
      return '上传覆盖'
    default:
      if (ver && ver > 1) return '上传覆盖'
      return '首次入库'
  }
}

export function isAgentDeviceName(name?: string): boolean {
  return Boolean(name && name.startsWith('Agent · '))
}

export function extractAgentLabel(name?: string): string {
  return String(name || '')
    .replace(/^Agent\s*·\s*/, '')
    .trim()
}

export function formatStorageModeLabel(mode?: string): string {
  switch (mode) {
    case 'hardlink':
      return '零拷贝硬链'
    case 'external_ref':
      return '外链引用'
    case 'mount':
      return '虚拟挂载'
    default:
      return ''
  }
}

export const categoryNames: Record<string, string> = {
  video: '视频',
  image: '图片',
  audio: '音乐',
  doc: '文档',
  archive: '压缩包',
  code: '文本/代码',
  other: '其他',
}

const editableExts = new Set([
  'txt', 'md', 'markdown', 'json', 'yaml', 'yml', 'toml', 'xml', 'csv', 'tsv',
  'ini', 'conf', 'cfg', 'env', 'log', 'sql', 'sh', 'bash', 'ps1', 'bat', 'cmd',
  'py', 'go', 'js', 'mjs', 'ts', 'tsx', 'jsx', 'css', 'scss', 'html', 'htm', 'vue',
])

const previewExts = new Set([
  ...editableExts,
  'jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'pdf',
  'mp4', 'webm', 'mov', 'm4v', 'mkv', 'mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a',
  'docx', 'xlsx', 'pptx', 'zip', 'tar', 'gz', 'tgz',
])

export function getFileExt(rec: Pick<FileRecord, 'ext' | 'name'>): string {
  if (rec.ext) return rec.ext.toLowerCase().replace(/^\./, '')
  const name = rec.name || ''
  const dot = name.lastIndexOf('.')
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : ''
}

export function isEditableTextFile(rec: FileRecord, writableMountIds?: Set<string>): boolean {
  if (rec.storage_mode === 'mount') {
    if (!rec.mount_id || !writableMountIds?.has(rec.mount_id)) return false
  }
  const ext = getFileExt(rec)
  return editableExts.has(ext)
}

export function isArchiveFile(rec: FileRecord): boolean {
  const name = (rec.name || '').toLowerCase()
  const ext = getFileExt(rec)
  return ext === 'zip' || ext === 'tar' || ext === 'gz' || ext === 'tgz' || name.endsWith('.tar.gz')
}

export function canPreviewFile(rec: FileRecord): boolean {
  const ext = getFileExt(rec)
  return previewExts.has(ext) || isArchiveFile(rec)
}

export function matchExtForPlugin(supportedExts: string[] | undefined, fileName: string): boolean {
  if (!Array.isArray(supportedExts) || supportedExts.length === 0) return false
  if (supportedExts.some((x) => x === '*' || x === '.*')) return true
  const dot = (fileName || '').lastIndexOf('.')
  if (dot < 0) return false
  const ext = fileName.slice(dot).toLowerCase()
  const bare = ext.replace(/^\./, '')
  return supportedExts.some((x) => {
    const n = String(x || '').trim().toLowerCase()
    return n === ext || n === bare || `.${n}` === ext
  })
}

export function getMatchingCapabilitiesForFile(
  capabilities: ClusterCapabilityView[],
  fileName: string
): ClusterCapabilityView[] {
  return capabilities.filter(
    (c) => Array.isArray(c.providers) && c.providers.length > 0 && matchExtForPlugin(c.supported_exts, fileName)
  )
}

export interface DeviceMeta {
  fp: string
  name: string
  detail: string
}

let cachedDeviceMeta: DeviceMeta | null = null

export function getDeviceMeta(): DeviceMeta {
  if (cachedDeviceMeta) return cachedDeviceMeta
  const fpKey = 'lan_filehub_device_fp'
  let fp = ''
  try {
    fp = localStorage.getItem(fpKey) || ''
    if (!fp) {
      const seed = `${navigator.userAgent}|${screen.width}x${screen.height}|${Intl.DateTimeFormat().resolvedOptions().timeZone || ''}`
      let hash = 2166136261
      for (let i = 0; i < seed.length; i++) {
        hash ^= seed.charCodeAt(i)
        hash = Math.imul(hash, 16777619)
      }
      fp = `FP-${(hash >>> 0).toString(16).toUpperCase().padStart(8, '0')}`
      localStorage.setItem(fpKey, fp)
    }
  } catch {
    fp = 'FP-WEB00001'
  }
  const ua = navigator.userAgent || ''
  let os = 'Desktop'
  if (/Windows/i.test(ua)) os = 'Windows'
  else if (/iPhone|iPad|iOS/i.test(ua)) os = 'iOS'
  else if (/Android/i.test(ua)) os = 'Android'
  else if (/Mac OS X|Macintosh/i.test(ua)) os = 'macOS'
  else if (/Linux/i.test(ua)) os = 'Linux'

  let browser = 'Browser'
  if (/Edg\//i.test(ua)) browser = 'Edge'
  else if (/Chrome\//i.test(ua)) browser = 'Chrome'
  else if (/Safari\//i.test(ua)) browser = 'Safari'
  else if (/Firefox\//i.test(ua)) browser = 'Firefox'

  const customName = (() => {
    try {
      return localStorage.getItem('lan_filehub_device_name') || ''
    } catch {
      return ''
    }
  })()
  cachedDeviceMeta = {
    fp,
    name: customName || `${os} · ${browser}`,
    detail: `${os} / ${browser} (${screen.width}x${screen.height})`,
  }
  return cachedDeviceMeta
}

export function isMobileLayout(): boolean {
  return document.documentElement.dataset.layout === 'mobile'
}

export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (!text) return false
  try {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Fallback for plain HTTP contexts (e.g. iOS Safari on LAN HTTP IP)
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    ta.style.left = '-9999px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    ta.setSelectionRange(0, text.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}
