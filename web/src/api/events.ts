/**
 * Server-sent events.
 *
 * One EventSource is shared by the whole app. Every hook subscriber gets the
 * same connection, so switching pages never opens a second stream.
 *
 * The stream is allowed to be down: a dropped link flips `connected` to
 * false, the UI shows "Sambungan ke server terputus" and marks the stats stale
 * (PRD §5.3). Nothing here throws at the call site.
 */

import { useCallback, useSyncExternalStore } from 'react';
import type { EventKind, EventPayloadMap } from '../types';

export type ServerEvent = {
  [K in EventKind]: { kind: K; payload: EventPayloadMap[K] };
}[EventKind];

export type EventHandler = (event: ServerEvent) => void;
export type TypedHandler<K extends EventKind> = (payload: EventPayloadMap[K]) => void;

const EVENTS_PATH = '/api/events';
const OFFLINE_MESSAGE = 'Sambungan ke server terputus';

/** Backoff schedule in ms; the last value repeats until the server returns. */
const BACKOFF_STEPS = [1000, 2000, 4000, 8000, 15000];

/** The six kinds the backend publishes (internal/api/sse.go). */
const EVENT_KINDS: Record<string, true> = {
  metrics: true,
  stream_state: true,
  log: true,
  media: true,
  schedule: true,
  alert: true,
};

export interface StreamSnapshot {
  connected: boolean;
  /** Epoch ms of the last frame received; 0 before the first one. */
  lastEventAt: number;
  lastError: string | null;
}

let source: EventSource | null = null;
let retryTimer: number | null = null;
let retryIndex = 0;

// Replaced wholesale on every change: useSyncExternalStore compares the
// snapshot by identity, so a mutated object would never trigger a re-render.
let snapshot: StreamSnapshot = { connected: false, lastEventAt: 0, lastError: null };

const watchers = new Set<() => void>();
const handlers = new Set<EventHandler>();

function emit(): void {
  for (const watcher of watchers) watcher();
}

function update(next: Partial<StreamSnapshot>): void {
  snapshot = { ...snapshot, ...next };
  emit();
}

/**
 * Builds one event from a validated kind plus its payload.
 *
 * `kind` is checked against EVENT_KINDS first, so it is provably a key of
 * EventPayloadMap. That narrowing is what makes the union collapse safe, and
 * it is the single place the cast appears.
 */
function buildEvent<K extends keyof EventPayloadMap>(
  kind: K,
  payload: EventPayloadMap[K],
): ServerEvent {
  return { kind, payload } as ServerEvent;
}

function parse(raw: string): ServerEvent | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  if (!('kind' in parsed) || !('payload' in parsed)) return null;

  const kind = (parsed as { kind: unknown }).kind;
  if (typeof kind !== 'string' || !(kind in EVENT_KINDS)) return null;

  // The key lookup and the payload read both come off the same object, so
  // `kind` is narrowed through EventPayloadMap and the payload picks up the
  // matching member type.
  const payloadForKind = parsed as { [K in EventKind]: { payload: EventPayloadMap[K] } }[
    EventKind
  ];
  return buildEvent(kind as keyof EventPayloadMap, payloadForKind.payload);
}

function clearRetry(): void {
  if (retryTimer !== null) {
    window.clearTimeout(retryTimer);
    retryTimer = null;
  }
}

function scheduleReconnect(): void {
  if (retryTimer !== null) return;
  const delay = BACKOFF_STEPS[Math.min(retryIndex, BACKOFF_STEPS.length - 1)];
  retryIndex += 1;
  retryTimer = window.setTimeout(() => {
    retryTimer = null;
    connect();
  }, delay);
}

function markOnline(): void {
  retryIndex = 0;
  update({ connected: true, lastError: null });
}

function markOffline(): void {
  update({ connected: false, lastError: OFFLINE_MESSAGE });
}

function connect(): void {
  if (source !== null || typeof EventSource === 'undefined') return;

  let next: EventSource;
  try {
    next = new EventSource(EVENTS_PATH, { withCredentials: true });
  } catch {
    markOffline();
    scheduleReconnect();
    return;
  }

  next.onopen = () => markOnline();

  next.onmessage = (event: MessageEvent<string>) => {
    update({ lastEventAt: Date.now() });
    const parsed = parse(event.data);
    if (!parsed) return;
    for (const handler of handlers) handler(parsed);
  };

  // The server greets with `event: ready`, which never reaches onmessage.
  next.addEventListener('ready', () => markOnline());

  next.onerror = () => {
    // EventSource would retry on its own, but with no backoff control, so the
    // connection is rebuilt here instead.
    next.close();
    if (source === next) source = null;
    markOffline();
    scheduleReconnect();
  };

  source = next;
}

/** Force an immediate reconnection, e.g. from a "Coba lagi" button. */
function reconnectNow(): void {
  source?.close();
  source = null;
  clearRetry();
  retryIndex = 0;
  connect();
}

function disconnect(): void {
  source?.close();
  source = null;
  clearRetry();
  retryIndex = 0;
  update({ connected: false, lastError: null });
}

function subscribeHandler(handler: EventHandler): () => void {
  handlers.add(handler);
  return () => {
    handlers.delete(handler);
  };
}

/**
 * The connection lives exactly as long as somebody is watching it: the first
 * watcher opens the stream, the last one to leave closes it.
 */
function subscribeWatcher(watcher: () => void): () => void {
  watchers.add(watcher);
  clearRetry();
  retryIndex = 0;
  connect();
  return () => {
    watchers.delete(watcher);
    if (watchers.size === 0) disconnect();
  };
}

const getSnapshot = (): StreamSnapshot => snapshot;

export interface EventStream {
  connected: boolean;
  lastEventAt: number;
  lastError: string | null;
  subscribe: (handler: EventHandler) => () => void;
  subscribeTo: <K extends EventKind>(kind: K, handler: TypedHandler<K>) => () => void;
  reconnect: () => void;
}

/** Imperative access to the same stream, outside React. */
export function getEventStream(): Omit<EventStream, 'subscribeTo'> {
  return {
    get connected() {
      return snapshot.connected;
    },
    get lastEventAt() {
      return snapshot.lastEventAt;
    },
    get lastError() {
      return snapshot.lastError;
    },
    subscribe: subscribeHandler,
    reconnect: reconnectNow,
  };
}

/** Subscribe to every event, and to the connection state. */
export function useEventStream(): EventStream {
  const { connected, lastEventAt, lastError } = useSyncExternalStore(
    subscribeWatcher,
    getSnapshot,
    getSnapshot,
  );

  const subscribe = useCallback((handler: EventHandler) => subscribeHandler(handler), []);
  const reconnect = useCallback(() => reconnectNow(), []);

  const subscribeTo = useCallback(
    <K extends EventKind>(kind: K, handler: TypedHandler<K>) =>
      subscribeHandler((event) => {
        if (event.kind !== kind) return;
        // A generic kind cannot be narrowed through the union, so the match
        // above is what establishes the payload type for this handler.
        const payload: EventPayloadMap[K] = event.payload as EventPayloadMap[K];
        handler(payload);
      }),
    [],
  );

  return { connected, lastEventAt, lastError, subscribe, subscribeTo, reconnect };
}

/** Seconds since the last frame; infinity before the first one arrives. */
export function secondsSinceLastEvent(lastEventAt: number, now = Date.now()): number {
  if (!lastEventAt) return Number.POSITIVE_INFINITY;
  return (now - lastEventAt) / 1000;
}