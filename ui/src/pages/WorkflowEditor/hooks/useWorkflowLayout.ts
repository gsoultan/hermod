import { useCallback } from 'react';
import { useReactFlow } from 'reactflow';
import dagre from 'dagre';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useWorkflowLayout() {
  const { getNodes, getEdges } = useReactFlow();
  const { setNodes, setEdges } = useWorkflowStore();

  const onLayout = useCallback((direction = 'LR') => {
    const nodes = getNodes();
    const edges = getEdges();

    const dagreGraph = new dagre.graphlib.Graph();
    dagreGraph.setDefaultEdgeLabel(() => ({}));
    dagreGraph.setGraph({ rankdir: direction });

    nodes.forEach((node) => {
      dagreGraph.setNode(node.id, { width: 250, height: 100 });
    });

    edges.forEach((edge) => {
      dagreGraph.setEdge(edge.source, edge.target);
    });

    dagre.layout(dagreGraph);

    const layoutedNodes = nodes.map((node) => {
      const nodeWithPosition = dagreGraph.node(node.id);
      return {
        ...node,
        position: {
          x: nodeWithPosition.x - 125,
          y: nodeWithPosition.y - 50,
        },
      };
    });

    setNodes(layoutedNodes);
    setEdges(edges);
  }, [getNodes, getEdges, setNodes, setEdges]);

  return { onLayout };
}
