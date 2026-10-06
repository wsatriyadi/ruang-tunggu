/**
 * The application frame: sidebar, header, and the page outlet.
 *
 * Below 900px the sidebar becomes a drawer toggled from the header, and the
 * three-column body collapses (PRD §5.1). On a phone the session state and
 * the connection banner move to the top of the column, which the page
 * components place themselves.
 */

import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { useEventStream } from '../api/events';
import { useDashboard } from '../state/useDashboard';
import { useSession } from '../state/useSession';
import { useSystem } from '../state/useSystem';
import { StatusBadge } from './StatusBadge';
import { IconButton } from './IconButton';
import { Spinner } from './Spinner';
import { formatBytes } from '../pages/shared/ui';

interface NavItem {
  to: string;
  label: string;
  glyph: string;
  /** Matches nested routes too, so /videos/123 keeps Video Library active. */
  end: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: '/', label: 'Dashboard', glyph: '▤', end: true },
  { to: '/videos', label: 'Video Library', glyph: '▶', end: false },
  { to: '/playlist', label: 'Playlist', glyph: '≡', end: false },
  { to: '/schedule', label: 'Schedule', glyph: '◷', end: false },
  { to: '/live', label: 'Live Control', glyph: '●', end: false },
  { to: '/logs', label: 'Logs', glyph: '⎘', end: false },
  { to: '/settings', label: 'Settings', glyph: '⚙', end: false },
];

const WEEKDAYS = ['Minggu', 'Senin', 'Selasa', 'Rabu', 'Kamis', 'Jumat', 'Sabtu'];

const pad = (n: number): string => (n < 10 ? `0${n}` : String(n));

/** Compact uptime such as "3d 12j" or "45m". */
function formatUptime(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const d = Math.floor(total / 86400);
  const h = Math.floor((total % 86400) / 3600);
  const m = Math.floor((total % 3600) / 60);
  if (d > 0) return `${d}h ${h}j`;
  if (h > 0) return `${h}j ${m}m`;
  return `${m}m`;
}

/**
 * A live clock in the instance timezone.
 *
 * Intl renders the zone abbreviation, which is what tells an operator whether
 * the displayed time matches the one the schedule windows were entered in.
 */
function LiveClock({ timezone }: { timezone: string }) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  const formatted = useMemo(() => {
    try {
      const date = new Date(now);
      const time = new Intl.DateTimeFormat('id-ID', {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
        timeZone: timezone,
      }).format(date);

      const day = new Intl.DateTimeFormat('id-ID', {
        weekday: 'long',
        day: 'numeric',
        month: 'long',
        year: 'numeric',
        timeZone: timezone,
      }).format(date);

      const zone = new Intl.DateTimeFormat('id-ID', {
        timeZone: timezone,
        timeZoneName: 'short',
      })
        .formatToParts(date)
        .find((part) => part.type === 'timeZoneName')?.value;

      return { time, day, zone: zone ?? timezone };
    } catch {
      // An unknown IANA zone falls back to the browser's own local time.
      const date = new Date(now);
      return {
        time: `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`,
        day: `${WEEKDAYS[date.getDay()]}, ${date.getDate()}`,
        zone: 'lokal',
      };
    }
  }, [now, timezone]);

  return (
    <div className="shell__clock">
      <time className="shell__clock-time" dateTime={new Date(now).toISOString()}>
        {formatted.time}
      </time>
      <span className="shell__clock-meta">
        {formatted.day} · {formatted.zone}
      </span>
    </div>
  );
}

/** The address a browser or another device can reach this panel on. */
function originLabel(): string {
  if (typeof window === 'undefined') return '';
  const { hostname, port } = window.location;
  return port ? `${hostname}:${port}` : hostname;
}

/** Renders one inventory row, or nothing when the device cannot report it. */
function systemRow(label: string, value: string | undefined) {
  if (!value) return null;
  return (
    <>
      <dt>{label}</dt>
      <dd title={value}>{value}</dd>
    </>
  );
}

