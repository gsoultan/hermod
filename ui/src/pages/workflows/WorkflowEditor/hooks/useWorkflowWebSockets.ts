import { useEffect, useRef } from 'react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { connectWithReconnect } from '@/lib/reconnectingSocket';
import { advanceFlows } from '../live/edgeFlow';
import { FAILING_WINDOW_MS, isEngineLive } from '../live/liveHealth';

/**
 * A running workflow that has sent no status for this long is no longer drawn
 * as if its last numbers were current. While the editor is open the engine
 * sends a frame every second; its own health tick is every five.
 */
export const STATUS_STALE_AFTER_MS = 15_000;

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

/**
 * Fills the workflow store from the workflow's log and status feeds. The
 * editor and the read-only view both use it, so each step's counts, samples
 * and problems, and each edge's flow, read the same on either page.
 *
 * onStatus, when given, is handed every status frame for this workflow as it
 * arrives, for what a page shows that the store does not keep.
 */
export function useWorkflowWebSockets(id: string, active: boolean, logsPaused: boolean, onStatus?: (update: any) => void) {
  const onStatusRef = useRef(onStatus);
  useEffect(() => {
    onStatusRef.current = onStatus;
  });

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

    // When the last frame arrived, by this browser's clock: what staleness is
    // measured against. Null until the first one.
    let lastFrameAt: number | null = null;
    const setStale = (stale: boolean) => {
      if (useWorkflowStore.getState().statusFeedStale !== stale) {
        useWorkflowStore.setState({ statusFeedStale: stale });
      }
    };

    const onMessage = (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data);
        const updates = Array.isArray(data) ? data : [data];

        updates.forEach(update => {
          if (update.workflow_id === id) {
            onStatusRef.current?.(update);
            lastFrameAt = Date.now();
            const currentStore = useWorkflowStore.getState();
            const nextState: any = {};
            if (currentStore.statusFeedStale) nextState.statusFeedStale = false;

            // When the engine read its counters. A frame from an engine that
            // does not send it is timed on arrival instead.
            const sampledAt: number = typeof update.sampled_at === 'number' ? update.sampled_at : lastFrameAt;
            // Every engine frame carries sampled_at, and leaves an empty map
            // out; in such a frame a missing map means none.
            const fullFrame = typeof update.sampled_at === 'number';

            if (update.node_metrics) {
              const next = shareEntries(currentStore.nodeMetrics, update.node_metrics);
              if (next !== currentStore.nodeMetrics) nextState.nodeMetrics = next;
            }
            if (update.node_error_metrics) {
              const next = shareEntries(currentStore.nodeErrorMetrics, update.node_error_metrics);
              if (next !== currentStore.nodeErrorMetrics) nextState.nodeErrorMetrics = next;
              const flows = advanceFlows(currentStore.nodeErrorFlows, update.node_error_metrics, sampledAt, FAILING_WINDOW_MS);
              if (flows !== currentStore.nodeErrorFlows) nextState.nodeErrorFlows = flows;
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
            if (update.edge_counts || fullFrame) {
              const next = advanceFlows(currentStore.edgeFlows, update.edge_counts ?? {}, sampledAt);
              if (next !== currentStore.edgeFlows) nextState.edgeFlows = next;
            }
            if (update.node_busy || fullFrame) {
              const next = shareEntries(currentStore.nodeBusy, update.node_busy ?? {});
              if (next !== currentStore.nodeBusy) nextState.nodeBusy = next;
            }
            if (update.node_held || fullFrame) {
              const next = shareEntries(currentStore.nodeHeld, update.node_held ?? {});
              if (next !== currentStore.nodeHeld) nextState.nodeHeld = next;
            }
            if (update.node_latencies) {
              const next = shareEntries(currentStore.nodeLatencies, update.node_latencies);
              if (next !== currentStore.nodeLatencies) nextState.nodeLatencies = next;
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

    // A closed socket means nothing on screen is current any more; a running
    // workflow that has gone silent means the same thing, more slowly.
    const onConnection = (open: boolean) => {
      if (!open) setStale(true);
    };
    const watchdog = setInterval(() => {
      const running = isEngineLive(useWorkflowStore.getState().workflowStatus);
      if (!running || lastFrameAt === null) return;
      if (Date.now() - lastFrameAt > STATUS_STALE_AFTER_MS) setStale(true);
    }, 1000);

    const close = connectWithReconnect(buildUrl, onMessage, onConnection);
    return () => {
      clearInterval(watchdog);
      close();
    };
  }, [id]);
}
