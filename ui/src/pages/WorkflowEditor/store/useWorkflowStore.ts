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
  drawerTab: string;
  settingsOpened: boolean;
  testModalOpened: boolean;
  dlqInspectorOpened: boolean;
  dlqInspectorSink: any | null;
  testInput: string;
  testResults: any[] | null;
  selectedNode: Node | null;
  quickAddSource: { nodeId: string; handleId: string | null } | null;
  
  traceInspectorOpened: boolean;
  traceMessageID: string;
  sampleInspectorOpened: boolean;
  sampleNodeId: string | null;
  schemaRegistryOpened: boolean;
  historyOpened: boolean;
  liveStreamOpened: boolean;
  aiGeneratorOpened: boolean;
  complianceReportOpened: boolean;

  deadLetterSinkID: string;
  dlqThreshold: number;
  prioritizeDLQ: boolean;
  maxRetries: number;
  retryInterval: string;
  reconnectInterval: string;
  dryRun: boolean;
  idleTimeout: string;
  tier: string;
  workspaceID: string;
  schemaType: string;
  schema: string;
  tags: string[];
  cpuRequest: number;
  memoryRequest: number;
  throughputRequest: number;

  sourceStatus: string;
  sinkStatuses: Record<string, string>;
  nodeMetrics: Record<string, number>;
  nodeErrorMetrics: Record<string, number>;
  nodeSamples: Record<string, any>;
  sinkCBStatuses: Record<string, string>;
  sinkBufferFill: Record<string, number>;
  workflowDeadLetterCount: number;

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
  setDrawerTab: (tab: string) => void;
  setSettingsOpened: (opened: boolean) => void;
  setTestModalOpened: (opened: boolean) => void;
  setDlqInspectorOpened: (opened: boolean) => void;
  setDlqInspectorSink: (sink: any | null) => void;
  setTestInput: (input: string) => void;
  setTestResults: (results: any[] | null) => void;
  setSelectedNode: (node: Node | null) => void;
  setQuickAddSource: (source: { nodeId: string; handleId: string | null } | null) => void;
  setTraceInspectorOpened: (opened: boolean) => void;
  setTraceMessageID: (id: string) => void;
  setSampleInspectorOpened: (opened: boolean) => void;
  setSampleNodeId: (id: string | null) => void;
  setSchemaRegistryOpened: (opened: boolean) => void;
  setHistoryOpened: (opened: boolean) => void;
  setLiveStreamOpened: (opened: boolean) => void;
  setAIGeneratorOpened: (opened: boolean) => void;
  setComplianceReportOpened: (opened: boolean) => void;

  setDeadLetterSinkID: (id: string) => void;
  setDlqThreshold: (threshold: number) => void;
  setPrioritizeDLQ: (prioritize: boolean) => void;
  setMaxRetries: (retries: number) => void;
  setRetryInterval: (interval: string) => void;
  setReconnectInterval: (interval: string) => void;
  setDryRun: (dryRun: boolean) => void;
  setIdleTimeout: (timeout: string) => void;
  setTier: (tier: string) => void;
  setWorkspaceID: (id: string) => void;
  setSchemaType: (type: string) => void;
  setSchema: (schema: string) => void;
  setTags: (tags: string[]) => void;
  setCPURequest: (cpu: number) => void;
  setMemoryRequest: (mem: number) => void;
  setThroughputRequest: (throughput: number) => void;

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
  drawerTab: 'nodes',
  settingsOpened: false,
  testModalOpened: false,
  dlqInspectorOpened: false,
  dlqInspectorSink: null,
  testInput: '{\n  "payload": "test"\n}',
  testResults: null,
  selectedNode: null,
  quickAddSource: null,
  traceInspectorOpened: false,
  traceMessageID: '',
  sampleInspectorOpened: false,
  sampleNodeId: null,
  schemaRegistryOpened: false,
  historyOpened: false,
  liveStreamOpened: false,
  aiGeneratorOpened: false,
  complianceReportOpened: false,

  deadLetterSinkID: '',
  dlqThreshold: 0,
  prioritizeDLQ: false,
  maxRetries: 3,
  retryInterval: '100ms',
  reconnectInterval: '30s',
  dryRun: false,
  idleTimeout: '',
  tier: 'Hot',
  workspaceID: '',
  schemaType: '',
  schema: '',
  tags: [],
  cpuRequest: 0,
  memoryRequest: 0,
  throughputRequest: 0,

  sourceStatus: '',
  sinkStatuses: {},
  nodeMetrics: {},
  nodeErrorMetrics: {},
  nodeSamples: {},
  sinkCBStatuses: {},
  sinkBufferFill: {},
  workflowDeadLetterCount: 0,

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
  setDrawerTab: (drawerTab) => set({ drawerTab }),
  setSettingsOpened: (settingsOpened) => set({ settingsOpened }),
  setTestModalOpened: (testModalOpened) => set({ testModalOpened }),
  setDlqInspectorOpened: (dlqInspectorOpened) => set({ dlqInspectorOpened }),
  setDlqInspectorSink: (dlqInspectorSink) => set({ dlqInspectorSink }),
  setTestInput: (testInput) => set({ testInput }),
  setTestResults: (testResults) => set({ testResults }),
  setSelectedNode: (selectedNode) => set({ selectedNode }),
  setQuickAddSource: (quickAddSource) => set({ quickAddSource }),
  setTraceInspectorOpened: (traceInspectorOpened) => set({ traceInspectorOpened }),
  setTraceMessageID: (traceMessageID) => set({ traceMessageID }),
  setSampleInspectorOpened: (sampleInspectorOpened) => set({ sampleInspectorOpened }),
  setSampleNodeId: (sampleNodeId) => set({ sampleNodeId }),
  setSchemaRegistryOpened: (schemaRegistryOpened) => set({ schemaRegistryOpened }),
  setHistoryOpened: (historyOpened) => set({ historyOpened }),
  setLiveStreamOpened: (liveStreamOpened) => set({ liveStreamOpened }),
  setAIGeneratorOpened: (aiGeneratorOpened) => set({ aiGeneratorOpened }),
  setComplianceReportOpened: (complianceReportOpened) => set({ complianceReportOpened }),

  setDeadLetterSinkID: (deadLetterSinkID) => set({ deadLetterSinkID }),
  setDlqThreshold: (dlqThreshold) => set({ dlqThreshold }),
  setPrioritizeDLQ: (prioritizeDLQ) => set({ prioritizeDLQ }),
  setMaxRetries: (maxRetries) => set({ maxRetries }),
  setRetryInterval: (retryInterval) => set({ retryInterval }),
  setReconnectInterval: (reconnectInterval) => set({ reconnectInterval }),
  setDryRun: (dryRun) => set({ dryRun }),
  setIdleTimeout: (idleTimeout) => set({ idleTimeout }),
  setTier: (tier) => set({ tier }),
  setWorkspaceID: (workspaceID) => set({ workspaceID }),
  setSchemaType: (schemaType) => set({ schemaType }),
  setSchema: (schema) => set({ schema }),
  setTags: (tags) => set({ tags }),
  setCPURequest: (cpuRequest) => set({ cpuRequest }),
  setMemoryRequest: (memoryRequest) => set({ memoryRequest }),
  setThroughputRequest: (throughputRequest) => set({ throughputRequest }),

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
