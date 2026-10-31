import { useCallback, useEffect, useMemo, useRef } from 'react';
import { 
  ReactFlow, 
  Background, 
  MiniMap,
  type Node,
  type Edge,
  type Connection,
  addEdge,
  useNodesInitialized,
  useReactFlow
} from '@xyflow/react';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { ConnectionLine } from './ConnectionLine';
import { LiveEdge } from './LiveEdge';
import { SimulationOverlay } from '../simulation/SimulationOverlay';
import { LiveRunBanner } from '../live/LiveRunBanner';
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
  WaitNode,
  ForeachNode,
  ExplodeNode,
  LogNode,
  CollectNode,
  DeduplicateNode,
  NoteNode 
} from '../nodes/MiscNodes';
import { ConditionNode } from '../nodes/ConditionNode';
import { ApprovalNode } from '../nodes/ApprovalNode';
import { AIClassifyNode } from '../nodes/AIClassifyNode';
import { AIAgentNode } from '../nodes/AIAgentNode';

/**
 * The renderer for each node type. A type missing here is drawn as React
 * Flow's default node, with none of its outputs. Module-level, so React Flow
 * sees the same object on every render.
 */
export const canvasNodeTypes = {
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
  wait: WaitNode,
  foreach: ForeachNode,
  explode: ExplodeNode,
  log: LogNode,
  collect: CollectNode,
  deduplicate: DeduplicateNode,
  note: NoteNode,
  ai_classify: AIClassifyNode,
  ai_agent: AIAgentNode,
  ml_recommend_build: TransformationNode,
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

  const nodeTypes = canvasNodeTypes;

  const edgeTypes = useMemo(() => ({
    default: LiveEdge,
    live: LiveEdge,
  }), []);
  
  const { nodes, edges, onNodesChange, onEdgesChange, setEdges } = useWorkflowStore(useShallow(state => ({
    nodes: state.nodes,
    edges: state.edges,
    onNodesChange: state.onNodesChange,
    onEdgesChange: state.onEdgesChange,
    setEdges: state.setEdges,
  })));

  // Open on the whole flow once its steps have been measured, never closer
  // than 100%. The steps arrive after the canvas mounts, so React Flow's own
  // fitView-on-init would fit an empty canvas.
  const { fitView } = useReactFlow();
  const nodesInitialized = useNodesInitialized();
  const fitted = useRef(false);
  useEffect(() => {
    if (!nodesInitialized || fitted.current || nodes.length === 0) return;
    fitted.current = true;
    // A frame later, once the page around the canvas has settled its size.
    const t = setTimeout(() => fitView({ padding: 0.2, maxZoom: 1 }), 50);
    return () => clearTimeout(t);
  }, [nodesInitialized, nodes.length, fitView]);

  const onConnect = useCallback((params: Connection) => {
    const label = params.sourceHandle?.split(':::')[0] || params.sourceHandle || '';
    const edge: Edge = {
      ...params,
      id: `edge_${Date.now()}`,
      type: 'live',
      data: { label }
    };
    setEdges((eds) => addEdge(edge, eds));
  }, [setEdges]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
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
      fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
    >
      <Background color={isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'} gap={20} />
      <SimulationOverlay />
      <LiveRunBanner />
      {/* Zoom, Fit and Tidy up are the page's CanvasControls, bottom left. The
          minimap is lifted clear of the collapsed Live logs bar (40px), which
          floats along the bottom of the canvas card. */}
      <MiniMap
        pannable
        nodeColor={(n) => {
          if (n.type === 'source') return 'var(--mantine-color-blue-6)';
          if (n.type === 'sink') return 'var(--mantine-color-green-6)';
          return 'var(--mantine-color-violet-6)';
        }}
        maskColor={isDark ? 'rgba(0, 0, 0, 0.45)' : undefined}
        style={{
          backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
          bottom: 40,
          width: 168,
          height: 96,
          border: '1px solid var(--mantine-color-default-border)',
          borderRadius: 'var(--mantine-radius-md)',
          overflow: 'hidden',
        }}
      />
    </ReactFlow>
  );
}
