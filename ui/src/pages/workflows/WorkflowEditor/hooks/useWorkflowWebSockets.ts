import { useEffect } from 'react';
import { getToken } from '@/auth/storage';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useWorkflowWebSockets(id: string, active: boolean, logsPaused: boolean) {
  // WebSocket for logs
  useEffect(() => {
    if (!id || id === 'new' || !active || logsPaused) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `&token=${token}` : '';
    const ws = new WebSocket(`${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}${tokenParam}`);

    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data);
        useWorkflowStore.setState((state) => {
          if (Array.isArray(log)) {
            return { logs: log.slice(0, 100) };
          } else {
            return { logs: [log, ...state.logs].slice(0, 100) };
          }
        });
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active, logsPaused]);

  // WebSocket for status
  useEffect(() => {
    if (!id || id === 'new' || !active) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `?token=${token}` : '';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status${tokenParam}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
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
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active]);
}
