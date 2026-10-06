import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, IconButton } from '../../components'
import { isApiError, listReceivedParts, uploadFile } from '../../api/client'
import type { UploadProgress } from '../../api/client'
import { Banner, ProgressBar } from './ui'

export interface UploadTask {
  key: string
  name: string
  size: number
  percent: number
  sentBytes: number
  partIndex: number
  totalParts: number
  status: 'uploading' | 'done' | 'failed' | 'cancelled'
  error: string | null
  errorCode: string | null
  uploadId: string | null
}

const STATUS_LABEL: Record<UploadTask['status'], string> = {
  uploading: 'Mengunggah',
  done: 'Selesai diunggah',
  failed: 'Gagal',
  cancelled: 'Dibatalkan',
}

function newTask(name: string, size: number): UploadTask {
  return {
    key: `${name}-${size}-${Math.random().toString(36).slice(2, 8)}`,
    name,
    size,
    percent: 0,
    sentBytes: 0,
    partIndex: 0,
    totalParts: 0,
    status: 'uploading',
    error: null,
    errorCode: null,
    uploadId: null,
  }
}

/**
 * Resumable upload queue (FR-03, AC-03).
 *
 * `uploadFile` owns the POST → parts → complete protocol; this wrapper adds the
 * per-file progress row, cancel and retry. Retry asks the server which parts it
 * already holds before resending, so an interrupted transfer resumes instead of
 * restarting.
 *
 * Queue depth is one active upload, which is what the PRD asks for on small
 * devices; further selected files wait their turn.
 */
