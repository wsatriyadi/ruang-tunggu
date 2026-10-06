import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, EmptyState, IconButton, Modal } from '../components'
import {
  createSchedule,
  deleteSchedule,
  errorMessage,
  getSettings,
  isApiError,
  updateSchedule,
} from '../api/client'
import { usePlaylists } from '../state/usePlaylists'
import { useSchedules } from '../state/useSchedules'
import type { Schedule } from '../types'
import {
  Banner,
  Loading,
  PLAYBACK_MODES,
  detailArray,
  formatDateTime,
  isoToLocalInput,
  localInputToIsoWithOffset,
  playbackModeHelp,
  timeZoneOptions,
} from './shared/ui'

type PlaybackMode = 'sequential' | 'random' | 'random_no_repeat'

interface FormState {
  id: string | null
  name: string
  playlistId: string
  startLocal: string
  endLocal: string
  timezone: string
  mode: PlaybackMode
  loop: boolean
  autoReconnect: boolean
  enabled: boolean
}

const EMPTY: FormState = {
  id: null,
  name: '',
  playlistId: '',
  startLocal: '',
  endLocal: '',
  timezone: 'UTC',
  mode: 'sequential',
  loop: true,
  autoReconnect: true,
  enabled: true,
}

/** One blocking schedule from a 409 `SCHEDULE_CONFLICT` details payload. */
interface ScheduleConflict {
  id: string
  name: string
  startLocal: string
  endLocal: string
}

/* ---------------------------------------------------------------------------
 * Status presentation. Every status carries a distinct label, glyph and tone,
 * plus a one-line explanation: colour alone never carries the meaning
 * (PRD §5.1).
 * ------------------------------------------------------------------------- */

interface StatusPresentation {
  label: string
  glyph: string
  /** Class on `y-badge`, one per status so none are told apart by hue alone. */
  tone: string
  help: string
}

const STATUS_PRESENTATION: Record<string, StatusPresentation> = {
  disabled: {
    label: 'Nonaktif',
    glyph: '○',
    tone: 'is-muted',
    help: 'Tidak dipicu. Sesi yang sedang berjalan tidak ikut berhenti.',
  },
  scheduled: {
    label: 'Terjadwal',
    glyph: '◔',
    tone: 'is-blue',
    help: 'Menunggu waktu mulai. Server akan memicu sesi sendiri.',
  },
  running: {
    label: 'Berjalan',
    glyph: '●',
    tone: 'is-green',
    help: 'Sesi aktif sedang mengirim. Penghentian dilakukan dari Live Control.',
  },
  completed: {
    label: 'Selesai',
    glyph: '✓',
    tone: 'is-green',
    help: 'Selesai normal: playlist habis atau waktu akhir tercapai.',
  },
  stopped: {
    label: 'Dihentikan',
    glyph: '■',
    tone: 'is-yellow',
    help: 'Dihentikan operator lewat Stop Live sebelum waktu akhir.',
  },
  failed: {
    label: 'Gagal',
    glyph: '✕',
    tone: 'is-red',
    help: 'Kegagalan terminal; diperlukan tindakan operator. Periksa Log.',
  },
  missed: {
    label: 'Terlewat',
    glyph: '!',
    tone: 'is-yellow',
    help: 'Waktu mulai sudah lewat tanpa sempat dijalankan, atau pemulihan dimatikan.',
  },
  skipped_busy: {
    label: 'Dilewati (sibuk)',
    glyph: '»',
    tone: 'is-yellow',
    help: 'Sesi manual masih aktif saat jadwal tiba, sehingga jadwal ini dilewati.',
  },
}

const STATUS_FALLBACK: StatusPresentation = {
  label: 'Tidak diketahui',
  glyph: '•',
  tone: 'is-muted',
  help: 'Status tidak dikenali. Muat ulang halaman atau periksa versi server.',
}

