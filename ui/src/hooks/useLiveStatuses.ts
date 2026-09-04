import { useEffect, useRef, useState } from 'react';

export interface LiveStatus {
  workflow_id: string;
  source_id?: string;
  sink_id?: string;
  source_status?: string;
  sink_status?: string;
  [key: string]: unknown;
}

/**
 * Most workflows whose live status is worth holding on one screen.
 *
 * The map is keyed by an id that arrives over the network, so it needs a bound
 * and an eviction. Without one it grew for the lifetime of the page. The list
 * pages behind it show 30 rows, so this is generous.
 */
export const LIVE_STATUS_LIMIT = 200;

/** Reconnect backoff, milliseconds. Jittered so tabs do not resynchronise. */
const BACKOFF_MS = [1_000, 2_000, 4_000, 8_000, 15_000, 30_000];

/**
 * Live workflow status over the shared status socket.
 *
 * Replaces two identical copies of this effect in SourcesPage and SinksPage,
 * both of which had the same three problems:
 *
 *  - the map was unbounded and never evicted;
 *  - every frame did a full object spread and re-rendered the page, so a busy
 *    system re-rendered a 30-row table many times a second;
 *  - a dropped socket was never re-established and never surfaced, so badges
 *    froze on their last value and went on reporting a stopped pipeline as
 *    running — the failure mode an operations tool can least afford.
 *
 * Frames are coalesced into one state update per animation frame, the map is
 * bounded with oldest-first eviction, and `connected` lets callers say plainly
 * that what is on screen may be stale.
 */
export function useLiveStatuses() {
  const [statuses, setStatuses] = useState<Record<string, LiveStatus>>({});
  const [connected, setConnected] = useState(false);

  // Held outside state so a burst of frames costs one render, not one each.
  const pending = useRef<Map<string, LiveStatus>>(new Map());
  const flushHandle = useRef<number | null>(null);

  useEffect(() => {
    let socket: WebSocket | null = null;
    let retry = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const flush = () => {
      flushHandle.current = null;
      if (pending.current.size === 0) return;
      const batch = pending.current;
      pending.current = new Map();

      setStatuses((prev) => {
        const next = { ...prev };
        for (const [id, update] of batch) next[id] = update;

        // Oldest-first eviction. Object key order is insertion order for string
        // keys, and re-assigning an existing key keeps its original position, so
        // the front of this list is genuinely the least recently *added*.
        const keys = Object.keys(next);
        if (keys.length > LIVE_STATUS_LIMIT) {
          for (const stale of keys.slice(0, keys.length - LIVE_STATUS_LIMIT)) {
            delete next[stale];
          }
        }
        return next;
      });
    };

    const scheduleFlush = () => {
      if (flushHandle.current !== null) return;
      flushHandle.current =
        typeof requestAnimationFrame === 'function'
          ? requestAnimationFrame(flush)
          : (setTimeout(flush, 16) as unknown as number);
    };

    const connect = () => {
      if (cancelled) return;
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      socket = new WebSocket(`${protocol}//${window.location.host}/api/ws/status`);

      socket.onopen = () => {
        retry = 0;
        setConnected(true);
      };

      socket.onmessage = (event: MessageEvent) => {
        try {
          const update = JSON.parse(event.data) as LiveStatus;
          // A frame without an id has nowhere to go; dropping it is correct and
          // must not take the connection down with it.
          if (!update || typeof update.workflow_id !== 'string') return;
          pending.current.set(update.workflow_id, update);
          scheduleFlush();
        } catch {
          // Malformed frame. Ignore it and keep the socket.
        }
      };

      const onDown = () => {
        setConnected(false);
        if (cancelled) return;
        const base = BACKOFF_MS[Math.min(retry, BACKOFF_MS.length - 1)];
        retry += 1;
        // Jitter so every open tab does not retry on the same tick.
        const wait = base + Math.floor(Math.random() * 500);
        reconnectTimer = setTimeout(connect, wait);
      };

      socket.onclose = onDown;
      socket.onerror = () => socket?.close();
    };

    connect();

    return () => {
      cancelled = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (flushHandle.current !== null) {
        if (typeof cancelAnimationFrame === 'function') cancelAnimationFrame(flushHandle.current);
        else clearTimeout(flushHandle.current as unknown as ReturnType<typeof setTimeout>);
        flushHandle.current = null;
      }
      pending.current.clear();
      if (socket) {
        // Drop the handler first: close() fires onclose, which would otherwise
        // schedule a reconnect for a page that is going away.
        socket.onclose = null;
        socket.onerror = null;
        socket.close();
      }
    };
  }, []);

  return { statuses, connected };
}
