import type { CSSProperties } from 'react';

const SIZES: Record<'sm' | 'md' | 'lg', number> = { sm: 14, md: 20, lg: 32 };

export interface SpinnerProps {
  size?: 'sm' | 'md' | 'lg' | number;
  /** Announced to screen readers; also shown beside the spinner when set. */
  label?: string;
  className?: string;
}

/** A busy indicator that never blocks layout and keeps its size in the flow. */
export function Spinner({ size = 'md', label, className = '' }: SpinnerProps) {
  const px = typeof size === 'number' ? size : SIZES[size];
  const style: CSSProperties = {
    width: px,
    height: px,
    borderWidth: Math.max(2, Math.round(px / 8)),
  };

  return (
    <span className={`spinner${label ? ' spinner--labelled' : ''} ${className}`.trim()}>
      <span className="spinner__ring" style={style} aria-hidden="true" />
      {label ? <span className="spinner__label">{label}</span> : null}
      <span className="visually-hidden" role="status">
        {label ?? 'Memuat'}
      </span>
    </span>
  );
}