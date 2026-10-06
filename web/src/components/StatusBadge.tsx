/**
 * A status chip that always pairs its colour with a word and a glyph, so the
 * state survives a greyscale print and a colour-blind reader (PRD §5.1).
 */

export type StatusKind =
  | 'stream'
  | 'file'
  | 'compatibility'
  | 'schedule'
  | 'connection'
  | 'youtube';

export type StatusTone = 'ok' | 'warn' | 'error' | 'live' | 'idle';

interface StatusDescriptor {
  tone: StatusTone;
  label: string;
  /** A text glyph, so the meaning survives without colour. */
  glyph: string;
}

const STREAM_LABELS: Record<string, StatusDescriptor> = {
  IDLE: { tone: 'idle', label: 'Siap', glyph: '○' },
  VALIDATING: { tone: 'warn', label: 'Memeriksa', glyph: '◍' },
  STARTING: { tone: 'live', label: 'Memulai', glyph: '◔' },
  STREAMING: { tone: 'ok', label: 'Mengirim', glyph: '●' },
  RECONNECTING: { tone: 'warn', label: 'Menyambung ulang', glyph: '↻' },
  STOPPING: { tone: 'warn', label: 'Menghentikan', glyph: '◑' },
  STOPPED: { tone: 'idle', label: 'Berhenti', glyph: '○' },
  COMPLETED: { tone: 'ok', label: 'Selesai', glyph: '✓' },
  FAILED: { tone: 'error', label: 'Gagal', glyph: '✕' },
};

const FILE_DESCRIPTORS: Record<string, StatusDescriptor> = {
  uploading: { tone: 'live', label: 'Mengunggah', glyph: '↑' },
  processing: { tone: 'warn', label: 'Diproses', glyph: '◍' },
  ready: { tone: 'ok', label: 'Siap', glyph: '✓' },
  invalid: { tone: 'error', label: 'Tidak sesuai', glyph: '✕' },
  missing: { tone: 'error', label: 'Berkas hilang', glyph: '⊘' },
};

const COMPATIBILITY_DESCRIPTORS: Record<string, StatusDescriptor> = {
  unchecked: { tone: 'idle', label: 'Belum diperiksa', glyph: '?' },
  compatible: { tone: 'ok', label: 'Sesuai', glyph: '✓' },
  incompatible: { tone: 'error', label: 'Tidak sesuai', glyph: '✕' },
  needs_review: { tone: 'warn', label: 'Perlu ditinjau', glyph: '!' },
};

const SCHEDULE_DESCRIPTORS: Record<string, StatusDescriptor> = {
  disabled: { tone: 'idle', label: 'Nonaktif', glyph: '○' },
  scheduled: { tone: 'live', label: 'Terjadwal', glyph: '◔' },
  running: { tone: 'ok', label: 'Berjalan', glyph: '●' },
  completed: { tone: 'ok', label: 'Selesai', glyph: '✓' },
  stopped: { tone: 'idle', label: 'Dihentikan', glyph: '○' },
  failed: { tone: 'error', label: 'Gagal', glyph: '✕' },
  missed: { tone: 'warn', label: 'Terlewat', glyph: '!' },
  skipped_busy: { tone: 'warn', label: 'Dilewati (sibuk)', glyph: '»' },
};

const TABLES: Record<StatusKind, Record<string, StatusDescriptor>> = {
  stream: STREAM_LABELS,
  file: FILE_DESCRIPTORS,
  compatibility: COMPATIBILITY_DESCRIPTORS,
  schedule: SCHEDULE_DESCRIPTORS,
  connection: {
    online: { tone: 'ok', label: 'Terhubung', glyph: '●' },
    offline: { tone: 'error', label: 'Terputus', glyph: '✕' },
    stale: { tone: 'warn', label: 'Data lama', glyph: '!' },
  },
  youtube: {
    unverified: { tone: 'idle', label: 'Belum diverifikasi', glyph: '?' },
    live: { tone: 'ok', label: 'Tayang', glyph: '●' },
    offline: { tone: 'idle', label: 'Tidak diketahui', glyph: '○' },
  },
};

export interface StatusBadgeProps {
  state: string;
  kind?: StatusKind;
  /** Overrides the derived wording, e.g. "Mengirim (sesi #2)". */
  label?: string;
  className?: string;
}

/**
 * An unknown state still renders: it falls back to a neutral chip carrying
 * the raw value, which is more useful than an empty space.
 */
export function StatusBadge({ state, kind = 'stream', label, className = '' }: StatusBadgeProps) {
  const table = TABLES[kind];
  const descriptor = table[state] ?? {
    tone: 'idle' as StatusTone,
    label: state || 'Tidak diketahui',
    glyph: '•',
  };

  return (
    <span
      className={`status-badge status-badge--${descriptor.tone} ${className}`.trim()}
      // The visible text is the state; this adds it for assistive tech too.
      role="status"
    >
      <span className="status-badge__glyph" aria-hidden="true">
        {descriptor.glyph}
      </span>
      <span className="status-badge__label">{label ?? descriptor.label}</span>
    </span>
  );
}