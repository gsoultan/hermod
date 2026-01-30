import { 
  useCallback, useEffect, useMemo, useRef, lazy 
} from 'react';
import { useShallow } from 'zustand/react/shallow';
import ReactFlow, { 
  addEdge, 
  Background, 
  Controls, 
  MiniMap,
  MarkerType,
  type Node,
  type Edge,
  ReactFlowProvider,
  useReactFlow,
  useViewport
} from 'reactflow';
import 'reactflow/dist/style.css';
import { 
  Group, Paper, Stack, ActionIcon, 
  Text, Box, Badge, ScrollArea, Flex,
  Code, Modal, Button, Divider, ThemeIcon, Title
} from '@mantine/core';
import { useHotkeys } from '@mantine/hooks';
import { 
  IconChevronDown, IconChevronUp, IconClearAll, IconPlayerPause,
  IconPlayerPlay, IconSettings, IconTrash
} from '@tabler/icons-react';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useVHost } from '../context/VHostContext';
import { notifications } from '@mantine/notifications';
import { useMantineColorScheme } from '@mantine/core';
import { SourceForm } from '../components/SourceForm';
import { SinkForm } from '../components/SinkForm';
import { TransformationForm } from '../components/TransformationForm';
import { getAllKeys, deepMergeSim, preparePayload } from '../utils/transformationUtils';
import { formatTime } from '../utils/dateUtils';

// Refactored Components & Hooks
import { useWorkflowStore } from './WorkflowEditor/store/useWorkflowStore';
import { useStyledFlow } from './WorkflowEditor/hooks/useStyledFlow';
import { EditorToolbar } from './WorkflowEditor/components/EditorToolbar';
import { SidebarDrawer } from './WorkflowEditor/components/SidebarDrawer';
import { Modals } from './WorkflowEditor/components/Modals';
import { WorkflowContext } from './WorkflowEditor/nodes/BaseNode';
// Lazy-load heavy editor node components to reduce initial bundle size
const SourceNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/SourceSinkNodes')).SourceNode }))
const SinkNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/SourceSinkNodes')).SinkNode }))
const TransformationNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).TransformationNode }))
const SwitchNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).SwitchNode }))
const RouterNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).RouterNode }))
const MergeNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).MergeNode }))
const StatefulNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).StatefulNode }))
const NoteNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).NoteNode }))
const ValidatorNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/MiscNodes')).ValidatorNode }))
const ConditionNode = lazy(async () => ({ default: (await import('./WorkflowEditor/nodes/ConditionNode')).ConditionNode }))

const API_BASE = '/api';

const nodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: TransformationNode,
  validator: ValidatorNode,
  condition: ConditionNode,
  switch: SwitchNode,
  router: RouterNode,
  merge: MergeNode,
  stateful: StatefulNode,
  note: NoteNode,
};

