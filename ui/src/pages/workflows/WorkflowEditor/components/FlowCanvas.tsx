import { useCallback, useMemo } from 'react';
import { 
  ReactFlow, 
  Background, 
  Controls, 
  MiniMap,
  type Node,
  type Edge,
  type Connection,
  addEdge
} from '@xyflow/react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { useStyledFlow } from '../hooks/useStyledFlow';
import { ConnectionLine } from './ConnectionLine';
import { LiveEdge } from './LiveEdge';
import { useMantineColorScheme } from '@mantine/core';

// Node types are better imported or defined where they are used
import { SourceNode, SinkNode } from '../nodes/SourceSinkNodes';
import { 
  TransformationNode, 
  ValidatorNode, 
  SwitchNode, 
  RouterNode, 
  MergeNode, 
  StatefulNode, 
  NoteNode 
} from '../nodes/MiscNodes';
import { ConditionNode } from '../nodes/ConditionNode';
import { ApprovalNode } from '../nodes/ApprovalNode';

const nodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: TransformationNode,
  validator: ValidatorNode,
  condition: ConditionNode,
  approval: ApprovalNode,
  switch: SwitchNode,
  router: RouterNode,
  merge: MergeNode,
  stateful: StatefulNode,
  note: NoteNode,
};

const edgeTypes = {
  live: LiveEdge,
};

interface FlowCanvasProps {
  onNodeClick: (event: React.MouseEvent, node: Node) => void;
  onEdgeClick: (event: React.MouseEvent, edge: Edge) => void;
  onDrop: (event: React.DragEvent) => void;
  onDragOver: (event: React.DragEvent) => void;
}

export function FlowCanvas({ onNodeClick, onEdgeClick, onDrop, onDragOver }: FlowCanvasProps) {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  
  const { nodes, edges, onNodesChange, onEdgesChange, setEdges, active } = useWorkflowStore(useShallow(state => ({
    nodes: state.nodes,
    edges: state.edges,
    onNodesChange: state.onNodesChange,
    onEdgesChange: state.onEdgesChange,
    setEdges: state.setEdges,
    active: state.active
  })));

  const { styledEdges } = useStyledFlow();
  
  const allEdgesRaw = useMemo(() => {
    return JSON.stringify([...edges, ...styledEdges]);
  }, [edges, styledEdges]);

  const allEdges = useMemo(() => JSON.parse(allEdgesRaw), [allEdgesRaw]);

  const onConnect = useCallback((params: Connection) => {
    const label = params.sourceHandle?.split(':::')[0] || params.sourceHandle || '';
    const edge: Edge = {
      ...params,
      id: `edge_${Date.now()}`,
      animated: active || false,
      style: { strokeWidth: 2 },
      data: { label }
    };
    setEdges((eds) => addEdge(edge, eds));
  }, [active, setEdges]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={allEdges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      onNodeClick={onNodeClick}
      onEdgeClick={onEdgeClick}
      onDragOver={onDragOver}
      onDrop={onDrop}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      connectionLineComponent={ConnectionLine}
      defaultViewport={{ x: 0, y: 0, zoom: 1 }}
      snapToGrid
      snapGrid={[15, 15]}
      fitViewOptions={{ padding: 0.2 }}
    >
      <Background color={isDark ? 'var(--mantine-color-dark-4)' : '#aaa'} gap={20} />
      <Controls />
      <MiniMap 
        nodeColor={(n) => {
          if (n.type === 'source') return 'var(--mantine-color-blue-6)';
          if (n.type === 'sink') return 'var(--mantine-color-green-6)';
          return 'var(--mantine-color-violet-6)';
        }}
        style={{
          backgroundColor: isDark ? 'var(--mantine-color-dark-6)' : 'var(--mantine-color-white)',
        }}
      />
    </ReactFlow>
  );
}
