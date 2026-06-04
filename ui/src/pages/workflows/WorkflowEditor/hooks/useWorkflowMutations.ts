import { useCallback } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '../store/useWorkflowStore';
import type { Source, Sink } from '@/types';

const API_BASE = '/api';

export function useWorkflowMutations(
  id: string, 
  isNew: boolean, 
  sourcesData: Source[] | undefined,
  setSaveConfirmOpened: (opened: boolean) => void
) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  
  const { 
    name, active, testInput, selectedNode,
    setWorkflowStatus, setActive, setTestResults, setTestModalOpened,
    updateNodeConfig, setSettingsOpened, setSelectedNode
  } = useWorkflowStore();

  const testMutation = useMutation<any, Error, { input: any, dryRun?: boolean }>({
    mutationFn: async ({ input, dryRun }) => {
      const s = useWorkflowStore.getState();
      let msg = input;
      if (!msg) {
        try {
          msg = JSON.parse(s.testInput);
        } catch (e) {
          throw new Error('Invalid JSON in Input Message');
        }
      }
      
      const res = await apiFetch(`${API_BASE}/workflows/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflow: { 
            name: s.name, 
            vhost: s.vhost, 
            dead_letter_sink_id: s.deadLetterSinkID,
            dlq_threshold: s.dlqThreshold,
            prioritize_dlq: s.prioritizeDLQ,
            max_retries: s.maxRetries,
            retry_interval: s.retryInterval,
            reconnect_interval: s.reconnectInterval,
            schema_type: s.schemaType,
            schema: s.schema,
            nodes: s.nodes.map(n => ({
              id: n.id,
              type: n.type,
              ref_id: n.data.ref_id,
              config: n.data,
              x: n.position.x,
              y: n.position.y
            })),
            edges: s.edges.map(e => ({
              id: e.id,
              source_id: e.source,
              target_id: e.target,
              config: e.data
            })),
          },
          message: msg,
          dry_run: dryRun
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setTestResults(data);
      setTestModalOpened(false);
      notifications.show({ title: 'Test Complete', message: 'The flow has been simulated. Active paths are highlighted.', color: 'blue' });
    },
    onError: (err) => {
      notifications.show({ title: 'Test Failed', message: err.message, color: 'red' });
    }
  });

  const saveMutation = useMutation({
    mutationFn: async () => {
      const s = useWorkflowStore.getState();
      const payload = {
        name: s.name,
        vhost: s.vhost,
        active: s.active,
        status: s.workflowStatus,
        worker_id: s.workerID,
        dead_letter_sink_id: s.deadLetterSinkID,
        dlq_threshold: s.dlqThreshold,
        prioritize_dlq: s.prioritizeDLQ,
        max_retries: s.maxRetries,
        retry_interval: s.retryInterval,
        reconnect_interval: s.reconnectInterval,
        idle_timeout: s.idleTimeout,
        tier: s.tier,
        dry_run: s.dryRun,
        workspace_id: s.workspaceID,
        cpu_request: s.cpuRequest,
        memory_request: s.memoryRequest,
        throughput_request: s.throughputRequest || 0,
        cron: s.cron,
        retention_days: s.retentionDays,
        trace_sample_rate: s.traceSampleRate,
        trace_retention: s.traceRetention,
        audit_retention: s.auditRetention,
        schema_type: s.schemaType,
        schema: s.schema,
        tags: s.tags,
        nodes: s.nodes.map(n => ({
          id: n.id,
          type: n.type,
          ref_id: n.data.ref_id,
          config: n.data,
          x: n.position.x,
          y: n.position.y
        })),
        edges: s.edges.map(e => ({
          id: e.id,
          source_id: e.source,
          target_id: e.target,
          config: e.data
        })),
      };
      if (isNew) {
        return apiFetch(`${API_BASE}/workflows`, {
          method: 'POST',
          body: JSON.stringify(payload)
        });
      } else {
        return apiFetch(`${API_BASE}/workflows/${id}`, {
          method: 'PUT',
          body: JSON.stringify(payload)
        });
      }
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Workflow saved successfully', color: 'green' });
      if (!isNew && active) {
        setWorkflowStatus('Restarting');
      }
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      if (isNew) navigate({ to: '/workflows' });
    }
  });

  const toggleMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}/toggle`, { method: 'POST' });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setActive(data.active);
      setWorkflowStatus(data.status);
      notifications.show({ 
        title: data.active ? 'Workflow Started' : 'Workflow Stopped', 
        message: `Workflow ${name} is now ${data.status.toLowerCase()}`, 
        color: data.active ? 'green' : 'gray' 
      });
      queryClient.invalidateQueries({ queryKey: ['workflow', id] });
    },
    onError: (err: any) => {
      if (err.message?.includes('already running')) {
        setActive(true);
        setWorkflowStatus('Running');
        queryClient.invalidateQueries({ queryKey: ['workflow', id] });
      }
    }
  });

  const rebuildMutation = useMutation({
    mutationFn: async (fromOffset: number) => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}/rebuild`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from_offset: fromOffset }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || 'Failed to start rebuild');
      }
      return res.json();
    },
    onSuccess: () => {
      notifications.show({
        title: 'Rebuild Started',
        message: 'Projection rebuilding has started in the background.',
        color: 'blue',
      });
    },
    onError: (err: any) => {
      notifications.show({
        title: 'Rebuild Failed',
        message: err.message,
        color: 'red',
      });
    },
  });

  const handleTest = useCallback((overrideInput?: any, dryRun: boolean = false) => {
    let input = overrideInput;
    const s = useWorkflowStore.getState();
    const { nodes, selectedNode } = s;
    
    if (!input && selectedNode?.type === 'source') {
      const sourceData = sourcesData?.find((s: any) => s.id === selectedNode?.data.ref_id);
      if (sourceData?.sample) {
        try { input = JSON.parse(sourceData.sample); } catch(e) {}
      }
    }
    
    if (!input) {
      const firstSource = nodes.find(n => n.type === 'source');
      if (firstSource) {
        const sourceData = sourcesData?.find((s: any) => s.id === firstSource.data.ref_id);
        if (sourceData?.sample) {
          try { input = JSON.parse(sourceData.sample); } catch(e) {}
        }
      }
    }
    
    if (!input && testInput && testInput !== '{\n  "payload": "test"\n}') {
      try { input = JSON.parse(testInput); } catch(e) {}
    }

    if (input) {
      testMutation.mutate({ input, dryRun });
    } else {
      setTestModalOpened(true);
    }
  }, [sourcesData, testInput, testMutation, setTestModalOpened]);

  const handleRefreshFields = useCallback(async () => {
    let input = null;
    const s = useWorkflowStore.getState();
    const { nodes, selectedNode } = s;

    const sourceNode = selectedNode?.type === 'source' ? selectedNode : nodes.find(n => n.type === 'source');
    
    if (sourceNode) {
      const sourceData = sourcesData?.find((s: any) => s.id === sourceNode.data.ref_id);
      if (sourceData) {
        try {
          notifications.show({ 
            id: 'refresh-fields', 
            title: 'Refreshing Fields', 
            message: `Fetching fresh sample from ${sourceData.name || sourceNode.id}...`, 
            loading: true,
            autoClose: false,
            withCloseButton: false
          });
          
          let table = sourceData.config.table || sourceData.config.collection || '';
          if (!table && sourceData.config.tables) {
            table = sourceData.config.tables.split(',')[0].trim();
          }

          const res = await apiFetch(`${API_BASE}/sources/sample`, {
            method: 'POST',
            body: JSON.stringify({
              source: { type: sourceData.type, config: sourceData.config },
              table: table
            })
          });
          
          if (res.ok) {
            const sampleMsg = await res.json();
            if (sampleMsg && typeof sampleMsg === 'object') {
              if (typeof sampleMsg.after === 'string') {
                try { sampleMsg.after = JSON.parse(sampleMsg.after); } catch (_) {}
              }
              if (typeof sampleMsg.before === 'string') {
                try { sampleMsg.before = JSON.parse(sampleMsg.before); } catch (_) {}
              }
            }

            input = sampleMsg;
            
            await apiFetch(`${API_BASE}/sources/${sourceData.id}`, {
              method: 'PUT',
              body: JSON.stringify({ ...sourceData, sample: JSON.stringify(sampleMsg) })
            });
            
            queryClient.invalidateQueries({ queryKey: ['sources'] });
            notifications.update({ 
              id: 'refresh-fields', 
              title: 'Refresh Complete', 
              message: 'Fresh sample fetched and saved.', 
              color: 'green', 
              loading: false,
              autoClose: 2000
            });
          }
        } catch (e: any) {
          notifications.update({ 
            id: 'refresh-fields', 
            title: 'Refresh Partial', 
            message: 'Could not fetch fresh sample from source. Re-simulating with existing data.', 
            color: 'orange', 
            loading: false,
            autoClose: 3000
          });
        }
      }
    }

    handleTest(input, true);
  }, [sourcesData, handleTest, queryClient]);

  const handleSave = useCallback(() => {
    if (!isNew && active) {
      setSaveConfirmOpened(true);
    } else {
      saveMutation.mutate();
    }
  }, [isNew, active, saveMutation, setSaveConfirmOpened]);

  const handleInlineSave = (updatedData: Partial<Source | Sink>) => {
    if (!selectedNode) return;
    updateNodeConfig(selectedNode.id, { 
       ...updatedData, 
       label: (updatedData as any).name || selectedNode.data.label,
       ref_id: (updatedData as any).id 
    });
    setSettingsOpened(false);
    setSelectedNode(null);
    queryClient.invalidateQueries({ queryKey: ['sources'] });
    queryClient.invalidateQueries({ queryKey: ['sinks'] });
    // Auto-save workflow when a node configuration is saved to ensure state is consistent
    saveMutation.mutate();
  };

  return {
    testMutation,
    saveMutation,
    toggleMutation,
    rebuildMutation,
    handleTest,
    handleRefreshFields,
    handleSave,
    handleInlineSave
  };
}
