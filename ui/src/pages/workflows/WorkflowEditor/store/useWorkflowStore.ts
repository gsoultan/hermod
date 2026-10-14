import { create } from 'zustand';
import { 
  type Node, 
  type Edge, 
  type OnNodesChange, 
  type OnEdgesChange, 
  applyNodeChanges, 
  applyEdgeChanges 
} from '@xyflow/react';
import type { LogEntry } from '../../../../types';

export interface WorkflowState {
  nodes: Node[];
  edges: Edge[];
  name: string;
  vhost: string;
  workerID: string;
  active: boolean;
  workflowStatus: string;
  logs: LogEntry[];
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
  /**
   * persistedSnapshot of the version last loaded from or saved to the server.
   * The canvas has unsaved edits exactly when it no longer matches this.
   */
  persistedBaseline: string | null;
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
  cron: string;
  retentionDays: number | null;
  traceSampleRate: number;
  traceRetention: string;
  auditRetention: string;
  cpuRequest: number;
  memoryRequest: number;
  throughputRequest: number;

  sourceStatus: string;
  sinkStatuses: Record<string, string>;
  nodeMetrics: Record<string, number>;
  nodeErrorMetrics: Record<string, number>;
  nodeSamples: Record<string, any>;
  // What each node emitted the last time its own Run Preview ran, by node id.
  // The node after it reads this as its input (useNodeContext), so a lookup's
  // previewed object reaches the next node without a whole-workflow simulation.
  nodePreviews: Record<string, any>;
  sinkCBStatuses: Record<string, string>;
  sinkBufferFill: Record<string, number>;
  workflowDeadLetterCount: number;

  // Live edge telemetry
  edgeSamples: Record<string, any[]>;
  edgeThroughput: Record<string, number>;
  pulseEnabled: boolean;

  setNodes: (nodes: Node[] | ((nds: Node[]) => Node[])) => void;
  setEdges: (edges: Edge[] | ((eds: Edge[]) => Edge[])) => void;
  onNodesChange: OnNodesChange;
  onEdgesChange: OnEdgesChange;
  
  setName: (name: string) => void;
  setVHost: (vhost: string) => void;
  setWorkerID: (workerID: string) => void;
  setActive: (active: boolean) => void;
  setWorkflowStatus: (status: string) => void;
  setLogs: (logs: LogEntry[] | ((prev: LogEntry[]) => LogEntry[])) => void;
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
  setNodePreview: (nodeId: string, payload: any | null) => void;
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
  setPulseEnabled: (val: boolean) => void;

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
  setCron: (cron: string) => void;
  setRetentionDays: (days: number | null) => void;
  setTraceSampleRate: (rate: number) => void;
  setTraceRetention: (retention: string) => void;
  setAuditRetention: (retention: string) => void;
  setCPURequest: (cpu: number) => void;
  setMemoryRequest: (mem: number) => void;
  setThroughputRequest: (throughput: number) => void;

  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
}

const isPlainObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v);

/** Same keys, and each value identical or, when both are objects, with identical values. */
function sameFields(a: Record<string, unknown>, b: Record<string, unknown>, depth: number): boolean {
  if (a === b) return true;
  const keys = Object.keys(a);
  if (keys.length !== Object.keys(b).length) return false;
  for (const k of keys) {
    if (!(k in b)) return false;
    const x = a[k];
    const y = b[k];
    if (x === y) continue;
    if (depth > 0 && isPlainObject(x) && isPlainObject(y) && sameFields(x, y, depth - 1)) continue;
    return false;
  }
  return true;
}

