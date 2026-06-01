import { useMemo } from 'react';
import { MarkerType } from '@xyflow/react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';

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

  const sinkDataRaw = useWorkflowStore((state) => 
    JSON.stringify(state.nodes
      .filter((n) => n.type === 'sink')
      .map((n) => ({ id: n.id, ref_id: n.data.ref_id })))
  );

  const sinkData = useMemo(() => JSON.parse(sinkDataRaw) as {id: string, ref_id: string}[], [sinkDataRaw]);

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
            source: sink.id,
            target: dlqNodeId,
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
          source: dlqNodeId,
          target: sourceId,
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
