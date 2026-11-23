/** A sink node in the workflow graph, as the trace view needs it. */
export interface SinkNode {
  /** The node's id: the traversal records a step under it on reaching the node. */
  id: string;
  /** The sink connection it writes to: the engine records the write under it. */
  refId: string;
  name: string;
}

export interface OutcomeStep {
  node_id: string;
  error?: string;
}

export type TraceOutcome =
  | { kind: 'delivered'; deliveredTo: string[]; failedAt?: undefined }
  | { kind: 'failed'; deliveredTo: string[]; failedAt: OutcomeStep }
  | { kind: 'no_sink'; deliveredTo: string[]; failedAt?: undefined };

/**
 * What became of a traced message, read from its steps.
 *
 * Every step used to render as the same blue check, so a journey said nothing
 * about whether the message got anywhere. Two steps say it did: the traversal
 * records one under a sink *node's* id when it routes there, and the engine
 * records one under the sink *connection's* id once the write succeeds (or,
 * carrying the error, once it has failed for good). Reaching the node is not a
 * delivery; the write is.
 *
 * Several sink nodes may share one connection — a log sink at the end of each
 * branch — so the names come from the nodes that were reached, provided their
 * connection's write is on the trace.
 */
export function traceOutcome(steps: OutcomeStep[], sinks: SinkNode[]): TraceOutcome {
  const failedAt = steps.find((s) => !!s.error);
  const seen = new Set(steps.map((s) => s.node_id));
  const written = new Set(steps.filter((s) => !s.error).map((s) => s.node_id));

  const deliveredTo: string[] = [];
  for (const sink of sinks) {
    if (seen.has(sink.id) && written.has(sink.refId)) deliveredTo.push(sink.name);
  }
  // A workflow without a node graph routes to its sinks without node steps.
  if (deliveredTo.length === 0) {
    for (const sink of sinks) {
      if (written.has(sink.refId) && !deliveredTo.includes(sink.name)) deliveredTo.push(sink.name);
    }
  }

  if (failedAt) return { kind: 'failed', deliveredTo, failedAt };
  if (deliveredTo.length > 0) return { kind: 'delivered', deliveredTo };
  return { kind: 'no_sink', deliveredTo };
}
