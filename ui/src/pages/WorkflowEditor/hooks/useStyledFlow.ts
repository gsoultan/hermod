import { useMemo } from 'react';
import { MarkerType } from 'reactflow';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useStyledFlow() {
  const nodes = useWorkflowStore((state) => state.nodes);
  const edges = useWorkflowStore((state) => state.edges);
  const active = useWorkflowStore((state) => state.active);
  const testResults = useWorkflowStore((state) => state.testResults);
  const deadLetterSinkID = useWorkflowStore((state) => state.deadLetterSinkID);
  const prioritizeDLQ = useWorkflowStore((state) => state.prioritizeDLQ);
  const nodeMetrics = useWorkflowStore((state) => state.nodeMetrics);
  const nodeErrorMetrics = useWorkflowStore((state) => state.nodeErrorMetrics);
  const nodeSamples = useWorkflowStore((state) => state.nodeSamples);
  const sinkCBStatuses = useWorkflowStore((state) => state.sinkCBStatuses);
  const sinkBufferFill = useWorkflowStore((state) => state.sinkBufferFill);
  const workflowDeadLetterCount = useWorkflowStore((state) => state.workflowDeadLetterCount);

  const dlqNode = useMemo(() => {
    if (!deadLetterSinkID) return null;
    return nodes.find((n) => n.type === 'sink' && n.data.ref_id === deadLetterSinkID);
  }, [nodes, deadLetterSinkID]);

  const styledNodes = useMemo(() => {
    return nodes.map((node) => {
      const result = testResults?.find((r) => r.node_id === node.id);
      const isDLQ = dlqNode && node.id === dlqNode.id;
      const metric = nodeMetrics[node.id] || 0;
      const errorCount = nodeErrorMetrics[node.id] || 0;
      const errorRate = metric > 0 ? (errorCount / (metric + errorCount)) : 0;
      
      const sinkId = node.type === 'sink' ? node.data.ref_id : null;
      const cbStatus = sinkId ? sinkCBStatuses[sinkId] : null;
      const bufferFill = sinkId ? sinkBufferFill[sinkId] : 0;

      return {
        ...node,
        data: {
          ...node.data,
          testResult: result,
          isDLQ,
          metric,
          errorCount,
          errorRate,
          cbStatus,
          bufferFill,
          sample: nodeSamples[node.id],
          dlqCount: isDLQ ? workflowDeadLetterCount : 0,
        },
      };
    });
  }, [nodes, testResults, dlqNode, nodeMetrics, nodeErrorMetrics, nodeSamples, workflowDeadLetterCount, sinkCBStatuses, sinkBufferFill]);

  const styledEdges = useMemo(() => {
    const baseEdges = edges.map((edge) => {
      const sourceResult = testResults?.find((r) => r.node_id === edge.source);
      
      let isPathActive = active;
      if (!active && sourceResult) {
        // In simulation mode, check if message passed through this edge
        if (!sourceResult.filtered) {
          if (sourceResult.branch) {
            // If source is a router/switch, edge must match the branch label
            isPathActive = edge.sourceHandle === sourceResult.branch || edge.label === sourceResult.branch;
          } else {
            isPathActive = true;
          }
        }
      }

      return {
        ...edge,
        animated: isPathActive,
        style: { 
          strokeWidth: isPathActive ? 3 : 2,
          stroke: isPathActive ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-gray-5)',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 20,
          height: 20,
          color: isPathActive ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-gray-5)',
        },
      };
    });

    const reliabilityEdges: any[] = [];
    if (dlqNode) {
      // Add dashed edges from all other sinks to DLQ
      nodes.forEach((node) => {
        if (node.type === 'sink' && node.id !== dlqNode.id) {
          reliabilityEdges.push({
            id: `reliability_${node.id}_${dlqNode.id}`,
            source: node.id,
            target: dlqNode.id,
            animated: false,
            style: { 
              strokeDasharray: '5,5', 
              stroke: 'var(--mantine-color-orange-6)',
              opacity: 0.5 
            },
            markerEnd: {
              type: MarkerType.ArrowClosed,
              color: 'var(--mantine-color-orange-6)',
            },
            data: { label: 'DLQ' }
          });
        }
      });

      // If Prioritize DLQ is enabled, show recovery path
      if (prioritizeDLQ) {
        const firstSource = nodes.find(n => n.type === 'source');
        if (firstSource) {
          reliabilityEdges.push({
            id: `recovery_${dlqNode.id}_${firstSource.id}`,
            source: dlqNode.id,
            target: firstSource.id,
            animated: active,
            style: { 
              strokeDasharray: '5,5', 
              stroke: 'var(--mantine-color-blue-6)',
              strokeWidth: 2
            },
            markerEnd: {
              type: MarkerType.ArrowClosed,
              color: 'var(--mantine-color-blue-6)',
            },
            data: { label: 'RECOVERY' }
          });
        }
      }
    }

    return [...baseEdges, ...reliabilityEdges];
  }, [edges, nodes, active, testResults, dlqNode, prioritizeDLQ]);

  return { styledNodes, styledEdges };
}
