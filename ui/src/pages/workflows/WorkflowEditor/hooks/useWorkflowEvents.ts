import { useCallback } from 'react';
import { type Node, type Edge, useReactFlow, addEdge } from '@xyflow/react';
import { notifications } from '@mantine/notifications';
import { useWorkflowStore } from '../store/useWorkflowStore';

export function useWorkflowEvents(
  reactFlowWrapper: React.RefObject<HTMLDivElement | null>,
  setConfigModalOpen: (open: boolean) => void
) {
  const { screenToFlowPosition } = useReactFlow();
  const { 
    setNodes, setEdges, setSelectedNode, setDrawerOpened,
    setQuickAddSource, quickAddSource, active, testResults, setSettingsOpened
  } = useWorkflowStore();

  const addNodeAtPosition = useCallback((type: string, refId: string, label: string, subType: string, position: { x: number, y: number }, extraData?: any) => {
    const newNode: Node = {
      id: `node_${Date.now()}`,
      type,
      position,
      data: { 
        label, 
        ref_id: refId, 
        ...(type === 'transformation' ? { transType: subType } : { type: subType }),
        ...(extraData || {})
      },
    };

    setNodes((nds) => nds.concat(newNode));

    if (quickAddSource) {
      const newEdge: Edge = {
        id: `edge_${Date.now()}`,
        source: quickAddSource.nodeId,
        sourceHandle: quickAddSource.handleId,
        target: newNode.id,
        type: 'live',
        animated: active,
        style: { strokeWidth: active ? 3 : 2 },
      };
      setEdges((eds) => addEdge(newEdge, eds));
      setQuickAddSource(null);
    }
    return newNode;
  }, [quickAddSource, active, setEdges, setNodes, setQuickAddSource]);

  const onNodeClick = useCallback((_event: React.MouseEvent, node: Node) => {
    setSelectedNode(node);
    if (node.type === 'source' || node.type === 'sink' || node.type === 'transformation' || node.type === 'validator') {
      setConfigModalOpen(true);
      return;
    }
    // Logic/flow nodes (switch, router, condition, merge, stateful, etc.)
    // are configured in the node settings modal, not the palette drawer.
    setDrawerOpened(false);
    setSettingsOpened(true);
  }, [setSelectedNode, setSettingsOpened, setDrawerOpened, setConfigModalOpen]);

  const onEdgeClick = useCallback((_event: React.MouseEvent, edge: Edge) => {
    if (!testResults) return;
    const sourceResult = testResults.find(r => r.node_id === edge.source);
    if (sourceResult) {
      notifications.show({
        title: `Edge Data: ${edge.id}`,
        message: 'Data passing through this path (see console for full payload)',
        color: 'blue',
      });
      console.log(`Data for edge ${edge.id}:`, (sourceResult as any).payload);
    }
  }, [testResults]);

  const onDragStart = (event: React.DragEvent, nodeType: string, refId: string, label: string, subType: string, extraData?: any) => {
    event.dataTransfer.setData('application/reactflow', JSON.stringify({ nodeType, refId, label, subType, ...(extraData || {}) }));
    event.dataTransfer.effectAllowed = 'move';
  };

  const onDragOver = useCallback((event: React.DragEvent) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback((event: React.DragEvent) => {
    event.preventDefault();
    const reactFlowBounds = reactFlowWrapper.current?.getBoundingClientRect();
    const dataStr = event.dataTransfer.getData('application/reactflow');
    if (!dataStr || !reactFlowBounds) return;

    const { nodeType, refId, label, subType, ...extraData } = JSON.parse(dataStr);
    const position = screenToFlowPosition({
      x: event.clientX - reactFlowBounds.left,
      y: event.clientY - reactFlowBounds.top,
    });

    const node = addNodeAtPosition(nodeType, refId, label, subType, position, extraData);
    setSelectedNode(node);
    if (nodeType === 'source' || nodeType === 'sink' || nodeType === 'transformation' || nodeType === 'validator') {
      setConfigModalOpen(true);
    } else {
      setDrawerOpened(false);
      setSettingsOpened(true);
    }
  }, [screenToFlowPosition, addNodeAtPosition, setSelectedNode, setDrawerOpened, setSettingsOpened, setConfigModalOpen, reactFlowWrapper]);

  const deleteNode = (nodeId: string) => {
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setSelectedNode(null);
    setDrawerOpened(false);
    setSettingsOpened(false);
  };

  return {
    onNodeClick,
    onEdgeClick,
    onDragStart,
    onDragOver,
    onDrop,
    addNodeAtPosition,
    deleteNode
  };
}
