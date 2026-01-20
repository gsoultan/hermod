import { useMemo } from 'react';
import { MarkerType } from 'reactflow';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useStyledFlow() {
  const nodes = useWorkflowStore((state) => state.nodes);
  const edges = useWorkflowStore((state) => state.edges);
  const active = useWorkflowStore((state) => state.active);
  const testResults = useWorkflowStore((state) => state.testResults);

  const styledNodes = useMemo(() => {
    return nodes.map((node) => {
      const result = testResults?.find((r) => r.node_id === node.id);
      return {
        ...node,
        data: {
          ...node.data,
          testResult: result,
        },
      };
    });
  }, [nodes, testResults]);

  const styledEdges = useMemo(() => {
    return edges.map((edge) => {
      const sourceResult = testResults?.find((r) => r.node_id === edge.source);
      const isPathActive = active || (sourceResult && sourceResult.status === 'COMPLETED');

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
  }, [edges, active, testResults]);

  return { styledNodes, styledEdges };
}
