import type { Node } from '@xyflow/react';
import { formatRate, type FlowBand, type FlowSample } from './edgeFlow';

/**
 * What the canvas says about a running workflow: how each edge is drawn and
 * what is wrong at each node. Every answer comes from the status frames the
 * engine sends; nothing here is inferred from a lifetime total or assumed.
 */

/** A node with a run in flight (the engine's node_busy). */
export interface NodeBusy {
  in_flight: number;
  /** How long since the node last finished a run. */
  since_progress_ms: number;
}

/** The store fields a live view reads. */
export interface LiveInputs {
  nodes: readonly Node[];
  workflowStatus: string;
  /** The status feed is down, or nothing has arrived for too long. */
  statusFeedStale: boolean;
  edgeFlows: Record<string, FlowSample>;
  nodeErrorFlows: Record<string, FlowSample>;
  nodeBusy: Record<string, NodeBusy>;
  nodeHeld: Record<string, number>;
  /** Each node's moving-average run time, in nanoseconds. */
  nodeLatencies: Record<string, number>;
  sourceStatus: string;
  /** Keyed by the sink's ref_id, as are the two below. */
  sinkStatuses: Record<string, string>;
  sinkCBStatuses: Record<string, string>;
  sinkBufferFill: Record<string, number>;
}

export type EdgeLiveState =
  | 'idle'
  | 'stale'
  | 'blocked'
  | 'hung'
  | 'held'
  | 'failing'
  | 'backpressure'
  | 'flowing'
  | 'quiet';

export interface EdgeLiveView {
  state: EdgeLiveState;
  band: FlowBand;
  /** The edge's chip; null draws none. */
  label: string | null;
}

export type NodeProblemKind = 'stopped' | 'blocked' | 'hung' | 'failing' | 'stuck' | 'held' | 'backpressure' | 'reconnecting';

export interface NodeProblem {
  kind: NodeProblemKind;
  severity: 'error' | 'warning';
  /** What is wrong, in a few words: "no progress for 45s". */
  text: string;
}

export interface LiveProblem extends NodeProblem {
  nodeId: string;
  /** "Customer lookup: no progress for 45s". */
  text: string;
}

/** A node is hung after this long with work in it and nothing finished… */
export const HANG_AFTER_MS = 30_000;
/** …or after this many times its usual run time, if that is longer. */
const HANG_LATENCY_FACTOR = 5;
/** A sink is falling behind with its buffer this full, and stuck when full. */
export const BACKPRESSURE_FILL = 0.8;
const FULL_FILL = 0.98;
/** How long after the last error a node still reads as failing. */
export const FAILING_WINDOW_MS = 10_000;

/** Whether the engine is running, so its counts can move. */
export function isEngineLive(status: string | null | undefined): boolean {
  const s = (status ?? '').trim().toLowerCase();
  return (
    s === 'running' ||
    s === 'active' ||
    s === 'stalled' ||
    s === 'connecting' ||
    s === 'starting' ||
    s === 'restarting' ||
    s.startsWith('reconnecting')
  );
}

/** A duration as a person reads it: 45s, 2m, 1h 5m. */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
}

const errorRate = (rate: number) => formatRate(rate).replace('/s', ' err/s');

// Every node and edge asks about its own node from inside a store selector, so
// the lookup by ID is built once per nodes array rather than once per call.
const indexes = new WeakMap<readonly Node[], Map<string, Node>>();
function nodeById(nodes: readonly Node[], id: string): Node | undefined {
  let index = indexes.get(nodes);
  if (!index) {
    index = new Map(nodes.map((n) => [n.id, n]));
    indexes.set(nodes, index);
  }
  return index.get(id);
}

const refOf = (node: Node) => (node.data as { ref_id?: string } | undefined)?.ref_id ?? '';
const labelOf = (node: Node) => ((node.data as { label?: string } | undefined)?.label || node.id);

function hangThresholdMs(latencyNs: number | undefined): number {
  const usualMs = (latencyNs ?? 0) / 1e6;
  return Math.max(HANG_AFTER_MS, usualMs * HANG_LATENCY_FACTOR);
}

