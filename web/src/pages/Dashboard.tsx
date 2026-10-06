import { Link } from 'react-router-dom'
import { EmptyState, StatCard, StatusBadge } from '../components'
import { useDashboard } from '../state/useDashboard'
import { useLogs } from '../state/useLogs'
import { usePlaylists } from '../state/usePlaylists'
import { useSchedules } from '../state/useSchedules'
import { useEventStream } from '../api/events'
import { UploadQueue } from './shared/UploadQueue'
import { YouTubePreview } from './shared/YouTubePreview'
import {
  Banner,
  COMPAT_LABEL,
  Loading,
  SCHEDULE_STATUS_LABEL,
  STREAM_STATE_HINT,
  STREAM_STATE_LABEL,
  formatBytes,
  formatClock,
  formatClockTime,
  formatDateTime,
  formatDuration,
  formatKbps,
} from './shared/ui'

const PAGE_SIZE = 12

export default function Dashboard() {
  const { data, loading, error, stale, connected, refresh } = useDashboard()
  const stream = useEventStream()
  const playlists = usePlaylists()
  const schedules = useSchedules()
  const logs = useLogs({ limit: 40 })


  const lastEventIso = stream.lastEventAt ? new Date(stream.lastEventAt).toISOString() : null
  const timezone = data?.instance.timezone || 'UTC'
  const staleStats = stale || data?.system.stale === true

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>{data?.instance.name ?? 'Dashboard'}</h1>
            <p>
              {data ? `Versi ${data.instance.version} · zona waktu ${data.instance.timezone}` : 'Ringkasan server'}
            </p>
          </div>
          <button type="button" className="y-btn is-sm" onClick={() => void refresh()}>
            Muat ulang
          </button>
        </header>

        {!connected ? (
          <Banner tone="warn" title="Sambungan ke server terputus">
            <p style={{ margin: 0 }}>
              Panel tidak menerima pembaruan realtime. Angka di bawah ditampilkan sebagai data terakhir
              yang diketahui
              {lastEventIso ? ` (${formatClockTime(lastEventIso, timezone)})` : ''} dan ditandai
              kedaluwarsa. Siaran yang sedang berjalan tidak ikut berhenti karena sambungan browser
              terputus.
            </p>
          </Banner>
        ) : null}

        {error ? (
          <Banner tone="error" title="Gagal memuat dashboard">
            {error}
          </Banner>
        ) : null}

        {loading && !data ? <Loading label="Memuat ringkasan server…" /> : null}

        {!loading && !data && !error ? (
          <EmptyState title="Belum ada data dashboard" description="Muat ulang halaman untuk mencoba lagi." />
        ) : null}

        {data ? (
          <>
            {/* An active broadcast is the single most important fact on this
                screen. A full-width strip states it plainly, with the elapsed
                time, so the operator never has to hunt for it in the cards. */}
            {data.stream.state === 'STREAMING' ? (
              <div className="y-live-strip" role="status">
                <span className="y-live-strip__dot" aria-hidden="true" />
                <strong className="y-live-strip__title">Siaran sedang berjalan</strong>
                <span className="y-live-strip__meta">
                    {formatClock(data.stream.session_duration)} sejak mulai
                </span>
                {data.stream.item_name ? (
                  <span className="y-live-strip__item">{data.stream.item_name}</span>
                ) : null}
                <Link className="y-btn is-sm" to="/live">
                  Buka Live Control
                </Link>
              </div>
            ) : null}

            {data.stream.state === 'RECONNECTING' ? (
              <Banner tone="warn" title="Publisher sedang menyambung ulang">
                Percobaan ke-{data.stream.attempt}. Sesi tidak melewati waktu akhir jadwal.{' '}
                <Link to="/live">Buka Live Control</Link> untuk melihat rinciannya.
              </Banner>
            ) : null}

            {data.stream.state === 'FAILED' && data.stream.error_message ? (
              <Banner tone="error" title="Sesi terakhir gagal">
                {data.stream.error_message} <Link to="/live">Buka Live Control</Link>
              </Banner>
            ) : null}

            {/* Five primary tiles, matching the reference layout. The three
                timers AC-17 requires to stay distinct (session, connection,
                position) live in the strip below rather than here, so the row
                keeps the reference's proportions instead of wrapping. */}
            <section className="y-stats" aria-label="Statistik sistem">
              <StatCard
                label="CPU"
                value={data.system.cpu_percent === null ? null : data.system.cpu_percent.toFixed(1)}
                unit="%"
                status={data.system.cpu_percent === null ? 'unknown' : data.system.cpu_percent > 90 ? 'error' : 'ok'}
                sparkline={(data.system.history ?? [])
                  .map((p) => p.cpu)
                  .filter((v): v is number => typeof v === 'number')}
                stale={staleStats}
              />
              <StatCard
                label="RAM terpakai"
                value={data.system.mem_used_percent === null ? null : data.system.mem_used_percent.toFixed(1)}
                unit="%"
                status={
                  data.system.mem_used_percent === null
                    ? 'unknown'
                    : data.system.mem_used_percent > 90
                      ? 'error'
                      : 'ok'
                }
                stale={staleStats}
              />
              <StatCard
                label="Suhu"
                value={
                  data.system.temperature_celsius === null
                    ? null
                    : data.system.temperature_celsius.toFixed(1)
                }
                unit="°C"
                status={data.system.temperature_celsius === null ? 'unknown' : 'ok'}
                stale={staleStats}
              />
              {/* Interface upload throughput: distinct from the publisher
                  bitrate shown in the strip below, and labelled so the two are
                  never confused (FR-02). */}
              <StatCard
                label="Upload jaringan (interface)"
                value={
                  data.system.network
                    ? data.system.network.upload_mbps.toFixed(2)
                    : null
                }
                unit="Mbps"
                status={data.system.network ? 'ok' : 'unknown'}
                hint={
                  data.system.network
                    ? `${data.system.network.interface} · seluruh trafik keluar server, bukan hanya media`
                    : 'Antarmuka jaringan tidak dilaporkan.'
                }
                sparkline={(data.system.history ?? [])
                  .map((p) => p.up)
                  .filter((v): v is number => typeof v === 'number')}
                stale={staleStats}
              />
              <StatCard
                label="Status streaming (publisher lokal)"
                value={STREAM_STATE_LABEL[data.stream.state] ?? data.stream.state}
                status={
                  data.stream.state === 'STREAMING'
                    ? 'ok'
                    : data.stream.state === 'FAILED'
                      ? 'error'
                      : data.stream.state === 'RECONNECTING'
                        ? 'warn'
                        : 'idle'
                }
                live={data.stream.state === 'STREAMING'}
                timer={formatClock(data.stream.session_duration)}
                hint={STREAM_STATE_HINT[data.stream.state] ?? ''}
                stale={staleStats}
              />
            </section>

            {data.dependencies.ready ? null : (
              <Banner tone="error" title="FFmpeg belum siap">
                <ul className="y-issue-list">
                  {!data.dependencies.ffmpeg ? <li>ffmpeg tidak ditemukan di server.</li> : null}
                  {!data.dependencies.ffprobe ? <li>ffprobe tidak ditemukan di server.</li> : null}
                </ul>
                <p style={{ margin: '4px 0 0' }}>
                  Mulai siaran dinonaktifkan sampai keduanya terpasang.{' '}
                  <Link to="/settings">Buka Pengaturan</Link> untuk melihat lokasi yang dicari.
                </p>
              </Banner>
            )}

            <div className="y-cols">
              <div className="y-col">
                <section className="y-card" aria-label="Unggah video">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">⇧</span>
                    <h2>Unggah Video</h2>
                    <Link className="y-btn is-sm is-ghost" to="/videos">
                      Pustaka video
                    </Link>
                  </div>
                  <UploadQueue onFinished={() => void refresh()} />
                </section>

                <section className="y-card" aria-label="Playlist">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">≡</span>
                    <h2>Playlist</h2>
                    <Link className="y-btn is-sm is-ghost" to="/playlist">
                      Kelola playlist
                    </Link>
                  </div>
                  {playlists.loading && playlists.items.length === 0 ? <Loading /> : null}
                  {playlists.error ? <Banner tone="error">{playlists.error}</Banner> : null}
                  {!playlists.loading && !playlists.error && playlists.items.length === 0 ? (
                    <EmptyState
                      title="Belum ada playlist"
                      description="Buat playlist pertama di halaman Playlist."
                    />
                  ) : null}
                  {playlists.items.length > 0 ? (
                    <ul className="y-rows">
                      {playlists.items.slice(0, PAGE_SIZE).map((p) => (
                        <li className="y-row" key={p.id}>
                          <div className="y-row-main">
                            <div className="y-row-title">{p.name}</div>
                            <div className="y-row-meta">
                              revisi {p.revision_number} · {p.item_count} video · durasi satu putaran{' '}
                              {formatDuration(p.cycle_duration)}
                            </div>
                          </div>
                          <div className="y-row-actions">
                            <StatusBadge
                              state={p.compatibility}
                              kind="compatibility"
                              label={COMPAT_LABEL[p.compatibility] ?? p.compatibility}
                            />
                          </div>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </section>
              </div>

              <div className="y-col">
                <section className="y-card" aria-label="Video Library">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">▤</span>
                    <h2>Video Library</h2>
                    <Link className="y-btn is-sm is-ghost" to="/videos">
                      Buka pustaka
                    </Link>
                  </div>
                  <dl className="y-kv">
                    <dt>Total file</dt>
                    <dd>{data.media.total}</dd>
                    <dt>Siap</dt>
                    <dd>{data.media.ready}</dd>
                    <dt>Diproses</dt>
                    <dd>{data.media.processing}</dd>
                    <dt>Perlu ditinjau</dt>
                    <dd>{data.media.needs_review}</dd>
                    <dt>Tidak valid</dt>
                    <dd>{data.media.invalid}</dd>
                    <dt>File hilang</dt>
                    <dd>{data.media.missing}</dd>
                    <dt>Kapasitas terpakai</dt>
                    <dd>{formatBytes(data.media.total_bytes)}</dd>
                    <dt>Durasi total</dt>
                    <dd>{formatDuration(data.media.total_duration_seconds)}</dd>
                  </dl>
                  {data.media.missing > 0 ? (
                    <Banner tone="warn" title="Ada file yang hilang">
                      {data.media.missing} file hilang dari penyimpanan. Siaran baru diblokir sampai
                      file diperiksa ulang.
                    </Banner>
                  ) : null}
                </section>

                <section className="y-card" aria-label="Live Schedule">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">◷</span>
                    <h2>Live Schedule</h2>
                    <Link className="y-btn is-sm is-ghost" to="/schedule">
                      Kelola jadwal
                    </Link>
                  </div>
                  {schedules.loading && schedules.items.length === 0 ? <Loading /> : null}
                  {schedules.error ? <Banner tone="error">{schedules.error}</Banner> : null}
                  {!schedules.loading && !schedules.error && schedules.items.length === 0 ? (
                    <EmptyState
                      title="Belum ada jadwal"
                      description="Buat jadwal untuk menjalankan siaran tanpa panel terbuka."
                    />
                  ) : null}
                  {schedules.items.length > 0 ? (
                    <ul className="y-rows">
                      {schedules.items.slice(0, PAGE_SIZE).map((s) => (
                        <li className="y-row" key={s.id}>
                          <div className="y-row-main">
                            <div className="y-row-title">{s.name}</div>
                            <div className="y-row-meta">
                              {formatDateTime(s.start_at, timezone)} → {formatDateTime(s.end_at, timezone)}{' '}
                              ({s.timezone})
                            </div>
                          </div>
                          <div className="y-row-actions">
                            <StatusBadge
                              state={s.status}
                              kind="schedule"
                              label={SCHEDULE_STATUS_LABEL[s.status] ?? s.status}
                            />
                          </div>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </section>
              </div>

              <div className="y-col">
                {/* The reference puts the active-file metadata inside this
                    panel rather than in a panel of its own. */}
                <section className="y-card" aria-label="Live Preview (YouTube)">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">▶</span>
                    <h2>Live Preview (YouTube)</h2>
                  </div>
                  <YouTubePreview
                    previewVideoId={data.youtube.preview_video_id ?? undefined}
                    thumbnailUrl={undefined}
                    thumbnailLabel="Thumbnail video aktif"
                  />
                  <dl className="y-kv y-video-meta">
                    <dt>Video Saat Ini</dt>
                    <dd>{data.stream.item_name ?? 'Tidak ada berkas aktif'}</dd>
                    <dt>Durasi</dt>
                    <dd className="y-mono">
                      {data.stream.item_duration > 0
                        ? formatClock(data.stream.item_duration)
                        : '—'}
                    </dd>
                    <dt>Waktu main</dt>
                    <dd className="y-mono">
                      {formatClock(data.stream.position_seconds)}
                      {data.stream.item_duration > 0
                        ? ` / ${formatClock(data.stream.item_duration)}`
                        : ''}
                    </dd>
                    <dt>Putaran</dt>
                    <dd>
                      {data.stream.item_count > 0
                        ? `${data.stream.cycle} · item ${data.stream.item_index + 1} dari ${data.stream.item_count}`
                        : '—'}
                    </dd>
                    {/* The three clocks AC-17 requires to stay distinguishable. */}
                    <dt>Durasi sesi</dt>
                    <dd className="y-mono">{formatClock(data.stream.session_duration)}</dd>
                    <dt>Durasi koneksi</dt>
                    <dd className="y-mono">{formatClock(data.stream.connection_duration)}</dd>
                    <dt>Bitrate publisher</dt>
                    <dd className="y-mono">{formatKbps(data.stream.publisher_bitrate_kbps)}</dd>
                  </dl>
                  {data.stream.error_message ? (
                    <Banner tone="error" title={`Gagal: ${data.stream.error_code ?? 'tanpa kode'}`}>
                      {data.stream.error_message}
                    </Banner>
                  ) : null}
                  {data.stream.attempt > 0 ? (
                    <Banner tone="warn" title="Menyambung ulang">
                      Percobaan ke-{data.stream.attempt}.
                    </Banner>
                  ) : null}
                </section>

                {/* Stream Control mirrors the reference: three buttons only.
                    The full controls, preflight and confirmations live on the
                    Live Control page, which this panel links to. */}
                <section className="y-card" aria-label="Stream Control">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">●</span>
                    <h2>Stream Control</h2>
                  </div>
                  <div className="y-stream-buttons">
                    <Link className="y-btn is-go" to="/live">
                      Start Live
                    </Link>
                    <Link className="y-btn is-stop" to="/live">
                      Stop Live
                    </Link>
                    <Link className="y-btn" to="/live">
                      Restart Stream
                    </Link>
                  </div>
                  <p className="y-card-note">
                    Perintah lengkap, pemeriksaan preflight, dan konfirmasi ada di halaman Live
                    Control.
                  </p>
                </section>

                <section className="y-card" aria-label="Log FFmpeg (Realtime)">
                  <div className="y-card-head">
                    <span className="y-card-glyph" aria-hidden="true">⎘</span>
                    <h2>Log FFmpeg (Realtime)</h2>
                    <Link className="y-btn is-sm is-ghost" to="/logs">
                      Log lengkap
                    </Link>
                  </div>
                  {logs.loading && logs.items.length === 0 ? <Loading /> : null}
                  {logs.error ? <Banner tone="error">{logs.error}</Banner> : null}
                  {!logs.loading && !logs.error && logs.items.length === 0 ? (
                    <EmptyState title="Belum ada entri log" description="Log publisher muncul di sini." />
                  ) : null}
                  {logs.items.length > 0 ? (
                    <div className="y-log" role="log" aria-label="Log publisher">
                      {logs.items
                        .slice(-12)
                        .reverse()
                        .map((entry, i) => (
                          <div className={`y-log-row is-${entry.level}`} key={`${entry.ts}-${i}`}>
                            <span className="y-log-ts">{formatClockTime(entry.ts, timezone)}</span>
                            <span className="y-lvl">{entry.level.toUpperCase()}</span>
                            <span className="y-source">{entry.source}</span>
                            <span className="y-log-msg">{entry.message}</span>
                          </div>
                        ))}
                    </div>
                  ) : null}
                </section>
              </div>
            </div>
          </>
        ) : null}
      </div>
    </div>
  )
}