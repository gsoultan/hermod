import { useCallback, useRef } from 'react';
import { useMutation } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { apiFetch } from '@/api';
import { useWorkflowStore, type SimulationGraph } from '../store/useWorkflowStore';
import { simulationInputs } from '../sampleCapture';

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

/**
 * Running a flow simulation: what the editor's Test button does, and the
 * read-only view's too, so both run the same request on the same inputs and
 * draw the same result. It simulates the flow in the workflow store.
 * `sourcesData` is the connection list, whose saved samples feed the sources
 * that have none of their own.
 */
export function useSimulation(sourcesData: any[] | undefined) {
  const testInput = useWorkflowStore((s) => s.testInput);
  const setTestResults = useWorkflowStore((s) => s.setTestResults);
  const setTestModalOpened = useWorkflowStore((s) => s.setTestModalOpened);

  // The number of the latest simulation run. Runs can overlap -- the Test
  // hotkey is not disabled while a refresh is running -- and only the latest
  // may store its results: an older one finishing last put back the fields the
  // newer run had replaced.
  const latestRun = useRef(0);

  // The canvas each run was asked for on, by run number: its result describes
  // that graph, not whatever the canvas has become by the time it answers.
  const runGraphs = useRef(new Map<number, SimulationGraph>());
  const testMutation = useMutation<any, Error, SimulationVars | undefined, number>({
    onMutate: () => {
      const run = ++latestRun.current;
      const { nodes, edges } = useWorkflowStore.getState();
      runGraphs.current.set(run, { nodes, edges });
      return run;
    },
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
            dead_letters: s.deadLetters,
            dead_letter_key_fields: s.deadLetterKeyFields,
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
    onSuccess: (data, vars, run) => {
      setTestModalOpened(false);
      const graph = run !== undefined ? runGraphs.current.get(run) : undefined;
      if (run !== undefined) runGraphs.current.delete(run);
      if (run !== latestRun.current) return;
      // A run the operator asked for replays the path; a background refresh
      // only updates it.
      setTestResults(data, { graph, replay: !vars?.quiet });
      if (vars?.quiet) return;
      notifications.show({ title: 'Test Complete', message: 'The flow has been simulated. Active paths are highlighted.', color: 'blue' });
    },
    onError: (err, vars, run) => {
      if (run !== undefined) runGraphs.current.delete(run);
      if (vars?.quiet) return;
      notifications.show({ title: 'Test Failed', message: err.message, color: 'red' });
    }
  });
  const { mutate: runSimulation } = testMutation;

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

  return { testMutation, handleTest };
}
