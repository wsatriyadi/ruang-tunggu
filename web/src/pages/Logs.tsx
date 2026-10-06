import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, EmptyState } from '../components'
import { clearLogs, errorMessage, exportLogsUrl, getAudit } from '../api/client'
import { useLogs } from '../state/useLogs'
import { useEventStream } from '../api/events'
import type { LogLevel, LogSource } from '../types'
import { Banner, Loading, browserTimeZone, formatClockTime, formatDateTime, timeZoneOptions } from './shared/ui'

const LEVELS: LogLevel[] = ['debug', 'info', 'warn', 'error']
const SOURCES: LogSource[] = ['app', 'scheduler', 'publisher', 'audit', 'upload']

const LEVEL_LABEL: Record<LogLevel, string> = {
  debug: 'Debug',
  info: 'Info',
  warn: 'Peringatan',
  error: 'Galat',
}

const SOURCE_LABEL: Record<LogSource, string> = {
  app: 'Aplikasi',
  scheduler: 'Penjadwal',
  publisher: 'Publisher',
  audit: 'Audit',
  upload: 'Unggahan',
}

/**
 * Level is carried by a word AND a colour, never colour alone (PRD §5.1).
 *
 * The shared sheet scopes its level rules to `.y-log.is-<level>`, a class the
 * container never carries, so the colours are applied here against the same
 * palette the rest of the panel uses.
 */
const LEVEL_COLOR: Record<LogLevel, string> = {
  debug: 'var(--y-muted)',
  info: '#93c5fd',
  warn: '#fde047',
  error: '#fca5b5',
}

const LEVEL_TONE: Record<LogLevel, string> = {
  debug: 'is-muted',
  info: 'is-blue',
  warn: 'is-yellow',
  error: 'is-red',
}

const CHIP_TONE: Record<LogSource, string> = {
  app: 'is-muted',
  scheduler: 'is-blue',
  publisher: 'is-green',
  audit: 'is-yellow',
  upload: 'is-red',
}

const CLEAR_FILTERS_HINT =
  'Hanya satu tampilan yang difilter. Bersihkan filter untuk melihat seluruh log lagi.'

