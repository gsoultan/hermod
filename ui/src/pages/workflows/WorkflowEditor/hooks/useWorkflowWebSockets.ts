import { useEffect } from 'react';
import { getToken } from '@/auth/storage';
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

export function useWorkflowWebSockets(id: string, active: boolean, logsPaused: boolean) {
  // WebSocket for logs
  useEffect(() => {
    if (!id || id === 'new' || !active || logsPaused) return;

    const buildUrl = () => {
      const token = getToken();
      const tokenParam = token ? `&token=${encodeURIComponent(token)}` : '';
      return `${getWsProtocol()}//${window.location.host}/api/ws/logs?workflow_id=${encodeURIComponent(id)}${tokenParam}`;
    };

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
      } catch (e) {
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

    const buildUrl = () => {
      const token = getToken();
      const base = `${getWsProtocol()}//${window.location.host}/api/ws/status`;
      return token
        ? `${base}?token=${encodeURIComponent(token)}&workflow_id=${encodeURIComponent(id)}`
        : `${base}?workflow_id=${encodeURIComponent(id)}`;
    };

    const onMessage = (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data);
        const updates = Array.isArray(data) ? data : [data];

        updates.forEach(update => {
          if (update.workflow_id === id) {
            const currentStore = useWorkflowStore.getState();
            const nextState: any = {};

            if (update.node_metrics) {
              if (JSON.stringify(currentStore.nodeMetrics) !== JSON.stringify(update.node_metrics)) {
                nextState.nodeMetrics = update.node_metrics;
              }
            }
            if (update.node_error_metrics) {
              if (JSON.stringify(currentStore.nodeErrorMetrics) !== JSON.stringify(update.node_error_metrics)) {
                nextState.nodeErrorMetrics = update.node_error_metrics;
              }
            }
            if (update.node_samples) {
              if (JSON.stringify(currentStore.nodeSamples) !== JSON.stringify(update.node_samples)) {
                nextState.nodeSamples = update.node_samples;
              }
            }
            if (update.sink_cb_statuses) {
              if (JSON.stringify(currentStore.sinkCBStatuses) !== JSON.stringify(update.sink_cb_statuses)) {
                nextState.sinkCBStatuses = update.sink_cb_statuses;
              }
            }
            if (update.sink_buffer_fill) {
              if (JSON.stringify(currentStore.sinkBufferFill) !== JSON.stringify(update.sink_buffer_fill)) {
                nextState.sinkBufferFill = update.sink_buffer_fill;
              }
            }
            if (update.edge_metrics) {
              if (JSON.stringify(currentStore.edgeThroughput) !== JSON.stringify(update.edge_metrics)) {
                nextState.edgeThroughput = update.edge_metrics;
              }
            }
            if (update.source_status) {
              if (currentStore.sourceStatus !== update.source_status) {
                nextState.sourceStatus = update.source_status;
              }
            }
            if (update.sink_statuses) {
              if (JSON.stringify(currentStore.sinkStatuses) !== JSON.stringify(update.sink_statuses)) {
                nextState.sinkStatuses = update.sink_statuses;
              }
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
      } catch (e) {
        // Ignore malformed status frames.
      }
    };

    return connectWithReconnect(buildUrl, onMessage);
  }, [id]);
}