/** Whether a change to a node or edge list left every item as it was. */
function sameItems<T extends object>(next: T[], prev: T[]): boolean {
  if (next === prev) return true;
  if (next.length !== prev.length) return false;
  for (let i = 0; i < next.length; i++) {
    if (!sameFields(next[i] as Record<string, unknown>, prev[i] as Record<string, unknown>, 1)) return false;
  }
  return true;
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
  persistedBaseline: null,
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
  cron: '',
  retentionDays: null,
  traceSampleRate: 1.0,
  traceRetention: '7d',
  auditRetention: '30d',
  cpuRequest: 0,
  memoryRequest: 0,
  throughputRequest: 0,

  sourceStatus: '',
  sinkStatuses: {},
  nodeMetrics: {},
  nodeErrorMetrics: {},
  nodeSamples: {},
  nodePreviews: {},
  sinkCBStatuses: {},
  sinkBufferFill: {},
  workflowDeadLetterCount: 0,

  edgeSamples: {},
  edgeThroughput: {},
  pulseEnabled: true,

  setNodes: (nodes) => set((state) => {
    const nextNodes = typeof nodes === 'function' ? nodes(state.nodes) : nodes;
    if (nextNodes === state.nodes) return state;
    if (JSON.stringify(nextNodes) === JSON.stringify(state.nodes)) return state;
    return { nodes: nextNodes };
  }),
  setEdges: (edges) => set((state) => {
    const nextEdges = typeof edges === 'function' ? edges(state.edges) : edges;
    if (nextEdges === state.edges) return state;
    if (JSON.stringify(nextEdges) === JSON.stringify(state.edges)) return state;
    return { edges: nextEdges };
  }),
  // These run on every drag frame. React Flow keeps an untouched node or edge
  // as the same object and copies the one a change applies to, so comparing
  // each pair two levels deep finds a no-op without serialising the graph.
  onNodesChange: (changes) => set((state) => {
    const nextNodes = applyNodeChanges(changes, state.nodes);
    if (sameItems(nextNodes, state.nodes)) return state;
    return { nodes: nextNodes };
  }),
  onEdgesChange: (changes) => set((state) => {
    const nextEdges = applyEdgeChanges(changes, state.edges);
    if (sameItems(nextEdges, state.edges)) return state;
    return { edges: nextEdges };
  }),

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
  setNodePreview: (nodeId, payload) => set((state) => {
    if (payload == null) {
      if (!(nodeId in state.nodePreviews)) return state;
      const { [nodeId]: _dropped, ...rest } = state.nodePreviews;
      return { nodePreviews: rest };
    }
    return { nodePreviews: { ...state.nodePreviews, [nodeId]: payload } };
  }),
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
  setPulseEnabled: (val) => set({ pulseEnabled: val }),

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
  setCron: (cron) => set({ cron }),
  setRetentionDays: (retentionDays) => set({ retentionDays }),
  setTraceSampleRate: (traceSampleRate) => set({ traceSampleRate }),
  setTraceRetention: (traceRetention) => set({ traceRetention }),
  setAuditRetention: (auditRetention) => set({ auditRetention }),
  setCPURequest: (cpuRequest) => set({ cpuRequest }),
  setMemoryRequest: (memoryRequest) => set({ memoryRequest }),
  setThroughputRequest: (throughputRequest) => set({ throughputRequest }),

  updateNodeConfig: (nodeId, config, replace = false) => set((state) => {
    const current = state.nodes.find((node) => node.id === nodeId);
    if (!current) return state;
    const nextData = replace ? config : { ...current.data, ...config };

    // Deep equality check to prevent infinite loops (Junie stabilization).
    // Only this node's data can have changed, so only it is compared: this
    // runs on every keystroke, and serialising the whole graph meant every
    // sample row saved on every node, twice.
    if (JSON.stringify(nextData) === JSON.stringify(current.data)) {
      return state;
    }

    const nextNodes = state.nodes.map((node) =>
      node.id === nodeId ? { ...node, data: nextData } : node
    );

    const nextSelectedNode = state.selectedNode?.id === nodeId 
      ? { ...state.selectedNode, data: replace ? config : { ...state.selectedNode.data, ...config } }
      : state.selectedNode;

    return {
      nodes: nextNodes,
      selectedNode: nextSelectedNode,
      nodePreviews: withoutPreviewsFrom(state.nodePreviews, nodeId, state.edges),
    };
  }),
}));

/**
 * withoutPreviewsFrom drops the preview of `nodeId` and of every node after it.
 * Each of those ran on what this node used to emit, so after an edit they
 * describe a message the workflow no longer produces. An open form previews
 * again on its own and puts its node's entry back.
 */
function withoutPreviewsFrom(previews: Record<string, any>, nodeId: string, edges: Edge[]): Record<string, any> {
  if (Object.keys(previews).length === 0) return previews;
  const stale = new Set<string>();
  const pending = [nodeId];
  while (pending.length > 0) {
    const id = pending.pop() as string;
    if (stale.has(id)) continue;
    stale.add(id);
    edges.forEach((e) => { if (e.source === id) pending.push(e.target); });
  }
  if (![...stale].some((id) => id in previews)) return previews;
  return Object.fromEntries(Object.entries(previews).filter(([id]) => !stale.has(id)));
}
