import { useEffect, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { MarkerType } from '@xyflow/react';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '../store/useWorkflowStore';

const API_BASE = '/api';

export function useWorkflowInitialization(id: string, selectedVHost: string) {
  const isNew = !id || id === 'new';
  const lastInitializedId = useRef<string | null>(null);

  const { data: workflow, isLoading } = useQuery({
    queryKey: ['workflow', id || 'new'],
    queryFn: async () => {
      if (isNew) return { 
        name: 'New Workflow', 
        vhost: selectedVHost === 'all' ? 'default' : selectedVHost, 
        worker_id: '', 
        nodes: [], 
        edges: [] 
      };
      const res = await apiFetch(`${API_BASE}/workflows/${id}`);
      return res.json();
    }
  });

  const workerID = useWorkflowStore(state => state.workerID);

  useEffect(() => {
    if (isNew && !workerID) {
      apiFetch(`${API_BASE}/workers/recommend`)
        .then(res => res.json())
        .then(data => {
          if (data && data.id) {
            useWorkflowStore.getState().setWorkerID(data.id);
          }
        })
        .catch(err => console.error('Failed to fetch recommended worker', err));
    }
  }, [isNew, workerID]);

  useEffect(() => {
    const wId = id || 'new';
    if (workflow && lastInitializedId.current !== wId) {
      lastInitializedId.current = wId;
      
      const newValues: any = {
        name: workflow.name || '',
        vhost: workflow.vhost || 'default',
        workerID: workflow.worker_id || '',
        active: workflow.active || false,
        workflowStatus: workflow.status || 'Stopped',
        deadLetterSinkID: workflow.dead_letter_sink_id || '',
        dlqThreshold: workflow.dlq_threshold || 0,
        prioritizeDLQ: workflow.prioritize_dlq || false,
        maxRetries: workflow.max_retries || 3,
        retryInterval: workflow.retry_interval || '100ms',
        reconnectInterval: workflow.reconnect_interval || '30s',
        schemaType: workflow.schema_type || '',
        schema: workflow.schema || '',
        tags: workflow.tags || [],
        dryRun: workflow.dry_run || false,
        workspaceID: workflow.workspace_id || '',
        cpuRequest: workflow.cpu_request || 0,
        memoryRequest: workflow.memory_request || 0,
        throughputRequest: workflow.throughput_request || 0,
        idleTimeout: workflow.idle_timeout || '',
        tier: workflow.tier || 'Hot',
        cron: workflow.cron || '',
        retentionDays: workflow.retention_days !== undefined ? workflow.retention_days : null,
        traceSampleRate: workflow.trace_sample_rate !== undefined ? workflow.trace_sample_rate : 1.0,
        traceRetention: workflow.trace_retention || '7d',
        auditRetention: workflow.audit_retention || '30d',
        historyOpened: false,
      };

      const initialNodes = (workflow.nodes || []).map((node: any) => ({
        id: node.id,
        type: node.type,
        position: { x: node.x || 0, y: node.y || 0 },
        data: { ...(node.config || {}), ref_id: node.ref_id }
      }));
      newValues.nodes = initialNodes;

      const initialEdges = (workflow.edges || []).map((edge: any) => ({
        id: edge.id,
        source: edge.source_id,
        target: edge.target_id,
        data: edge.config,
        animated: workflow.active,
        style: { strokeWidth: workflow.active ? 3 : 2 },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 20,
          height: 20,
          color: workflow.active ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-gray-5)',
        }
      }));
      newValues.edges = initialEdges;
      newValues.historyOpened = false;

      useWorkflowStore.setState(newValues);
    }
  }, [id, workflow?.id, workflow?.name]);

  return { workflow, isLoading, isNew };
}
