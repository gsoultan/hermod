import { useEffect } from 'react';
import { useWorkflowStore } from '../store/useWorkflowStore';

const MAX_RECONNECT_DELAY = 30000;
const BASE_RECONNECT_DELAY = 1000;

/**
 * connectWithReconnect opens a WebSocket and transparently re-establishes the
 * connection (with capped exponential backoff) whenever it closes unexpectedly.
 * It returns a teardown function that permanently stops reconnection and closes
 * the active socket.
 */
function connectWithReconnect(
  buildUrl: () => string,
  onMessage: (event: MessageEvent) => void,
): () => void {
  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let closed = false;

  const open = () => {
    if (closed) return;

    ws = new WebSocket(buildUrl());

    ws.onmessage = onMessage;

    ws.onopen = () => {
      attempt = 0;
    };

    ws.onclose = () => {
      if (closed) return;
      // Reconnect with capped exponential backoff and jitter to avoid a
      // thundering herd of reconnect attempts.
      const delay = Math.min(
        BASE_RECONNECT_DELAY * 2 ** attempt,
        MAX_RECONNECT_DELAY,
      );
      attempt += 1;
      const jitter = delay * (0.8 + Math.random() * 0.4);
      reconnectTimer = setTimeout(open, jitter);
    };

    ws.onerror = () => {
      // Force a close so the onclose handler schedules a reconnect.
      ws?.close();
    };
  };

  open();

  return () => {
    closed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (ws) {
      ws.onclose = null;
      ws.onerror = null;
      ws.close();
    }
  };
}

function getWsProtocol(): string {
  return window.location.protocol === 'https:' ? 'wss:' : 'ws:';
}

/** Deep equality of two JSON values, without serialising either. */
function sameJson(a: any, b: any): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keys = Object.keys(a);
  if (keys.length !== Object.keys(b).length) return false;
  for (const k of keys) {
    if (!Object.prototype.hasOwnProperty.call(b, k) || !sameJson(a[k], b[k])) return false;
  }
  return true;
}

/**
 * The map a status frame reports, keeping each entry that did not change as
 * the object already in the store, and the store's map itself when nothing
 * did. A canvas edge reads one entry, so it re-renders only when that entry
 * changes. This used to compare whole maps by serialising both on every frame
 * and then replace every entry.
 */
function shareEntries<T>(prev: Record<string, T>, next: Record<string, T>): Record<string, T> {
  const nextKeys = Object.keys(next);
  let changed = nextKeys.length !== Object.keys(prev).length;
  const out: Record<string, T> = {};
  for (const k of nextKeys) {
    const old = prev[k];
    if (Object.prototype.hasOwnProperty.call(prev, k) && sameJson(old, next[k])) {
      out[k] = old;
    } else {
      out[k] = next[k];
      changed = true;
    }
  }
  return changed ? out : prev;
}

export function useWorkflowWebSockets(id: string, active: boolean, logsPaused: boolean) {
  // WebSocket for logs
  useEffect(() => {
    if (!id || id === 'new' || !active || logsPaused) return;

    // Authenticated by the HttpOnly session cookie, sent on the same-origin
    // WebSocket handshake. No credential in the URL.
    const buildUrl = () =>
      `${getWsProtocol()}//${window.location.host}/api/ws/logs?workflow_id=${encodeURIComponent(id)}`;

    const onMessage = (event: MessageEvent) => {
      try {
        const log = JSON.parse(event.data);
        useWorkflowStore.setState((state) => {
          if (Array.isArray(log)) {
            return { logs: log.slice(0, 100) };
          } else {
            return { logs: [log, ...state.logs].slice(0, 100) };
          }
        });
      } catch {
        // Ignore malformed log frames.
      }
    };

    return connectWithReconnect(buildUrl, onMessage);
  }, [id, active, logsPaused]);

  // WebSocket for status.
  // Not gated on `active`: the engine reports lifecycle transitions
  // (running, reconnecting, restarting, stopping, stopped) in real time, so the
  // connection must stay open regardless of the workflow's active flag to keep
  // the UI status in sync with the backend engine.
  useEffect(() => {
    if (!id || id === 'new') return;

    const buildUrl = () =>
      `${getWsProtocol()}//${window.location.host}/api/ws/status?workflow_id=${encodeURIComponent(id)}`;

    const onMessage = (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data);
        const updates = Array.isArray(data) ? data : [data];

        updates.forEach(update => {
          if (update.workflow_id === id) {
            const currentStore = useWorkflowStore.getState();
            const nextState: any = {};

            if (update.node_metrics) {
              const next = shareEntries(currentStore.nodeMetrics, update.node_metrics);
              if (next !== currentStore.nodeMetrics) nextState.nodeMetrics = next;
            }
            if (update.node_error_metrics) {
              const next = shareEntries(currentStore.nodeErrorMetrics, update.node_error_metrics);
              if (next !== currentStore.nodeErrorMetrics) nextState.nodeErrorMetrics = next;
            }
            if (update.node_samples) {
              const next = shareEntries(currentStore.nodeSamples, update.node_samples);
              if (next !== currentStore.nodeSamples) nextState.nodeSamples = next;
            }
            if (update.sink_cb_statuses) {
              const next = shareEntries(currentStore.sinkCBStatuses, update.sink_cb_statuses);
              if (next !== currentStore.sinkCBStatuses) nextState.sinkCBStatuses = next;
            }
            if (update.sink_buffer_fill) {
              const next = shareEntries(currentStore.sinkBufferFill, update.sink_buffer_fill);
              if (next !== currentStore.sinkBufferFill) nextState.sinkBufferFill = next;
            }
            if (update.edge_metrics) {
              const next = shareEntries(currentStore.edgeThroughput, update.edge_metrics);
              if (next !== currentStore.edgeThroughput) nextState.edgeThroughput = next;
            }
            if (update.source_status) {
              if (currentStore.sourceStatus !== update.source_status) {
                nextState.sourceStatus = update.source_status;
              }
            }
            if (update.sink_statuses) {
              const next = shareEntries(currentStore.sinkStatuses, update.sink_statuses);
              if (next !== currentStore.sinkStatuses) nextState.sinkStatuses = next;
            }
            if (update.dead_letter_count !== undefined) {
              if (currentStore.workflowDeadLetterCount !== update.dead_letter_count) {
                nextState.workflowDeadLetterCount = update.dead_letter_count;
              }
            }
            if (update.engine_status) {
              if (currentStore.workflowStatus !== update.engine_status) {
                nextState.workflowStatus = update.engine_status;
              }
            }

            if (Object.keys(nextState).length > 0) {
              useWorkflowStore.setState(nextState);
            }
          }
        });
      } catch {
        // Ignore malformed status frames.
      }
    };

    return connectWithReconnect(buildUrl, onMessage);
  }, [id]);
}
