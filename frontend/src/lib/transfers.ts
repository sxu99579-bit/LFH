import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '@/lib/api'
import { getDeviceMeta } from '@/lib/utils'
import type { FileRecord, TransferTask } from '@/types'

const CHUNK_BYTES = 4 * 1024 * 1024 // 4 MB
const CHUNK_WORKERS = 3

interface InternalTransfer {
  task: TransferTask
  file?: File
  relativePath?: string
  targetNode?: string
  allowedUsers?: string[]
  uploadId?: string
  totalChunks?: number
  abortCtrl?: AbortController
  xhr?: XMLHttpRequest
  dlParts?: BlobPart[]
}

async function computeBlobSHA256(blob: Blob): Promise<string> {
  if (typeof window === 'undefined' || !window.crypto?.subtle) return ''
  try {
    const buf = await blob.arrayBuffer()
    const digest = await window.crypto.subtle.digest('SHA-256', buf)
    return Array.from(new Uint8Array(digest))
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('')
  } catch {
    return ''
  }
}

export function useTransferQueue(onUploadedRefresh: () => void) {
  const [tasks, setTasks] = useState<TransferTask[]>([])
  const [open, setOpen] = useState(false)
  const storeRef = useRef<Map<string, InternalTransfer>>(new Map())

  const syncList = useCallback(() => {
    setTasks(Array.from(storeRef.current.values()).map((x) => ({ ...x.task })))
  }, [])

  const updateTask = useCallback(
    (id: string, patch: Partial<TransferTask>) => {
      const entry = storeRef.current.get(id)
      if (!entry) return
      entry.task = { ...entry.task, ...patch }
      syncList()
    },
    [syncList]
  )

  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      const active = Array.from(storeRef.current.values()).some(
        (x) => x.task.status === 'running' || x.task.status === 'queued'
      )
      if (active) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [])

  const runUpload = useCallback(
    async (id: string) => {
      const entry = storeRef.current.get(id)
      if (!entry || !entry.file) return
      const file = entry.file
      const relPath = entry.relativePath || file.name
      const targetNode = entry.targetNode || 'auto'
      const allowedUsers = entry.allowedUsers || []
      const dev = getDeviceMeta()

      updateTask(id, { status: 'running', error: undefined })

      if (file.size < CHUNK_BYTES) {
        await new Promise<void>((resolve) => {
          const fd = new FormData()
          fd.append('files', file)
          fd.append('relative_paths', relPath)
          fd.append('target_node', targetNode)
          if (allowedUsers.length > 0) fd.append('allowed_users', JSON.stringify(allowedUsers))
          fd.append('device_fp', dev.fp)
          fd.append('device_name', dev.name)
          fd.append('device_detail', dev.detail)

          const xhr = new XMLHttpRequest()
          entry.xhr = xhr
          xhr.open('POST', '/api/upload', true)
          xhr.upload.onprogress = (ev) => {
            if (ev.lengthComputable) {
              const pct = Math.min(99, Math.round((ev.loaded * 100) / Math.max(1, ev.total)))
              updateTask(id, { transferred: ev.loaded, progress: pct })
            }
          }
          xhr.onload = () => {
            entry.xhr = undefined
            if (xhr.status >= 200 && xhr.status < 300) {
              updateTask(id, { status: 'done', transferred: file.size, progress: 100 })
              onUploadedRefresh()
            } else {
              let errMsg = `上传失败 (${xhr.status})`
              try {
                const parsed = JSON.parse(xhr.responseText || '{}')
                if (parsed.error) errMsg = parsed.error
              } catch {
                // ignore
              }
              updateTask(id, { status: 'error', error: errMsg })
            }
            resolve()
          }
          xhr.onerror = () => {
            entry.xhr = undefined
            if (entry.task.status === 'paused' || entry.task.status === 'cancelled') {
              resolve()
              return
            }
            updateTask(id, { status: 'error', error: '网络中断' })
            resolve()
          }
          xhr.onabort = () => {
            entry.xhr = undefined
            resolve()
          }
          xhr.send(fd)
        })
        return
      }

      const ctrl = new AbortController()
      entry.abortCtrl = ctrl
      try {
        const lastSlash = relPath.lastIndexOf('/')
        const folderPath = lastSlash > 0 ? relPath.slice(0, lastSlash) : ''
        const fileName = lastSlash >= 0 ? relPath.slice(lastSlash + 1) : relPath
        const fileSHA = file.size <= 128 * 1024 * 1024 ? await computeBlobSHA256(file) : ''
        if (ctrl.signal.aborted) return

        const initData = await apiFetch<{
          instant?: boolean
          upload_id?: string
          total_chunks?: number
          chunk_size?: number
          uploaded_chunks?: number[]
        }>('/api/uploads/init', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: ctrl.signal,
          body: JSON.stringify({
            name: fileName,
            folder_path: folderPath,
            file_size: file.size,
            chunk_size: CHUNK_BYTES,
            file_sha256: fileSHA,
            target_node: targetNode,
            allowed_users: allowedUsers,
          }),
        })

        if (initData.instant) {
          updateTask(id, { status: 'done', transferred: file.size, progress: 100 })
          onUploadedRefresh()
          return
        }

        const uploadId = initData.upload_id || ''
        const totalChunks = Number(initData.total_chunks || 1)
        const cSize = Number(initData.chunk_size || CHUNK_BYTES)
        entry.uploadId = uploadId
        entry.totalChunks = totalChunks
        const doneSet = new Set(Array.isArray(initData.uploaded_chunks) ? initData.uploaded_chunks : [])
        const pending: number[] = []
        let uploadedBytes = 0
        for (let i = 0; i < totalChunks; i++) {
          const start = i * cSize
          const end = Math.min(file.size, start + cSize)
          if (doneSet.has(i)) uploadedBytes += end - start
          else pending.push(i)
        }
        updateTask(id, {
          transferred: uploadedBytes,
          progress: Math.min(99, Math.round((uploadedBytes * 100) / Math.max(1, file.size))),
        })

        let cursor = 0
        const worker = async () => {
          while (cursor < pending.length) {
            if (ctrl.signal.aborted) return
            const idx = pending[cursor++]
            const start = idx * cSize
            const end = Math.min(file.size, start + cSize)
            const blob = file.slice(start, end)
            const chunkSHA = await computeBlobSHA256(blob)
            if (ctrl.signal.aborted) return
            await apiFetch(`/api/uploads/${encodeURIComponent(uploadId)}/chunk/${idx}`, {
              method: 'PUT',
              headers: chunkSHA ? { 'X-Chunk-SHA256': chunkSHA } : {},
              body: blob,
              signal: ctrl.signal,
            })
            uploadedBytes = Math.min(file.size, uploadedBytes + blob.size)
            updateTask(id, {
              transferred: uploadedBytes,
              progress: Math.min(99, Math.round((uploadedBytes * 100) / Math.max(1, file.size))),
            })
          }
        }
        await Promise.all(
          Array.from({ length: Math.min(CHUNK_WORKERS, Math.max(1, pending.length)) }, () => worker())
        )
        if (ctrl.signal.aborted) return

        await apiFetch(`/api/uploads/${encodeURIComponent(uploadId)}/complete`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: ctrl.signal,
          body: JSON.stringify({ target_node: targetNode }),
        })
        updateTask(id, { status: 'done', transferred: file.size, progress: 100 })
        onUploadedRefresh()
      } catch (err) {
        if (entry.task.status === 'paused' || entry.task.status === 'cancelled') return
        if (err instanceof DOMException && err.name === 'AbortError') return
        updateTask(id, {
          status: 'error',
          error: err instanceof Error ? err.message : '分片上传失败',
        })
      } finally {
        entry.abortCtrl = undefined
      }
    },
    [onUploadedRefresh, updateTask]
  )

  const runDownload = useCallback(
    async (id: string) => {
      const entry = storeRef.current.get(id)
      if (!entry || !entry.task.fileId) return
      const fileId = entry.task.fileId
      const ctrl = new AbortController()
      entry.abortCtrl = ctrl
      if (!entry.dlParts) entry.dlParts = []

      updateTask(id, { status: 'running', error: undefined })
      const androidBridge = (
        window as Window & {
          QuailNestAndroid?: { downloadUrl?: (url: string, fileName: string) => void }
        }
      ).QuailNestAndroid
      if (androidBridge?.downloadUrl) {
        androidBridge.downloadUrl(`/api/files/${encodeURIComponent(fileId)}/download`, entry.task.name)
        updateTask(id, { status: 'done', transferred: entry.task.size, progress: 100 })
        return
      }
      try {
        const loadedSoFar = entry.task.transferred || 0
        const headers: HeadersInit = loadedSoFar > 0 ? { Range: `bytes=${loadedSoFar}-` } : {}
        const res = await fetch(`/api/files/${encodeURIComponent(fileId)}/download`, {
          headers,
          signal: ctrl.signal,
        })
        if (!res.ok && res.status !== 206) {
          throw new Error(`下载失败 (${res.status})`)
        }
        if (res.status === 200 && loadedSoFar > 0) {
          // Server sent full response instead of 206 partial
          entry.dlParts = []
          updateTask(id, { transferred: 0 })
        }
        const reader = res.body?.getReader()
        if (!reader) throw new Error('浏览器不支持流式读取')
        let received = res.status === 206 ? loadedSoFar : 0
        const total = Math.max(1, entry.task.size)
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          if (value && value.byteLength > 0) {
            entry.dlParts.push(value)
            received += value.byteLength
            updateTask(id, {
              transferred: received,
              progress: Math.min(99, Math.round((received * 100) / total)),
            })
          }
        }
        const blob = new Blob(entry.dlParts, { type: 'application/octet-stream' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = entry.task.name
        document.body.appendChild(a)
        a.click()
        a.remove()
        setTimeout(() => URL.revokeObjectURL(url), 1000)
        entry.dlParts = []
        updateTask(id, { status: 'done', transferred: entry.task.size || received, progress: 100 })
      } catch (err) {
        if (entry.task.status === 'paused' || entry.task.status === 'cancelled') return
        if (err instanceof DOMException && err.name === 'AbortError') return
        updateTask(id, {
          status: 'error',
          error: err instanceof Error ? err.message : '下载失败',
        })
      } finally {
        entry.abortCtrl = undefined
      }
    },
    [updateTask]
  )

  const enqueueUploads = useCallback(
    (items: Array<{ file: File; relativePath?: string }>, targetNode = 'auto', allowedUsers: string[] = []) => {
      if (items.length === 0) return
      setOpen(true)
      const ids: string[] = []
      for (const item of items) {
        const id = `up-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
        storeRef.current.set(id, {
          task: {
            id,
            kind: 'upload',
            name: item.relativePath || item.file.name,
            size: item.file.size,
            transferred: 0,
            progress: 0,
            status: 'queued',
          },
          file: item.file,
          relativePath: item.relativePath || item.file.name,
          targetNode,
          allowedUsers,
        })
        ids.push(id)
      }
      syncList()
      ids.forEach((id) => {
        void runUpload(id)
      })
    },
    [runUpload, syncList]
  )

  const enqueueDownload = useCallback(
    (rec: FileRecord) => {
      setOpen(true)
      const id = `dl-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
      storeRef.current.set(id, {
        task: {
          id,
          kind: 'download',
          name: rec.name,
          size: rec.size,
          transferred: 0,
          progress: 0,
          status: 'queued',
          fileId: rec.id,
        },
        dlParts: [],
      })
      syncList()
      void runDownload(id)
    },
    [runDownload, syncList]
  )

  const pauseTask = useCallback(
    (id: string) => {
      const entry = storeRef.current.get(id)
      if (!entry) return
      updateTask(id, { status: 'paused' })
      entry.abortCtrl?.abort()
      entry.xhr?.abort()
    },
    [updateTask]
  )

  const resumeTask = useCallback(
    (id: string) => {
      const entry = storeRef.current.get(id)
      if (!entry) return
      if (entry.task.kind === 'upload') void runUpload(id)
      else void runDownload(id)
    },
    [runDownload, runUpload]
  )

  const cancelTask = useCallback(
    (id: string) => {
      const entry = storeRef.current.get(id)
      if (!entry) return
      updateTask(id, { status: 'cancelled' })
      entry.abortCtrl?.abort()
      entry.xhr?.abort()
      if (entry.uploadId) {
        void apiFetch(`/api/uploads/${encodeURIComponent(entry.uploadId)}`, { method: 'DELETE' }).catch(() => {})
      }
      storeRef.current.delete(id)
      syncList()
    },
    [syncList, updateTask]
  )

  const clearDone = useCallback(() => {
    for (const [id, entry] of storeRef.current.entries()) {
      if (entry.task.status === 'done' || entry.task.status === 'cancelled') {
        storeRef.current.delete(id)
      }
    }
    syncList()
  }, [syncList])

  return {
    tasks,
    open,
    setOpen,
    enqueueUploads,
    enqueueDownload,
    pauseTask,
    resumeTask,
    cancelTask,
    clearDone,
  }
}
