import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  Archive,
  Bot,
  File,
  FileAudio,
  FileSpreadsheet,
  Lock,
  Play,
  Presentation,
} from 'lucide-react'
import { fetchSnippetLines, getCachedPdfThumbnail, getCachedSnippetLines, renderPdfThumbnail } from '@/lib/pdfThumb'
import { cn, getFileExt, isAgentDeviceName } from '@/lib/utils'
import type { FileRecord } from '@/types'

export type AppleIconSize = 'sm' | 'md' | 'lg'

const SERVER_THUMB_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif'])
const RAW_IMAGE_EXTS = new Set(['webp', 'svg', 'bmp', 'ico'])
const VIDEO_EXTS = new Set(['mp4', 'webm', 'mov', 'm4v'])
const TEXT_SNIPPET_EXTS = new Set([
  'txt',
  'md',
  'markdown',
  'json',
  'yaml',
  'yml',
  'csv',
  'log',
  'ini',
  'conf',
  'sql',
  'xml',
  'toml',
  'go',
  'ts',
  'tsx',
  'js',
  'jsx',
  'py',
  'sh',
  'ps1',
  'css',
  'html',
  'htm',
  'c',
  'cpp',
  'h',
  'java',
  'rs',
  'vue',
  'env',
])
const OFFICE_SNIPPET_EXTS = new Set(['docx', 'xlsx', 'pptx'])

export function AppleFolderIcon({ className = 'h-20 w-24' }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 112 92"
      className={cn(
        'select-none drop-shadow-[0_4px_10px_rgba(37,99,235,0.18)] transition-transform duration-200 group-hover:scale-[1.03]',
        className
      )}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="lfhFolderBack" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#77c6fb" />
          <stop offset="100%" stopColor="#4aa1ec" />
        </linearGradient>
        <linearGradient id="lfhFolderFront" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#86d1ff" />
          <stop offset="45%" stopColor="#5fb5fa" />
          <stop offset="100%" stopColor="#3c94e4" />
        </linearGradient>
      </defs>
      {/* Back folder tab */}
      <path
        d="M10 14 C10 9.6 13.6 6 18 6 L39 6 C43 6 45.6 7.8 48.2 11.4 L51.4 15.8 L96 15.8 C100.4 15.8 104 19.4 104 23.8 L104 74 C104 79 100 83 95 83 L17 83 C12 83 8 79 8 74 Z"
        fill="url(#lfhFolderBack)"
      />
      {/* Crisp white inner paper hint */}
      <rect x="13" y="21" width="86" height="12" rx="3" fill="#ffffff" opacity="0.92" />
      {/* Front folder pocket */}
      <rect
        x="6"
        y="24"
        width="100"
        height="61"
        rx="9"
        fill="url(#lfhFolderFront)"
      />
      {/* Specular top edge line */}
      <path
        d="M15 24.75 L97 24.75"
        stroke="#ffffff"
        strokeOpacity="0.52"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
      {/* Subtle bottom inner shadow */}
      <path
        d="M14 82.5 L98 82.5"
        stroke="#1d4ed8"
        strokeOpacity="0.22"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  )
}

function sizeBoxClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'h-[80px]'
    case 'lg':
      return 'h-[126px] sm:h-[144px]'
    default:
      return 'h-[100px] sm:h-[112px]'
  }
}

function paperSheetSizeClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'w-[58px] h-[78px] shrink-0'
    case 'lg':
      return 'w-[90px] h-[122px] sm:w-[104px] sm:h-[140px] shrink-0'
    default:
      return 'w-[72px] h-[98px] sm:w-[80px] sm:h-[108px] shrink-0'
  }
}

function imageMaxSizeClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'max-w-[76px] max-h-[78px]'
    case 'lg':
      return 'max-w-[116px] max-h-[122px] sm:max-w-[136px] sm:max-h-[140px]'
    default:
      return 'max-w-[92px] max-h-[98px] sm:max-w-[108px] sm:max-h-[108px]'
  }
}

