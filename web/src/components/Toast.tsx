/**
 * Transient operator messages.
 *
 * The queue is a module-level store with a subscription, so any component can
 * raise a message without threading a callback through the tree. Errors stay
 * until dismissed; everything else fades on its own.
 */

import { useCallback, useSyncExternalStore, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

export type ToastKind = 'info' | 'success' | 'warn' | 'error';

export interface ToastItem {
  id: number;
  kind: ToastKind;
  message: string;
}

const AUTO_DISMISS_MS: Record<ToastKind, number | null> = {
  info: 4000,
  success: 4000,
  warn: 7000,
  // A failure needs the operator to read it, so it waits.
  error: null,
};

const GLYPHS: Record<ToastKind, string> = {
  info: 'ℹ',
  success: '✓',
  warn: '!',
  error: '✕',
};

/** Most toasts shown at once; a phone cannot stack more than this legibly. */
const MAX_VISIBLE = 4;

interface ToastApi {
  push: (kind: ToastKind, message: string) => number;
  dismiss: (id: number) => void;
}

let items: ToastItem[] = [];
let nextId = 1;
const timers = new Map<number, number>();
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

const getSnapshot = (): ToastItem[] => items;

function push(kind: ToastKind, message: string): number {
  const item: ToastItem = { id: nextId, kind, message };
  nextId += 1;
  items = [...items, item];

  while (items.length > MAX_VISIBLE) {
    const dropped = items[0];
    if (!dropped) break;
    items = items.slice(1);
    clearTimer(dropped.id);
  }

  const delay = AUTO_DISMISS_MS[kind];
  if (delay !== null) {
    timers.set(
      item.id,
      window.setTimeout(() => {
        dismiss(item.id);
      }, delay),
    );
  }

  emit();
  return item.id;
}

function clearTimer(id: number): void {
  const timer = timers.get(id);
  if (timer !== undefined) {
    window.clearTimeout(timer);
    timers.delete(id);
  }
}

function dismiss(id: number): void {
  clearTimer(id);
  const next = items.filter((item) => item.id !== id);
  if (next.length === items.length) return;
  items = next;
  emit();
}

/** Works with or without a provider: the store is global. */
export function useToast(): ToastApi {
  return { push, dismiss };
}

export interface ToastProps {
  item: ToastItem;
  onDismiss: (id: number) => void;
}

export function Toast({ item, onDismiss }: ToastProps) {
  return (
    <div className={`toast toast--${item.kind}`} role={item.kind === 'error' ? 'alert' : 'status'}>
      <span className="toast__glyph" aria-hidden="true">
        {GLYPHS[item.kind]}
      </span>
      <p className="toast__message">{item.message}</p>
      <button
        type="button"
        className="toast__close"
        onClick={() => onDismiss(item.id)}
        aria-label="Tutup notifikasi"
      >
        ✕
      </button>
    </div>
  );
}

export function ToastProvider({ children }: { children?: ReactNode }) {
  const visible = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const onDismiss = useCallback((id: number) => {
    dismiss(id);
  }, []);

  return (
    <>
      {children}
      {createPortal(
        <div className="toast-region" aria-live="polite" aria-atomic="false">
          {visible.map((item) => (
            <Toast key={item.id} item={item} onDismiss={onDismiss} />
          ))}
        </div>,
        document.body,
      )}
    </>
  );
}