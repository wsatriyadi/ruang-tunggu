import { useState, type ReactNode } from 'react'
import './pages.css'

/* ---------------------------------------------------------------------------
 * Formatting. Every figure the operator reads goes through one of these so a
 * unit is spelled the same way on every page.
 * ------------------------------------------------------------------------- */

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return 'Tidak tersedia'
  let value = n
  let unit = 0
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${BYTE_UNITS[unit]}`
}

/** Elapsed time as m:ss, or h:mm:ss past an hour. */
export function formatClock(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) {
    return '00:00'
  }
  const total = Math.floor(seconds)
  const hh = Math.floor(total / 3600)
  const mm = Math.floor((total % 3600) / 60)
  const ss = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return hh > 0 ? `${hh}:${pad(mm)}:${pad(ss)}` : `${pad(mm)}:${pad(ss)}`
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) {
    return 'Tidak tersedia'
  }
  if (seconds < 1) return `${Math.round(seconds * 1000)} md`
  const totalMinutes = Math.round(seconds / 60)
  const restMinutes = totalMinutes % 60
  const hours = Math.floor(totalMinutes / 60)
  if (hours === 0) return `${restMinutes} menit`
  return restMinutes === 0 ? `${hours} jam` : `${hours} jam ${restMinutes} menit`
}

const NUMBER_FMT = new Intl.NumberFormat('id-ID')

export function formatNumber(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return 'Tidak tersedia'
  return NUMBER_FMT.format(n)
}

/* ---------------------------------------------------------------------------
 * Time zones. Schedule timestamps must carry their zone's offset so the chosen
 * zone is honoured (docs/API.md, Schedules).
 * ------------------------------------------------------------------------- */

const FALLBACK_TIMEZONES = [
  'Asia/Makassar',
  'Asia/Jayapura',
  'Asia/Jakarta',
  'Asia/Pontianak',
  'Australia/Perth',
  'Europe/Amsterdam',
  'UTC',
]

/** Publisher throughput, always kbps so it never reads like an interface figure. */
export function formatKbps(kbps: number | null | undefined): string {
  if (kbps === null || kbps === undefined || !Number.isFinite(kbps) || kbps <= 0) {
    return '0 kbps'
  }
  return `${formatNumber(Math.round(kbps))} kbps`
}

export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function timeZoneOptions(preferred?: string): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
  let list: string[]
  if (typeof intl.supportedValuesOf === 'function') {
    try {
      list = intl.supportedValuesOf('timeZone')
    } catch {
      list = FALLBACK_TIMEZONES
    }
  } else {
    list = FALLBACK_TIMEZONES
  }
  const out = list.slice()
  for (const extra of [preferred, browserTimeZone()]) {
    if (extra && !out.includes(extra)) out.unshift(extra)
  }
  return out
}

/** Wall-clock fields of `date` as seen in `timeZone`. */
function zoneParts(date: Date, timeZone: string): Record<string, string> {
  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  const out: Record<string, string> = {}
  for (const part of dtf.formatToParts(date)) out[part.type] = part.value
  return out
}

/** The `+HH:MM` offset `timeZone` was at on `date`. */
function offsetFor(timeZone: string, date: Date): string {
  const p = zoneParts(date, timeZone)
  const asUTC = Date.UTC(
    Number(p.year),
    Number(p.month) - 1,
    Number(p.day),
    Number(p.hour) % 24,
    Number(p.minute),
    Number(p.second),
  )
  const delta = asUTC - date.getTime()
  const abs = Math.abs(delta)
  const hh = String(Math.floor(abs / 3_600_000)).padStart(2, '0')
  const mm = String(Math.floor((abs % 3_600_000) / 60_000)).padStart(2, '0')
  return `${delta < 0 ? '-' : '+'}${hh}:${mm}`
}

/**
 * Turns a `datetime-local` value ("2026-10-06T20:00"), read as wall-clock time
 * in `timeZone`, into an ISO string carrying that zone's UTC offset.
 *
 * The offset is resolved twice because a zone can switch offset between the
 * naive instant and the instant it denotes (DST edges).
 */
export function localInputToIsoWithOffset(localValue: string, timeZone: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(localValue)
  if (!m) throw new Error('Tanggal atau jam belum lengkap.')
  const [, y = '', mo = '', d = '', h = '', mi = ''] = m
  const naive = Date.UTC(+y, +mo - 1, +d, +h, +mi)
  const firstOffset = offsetFor(timeZone, new Date(naive))
  const first = new Date(`${localValue}:00${firstOffset}`)
  if (Number.isNaN(first.getTime())) throw new Error('Tanggal atau jam tidak valid.')
  const secondOffset = offsetFor(timeZone, first)
  if (secondOffset === firstOffset) return `${localValue}:00${firstOffset}`
  const second = new Date(`${localValue}:00${secondOffset}`)
  if (Number.isNaN(second.getTime())) return `${localValue}:00${firstOffset}`
  return `${localValue}:00${offsetFor(timeZone, second)}`
}

/** Inverse of localInputToIsoWithOffset, for a datetime-local field value. */
export function isoToLocalInput(iso: string, timeZone: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = zoneParts(d, timeZone)
  return `${p.year}-${p.month}-${p.day}T${String(Number(p.hour) % 24).padStart(2, '0')}:${p.minute}`
}

export function formatDateTime(iso: string | null | undefined, timeZone: string): string {
  if (!iso) return 'Tidak tersedia'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return 'Tidak tersedia'
  try {
    return new Intl.DateTimeFormat('id-ID', { timeZone, dateStyle: 'medium', timeStyle: 'short' }).format(d)
  } catch {
    return d.toISOString()
  }
}

export function formatClockTime(iso: string | null | undefined, timeZone: string): string {
  if (!iso) return '--:--:--'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--:--:--'
  try {
    return new Intl.DateTimeFormat('id-ID', {
      timeZone,
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).format(d)
  } catch {
    return d.toISOString().slice(11, 19)
  }
}

/* ---------------------------------------------------------------------------
 * Indonesian wording shared by several pages. Status always carries a word or
 * icon, never colour alone (PRD §5.1).
 * ------------------------------------------------------------------------- */

export interface PlaybackModeOption {
  value: 'sequential' | 'random' | 'random_no_repeat'
  label: string
  help: string
}

export const PLAYBACK_MODES: PlaybackModeOption[] = [
  {
    value: 'sequential',
    label: 'Sequential',
    help: 'Memutar semua video berurutan sesuai urutan playlist, dari item pertama sampai terakhir.',
  },
  {
    value: 'random',
    label: 'Random',
    help: 'Memilih video secara acak dan boleh mengulang video yang sama. Satu putaran berisi N pilihan, dengan N = jumlah video dalam playlist.',
  },
  {
    value: 'random_no_repeat',
    label: 'Random tanpa pengulangan',
    help: 'Mengacak semua video lalu memutar masing-masing tepat satu kali dalam satu putaran. Urutan diacak ulang pada setiap putaran.',
  },
]

export function playbackModeHelp(mode: string): string {
  return PLAYBACK_MODES.find((m) => m.value === mode)?.help ?? ''
}

export const STREAM_STATE_LABEL: Record<string, string> = {
  IDLE: 'Tidak ada sesi',
  VALIDATING: 'Memeriksa',
  STARTING: 'Menyiapkan publisher',
  STREAMING: 'Mengirim',
  RECONNECTING: 'Menyambung ulang',
  STOPPING: 'Menghentikan',
  STOPPED: 'Dihentikan operator',
  COMPLETED: 'Selesai',
  FAILED: 'Gagal',
}

export const STREAM_STATE_HINT: Record<string, string> = {
  IDLE: 'Tidak ada publisher yang berjalan.',
  VALIDATING: 'Server sedang memeriksa playlist, berkas, dan kredensial tujuan.',
  STARTING: 'FFmpeg sedang disiapkan atau membuka koneksi.',
  STREAMING: 'Publisher lokal hidup. Status tayang di YouTube belum diverifikasi.',
  RECONNECTING: 'Koneksi terputus; menunggu jadwal percobaan berikutnya.',
  STOPPING: 'Niat berhenti diterima; proses sedang ditutup.',
  STOPPED: 'Sesi dihentikan operator.',
  COMPLETED: 'Satu putaran selesai atau waktu akhir tercapai.',
  FAILED: 'Kegagalan terminal; diperlukan tindakan operator.',
}

/** Static membership lookup: is the local publisher process live? */
export const ACTIVE_STATE: Record<string, true> = {
  VALIDATING: true,
  STARTING: true,
  STREAMING: true,
  RECONNECTING: true,
  STOPPING: true,
}

export const FILE_STATUS_LABEL: Record<string, string> = {
  uploading: 'Mengunggah',
  processing: 'Diproses',
  ready: 'Siap',
  invalid: 'Tidak valid',
  missing: 'File hilang',
}

export const COMPAT_LABEL: Record<string, string> = {
  unchecked: 'Belum diperiksa',
  compatible: 'Kompatibel',
  incompatible: 'Tidak kompatibel',
  needs_review: 'Perlu ditinjau',
}

export const SEVERITY_LABEL: Record<string, string> = {
  blocking: 'Menghambat',
  warning: 'Peringatan',
  notice: 'Informasi',
}

export const SCHEDULE_STATUS_LABEL: Record<string, string> = {
  disabled: 'Nonaktif',
  scheduled: 'Terjadwal',
  running: 'Berjalan',
  completed: 'Selesai',
  stopped: 'Dihentikan',
  failed: 'Gagal',
  missed: 'Terlewat',
  skipped_busy: 'Dilewati: sesi lain berjalan',
}

/* ---------------------------------------------------------------------------
 * Small presentational pieces shared by the pages.
 * ------------------------------------------------------------------------- */

export function Banner({
  tone = 'info',
  title,
  children,
}: {
  tone?: 'info' | 'warn' | 'error'
  title?: string
  children: ReactNode
}) {
  const icon = tone === 'error' ? '✕' : tone === 'warn' ? '!' : 'i'
  return (
    <div className={`y-banner is-${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <span aria-hidden="true">{icon}</span>
      <div className="y-stack">
        {title ? <span className="y-banner-title">{title}</span> : null}
        <div>{children}</div>
      </div>
    </div>
  )
}

