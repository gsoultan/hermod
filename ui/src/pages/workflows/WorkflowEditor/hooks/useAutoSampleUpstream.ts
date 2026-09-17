import { useEffect, useRef } from 'react';
import type { Node, Edge } from '@xyflow/react';
import { notifications } from '@mantine/notifications';
import { resolveSampleSource, shouldAutoSample } from '../sampleCapture';

interface AutoSampleParams {
  /** A node settings panel is open. */
  enabled: boolean;
  selectedNode: Node | null;
  nodes: Node[];
  edges: Edge[];
  sources: any[] | undefined;
  /** How many fields the panel is currently offering. */
  fieldCount: number;
  capture: (source: any, opts?: { silent?: boolean }) => Promise<any>;
}

/**
 * useAutoSampleUpstream fills AVAILABLE FIELDS the first time a node is opened
 * with nothing in it.
 *
 * The field list is built from the upstream source's stored sample, and that
 * sample was only ever written by Test Connection or by the refresh icon beside
 * the list. Neither is on the path to opening a transformation, so a source
 * created through the API, restored from a bundle, or saved from the wizard
 * without pressing Test Connection left every downstream node showing an empty
 * list on a workflow that was fully configured and sampled fine on the first
 * try.
 *
 * Deliberately narrow:
 *   - only when the list is actually empty, so a node that already has fields
 *     never pays for a request;
 *   - only for non-source nodes, since a source node has Test Connection in
 *     front of the operator already;
 *   - only for the read-only source types (shouldAutoSample), because sampling
 *     a queue consumes a message and nobody asked for that;
 *   - once per source per editor session, so a source that cannot be sampled
 *     fails once instead of on every node the operator opens.
 */
export function useAutoSampleUpstream({
  enabled,
  selectedNode,
  nodes,
  edges,
  sources,
  fieldCount,
  capture,
}: AutoSampleParams): void {
  // Keyed by source id, not node id: the answer is the same for every node on
  // the branch, and retrying it per node is how one unreachable database turns
  // into a request for each node opened.
  const attempted = useRef(new Set<string>());

  useEffect(() => {
    if (!enabled || !selectedNode || fieldCount > 0) return;
    if (selectedNode.type === 'source') return;

    const source = resolveSampleSource(selectedNode.id, nodes, edges, sources);
    if (!shouldAutoSample(source)) return;
    if (attempted.current.has(source.id)) return;
    attempted.current.add(source.id);

    capture(source, { silent: true }).catch(() => {
      notifications.show({
        // A fixed id: opening three nodes fed by the same unreachable source
        // should say so once, not stack three identical banners.
        id: `auto-sample-${source.id}`,
        title: 'Could not load fields',
        message:
          `Hermod could not preview ${source.name || 'the upstream source'}. ` +
          'Open the source and run Test Connection, or use the refresh icon beside AVAILABLE FIELDS to see why.',
        color: 'orange',
        autoClose: 6000,
      });
    });
  }, [enabled, selectedNode, fieldCount, nodes, edges, sources, capture]);
}
