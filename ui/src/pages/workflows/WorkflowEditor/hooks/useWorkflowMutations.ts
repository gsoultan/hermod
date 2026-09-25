import { useCallback, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { useWorkflowStore } from '../store/useWorkflowStore';
import type { Source, Sink } from '@/types';
import { resolveSampleSource, sampleTableFor, simulationInputs } from '../sampleCapture';

const API_BASE = '/api';

// The Test Input modal's untouched placeholder. Running it is never what the
// operator meant, so it does not count as an input.
const DEFAULT_TEST_INPUT = '{\n  "payload": "test"\n}';

/**
 * What one simulation run is fed. `input` goes to every source node. `inputs`
 * seeds source nodes, by node id, with their own sample; a source it does not
 * name gets `input`, or nothing. `partial` previews a workflow that is still
 * being built, and `quiet` leaves reporting the outcome to the caller.
 */
type SimulationVars = {
  input?: any;
  inputs?: Record<string, any>;
  partial?: boolean;
  quiet?: boolean;
};

type RefreshOutcome = { color: string; title: string; message: string; autoClose: number };

/**
 * What the refresh notification says once the run has settled. It is the one
 * place the operator hears about the refresh, so it has to say where the new
 * sample stopped: a source that could not be sampled, or a node that failed on
 * the new data — everything after that node can only show what reached it.
 */
function refreshOutcome(o: {
  sourceName?: string;
  /** A fresh sample was fetched; false when the node had no source to ask. */
  sampled: boolean;
  captureError: string | null;
  branchSeeded: boolean;
  failures: string[];
}): RefreshOutcome {
  if (o.captureError && !o.branchSeeded) {
    return {
      color: 'orange',
      title: 'Could not refresh fields',
      message: `Could not fetch a sample from ${o.sourceName}: ${o.captureError}`,
      autoClose: 8000,
    };
  }
  const notes: string[] = [];
  if (o.captureError) {
    notes.push(`Could not fetch a new sample from ${o.sourceName} (${o.captureError}), so its nodes show the last one.`);
  }
  if (o.failures.length > 0) {
    notes.push(`Failed on the new data — ${o.failures.join('; ')}. Nodes after it show what reached it.`);
  }
  if (notes.length > 0) {
    return {
      color: 'orange',
      title: o.captureError ? 'Fields refreshed from the last sample' : 'Fields refreshed, but a node failed',
      message: notes.join(' '),
      autoClose: 8000,
    };
  }
  return {
    color: 'green',
    title: 'Fields refreshed',
    message: o.sampled
      ? 'Every node downstream now reads the new sample.'
      : 'Re-ran the workflow on the samples it already had.',
    autoClose: 2500,
  };
}

/** Each node that reported an error in a run, as "label: error". */
function previewFailures(steps: any[] | undefined, nodes: { id: string; data?: any }[]): string[] {
  const failed = new Map<string, string>();
  for (const step of steps ?? []) {
    if (!step?.error || failed.has(step.node_id)) continue;
    const label = nodes.find((n) => n.id === step.node_id)?.data?.label || step.node_id;
    failed.set(step.node_id, `${label}: ${step.error}`);
  }
  return [...failed.values()];
}

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

  const [refreshing, setRefreshing] = useState(false);

  const testMutation = useMutation<any, Error, SimulationVars | undefined>({
    // The Configure Test modal's Run Simulation calls mutate() with no
    // variables, and destructuring them here threw before any request was
    // sent — so that button never ran anything.
    mutationFn: async (vars) => {
      const { input, inputs, partial, quiet } = vars ?? {};
      const s = useWorkflowStore.getState();
      let msg = input;
      if (!msg && !inputs) {
        try {
          msg = JSON.parse(s.testInput);
        } catch {
          throw new Error('Invalid JSON in Input Message');
        }
      }
      
      const res = await apiFetch(`${API_BASE}/workflows/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        silent: quiet,
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
              source_handle: e.sourceHandle,
              target_handle: e.targetHandle,
              config: e.data
            })),
          },
          message: msg,
          messages: inputs,
          partial,
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data, vars) => {
      setTestResults(data);
      setTestModalOpened(false);
      if (vars?.quiet) return;
      notifications.show({ title: 'Test Complete', message: 'The flow has been simulated. Active paths are highlighted.', color: 'blue' });
    },
    onError: (err, vars) => {
      if (vars?.quiet) return;
      notifications.show({ title: 'Test Failed', message: err.message, color: 'red' });
    }
  });
  const { mutate: runSimulation, mutateAsync: runSimulationAsync } = testMutation;

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
          source_handle: e.sourceHandle,
          target_handle: e.targetHandle,
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
      const res = await apiFetch(`${API_BASE}/workflows/${id}/toggle`, { method: 'POST', silent: true });
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
      } else {
        notifications.show({
          id: `workflow-toggle-error-${id}`,
          title: 'Action Failed',
          message: err.message,
          color: 'red'
        });
      }
    }
  });

  const rebuildMutation = useMutation({
    mutationFn: async (fromOffset: number) => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}/rebuild`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from_offset: fromOffset }),
        silent: true,
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
        id: `workflow-rebuild-error-${id}`,
        title: 'Rebuild Failed',
        message: err.message,
        color: 'red',
      });
    },
  });

  const handleTest = useCallback((overrideInput?: any) => {
    if (overrideInput) {
      runSimulation({ input: overrideInput });
      return;
    }

    // Each source node with its own sample. This used to take the selected
    // source's sample, else the first source in the workflow, and give it to
    // every source — so a two-source workflow was simulated with one branch's
    // data on both.
    const { nodes, nodeSamples } = useWorkflowStore.getState();
    const inputs = simulationInputs(nodes, sourcesData, nodeSamples);
    if (Object.keys(inputs).length > 0) {
      runSimulation({ inputs });
      return;
    }

    if (testInput && testInput !== DEFAULT_TEST_INPUT) {
      try {
        runSimulation({ input: JSON.parse(testInput) });
        return;
      } catch {}
    }
    setTestModalOpened(true);
  }, [sourcesData, testInput, runSimulation, setTestModalOpened]);

  // Fetch a source's sample and store it, so AVAILABLE FIELDS has something to
  // read. Shared by the refresh icon and by the automatic capture that runs
  // when a node opens with no fields; the icon additionally re-runs the
  // preview, which the automatic path deliberately does not.
  //
  // `silent` suppresses apiFetch's own error toast: a failure the operator
  // asked for should say so loudly, one they never asked for should not throw a
  // red banner over the canvas.
  const captureSample = useCallback(async (source: any, opts: { silent?: boolean } = {}) => {
    const res = await apiFetch(`${API_BASE}/sources/sample`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        source: { type: source.type, config: source.config },
        table: sampleTableFor(source.config),
      }),
      silent: opts.silent,
    });

    const sampleMsg = await res.json();
    if (sampleMsg && typeof sampleMsg === 'object') {
      if (typeof sampleMsg.after === 'string') {
        try { sampleMsg.after = JSON.parse(sampleMsg.after); } catch {}
      }
      if (typeof sampleMsg.before === 'string') {
        try { sampleMsg.before = JSON.parse(sampleMsg.before); } catch {}
      }
    }

    // Sent to the sample endpoint rather than PUT back through the source, so
    // the only column that travels is the one being captured. Storing this
    // through the full update meant spreading `source` — a copy taken from a
    // cached list — over the row, which reverted any config edit made since
    // that copy was fetched and rewound the cursor if the engine had moved on.
    // The endpoint also does not refuse while a workflow is running: a preview
    // payload is editor state, not configuration.
    await apiFetch(`${API_BASE}/sources/${source.id}/sample`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sample: JSON.stringify(sampleMsg) }),
      silent: opts.silent,
    });

    await queryClient.invalidateQueries({ queryKey: ['sources'] });
    return sampleMsg;
  }, [queryClient]);

  // Refresh fetches a fresh sample for the source feeding the selected node,
  // then runs the whole workflow on it. Every node reads what the node before
  // it emitted in that run, and each node's Live Preview re-runs whenever the
  // payload it reads changes — so one click carries the new sample through the
  // field list and the preview of every node downstream, one after another.
  const handleRefreshFields = useCallback(async () => {
    const { nodes, edges, selectedNode } = useWorkflowStore.getState();

    // The source on the selected node's own branch. This used to be
    // `nodes.find(n => n.type === 'source')` — the first source anywhere in the
    // workflow — so refreshing a node on the second branch of a two-source
    // workflow pulled the first branch's columns.
    const sourceData = selectedNode
      ? resolveSampleSource(selectedNode.id, nodes, edges, sourcesData)
      : null;
    const sourceName = sourceData?.name || sourceData?.id;
    const id = 'refresh-fields';

    setRefreshing(true);
    notifications.show({
      id,
      title: 'Refreshing fields',
      message: sourceData
        ? `Fetching a fresh sample from ${sourceName}…`
        : 'Running the workflow on the samples it already has…',
      loading: true,
      autoClose: false,
      withCloseButton: false,
    });

    const report = (color: string, title: string, message: string, autoClose: number) =>
      notifications.update({ id, color, title, message, loading: false, autoClose, withCloseButton: true });

    try {
      // Captured quietly: a failure is reported in this one notification, with
      // its reason, rather than as a second toast.
      let fresh: { sourceId: string; sample: any } | undefined;
      let captureError: string | null = null;
      if (sourceData) {
        try {
          fresh = { sourceId: sourceData.id, sample: await captureSample(sourceData, { silent: true }) };
        } catch (e: any) {
          captureError = e?.message || 'the source did not answer';
        }
      }

      const { nodes: current, nodeSamples } = useWorkflowStore.getState();
      const inputs = simulationInputs(current, sourcesData, nodeSamples, fresh);

      // Every field list reads the last simulation before anything else, and a
      // new sample means that simulation ran on an input that no longer exists.
      // Dropped, each node falls back to the sample just stored if the run below
      // fails; kept, the old results pinned every node to the old fields.
      if (fresh) setTestResults(null);

      if (Object.keys(inputs).length === 0) {
        report(
          'orange',
          'No sample to preview',
          captureError
            ? `Could not fetch a sample from ${sourceName}: ${captureError}`
            : 'No source in this workflow has a sample yet. Open the source and run Test Connection.',
          6000,
        );
        return;
      }

      // Whether the refreshed source's own branch has anything to run on: a
      // stored sample or lastSample when the fetch failed, the new one when not.
      const branchSeeded = !sourceData || current.some(
        (n) => n.type === 'source' && (n.data as any)?.ref_id === sourceData.id && inputs[n.id] !== undefined
      );

      try {
        // Partial: a workflow is missing its sink exactly while its nodes are
        // being set up, and the Test button's rule — refuse anything that could
        // not run — would stop the new sample at the first node.
        const steps = await runSimulationAsync({ inputs, partial: true, quiet: true });
        const outcome = refreshOutcome({
          sourceName,
          sampled: fresh !== undefined,
          captureError,
          branchSeeded,
          failures: previewFailures(steps, current),
        });
        report(outcome.color, outcome.title, outcome.message, outcome.autoClose);
      } catch (e: any) {
        report(
          'orange',
          fresh ? 'Sample refreshed, but the workflow preview failed' : 'The workflow preview failed',
          e?.message || 'The preview did not run.',
          8000,
        );
      }
    } finally {
      setRefreshing(false);
    }
  }, [sourcesData, captureSample, runSimulationAsync, setTestResults]);

  const handleSave = useCallback(() => {
    if (!isNew && active) {
      setSaveConfirmOpened(true);
    } else {
      saveMutation.mutate();
    }
  }, [isNew, active, saveMutation, setSaveConfirmOpened]);

  const handleInlineSave = (updatedData: Partial<Source | Sink> | null | undefined) => {
    if (!selectedNode) return;
    // Cancel is delivered via onCancel, not as a null save. Historically it
    // arrived here as null and `updatedData.name` threw, so the settings modal
    // never closed and the Cancel button appeared dead. Treat a missing payload
    // as "nothing to persist" instead of crashing the click handler.
    if (updatedData == null) {
      setSettingsOpened(false);
      setSelectedNode(null);
      return;
    }
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
    captureSample,
    handleRefreshFields,
    isRefreshing: refreshing,
    handleSave,
    handleInlineSave
  };
}
