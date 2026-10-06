import type { ReactNode } from 'react';

export type StatStatus = 'ok' | 'warn' | 'error' | 'idle' | 'unknown';

const STATUS_TEXT: Record<StatStatus, string> = {
  ok: 'Normal',
  warn: 'Perlu perhatian',
  error: 'Kritis',
  idle: 'Siap',
  unknown: 'Tidak diketahui',
};

/** PRD §4: a figure the device cannot read is reported, never guessed. */
const UNAVAILABLE = 'Tidak tersedia';

export interface StatCardProps {
  label: string;
  /** null or undefined renders "Tidak tersedia". */
  value: string | number | null | undefined;
  unit?: string;
  status?: StatStatus;
  /** Replaces the default wording for the status. */
  statusText?: string;
  icon?: ReactNode;
  hint?: string;
  /** Recent readings, oldest first; renders a compact area sparkline. */
  sparkline?: number[] | null;
  /** Marks the figures as no longer refreshed (PRD §5.3). */
  stale?: boolean;
  /** Switches the tile to the reference layout's red broadcast treatment. */
  live?: boolean;
  /** Elapsed session clock shown on the right of a live tile. */
  timer?: string;
  className?: string;
}

function Sparkline({ points }: { points: number[] }) {
  const width = 120;
  const height = 28;
  if (points.length < 2) return null;

  const finite = points.filter((p) => Number.isFinite(p));
  if (finite.length < 2) return null;

  const min = Math.min(...finite);
  const max = Math.max(...finite);
  const span = max - min || 1;

  // One path for the whole series: cheaper than per-point nodes and it fills
  // cleanly against the baseline.
  const step = width / (points.length - 1);
  const coords = points.map((p, i) => {
    const x = (i * step).toFixed(2);
    const y = (height - ((p - min) / span) * height).toFixed(2);
    return `${x},${y}`;
  });

  return (
    <svg
      className="stat-card__sparkline"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <polyline className="stat-card__sparkline-line" points={coords.join(' ')} />
      <polygon
        className="stat-card__sparkline-fill"
        points={`0,${height} ${coords.join(' ')} ${width},${height}`}
      />
    </svg>
  );
}

export function StatCard({
  label,
  value,
  unit,
  status = 'unknown',
  statusText,
  icon,
  hint,
  sparkline,
  stale = false,
  live = false,
  timer,
  className = '',
}: StatCardProps) {
  const available = value !== null && value !== undefined && value !== '';
  const shown = available ? value : UNAVAILABLE;

  return (
    <article
      className={`stat-card stat-card--${status}${live ? ' stat-card--live' : ''}${stale ? ' stat-card--stale' : ''} ${className}`.trim()}
    >
      <div className="stat-card__top">
        <span className="stat-card__label">{label}</span>
        {icon ? (
          <span className="stat-card__icon" aria-hidden="true">
            {icon}
          </span>
        ) : null}
        {/* The elapsed clock sits opposite the state so the tile reads in one
            glance, as in the reference layout. */}
        {live && timer ? <span className="stat-card__timer">{timer}</span> : null}
      </div>

      <div className="stat-card__value-row">
        {live ? (
          <span className="stat-card__pulse" aria-hidden="true" />
        ) : null}
        <span className="stat-card__value">{shown}</span>
        {unit && available ? <span className="stat-card__unit">{unit}</span> : null}
      </div>

      {sparkline && sparkline.length > 1 ? (
        <Sparkline points={sparkline} />
      ) : (
        <div className="stat-card__sparkline stat-card__sparkline--empty" aria-hidden="true" />
      )}

      <div className="stat-card__footer">
        {/* Colour alone never carries the meaning; the word does. */}
        <span className={`stat-card__status stat-card__status--${status}`}>
          <span className="stat-card__status-dot" aria-hidden="true" />
          {statusText ?? STATUS_TEXT[status]}
        </span>
        {stale ? <span className="stat-card__stale">Data lama</span> : null}
        {hint ? <span className="stat-card__hint">{hint}</span> : null}
      </div>
    </article>
  );
}