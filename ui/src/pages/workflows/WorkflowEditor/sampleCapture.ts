import type { Node, Edge } from '@xyflow/react';
import { isNonDestructiveSample } from '@/components/workflow/Source/sourceSampling';

/**
 * Resolving, and automatically capturing, the sample a node's field list is
 * built from.
 *
 * AVAILABLE FIELDS comes from the upstream source's stored sample
 * (useNodeContext), and that sample was only ever written by two explicit user
 * actions: Test Connection in the source wizard, and the refresh icon beside
 * the field list. A source created through the API, imported from a bundle, or
 * saved from the wizard without pressing Test Connection therefore has no
 * sample at all — so opening any node downstream of it showed an empty list on
 * a workflow that was otherwise completely configured.
 */

/**
 * findUpstreamSource returns the source record feeding `nodeId`: the node
 * itself when a source node is selected, otherwise the nearest source reachable
 * by walking incoming edges.
 *
 * The walk matters. handleRefreshFields used to take
 * `nodes.find(n => n.type === 'source')` — the first source anywhere in the
 * workflow, in node order — so on a workflow with two branches it sampled
 * whichever source happened to be listed first and filled the panel with the
 * other branch's columns.
 */
export function findUpstreamSource(
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
  sources: any[] | undefined
): any | null {
  const visited = new Set<string>();

  const walk = (id: string): any | null => {
    if (visited.has(id)) return null;
    visited.add(id);

    const node = nodes.find((n) => n.id === id);
    if (!node) return null;

    if (node.type === 'source') {
      return sources?.find((s: any) => s.id === (node.data as any)?.ref_id) ?? null;
    }

    for (const e of edges.filter((e: Edge) => e.target === id)) {
      const found = walk(e.source);
      if (found) return found;
    }
    return null;
  };

  return walk(nodeId);
}

/**
 * resolveSampleSource returns the source to preview for `nodeId`, falling back
 * to the workflow's only source when the node is not wired to anything yet.
 *
 * A transformation dragged onto the canvas has no branch to walk, and the old
 * first-source-in-the-workflow rule did at least answer for it. That answer is
 * only safe when there is nothing to confuse it with, so the fallback applies
 * to a single-source workflow and stops there: with two sources and no edge to
 * say which one feeds the node, any pick is a guess, and a guess here fills the
 * field list with columns the node will never see.
 */
export function resolveSampleSource(
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
  sources: any[] | undefined
): any | null {
  const upstream = findUpstreamSource(nodeId, nodes, edges, sources);
  if (upstream) return upstream;

  const sourceNodes = nodes.filter((n) => n.type === 'source');
  if (sourceNodes.length !== 1) return null;
  return sources?.find((s: any) => s.id === (sourceNodes[0].data as any)?.ref_id) ?? null;
}

/**
 * sampleTableFor picks the table a source should be previewed from.
 *
 * The empty string is meaningful: it tells the backend to preview what the
 * source is actually configured to read rather than building
 * `SELECT * FROM <table>`. batch_sql carries `queries` and has never carried
 * `table` or `tables`, so it takes that branch and its configured query is what
 * gets previewed.
 */
export function sampleTableFor(config: Record<string, any> | undefined): string {
  if (!config) return '';
  const named = config.table || config.collection || '';
  if (named) return String(named).trim();
  if (config.tables) return String(config.tables).split(',')[0].trim();
  return '';
}

/**
 * shouldAutoSample reports whether a source may be sampled without the operator
 * asking for it.
 *
 * Sampling a queue consumes a message, so firing one because a settings panel
 * opened would silently eat real data. Auto-capture is therefore limited to the
 * types isNonDestructiveSample already certifies as read-only, and skipped
 * entirely once a sample exists — a stored sample is what the field list reads,
 * and re-fetching it would add a request per node opened for no new fields.
 *
 * A source that has already advanced a cursor is fair game: captureSample omits
 * `state` from the write and UpdateSource keeps the row's own value, so storing
 * a sample cannot rewind a watermark however stale this copy of the source is.
 */
export function shouldAutoSample(source: any | null | undefined): boolean {
  if (!source?.type) return false;
  if (source.sample) return false;
  return isNonDestructiveSample(source.type);
}
