/** Video library list, with search and pagination. */

import { listVideos, type VideoListParams } from '../api/client';
import { useAsyncCollection } from './common';
import type { Media, VideoListResponse } from '../types';

export interface VideosState {
  items: Media[];
  /** Library-wide count, which may exceed the current page. */
  total: number;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

export function useVideos(params: VideoListParams = {}): VideosState {
  const { q, limit = 50, offset = 0 } = params;

  // Inline arrows are safe here: useAsyncCollection holds them in refs.
  const collection = useAsyncCollection<VideoListResponse, Media>(
    () => listVideos({ q, limit, offset }),
    (payload) => payload.items,
    { deps: [q, limit, offset] },
  );

  return {
    items: collection.items,
    total: collection.data?.total ?? 0,
    loading: collection.loading,
    error: collection.error,
    refresh: collection.refresh,
  };
}