export function Shell({ children }: { children: ReactNode }) {
  const { state, logout } = useSession();
  const { data } = useDashboard();
  const { data: system } = useSystem();

  // A one-second tick keeps the uptime figure honest; it re-renders only this
  // card, and the value is a duration string.
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setTick(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const uptimeSeconds = system?.info.boot_time
    ? (tick - Date.parse(system.info.boot_time)) / 1000
    : undefined;
  const { connected } = useEventStream();
  const location = useLocation();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [clockTick, setClockTick] = useState(0);

  // Route changes close the drawer, otherwise it covers the new page.
  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  // Re-renders the header's relative figures on a slow, cheap interval.
  useEffect(() => {
    const timer = window.setInterval(() => setClockTick((n) => n + 1), 5000);
    return () => window.clearInterval(timer);
  }, []);

  const instanceName = data?.instance.name || 'YouTube Live Server';
  const timezone = data?.instance.timezone || system?.info.os || 'UTC';
  const device = system?.info;
  const origin = originLabel();

  const deviceLine = device
    ? [device.cpu_model || device.soc || device.os, device.cpu_cores ? `${device.cpu_cores} inti` : '']
        .filter(Boolean)
        .join(' · ')
    : '';

  return (
    <div className={`shell${drawerOpen ? ' shell--drawer-open' : ''}`} data-tick={clockTick}>
      <a className="skip-link" href="#main-content">
        Lewati ke konten utama
      </a>

      {/* Backdrop only exists while the drawer is open, on narrow screens. */}
      {drawerOpen ? (
        <div className="shell__backdrop" onClick={() => setDrawerOpen(false)} aria-hidden="true" />
      ) : null}

      <aside className="shell__sidebar" aria-label="Navigasi utama">
        <div className="shell__brand">
          <span className="shell__brand-mark" aria-hidden="true">
            ▶
          </span>
          <span className="shell__brand-text">
            <span className="shell__brand-name">{instanceName}</span>
            <span className="shell__brand-sub">Live Server</span>
          </span>
        </div>

        <nav className="shell__nav">
          <ul>
            {NAV_ITEMS.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) =>
                    `shell__nav-link${isActive ? ' shell__nav-link--active' : ''}`
                  }
                >
                  <span className="shell__nav-glyph" aria-hidden="true">
                    {item.glyph}
                  </span>
                  <span className="shell__nav-label">{item.label}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/* Device inventory, pinned to the bottom as in the reference layout.
            Every field that cannot be read is left blank rather than guessed
            (PRD §4 "System", FR-02). */}
        <section className="shell__system" aria-label="Informasi sistem">
          <h2 className="shell__system-title">System</h2>
          <dl className="shell__system-data">
            {systemRow('SoC', system?.info.soc)}
            {systemRow('CPU', system?.info.cpu_model)}
            {systemRow('RAM', system?.info.total_ram_bytes ? formatBytes(system.info.total_ram_bytes) : '')}
            {systemRow('OS', system?.info.os)}
            {systemRow('Kernel', system?.info.kernel)}
            {systemRow('Inti', system?.info.cpu_cores ? String(system.info.cpu_cores) : '')}
            {systemRow('Uptime', uptimeSeconds === undefined ? '' : formatUptime(uptimeSeconds))}
          </dl>
        </section>

        <div className="shell__sidebar-footer">
          <StatusBadge
            kind="connection"
            state={connected ? 'online' : 'offline'}
            label={connected ? 'Server online' : 'Server terputus'}
          />
          <button type="button" className="shell__logout" onClick={() => void logout()}>
            Keluar
          </button>
        </div>
      </aside>

      <header className="shell__header">
        <IconButton
          className="shell__drawer-toggle"
          label={drawerOpen ? 'Tutup menu' : 'Buka menu'}
          icon={drawerOpen ? '✕' : '☰'}
          onClick={() => setDrawerOpen((open) => !open)}
          aria-expanded={drawerOpen}
        />

        <div className="shell__identity">
          <span className="shell__identity-name">{instanceName}</span>
          <span className="shell__identity-meta">
            {deviceLine ? <span>{deviceLine}</span> : null}
            {origin ? <span className="shell__identity-host">{origin}</span> : null}
            {data?.instance.version ? <span>v{data.instance.version}</span> : null}
          </span>
        </div>

        <div className="shell__header-status">
          <StatusBadge
            kind="connection"
            state={connected ? 'online' : 'offline'}
            label={connected ? 'Server online' : 'Sambungan ke server terputus'}
          />
          {data ? <StatusBadge kind="stream" state={data.stream.state} /> : null}
        </div>

        <LiveClock timezone={timezone} />

        <NavLink to="/settings" className="shell__settings-link" aria-label="Buka pengaturan">
          <span aria-hidden="true">⚙</span>
        </NavLink>
      </header>

      <main className="shell__main" id="main-content">
        {connected ? null : (
          <div className="shell__offline-banner" role="status">
            <span aria-hidden="true">⚠</span>
            Sambungan ke server terputus. Angka monitor tidak dapat diperbarui
            {state === 'authenticated' ? ' — stream lokal tetap berjalan.' : '.'}
          </div>
        )}
        {children}
      </main>

      {state !== 'authenticated' ? <Spinner className="visually-hidden" /> : null}
    </div>
  );
}