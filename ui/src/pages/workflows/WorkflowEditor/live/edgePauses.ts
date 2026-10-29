/** A live message held on an edge breakpoint, waiting to be let go. */
export interface EdgePause {
  /** What a command names to answer this pause. */
  pauseId: string;
  edgeId: string;
  /** The node the edge leads to, which the message has not reached yet. */
  nodeId: string;
  msgId: string;
  /** The message as it would cross the edge. */
  data: unknown;
}

/** One event from the debugger socket (registry.DebuggerEvent). */
export interface DebuggerEvent {
  workflow_id?: string;
  node_id?: string;
  msg_id?: string;
  edge_id?: string;
  pause_id?: string;
  state?: string;
  data?: unknown;
}

/**
 * The held messages after one debugger event. A pause on an edge is added; any
 * other state for it — resumed, stepped, stopped, timed out — means it has
 * gone. A pause before a node is the Debugger tab's, and leaves the map as it
 * was.
 */
export function applyDebuggerEvent(pauses: Record<string, EdgePause>, ev: DebuggerEvent): Record<string, EdgePause> {
  const pauseId = ev.pause_id;
  if (!ev.edge_id || !pauseId) return pauses;
  if (ev.state === 'paused') {
    if (pauses[pauseId]) return pauses;
    return {
      ...pauses,
      [pauseId]: { pauseId, edgeId: ev.edge_id, nodeId: ev.node_id ?? '', msgId: ev.msg_id ?? '', data: ev.data },
    };
  }
  if (!pauses[pauseId]) return pauses;
  const { [pauseId]: _ended, ...rest } = pauses;
  return rest;
}

const NONE: EdgePause[] = [];
// Every canvas edge asks from inside a store selector, so each edge gets the
// same list back until its own pauses change.
const listCache = new Map<string, { pauses: Record<string, EdgePause>; list: EdgePause[] }>();

/** The messages held on one edge, oldest first. */
export function pausesOnEdge(pauses: Record<string, EdgePause>, edgeId: string): EdgePause[] {
  const cached = listCache.get(edgeId);
  if (cached?.pauses === pauses) return cached.list;
  const found = Object.values(pauses).filter((p) => p.edgeId === edgeId);
  let list = found.length === 0 ? NONE : found;
  if (cached && cached.list.length === list.length && cached.list.every((p, i) => p === list[i])) list = cached.list;
  listCache.set(edgeId, { pauses, list });
  return list;
}