function videoBoxSizeClasses(size: AppleIconSize): string {
  switch (size) {
    case 'sm':
      return 'w-[78px] h-[56px] shrink-0'
    case 'lg':
      return 'w-[118px] h-[82px] sm:w-[136px] sm:h-[94px] shrink-0'
    default:
      return 'w-[92px] h-[66px] sm:w-[112px] sm:h-[78px] shrink-0'
  }
}

function FoldedDocSheet({
  size,
  extLabel,
  accentClass,
  badgeClass,
  kind,
}: {
  size: AppleIconSize
  extLabel: string
  accentClass: string
  badgeClass: string
  kind: 'pdf' | 'doc' | 'sheet' | 'slide' | 'archive' | 'audio' | 'generic'
}) {
  return (
    <div
      className={cn(
        'relative flex flex-col items-center justify-between overflow-hidden rounded-[5px] bg-white p-2 text-slate-800',
        paperSheetSizeClasses(size),
        'ring-1 ring-black/10 shadow-[0_2px_6px_rgba(0,0,0,0.12),0_10px_20px_rgba(0,0,0,0.08)]',
        'transition-transform duration-200 group-hover:scale-[1.03]'
      )}
    >
      {/* Top folded corner */}
      <div className="pointer-events-none absolute top-0 right-0 h-3.5 w-3.5 overflow-hidden">
        <div className="h-full w-full bg-gradient-to-bl from-slate-200 via-slate-100 to-slate-200/80 shadow-[-1px_1px_2px_rgba(0,0,0,0.08)]" />
      </div>

      {/* Top accent strip */}
      <div className={cn('h-1 w-6 self-start rounded-full', accentClass)} />

      {/* Center crisp icon */}
      <div className="flex flex-1 items-center justify-center">
        {kind === 'archive' ? (
          <Archive className="h-6 w-6 sm:h-7 sm:w-7 text-amber-500" />
        ) : kind === 'audio' ? (
          <FileAudio className="h-6 w-6 sm:h-7 sm:w-7 text-pink-500" />
        ) : kind === 'sheet' ? (
          <FileSpreadsheet className="h-6 w-6 sm:h-7 sm:w-7 text-emerald-600" />
        ) : kind === 'slide' ? (
          <Presentation className="h-6 w-6 sm:h-7 sm:w-7 text-orange-500" />
        ) : (
          <File className={cn('h-6 w-6 sm:h-7 sm:w-7', kind === 'pdf' ? 'text-rose-500' : 'text-slate-400')} />
        )}
      </div>

      {/* Bottom type label */}
      <span
        className={cn(
          'inline-flex items-center justify-center rounded px-1.5 py-0.5 text-[8.5px] sm:text-[9px] font-bold tracking-wider uppercase',
          badgeClass
        )}
      >
        {extLabel.slice(0, 6)}
      </span>
    </div>
  )
}

