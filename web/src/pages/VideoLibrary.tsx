import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { Button, EmptyState, IconButton, Modal, StatusBadge } from '../components'
import {
  deleteVideo,
  errorMessage,
  getVideoContentUrl,
  getVideoThumbnailUrl,
  isApiError,
  recheckVideo,
} from '../api/client'
import { useVideos } from '../state/useVideos'
import { useEventStream } from '../api/events'
import type { Media, MediaIssue, MediaUsage } from '../types'
import { UploadQueue } from './shared/UploadQueue'
import {
  Banner,
  COMPAT_LABEL,
  FILE_STATUS_LABEL,
  IssueList,
  Loading,
  SEVERITY_LABEL,
  detailArray,
  formatBytes,
  formatClock,
  formatDateTime,
  formatNumber,
} from './shared/ui'

const PAGE_SIZE = 20

/* --- row geometry --------------------------------------------------------
 * A dense table only lines up when the columns that must not move are pinned.
 * Without these widths the browser hands every spare pixel to whichever column
 * is widest, and the thumbnails drift out of column. */
const THUMB_CELL: CSSProperties = { width: 92, minWidth: 92, maxWidth: 92 }
const NAME_CELL: CSSProperties = { minWidth: 220 }
const STATUS_CELL: CSSProperties = { width: 128 }
const COMPAT_CELL: CSSProperties = { width: 152 }
const ACTION_CELL: CSSProperties = { width: 224, minWidth: 224 }

/* A row action is a control, not a stray glyph floating in a cell: hairline
   border, its own surface, and the 44px target PRD §5.2 asks for. */
const ACTION_BTN: CSSProperties = {
  border: '1px solid var(--y-line)',
  background: 'var(--y-card-2)',
}
const ACTION_BTN_DANGER: CSSProperties = {
  ...ACTION_BTN,
  color: 'var(--color-danger)',
  borderColor: 'rgba(239, 35, 60, 0.38)',
}

const EMPTY_ICON: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 56,
  height: 56,
  borderRadius: 16,
  border: '1px dashed var(--y-line)',
  background: 'var(--y-card-2)',
  color: 'var(--y-accent)',
  fontSize: 24,
  lineHeight: 1,
}

/* Severity is carried by word, glyph and colour together (PRD §5.1): colour
   alone never has to carry the meaning. */
const SEVERITY_TONE: Record<string, string> = {
  blocking: 'is-red',
  warning: 'is-yellow',
  notice: 'is-blue',
}
const SEVERITY_GLYPH: Record<string, string> = { blocking: '✕', warning: '!', notice: 'i' }
/** Lower sorts first: a blocking problem outranks a warning outranks a notice. */
const SEVERITY_RANK: Record<string, number> = { blocking: 0, warning: 1, notice: 2 }

/* The same wording and glyphs the StatusBadge uses, so a chip in the table
   header and a badge in a row never disagree. */
const FILE_STATUS_TONE: Record<string, string> = {
  ready: 'is-green',
  uploading: 'is-blue',
  processing: 'is-yellow',
  invalid: 'is-red',
  missing: 'is-red',
}
const FILE_STATUS_GLYPH: Record<string, string> = {
  uploading: '↑',
  processing: '◍',
  ready: '✓',
  invalid: '✕',
  missing: '⊘',
}
/** Lower sorts first, so the header chips run the operator's mental model. */
const FILE_STATUS_RANK: Record<string, number> = {
  uploading: 0,
  processing: 1,
  ready: 2,
  invalid: 3,
  missing: 4,
}
/** An unrecognised state must still sort: it falls to the end of the group. */
const UNKNOWN_SEVERITY_RANK = 3
const UNKNOWN_STATUS_RANK = 5

/** What a `usage[].kind` entry means to the operator, in their own words. */
const USAGE_KIND_LABEL: Record<string, string> = {
  playlist: 'Playlist',
  revision: 'Revisi playlist',
  schedule: 'Jadwal',
  session: 'Sesi siaran',
}

/** A row shows at most this many messages per severity; the rest live in Detail. */
const MAX_ROW_ISSUES = 2

