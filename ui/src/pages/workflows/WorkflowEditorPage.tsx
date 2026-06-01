import { 
  useCallback, useEffect, useMemo, useRef, useState 
} from 'react';
import { useShallow } from 'zustand/react/shallow';
import { 
  MarkerType,
  type Node,
  type Edge,
  ReactFlowProvider,
  useReactFlow,
  addEdge
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { 
  Group, Paper, Stack, 
  Text, Box, Badge, ScrollArea, Flex,
  Code, Modal, Button, Divider, ThemeIcon, Title
} from '@mantine/core';
import { useHotkeys } from '@mantine/hooks';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import type { Source, Sink } from '@/types';
import { apiFetch } from '@/api';
import { useVHost } from '@/context/VHostContext';
import { notifications } from '@mantine/notifications';
import { useMantineColorScheme } from '@mantine/core';
import { SourceForm } from '@/components/forms/SourceForm';
import { SinkForm } from '@/components/forms/SinkForm';
import { TransformationForm } from '@/components/forms/TransformationForm';
import { getToken } from '@/auth/storage';
// Refactored Components & Hooks
import { useWorkflowStore } from './WorkflowEditor/store/useWorkflowStore';
import { EditorToolbar } from './WorkflowEditor/components/EditorToolbar';
import { FlowCanvas } from './WorkflowEditor/components/FlowCanvas';
import { LiveLogPanel } from './WorkflowEditor/components/LiveLogPanel';
import { SidebarDrawer } from './WorkflowEditor/components/SidebarDrawer';
import { NodeConfigModal } from './WorkflowEditor/components/NodeConfigModal';
import { Modals } from './WorkflowEditor/components/Modals';
import { LiveStreamInspector } from './WorkflowEditor/components/LiveStreamInspector';
import { SchemaRegistryModal } from '@/components/modals/SchemaRegistryModal';
import { WorkflowHistoryModal } from '@/components/modals/WorkflowHistoryModal';
import { AIGeneratorModal } from './WorkflowEditor/components/AIGeneratorModal';
import { AIFixModal } from './WorkflowEditor/components/AIFixModal';
import { WorkflowContext } from './WorkflowEditor/nodes/BaseNode';
import { IconDeviceFloppy, IconRefresh, IconSettings, IconTrash } from '@tabler/icons-react';
import { useWorkflowLayout } from './WorkflowEditor/hooks/useWorkflowLayout';
import { useNodeContext } from './WorkflowEditor/hooks/useNodeContext';

const API_BASE = '/api';

function EditorInner() {
  const { id } = useParams({ strict: false }) as any;
  const isNew = !id || id === 'new';
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const { screenToFlowPosition, zoomIn, zoomOut, fitView: rfFitView } = useReactFlow();
  const lastInitializedId = useRef<string | null>(null);
  const { onLayout } = useWorkflowLayout();
  
  const { 
    vhost, selectedNode, active, logsPaused, quickAddSource,
    testResults, testInput, name, workerID,
    workflowStatus, settingsOpened,
    setName, setActive, setWorkflowStatus, 
    setNodes, setEdges, setQuickAddSource,
    setSelectedNode, setSettingsOpened, setDrawerOpened, setDrawerTab, updateNodeConfig,
    setTestResults, setTestModalOpened,
    setTraceInspectorOpened, setTraceMessageID, setSchemaRegistryOpened,
    schemaRegistryOpened, historyOpened, setHistoryOpened, liveStreamOpened, setLiveStreamOpened,
    aiGeneratorOpened, setAIGeneratorOpened
  } = useWorkflowStore(useShallow(state => ({
    vhost: state.vhost,
    selectedNode: state.selectedNode,
    active: state.active,
    logsPaused: state.logsPaused,
    quickAddSource: state.quickAddSource,
    testResults: state.testResults,
    testInput: state.testInput,
    name: state.name,
    workerID: state.workerID,
    workflowStatus: state.workflowStatus,
    logsOpened: state.logsOpened,
    settingsOpened: state.settingsOpened,
    schemaRegistryOpened: state.schemaRegistryOpened,
    historyOpened: state.historyOpened,
    liveStreamOpened: state.liveStreamOpened,
    traceInspectorOpened: state.traceInspectorOpened,
    traceMessageID: state.traceMessageID,
    aiGeneratorOpened: state.aiGeneratorOpened,
    setName: state.setName,
    setActive: state.setActive,
    setWorkflowStatus: state.setWorkflowStatus,
    setNodes: state.setNodes,
    setEdges: state.setEdges,
    setQuickAddSource: state.setQuickAddSource,
    setSelectedNode: state.setSelectedNode,
    setSettingsOpened: state.setSettingsOpened,
    setDrawerOpened: state.setDrawerOpened,
    setDrawerTab: state.setDrawerTab,
    updateNodeConfig: state.updateNodeConfig,
    setTestResults: state.setTestResults,
    setTestModalOpened: state.setTestModalOpened,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setTraceMessageID: state.setTraceMessageID,
    setSchemaRegistryOpened: state.setSchemaRegistryOpened,
    setHistoryOpened: state.setHistoryOpened,
    setLiveStreamOpened: state.setLiveStreamOpened,
    setAIGeneratorOpened: state.setAIGeneratorOpened,
    setDryRun: state.setDryRun,
  })));
  // Removed unused setters from here as they are now handled via setState batching
  
  // Decoupled nodes and edges from main component selection to avoid re-renders on every move

  const { selectedVHost } = useVHost();
  const [configModalOpen, setConfigModalOpen] = useState(false);
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const [aiFixModalData, setAIFixModalData] = useState<any>(null);
  const [saveConfirmOpened, setSaveConfirmOpened] = useState(false);

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
      return (sources?.data as Source[])?.find((s: Source) => s.id === selectedNode?.data.ref_id);
    }
    if (selectedNode.type === 'sink') {
      return (sinks?.data as Sink[])?.find((s: Sink) => s.id === selectedNode?.data.ref_id);
    }
    return null;
  }, [selectedNode?.id, selectedNode?.data?.ref_id, sources, sinks, vhost]);

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
    const wId = id || 'new';
    if (workflow && lastInitializedId.current !== wId) {
      console.log('Initializing workflow:', wId);
      lastInitializedId.current = wId;
      
      const newValues: any = {
        name: workflow.name || '',
        vhost: workflow.vhost || 'default',
        workerID: workflow.worker_id || '',
        active: workflow.active || false,
        workflowStatus: workflow.status || 'Stopped',
        deadLetterSinkID: workflow.dead_letter_sink_id || '',
        dlqThreshold: workflow.dlq_threshold || 0,
        prioritizeDLQ: workflow.prioritize_dlq || false,
        maxRetries: workflow.max_retries || 3,
        retryInterval: workflow.retry_interval || '100ms',
        reconnectInterval: workflow.reconnect_interval || '30s',
        schemaType: workflow.schema_type || '',
        schema: workflow.schema || '',
        tags: workflow.tags || [],
        dryRun: workflow.dry_run || false,
        workspaceID: workflow.workspace_id || '',
        cpuRequest: workflow.cpu_request || 0,
        memoryRequest: workflow.memory_request || 0,
        throughputRequest: workflow.throughput_request || 0,
        idleTimeout: workflow.idle_timeout || '',
        tier: workflow.tier || 'Hot',
        cron: workflow.cron || '',
        retentionDays: workflow.retention_days !== undefined ? workflow.retention_days : null,
        traceSampleRate: workflow.trace_sample_rate !== undefined ? workflow.trace_sample_rate : 1.0,
        traceRetention: workflow.trace_retention || '7d',
        auditRetention: workflow.audit_retention || '30d',
        historyOpened: false,
      };

      const initialNodes = (workflow.nodes || []).map((node: any) => ({
        id: node.id,
        type: node.type,
        position: { x: node.x || 0, y: node.y || 0 },
        data: { ...(node.config || {}), ref_id: node.ref_id }
      }));
      newValues.nodes = initialNodes;

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
      newValues.edges = initialEdges;
      newValues.historyOpened = false;

      useWorkflowStore.setState(newValues);
    }
  }, [id, workflow?.id, workflow?.name]);

  useEffect(() => {
    if (!id || id === 'new' || !active || logsPaused) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `&token=${token}` : '';
    const ws = new WebSocket(`${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}${tokenParam}`);

    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data);
        useWorkflowStore.setState((state) => {
          if (Array.isArray(log)) {
            return { logs: log.slice(0, 100) };
          } else {
            return { logs: [log, ...state.logs].slice(0, 100) };
          }
        });
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active, logsPaused]);

  // WebSocket for status
  useEffect(() => {
    if (!id || id === 'new' || !active) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const tokenParam = token ? `?token=${token}` : '';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/status${tokenParam}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        const updates = Array.isArray(data) ? data : [data];
        
        updates.forEach(update => {
          if (update.workflow_id === id) {
            const currentStore = useWorkflowStore.getState();
            const nextState: any = {};

            if (update.node_metrics) {
              if (JSON.stringify(currentStore.nodeMetrics) !== JSON.stringify(update.node_metrics)) {
                nextState.nodeMetrics = update.node_metrics;
              }
            }
            if (update.node_error_metrics) {
              if (JSON.stringify(currentStore.nodeErrorMetrics) !== JSON.stringify(update.node_error_metrics)) {
                nextState.nodeErrorMetrics = update.node_error_metrics;
              }
            }
            if (update.node_samples) {
              if (JSON.stringify(currentStore.nodeSamples) !== JSON.stringify(update.node_samples)) {
                nextState.nodeSamples = update.node_samples;
              }
            }
            if (update.sink_cb_statuses) {
              if (JSON.stringify(currentStore.sinkCBStatuses) !== JSON.stringify(update.sink_cb_statuses)) {
                nextState.sinkCBStatuses = update.sink_cb_statuses;
              }
            }
            if (update.sink_buffer_fill) {
              if (JSON.stringify(currentStore.sinkBufferFill) !== JSON.stringify(update.sink_buffer_fill)) {
                nextState.sinkBufferFill = update.sink_buffer_fill;
              }
            }
            if (update.edge_metrics) {
              if (JSON.stringify(currentStore.edgeThroughput) !== JSON.stringify(update.edge_metrics)) {
                nextState.edgeThroughput = update.edge_metrics;
              }
            }
            if (update.source_status) {
              if (currentStore.sourceStatus !== update.source_status) {
                nextState.sourceStatus = update.source_status;
              }
            }
            if (update.sink_statuses) {
              if (JSON.stringify(currentStore.sinkStatuses) !== JSON.stringify(update.sink_statuses)) {
                nextState.sinkStatuses = update.sink_statuses;
              }
            }
            if (update.dead_letter_count !== undefined) {
              if (currentStore.workflowDeadLetterCount !== update.dead_letter_count) {
                nextState.workflowDeadLetterCount = update.dead_letter_count;
              }
            }
            if (update.engine_status) {
              if (currentStore.workflowStatus !== update.engine_status) {
                nextState.workflowStatus = update.engine_status;
              }
            }

            if (Object.keys(nextState).length > 0) {
              useWorkflowStore.setState(nextState);
            }
          }
        });
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, active]);

  // Node operations
  const handlePlusClick = (nodeId: string, handleId: string | null) => {
    setQuickAddSource({ nodeId, handleId });
    setDrawerOpened(true);
  };

  const onNodeClick = useCallback((_event: React.MouseEvent, node: Node) => {
    setSelectedNode(node);
    // For source/sink/transformation/validator open the legacy popup config
    if (node.type === 'source' || node.type === 'sink' || node.type === 'transformation' || node.type === 'validator') {
      setConfigModalOpen(true);
      return;
    }
    // For other nodes keep using the sidebar drawer Config tab
    setSettingsOpened(false);
    setDrawerOpened(true);
    setDrawerTab('config');
  }, [setSelectedNode, setSettingsOpened, setDrawerOpened, setDrawerTab]);

  const onEdgeClick = useCallback((_event: React.MouseEvent, edge: Edge) => {
    if (!testResults) return;
    const sourceResult = testResults.find(r => r.node_id === edge.source);
    if (sourceResult) {
      notifications.show({
        title: `Edge Data: ${edge.id}`,
        message: (
          <Stack gap="xs">
            <Text size="xs" fw={700}>Data passing through this path:</Text>
            <Code block style={{ fontSize: '10px', maxHeight: '300px', overflow: 'auto' }}>
              {JSON.stringify((sourceResult as any).payload, null, 2)}
            </Code>
          </Stack>
        ),
        color: 'blue',
        autoClose: false,
      });
    }
  }, [testResults]);

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
        animated: active,
        style: { strokeWidth: active ? 3 : 2 },
      };
      setEdges((eds) => addEdge(newEdge, eds));
      setQuickAddSource(null);
    }
    return newNode;
  }, [quickAddSource, active, setEdges, setNodes, setQuickAddSource]);

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
    // Auto-select newly dropped node and open popup for modal-config types
    setSelectedNode(node);
    if (nodeType === 'source' || nodeType === 'sink' || nodeType === 'transformation' || nodeType === 'validator') {
      setConfigModalOpen(true);
    } else {
      setDrawerOpened(true);
      setDrawerTab('config');
    }
  }, [screenToFlowPosition, addNodeAtPosition, setSelectedNode, setDrawerOpened, setDrawerTab]);

  const handleInlineSave = (updatedData: Partial<Source | Sink>) => {
    if (!selectedNode) return;
    updateNodeConfig(selectedNode.id, { 
       ...updatedData, 
       label: (updatedData as any).name || selectedNode.data.label,
       ref_id: (updatedData as any).id 
    });
    setSettingsOpened(false);
    setSelectedNode(null);
    queryClient.invalidateQueries({ queryKey: ['sources'] });
    queryClient.invalidateQueries({ queryKey: ['sinks'] });
    // Auto-save workflow when a node configuration is saved to ensure state is consistent
    saveMutation.mutate();
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
      const s = useWorkflowStore.getState();
      let msg = input;
      if (!msg) {
        try {
          msg = JSON.parse(s.testInput);
        } catch (e) {
          throw new Error('Invalid JSON in Input Message');
        }
      }
      
      const res = await apiFetch(`${API_BASE}/workflows/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflow: { 
            name: s.name, 
            vhost: s.vhost, 
            dead_letter_sink_id: s.deadLetterSinkID,
            dlq_threshold: s.dlqThreshold,
            prioritize_dlq: s.prioritizeDLQ,
            max_retries: s.maxRetries,
            retry_interval: s.retryInterval,
            reconnect_interval: s.reconnectInterval,
            schema_type: s.schemaType,
            schema: s.schema,
            nodes: s.nodes.map(n => ({
              id: n.id,
              type: n.type,
              ref_id: n.data.ref_id,
              config: n.data,
              x: n.position.x,
              y: n.position.y
            })),
            edges: s.edges.map(e => ({
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
    const s = useWorkflowStore.getState();
    const { nodes } = s;
    
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
  }, [selectedNode, sources, testInput, testMutation, setTestModalOpened]);

  const handleRefreshFields = useCallback(async () => {
    let input = null;
    const s = useWorkflowStore.getState();
    const { nodes } = s;

    // 1. Identify the primary source node to refresh from
    const sourceNode = selectedNode?.type === 'source' ? selectedNode : nodes.find(n => n.type === 'source');
    
    if (sourceNode) {
      const sourceData = sources?.data?.find((s: any) => s.id === sourceNode.data.ref_id);
      if (sourceData) {
        try {
          notifications.show({ 
            id: 'refresh-fields', 
            title: 'Refreshing Fields', 
            message: `Fetching fresh sample from ${sourceData.name || sourceNode.id}...`, 
            loading: true,
            autoClose: false,
            withCloseButton: false
          });
          
          let table = sourceData.config.table || sourceData.config.collection || '';
          if (!table && sourceData.config.tables) {
            table = sourceData.config.tables.split(',')[0].trim();
          }

          const res = await apiFetch(`${API_BASE}/sources/sample`, {
            method: 'POST',
            body: JSON.stringify({
              source: { type: sourceData.type, config: sourceData.config },
              table: table
            })
          });
          
          if (res.ok) {
            const sampleMsg = await res.json();
            // Handle CDC envelopes - parse them if they are strings for better UX in field explorer
            if (sampleMsg && typeof sampleMsg === 'object') {
              if (typeof sampleMsg.after === 'string') {
                try { sampleMsg.after = JSON.parse(sampleMsg.after); } catch (_) {}
              }
              if (typeof sampleMsg.before === 'string') {
                try { sampleMsg.before = JSON.parse(sampleMsg.before); } catch (_) {}
              }
            }

            input = sampleMsg;
            
            // Persist the new sample to the source config so it's available for next time
            await apiFetch(`${API_BASE}/sources/${sourceData.id}`, {
              method: 'PUT',
              body: JSON.stringify({ ...sourceData, sample: JSON.stringify(sampleMsg) })
            });
            
            queryClient.invalidateQueries({ queryKey: ['sources'] });
            notifications.update({ 
              id: 'refresh-fields', 
              title: 'Refresh Complete', 
              message: 'Fresh sample fetched and saved.', 
              color: 'green', 
              loading: false,
              autoClose: 2000
            });
          }
        } catch (e: any) {
          notifications.update({ 
            id: 'refresh-fields', 
            title: 'Refresh Partial', 
            message: 'Could not fetch fresh sample from source. Re-simulating with existing data.', 
            color: 'orange', 
            loading: false,
            autoClose: 3000
          });
        }
      }
    }

    // 2. Trigger simulation (dry run) to update all downstream nodes and available fields
    handleTest(input, true);
  }, [selectedNode, sources, handleTest, queryClient]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      const s = useWorkflowStore.getState();
      const payload = {
        name: s.name,
        vhost: s.vhost,
        active: s.active,
        status: s.workflowStatus,
        worker_id: s.workerID,
        dead_letter_sink_id: s.deadLetterSinkID,
        dlq_threshold: s.dlqThreshold,
        prioritize_dlq: s.prioritizeDLQ,
        max_retries: s.maxRetries,
        retry_interval: s.retryInterval,
        reconnect_interval: s.reconnectInterval,
        idle_timeout: s.idleTimeout,
        tier: s.tier,
        dry_run: s.dryRun,
        workspace_id: s.workspaceID,
        cpu_request: s.cpuRequest,
        memory_request: s.memoryRequest,
        throughput_request: s.throughputRequest,
        cron: s.cron,
        retention_days: s.retentionDays,
        trace_sample_rate: s.traceSampleRate,
        trace_retention: s.traceRetention,
        audit_retention: s.auditRetention,
        schema_type: s.schemaType,
        schema: s.schema,
        tags: s.tags,
        nodes: s.nodes.map(n => ({
          id: n.id,
          type: n.type,
          ref_id: n.data.ref_id,
          config: n.data,
          x: n.position.x,
          y: n.position.y
        })),
        edges: s.edges.map(e => ({
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
      if (!isNew && active) {
        setWorkflowStatus('Restarting');
      }
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

  const handleSave = useCallback(() => {
    if (!isNew && active) {
      setSaveConfirmOpened(true);
    } else {
      saveMutation.mutate();
    }
  }, [isNew, active, saveMutation]);

  // Guard: do not trigger editor hotkeys while typing in inputs/textareas/selects or contentEditable
  const isTypingTarget = (evt: any) => {
    const t = (evt?.target as HTMLElement) || null;
    if (!t) return false;
    const tag = t.tagName?.toLowerCase();
    return tag === 'input' || tag === 'textarea' || tag === 'select' || (t as any).isContentEditable;
  };

  useHotkeys([
    ['ctrl+s', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleSave(); }],
    ['ctrl+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, false); }],
    ['ctrl+shift+enter', (e) => { if (isTypingTarget(e)) return; e.preventDefault(); handleTest(null, true); }],
    ['delete, backspace', (e) => {
       if (isTypingTarget(e)) return;
       const { nodes, edges } = useWorkflowStore.getState();
       const anySelected = nodes.some(n => n.selected) || edges.some(e => e.selected);
       if (anySelected) {
          setNodes(nds => nds.filter(n => !n.selected));
          setEdges(eds => eds.filter(e => !e.selected));
          setSelectedNode(null);
       }
    }]
  ]);

  const { incomingPayload, availableFields, sinkSchema, upstreamSource } = useNodeContext(
    selectedNode,
    testResults,
    sources?.data || [],
    sinks?.data || []
  );

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
          onSave={handleSave}
          onTest={(dry) => handleTest(null, dry)}
          onConfigureTest={() => setTestModalOpened(true)}
          onToggle={() => {
            if (!active) {
              // Ensure the latest workflow state is saved before starting
              saveMutation.mutate(undefined, {
                onSuccess: () => {
                  toggleMutation.mutate();
                }
              });
            } else {
              toggleMutation.mutate();
            }
          }}
          onRebuild={() => rebuildMutation.mutate(0)}
          onClearTest={() => setTestResults(null)}
          onAutoLayout={() => {
            try {
              onLayout('LR');
              notifications.show({ message: 'Auto-layout applied', color: 'blue' });
            } catch (e: any) {
              notifications.show({ message: e?.message || 'Failed to layout', color: 'red' });
            }
          }}
          isSaving={saveMutation.isPending}
          isTesting={testMutation.isPending}
          isToggling={toggleMutation.isPending}
          zoom={1}
          zoomIn={zoomIn}
          zoomOut={zoomOut}
          fitView={rfFitView}
          vhosts={vhosts?.data || []}
          workers={workers?.data || []}
        />

        <Flex style={{ flex: 1, overflow: 'hidden' }} gap="md">
          <Box style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', gap: 'var(--mantine-spacing-md)' }}>
            <Paper withBorder radius="md" style={{ flex: 1, position: 'relative' }} ref={reactFlowWrapper}>
              <FlowCanvas 
                onNodeClick={onNodeClick}
                onEdgeClick={onEdgeClick}
                onDragOver={onDragOver}
                onDrop={onDrop}
              />
            </Paper>

            {/* Node Config Modal (restores popup UX for source/sink/transformation/validator) */}
            <NodeConfigModal 
              opened={configModalOpen} 
              onClose={() => setConfigModalOpen(false)} 
              selectedNode={selectedNode}
              updateNodeConfig={updateNodeConfig}
              onSave={handleInlineSave}
              vhost={vhost}
              workerID={workerID}
              testResults={testResults}
              sources={sources?.data || []}
              sinks={sinks?.data || []}
              onRefreshFields={handleRefreshFields}
              isRefreshing={testMutation.isPending}
            />

            {/* Live Log Panel */}
            <LiveLogPanel 
              workflowId={id} 
              active={active} 
              onTraceClick={(msgId) => {
                setTraceMessageID(msgId);
                setTraceInspectorOpened(true);
              }}
              onErrorClick={(log) => {
                setAIFixModalData({ 
                  workflow_id: id, 
                  node_id: log.node_id, 
                  error: log.message 
                });
              }}
            />
          </Box>

          <SidebarDrawer 
            onDragStart={onDragStart}
            onAddItem={(type, refId, label, subType, extraData) => {
              const bounds = reactFlowWrapper.current?.getBoundingClientRect();
              const { nodes } = useWorkflowStore.getState();
              let pos;
              if (quickAddSource) {
                const sourceNode = nodes.find(n => n.id === quickAddSource!.nodeId);
                pos = sourceNode ? { x: sourceNode.position.x + 250, y: sourceNode.position.y } : { x: 100, y: 100 };
              } else {
                pos = screenToFlowPosition({ x: (bounds?.width || 400) / 2, y: (bounds?.height || 400) / 2 });
              }
              const node = addNodeAtPosition(type, refId, label, subType, pos, extraData);
              // Select the newly added node and open popup/drawer accordingly
              setSelectedNode(node);
              if (type === 'source' || type === 'sink' || type === 'transformation' || type === 'validator') {
                setConfigModalOpen(true);
              } else {
                setDrawerOpened(true);
                setDrawerTab('settings');
                if (quickAddSource) setDrawerOpened(false);
              }
            }}
            sources={sources?.data || []}
            sinks={sinks?.data || []}
          />
        </Flex>

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
                      initialData={selectedNodeData as Source | undefined} 
                      vhost={vhost}
                      workerID={workerID}
                      onRefreshFields={handleRefreshFields}
                      isRefreshing={testMutation.isPending}
                    />
                  )}
                  {selectedNode?.type === 'sink' && (
                    <SinkForm 
                      key={selectedNode.id}
                      embedded 
                      onSave={handleInlineSave} 
                      isEditing={selectedNode.data.ref_id !== 'new'} 
                      initialData={selectedNodeData as Sink | undefined} 
                      vhost={vhost}
                      workerID={workerID}
                      availableFields={availableFields}
                      incomingPayload={incomingPayload}
                      sinks={sinks?.data || []}
                      upstreamSource={upstreamSource}
                      onRefreshFields={handleRefreshFields}
                      isRefreshing={testMutation.isPending}
                    />
                  )}
                  {selectedNode && ['transformation', 'validator', 'condition', 'switch', 'router', 'merge', 'stateful', 'note'].includes(selectedNode.type!) && (
                    <Stack gap="sm">
                       <TransformationForm
                         selectedNode={selectedNode}
                         updateNodeConfig={updateNodeConfig}
                         onRunSimulation={handleTest}
                         availableFields={availableFields}
                         incomingPayload={incomingPayload}
                         sources={sources?.data || []}
                         sinkSchema={sinkSchema}
                         onRefreshFields={handleRefreshFields}
                         isRefreshing={testMutation.isPending}
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

              {(selectedNode?.type === 'source' || selectedNode?.type === 'sink') && (selectedNode?.data?.testResult || selectedNode?.data?.lastSample) ? (
                <Paper withBorder p="md" bg="gray.0">
                  <Stack gap="xs">
                    <Text fw={700} size="sm">Data Output</Text>
                    <Code block style={{ fontSize: '10px' }}>
                      {JSON.stringify((selectedNode.data.testResult as any)?.payload || selectedNode.data.lastSample, null, 2)}
                    </Code>
                  </Stack>
                </Paper>
              ) : null}

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

        <SchemaRegistryModal 
          opened={schemaRegistryOpened} 
          onClose={() => setSchemaRegistryOpened(false)} 
        />

        <WorkflowHistoryModal
          workflowId={id}
          opened={historyOpened}
          onClose={() => setHistoryOpened(false)}
        />

        <LiveStreamInspector
          workflowId={id}
          opened={liveStreamOpened}
          onClose={() => setLiveStreamOpened(false)}
        />

        <AIGeneratorModal
          opened={aiGeneratorOpened}
          onClose={() => setAIGeneratorOpened(false)}
          onGenerated={(generatedWorkflow) => {
            if (Array.isArray(generatedWorkflow.nodes)) {
              setNodes(generatedWorkflow.nodes.map((n: any) => ({
                ...n,
                position: n.position || { x: Math.random() * 400, y: Math.random() * 400 }
              })));
            }
            if (Array.isArray(generatedWorkflow.edges)) {
              setEdges(generatedWorkflow.edges);
            }
            if (generatedWorkflow.name) {
              setName(generatedWorkflow.name);
            }
            notifications.show({
              title: 'Workflow Generated',
              message: 'AI has scaffolded the workflow. Please configure node details.',
              color: 'indigo'
            });
          }}
        />

        <AIFixModal
          data={aiFixModalData}
          opened={!!aiFixModalData}
          onClose={() => setAIFixModalData(null)}
        />

        <Modal
          opened={saveConfirmOpened}
          onClose={() => setSaveConfirmOpened(false)}
          title={<Group gap="xs"><IconDeviceFloppy size="1.2rem" /><Text fw={700}>Save Active Workflow</Text></Group>}
          centered
          size="md"
        >
          <Stack gap="md">
            <Text size="sm">
              This workflow is currently <b>Active</b>. Saving changes will trigger a <b>graceful restart</b> of the engine to apply the new configuration.
            </Text>
            <Paper withBorder p="xs" bg="blue.0">
               <Group gap="xs" wrap="nowrap">
                  <IconRefresh size="1.2rem" color="var(--mantine-color-blue-6)" />
                  <Text size="xs" c="blue.9">
                    Hermod will perform a final checkpoint before restarting to ensure all processed states are saved and no data is lost.
                  </Text>
               </Group>
            </Paper>
            <Group justify="flex-end" mt="md">
              <Button variant="subtle" onClick={() => setSaveConfirmOpened(false)}>Cancel</Button>
              <Button 
                color="blue" 
                loading={saveMutation.isPending}
                onClick={() => {
                  setSaveConfirmOpened(false);
                  saveMutation.mutate();
                }}
              >
                Save & Restart
              </Button>
            </Group>
          </Stack>
        </Modal>
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


