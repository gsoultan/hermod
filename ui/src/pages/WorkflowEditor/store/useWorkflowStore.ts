import { create } from 'zustand';
import { 
  type Node, 
  type Edge, 
  type OnNodesChange, 
  type OnEdgesChange, 
  applyNodeChanges, 
  applyEdgeChanges 
} from 'reactflow';

interface WorkflowState {
  nodes: Node[];
  edges: Edge[];
  name: string;
  vhost: string;
  workerID: string;
  active: boolean;
  workflowStatus: string;
  logs: any[];
  logsOpened: boolean;
  logsPaused: boolean;
  drawerOpened: boolean;
  settingsOpened: boolean;
  testModalOpened: boolean;
  testInput: string;
  testResults: any[] | null;
  selectedNode: Node | null;
  quickAddSource: { nodeId: string; handleId: string | null } | null;

  setNodes: (nodes: Node[] | ((nds: Node[]) => Node[])) => void;
  setEdges: (edges: Edge[] | ((eds: Edge[]) => Edge[])) => void;
  onNodesChange: OnNodesChange;
  onEdgesChange: OnEdgesChange;
  
  setName: (name: string) => void;
  setVHost: (vhost: string) => void;
  setWorkerID: (workerID: string) => void;
  setActive: (active: boolean) => void;
  setWorkflowStatus: (status: string) => void;
  setLogs: (logs: any[] | ((prev: any[]) => any[])) => void;
  setLogsOpened: (opened: boolean) => void;
  setLogsPaused: (paused: boolean) => void;
  setDrawerOpened: (opened: boolean) => void;
  setSettingsOpened: (opened: boolean) => void;
  setTestModalOpened: (opened: boolean) => void;
  setTestInput: (input: string) => void;
  setTestResults: (results: any[] | null) => void;
  setSelectedNode: (node: Node | null) => void;
  setQuickAddSource: (source: { nodeId: string; handleId: string | null } | null) => void;
  
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
}

export const useWorkflowStore = create<WorkflowState>((set) => ({
  nodes: [],
  edges: [],
  name: '',
  vhost: 'default',
  workerID: '',
  active: false,
  workflowStatus: 'Stopped',
  logs: [],
  logsOpened: false,
  logsPaused: false,
  drawerOpened: false,
  settingsOpened: false,
  testModalOpened: false,
  testInput: '{\n  "payload": "test"\n}',
  testResults: null,
  selectedNode: null,
  quickAddSource: null,

  setNodes: (nodes) => set((state) => ({ 
    nodes: typeof nodes === 'function' ? nodes(state.nodes) : nodes 
  })),
  setEdges: (edges) => set((state) => ({ 
    edges: typeof edges === 'function' ? edges(state.edges) : edges 
  })),
  onNodesChange: (changes) => set((state) => ({
    nodes: applyNodeChanges(changes, state.nodes),
  })),
  onEdgesChange: (changes) => set((state) => ({
    edges: applyEdgeChanges(changes, state.edges),
  })),

  setName: (name) => set({ name }),
  setVHost: (vhost) => set({ vhost }),
  setWorkerID: (workerID) => set({ workerID }),
  setActive: (active) => set({ active }),
  setWorkflowStatus: (workflowStatus) => set({ workflowStatus }),
  setLogs: (logs) => set((state) => ({ 
    logs: typeof logs === 'function' ? logs(state.logs) : logs 
  })),
  setLogsOpened: (logsOpened) => set({ logsOpened }),
  setLogsPaused: (logsPaused) => set({ logsPaused }),
  setDrawerOpened: (drawerOpened) => set({ drawerOpened }),
  setSettingsOpened: (settingsOpened) => set({ settingsOpened }),
  setTestModalOpened: (testModalOpened) => set({ testModalOpened }),
  setTestInput: (testInput) => set({ testInput }),
  setTestResults: (testResults) => set({ testResults }),
  setSelectedNode: (selectedNode) => set({ selectedNode }),
  setQuickAddSource: (quickAddSource) => set({ quickAddSource }),

  updateNodeConfig: (nodeId, config, replace = false) => set((state) => {
    const nodes = state.nodes.map((node) => 
      node.id === nodeId ? { ...node, data: replace ? config : { ...node.data, ...config } } : node
    );
    const selectedNode = state.selectedNode?.id === nodeId 
      ? { ...state.selectedNode, data: replace ? config : { ...state.selectedNode.data, ...config } }
      : state.selectedNode;
    return { nodes, selectedNode };
  }),
}));
