import type { Edge, Node } from '@xyflow/react';

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

/** The graph a run was asked for on, and how far a replay of it has got. */
interface RunGraph {
  nodes: readonly Node[];
  edges: readonly Edge[];
}
interface Replay {
  hop: number;
}

/**
 * How an edge is drawn after a simulation.
 *
 * - `taken`: the message crossed it and the node it reached passed it on.
 * - `into-error` / `into-filtered`: it crossed it, and the node it reached
 *   failed on it or emitted nothing.
 * - `untaken`: the edge was in the run and the message did not cross it.
 * - `new`: the edge was drawn after the run, which says nothing about it.
 * - `crossing` / `pending`: during a replay, the hop being crossed now, and
 *   the ones still to come.
 */
export type EdgeSimulationState = 'taken' | 'into-error' | 'into-filtered' | 'untaken' | 'new' | 'crossing' | 'pending';

export interface EdgeSimulationView {
  state: EdgeSimulationState;
  /** The hop the message crossed this edge on, from 1; null if it did not. */
  hop: number | null;
}

interface Route {
  /** Each taken edge's hop. */
  hops: Map<string, number>;
  /** The node each edge of the run leads to. */
  targets: Map<string, string>;
  hopCount: number;
}

// Keyed on both the steps and the graph they ran on, which are stored together.
let routeCache: { steps: readonly SimulationStep[]; graph: RunGraph | null; route: Route } | null = null;

/**
 * The hop each taken edge was crossed on. The engine reports its steps in the
 * order it ran them, a node only once every edge into it has been walked, so a
 * node's depth is known by the time its own step comes up: one more than the
 * deepest edge that reached it. Branches that ran side by side share a hop.
 */
function routeOf(steps: readonly SimulationStep[], graph: RunGraph | null): Route {
  if (routeCache && routeCache.steps === steps && routeCache.graph === graph) return routeCache.route;
  const targets = new Map<string, string>();
  for (const edge of graph?.edges ?? []) targets.set(edge.id, edge.target);
  const depth = new Map<string, number>();
  const hops = new Map<string, number>();
  let hopCount = 0;
  for (const step of steps) {
    const from = depth.get(step.node_id) ?? 0;
    for (const edgeId of step.taken_edges ?? []) {
      const hop = from + 1;
      if (!hops.has(edgeId)) hops.set(edgeId, hop);
      hopCount = Math.max(hopCount, hop);
      const target = targets.get(edgeId);
      if (target !== undefined) depth.set(target, Math.max(depth.get(target) ?? 0, hop));
    }
  }
  const route = { hops, targets, hopCount };
  routeCache = { steps, graph, route };
  return route;
}

// One object per answer that carries no hop, so selectors comparing the last
// answer do not re-render the edge.
const UNTAKEN: EdgeSimulationView = { state: 'untaken', hop: null };
const NEW: EdgeSimulationView = { state: 'new', hop: null };

/** How an edge is drawn after the last simulation; undefined when none is shown. */
export function edgeSimulationView(
  steps: Steps,
  graph: RunGraph | null,
  replay: Replay | null,
  edgeId: string,
): EdgeSimulationView | undefined {
  const path = pathOf(steps);
  if (!path || !Array.isArray(steps)) return undefined;
  const route = routeOf(steps, graph);
  const hop = route.hops.get(edgeId);
  if (hop === undefined) {
    // Without the graph the run was on, every edge is taken to have been in it.
    return graph && !route.targets.has(edgeId) ? NEW : UNTAKEN;
  }
  if (replay) {
    if (hop > replay.hop) return { state: 'pending', hop };
    if (hop === replay.hop) return { state: 'crossing', hop };
  }
  const target = route.targets.get(edgeId);
  const reached = target !== undefined ? path.nodes.get(target) : undefined;
  if (reached?.status === 'error') return { state: 'into-error', hop };
  if (reached?.status === 'filtered') return { state: 'into-filtered', hop };
  return { state: 'taken', hop };
}

/** How many hops the last simulation's message made. */
export function simulationHopCount(steps: Steps, graph: RunGraph | null): number {
  if (!Array.isArray(steps)) return 0;
  return routeOf(steps, graph).hopCount;
}

let outdatedCache: { graph: RunGraph; nodes: readonly Node[]; edges: readonly Edge[]; outdated: boolean } | null = null;

const edgeLabel = (e: Edge) => (e.data as { label?: unknown } | undefined)?.label;
const sameWiring = (a: Edge, b: Edge) =>
  a.source === b.source &&
  a.target === b.target &&
  (a.sourceHandle ?? null) === (b.sourceHandle ?? null) &&
  (a.targetHandle ?? null) === (b.targetHandle ?? null) &&
  edgeLabel(a) === edgeLabel(b);

/**
 * Whether the canvas has changed since the shown simulation ran in a way that
 * could change its answer: a node added, removed or reconfigured, or an edge
 * added, removed or rewired. Moving or selecting things does not count.
 */
export function simulationOutdated(
  steps: Steps,
  graph: RunGraph | null,
  nodes: readonly Node[],
  edges: readonly Edge[],
): boolean {
  if (!Array.isArray(steps) || !graph) return false;
  if (outdatedCache && outdatedCache.graph === graph && outdatedCache.nodes === nodes && outdatedCache.edges === edges) {
    return outdatedCache.outdated;
  }
  let outdated = nodes.length !== graph.nodes.length || edges.length !== graph.edges.length;
  if (!outdated) {
    const ranNodes = new Map(graph.nodes.map((n) => [n.id, n]));
    // A node's data is replaced whenever it is configured, and kept as the
    // same object when it is only moved or selected.
    outdated = nodes.some((n) => {
      const ran = ranNodes.get(n.id);
      return !ran || ran.type !== n.type || ran.data !== n.data;
    });
  }
  if (!outdated) {
    const ranEdges = new Map(graph.edges.map((e) => [e.id, e]));
    outdated = edges.some((e) => {
      const ran = ranEdges.get(e.id);
      return !ran || !sameWiring(ran, e);
    });
  }
  outdatedCache = { graph, nodes, edges, outdated };
  return outdated;
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