/** The secondary line under the file name: size, resolution, frame rate, length. */
function fileMeta(m: Media): string {
  return [
    formatBytes(m.size),
    m.resolution ?? (m.width && m.height ? `${m.width}×${m.height}` : null),
    m.fps ? `${m.fps.toFixed(2)} fps` : null,
    m.duration ? formatClock(m.duration) : null,
    m.container ? m.container.toUpperCase() : null,
  ]
    .filter((part): part is string => !!part)
    .join(' · ')
}

export default function VideoLibrary() {
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [offset, setOffset] = useState(0)
  const [preview, setPreview] = useState<Media | null>(null)
  const [detail, setDetail] = useState<Media | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Media | null>(null)
  const [deleteBlockers, setDeleteBlockers] = useState<MediaUsage[]>([])
  /** True only once the server itself has refused with 409 MEDIA_IN_USE. */
  const [deleteRejected, setDeleteRejected] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const uploadRef = useRef<HTMLElement | null>(null)
  const { connected } = useEventStream()

  const videos = useVideos({ q: query, limit: PAGE_SIZE, offset })
  const { items, total, loading, error, refresh } = videos

  // Media changes arrive over SSE; a re-read keeps the table in step without a
  // manual reload.
  const { subscribeTo } = useEventStream()
  useEffect(() => subscribeTo('media', () => void refresh()), [subscribeTo, refresh])

  const submitSearch = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault()
      setOffset(0)
      setQuery(search.trim())
    },
    [search],
  )

  const clearSearch = useCallback(() => {
    setSearch('')
    setQuery('')
    setOffset(0)
  }, [])

  const firstPage = useCallback(() => setOffset(0), [])

  /**
   * The drop zone owns the only file input on the page, so the empty state's
   * primary action opens that picker rather than adding a second one.
   */
  const openFilePicker = useCallback(() => {
    uploadRef.current?.querySelector<HTMLInputElement>('input[type="file"]')?.click()
  }, [])

  const askDelete = useCallback((m: Media) => {
    setActionError(null)
    setDeleteRejected(false)
    // `usage` is what the server already knows about, so the operator sees the
    // blockers before pressing the button rather than after (AC-20).
    setDeleteBlockers(m.usage ?? [])
    setPendingDelete(m)
  }, [])

  const closeDelete = useCallback(() => {
    setPendingDelete(null)
    setDeleteBlockers([])
    setDeleteRejected(false)
  }, [])

  const onDelete = useCallback(async () => {
    if (!pendingDelete) return
    setBusyId(pendingDelete.id)
    setActionError(null)
    try {
      await deleteVideo(pendingDelete.id)
      closeDelete()
      await refresh()
    } catch (err) {
      if (isApiError(err) && err.code === 'MEDIA_IN_USE') {
        setDeleteBlockers(
          detailArray(err.details, 'usage').map((u) => ({
            kind: String(u.kind ?? ''),
            id: String(u.id ?? ''),
            name: String(u.name ?? ''),
          })),
        )
        setDeleteRejected(true)
      } else {
        setActionError(errorMessage(err, 'Penghapusan gagal.'))
      }
    } finally {
      setBusyId(null)
    }
  }, [pendingDelete, refresh, closeDelete])

  const onRecheck = useCallback(
    async (media: Media) => {
      setBusyId(media.id)
      setActionError(null)
      try {
        await recheckVideo(media.id)
        await refresh()
      } catch (err) {
        setActionError(errorMessage(err, 'Pemeriksaan ulang gagal.'))
      } finally {
        setBusyId(null)
      }
    },
    [refresh],
  )

  const page = Math.floor(offset / PAGE_SIZE) + 1
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const canPrev = offset > 0
  const canNext = offset + PAGE_SIZE < total

  const statusCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const m of items) counts.set(m.file_status, (counts.get(m.file_status) ?? 0) + 1)
    // An unrecognised state sorts last rather than leading the chip row.
    return [...counts.entries()].sort(
      (a, b) =>
        (FILE_STATUS_RANK[a[0]] ?? UNKNOWN_STATUS_RANK) -
        (FILE_STATUS_RANK[b[0]] ?? UNKNOWN_STATUS_RANK),
    )
  }, [items])

  const listEmpty = !loading && !error && items.length === 0

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Pustaka Video</h1>
            <p>
              {formatNumber(total)} video tersimpan
              {query ? ` untuk “${query}”` : ''}. Berkas yang masih dipakai tidak dapat dihapus.
            </p>
          </div>
        </header>

        {!connected ? (
          <Banner tone="warn" title="Sambungan ke server terputus">
            Daftar video mungkin belum terbaru.
          </Banner>
        ) : null}

        {actionError ? (
          <Banner tone="error" title="Tindakan gagal">
            {actionError}
          </Banner>
        ) : null}

        <section className="y-card y-card--primary" aria-label="Unggah video" ref={uploadRef}>
          <div className="y-card-head">
            <h2>
              <span aria-hidden="true">↑</span>
              Unggah Video
            </h2>
            <span className="y-row-meta">
              MP4, MOV, atau MKV · server memeriksa codec dan membuat thumbnail
            </span>
          </div>
          <UploadQueue onFinished={() => void refresh()} />
        </section>

        <section className="y-card" aria-label="Daftar video">
          <div className="y-card-head">
            <h2>Daftar Video</h2>
            {statusCounts.length > 0 ? (
              <div className="y-btn-row" style={{ gap: 6 }}>
                {statusCounts.map(([state, count]) => (
                  <span key={state} className={`y-badge ${FILE_STATUS_TONE[state] ?? 'is-muted'}`}>
                    <span aria-hidden="true">{FILE_STATUS_GLYPH[state] ?? '•'}</span>
                    {FILE_STATUS_LABEL[state] ?? state} {formatNumber(count)}
                  </span>
                ))}
              </div>
            ) : (
              <span className="y-row-meta">{formatNumber(total)} video</span>
            )}
          </div>

          <form className="y-inline" onSubmit={submitSearch} role="search">
            <label className="y-sr" htmlFor="video-search">
              Cari video
            </label>
            <input
              id="video-search"
              className="y-input"
              style={{ flex: '1 1 200px' }}
              type="search"
              value={search}
              placeholder="Cari nama berkas…"
              onChange={(e) => setSearch(e.target.value)}
            />
            <Button type="submit">Cari</Button>
            {query ? (
              <Button variant="ghost" onClick={clearSearch}>
                Reset
              </Button>
            ) : null}
          </form>

          {loading && items.length === 0 ? <Loading label="Memuat pustaka video…" /> : null}
          {error ? (
            <Banner tone="error" title="Gagal memuat pustaka video">
              {error}
            </Banner>
          ) : null}

          {listEmpty && query ? (
            <NoResults query={query} onClear={clearSearch} />
          ) : null}

          {listEmpty && !query && total === 0 ? (
            <LibraryEmpty onPick={openFilePicker} />
          ) : null}

          {/* Reachable when the last row of a later page was deleted. */}
          {listEmpty && !query && total > 0 ? <EmptyPage onBack={firstPage} /> : null}

          {items.length > 0 ? (
            <>
              <div className="y-scroll">
                <table className="y-table">
                  <caption className="y-sr">
                    Daftar video dengan thumbnail, nama berkas, ukuran, resolusi, durasi, status
                    penyimpanan, kompatibilitas, dan aksi
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col" style={THUMB_CELL}>
                        Pratinjau
                      </th>
                      <th scope="col" style={NAME_CELL}>
                        Video
                      </th>
                      <th scope="col" style={STATUS_CELL}>
                        Status penyimpanan
                      </th>
                      <th scope="col" style={COMPAT_CELL}>
                        Kompatibilitas
                      </th>
                      <th scope="col" style={{ ...ACTION_CELL, textAlign: 'right' }}>
                        Aksi
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((m) => {
                      const canPreview = m.file_status !== 'missing'
                      const usageCount = m.usage ? m.usage.length : 0
                      return (
                        <tr key={m.id}>
                          <td style={THUMB_CELL}>
                            <button
                              type="button"
                              onClick={() => setPreview(m)}
                              disabled={!canPreview}
                              aria-label={
                                canPreview
                                  ? `Buka pratinjau ${m.original_name}`
                                  : `Thumbnail ${m.original_name}: berkas hilang`
                              }
                              style={{
                                display: 'block',
                                padding: 0,
                                borderRadius: 6,
                                border: '1px solid transparent',
                                cursor: canPreview ? 'pointer' : 'not-allowed',
                                opacity: canPreview ? 1 : 0.55,
                              }}
                            >
                              {m.has_thumbnail ? (
                                <img
                                  className="y-thumb"
                                  src={getVideoThumbnailUrl(m.id)}
                                  alt={`Thumbnail ${m.original_name}`}
                                  loading="lazy"
                                />
                              ) : (
                                <span className="y-thumb-ph">Thumbnail belum ada</span>
                              )}
                            </button>
                          </td>
                          <td style={NAME_CELL}>
                            <div
                              style={{
                                display: 'flex',
                                flexDirection: 'column',
                                gap: 2,
                                minWidth: 0,
                              }}
                            >
                              <span
                                style={{
                                  fontWeight: 650,
                                  lineHeight: 1.35,
                                  overflowWrap: 'anywhere',
                                }}
                              >
                                {m.original_name}
                              </span>
                              <span className="y-row-meta">{fileMeta(m)}</span>
                            </div>
                            <RowIssues issues={m.issues} />
                          </td>
                          <td style={STATUS_CELL}>
                            <div
                              style={{
                                display: 'flex',
                                flexDirection: 'column',
                                gap: 4,
                                alignItems: 'flex-start',
                              }}
                            >
                              <StatusBadge
                                state={m.file_status}
                                kind="file"
                                label={FILE_STATUS_LABEL[m.file_status] ?? m.file_status}
                              />
                              {usageCount > 0 ? (
                                <span className="y-row-meta">Dipakai {usageCount} entitas</span>
                              ) : null}
                            </div>
                          </td>
                          <td style={COMPAT_CELL}>
                            <StatusBadge
                              state={m.compatibility}
                              kind="compatibility"
                              label={COMPAT_LABEL[m.compatibility] ?? m.compatibility}
                            />
                          </td>
                          <td style={{ ...ACTION_CELL, textAlign: 'right' }}>
                            <div
                              className="y-row-actions"
                              style={{ justifyContent: 'flex-end', flexWrap: 'nowrap' }}
                            >
                              <IconButton
                                label={`Pratinjau ${m.original_name}`}
                                icon="▶"
                                style={ACTION_BTN}
                                disabled={!canPreview}
                                onClick={() => setPreview(m)}
                              />
                              <IconButton
                                label={`Detail ${m.original_name}`}
                                icon="i"
                                style={ACTION_BTN}
                                onClick={() => setDetail(m)}
                              />
                              <IconButton
                                label={`Periksa ulang ${m.original_name}`}
                                icon="↻"
                                style={ACTION_BTN}
                                busy={busyId === m.id}
                                onClick={() => void onRecheck(m)}
                              />
                              {/* The rule keeps the destructive control from
                                  reading as one more item in the group. */}
                              <span
                                style={{
                                  display: 'inline-flex',
                                  alignItems: 'center',
                                  marginLeft: 2,
                                  paddingLeft: 8,
                                  borderLeft: '1px solid var(--y-line)',
                                }}
                              >
                                <IconButton
                                  label={`Hapus ${m.original_name}`}
                                  icon="✕"
                                  variant="danger"
                                  style={ACTION_BTN_DANGER}
                                  onClick={() => askDelete(m)}
                                />
                              </span>
                            </div>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>

              <div className="y-pager">
                <span className="y-row-meta">
                  Menampilkan {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} dari{' '}
                  {formatNumber(total)}
                </span>
                <div className="y-btn-row">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={!canPrev}
                    onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                  >
                    Sebelumnya
                  </Button>
                  <span className="y-row-meta">
                    Halaman {page} dari {pages}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={!canNext}
                    onClick={() => setOffset(offset + PAGE_SIZE)}
                  >
                    Berikutnya
                  </Button>
                </div>
              </div>
            </>
          ) : null}
        </section>
      </div>

      <Modal
        open={!!preview}
        title={preview ? `Pratinjau: ${preview.original_name}` : ''}
        onClose={() => setPreview(null)}
      >
        {preview ? <LocalPreview media={preview} /> : null}
      </Modal>

      <Modal
        open={!!detail}
        title={detail ? `Detail: ${detail.original_name}` : ''}
        onClose={() => setDetail(null)}
      >
        {detail ? <MediaDetail media={detail} /> : null}
      </Modal>

      <Modal
        open={!!pendingDelete}
        title="Hapus video"
        onClose={closeDelete}
        busy={busyId === pendingDelete?.id}
        footer={
          <>
            <Button variant="ghost" onClick={closeDelete}>
              Batal
            </Button>
            <Button
              variant="danger"
              busy={busyId === pendingDelete?.id}
              disabled={deleteRejected}
              onClick={() => void onDelete()}
            >
              Hapus permanen
            </Button>
          </>
        }
      >
        {pendingDelete ? (
          <div className="y-stack" style={{ gap: 12 }}>
            <p style={{ margin: 0 }}>
              File <strong>{pendingDelete.original_name}</strong> ({formatBytes(pendingDelete.size)})
              akan dihapus dari penyimpanan server. Tindakan ini tidak dapat dibatalkan.
            </p>
            {deleteBlockers.length > 0 ? (
              <Banner
                tone={deleteRejected ? 'error' : 'warn'}
                title={
                  deleteRejected
                    ? 'File masih dipakai, penghapusan ditolak'
                    : 'File ini masih dirujuk entitas lain'
                }
              >
                <p style={{ margin: '0 0 6px' }}>
                  {deleteRejected
                    ? 'Hapus dependensi berikut lebih dahulu, atau pindahkan ke revisi playlist/jadwal yang tidak lagi memakainya:'
                    : 'Hapus rujukan berikut lebih dahulu bila video ini tidak boleh hilang:'}
                </p>
                <BlockerList blockers={deleteBlockers} />
              </Banner>
            ) : null}
            {deleteRejected ? (
              <p className="y-card-note">
                Server menolak penghapusan selama masih ada rujukan. Setelah dependensi di atas
                dihapus, tekan Periksa ulang lalu coba hapus kembali.
              </p>
            ) : null}
          </div>
        ) : null}
      </Modal>
    </div>
  )
}

/* ---------------------------------------------------------------------------
 * States the list can land in when it has nothing to show.
 * ------------------------------------------------------------------------- */

/** Nothing has ever been uploaded (PRD §5.3 wording). */
function LibraryEmpty({ onPick }: { onPick: () => void }) {
  return (
    <EmptyState
      icon={
        <span aria-hidden="true" style={EMPTY_ICON}>
          ▤
        </span>
      }
      title="Belum ada video. Unggah video pertama"
      description="Pustaka ini menyimpan berkas yang bisa disiarkan ke YouTube. Format yang diterima: MP4, MOV, dan MKV. Setelah unggah selesai, server memeriksa codec dan membuat thumbnail, lalu video bisa dimasukkan ke playlist."
      action={
        <Button onClick={onPick} icon="↑">
          Pilih berkas video
        </Button>
      }
    />
  )
}

/** A search that matched nothing: say which query, and offer the way out. */
function NoResults({ query, onClear }: { query: string; onClear: () => void }) {
  return (
    <EmptyState
      icon={
        <span aria-hidden="true" style={EMPTY_ICON}>
          ≡
        </span>
      }
      title={`Tidak ada video yang cocok dengan “${query}”`}
      description="Pencarian mencocokkan nama berkas saja. Periksa kembali penulisan, atau kosongkan pencarian untuk melihat seluruh pustaka."
      action={
        <Button variant="secondary" onClick={onClear}>
          Hapus pencarian
        </Button>
      }
    />
  )
}

/** The operator deleted the last row of a page beyond the first. */
function EmptyPage({ onBack }: { onBack: () => void }) {
  return (
    <EmptyState
      icon={
        <span aria-hidden="true" style={EMPTY_ICON}>
          ↻
        </span>
      }
      title="Halaman ini sudah kosong"
      description="Tidak ada video tersisa pada halaman ini, tetapi pustaka masih punya isi di halaman lain."
      action={
        <Button variant="secondary" onClick={onBack}>
          Kembali ke halaman 1
        </Button>
      }
    />
  )
}

/* ---------------------------------------------------------------------------
 * Row pieces.
 * ------------------------------------------------------------------------- */

/**
 * Probe issues grouped by severity, worst first, each group headed by a chip
 * carrying the severity word and glyph as well as its colour.
 */
function RowIssues({ issues }: { issues: MediaIssue[] | undefined }) {
  const groups = useMemo(() => {
    const buckets = new Map<string, MediaIssue[]>()
    for (const issue of issues ?? []) {
      const severity = issue.severity || 'notice'
      const bucket = buckets.get(severity)
      if (bucket) bucket.push(issue)
      else buckets.set(severity, [issue])
    }
    return [...buckets.entries()]
      .sort(
        (a, b) =>
          (SEVERITY_RANK[a[0]] ?? UNKNOWN_SEVERITY_RANK) -
          (SEVERITY_RANK[b[0]] ?? UNKNOWN_SEVERITY_RANK),
      )
      .map(([severity, items]) => ({ severity, items }))
  }, [issues])

  if (groups.length === 0) return null

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        marginTop: 8,
        maxWidth: 460,
      }}
    >
      {groups.map((group) => {
        const shown = group.items.slice(0, MAX_ROW_ISSUES)
        const hidden = group.items.length - shown.length
        return (
          <div
            key={group.severity}
            style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}
          >
            <span className={`y-badge ${SEVERITY_TONE[group.severity] ?? 'is-muted'}`}>
              <span aria-hidden="true">{SEVERITY_GLYPH[group.severity] ?? '•'}</span>
              {SEVERITY_LABEL[group.severity] ?? group.severity} · {group.items.length}
            </span>
            <ul
              className="y-issue-list"
              style={{ margin: 0, fontSize: 12, color: 'var(--y-muted)' }}
            >
              {shown.map((issue, i) => (
                <li key={`${issue.field ?? 'field'}-${i}`}>
                  {issue.field ? (
                    <>
                      <span className="y-mono">{issue.field}</span>:{' '}
                    </>
                  ) : null}
                  {issue.message}
                </li>
              ))}
            </ul>
            {hidden > 0 ? (
              <span className="y-row-meta">
                …dan {hidden} catatan lain. Buka Detail untuk daftar lengkap.
              </span>
            ) : null}
          </div>
        )
      })}
    </div>
  )
}

