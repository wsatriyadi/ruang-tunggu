import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { Spinner } from './Spinner';

export type IconButtonVariant = 'ghost' | 'primary' | 'danger';

export interface IconButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'aria-label'> {
  /** Required: becomes the accessible name and the tooltip. */
  label: string;
  icon: ReactNode;
  variant?: IconButtonVariant;
  size?: 'sm' | 'md';
  busy?: boolean;
}

/** Square control with a mandatory 44px target and a text alternative. */
export function IconButton({
  label,
  icon,
  variant = 'ghost',
  size = 'md',
  busy = false,
  disabled,
  className = '',
  type = 'button',
  title,
  ...rest
}: IconButtonProps) {
  const classes = [
    'icon-button',
    `icon-button--${variant}`,
    `icon-button--${size}`,
    busy ? 'icon-button--busy' : '',
    className,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <button
      {...rest}
      type={type}
      className={classes}
      disabled={disabled === true || busy}
      aria-label={label}
      aria-busy={busy || undefined}
      title={title ?? label}
    >
      {busy ? <Spinner size="sm" /> : icon}
    </button>
  );
}