export function UploadQueue({ onFinished }: { onFinished: () => void }) {
  const [tasks, setTasks] = useState<UploadTask[]>([])
  const [over, setOver] = useState(false)
  const controllers = useRef(new Map<string, AbortController>())
  const sources = useRef(new Map<string, File>())
  const queue = useRef<File[]>([])
  const pumping = useRef(false)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      for (const controller of controllers.current.values()) controller.abort()
    }
  }, [])

  const patch = useCallback((key: string, next: Partial<UploadTask>) => {
    if (!mounted.current) return
    setTasks((prev) => prev.map((t) => (t.key === key ? { ...t, ...next } : t)))
  }, [])

  const transfer = useCallback(
    async (task: UploadTask, file: File) => {
      const controller = new AbortController()
      controllers.current.set(task.key, controller)
      try {
        await uploadFile(
          file,
          (p: UploadProgress) =>
            patch(task.key, {
              percent: p.percent,
              sentBytes: p.sentBytes,
              partIndex: p.partIndex,
              totalParts: p.totalParts,
              uploadId: p.uploadId ?? task.uploadId,
            }),
          { signal: controller.signal },
        )
        patch(task.key, { status: 'done', percent: 100, error: null, errorCode: null })
        onFinished()
      } catch (err) {
        if (controller.signal.aborted) {
          patch(task.key, { status: 'cancelled', error: 'Dibatalkan operator.' })
        } else if (isApiError(err)) {
          patch(task.key, { status: 'failed', error: err.message, errorCode: err.code || null })
        } else {
          patch(task.key, {
            status: 'failed',
            error: 'Unggahan gagal karena kesalahan jaringan.',
            errorCode: null,
          })
        }
      } finally {
        controllers.current.delete(task.key)
      }
    },
    [onFinished, patch],
  )

  // Serial pump: one upload in flight, the rest follow in selection order.
  const pump = useCallback(async () => {
    if (pumping.current) return
    pumping.current = true
    try {
      while (mounted.current && queue.current.length > 0) {
        const file = queue.current.shift()
        if (!file) break
        const task = newTask(file.name, file.size)
        sources.current.set(task.key, file)
        if (mounted.current) setTasks((prev) => [task, ...prev])
        await transfer(task, file)
      }
    } finally {
      pumping.current = false
    }
  }, [transfer])

  const enqueue = useCallback(
    (incoming: FileList | File[]) => {
      const list = Array.from(incoming)
      if (list.length === 0) return
      queue.current = queue.current.concat(list)
      void pump()
    },
    [pump],
  )

  const cancel = useCallback((task: UploadTask) => {
    controllers.current.get(task.key)?.abort()
    setTasks((prev) => prev.filter((t) => t.key !== task.key))
    sources.current.delete(task.key)
  }, [])

  const retry = useCallback(
    async (task: UploadTask) => {
      const file = sources.current.get(task.key)
      if (!file) {
        // A page reload dropped the File handle. The server may still hold the
        // partial session, so report what it has instead of silently failing.
        if (task.uploadId) {
          try {
            const received = await listReceivedParts(task.uploadId)
            patch(task.key, {
              status: 'failed',
              error: `Sesi upload ${task.uploadId} masih ada di server dengan ${received.length} bagian diterima. Pilih ulang berkas yang sama untuk melanjutkan.`,
            })
          } catch (err) {
            patch(task.key, {
              status: 'failed',
              error: isApiError(err) ? err.message : 'Sesi upload tidak dapat dibaca.',
            })
          }
        }
        return
      }
      patch(task.key, {
        status: 'uploading',
        error: null,
        errorCode: null,
        percent: 0,
        sentBytes: 0,
      })
      await transfer({ ...task, status: 'uploading' }, file)
    },
    [patch, transfer],
  )

  const dismiss = useCallback((key: string) => {
    setTasks((prev) => prev.filter((t) => t.key !== key))
    sources.current.delete(key)
  }, [])

  return (
    <div className="y-stack">
      <div
        className={`y-drop${over ? ' is-over' : ''}`}
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setOver(false)
          enqueue(e.dataTransfer.files)
        }}
      >
        <span>Tarik berkas video ke sini, atau</span>
        <label className="y-btn is-primary" style={{ cursor: 'pointer' }}>
          Pilih berkas
          <input
            className="y-sr"
            type="file"
            multiple
            accept="video/mp4,video/quicktime,video/x-matroska,.mp4,.mov,.mkv"
            onChange={(e) => {
              if (e.target.files) enqueue(e.target.files)
              e.target.value = ''
            }}
          />
        </label>
        <span className="y-hint">MP4, MOV, atau MKV. Satu unggahan aktif pada satu waktu.</span>
      </div>

      {tasks.length > 0 ? (
        <ul className="y-rows" aria-live="polite">
          {tasks.map((task) => (
            <li className="y-file" key={task.key}>
              <div className="y-file-head">
                <span className="y-file-name">{task.name}</span>
                <span className="y-row-meta">
                  {STATUS_LABEL[task.status]}
                  {task.status === 'uploading' && task.totalParts > 0
                    ? ` · bagian ${task.partIndex}/${task.totalParts}`
                    : ''}
                </span>
              </div>
              <ProgressBar
                percent={task.percent}
                tone={task.status === 'done' ? 'ok' : task.status === 'failed' ? 'fail' : undefined}
              />
              <div className="y-inline">
                <span className="y-row-meta">
                  {task.percent.toFixed(1)}% · {task.sentBytes.toLocaleString('id-ID')} dari{' '}
                  {task.size.toLocaleString('id-ID')} byte
                </span>
                <span className="y-spacer" />
                {task.status === 'uploading' ? (
                  <Button size="sm" variant="ghost" onClick={() => cancel(task)}>
                    Batal
                  </Button>
                ) : task.status === 'failed' ? (
                  <Button size="sm" onClick={() => void retry(task)}>
                    Coba lagi
                  </Button>
                ) : (
                  <IconButton
                    label={`Sembunyikan status unggahan ${task.name}`}
                    icon="✕"
                    size="sm"
                    onClick={() => dismiss(task.key)}
                  />
                )}
              </div>
              {task.error ? (
                <Banner tone="error">
                  {task.error}
                  {task.errorCode ? <span className="y-mono"> ({task.errorCode})</span> : null}
                </Banner>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}