/** What is wrong at a node of a running workflow; null when nothing is. */
export function nodeLiveProblem(s: LiveInputs, nodeId: string): NodeProblem | null {
  if (!isEngineLive(s.workflowStatus) || s.statusFeedStale) return null;
  const node = nodeById(s.nodes, nodeId);
  if (!node) return null;
  const isSink = node.type === 'sink';
  const ref = refOf(node);

  if (isSink) {
    const status = (s.sinkStatuses[ref] ?? '').toLowerCase();
    if (status.startsWith('error')) {
      const why = status.includes('circuit') ? ': circuit breaker open' : '';
      return { kind: 'stopped', severity: 'error', text: `stopped${why}` };
    }
    if (s.sinkCBStatuses[ref] === 'open') {
      return { kind: 'blocked', severity: 'error', text: 'circuit breaker open' };
    }
  }

  const busy = s.nodeBusy[nodeId];
  if (busy && busy.in_flight > 0 && busy.since_progress_ms >= hangThresholdMs(s.nodeLatencies[nodeId])) {
    return { kind: 'hung', severity: 'error', text: `no progress for ${formatDuration(busy.since_progress_ms)}` };
  }

  const held = s.nodeHeld[nodeId] ?? 0;
  if (held > 0) {
    return { kind: 'held', severity: 'warning', text: `${held} held by the debugger` };
  }

  const errors = s.nodeErrorFlows[nodeId];
  if (errors && errors.band > 0) {
    return { kind: 'failing', severity: 'error', text: `failing, ${errorRate(errors.rate)}` };
  }

  if (isSink) {
    const fill = s.sinkBufferFill[ref] ?? 0;
    if (fill >= FULL_FILL) return { kind: 'stuck', severity: 'error', text: 'buffer full, not draining' };
    if (fill >= BACKPRESSURE_FILL) {
      return { kind: 'backpressure', severity: 'warning', text: `falling behind, buffer ${Math.round(fill * 100)}%` };
    }
    if (s.sinkCBStatuses[ref] === 'half-open') {
      return { kind: 'backpressure', severity: 'warning', text: 'recovering, circuit breaker half-open' };
    }
    if ((s.sinkStatuses[ref] ?? '').toLowerCase().startsWith('reconnecting')) {
      return { kind: 'reconnecting', severity: 'warning', text: 'reconnecting' };
    }
  }

  if (node.type === 'source' && s.sourceStatus.toLowerCase().startsWith('reconnecting')) {
    return { kind: 'reconnecting', severity: 'warning', text: 'source reconnecting' };
  }
  return null;
}

const IDLE: EdgeLiveView = { state: 'idle', band: 0, label: null };
const STALE: EdgeLiveView = { state: 'stale', band: 0, label: 'no data' };

/** How an edge of a running workflow is drawn. */
export function edgeLiveView(s: LiveInputs, edge: { id: string; target: string }): EdgeLiveView {
  if (!isEngineLive(s.workflowStatus)) return IDLE;
  if (s.statusFeedStale) return STALE;

  const flow = s.edgeFlows[edge.id];
  const band: FlowBand = flow?.band ?? 0;
  const rate = formatRate(band > 0 ? flow!.rate : 0);
  const problem = nodeLiveProblem(s, edge.target);

  switch (problem?.kind) {
    case 'stopped':
      return { state: 'blocked', band: 0, label: 'sink stopped' };
    case 'blocked':
      return { state: 'blocked', band: 0, label: 'circuit open' };
    case 'hung':
      return { state: 'hung', band: 0, label: `stuck ${formatDuration(s.nodeBusy[edge.target].since_progress_ms)}` };
    case 'held':
      return { state: 'held', band: 0, label: `${s.nodeHeld[edge.target]} held` };
    case 'failing':
      return { state: 'failing', band, label: `${rate} · ${errorRate(s.nodeErrorFlows[edge.target].rate)}` };
    case 'stuck':
      return { state: 'backpressure', band, label: `${rate} · buffer full` };
    case 'backpressure': {
      const target = nodeById(s.nodes, edge.target);
      const fill = target ? s.sinkBufferFill[refOf(target)] ?? 0 : 0;
      const why = fill >= BACKPRESSURE_FILL ? `buffer ${Math.round(fill * 100)}%` : 'recovering';
      return { state: 'backpressure', band, label: `${rate} · ${why}` };
    }
    case 'reconnecting':
      return { state: 'backpressure', band, label: `${rate} · reconnecting` };
  }
  return band > 0 ? { state: 'flowing', band, label: rate } : { state: 'quiet', band: 0, label: '0/s' };
}

/** Every node with something wrong, errors first, then in canvas order. */
export function liveProblems(s: LiveInputs): LiveProblem[] {
  const found: LiveProblem[] = [];
  for (const node of s.nodes) {
    if (node.type === 'note') continue;
    const p = nodeLiveProblem(s, node.id);
    if (p) found.push({ ...p, nodeId: node.id, text: `${labelOf(node)}: ${p.text}` });
  }
  const weight = (p: LiveProblem) => (p.severity === 'error' ? 0 : 1);
  return found.sort((a, b) => weight(a) - weight(b));
}
