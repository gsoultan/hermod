import { useEffect, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { MarkerType } from '@xyflow/react';
import { apiFetch } from '@/api';
import { notifications } from '@mantine/notifications';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { persistedSnapshot } from '../store/persistedSnapshot';

const API_BASE = '/api';

/** The store state a server workflow is drawn from. */
function toStoreValues(workflow: any): any {
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

  newValues.nodes = (workflow.nodes || []).map((node: any) => ({
    id: node.id,
    type: node.type,
    position: { x: node.x || 0, y: node.y || 0 },
    data: { ...(node.config || {}), ref_id: node.ref_id }
  }));

  newValues.edges = (workflow.edges || []).map((edge: any) => ({
    id: edge.id,
    source: edge.source_id,
    target: edge.target_id,
    sourceHandle: edge.source_handle,
    targetHandle: edge.target_handle,
    type: 'live',
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
  // A simulation describes the workflow it ran on. The store outlives this
  // page, so without this the next workflow opened was drawn with the last
  // one's result.
  newValues.testResults = null;
  return newValues;
}

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
    },
    // Coming back to the tab is when an edit made elsewhere is noticed. It
    // only ever replaces a canvas with nothing unsaved; see the effect below.
    refetchOnWindowFocus: !isNew,
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

  // The server version the newer-version warning was last shown for, so a
  // refetch of the same version does not raise it again.
  const warnedFor = useRef<string | null>(null);

  useEffect(() => {
    const wId = id || 'new';
    if (!workflow) return;

    const values = toStoreValues(workflow);
    const serverSnap = persistedSnapshot(values);

    if (lastInitializedId.current !== wId) {
      lastInitializedId.current = wId;
      warnedFor.current = null;
      useWorkflowStore.setState({ ...values, persistedBaseline: serverSnap });
      return;
    }

    // A later fetch of the same workflow: a rollback, a save coming back, or
    // an edit made somewhere else. The canvas used to ignore all of them, so
    // a rollback was overwritten by the next Save of the graph it replaced.
    const state = useWorkflowStore.getState();
    // persistedBaseline is also moved by a save, to what the save sent.
    if (serverSnap === state.persistedBaseline) return;

    const localSnap = persistedSnapshot(state);
    if (serverSnap === localSnap) {
      useWorkflowStore.setState({ persistedBaseline: serverSnap });
      return;
    }

    if (localSnap === state.persistedBaseline) {
      warnedFor.current = null;
      const { selectedNode } = state;
      useWorkflowStore.setState({
        ...values,
        persistedBaseline: serverSnap,
        // The open node's panel reads this copy, so it moves to the reloaded
        // node, or closes if the new version no longer has it.
        selectedNode: selectedNode
          ? values.nodes.find((n: any) => n.id === selectedNode.id) ?? null
          : null,
      });
      return;
    }

    // Unsaved edits on an older version. They are the operator's and stay;
    // what they need to know is that saving replaces the newer version.
    if (warnedFor.current === serverSnap) return;
    warnedFor.current = serverSnap;
    notifications.show({
      id: `workflow-newer-version-${wId}`,
      title: 'A newer version of this workflow was saved',
      message:
        'Your unsaved changes are kept. Saving now will replace the newer version; ' +
        'reload the page to discard your changes and open it instead.',
      color: 'orange',
      autoClose: false,
    });
  }, [id, workflow]);

  return { workflow, isLoading, isNew };
}
