import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Modal, StatusBadge } from '../components'
import {
  errorMessage,
  getSettings,
  getStreamStatus,
  getVideoThumbnailUrl,
  isApiError,
  newIdempotencyKey,
  preflight,
  restartStream,
  startStream,
  stopStream,
} from '../api/client'
import { useEventStream } from '../api/events'
import { usePlaylists } from '../state/usePlaylists'
import type { DependencyStatus, PreflightResult, Progress, YouTubeStatus } from '../types'
import { YouTubePreview } from './shared/YouTubePreview'
import {
  ACTIVE_STATE,
  Banner,
  IssueList,
  Loading,
  PLAYBACK_MODES,
  STREAM_STATE_HINT,
  STREAM_STATE_LABEL,
  SafeThumbnail,
  formatClock,
  formatClockTime,
  formatKbps,
} from './shared/ui'

type PlaybackMode = 'sequential' | 'random' | 'random_no_repeat'

/** Fallback when the status endpoint cannot be reached (e.g. SSE dropped). */
const IDLE_PROGRESS: Progress = {
  session_id: '',
  state: 'IDLE',
  desired_state: 'idle',
  cycle: 0,
  item_index: 0,
  item_count: 0,
  position_seconds: 0,
  item_duration: 0,
  session_duration: 0,
  connection_duration: 0,
  attempt: 0,
  publisher_bitrate_kbps: 0,
  media_time_seconds: 0,
  started_at: '',
  updated_at: '',
}

