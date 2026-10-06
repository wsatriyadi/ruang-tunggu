/** Broadcast windows, refreshed whenever the scheduler changes one. */

import { useEffect } from 'react';
import { useEventStream } from '../api/events';
import { listSchedules } from '../api/client';
import { useAsyncCollection } from './common';
import type { Schedule, ScheduleListResponse } from '../types';

export function useSchedules() {
  const collection = useAsyncCollection<ScheduleListResponse, Schedule>(
    listSchedules,
    (payload) => payload.items,
  );

  const { subscribeTo } = useEventStream();
  useEffect(
    () =>
      subscribeTo('schedule', () => {
        void collection.refresh();
      }),
    [subscribeTo, collection.refresh],
  );

  return collection;
}