import { api } from '@/lib/api'

const MAX_PDF_CACHE = 120
const MAX_SNIPPET_CACHE = 200
const PDF_CONCURRENCY = 2
const SNIPPET_CONCURRENCY = 4

const pdfCache = new Map<string, string>()
const pdfInflight = new Map<string, Promise<string>>()
const snippetCache = new Map<string, string[]>()
const snippetInflight = new Map<string, Promise<string[]>>()

function setLRU<T>(cache: Map<string, T>, key: string, val: T, maxEntries: number) {
  if (cache.has(key)) cache.delete(key)
  cache.set(key, val)
  while (cache.size > maxEntries) {
    const oldest = cache.keys().next().value
    if (oldest === undefined) break
    cache.delete(oldest)
  }
}

class AsyncQueue {
  private running = 0
  private waiters: Array<() => void> = []
  private readonly limit: number

  constructor(limit: number) {
    this.limit = limit
  }

  async run<T>(signal: AbortSignal | undefined, fn: () => Promise<T>): Promise<T> {
    if (signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError')
    }
    if (this.running >= this.limit) {
      await new Promise<void>((resolve, reject) => {
        const onAbort = () => {
          const idx = this.waiters.indexOf(wake)
          if (idx >= 0) this.waiters.splice(idx, 1)
          reject(new DOMException('Aborted', 'AbortError'))
        }
        const wake = () => {
          signal?.removeEventListener('abort', onAbort)
          resolve()
        }
        signal?.addEventListener('abort', onAbort, { once: true })
        this.waiters.push(wake)
      })
    }
    this.running++
    try {
      return await fn()
    } finally {
      this.running--
      const next = this.waiters.shift()
      if (next) next()
    }
  }
}

const pdfQueue = new AsyncQueue(PDF_CONCURRENCY)
const snippetQueue = new AsyncQueue(SNIPPET_CONCURRENCY)

let pdfjsPromise: Promise<typeof import('pdfjs-dist')> | null = null

async function getPdfJs() {
  if (!pdfjsPromise) {
    pdfjsPromise = import('pdfjs-dist').then((mod) => {
      mod.GlobalWorkerOptions.workerSrc = new URL(
        'pdfjs-dist/build/pdf.worker.min.mjs',
        import.meta.url
      ).href
      return mod
    })
  }
  return pdfjsPromise
}

export function getCachedPdfThumbnail(cacheKey: string): string | null {
  const hit = pdfCache.get(cacheKey)
  if (!hit) return null
  setLRU(pdfCache, cacheKey, hit, MAX_PDF_CACHE)
  return hit
}

export async function renderPdfThumbnail(
  cacheKey: string,
  url: string,
  signal?: AbortSignal
): Promise<string> {
  const cached = getCachedPdfThumbnail(cacheKey)
  if (cached) return cached

  const existing = pdfInflight.get(cacheKey)
  if (existing) return existing

  const work = pdfQueue.run(signal, async () => {
    const again = getCachedPdfThumbnail(cacheKey)
    if (again) return again

    const pdfjs = await getPdfJs()
    if (signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError')
    }

    const loadingTask = pdfjs.getDocument({
      url,
      withCredentials: true,
      rangeChunkSize: 65536,
      disableAutoFetch: true,
      disableStream: false,
      verbosity: 0,
    })

    const onAbort = () => {
      void loadingTask.destroy()
    }
    signal?.addEventListener('abort', onAbort, { once: true })

    try {
      const pdf = await loadingTask.promise
      if (signal?.aborted) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const page = await pdf.getPage(1)
      const baseViewport = page.getViewport({ scale: 1 })
      const maxBase = Math.max(baseViewport.width, baseViewport.height, 1)
      const scale = Math.min(2.0, 260 / maxBase)
      const viewport = page.getViewport({ scale })

      const canvas = document.createElement('canvas')
      canvas.width = Math.max(1, Math.round(viewport.width))
      canvas.height = Math.max(1, Math.round(viewport.height))
      const ctx = canvas.getContext('2d')
      if (!ctx) {
        throw new Error('2D canvas unavailable')
      }
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, canvas.width, canvas.height)

      await page.render({
        canvasContext: ctx,
        canvas,
        viewport,
      }).promise

      const dataUrl = canvas.toDataURL('image/jpeg', 0.84)
      canvas.width = 0
      canvas.height = 0
      page.cleanup()
      setLRU(pdfCache, cacheKey, dataUrl, MAX_PDF_CACHE)
      return dataUrl
    } finally {
      signal?.removeEventListener('abort', onAbort)
      await loadingTask.destroy().catch(() => {})
    }
  })

  pdfInflight.set(cacheKey, work)
  try {
    return await work
  } finally {
    pdfInflight.delete(cacheKey)
  }
}

export function getCachedSnippetLines(cacheKey: string): string[] | null {
  const hit = snippetCache.get(cacheKey)
  if (!hit) return null
  setLRU(snippetCache, cacheKey, hit, MAX_SNIPPET_CACHE)
  return hit
}

export async function fetchSnippetLines(
  cacheKey: string,
  fileId: string,
  signal?: AbortSignal
): Promise<string[]> {
  const cached = getCachedSnippetLines(cacheKey)
  if (cached) return cached

  const existing = snippetInflight.get(cacheKey)
  if (existing) return existing

  const work = snippetQueue.run(signal, async () => {
    const again = getCachedSnippetLines(cacheKey)
    if (again) return again

    const res = await api.previewFileSnippet(fileId)
    let lines: string[] = []
    if (Array.isArray(res.lines) && res.lines.length > 0) {
      lines = res.lines.slice(0, 14)
    } else if (typeof res.content === 'string') {
      lines = res.content
        .replace(/\r\n/g, '\n')
        .split('\n')
        .map((l) => l.trimEnd())
        .filter((l, idx, arr) => l.length > 0 || (idx > 0 && arr[idx - 1].length > 0))
        .slice(0, 14)
    }
    setLRU(snippetCache, cacheKey, lines, MAX_SNIPPET_CACHE)
    return lines
  })

  snippetInflight.set(cacheKey, work)
  try {
    return await work
  } finally {
    snippetInflight.delete(cacheKey)
  }
}
