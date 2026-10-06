/**
 * Dashboard data, kept live by SSE.
 *
 * `GET /dashboard` fills the screen once; `metrics` and `stream_state` events
 * then merge over it field by field. When the event link drops, `connected`
 * goes false and `stale` goes true so the UI can say the figures are old
 * (PRD §5.3) instead of presenting them as current.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useEventStream } from '../api/events';
import { errorMessage, getDashboard } from '../api/client';
import type { Dashboard, MetricsEventPayload, Progress } from '../types';

/** A figures-only merge keeps server-side flags (stale, staleness window). */
function mergeMetrics(current: Dashboard | null, patch: MetricsEventPayload): Dashboard | null {
  if (!current) return current;
  const { system } = current;
  return {
    ...current,
    system: {
      ...system,
      ...(typeof patch.cpu_percent === 'number' ? { cpu_percent: patch.cpu_percent } : {}),
      ...(typeof patch.mem_used_percent === 'number' ? { mem_used_percent: patch.mem_used_percent } : {}),
      ...(typeof patch.mem_total_bytes === 'number' ? { mem_total_bytes: patch.mem_total_bytes } : {}),
      ...(typeof patch.mem_available_bytes === 'number'
        ? { mem_available_bytes: patch.mem_available_bytes }
        : {}),
      ...(typeof patch.temperature_celsius === 'number' ||
      patch.temperature_celsius === null
        ? { temperature_celsius: patch.temperature_celsius }
        : {}),
      ...(typeof patch.uptime_seconds === 'number' ? { uptime_seconds: patch.uptime_seconds } : {}),
      ...(typeof patch.process_rss_bytes === 'number'
        ? { process_rss_bytes: patch.process_rss_bytes }
        : {}),
      ...(typeof patch.network === 'undefined' ? {} : { network: patch.network }),
    },
    stream: {
      ...current.stream,
      publisher_bitrate_kbps:
        patch.publisher_bitrate_kbps ?? current.stream.publisher_bitrate_kbps,
    },
  };
}

export interface DashboardState {
  data: Dashboard | null;
  loading: boolean;
  error: string | null;
  /** True when the numbers on screen are no longer being refreshed. */
  stale: boolean;
  /** True while the SSE link is down. */
  connected: boolean;
  refresh: () => Promise<void>;
}

export function useDashboard(): DashboardState {
  const [data, setData] = useState<Dashboard | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { connected, subscribeTo } = useEventStream();
  const [linkDropped, setLinkDropped] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const next = await getDashboard();
      setData(next);
      setError(null);
    } catch (e) {
      setError(errorMessage(e, 'Gagal memuat dashboard.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // A reconnect restores freshness without another full fetch.
  const wasConnected = useRef(connected);
  useEffect(() => {
    if (connected && !wasConnected.current) setLinkDropped(false);
    wasConnected.current = connected;
  }, [connected]);

  useEffect(
    () =>
      subscribeTo('metrics', (payload) => {
        setData((current) => mergeMetrics(current, payload));
      }),
    [subscribeTo],
  );

  useEffect(
    () =>
      subscribeTo('stream_state', (progress: Progress) => {
        setData((current) => (current ? { ...current, stream: progress } : current));
      }),
    [subscribeTo],
  );

  const stale = useMemo(() => {
    if (!connected || linkDropped) return true;
    if (!data) return false;
    // The backend marks its own sample stale once it ages out.
    return data.system.stale;
  }, [connected, linkDropped, data]);

  return { data, loading, error, stale, connected, refresh };
}