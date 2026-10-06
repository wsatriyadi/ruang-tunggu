/**
 * The shape every small collection hook returns, so pages handle loading and
 * failure the same way regardless of which resource they show.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { errorMessage } from '../api/client';

export interface AsyncCollection<TResponse, TItem> {
  items: TItem[];
  /** The full response, for fields beside the items (total, counts). */
  data: TResponse | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

export interface AsyncCollectionOptions {
  /** Change signal: a new value re-runs the fetch. */
  deps?: readonly unknown[];
  /** Set false to skip the initial fetch. */
  enabled?: boolean;
}

/**
 * Fetches a resource once per `deps` change, and on demand.
 *
 * `fetcher` and `select` are read through refs, so a caller may pass inline
 * arrows without re-triggering the effect on every render. A response from a
 * superseded request is discarded, so fast typing cannot leave stale results.
 */
export function useAsyncCollection<TResponse, TItem>(
  fetcher: () => Promise<TResponse>,
  select: (payload: TResponse) => TItem[],
  options: AsyncCollectionOptions = {},
): AsyncCollection<TResponse, TItem> {
  const { deps = [], enabled = true } = options;
  const [data, setData] = useState<TResponse | null>(null);
  const [items, setItems] = useState<TItem[]>([]);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<string | null>(null);

  const generation = useRef(0);
  const fetcherRef = useRef(fetcher);
  const selectRef = useRef(select);
  fetcherRef.current = fetcher;
  selectRef.current = select;

  const refresh = useCallback(async () => {
    generation.current += 1;
    const mine = generation.current;
    setLoading(true);
    try {
      const payload = await fetcherRef.current();
      if (generation.current !== mine) return;
      setData(payload);
      setItems(selectRef.current(payload));
      setError(null);
    } catch (e) {
      if (generation.current !== mine) return;
      setError(errorMessage(e, 'Gagal memuat data.'));
    } finally {
      if (generation.current === mine) setLoading(false);
    }
  }, []);

  // depsKey collapses an inline array into a stable comparable value.
  const depsKey = JSON.stringify(deps);
  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      return;
    }
    void refresh();
    // depsKey is the caller's change signal; eslint cannot see through JSON.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, refresh, depsKey]);

  return { items, data, loading, error, refresh };
}