export function Loading({ label = 'Memuat…' }: { label?: string }) {
  return (
    <p className="y-spinner" role="status">
      <span className="y-spinner-ring" aria-hidden="true" />
      <span>{label}</span>
    </p>
  )
}

export function ProgressBar({ percent, tone }: { percent: number; tone?: 'ok' | 'fail' }) {
  const clamped = Math.max(0, Math.min(100, percent))
  return (
    <div
      className={`y-bar${tone === 'ok' ? ' is-done' : tone === 'fail' ? ' is-fail' : ''}`}
      role="progressbar"
      aria-valuenow={Math.round(clamped)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <span style={{ width: `${clamped}%` }} />
    </div>
  )
}

/**
 * A thumbnail that degrades to a labelled placeholder instead of a broken
 * image. the raw payload carries no image path that the
 * browser cannot fetch, so an unloadable source must not render as an image
 * (AC-18: a static image is never presented as live video).
 */
export function SafeThumbnail({
  src,
  alt,
  fallbackLabel,
}: {
  src: string | undefined
  alt: string
  fallbackLabel: string
}) {
  const [failed, setFailed] = useState(false)
  if (!src || failed) {
    return (
      <div className="y-preview-frame" style={{ padding: 16, textAlign: 'center' }}>
        <span className="y-card-note">
          {failed ? 'Thumbnail tidak dapat dimuat dari server.' : fallbackLabel}
        </span>
      </div>
    )
  }
  return (
    <div className="y-preview-frame" style={{ maxWidth: 260 }}>
      <img
        src={src}
        alt={alt}
        style={{ width: '100%', display: 'block' }}
        onError={() => setFailed(true)}
      />
    </div>
  )
}

export function Field({
  label,
  hint,
  wide,
  htmlFor,
  children,
}: {
  label: string
  hint?: ReactNode
  wide?: boolean
  htmlFor?: string
  children: ReactNode
}) {
  return (
    <div className={`y-field${wide ? ' is-wide' : ''}`}>
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {hint ? <span className="y-hint">{hint}</span> : null}
    </div>
  )
}

export interface IssueLike {
  field?: string
  severity?: string
  message: string
  name?: string
  media_id?: string
}

/**
 * Renders `{field, severity, message}` issues, the shape used by both
 * `Media.issues[]` and `Playlist.issues[]` (docs/API.md).
 */
export function IssueList({ issues, showField }: { issues: IssueLike[]; showField?: boolean }) {
  return (
    <ul className="y-issue-list">
      {issues.map((issue, i) => (
        <li key={`${issue.field ?? 'field'}-${i}`}>
          {issue.severity ? <strong>[{SEVERITY_LABEL[issue.severity] ?? issue.severity}] </strong> : null}
          {showField && issue.field ? (
            <>
              <span className="y-mono">{issue.field}</span>:{' '}
            </>
          ) : null}
          {issue.message}
        </li>
      ))}
    </ul>
  )
}

/**
 * Reads a named array out of a 409 `details` payload without assuming anything
 * beyond the documented keys (`usage`, `conflicting`, `schedules`).
 */
export function detailArray(details: unknown, key: string): Record<string, unknown>[] {
  if (!details || typeof details !== 'object') return []
  const value = (details as Record<string, unknown>)[key]
  if (!Array.isArray(value)) return []
  return value.filter((item): item is Record<string, unknown> => !!item && typeof item === 'object')
}