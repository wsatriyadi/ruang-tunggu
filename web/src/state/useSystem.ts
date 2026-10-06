/** Static device inventory for the header's device info (host, OS, CPU). */

import { useCallback, useEffect, useState } from 'react';
import { errorMessage, getSystem } from '../api/client';
import type { SystemResponse } from '../types';

export interface SystemState {
  data: SystemResponse | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

export function useSystem(): SystemState {
  const [data, setData] = useState<SystemResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setData(await getSystem());
      setError(null);
    } catch (e) {
      setError(errorMessage(e, 'Gagal memuat informasi perangkat.'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { data, loading, error, refresh };
}