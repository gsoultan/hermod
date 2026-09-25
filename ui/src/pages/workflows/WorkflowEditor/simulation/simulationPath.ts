import type { Node } from '@xyflow/react';

/**
 * What a simulation did to one node, as the canvas shows it.
 *
 * - `passed`: the message reached the node, and the node emitted output.
 * - `filtered`: the message reached the node, and the node emitted nothing.
 * - `error`: the node failed on the message.
 * - `skipped`: no message reached the node.
 */
export type SimulationNodeStatus = 'passed' | 'filtered' | 'error' | 'skipped';

/** One entry of POST /api/workflows/test's answer (registry.WorkflowStepResult). */
export interface SimulationStep {
  node_id: string;
  node_type?: string;
  payload?: Record<string, unknown>;
  error?: string;
  filtered?: boolean;
  /** Nothing reached the node. `filtered` is set too, for older readers. */
  skipped?: boolean;
  branch?: string;
  /** The edges this node's output travelled along. */
  taken_edges?: string[];
}

export interface SimulationNodeResult {
  status: SimulationNodeStatus;
  error?: string;
  /** The branch a routing node took, or the reason a node emitted nothing. */
  branch?: string;
}

type Steps = readonly SimulationStep[] | null | undefined;

interface SimulationPath {
  nodes: Map<string, SimulationNodeResult>;
  takenEdges: Set<string>;
}

// Every node the run did not report on reads as not reached: one added after
// the run, or a source that had no sample to send. One shared object, so every
// such node compares equal to the last answer and does not re-render.
const NOT_REACHED: SimulationNodeResult = { status: 'skipped' };

/**
 * A node can be reported more than once. One that fails is reported with its
 * error and then again as having emitted nothing, so its outcome is the most
 * telling of its steps rather than the last one.
 */
function resultOf(steps: SimulationStep[]): SimulationNodeResult {
  const failed = steps.find((s) => s.error);
  if (failed) return { status: 'error', error: failed.error };
  const emitted = steps.find((s) => !s.filtered);
  if (emitted) return { status: 'passed', branch: emitted.branch || undefined };
  if (steps.some((s) => s.skipped)) return NOT_REACHED;
  return { status: 'filtered', branch: steps[0].branch || undefined };
}

function buildPath(steps: readonly SimulationStep[]): SimulationPath {
  const byNode = new Map<string, SimulationStep[]>();
  const takenEdges = new Set<string>();
  for (const step of steps) {
    const reported = byNode.get(step.node_id);
    if (reported) reported.push(step);
    else byNode.set(step.node_id, [step]);
    for (const edgeId of step.taken_edges ?? []) takenEdges.add(edgeId);
  }
  const nodes = new Map<string, SimulationNodeResult>();
  for (const [nodeId, reported] of byNode) nodes.set(nodeId, resultOf(reported));
  return { nodes, takenEdges };
}

// Every node and edge on the canvas reads its own entry from inside a store
// selector, and the store is written on every telemetry frame. Deriving the
// path once per run, and handing back the same objects until the next one,
// keeps that to a lookup and keeps a frame from re-rendering the canvas.
let cached: { steps: readonly SimulationStep[]; path: SimulationPath } | null = null;

function pathOf(steps: Steps): SimulationPath | null {
  if (!Array.isArray(steps)) return null;
  if (cached?.steps !== steps) cached = { steps, path: buildPath(steps) };
  return cached.path;
}

/** What the last simulation did to a node; undefined when none is shown. */
export function nodeSimulationResult(steps: Steps, nodeId: string): SimulationNodeResult | undefined {
  const path = pathOf(steps);
  if (!path) return undefined;
  return path.nodes.get(nodeId) ?? NOT_REACHED;
}

/** Whether the last simulation's message travelled along an edge. */
export function edgeSimulationState(steps: Steps, edgeId: string): 'taken' | 'untaken' | undefined {
  const path = pathOf(steps);
  if (!path) return undefined;
  return path.takenEdges.has(edgeId) ? 'taken' : 'untaken';
}

// Notes sit on the canvas but are not part of the workflow, so they have no
// outcome to count.
const isWorkflowNode = (node: Node) => node.type !== 'note';

/** How many of the canvas's nodes ended each way. */
export function simulationCounts(steps: Steps, nodes: readonly Node[]): Record<SimulationNodeStatus, number> | null {
  const path = pathOf(steps);
  if (!path) return null;
  const counts: Record<SimulationNodeStatus, number> = { passed: 0, filtered: 0, error: 0, skipped: 0 };
  for (const node of nodes) {
    if (isWorkflowNode(node)) counts[(path.nodes.get(node.id) ?? NOT_REACHED).status] += 1;
  }
  return counts;
}

/** Each node that failed, as "label: error". */
export function simulationFailures(steps: Steps, nodes: readonly Node[]): string[] {
  const path = pathOf(steps);
  if (!path) return [];
  const failures: string[] = [];
  for (const node of nodes) {
    const result = path.nodes.get(node.id);
    if (result?.status === 'error') failures.push(`${(node.data?.label as string) || node.id}: ${result.error}`);
  }
  return failures;
}

const STATUS_WORDS: Record<SimulationNodeStatus, string> = {
  passed: 'passed',
  filtered: 'filtered',
  error: 'failed',
  skipped: 'not reached',
};

/** The words for a status, as the summary counts it. */
export const simulationStatusWord = (status: SimulationNodeStatus) => STATUS_WORDS[status];

/** A node's outcome as one sentence: its badge's accessible name and tooltip. */
export function describeSimulationResult(result: SimulationNodeResult): string {
  let text = `Simulation: ${STATUS_WORDS[result.status]}`;
  if (result.branch) {
    text += result.status === 'passed' ? `, took branch "${result.branch}"` : ` (branch "${result.branch}")`;
  }
  if (result.error) text += `. ${result.error}`;
  return text;
}