/** Blocking dependencies named by `usage[]`, grouped by what they are (AC-20). */
function BlockerList({ blockers }: { blockers: MediaUsage[] }) {
  const groups = useMemo(() => {
    const buckets = new Map<string, string[]>()
    for (const blocker of blockers) {
      const kind = blocker.kind || 'lainnya'
      const name = blocker.name || blocker.id || '(tanpa nama)'
      const bucket = buckets.get(kind)
      if (bucket) bucket.push(name)
      else buckets.set(kind, [name])
    }
    return [...buckets.entries()]
  }, [blockers])

  return (
    <ul className="y-issue-list" style={{ margin: 0 }}>
      {groups.map(([kind, names]) => (
        <li key={kind}>
          <strong>{USAGE_KIND_LABEL[kind] ?? kind}</strong>
          <ul className="y-list-reset" style={{ marginTop: 2 }}>
            {names.map((name, i) => (
              <li key={`${name}-${i}`} style={{ overflowWrap: 'anywhere' }}>
                {name}
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  )
}

/**
 * Local preview through the byte-range endpoint (FR-03).
 *
 * If the browser cannot decode the container or codec, we fall back to the
 * thumbnail plus an explanation. Realtime transcoding is deliberately not
 * offered: stream copy is the only engine path in this release.
 */
function LocalPreview({ media }: { media: Media }) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [playable, setPlayable] = useState<boolean | null>(null)
  const [started, setStarted] = useState(false)

  return (
    <div className="y-stack">
      {playable === false ? (
        <div className="y-preview-frame">
          {media.has_thumbnail ? (
            <img
              src={getVideoThumbnailUrl(media.id)}
              alt={`Thumbnail ${media.original_name}`}
              style={{ width: '100%', display: 'block' }}
            />
          ) : (
            <div style={{ padding: 16, textAlign: 'center' }}>
              <span className="y-card-note">Thumbnail belum tersedia untuk file ini.</span>
            </div>
          )}
        </div>
      ) : (
        <div className="y-preview-frame">
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video
            ref={videoRef}
            controls
            preload="metadata"
            src={getVideoContentUrl(media.id)}
            onCanPlay={() => setPlayable(true)}
            onError={() => setPlayable(false)}
          />
        </div>
      )}

      {playable === false ? (
        <Banner tone="warn" title="Browser tidak dapat memutar berkas ini">
          <p style={{ margin: 0 }}>
            Codec atau kontainer <span className="y-mono">{media.container}</span> mungkin tidak
            didukung browser ini. Ini tidak berarti file rusak: server tetap dapat dikirim dengan
            stream copy bila codec-nya sesuai profil. Pemeriksaan ulang di server tetap tersedia
            melalui tombol Periksa ulang.
          </p>
        </Banner>
      ) : null}

      {playable === null ? <Loading label="Menyiapkan pratinjau lokal…" /> : null}

      {playable ? (
        <div className="y-btn-row">
          <Button size="sm" onClick={() => videoRef.current?.play()}>
            Putar
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setStarted((s) => !s)}>
            {started ? 'Sembunyikan informasi' : 'Tampilkan informasi berkas'}
          </Button>
        </div>
      ) : null}

      {playable && started ? <MediaDetail media={media} /> : null}
    </div>
  )
}

function MediaDetail({ media }: { media: Media }) {
  return (
    <div className="y-stack">
      <dl className="y-kv">
        <dt>Nama asli</dt>
        <dd>{media.original_name}</dd>
        <dt>Kontainer</dt>
        <dd className="y-mono">{media.container}</dd>
        <dt>Video</dt>
        <dd>
          {media.video_codec ?? '—'} {media.width ?? '—'}×{media.height ?? '—'}
          {media.fps ? ` · ${media.fps.toFixed(2)} fps` : ''}
          {media.pix_fmt ? ` · ${media.pix_fmt}` : ''}
        </dd>
        <dt>Audio</dt>
        <dd>
          {media.audio_codec ?? '—'}
          {media.sample_rate ? ` · ${media.sample_rate} Hz` : ''}
          {media.channels ? ` · ${media.channels} kanal` : ''}
        </dd>
        <dt>Track</dt>
        <dd>
          {media.has_video ? 'Ada video' : 'Tanpa video'}
          {media.has_audio ? ' · ada audio' : ' · tanpa audio'}
        </dd>
        <dt>Bitrate</dt>
        <dd>{media.bitrate ? `${formatNumber(media.bitrate / 1000)} kbps` : 'Tidak tersedia'}</dd>
        <dt>Jarak keyframe</dt>
        <dd>{media.keyframe_max_gap ? `${media.keyframe_max_gap.toFixed(2)} dtk` : 'Tidak tersedia'}</dd>
        <dt>Status penyimpanan</dt>
        <dd>{FILE_STATUS_LABEL[media.file_status] ?? media.file_status}</dd>
        <dt>Kompatibilitas</dt>
        <dd>{COMPAT_LABEL[media.compatibility] ?? media.compatibility}</dd>
        <dt>Diperbarui</dt>
        <dd>{formatDateTime(media.updated_at, Intl.DateTimeFormat().resolvedOptions().timeZone)}</dd>
      </dl>

      {media.issues && media.issues.length > 0 ? (
        <Banner
          tone={media.compatibility === 'incompatible' ? 'error' : 'warn'}
          title="Catatan pemeriksaan"
        >
          <IssueList issues={media.issues} showField />
        </Banner>
      ) : null}

      {media.usage && media.usage.length > 0 ? (
        <Banner tone="warn" title="Dipakai oleh">
          <BlockerList blockers={media.usage} />
        </Banner>
      ) : null}
    </div>
  )
}