export default function Logs() {
  const [levels, setLevels] = useState<LogLevel[]>([])
  const [sources, setSources] = useState<LogSource[]>([])
  const [sessionId, setSessionId] = useState('')
  const [term, setTerm] = useState('')
  const [query, setQuery] = useState('')
  const [paused, setPaused] = useState(false)
  const [timeZone, setTimeZone] = useState(browserTimeZone())
  const [note, setNote] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [clearing, setClearing] = useState(false)
  const [auditRows, setAuditRows] = useState<Array<{ id: number; action: string; entity_type: string; entity_id: string; created_at: string }>>([])
  const [auditOpen, setAuditOpen] = useState(false)
  // Lines that arrived while paused; cleared when the operator resumes.
  const [pending, setPending] = useState(0)
  const lastSeenCount = useRef(0)

  const params = useMemo(
    () => ({
      levels: levels.join(','),
      sources: sources.join(','),
      session_id: sessionId.trim() || undefined,
      q: query || undefined,
      limit: 300,
    }),
    [levels, query, sessionId, sources],
  )

  const logs = useLogs(params)
  const { connected } = useEventStream()
  const logRef = useRef<HTMLDivElement | null>(null)
  const pinnedToTop = useRef(true)

  // Auto-scroll follows new entries unless the operator scrolled away or
  // paused; either way the view is never yanked back under their cursor.
  useEffect(() => {
    if (paused || !pinnedToTop.current) return
    const el = logRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [logs.items, paused])

  useEffect(() => {
    if (!paused) {
      lastSeenCount.current = logs.items.length
      setPending(0)
    } else if (logs.items.length > lastSeenCount.current) {
      setPending(logs.items.length - lastSeenCount.current)
    }
  }, [logs.items.length, paused])

  const resume = useCallback(() => {
    setPaused(false)
    setPending(0)
    lastSeenCount.current = logs.items.length
    pinnedToTop.current = true
    const el = logRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [logs.items.length])

  const onScroll = useCallback(() => {
    const el = logRef.current
    if (!el) return
    pinnedToTop.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
  }, [])

  const toggle = <T,>(list: T[], value: T, set: (next: T[]) => void) => {
    set(list.includes(value) ? list.filter((v) => v !== value) : [...list, value])
  }

  const onClear = useCallback(async () => {
    setClearing(true)
    setError(null)
    try {
      await clearLogs()
      setNote('Tampilan log di peramban ini dikosongkan. Riwayat log server tidak dihapus.')
      await logs.refresh()
    } catch (err) {
      setError(errorMessage(err, 'Pembersihan tampilan log gagal.'))
    } finally {
      setClearing(false)
    }
  }, [logs])

  const onAudit = useCallback(async () => {
    setAuditOpen(true)
    setError(null)
    try {
      const r = await getAudit(100)
      setAuditRows(
        r.items.map((i) => ({
          id: i.id,
          action: i.action,
          entity_type: i.entity_type,
          entity_id: i.entity_id,
          created_at: i.created_at,
        })),
      )
    } catch (err) {
      setError(errorMessage(err, 'Riwayat audit gagal dimuat.'))
    }
  }, [])

  // Counts come from the loaded set, so they describe what is on screen now.
  const counts = useMemo(() => {
    const byLevel = { debug: 0, info: 0, warn: 0, error: 0 }
    const bySource: Record<LogSource, number> = { app: 0, scheduler: 0, publisher: 0, audit: 0, upload: 0 }
    for (const e of logs.items) {
      byLevel[e.level] += 1
      bySource[e.source] += 1
    }
    return { byLevel, bySource }
  }, [logs.items])

  const filtersActive = levels.length > 0 || sources.length > 0 || query !== '' || sessionId.trim() !== ''

  const resetFilters = useCallback(() => {
    setLevels([])
    setSources([])
    setTerm('')
    setQuery('')
    setSessionId('')
  }, [])

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Log</h1>
            <p>Log aplikasi, penjadwal, publisher, audit, dan unggahan, diperbarui realtime.</p>
          </div>
          <div className="y-btn-row">
            <a
              className="y-btn is-sm"
              href={exportLogsUrl({
                levels: levels.join(','),
                sources: sources.join(','),
                q: query || undefined,
                limit: 2000,
              })}
              download
            >
              Unduh log (sanitasi)
            </a>
            <Button size="sm" variant="ghost" onClick={() => void onAudit()}>
              Riwayat audit
            </Button>
          </div>
        </header>

        <Banner tone="info" title="Ekspor log sudah disanitasi di server">
          Berkas unduhan sudah disanitasi di server: stream key dan token Target disembunyikan
          sebelum pesan sampai ke panel maupun ke berkas. Unduhan memakai filter dan pencarian yang
          sedang aktif, maksimal 2000 baris.
        </Banner>

        {!connected ? (
          <Banner tone="warn" title="Sambungan ke server terputus">
            Entri baru mungkin belum sampai. Daftar di bawah adalah data terakhir yang diterima.
          </Banner>
        ) : null}
        {note ? <Banner tone="info">{note}</Banner> : null}
        {error ? (
          <Banner tone="error" title="Tindakan gagal">
            {error}
          </Banner>
        ) : null}

        <section className="y-card" aria-label="Filter log">
          <form
            className="y-stack"
            onSubmit={(e) => {
              e.preventDefault()
              setQuery(term.trim())
            }}
          >
            <fieldset style={{ border: 'none', padding: 0, margin: 0 }}>
              <legend className="y-hint">
                Level — ketuk untuk menyaring. Tidak ada yang dipilih berarti semua level.
              </legend>
              <div className="y-chips">
                {LEVELS.map((l) => {
                  const on = levels.includes(l)
                  return (
                    <button
                      key={l}
                      type="button"
                      className={`y-chip ${LEVEL_TONE[l]}`}
                      aria-pressed={on}
                      onClick={() => toggle(levels, l, setLevels)}
                    >
                      <span aria-hidden="true">{on ? '✓' : '○'}</span>
                      {LEVEL_LABEL[l]}
                      <span className="y-mono" style={{ opacity: 0.75 }}>
                        {counts.byLevel[l]}
                      </span>
                      <span className="y-sr">{on ? 'difilter' : 'tidak difilter'}</span>
                    </button>
                  )
                })}
              </div>
            </fieldset>

            <fieldset style={{ border: 'none', padding: 0, margin: 0 }}>
              <legend className="y-hint">
                Sumber — ketuk untuk menyaring. Tidak ada yang dipilih berarti semua sumber.
              </legend>
              <div className="y-chips">
                {SOURCES.map((s) => {
                  const on = sources.includes(s)
                  return (
                    <button
                      key={s}
                      type="button"
                      className={`y-chip ${CHIP_TONE[s]}`}
                      aria-pressed={on}
                      onClick={() => toggle(sources, s, setSources)}
                    >
                      <span aria-hidden="true">{on ? '✓' : '○'}</span>
                      {SOURCE_LABEL[s]}
                      <span className="y-mono" style={{ opacity: 0.75 }}>
                        {counts.bySource[s]}
                      </span>
                      <span className="y-sr">{on ? 'difilter' : 'tidak difilter'}</span>
                    </button>
                  )
                })}
              </div>
            </fieldset>

            <div className="y-form">
              <div className="y-field">
                <label htmlFor="log-session">ID sesi</label>
                <input
                  id="log-session"
                  className="y-input"
                  value={sessionId}
                  placeholder="Kosongkan untuk semua sesi"
                  onChange={(e) => setSessionId(e.target.value)}
                />
              </div>
              <div className="y-field">
                <label htmlFor="log-q">Cari pesan</label>
                <input
                  id="log-q"
                  className="y-input"
                  type="search"
                  value={term}
                  onChange={(e) => setTerm(e.target.value)}
                />
              </div>
              <div className="y-field">
                <label htmlFor="log-tz">Zona waktu tampilan</label>
                <select
                  id="log-tz"
                  className="y-select"
                  value={timeZone}
                  onChange={(e) => setTimeZone(e.target.value)}
                >
                  {timeZoneOptions(timeZone).map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="y-btn-row">
              <Button type="submit">Terapkan pencarian</Button>
              {paused ? (
                <Button type="button" variant="primary" onClick={resume}>
                  Lanjutkan gulir{pending > 0 ? ` (${pending} baris baru)` : ''}
                </Button>
              ) : (
                <Button type="button" variant="ghost" aria-pressed={false} onClick={() => setPaused(true)}>
                  Jeda gulir otomatis
                </Button>
              )}
              <Button type="button" variant="ghost" onClick={() => void logs.refresh()}>
                Muat ulang
              </Button>
              {filtersActive ? (
                <Button type="button" variant="ghost" onClick={resetFilters}>
                  Hapus semua filter
                </Button>
              ) : null}
              <Button type="button" variant="danger" busy={clearing} onClick={() => void onClear()}>
                Bersihkan tampilan
              </Button>
            </div>
          </form>

          {paused ? (
            <Banner tone="warn" title="Gulir otomatis dijeda">
              Daftar tidak ikut bergulir ke baris terbaru. Entri baru tetap diterima dan
              {pending > 0 ? ` sudah bertambah ${pending} baris` : ' akan dihitung di sini'}. Tekan
              Lanjutkan gulir untuk langsung ikut bergulir.
            </Banner>
          ) : null}

          <Banner tone="warn" title="Apa arti Bersihkan tampilan">
            Perintah ini hanya mengosongkan tampilan log di peramban ini. Riwayat log server tidak
            dihapus dan tetap tersedia sampai kedaluwarsa menurut kebijakan retensi di Pengaturan.
          </Banner>
        </section>
        <section className="y-card" aria-label="Daftar entri log">
          <div className="y-card-head">
            <h2>Entri</h2>
            <span className="y-row-meta">
              {logs.items.length} baris{filtersActive ? ' (difilter)' : ''} · zona {timeZone}
            </span>
            {paused ? <span className="y-badge is-yellow">Dijeda</span> : null}
          </div>

          {logs.loading && logs.items.length === 0 ? <Loading label="Memuat log…" /> : null}
          {logs.error ? (
            <Banner tone="error" title="Gagal memuat log">
              {logs.error}
            </Banner>
          ) : null}

          {!logs.loading && !logs.error && logs.items.length === 0 ? (
            filtersActive ? (
              <EmptyState
                title="Tidak ada entri yang cocok dengan filter"
                description={CLEAR_FILTERS_HINT}
                action={
                  <Button variant="primary" onClick={resetFilters}>
                    Hapus semua filter
                  </Button>
                }
              />
            ) : (
              <EmptyState
                title="Belum ada entri log"
                description="Log aplikasi, penjadwal, publisher, audit, dan unggahan akan muncul di sini begitu server menuliskannya."
              />
            )
          ) : null}

          {logs.items.length > 0 ? (
            <>
              {paused && pending > 0 ? (
                <button
                  type="button"
                  className="y-btn is-primary is-sm"
                  style={{ alignSelf: 'flex-start' }}
                  onClick={resume}
                >
                  ↓ {pending} baris baru — klik untuk melanjutkan
                </button>
              ) : null}
              <div
                className="y-log"
                ref={logRef}
                onScroll={onScroll}
                role="log"
                aria-label="Daftar entri log"
                aria-live={paused ? 'off' : 'polite'}
              >
                {logs.items.map((entry, i) => (
                  <div className={`y-log-row is-${entry.level}`} key={`${entry.ts}-${i}`}>
                    <span className="y-log-ts" style={{ textAlign: 'right' }}>
                      {formatClockTime(entry.ts, timeZone)}
                    </span>
                    <span
                      className={`y-lvl ${LEVEL_TONE[entry.level]}`}
                      style={{ color: LEVEL_COLOR[entry.level] }}
                    >
                      {LEVEL_LABEL[entry.level] ?? entry.level}
                    </span>
                    <span className="y-source">{SOURCE_LABEL[entry.source] ?? entry.source}</span>
                    <span className="y-log-msg">
                      {entry.message}
                      {entry.session_id ? (
                        <span className="y-mono"> · sesi {entry.session_id}</span>
                      ) : null}
                    </span>
                  </div>
                ))}
              </div>
            </>
          ) : null}
        </section>
      </div>
      {auditOpen ? (
        <div className="y-overlay" role="dialog" aria-modal="true" aria-label="Riwayat audit">
          <div className="y-modal">
            <h2>Riwayat audit operator</h2>
            {auditRows.length === 0 ? (
              <p className="y-card-note">Belum ada tindakan operator tercatat.</p>
            ) : (
              <div className="y-scroll" style={{ maxHeight: 320 }}>
                <table className="y-table">
                  <thead>
                    <tr>
                      <th scope="col">Waktu</th>
                      <th scope="col">Tindakan</th>
                      <th scope="col">Objek</th>
                    </tr>
                  </thead>
                  <tbody>
                    {auditRows.map((row) => (
                      <tr key={row.id}>
                        <td className="y-mono">{formatDateTime(row.created_at, timeZone)}</td>
                        <td>{row.action}</td>
                        <td className="y-name">
                          {row.entity_type} {row.entity_id}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="y-modal-foot">
              <button type="button" className="y-btn" onClick={() => setAuditOpen(false)}>
                Tutup
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}