export default function LiveControl() {
  const playlists = usePlaylists()
  const { connected, lastEventAt, subscribeTo } = useEventStream()

  const [progress, setProgress] = useState<Progress>(IDLE_PROGRESS)
  const [deps, setDeps] = useState<DependencyStatus | null>(null)
  const [youtube, setYoutube] = useState<YouTubeStatus | null>(null)
  const [previewVideoId, setPreviewVideoId] = useState<string | undefined>(undefined)
  const [runningScheduleId, setRunningScheduleId] = useState<string | null>(null)

  const [playlistId, setPlaylistId] = useState('')
  const [mode, setMode] = useState<PlaybackMode>('sequential')
  const [loop, setLoop] = useState(true)

  const [check, setCheck] = useState<PreflightResult | null>(null)
  const [checking, setChecking] = useState(false)
  const [pendingStart, setPendingStart] = useState(false)
  const [confirm, setConfirm] = useState<'stop' | 'restart' | null>(null)
  const [busy, setBusy] = useState<'start' | 'stop' | 'restart' | null>(null)
  const [error, setError] = useState<string | null>(null)
  // One idempotency key per user intent (AC-10): generated when the operator
  // asks to start, reused by the confirmation retry, discarded on success.
  const startKey = useRef<string | null>(null)
  const stopKey = useRef<string | null>(null)
  const restartKey = useRef<string | null>(null)

  const selected = playlists.items.find((p) => p.id === playlistId) ?? null

  // The session payload names the file but carries no image path, so the
  // thumbnail for the item on air is resolved from the playlist the operator
  // selected, matched on the active file name so a stale selection cannot
  // show the wrong image.
  const activeThumbnail = (() => {
    const item = selected?.items?.[progress.item_index]
    if (!item?.has_thumbnail) return undefined
    if (progress.item_name && item.name !== progress.item_name) return undefined
    return getVideoThumbnailUrl(item.media_id)
  })()

  const pollStatus = useCallback(async () => {
    try {
      const status = await getStreamStatus()
      setProgress(status.stream)
      setDeps(status.dependencies)
      setYoutube(status.youtube)
      setRunningScheduleId(status.running_schedule?.id ?? null)
    } catch {
      // Keep the last known state; the disconnect banner explains staleness.
    }
  }, [])

  useEffect(() => {
    void pollStatus()
    const timer = window.setInterval(() => void pollStatus(), 5000)
    return () => window.clearInterval(timer)
  }, [pollStatus])

  useEffect(
    () =>
      subscribeTo('stream_state', (next: Progress) => {
        setProgress(next)
      }),
    [subscribeTo],
  )

  useEffect(() => {
    if (playlists.items.length > 0 && !playlistId) setPlaylistId(playlists.items[0]?.id ?? '')
  }, [playlistId, playlists.items])

  // The preview id lives in settings; read it once for the embed.
  useEffect(() => {
    let cancelled = false
    void getSettings()
      .then((r) => {
        if (!cancelled) setPreviewVideoId(r.settings.preview_video_id || undefined)
      })
      .catch(() => {
        if (!cancelled) setPreviewVideoId(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Preflight runs before every start attempt, and again whenever the operator
  // changes the selection (FR-06).
  const runPreflight = useCallback(async () => {
    setChecking(true)
    setError(null)
    try {
      const result = await preflight({
        revision_id: selected?.revision_id,
        mode,
        loop,
      })
      setCheck(result)
      return result
    } catch (err) {
      setCheck(null)
      setError(errorMessage(err, 'Preflight gagal dijalankan.'))
      return null
    } finally {
      setChecking(false)
    }
  }, [loop, mode, selected?.revision_id])

  const start = useCallback(async () => {
    // Run preflight again at the moment of intent: the selection may have
    // changed since the operator last looked at it.
    const result = await runPreflight()
    if (!result) return
    if (!result.ok) {
      setPendingStart(false)
      setError(result.message ?? 'Preflight gagal.')
      return
    }
    setBusy('start')
    setError(null)
    if (!startKey.current) startKey.current = newIdempotencyKey()
    try {
      await startStream({
        revision_id: selected?.revision_id,
        mode,
        loop,
        idempotency_key: startKey.current,
      })
      startKey.current = null
      setPendingStart(false)
      await pollStatus()
    } catch (err) {
      if (isApiError(err)) {
        setError(`${err.message}${err.code ? ` (${err.code})` : ''}`)
        // A 409 from start means preflight failed server-side; show its detail.
        setPendingStart(false)
      } else {
        setError(errorMessage(err, 'Permintaan mulai gagal.'))
      }
      // The key is kept: a retry of the same intent must not start twice.
    } finally {
      setBusy(null)
    }
  }, [loop, mode, pollStatus, runPreflight, selected?.revision_id])

  const stop = useCallback(async () => {
    setBusy('stop')
    setError(null)
    if (!stopKey.current) stopKey.current = newIdempotencyKey()
    try {
      await stopStream({ idempotency_key: stopKey.current })
      stopKey.current = null
      setConfirm(null)
      await pollStatus()
    } catch (err) {
      setError(errorMessage(err, 'Permintaan berhenti gagal.'))
    } finally {
      setBusy(null)
    }
  }, [pollStatus])

  const restart = useCallback(async () => {
    setBusy('restart')
    setError(null)
    if (!restartKey.current) restartKey.current = newIdempotencyKey()
    try {
      await restartStream({ idempotency_key: restartKey.current })
      restartKey.current = null
      setConfirm(null)
      await pollStatus()
    } catch (err) {
      setError(errorMessage(err, 'Permintaan mulai ulang gagal.'))
    } finally {
      setBusy(null)
    }
  }, [pollStatus])

  const depsReady = deps?.ready ?? false
  const active = !!ACTIVE_STATE[progress.state]
  const canStart = depsReady && !active && !!selected && selected.item_count > 0 && !busy
  const blockedByDeps = !depsReady
  const missing: string[] = deps
    ? [
        ...(deps.ffmpeg ? [] : ['ffmpeg']),
        ...(deps.ffprobe ? [] : ['ffprobe']),
      ]
    : []

  const retryInSeconds = progress.next_retry_at
    ? Math.max(0, Math.round((new Date(progress.next_retry_at).getTime() - Date.now()) / 1000))
    : null

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Live Control</h1>
            <p>
              Status di bawah adalah publisher lokal. Status tayang di YouTube tidak dapat diverifikasi
              tanpa integrasi API.
            </p>
          </div>
        </header>

        {!connected ? (
          <Banner tone="warn" title="Sambungan ke server terputus">
            <p style={{ margin: 0 }}>
              Status terakhir yang diketahui
              {lastEventAt ? ` (${formatClockTime(new Date(lastEventAt).toISOString(), 'UTC')})` : ''}
              . Menghentikan browser tidak menghentikan siaran; panel menyambung ulang sendiri.
            </p>
          </Banner>
        ) : null}

        {error ? (
          <Banner tone="error" title="Tindakan gagal">
            {error}
          </Banner>
        ) : null}

        {blockedByDeps ? (
          <Banner tone="error" title="Mulai siaran dinonaktifkan">
            <p style={{ margin: '0 0 4px' }}>
              Server belum menemukan {missing.length > 0 ? missing.join(' dan ') : 'dependensi'}.
              Pasang paket FFmpeg yang memuat kedua executable, atau tentukan lokasi manual di
              halaman Pengaturan.
            </p>
            {deps ? (
              <dl className="y-kv">
                <dt>ffmpeg</dt>
                <dd className="y-mono">{deps.ffmpeg_path || 'tidak ditemukan'}</dd>
                <dt>ffprobe</dt>
                <dd className="y-mono">{deps.ffprobe_path || 'tidak ditemukan'}</dd>
              </dl>
            ) : null}
          </Banner>
        ) : null}

        <section className="y-card" aria-label="Kontrol siaran">
          <div className="y-card-head">
            <h2>Mulai Siaran</h2>
          </div>

          <div className="y-form">
            <div className="y-field">
              <label htmlFor="live-playlist">Playlist</label>
              <select
                id="live-playlist"
                className="y-select"
                value={playlistId}
                disabled={active}
                onChange={(e) => {
                  setPlaylistId(e.target.value)
                  setCheck(null)
                }}
              >
                <option value="">— pilih playlist —</option>
                {playlists.items.map((p) => (
                  <option key={p.id} value={p.id} disabled={p.item_count === 0}>
                    {p.name} — revisi {p.revision_number} ({p.item_count} video)
                  </option>
                ))}
              </select>
              {selected ? (
                <span className="y-hint">
                  Sesi memakai snapshot revisi {selected.revision_number}; perubahan playlist
                  berikutnya tidak memengaruhi sesi berjalan.
                </span>
              ) : null}
            </div>

            <div className="y-field">
              <label htmlFor="live-mode">Mode pemutaran</label>
              <select
                id="live-mode"
                className="y-select"
                value={mode}
                disabled={active}
                onChange={(e) => {
                  setMode(e.target.value as PlaybackMode)
                  setCheck(null)
                }}
              >
                {PLAYBACK_MODES.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </select>
            </div>

            <div className="y-check">
              <input
                id="live-loop"
                type="checkbox"
                checked={loop}
                disabled={active}
                onChange={(e) => {
                  setLoop(e.target.checked)
                  setCheck(null)
                }}
              />
              <label htmlFor="live-loop" className="y-checklabel">
                <span>Loop</span>
                <span className="y-hint">Mati: sesi berhenti setelah satu putaran.</span>
              </label>
            </div>
          </div>

          <div className="y-btn-row">
            <Button
              variant="primary"
              busy={busy === 'start'}
              disabled={!canStart}
              onClick={() => setPendingStart(true)}
              title={
                blockedByDeps
                  ? 'FFmpeg/ffprobe belum tersedia di server'
                  : active
                    ? 'Sudah ada sesi aktif'
                    : !selected || selected.item_count === 0
                      ? 'Playlist kosong'
                      : undefined
              }
            >
              Start Live
            </Button>
            <Button
              variant="danger"
              busy={busy === 'stop'}
              disabled={!active || busy !== null}
              onClick={() => setConfirm('stop')}
            >
              Stop
            </Button>
            <Button
              variant="ghost"
              busy={busy === 'restart'}
              disabled={!active || busy !== null}
              onClick={() => setConfirm('restart')}
            >
              Restart Stream
            </Button>
          </div>

          {active ? (
            <p className="y-card-note">
              Stop dan Restart pada sesi aktif meminta konfirmasi singkat. Tombol dinonaktifkan
              selama permintaannya berjalan.
            </p>
          ) : null}
        </section>

        {check ? (
          <section className="y-card" aria-label="Hasil pemeriksaan sebelum mulai">
            <div className="y-card-head">
              <h2>Pemeriksaan sebelum mulai</h2>
              <Button size="sm" variant="ghost" busy={checking} onClick={() => void runPreflight()}>
                Periksa ulang
              </Button>
            </div>
            {check.ok ? (
              <Banner tone="info" title="Pemeriksaan lulus">
                Playlist, berkas, kredensial tujuan, executable, dan ruang penyimpanan sudah
                diperiksa server. Siaran dapat diteruskan.
              </Banner>
            ) : (
              <Banner tone="error" title={check.message ?? 'Pemeriksaan gagal'}>
                {check.code ? <p style={{ margin: '0 0 6px' }}>Kode: {check.code}</p> : null}
                <IssueList issues={check.issues ?? []} showField />
                {check.issues && check.issues.length === 0 ? (
                  <p style={{ margin: 0 }}>Server tidak merincikan masalahnya secara spesifik.</p>
                ) : null}
              </Banner>
            )}
            {check.warnings && check.warnings.length > 0 ? (
              <Banner tone="warn" title="Peringatan">
                <IssueList issues={check.warnings.map((w) => ({ message: w }))} />
              </Banner>
            ) : null}
          </section>
        ) : null}

        <section className="y-card" aria-label="Kemajuan sesi">
          <div className="y-card-head">
            <h2>Kemajuan Sesi</h2>
            <StatusBadge
              state={progress.state}
              kind="stream"
              label={STREAM_STATE_LABEL[progress.state] ?? progress.state}
            />
          </div>

          <p className="y-card-note">{STREAM_STATE_HINT[progress.state] ?? ''}</p>

          <dl className="y-kv">
            {/* Session duration, connection duration and video position are three
                different values and are labelled separately (AC-17). */}
            <dt>Durasi sesi</dt>
            <dd className="y-mono">{formatClock(progress.session_duration)}</dd>
            <dt>Durasi koneksi</dt>
            <dd className="y-mono">{formatClock(progress.connection_duration)}</dd>
            <dt>Posisi video</dt>
            <dd className="y-mono">
              {formatClock(progress.position_seconds)} / {formatClock(progress.item_duration)}
            </dd>
            <dt>Putaran</dt>
            <dd>{progress.cycle || '—'}</dd>
            <dt>Item</dt>
            <dd>
              {progress.item_count > 0
                ? `${progress.item_index + 1} dari ${progress.item_count}`
                : '—'}
            </dd>
            <dt>Bitrate publisher</dt>
            <dd>{formatKbps(progress.publisher_bitrate_kbps)}</dd>
            <dt>Media terkirim</dt>
            <dd className="y-mono">{formatClock(progress.media_time_seconds)}</dd>
            <dt>Berkas aktif</dt>
            <dd>{progress.item_name ?? 'Tidak ada'}</dd>
            <dt>Percobaan</dt>
            <dd>{progress.attempt || '—'}</dd>
            <dt>ID sesi</dt>
            <dd className="y-mono">{progress.session_id || '—'}</dd>
            <dt>Waktu server</dt>
            <dd>{progress.updated_at ? formatClockTime(progress.updated_at, 'UTC') : '—'}</dd>
          </dl>

          <SafeThumbnail
            src={activeThumbnail}
            alt="Thumbnail video aktif"
            fallbackLabel="Thumbnail video aktif belum tersedia."
          />

          {progress.state === 'RECONNECTING' ? (
            <Banner tone="warn" title="Menyambung ulang">
              Percobaan ke-{progress.attempt}
              {retryInSeconds !== null ? `, percobaan berikutnya dalam ${retryInSeconds} detik` : ''}
              . Sesi tidak melewati waktu akhir jadwal.
            </Banner>
          ) : null}

          {progress.error_message || progress.error_code ? (
            <Banner tone="error" title={progress.error_code ?? 'Kegagalan'}>
              {progress.error_message ?? 'Tanpa keterangan tambahan.'}
            </Banner>
          ) : null}

          {runningScheduleId ? (
            <Banner tone="info" title="Sesi dipicu oleh jadwal">
              Sesi ini berjalan karena jadwal terpicu. Menghentikannya tetap dilakukan dengan Stop.
            </Banner>
          ) : null}
        </section>

        <section className="y-card" aria-label="Pratinjau YouTube">
          <div className="y-card-head">
            <h2>Pratinjau</h2>
          </div>
          <YouTubePreview
            previewVideoId={previewVideoId}
            thumbnailUrl={activeThumbnail}
            thumbnailLabel="Thumbnail video aktif"
          />
          {youtube ? (
            <Banner tone="info" title="Status tayang belum diverifikasi">
              {youtube.message}
            </Banner>
          ) : null}
        </section>
      </div>

      <Modal
        open={pendingStart}
        title="Mulai siaran"
        onClose={() => setPendingStart(false)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setPendingStart(false)}>
              Batal
            </Button>
            <Button
              variant="primary"
              busy={busy === 'start'}
              disabled={check ? !check.ok : checking}
              onClick={() => void start()}
            >
              Ya, mulai
            </Button>
          </>
        }
      >
        {checking && !check ? <Loading label="Menjalankan pemeriksaan sebelum mulai…" /> : null}
        {check && !check.ok ? (
          <Banner tone="error" title="Mulai diblokir oleh pemeriksaan server">
            <IssueList issues={check.issues ?? []} showField />
          </Banner>
        ) : (
          <p>
            Sesi akan memakai playlist <strong>{selected?.name ?? '—'}</strong> revisi{' '}
            {selected?.revision_number ?? '—'} dengan mode{' '}
            <strong>{PLAYBACK_MODES.find((m) => m.value === mode)?.label ?? mode}</strong> dan loop{' '}
            <strong>{loop ? 'aktif' : 'mati'}</strong>.
          </p>
        )}
      </Modal>

      <Modal
        open={confirm === 'stop'}
        title="Hentikan sesi?"
        onClose={() => setConfirm(null)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirm(null)}>
              Batal
            </Button>
            <Button variant="danger" busy={busy === 'stop'} onClick={() => void stop()}>
              Ya, hentikan
            </Button>
          </>
        }
      >
        <p>
          Sesi saat ini akan dihentikan, termasuk saat sedang menyambung ulang. Niat berhenti
          menang atas percobaan yang belum dimulai. Berhentinya publisher lokal tidak menjamin
          broadcast YouTube berakhir pada detik yang sama.
        </p>
      </Modal>

      <Modal
        open={confirm === 'restart'}
        title="Mulai ulang stream?"
        onClose={() => setConfirm(null)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirm(null)}>
              Batal
            </Button>
            <Button busy={busy === 'restart'} onClick={() => void restart()}>
              Ya, mulai ulang
            </Button>
          </>
        }
      >
        <p>
          Publisher yang berjalan akan dihentikan dan diganti proses baru pada sesi yang sama.
          Posisi video mengikuti file yang dimulai ulang, sehingga durasi sesi dan durasi koneksi
          akan berbeda setelah restart.
        </p>
      </Modal>
    </div>
  )
}