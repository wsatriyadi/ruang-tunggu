/**
 * Session state.
 *
 * A module-level store rather than a provider: every page needs to know
 * whether it may render, and the session changes rarely enough that a
 * re-render of the whole tree is cheap. `useSession` subscribes to it, so
 * calling the hook anywhere gives the same answer.
 */

import { useSyncExternalStore } from 'react';
import {
  clearCsrfToken,
  errorMessage,
  getAuthState,
  login as loginRequest,
  logout as logoutRequest,
  setCsrfToken,
  setup as setupRequest,
} from '../api/client';
import type { AuthState, LoginRequest, SetupRequest } from '../types';

export interface SessionStore {
  /** null until /auth/state has answered. */
  state: AuthState | null;
  loading: boolean;
  app: string;
  version: string;
  error: string | null;
  login: (body: LoginRequest) => Promise<void>;
  setup: (body: SetupRequest) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const store = {
  state: null as AuthState | null,
  loading: true,
  app: 'YouTube Live Server',
  version: '',
  error: null as string | null,
};

const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

// useSyncExternalStore compares snapshots by Object.is, so the value handed to
// it must be a fresh immutable object on every change. Mutating one shared
// object leaves the panel stuck on its loading spinner, because React never sees
// a changed reference.
let snapshotValue: SessionStore;

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

const snapshot = (): SessionStore => snapshotValue;

/** Merge into the store and publish a fresh snapshot so React re-renders. */
function patch(next: Partial<typeof store>): void {
  Object.assign(store, next);
  snapshotValue = { ...store, ...actions };
  emit();
}

let inFlight: Promise<void> | null = null;

async function refresh(): Promise<void> {
  if (inFlight) return inFlight;
  inFlight = (async () => {
    patch({ loading: true });
    try {
      const res = await getAuthState();
      patch({ state: res.state, app: res.app, version: res.version, error: null });
    } catch (e) {
      patch({ state: null, error: errorMessage(e, 'Gagal memeriksa sesi.') });
    } finally {
      patch({ loading: false });
      inFlight = null;
    }
  })();
  return inFlight;
}

/** Both credential calls land here: adopt the token, then confirm the state. */
async function completeAuth(resolve: () => Promise<{ username: string; csrf_token: string }>) {
  patch({ loading: true });
  try {
    const session = await resolve();
    setCsrfToken(session.csrf_token);
    patch({ error: null });
    await refresh();
  } catch (e) {
    patch({ error: errorMessage(e, 'Autentikasi gagal.') });
    throw e;
  } finally {
    patch({ loading: false });
  }
}

const actions: Pick<SessionStore, 'login' | 'setup' | 'logout' | 'refresh'> = {
  login: (body) => completeAuth(() => loginRequest(body)),
  setup: (body) => completeAuth(() => setupRequest(body)),
  logout: async () => {
    try {
      await logoutRequest();
    } finally {
      clearCsrfToken();
      patch({ state: 'login' });
    }
  },
  refresh,
};

// Publish the initial snapshot now that the actions exist, so the very first
// render already has a stable object to compare against.
snapshotValue = { ...store, ...actions };

// The first caller triggers the check; later ones reuse it.
if (typeof window !== 'undefined') void refresh();

export function useSession(): SessionStore {
  const current = useSyncExternalStore(subscribe, snapshot, snapshot);
  return { ...current, ...actions };
}

/** Non-hook read, for route guards outside a component. */
export function getSessionState(): AuthState | null {
  return store.state;
}