function EditorInner() {
  const { id } = useParams({ strict: false }) as any;
  const isNew = !id || id === 'new';
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const { project, zoomIn, zoomOut, fitView: rfFitView } = useReactFlow();
  const { zoom } = useViewport();
  const lastInitializedId = useRef<string | null>(null);
  
  const { 
    vhost, selectedNode, active, logsPaused, quickAddSource, nodes, edges, 
    testResults, testInput, name, deadLetterSinkID, dlqThreshold,
    prioritizeDLQ, maxRetries, retryInterval, reconnectInterval,
    schemaType, schema, workerID, dryRun, workflowStatus, logs, logsOpened, settingsOpened,
    onNodesChange, onEdgesChange,
    setName, setVHost, setWorkerID, setActive, setWorkflowStatus, 
    setDeadLetterSinkID, setDlqThreshold, setPrioritizeDLQ, setMaxRetries, setRetryInterval, 
    setReconnectInterval, setSchemaType, setSchema, setNodes, setEdges, setLogs, setQuickAddSource,
    setSelectedNode, setSettingsOpened, setDrawerOpened, updateNodeConfig,
    setTestResults, setTestModalOpened, setLogsOpened, setLogsPaused, setDryRun
  } = useWorkflowStore(useShallow(state => ({
    vhost: state.vhost,
    selectedNode: state.selectedNode,
    active: state.active,
    logsPaused: state.logsPaused,
    quickAddSource: state.quickAddSource,
    nodes: state.nodes,
    edges: state.edges,
    testResults: state.testResults,
    testInput: state.testInput,
    name: state.name,
    deadLetterSinkID: state.deadLetterSinkID,
    dlqThreshold: state.dlqThreshold,
    prioritizeDLQ: state.prioritizeDLQ,
    maxRetries: state.maxRetries,
    retryInterval: state.retryInterval,
    reconnectInterval: state.reconnectInterval,
    schemaType: state.schemaType,
    schema: state.schema,
    workerID: state.workerID,
    dryRun: state.dryRun,
    workflowStatus: state.workflowStatus,
    logs: state.logs,
    logsOpened: state.logsOpened,
    settingsOpened: state.settingsOpened,
    onNodesChange: state.onNodesChange,
    onEdgesChange: state.onEdgesChange,
    setName: state.setName,
    setVHost: state.setVHost,
    setWorkerID: state.setWorkerID,
    setActive: state.setActive,
    setWorkflowStatus: state.setWorkflowStatus,
    setDeadLetterSinkID: state.setDeadLetterSinkID,
    setDlqThreshold: state.setDlqThreshold,
    setPrioritizeDLQ: state.setPrioritizeDLQ,
    setMaxRetries: state.setMaxRetries,
    setRetryInterval: state.setRetryInterval,
    setReconnectInterval: state.setReconnectInterval,
    setSchemaType: state.setSchemaType,
    setSchema: state.setSchema,
    setNodes: state.setNodes,
    setEdges: state.setEdges,
    setLogs: state.setLogs,
    setQuickAddSource: state.setQuickAddSource,
    setSelectedNode: state.setSelectedNode,
    setSettingsOpened: state.setSettingsOpened,
    setDrawerOpened: state.setDrawerOpened,
    updateNodeConfig: state.updateNodeConfig,
    setTestResults: state.setTestResults,
    setTestModalOpened: state.setTestModalOpened,
    setLogsOpened: state.setLogsOpened,
    setLogsPaused: state.setLogsPaused,
    setDryRun: state.setDryRun,
  })));
  const { styledNodes, styledEdges } = useStyledFlow();

  const { selectedVHost } = useVHost();
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  const logScrollRef = useRef<HTMLDivElement>(null);

  const { data: sources } = useQuery({ 
    queryKey: ['sources', vhost], 
    queryFn: async () => {
      const vhostParam = (vhost && vhost !== 'all') ? `?vhost=${vhost}` : '';
      return (await apiFetch(`${API_BASE}/sources${vhostParam}`)).json();
    } 
  });
  
  const { data: sinks } = useQuery({ 
    queryKey: ['sinks', vhost], 
    queryFn: async () => {
      const vhostParam = (vhost && vhost !== 'all') ? `?vhost=${vhost}` : '';
      return (await apiFetch(`${API_BASE}/sinks${vhostParam}`)).json();
    } 
  });

  const selectedNodeData = useMemo(() => {
    if (!selectedNode) return null;
    if (selectedNode.data.ref_id === 'new') {
       const type = selectedNode.data.type;
       if (type && type !== 'new') {
           return { type, vhost: vhost };
       }
       return { vhost: vhost };
    }
    if (selectedNode.type === 'source') {
      return sources?.data?.find((s: any) => s.id === selectedNode?.data.ref_id);
    }
    if (selectedNode.type === 'sink') {
      return sinks?.data?.find((s: any) => s.id === selectedNode?.data.ref_id);
    }
    return null;
  }, [selectedNode, sources, sinks, vhost]);

  const { data: vhosts } = useQuery({ 
    queryKey: ['vhosts'], 
    queryFn: async () => (await apiFetch(`${API_BASE}/vhosts`)).json() 
  });

  const { data: workers } = useQuery({ 
    queryKey: ['workers'], 
    queryFn: async () => (await apiFetch(`${API_BASE}/workers`)).json() 
  });

  const { data: workflow, isLoading } = useQuery({
    queryKey: ['workflow', id || 'new'],
    queryFn: async () => {
      if (isNew) return { name: 'New Workflow', vhost: selectedVHost === 'all' ? 'default' : selectedVHost, worker_id: '', nodes: [], edges: [] };
      const res = await apiFetch(`${API_BASE}/workflows/${id}`);
      return res.json();
    }
  });


  useEffect(() => {
    if (workflow && lastInitializedId.current !== (id || 'new')) {
      setName(workflow.name || '');
      setVHost(workflow.vhost || 'default');
      setWorkerID(workflow.worker_id || '');
      setActive(workflow.active || false);
      setWorkflowStatus(workflow.status || 'Stopped');
      setDeadLetterSinkID(workflow.dead_letter_sink_id || '');
      setDlqThreshold(workflow.dlq_threshold || 0);
      setPrioritizeDLQ(workflow.prioritize_dlq || false);
      setMaxRetries(workflow.max_retries || 3);
      setRetryInterval(workflow.retry_interval || '100ms');
      setReconnectInterval(workflow.reconnect_interval || '30s');
      setSchemaType(workflow.schema_type || '');
      setSchema(workflow.schema || '');
      setDryRun(workflow.dry_run || false);
      
      const initialNodes = (workflow.nodes || []).map((node: any) => ({
        id: node.id,
        type: node.type,
        position: { x: node.x || 0, y: node.y || 0 },
        data: { ...(node.config || {}), ref_id: node.ref_id }
      }));
      setNodes(initialNodes);

      const initialEdges = (workflow.edges || []).map((edge: any) => ({
        id: edge.id,
        source: edge.source_id,
        target: edge.target_id,
        data: edge.config,
        animated: workflow.active,
        style: { strokeWidth: workflow.active ? 3 : 2 },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 20,
          height: 20,
          color: workflow.active ? 'var(--mantine-color-blue-6)' : 'var(--mantine-color-gray-5)',
        }
      }));
      setEdges(initialEdges);
      lastInitializedId.current = id || 'new';
    }
  }, [id, workflow, setName, setVHost, setWorkerID, setActive, setWorkflowStatus, setDeadLetterSinkID, setPrioritizeDLQ, setMaxRetries, setRetryInterval, setReconnectInterval, setSchemaType, setSchema, setDryRun, setNodes, setEdges]);

  // WebSocket for logs
  useEffect(() => {
    if (!id || id === 'new' || !active || logsPaused) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}`);

    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data);
        if (Array.isArray(log)) {
          setLogs(log.slice(0, 100));
        } else {
          setLogs(prev => [log, ...prev].slice(0, 100));
        }
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active, logsPaused, setLogs]);

  // WebSocket for status
  useEffect(() => {
    if (!id || id === 'new' || !active) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        const updates = Array.isArray(data) ? data : [data];
        
        updates.forEach(update => {
          if (update.workflow_id === id) {
            if (update.node_metrics) {
              useWorkflowStore.setState({ nodeMetrics: update.node_metrics });
            }
            if (update.node_samples) {
              useWorkflowStore.setState({ nodeSamples: update.node_samples });
            }
            if (update.dead_letter_count !== undefined) {
              useWorkflowStore.setState({ workflowDeadLetterCount: update.dead_letter_count });
            }
            if (update.engine_status) {
              setWorkflowStatus(update.engine_status);
            }
          }
        });
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active, setWorkflowStatus]);

  // Node operations
  const handlePlusClick = (nodeId: string, handleId: string | null) => {
    setQuickAddSource({ nodeId, handleId });
    setDrawerOpened(true);
  };

  const onConnect = useCallback((params: any) => {
    const label = params.sourceHandle?.split(':::')[0] || params.sourceHandle || '';
    const edge = {
      ...params,
      id: `edge_${Date.now()}`,
      animated: active,
      style: { strokeWidth: 2 },
      data: { label }
    };
    setEdges((eds) => addEdge(edge, eds));
  }, [active, setEdges]);

  const onNodeClick = useCallback((_event: any, node: Node) => {
    setSelectedNode(node);
    setSettingsOpened(true);
    setDrawerOpened(false);
  }, [setSelectedNode, setSettingsOpened, setDrawerOpened]);

  const onEdgeClick = useCallback((_event: any, edge: Edge) => {
    if (!testResults) return;
    const sourceResult = testResults.find(r => r.node_id === edge.source);
    if (sourceResult) {
      notifications.show({
        title: `Edge Data: ${edge.id}`,
        message: (
          <Stack gap="xs">
            <Text size="xs" fw={700}>Data passing through this path:</Text>
            <Code block style={{ fontSize: '10px', maxHeight: '300px', overflow: 'auto' }}>
              {JSON.stringify(sourceResult.payload, null, 2)}
            </Code>
          </Stack>
        ),
        color: 'blue',
        autoClose: false,
      });
    }
  }, [testResults]);

  const addNodeAtPosition = useCallback((type: string, refId: string, label: string, subType: string, position: { x: number, y: number }) => {
    const newNode: Node = {
      id: `node_${Date.now()}`,
      type,
      position,
      data: { 
        label, 
        ref_id: refId, 
        ...(type === 'transformation' ? { transType: subType } : { type: subType })
      },
    };

    setNodes((nds) => nds.concat(newNode));

    if (quickAddSource) {
      const newEdge: Edge = {
        id: `edge_${Date.now()}`,
        source: quickAddSource.nodeId,
        sourceHandle: quickAddSource.handleId,
        target: newNode.id,
        animated: active,
        style: { strokeWidth: active ? 3 : 2 },
      };
      setEdges((eds) => addEdge(newEdge, eds));
      setQuickAddSource(null);
    }
  }, [quickAddSource, active, setEdges, setNodes, setQuickAddSource]);

  const onDragStart = (event: any, nodeType: string, refId: string, label: string, subType: string) => {
    event.dataTransfer.setData('application/reactflow', JSON.stringify({ nodeType, refId, label, subType }));
    event.dataTransfer.effectAllowed = 'move';
  };

  const onDragOver = useCallback((event: any) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback((event: any) => {
    event.preventDefault();
    const reactFlowBounds = reactFlowWrapper.current?.getBoundingClientRect();
    const dataStr = event.dataTransfer.getData('application/reactflow');
    if (!dataStr || !reactFlowBounds) return;

    const { nodeType, refId, label, subType } = JSON.parse(dataStr);
    const position = project({
      x: event.clientX - reactFlowBounds.left,
      y: event.clientY - reactFlowBounds.top,
    });

    addNodeAtPosition(nodeType, refId, label, subType, position);
  }, [project, addNodeAtPosition]);

  const handleInlineSave = (updatedData: any) => {
    if (!selectedNode) return;
    updateNodeConfig(selectedNode.id, { 
       ...updatedData, 
       label: updatedData.name || selectedNode.data.label,
       ref_id: updatedData.id 
    });
    setSettingsOpened(false);
    setSelectedNode(null);
    queryClient.invalidateQueries({ queryKey: ['sources'] });
    queryClient.invalidateQueries({ queryKey: ['sinks'] });
  };

  const deleteNode = (nodeId: string) => {
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setSelectedNode(null);
    setDrawerOpened(false);
    setSettingsOpened(false);
  };

  // Mutations
  const testMutation = useMutation<any, Error, { input: any, dryRun?: boolean }>({
    mutationFn: async ({ input, dryRun }) => {
      let msg = input;
      if (!msg) {
        try {
          msg = JSON.parse(testInput);
        } catch (e) {
          throw new Error('Invalid JSON in Input Message');
        }
      }
      
      const res = await apiFetch(`${API_BASE}/workflows/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflow: { 
            name: name, 
            vhost: vhost, 
            dead_letter_sink_id: deadLetterSinkID,
            dlq_threshold: dlqThreshold,
            prioritize_dlq: prioritizeDLQ,
            max_retries: maxRetries,
            retry_interval: retryInterval,
            reconnect_interval: reconnectInterval,
            schema_type: schemaType,
            schema: schema,
            nodes: nodes.map(n => ({
              id: n.id,
              type: n.type,
              ref_id: n.data.ref_id,
              config: n.data,
              x: n.position.x,
              y: n.position.y
            })),
            edges: edges.map(e => ({
              id: e.id,
              source_id: e.source,
              target_id: e.target,
              config: e.data
            })),
          },
          message: msg,
          dry_run: dryRun
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setTestResults(data);
      setTestModalOpened(false);
      notifications.show({ title: 'Test Complete', message: 'The flow has been simulated. Active paths are highlighted.', color: 'blue' });
    },
    onError: (err) => {
      notifications.show({ title: 'Test Failed', message: err.message, color: 'red' });
    }
  });

  const handleTest = useCallback((overrideInput?: any, dryRun: boolean = false) => {
    let input = overrideInput;
    
    // 1. If no input provided, try to find a sample from the selected source node
    if (!input && selectedNode?.type === 'source') {
      const sourceData = sources?.data?.find((s: any) => s.id === selectedNode?.data.ref_id);
      if (sourceData?.sample) {
        try { input = JSON.parse(sourceData.sample); } catch(e) {}
      }
    }
    
    // 2. Try to find a sample from the first source node in the workflow
    if (!input) {
      const firstSource = nodes.find(n => n.type === 'source');
      if (firstSource) {
        const sourceData = sources?.data?.find((s: any) => s.id === firstSource.data.ref_id);
        if (sourceData?.sample) {
          try { input = JSON.parse(sourceData.sample); } catch(e) {}
        }
      }
    }
    
    // 3. Use existing test input if it looks customized
    if (!input && testInput && testInput !== '{\n  "payload": "test"\n}') {
      try { input = JSON.parse(testInput); } catch(e) {}
    }

    if (input) {
      testMutation.mutate({ input, dryRun });
    } else {
      // Fallback to modal if no valid payload is found
      setTestModalOpened(true);
    }
  }, [nodes, selectedNode, sources, testInput, testMutation, setTestModalOpened]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      const payload = {
        name: name,
        vhost: vhost,
        worker_id: workerID,
        dead_letter_sink_id: deadLetterSinkID,
        dlq_threshold: dlqThreshold,
        prioritize_dlq: prioritizeDLQ,
        max_retries: maxRetries,
        retry_interval: retryInterval,
        reconnect_interval: reconnectInterval,
        dry_run: dryRun,
        schema_type: schemaType,
        schema: schema,
        nodes: nodes.map(n => ({
          id: n.id,
          type: n.type,
          ref_id: n.data.ref_id,
          config: n.data,
          x: n.position.x,
          y: n.position.y
        })),
        edges: edges.map(e => ({
          id: e.id,
          source_id: e.source,
          target_id: e.target,
          config: e.data
        })),
      };
      if (isNew) {
        return apiFetch(`${API_BASE}/workflows`, {
          method: 'POST',
          body: JSON.stringify(payload)
        });
      } else {
        return apiFetch(`${API_BASE}/workflows/${id}`, {
          method: 'PUT',
          body: JSON.stringify(payload)
        });
      }
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Workflow saved successfully', color: 'green' });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      if (isNew) navigate({ to: '/workflows' });
    }
  });

  const toggleMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}/toggle`, { method: 'POST' });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      setActive(data.active);
      setWorkflowStatus(data.status);
      notifications.show({ 
        title: data.active ? 'Workflow Started' : 'Workflow Stopped', 
        message: `Workflow ${name} is now ${data.status.toLowerCase()}`, 
        color: data.active ? 'green' : 'gray' 
      });
      queryClient.invalidateQueries({ queryKey: ['workflow', id] });
    },
    onError: (err: any) => {
      if (err.message?.includes('already running')) {
        setActive(true);
        setWorkflowStatus('Running');
        queryClient.invalidateQueries({ queryKey: ['workflow', id] });
      }
    }
  });

  const rebuildMutation = useMutation({
    mutationFn: async (fromOffset: number) => {
      const res = await apiFetch(`${API_BASE}/workflows/${id}/rebuild`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from_offset: fromOffset }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || 'Failed to start rebuild');
      }
      return res.json();
    },
    onSuccess: () => {
      notifications.show({
        title: 'Rebuild Started',
        message: 'Projection rebuilding has started in the background.',
        color: 'blue',
      });
    },
    onError: (err: any) => {
      notifications.show({
        title: 'Rebuild Failed',
        message: err.message,
        color: 'red',
      });
    },
  });

  // Guard: do not trigger editor hotkeys while typing in inputs/textareas/selects or contentEditable
  const isTypingTarget = (evt: any) => {
    const t = (evt?.target as HTMLElement) || null;
    if (!t) return false;
    const tag = t.tagName?.toLowerCase();
    return tag === 'input' || tag === 'textarea' || tag === 'select' || (t as any).isContentEditable;
  };

  useHotkeys([
    ['ctrl+s', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); saveMutation.mutate(); }],
    ['ctrl+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, false); }],
    ['ctrl+shift+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, true); }],
    ['delete', (e) => {
       if (isTypingTarget(e)) return;
       const anySelected = nodes.some(n => n.selected) || edges.some(e => e.selected);
       if (anySelected) {
          setNodes(nds => nds.filter(n => !n.selected));
          setEdges(eds => eds.filter(e => !e.selected));
          setSelectedNode(null);
       }
    }]
  ]);

  const { incomingPayload, availableFields, sinkSchema } = useMemo(() => {
    let incomingPayload = null;
    let availableFields: string[] = [];
    let sinkSchema = null;

    if (!selectedNode) return { incomingPayload, availableFields, sinkSchema };

    // 1. Try to get payload from testResults (if simulation was run)
    if (testResults) {
      const incomingEdges = edges.filter((e: Edge) => e.target === selectedNode?.id);
      if (incomingEdges.length > 0) {
        // Collect all upstream payloads
        const mergedPayload: Record<string, any> = {};
        incomingEdges.forEach((edge: Edge) => {
          const result = testResults!.find(r => r.node_id === edge.source);
          if (result && result.payload) {
            deepMergeSim(mergedPayload, result.payload);
          }
        });
        if (Object.keys(mergedPayload).length > 0) {
          incomingPayload = preparePayload(mergedPayload);
          availableFields = getAllKeys(incomingPayload);
        }
      }
    }

    // 2. Try to get payload from selected node's upstream source if it has a sample
    if (!incomingPayload) {
      const upstreamEdges = edges.filter((e: Edge) => e.target === selectedNode?.id);
      if (upstreamEdges.length > 0) {
        const upstreamNode = nodes.find(n => n.id === upstreamEdges[0].source);
        if (upstreamNode && upstreamNode.type === 'source') {
          const sourceData = sources?.data?.find((s: any) => s.id === upstreamNode.data.ref_id);
          if (sourceData && sourceData.sample) {
            try {
              const sample = JSON.parse(sourceData.sample);
              incomingPayload = preparePayload(sample);
              availableFields = getAllKeys(incomingPayload);
            } catch (e) {}
          }
        }
      }
    }

    // 3. Try to get sink schema from downstream sink
    const downstreamEdges = edges.filter((e: Edge) => e.source === selectedNode?.id);
    if (downstreamEdges.length > 0) {
      const sinkNode = nodes.find(n => n.id === downstreamEdges[0].target);
      if (sinkNode && sinkNode.type === 'sink') {
        const sinkData = sinks?.data?.find((s: any) => s.id === sinkNode.data.ref_id);
        if (sinkData && sinkData.config?.table) {
           // We might want to fetch it but for now let's see if we have it in some cache or something
           // Actually, we can pass the sink info to TransformationForm and let it fetch
           sinkSchema = sinkData;
        }
      }
    }

    return { incomingPayload, availableFields, sinkSchema };
  }, [selectedNode, edges, nodes, testResults, sources, sinks]);

  if (isLoading && !isNew) return <Box p="xl" ta="center"><Text>Loading...</Text></Box>;

  return (
    <WorkflowContext.Provider value={{ onPlusClick: handlePlusClick }}>
      <Box style={{ height: 'calc(100vh - 120px)', display: 'flex', flexDirection: 'column' }}>
        {/* Page Header: Title moved above the editor toolbar */}
        <Paper
          withBorder
          radius="md"
          p="md"
          mb="sm"
          shadow="xs"
          style={{
            background: isDark
              ? 'linear-gradient(180deg, var(--mantine-color-dark-7), var(--mantine-color-dark-6))'
              : 'linear-gradient(180deg, var(--mantine-color-gray-0), var(--mantine-color-white))',
          }}
        >
          <Group justify="space-between" align="center">
            <Group gap="sm">
              <Title order={3} style={{ lineHeight: 1.2 }}>
                {isNew ? 'New Workflow' : (name || 'Untitled Workflow')}
              </Title>
              {!isNew && (
                <Badge
                  color={active ? 'green' : 'gray'}
                  variant="filled"
                >
                  {workflowStatus}
                </Badge>
              )}
            </Group>
            {!isNew && (
              <Text size="sm" c="dimmed">
                ID: {id}
              </Text>
            )}
          </Group>
        </Paper>

        <EditorToolbar 
          id={id}
          isNew={isNew}
          onSave={() => saveMutation.mutate()}
          onTest={(dry) => handleTest(null, dry)}
          onConfigureTest={() => setTestModalOpened(true)}
          onToggle={() => toggleMutation.mutate()}
          onRebuild={() => rebuildMutation.mutate(0)}
          onClearTest={() => setTestResults(null)}
          isSaving={saveMutation.isPending}
          isTesting={testMutation.isPending}
          isToggling={toggleMutation.isPending}
          zoom={zoom}
          zoomIn={zoomIn}
          zoomOut={zoomOut}
          fitView={rfFitView}
          vhosts={vhosts?.data || []}
          workers={workers?.data || []}
        />

        <Flex style={{ flex: 1, overflow: 'hidden' }}>
          <Box style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', gap: 'var(--mantine-spacing-md)' }}>
            <Paper withBorder radius="md" style={{ flex: 1, position: 'relative' }} ref={reactFlowWrapper}>
              <ReactFlow
                nodes={styledNodes}
                edges={styledEdges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onNodeClick={onNodeClick}
                onEdgeClick={onEdgeClick}
                onDragOver={onDragOver}
                onDrop={onDrop}
                nodeTypes={nodeTypes}
                defaultViewport={{ x: 0, y: 0, zoom: 1 }}
                snapToGrid
                snapGrid={[15, 15]}
              >
                <Background color={isDark ? '#333' : '#aaa'} gap={20} />
                <Controls />
                <MiniMap 
                  nodeColor={(n) => {
                     if (n.type === 'source') return 'var(--mantine-color-blue-6)';
                     if (n.type === 'sink') return 'var(--mantine-color-green-6)';
                     return 'var(--mantine-color-violet-6)';
                  }}
                  style={{
                     backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-white)',
                  }}
                />
              </ReactFlow>
            </Paper>

            {/* Live Log Panel */}
            <Paper withBorder radius="md" h={logsOpened ? 250 : 40} style={{ display: 'flex', flexDirection: 'column', transition: 'height 0.2s ease' }}>
               <Group justify="space-between" px="sm" h={40} style={{ borderBottom: logsOpened ? '1px solid var(--mantine-color-gray-2)' : 'none', cursor: 'pointer' }} onClick={() => setLogsOpened(!logsOpened)}>
                  <Group gap="xs">
                     {logsOpened ? <IconChevronDown size="1rem" /> : <IconChevronUp size="1rem" />}
                     <Text size="sm" fw={600}>Live Workflow Logs</Text>
                     {active && <Badge size="xs" color="green" variant="dot">Streaming</Badge>}
                  </Group>
                  <Group gap="xs">
                     <ActionIcon variant="subtle" size="sm" color="gray" onClick={(e) => { e.stopPropagation(); setLogs([]); }}>
                        <IconClearAll size="1rem" />
                     </ActionIcon>
                     <ActionIcon variant="subtle" size="sm" color={logsPaused ? 'orange' : 'gray'} onClick={(e) => { e.stopPropagation(); setLogsPaused(!logsPaused); }}>
                        {logsPaused ? <IconPlayerPlay size="1rem" /> : <IconPlayerPause size="1rem" />}
                     </ActionIcon>
                  </Group>
               </Group>
               {logsOpened && (
                  <ScrollArea style={{ flex: 1 }} p="xs" viewportRef={logScrollRef}>
                     <Stack gap={4}>
                        {logs.map((log: any, i: number) => (
                           <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
                              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap', fontFamily: 'monospace' }}>
                                 {formatTime(log.timestamp)}
                              </Text>
                              <Badge size="xs" color={log.level === 'ERROR' ? 'red' : log.level === 'WARN' ? 'orange' : 'blue'} variant="light" style={{ minWidth: 50 }}>
                                 {log.level}
                              </Badge>
                              <Text size="xs" style={{ wordBreak: 'break-all', fontFamily: 'monospace' }}>
                                 {log.message}
                              </Text>
                           </Group>
                        ))}
                        {logs.length === 0 && (
                           <Text size="xs" c="dimmed" ta="center" py="xl">No logs yet.</Text>
                        )}
                     </Stack>
                  </ScrollArea>
               )}
            </Paper>
          </Box>
        </Flex>

        <SidebarDrawer 
          onDragStart={onDragStart}
          onAddItem={(type, refId, label, subType) => {
            const bounds = reactFlowWrapper.current?.getBoundingClientRect();
            let pos;
            if (quickAddSource) {
              const sourceNode = nodes.find(n => n.id === quickAddSource!.nodeId);
              pos = sourceNode ? { x: sourceNode.position.x + 250, y: sourceNode.position.y } : { x: 100, y: 100 };
            } else {
              pos = project({ x: (bounds?.width || 400) / 2, y: (bounds?.height || 400) / 2 });
            }
            addNodeAtPosition(type, refId, label, subType, pos);
            if (quickAddSource) setDrawerOpened(false);
          }}
          sources={sources?.data || []}
          sinks={sinks?.data || []}
        />

        <Modal
          opened={settingsOpened}
          onClose={() => {
            setSettingsOpened(false);
            setSelectedNode(null);
          }}
          title={
            <Group gap="xs" id="workflow-settings-modal-title">
              <ThemeIcon variant="light" color="blue">
                <IconSettings size="1.2rem" />
              </ThemeIcon>
              <Text fw={700}>
                {selectedNode?.data?.ref_id === 'new' 
                  ? `Create New ${selectedNode?.type?.toUpperCase()}` 
                  : `Configure ${selectedNode?.type?.toUpperCase()} Node`}
              </Text>
            </Group>
          }
          aria-labelledby="workflow-settings-modal-title"
          aria-describedby="workflow-settings-modal-desc"
          fullScreen
          padding="md"
        >
          <Box mb="md">
            <Title order={4} mb={4}>Workflow Node Settings</Title>
            <Text id="workflow-settings-modal-desc" size="sm" c="dimmed">
              Configure node settings, run simulations, and review output data.
            </Text>
          </Box>
          <ScrollArea h="calc(100vh - 120px)" offsetScrollbars>
            <Stack gap="lg" style={{ width: '100%' }}>
              <Box>
                  {selectedNode?.type === 'source' && (
                    <SourceForm 
                      key={selectedNode.id}
                      embedded 
                      onSave={handleInlineSave} 
                      onRunSimulation={handleTest}
                      isEditing={selectedNode.data.ref_id !== 'new'} 
                      initialData={selectedNodeData} 
                      vhost={vhost}
                      workerID={workerID}
                    />
                  )}
                  {selectedNode?.type === 'sink' && (
                    <SinkForm 
                      key={selectedNode.id}
                      embedded 
                      onSave={handleInlineSave} 
                      isEditing={selectedNode.data.ref_id !== 'new'} 
                      initialData={selectedNodeData} 
                      vhost={vhost}
                      workerID={workerID}
                      availableFields={availableFields}
                      incomingPayload={incomingPayload}
                      sinks={sinks?.data || []}
                    />
                  )}
                  {selectedNode && ['transformation', 'validator', 'condition', 'switch', 'merge', 'stateful', 'note'].includes(selectedNode.type!) && (
                    <Stack gap="sm">
                       <TransformationForm
                         selectedNode={selectedNode}
                         updateNodeConfig={updateNodeConfig}
                         onRunSimulation={handleTest}
                         availableFields={availableFields}
                         incomingPayload={incomingPayload}
                         sources={sources?.data || []}
                         sinkSchema={sinkSchema}
                       />
                       <Group justify="flex-end" mt="md">
                         <Button variant="light" onClick={() => {
                           setSelectedNode(null);
                           setSettingsOpened(false);
                         }}>Done</Button>
                       </Group>
                    </Stack>
                  )}
              </Box>

              {(selectedNode?.type === 'source' || selectedNode?.type === 'sink') && (selectedNode?.data?.testResult || selectedNode?.data?.lastSample) && (
                <Paper withBorder p="md" bg="gray.0">
                  <Stack gap="xs">
                    <Text fw={700} size="sm">Data Output</Text>
                    <Code block style={{ fontSize: '10px' }}>
                      {JSON.stringify(selectedNode.data.testResult?.payload || selectedNode.data.lastSample, null, 2)}
                    </Code>
                  </Stack>
                </Paper>
              )}

              <Divider />
              <Button color="red" variant="light" leftSection={<IconTrash size="1rem" />} onClick={() => deleteNode(selectedNode!.id)}>
                Remove Node from Canvas
              </Button>
            </Stack>
          </ScrollArea>
        </Modal>

        <Modals
          onRunSimulation={(input) => testMutation.mutate(input)}
          isTesting={testMutation.isPending}
        />
      </Box>
    </WorkflowContext.Provider>
  );
}

export default function WorkflowEditorPage() {
  return (
    <ReactFlowProvider>
      <EditorInner />
    </ReactFlowProvider>
  );
}