/** Shared chrome for the four labelled fieldset groups in the schedule form. */
const FIELD_GROUP_STYLE = {
  border: '1px solid var(--y-line)',
  borderRadius: 'var(--radius-md)',
  padding: '12px',
  margin: 0,
  minWidth: 0,
} as const

const FIELD_GROUP_LEGEND_STYLE = {
  padding: '0 6px',
  fontSize: '12px',
  fontWeight: 700,
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  color: 'var(--y-muted)',
} as const

export default function Schedule() {
  const schedules = useSchedules()
  const playlists = usePlaylists()
  const [form, setForm] = useState<FormState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [conflicts, setConflicts] = useState<ScheduleConflict[]>([])
  const [runningBlock, setRunningBlock] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [settingsTimezone, setSettingsTimezone] = useState<string>('')

  useEffect(() => {
    void getSettings()
      .then((r) => setSettingsTimezone(r.settings.timezone || 'UTC'))
      .catch(() => setSettingsTimezone('UTC'))
  }, [])

  const openCreate = useCallback(() => {
    setError(null)
    setConflicts([])
    setRunningBlock(null)
    setForm({ ...EMPTY, timezone: settingsTimezone || 'UTC' })
  }, [settingsTimezone])

  const openEdit = useCallback(
    (schedule: Schedule) => {
      setError(null)
      setConflicts([])
      setRunningBlock(null)
      const playlist = playlists.items.find((p) => p.revision_id === schedule.revision_id)
      setForm({
        id: schedule.id,
        name: schedule.name,
        playlistId: playlist?.id ?? '',
        startLocal: isoToLocalInput(schedule.start_at, schedule.timezone),
        endLocal: isoToLocalInput(schedule.end_at, schedule.timezone),
        timezone: schedule.timezone,
        mode: schedule.playback_mode,
        loop: schedule.loop_enabled,
        autoReconnect: schedule.auto_reconnect,
        enabled: schedule.enabled,
      })
    },
    [playlists.items],
  )

  const selectedPlaylist = useMemo(
    () => playlists.items.find((p) => p.id === form?.playlistId) ?? null,
    [form?.playlistId, playlists.items],
  )

  const submit = useCallback(async () => {
    if (!form) return
    setSaving(true)
    setError(null)
    setConflicts([])
    setRunningBlock(null)
    try {
      // The zone is carried in the ISO string so the server stores the instant
      // the operator meant, not the browser's local time.
      const start_at = localInputToIsoWithOffset(form.startLocal, form.timezone)
      const end_at = localInputToIsoWithOffset(form.endLocal, form.timezone)
      if (new Date(end_at).getTime() <= new Date(start_at).getTime()) {
        setError('Waktu selesai harus lebih besar daripada waktu mulai.')
        return
      }
      const playlist = playlists.items.find((p) => p.id === form.playlistId)
      if (!playlist) {
        setError('Pilih playlist terlebih dahulu.')
        return
      }
      const body = {
        name: form.name.trim() || playlist.name,
        revision_id: playlist.revision_id,
        start_at,
        end_at,
        timezone: form.timezone,
        playback_mode: form.mode,
        loop_enabled: form.loop,
        auto_reconnect: form.autoReconnect,
        enabled: form.enabled,
      }
      if (form.id) {
        await updateSchedule(form.id, body)
      } else {
        await createSchedule(body)
      }
      setForm(null)
      await schedules.refresh()
    } catch (err) {
      if (isApiError(err)) {
        if (err.code === 'SCHEDULE_CONFLICT') {
          // Each blocking schedule keeps its own fields so the banner can name
          // it and show its window instead of dumping raw JSON.
          setConflicts(
            detailArray(err.details, 'conflicting').map((c) => ({
              id: String(c.id ?? c.schedule_id ?? ''),
              name: String(c.name ?? 'Jadwal tanpa nama'),
              startLocal: c.start_at ? formatDateTime(String(c.start_at), form.timezone) : '—',
              endLocal: c.end_at ? formatDateTime(String(c.end_at), form.timezone) : '—',
            })),
          )
        } else if (err.code === 'SCHEDULE_RUNNING') {
          setRunningBlock(err.message)
        } else {
          setError(`${err.message} (${err.code})`)
        }
      } else {
        setError(errorMessage(err, 'Penyimpanan jadwal gagal.'))
      }
    } finally {
      setSaving(false)
    }
  }, [form, playlists.items, schedules])

  const remove = useCallback(
    async (schedule: Schedule) => {
      setError(null)
      setRunningBlock(null)
      try {
        await deleteSchedule(schedule.id)
        await schedules.refresh()
      } catch (err) {
        if (isApiError(err) && err.code === 'SCHEDULE_RUNNING') {
          setRunningBlock(
            `Jadwal "${schedule.name}" sedang berjalan. Penghentian sesi dilakukan dari Live Control, bukan dari halaman ini.`,
          )
        } else {
          setError(errorMessage(err, 'Penghapusan jadwal gagal.'))
        }
      }
    },
    [schedules],
  )

  const toggleEnabled = useCallback(
    async (schedule: Schedule) => {
      setError(null)
      try {
        await updateSchedule(schedule.id, {
          name: schedule.name,
          revision_id: schedule.revision_id,
          start_at: schedule.start_at,
          end_at: schedule.end_at,
          timezone: schedule.timezone,
          playback_mode: schedule.playback_mode,
          loop_enabled: schedule.loop_enabled,
          auto_reconnect: schedule.auto_reconnect,
          enabled: !schedule.enabled,
        })
        await schedules.refresh()
      } catch (err) {
        setError(errorMessage(err, 'Perubahan status aktif gagal.'))
      }
    },
    [schedules],
  )

  const zones = timeZoneOptions(form?.timezone)

  // The exact instants are shown back to the operator so a zone or DST offset
  // mistake is visible before saving: the sent strings carry the zone offset,
  // while UTC is what the server ultimately stores.
  const resolvedRange = useMemo(() => {
    if (!form || !form.startLocal || !form.endLocal) return null
    try {
      const start = localInputToIsoWithOffset(form.startLocal, form.timezone)
      const end = localInputToIsoWithOffset(form.endLocal, form.timezone)
      if (Number.isNaN(new Date(end).getTime()) || Number.isNaN(new Date(start).getTime())) {
        return null
      }
      return {
        start,
        end,
        startUtc: new Date(start).toISOString(),
        endUtc: new Date(end).toISOString(),
        reversed: new Date(end).getTime() <= new Date(start).getTime(),
      }
    } catch {
      return null
    }
  }, [form])

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Jadwal</h1>
            <p>Penjadwalan berjalan di server, tetap bekerja saat panel ini ditutup.</p>
          </div>
          <Button onClick={openCreate}>Jadwal baru</Button>
        </header>

        {error ? (
          <Banner tone="error" title="Tindakan gagal">
            {error}
          </Banner>
        ) : null}
        {runningBlock ? (
          <Banner tone="warn" title="Jadwal sedang berjalan">
            {runningBlock}
          </Banner>
        ) : null}

        <Banner tone="info" title="Menghentikan sesi berjalan">
          Menonaktifkan jadwal hanya mencegah pemicuan berikutnya; sesi yang sedang berjalan tidak
          ikut berhenti. Penghentian sesi dilakukan dengan tombol Stop Live di halaman Live Control.
        </Banner>

        {schedules.loading && schedules.items.length === 0 ? <Loading label="Memuat jadwal…" /> : null}
        {schedules.error ? (
          <Banner tone="error" title="Gagal memuat jadwal">
            {schedules.error}
          </Banner>
        ) : null}

        {!schedules.loading && !schedules.error && schedules.items.length === 0 ? (
          <EmptyState
            title="Belum ada jadwal"
            description="Buat jadwal agar siaran berjalan otomatis pada waktu yang ditentukan."
            action={
              <Button onClick={openCreate}>Buat jadwal pertama</Button>
            }
          />
        ) : null}

        {schedules.items.length > 0 ? (
          <ul className="y-rows">
            {schedules.items.map((s) => {
              const status = STATUS_PRESENTATION[s.status] ?? STATUS_FALLBACK
              return (
              <li className="y-row" key={s.id}>
                <div className="y-row-main">
                  <div className="y-row-title">{s.name}</div>
                  <div className="y-row-meta">
                    {s.playlist_name} · revisi {s.revision_number} ·{' '}
                    {formatDateTime(s.start_at, s.timezone)} → {formatDateTime(s.end_at, s.timezone)} (
                    {s.timezone})
                  </div>
                  <div className="y-row-meta">
                    {s.loop_enabled ? 'Loop aktif' : 'Loop mati — satu putaran saja'} ·{' '}
                    {s.auto_reconnect ? 'Auto reconnect aktif' : 'Tanpa auto reconnect'} ·{' '}
                    {s.enabled ? 'Jadwal aktif' : 'Nonaktif'}
                  </div>
                  <div className="y-row-meta">{status.help}</div>
                  {s.message ? <div className="y-row-meta">{s.message}</div> : null}
                  {s.has_newer_revision ? (
                    <Banner tone="warn" title="Jadwal memakai revisi lama">
                      Playlist ini sudah punya revisi yang lebih baru, tetapi jadwal masih memakai
                      revisi {s.revision_number}. Perubahan tidak berlaku otomatis: buka Edit jadwal
                      lalu Simpan untuk sengaja mengunci revisi terbaru.
                    </Banner>
                  ) : null}
                  {s.is_running ? (
                    <div className="y-row-meta">
                      Sesi sedang berjalan. Edit dan Hapus dinonaktifkan; tekan Stop Live di halaman
                      Live Control untuk menghentikannya.
                    </div>
                  ) : null}
                </div>
                <div className="y-row-actions">
                  <span
                    className={`y-badge ${status.tone}`}
                    title={status.help}
                  >
                    <span className="y-badge-icon" aria-hidden="true">
                      {status.glyph}
                    </span>
                    {status.label}
                  </span>
                  <IconButton
                    label={s.enabled ? `Nonaktifkan ${s.name}` : `Aktifkan ${s.name}`}
                    icon={s.enabled ? '⏸' : '▶'}
                    disabled={s.is_running}
                    onClick={() => void toggleEnabled(s)}
                  />
                  <IconButton
                    label={`Edit ${s.name}`}
                    icon="✎"
                    disabled={s.is_running}
                    onClick={() => openEdit(s)}
                  />
                  <IconButton
                    label={`Hapus ${s.name}`}
                    icon="✕"
                    variant="danger"
                    disabled={s.is_running}
                    onClick={() => void remove(s)}
                  />
                </div>
              </li>
              )
            })}
          </ul>
        ) : null}
      </div>

      <Modal
        open={!!form}
        title={form?.id ? 'Edit jadwal' : 'Jadwal baru'}
        onClose={() => setForm(null)}
        footer={
          <>
            <Button variant="ghost" onClick={() => setForm(null)}>
              Batal
            </Button>
            <Button busy={saving} onClick={() => void submit()}>
              Simpan jadwal
            </Button>
          </>
        }
      >
        {form ? (
          <div className="y-stack">
            <fieldset style={FIELD_GROUP_STYLE}>
              <legend style={FIELD_GROUP_LEGEND_STYLE}>Playlist</legend>
              <div className="y-stack">
                <div className="y-field">
                  <label htmlFor="sch-name">Nama jadwal</label>
                  <input
                    id="sch-name"
                    className="y-input"
                    value={form.name}
                    onChange={(e) => setForm({ ...form, name: e.target.value })}
                  />
                  <span className="y-hint">
                    Kosongkan untuk memakai nama playlist. Nama hanya untuk mengenali jadwal di
                    daftar.
                  </span>
                </div>

                <div className="y-field">
                  <label htmlFor="sch-playlist">Playlist dan revisi</label>
                  <select
                    id="sch-playlist"
                    className="y-select"
                    value={form.playlistId}
                    onChange={(e) => setForm({ ...form, playlistId: e.target.value })}
                  >
                    <option value="">— pilih playlist —</option>
                    {playlists.items.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name} — revisi {p.revision_number} ({p.item_count} video)
                      </option>
                    ))}
                  </select>
                  <span className="y-hint">
                    {selectedPlaylist
                      ? `Simpan akan mengunci revisi ${selectedPlaylist.revision_number} (${selectedPlaylist.item_count} video). Perubahan playlist setelah ini tidak mengubah jadwal sampai jadwal disimpan ulang.`
                      : 'Wajib dipilih. Sesi memakai snapshot playlist yang dikunci saat disimpan.'}
                  </span>
                </div>
              </div>
            </fieldset>

            <fieldset style={FIELD_GROUP_STYLE}>
              <legend style={FIELD_GROUP_LEGEND_STYLE}>Waktu</legend>
              <div className="y-stack">
                <div className="y-field">
                  <label htmlFor="sch-tz">Zona waktu</label>
                  <select
                    id="sch-tz"
                    className="y-select"
                    value={form.timezone}
                    onChange={(e) => setForm({ ...form, timezone: e.target.value })}
                  >
                    {zones.map((z) => (
                      <option key={z} value={z}>
                        {z}
                      </option>
                    ))}
                  </select>
                  <span className="y-hint">
                    Jam di bawah dibaca sebagai jam dinding pada zona ini, bukan jam peramban.
                    Default dari Pengaturan: {settingsTimezone || '—'}.
                  </span>
                </div>

                <div className="y-form">
                  <div className="y-field">
                    <label htmlFor="sch-start">Mulai</label>
                    <input
                      id="sch-start"
                      className="y-input"
                      type="datetime-local"
                      value={form.startLocal}
                      onChange={(e) => setForm({ ...form, startLocal: e.target.value })}
                    />
                  </div>
                  <div className="y-field">
                    <label htmlFor="sch-end">Selesai</label>
                    <input
                      id="sch-end"
                      className="y-input"
                      type="datetime-local"
                      value={form.endLocal}
                      onChange={(e) => setForm({ ...form, endLocal: e.target.value })}
                    />
                  </div>
                </div>
                <span className="y-hint">
                  Siaran yang melewati tengah malam: isi tanggal selesai satu hari setelah tanggal
                  mulai. Sesi tetap berhenti tepat pada waktu selesai meski video belum habis.
                </span>

                {resolvedRange ? (
                  <div className="y-banner is-info">
                    <span>
                      <span className="y-banner-title">Waktu yang akan dikirim</span>
                      <br />
                      <span className="y-mono">
                        {resolvedRange.start} → {resolvedRange.end}
                      </span>
                      <br />
                      <span className="y-hint">
        Instan yang sama dalam UTC (disimpan server):
                      </span>{' '}
                      <span className="y-mono">
                        {resolvedRange.startUtc} → {resolvedRange.endUtc}
                      </span>
                      {resolvedRange.reversed ? (
                        <>
                          <br />
                          <span>
                            Waktu selesai harus lebih besar daripada waktu mulai.
                          </span>
                        </>
                      ) : null}
                    </span>
                  </div>
                ) : null}
              </div>
            </fieldset>

            <fieldset style={FIELD_GROUP_STYLE}>
              <legend style={FIELD_GROUP_LEGEND_STYLE}>Pemutaran</legend>
              <div className="y-stack">
                <div className="y-field">
                  <label htmlFor="sch-mode">Mode pemutaran</label>
                  <select
                    id="sch-mode"
                    className="y-select"
                    value={form.mode}
                    onChange={(e) => setForm({ ...form, mode: e.target.value as PlaybackMode })}
                  >
                    {PLAYBACK_MODES.map((m) => (
                      <option key={m.value} value={m.value}>
                        {m.label}
                      </option>
                    ))}
                  </select>
                  <span className="y-hint">{playbackModeHelp(form.mode)}</span>
                </div>
              </div>
            </fieldset>

            <fieldset style={FIELD_GROUP_STYLE}>
              <legend style={FIELD_GROUP_LEGEND_STYLE}>Opsi</legend>
              <div className="y-stack">
                <div className="y-check">
                  <input
                    id="sch-loop"
                    type="checkbox"
                    checked={form.loop}
                    onChange={(e) => setForm({ ...form, loop: e.target.checked })}
                  />
                  <label htmlFor="sch-loop" className="y-checklabel">
                    <span>Loop</span>
                    <span className="y-hint">
                      Nyala: putaran berikutnya dibuat sampai operator menekan Stop Live atau waktu
                      akhir tercapai. Mati: sesi berhenti setelah satu putaran.
                    </span>
                  </label>
                </div>

                <div className="y-check">
                  <input
                    id="sch-reconnect"
                    type="checkbox"
                    checked={form.autoReconnect}
                    onChange={(e) => setForm({ ...form, autoReconnect: e.target.checked })}
                  />
                  <label htmlFor="sch-reconnect" className="y-checklabel">
                    <span>Auto reconnect</span>
                    <span className="y-hint">
                      Percobaan ulang otomatis untuk gangguan sementara, tanpa melewati waktu akhir.
                    </span>
                  </label>
                </div>

                <div className="y-check">
                  <input
                    id="sch-enabled"
                    type="checkbox"
                    checked={form.enabled}
                    onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                  />
                  <label htmlFor="sch-enabled" className="y-checklabel">
                    <span>Aktif</span>
                    <span className="y-hint">
                      Jadwal nonaktif tidak akan dipicu. Menonaktifkan tidak menghentikan sesi yang
                      sedang berjalan.
                    </span>
                  </label>
                </div>
              </div>
            </fieldset>

            {!form.loop ? (
              <Banner tone="warn" title="Loop mati: sesi hanya satu putaran">
                Sesi akan berhenti setelah satu putaran meski waktu selesai masih jauh di masa depan.
                Waktu selesai hanya membatasi, tidak menambah: sesi tidak akan berlanjut melewati
                satu putaran. Aktifkan Loop bila Anda ingin sesi berlanjut sampai waktu selesai.
              </Banner>
            ) : null}

            {conflicts.length > 0 ? (
              <Banner tone="error" title={`Bentrok dengan ${conflicts.length} jadwal lain`}>
                <p style={{ margin: '0 0 6px' }}>
                  Hanya satu sesi dapat berjalan, jadi jendela waktu yang tumpang tindih ditolak.
                  Jadwal berikut memblokir rentang yang Anda masukkan:
                </p>
                <ul className="y-issue-list">
                  {conflicts.map((c) => (
                    <li key={c.id || c.name}>
                      <strong>{c.name}</strong>
                      {c.id ? <span className="y-mono"> · {c.id}</span> : null}
                      <br />
                      <span className="y-hint">
                        {c.startLocal} → {c.endLocal}
                      </span>
                    </li>
                  ))}
                </ul>
                <p style={{ margin: '6px 0 0' }} className="y-hint">
                  Geser jendela waktu agar tidak tumpang tindih, atau ubah jadwal yang memblokir
                  terlebih dahulu.
                </p>
              </Banner>
            ) : null}
          </div>
        ) : null}
      </Modal>
    </div>
  )
}