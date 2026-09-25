import { useMemo } from 'react';
import { MarkerType } from '@xyflow/react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';

/**
 * The edge type the dead-letter and recovery edges are drawn with. They used to
 * have none, so they fell to the default type, LiveEdge, which draws every edge
 * its own way and ignored their dashed orange and blue style.
 */
export const RELIABILITY_EDGE = 'reliability';

// Coloured by the theme rather than React Flow's fixed white, so the label reads
// in dark mode as well as light.
const reliabilityLabel = (color: 'orange' | 'blue') => ({
  labelStyle: { fill: `var(--mantine-color-${color}-6)`, fontWeight: 700, fontSize: 10 },
  labelBgStyle: { fill: 'var(--mantine-color-body)' },
  labelBgPadding: [4, 2] as [number, number],
  labelBgBorderRadius: 4,
});

export function useStyledFlow() {
  const {
    active,
    deadLetterSinkID,
    prioritizeDLQ,
  } = useWorkflowStore(
    useShallow((state) => ({
      active: state.active,
      deadLetterSinkID: state.deadLetterSinkID,
      prioritizeDLQ: state.prioritizeDLQ,
    }))
  );

  // Select the stored array, derive outside the selector.
  //
  // This used to run `JSON.stringify(state.nodes.filter(...).map(...))` inside
  // the selector and JSON.parse it back in a memo. Zustand runs every selector
  // on every set(), and this store carries live telemetry — nodeMetrics,
  // edgeThroughput, edgeSamples, logs — so a filter, a map and a full
  // serialisation ran on every WebSocket frame just to answer "did the sinks
  // change?".
  //
  // `state.nodes` is a stable reference between updates that do not touch it, so
  // a telemetry frame now re-renders nothing here at all. useShallow would not
  // work in its place: it compares one level deep, and the mapped objects are
  // freshly allocated on every call, so no two results would ever match.
  const nodes = useWorkflowStore((state) => state.nodes);

  const sinkData = useMemo(
    () =>
      nodes
        .filter((n) => n.type === 'sink')
        .map((n) => ({ id: n.id, ref_id: n.data.ref_id as string })),
    [nodes]
  );

  const sourceId = useWorkflowStore((state) => 
    state.nodes.find((n) => n.type === 'source')?.id || null
  );

  const dlqNodeId = useMemo(() => {
    if (!deadLetterSinkID) return null;
    return sinkData.find((s) => s.ref_id === deadLetterSinkID)?.id;
  }, [sinkData, deadLetterSinkID]);

  const styledEdges = useMemo(() => {
    const reliabilityEdges: any[] = [];
    if (dlqNodeId) {
      // Add dashed edges from all other sinks to DLQ
      sinkData.forEach((sink) => {
        if (sink.id !== dlqNodeId) {
          reliabilityEdges.push({
            id: `reliability_${sink.id}_${dlqNodeId}`,
            // Drawn by an edge type that honours `style`; see RELIABILITY_EDGE.
            type: RELIABILITY_EDGE,
            source: sink.id,
            // The sink's hidden anchor: a sink has no source handle of its own,
            // and React Flow draws no edge it cannot attach to one.
            sourceHandle: 'dlq',
            target: dlqNodeId,
            targetHandle: 'dlq-in',
            label: 'DLQ',
            ...reliabilityLabel('orange'),
            animated: false,
            style: {
              strokeDasharray: '6 6',
              strokeLinecap: 'round',
              stroke: 'var(--mantine-color-orange-6)',
              opacity: 0.5,
            },
            markerEnd: {
              type: MarkerType.ArrowClosed,
              color: 'var(--mantine-color-orange-6)',
            },
            focusable: false,
            deletable: false,
            selectable: false,
            data: { label: 'DLQ' }
          });
        }
      });

      // If Prioritize DLQ is enabled, show recovery path
      if (prioritizeDLQ && sourceId) {
        reliabilityEdges.push({
          id: `recovery_${dlqNodeId}_${sourceId}`,
          type: RELIABILITY_EDGE,
          source: dlqNodeId,
          sourceHandle: 'recovery-out',
          target: sourceId,
          // A source has no target handle of its own either.
          targetHandle: 'recovery',
          label: 'RECOVERY',
          ...reliabilityLabel('blue'),
          animated: active,
          style: {
            strokeDasharray: '6 6',
            strokeLinecap: 'round',
            stroke: 'var(--mantine-color-blue-6)',
            strokeWidth: 2,
          },
          markerEnd: {
            type: MarkerType.ArrowClosed,
            color: 'var(--mantine-color-blue-6)',
          },
          focusable: false,
          deletable: false,
          selectable: false,
          data: { label: 'RECOVERY' }
        });
      }
    }

    return reliabilityEdges;
  }, [sinkData, sourceId, active, dlqNodeId, prioritizeDLQ]);

  return useMemo(() => ({ styledEdges }), [styledEdges]);
}
