/** Playlists with their latest revision resolved, refreshed on `media` events. */

import { useEffect } from 'react';
import { useEventStream } from '../api/events';
import { listPlaylists } from '../api/client';
import { useAsyncCollection } from './common';
import type { Playlist, PlaylistListResponse } from '../types';

export function usePlaylists() {
  const collection = useAsyncCollection<PlaylistListResponse, Playlist>(
    listPlaylists,
    (payload) => payload.items,
  );

  // Saving a playlist publishes a media event, so the list reloads itself
  // rather than waiting for the operator to come back to this page.
  const { subscribeTo } = useEventStream();
  useEffect(
    () =>
      subscribeTo('media', (payload) => {
        if (payload.action === 'playlist_updated') void collection.refresh();
      }),
    [subscribeTo, collection.refresh],
  );

  return collection;
}