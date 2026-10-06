/** Log entries, filtered server-side, appended live from the `log` event. */

import { useEffect, useState } from 'react';
import { useEventStream } from '../api/events';
import { getLogs, type LogListParams } from '../api/client';
import { useAsyncCollection } from './common';
import type { LogEntry, LogsResponse } from '../types';

/** A page shows a bounded tail; older lines stay behind the export. */
const MAX_LIVE_ENTRIES = 2000;

export interface LogsState {
  /** Newest last, matching the order the server sends. */
  items: LogEntry[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  /** Empties the local view; the server keeps its history (FR-09). */
  clearLocal: () => void;
}

export function useLogs(params: LogListParams = {}): LogsState {
  const { levels, sources, session_id, q, limit = 200, since } = params;

  const collection = useAsyncCollection<LogsResponse, LogEntry>(
    () => getLogs({ levels, sources, session_id, q, limit, since }),
    (payload) => payload.entries,
    { deps: [levels, sources, session_id, q, limit, since] },
  );

  const { subscribeTo } = useEventStream();
  const [live, setLive] = useState<LogEntry[]>([]);

  useEffect(
    () =>
      subscribeTo('log', (entry) => {
        setLive((current) => {
          const next = [...current, entry];
          return next.length > MAX_LIVE_ENTRIES ? next.slice(next.length - MAX_LIVE_ENTRIES) : next;
        });
      }),
    [subscribeTo],
  );

  // Entries fetched under a previous filter must not linger beside the new set.
  const filterKey = `${levels ?? ''}|${sources ?? ''}|${session_id ?? ''}|${q ?? ''}|${limit}|${since ?? ''}`;
  useEffect(() => {
    setLive([]);
  }, [filterKey]);

  const clearLocal = () => {
    setLive([]);
    void collection.refresh();
  };

  const baseItems = collection.items;
  return {
    items: baseItems.length === 0 ? live : [...baseItems, ...live],
    loading: collection.loading,
    error: collection.error,
    refresh: collection.refresh,
    clearLocal,
  };
}