export function AppleFileThumbnail({
  rec,
  size = 'md',
}: {
  rec: FileRecord
  size?: AppleIconSize
}) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [visible, setVisible] = useState(false)
  const [imgFailed, setImgFailed] = useState(false)
  const [rawFallback, setRawFallback] = useState(false)

  const ext = getFileExt(rec)
  const extLabel = (ext || rec.category || 'FILE').toUpperCase()
  const ver = rec.version || 1
  const canStream = !rec.source_missing && rec.node_online !== false
  const isServerThumbImage = SERVER_THUMB_EXTS.has(ext)
  const isRawImage = RAW_IMAGE_EXTS.has(ext)
  const isVideo = VIDEO_EXTS.has(ext)
  const isPdf = ext === 'pdf'
  const isTextSnippet = TEXT_SNIPPET_EXTS.has(ext)
  const isOfficeSnippet = OFFICE_SNIPPET_EXTS.has(ext)

  const cacheKey = useMemo(
    () => `${rec.id}_v${ver}_${(rec.sha256 || '').slice(0, 10) || rec.size}`,
    [rec.id, rec.sha256, rec.size, ver]
  )

  const [pdfThumb, setPdfThumb] = useState<string | null>(() =>
    isPdf ? getCachedPdfThumbnail(cacheKey) : null
  )
  const [pdfFailed, setPdfFailed] = useState(false)

  const [snippetLines, setSnippetLines] = useState<string[] | null>(() =>
    isTextSnippet || isOfficeSnippet ? getCachedSnippetLines(cacheKey) : null
  )
  const [snippetFailed, setSnippetFailed] = useState(false)

  useEffect(() => {
    setImgFailed(false)
    setRawFallback(false)
    setPdfFailed(false)
    setSnippetFailed(false)
    if (isPdf) {
      setPdfThumb(getCachedPdfThumbnail(cacheKey))
    }
    if (isTextSnippet || isOfficeSnippet) {
      setSnippetLines(getCachedSnippetLines(cacheKey))
    }
  }, [cacheKey, isOfficeSnippet, isPdf, isTextSnippet])

  // Lazy visibility observer so offscreen PDFs/snippets stay zero-cost.
  useEffect(() => {
    if (visible) return
    const el = hostRef.current
    if (!el || typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true)
          io.disconnect()
        }
      },
      { rootMargin: '180px' }
    )
    io.observe(el)
    return () => io.disconnect()
  }, [visible])

  // Lazy Page-1 PDF renderer
  useEffect(() => {
    if (!isPdf || !canStream || !visible || pdfThumb || pdfFailed) return
    const ctrl = new AbortController()
    const url = `/api/files/${encodeURIComponent(rec.id)}/raw?v=${ver}`
    renderPdfThumbnail(cacheKey, url, ctrl.signal)
      .then((dataUrl) => {
        if (!ctrl.signal.aborted) setPdfThumb(dataUrl)
      })
      .catch((err) => {
        if (ctrl.signal.aborted || err?.name === 'AbortError') return
        setPdfFailed(true)
      })
    return () => ctrl.abort()
  }, [cacheKey, canStream, isPdf, pdfFailed, pdfThumb, rec.id, ver, visible])

  // Lazy Text / Office first-lines renderer
  useEffect(() => {
    if (!(isTextSnippet || isOfficeSnippet) || !canStream || !visible || snippetLines || snippetFailed) {
      return
    }
    const ctrl = new AbortController()
    fetchSnippetLines(cacheKey, rec.id, ctrl.signal)
      .then((lines) => {
        if (!ctrl.signal.aborted) setSnippetLines(lines)
      })
      .catch((err) => {
        if (ctrl.signal.aborted || err?.name === 'AbortError') return
        setSnippetFailed(true)
      })
    return () => ctrl.abort()
  }, [cacheKey, canStream, isOfficeSnippet, isTextSnippet, rec.id, snippetFailed, snippetLines, visible])

  const restricted = Boolean(rec.allowed_users && rec.allowed_users.length > 0)
  const fromAgent = isAgentDeviceName(rec.device_name)

  const imgSrc =
    isServerThumbImage && !rawFallback
      ? `/api/files/${encodeURIComponent(rec.id)}/thumb?w=360&v=${ver}`
      : `/api/files/${encodeURIComponent(rec.id)}/raw?v=${ver}`

  const renderPaperFallback = () => {
    if (isPdf) {
      return (
        <FoldedDocSheet
          size={size}
          extLabel="PDF"
          accentClass="bg-rose-500"
          badgeClass="bg-rose-500/12 text-rose-600"
          kind="pdf"
        />
      )
    }
    if (ext === 'xlsx' || ext === 'xls' || ext === 'csv') {
      return (
        <FoldedDocSheet
          size={size}
          extLabel={extLabel}
          accentClass="bg-emerald-500"
          badgeClass="bg-emerald-500/12 text-emerald-700"
          kind="sheet"
        />
      )
    }
    if (ext === 'pptx' || ext === 'ppt') {
      return (
        <FoldedDocSheet
          size={size}
          extLabel={extLabel}
          accentClass="bg-orange-500"
          badgeClass="bg-orange-500/12 text-orange-600"
          kind="slide"
        />
      )
    }
    if (ext === 'docx' || ext === 'doc') {
      return (
        <FoldedDocSheet
          size={size}
          extLabel={extLabel}
          accentClass="bg-blue-500"
          badgeClass="bg-blue-500/12 text-blue-600"
          kind="doc"
        />
      )
    }
    if (rec.category === 'archive') {
      return (
        <FoldedDocSheet
          size={size}
          extLabel={extLabel}
          accentClass="bg-amber-500"
          badgeClass="bg-amber-500/12 text-amber-700"
          kind="archive"
        />
      )
    }
    if (rec.category === 'audio') {
      return (
        <FoldedDocSheet
          size={size}
          extLabel={extLabel}
          accentClass="bg-pink-500"
          badgeClass="bg-pink-500/12 text-pink-600"
          kind="audio"
        />
      )
    }
    return (
      <FoldedDocSheet
        size={size}
        extLabel={extLabel}
        accentClass="bg-slate-400"
        badgeClass="bg-slate-100 text-slate-600"
        kind="generic"
      />
    )
  }

  return (
    <div
      ref={hostRef}
      className={cn(
        'relative flex w-full items-end justify-center select-none',
        sizeBoxClasses(size)
      )}
    >
      <div className="relative inline-flex items-end justify-center shrink-0">
        {/* 1. Image with pure-Go thumb + crisp white photo paper frame */}
        {(isServerThumbImage || isRawImage) && canStream && !imgFailed ? (
          <img
            src={imgSrc}
            alt={rec.name}
            loading="lazy"
            decoding="async"
            onError={() => {
              if (isServerThumbImage && !rawFallback) {
                setRawFallback(true)
              } else {
                setImgFailed(true)
              }
            }}
            className={cn(
              'block w-auto h-auto rounded-[4px] bg-white p-[2px] object-contain ring-1 ring-black/10 dark:ring-white/15 shadow-[0_2px_6px_rgba(0,0,0,0.12),0_10px_20px_rgba(0,0,0,0.08)] transition-transform duration-200 group-hover:scale-[1.03]',
              imageMaxSizeClasses(size)
            )}
          />
        ) : isVideo && canStream && !imgFailed ? (
          /* 2. Video frame */
          <div
            className={cn(
              'relative overflow-hidden rounded-[8px] bg-black/90 ring-1 ring-black/15 shadow-[0_3px_10px_rgba(0,0,0,0.18)] transition-transform duration-200 group-hover:scale-[1.03]',
              videoBoxSizeClasses(size)
            )}
          >
            <video
              src={`/api/files/${encodeURIComponent(rec.id)}/raw?v=${ver}#t=0.5`}
              preload="metadata"
              muted
              playsInline
              onError={() => setImgFailed(true)}
              className="h-full w-full object-cover"
            />
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-black/20">
              <span className="flex h-8 w-8 items-center justify-center rounded-full bg-white/85 text-slate-900 shadow-md backdrop-blur-xs">
                <Play className="h-3.5 w-3.5 ml-0.5 fill-current" />
              </span>
            </div>
            <span className="pointer-events-none absolute right-1.5 bottom-1.5 rounded bg-black/65 px-1 py-0.5 text-[9px] font-semibold tracking-wider text-white">
              {extLabel}
            </span>
          </div>
        ) : isPdf && canStream && !pdfFailed ? (
          /* 3. PDF Page 1 genuine paper sheet */
          pdfThumb ? (
            <div
              className={cn(
                'relative overflow-hidden rounded-[4px] bg-white ring-1 ring-black/10 shadow-[0_2px_6px_rgba(0,0,0,0.14),0_10px_22px_rgba(0,0,0,0.08)] transition-transform duration-200 group-hover:scale-[1.03]',
                paperSheetSizeClasses(size)
              )}
            >
              <img
                src={pdfThumb}
                alt={rec.name}
                className="h-full w-full object-cover object-top bg-white"
              />
            </div>
          ) : (
            <div
              className={cn(
                'relative flex flex-col justify-between overflow-hidden rounded-[4px] bg-white p-2 ring-1 ring-black/10 shadow-[0_2px_6px_rgba(0,0,0,0.1),0_8px_16px_rgba(0,0,0,0.06)]',
                paperSheetSizeClasses(size)
              )}
            >
              <div className="space-y-1.5 animate-pulse">
                <div className="h-1.5 w-3/4 rounded bg-slate-200" />
                <div className="h-1 w-full rounded bg-slate-100" />
                <div className="h-1 w-5/6 rounded bg-slate-100" />
                <div className="h-1 w-4/6 rounded bg-slate-100" />
              </div>
              <span className="self-end rounded bg-rose-500/12 px-1 py-0.5 text-[8px] font-bold tracking-wider text-rose-600">
                PDF
              </span>
            </div>
          )
        ) : (isTextSnippet || isOfficeSnippet) && canStream && !snippetFailed && snippetLines && snippetLines.length > 0 ? (
          /* 4. Authentic miniature paper typography preview for Text / Code / Office */
          <div
            className={cn(
              'relative overflow-hidden rounded-[4px] bg-white p-2 text-left text-slate-700',
              paperSheetSizeClasses(size),
              'ring-1 ring-black/10 shadow-[0_2px_6px_rgba(0,0,0,0.12),0_10px_20px_rgba(0,0,0,0.07)]',
              'transition-transform duration-200 group-hover:scale-[1.03]'
            )}
          >
            <div
              className={cn(
                'mb-1 h-0.5 w-6 rounded-full',
                ext === 'docx'
                  ? 'bg-blue-500'
                  : ext === 'xlsx'
                  ? 'bg-emerald-500'
                  : ext === 'pptx'
                  ? 'bg-orange-500'
                  : 'bg-slate-400'
              )}
            />
            <div
              className={cn(
                'space-y-[1.5px] text-[5.5px] leading-[1.35] break-all overflow-hidden',
                isTextSnippet && ext !== 'md' && ext !== 'markdown' && ext !== 'txt' ? 'font-mono text-slate-600' : 'text-slate-700'
              )}
            >
              {snippetLines.slice(0, 12).map((line, i) => (
                <div key={i} className="truncate">
                  {line || ' '}
                </div>
              ))}
            </div>
            <div className="pointer-events-none absolute inset-x-0 bottom-0 h-5 bg-gradient-to-t from-white via-white/85 to-transparent" />
            <span
              className={cn(
                'pointer-events-none absolute right-1.5 bottom-1 rounded px-1 py-[1px] text-[7.5px] font-bold tracking-wider uppercase',
                ext === 'docx'
                  ? 'bg-blue-500/12 text-blue-600'
                  : ext === 'xlsx'
                  ? 'bg-emerald-500/12 text-emerald-700'
                  : ext === 'pptx'
                  ? 'bg-orange-500/12 text-orange-600'
                  : 'bg-slate-100 text-slate-600'
              )}
            >
              {extLabel.slice(0, 5)}
            </span>
          </div>
        ) : (
          /* 5. Clean Apple folded paper icon fallback */
          renderPaperFallback()
        )}

        {/* Restrained 16px corner badges hugging the actual thumbnail corner */}
        {(restricted || fromAgent || rec.source_missing || rec.node_online === false) && (
          <div className="pointer-events-none absolute -top-1.5 -right-1.5 flex items-center gap-0.5">
            {restricted && (
              <span
                className="flex h-4 w-4 items-center justify-center rounded-full border border-border bg-card/95 text-amber-500 shadow-xs backdrop-blur-xs"
                title="限定成员可见"
              >
                <Lock className="h-2.5 w-2.5" />
              </span>
            )}
            {fromAgent && (
              <span
                className="flex h-4 w-4 items-center justify-center rounded-full border border-border bg-card/95 text-primary shadow-xs backdrop-blur-xs"
                title="Agent 上传"
              >
                <Bot className="h-2.5 w-2.5" />
              </span>
            )}
            {(rec.source_missing || rec.node_online === false) && (
              <span
                className="flex h-4 w-4 items-center justify-center rounded-full border border-border bg-card/95 text-destructive shadow-xs backdrop-blur-xs"
                title={rec.source_missing ? '源文件丢失' : '节点离线'}
              >
                <AlertTriangle className="h-2.5 w-2.5" />
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
