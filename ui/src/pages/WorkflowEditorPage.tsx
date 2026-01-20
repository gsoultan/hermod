import { 
  useCallback, useEffect, useMemo, useRef 
} from 'react';
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
  Button, Group, Paper, Stack, ActionIcon, 
  Text, Box, Divider, Modal, Badge, ScrollArea, Flex,
  ThemeIcon, Code
} from '@mantine/core';
import { useHotkeys } from '@mantine/hooks';
import { 
  IconTrash, 
  IconSettings,
  IconChevronDown, IconChevronUp, IconClearAll, IconPlayerPause,
  IconPlayerPlay
} from '@tabler/icons-react';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useVHost } from '../context/VHostContext';
import { notifications } from '@mantine/notifications';
import { SourceForm } from '../components/SourceForm';
import { SinkForm } from '../components/SinkForm';
import { TransformationForm } from '../components/TransformationForm';
import { useMantineColorScheme } from '@mantine/core';
import { getAllKeys, deepMergeSim } from '../utils/transformationUtils';

// Refactored Components & Hooks
import { useWorkflowStore } from './WorkflowEditor/store/useWorkflowStore';
import { useStyledFlow } from './WorkflowEditor/hooks/useStyledFlow';
import { EditorToolbar } from './WorkflowEditor/components/EditorToolbar';
import { SidebarDrawer } from './WorkflowEditor/components/SidebarDrawer';
import { Modals } from './WorkflowEditor/components/Modals';
import { WorkflowContext } from './WorkflowEditor/nodes/BaseNode';
import { SourceNode, SinkNode } from './WorkflowEditor/nodes/SourceSinkNodes';
import { TransformationNode, SwitchNode, MergeNode, StatefulNode, NoteNode } from './WorkflowEditor/nodes/MiscNodes';
import { ConditionNode } from './WorkflowEditor/nodes/ConditionNode';

const API_BASE = '/api';

const nodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: TransformationNode,
  condition: ConditionNode,
  switch: SwitchNode,
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
  
  const store = useWorkflowStore();
  const { styledNodes, styledEdges } = useStyledFlow();

  const { selectedVHost } = useVHost();
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  const logScrollRef = useRef<HTMLDivElement>(null);

  // Queries
  const { data: sources } = useQuery({ 
    queryKey: ['sources', store.vhost], 
    queryFn: async () => {
      const vhostParam = (store.vhost && store.vhost !== 'all') ? `?vhost=${store.vhost}` : '';
      return (await apiFetch(`${API_BASE}/sources${vhostParam}`)).json();
    } 
  });
  
  const { data: sinks } = useQuery({ 
    queryKey: ['sinks', store.vhost], 
    queryFn: async () => {
      const vhostParam = (store.vhost && store.vhost !== 'all') ? `?vhost=${store.vhost}` : '';
      return (await apiFetch(`${API_BASE}/sinks${vhostParam}`)).json();
    } 
  });

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

  const selectedNodeData = useMemo(() => {
    if (!store.selectedNode) return null;
    if (store.selectedNode.data.ref_id === 'new') {
       const type = store.selectedNode.data.type;
       if (type && type !== 'new') {
           return { type, vhost: store.vhost };
       }
       return { vhost: store.vhost };
    }
    if (store.selectedNode.type === 'source') {
      return sources?.data?.find((s: any) => s.id === store.selectedNode?.data.ref_id);
    }
    if (store.selectedNode.type === 'sink') {
      return sinks?.data?.find((s: any) => s.id === store.selectedNode?.data.ref_id);
    }
    return null;
  }, [store.selectedNode, sources, sinks, store.vhost]);

  useEffect(() => {
    if (workflow) {
      store.setName(workflow.name || '');
      store.setVHost(workflow.vhost || 'default');
      store.setWorkerID(workflow.worker_id || '');
      store.setActive(workflow.active || false);
      store.setWorkflowStatus(workflow.status || 'Stopped');
      
      const initialNodes = (workflow.nodes || []).map((node: any) => ({
        id: node.id,
        type: node.type,
        position: { x: node.x || 0, y: node.y || 0 },
        data: { ...(node.config || {}), ref_id: node.ref_id }
      }));
      store.setNodes(initialNodes);

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
      store.setEdges(initialEdges);
    }
  }, [workflow]);

  // WebSocket for logs
  useEffect(() => {
    if (!id || id === 'new' || !store.active || store.logsPaused) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${protocol}//${window.location.host}/api/ws/logs?workflow_id=${id}`);

    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data);
        store.setLogs(prev => [log, ...prev].slice(0, 100));
      } catch (e) {}
    };

    return () => ws.close();
  }, [id, store.active, store.logsPaused]);

  // Node operations
  const handlePlusClick = (nodeId: string, handleId: string | null) => {
    store.setQuickAddSource({ nodeId, handleId });
    store.setDrawerOpened(true);
  };

  const onConnect = useCallback((params: any) => {
    const label = params.sourceHandle?.split(':::')[0] || params.sourceHandle || '';
    const edge = {
      ...params,
      id: `edge_${Date.now()}`,
      animated: store.active,
      style: { strokeWidth: 2 },
      data: { label }
    };
    store.setEdges((eds) => addEdge(edge, eds));
  }, [store.active, store.setEdges]);

  const onNodeClick = useCallback((_event: any, node: Node) => {
    store.setSelectedNode(node);
    store.setSettingsOpened(true);
    store.setDrawerOpened(false);
  }, [store.setSelectedNode, store.setSettingsOpened, store.setDrawerOpened]);

  const onEdgeClick = useCallback((_event: any, edge: Edge) => {
    if (!store.testResults) return;
    const sourceResult = store.testResults.find(r => r.node_id === edge.source);
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
  }, [store.testResults]);

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

    store.setNodes((nds) => nds.concat(newNode));

    if (store.quickAddSource) {
      const newEdge: Edge = {
        id: `edge_${Date.now()}`,
        source: store.quickAddSource.nodeId,
        sourceHandle: store.quickAddSource.handleId,
        target: newNode.id,
        animated: store.active,
        style: { strokeWidth: store.active ? 3 : 2 },
      };
      store.setEdges((eds) => addEdge(newEdge, eds));
      store.setQuickAddSource(null);
    }
  }, [store.nodes, store.quickAddSource, store.active]);

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
    if (!store.selectedNode) return;
    store.updateNodeConfig(store.selectedNode.id, { 
       ...updatedData, 
       label: updatedData.name || store.selectedNode.data.label,
       ref_id: updatedData.id 
    });
    store.setSettingsOpened(false);
    store.setSelectedNode(null);
    queryClient.invalidateQueries({ queryKey: ['sources'] });
    queryClient.invalidateQueries({ queryKey: ['sinks'] });
  };

  const deleteNode = (nodeId: string) => {
    store.setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    store.setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    store.setSelectedNode(null);
    store.setDrawerOpened(false);
    store.setSettingsOpened(false);
  };

  // Mutations
  const testMutation = useMutation<any, Error, any | void>({
    mutationFn: async (overrideMsg?: any) => {
      let msg = overrideMsg;
      if (!msg) {
        try {
          msg = JSON.parse(store.testInput);
        } catch (e) {
          throw new Error('Invalid JSON in Input Message');
        }
      }
      
      const res = await apiFetch(`${API_BASE}/workflows/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflow: { 
            name: store.name, 
            vhost: store.vhost, 
            nodes: store.nodes.map(n => ({
              id: n.id,
              type: n.type,
              ref_id: n.data.ref_id,
              config: n.data,
              x: n.position.x,
              y: n.position.y
            })),
            edges: store.edges.map(e => ({
              id: e.id,
              source_id: e.source,
              target_id: e.target,
              config: e.data
            })),
          },
          message: msg
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: (data) => {
      store.setTestResults(data);
      store.setTestModalOpened(false);
      notifications.show({ title: 'Test Complete', message: 'The flow has been simulated. Active paths are highlighted.', color: 'blue' });
    },
    onError: (err) => {
      notifications.show({ title: 'Test Failed', message: err.message, color: 'red' });
    }
  });

  const handleTest = useCallback((overrideInput?: any) => {
    let input = overrideInput;
    
    // 1. If no input provided, try to find a sample from the selected source node
    if (!input && store.selectedNode?.type === 'source') {
      const sourceData = sources?.data?.find((s: any) => s.id === store.selectedNode?.data.ref_id);
      if (sourceData?.sample) {
        try { input = JSON.parse(sourceData.sample); } catch(e) {}
      }
    }
    
    // 2. Try to find a sample from the first source node in the workflow
    if (!input) {
      const firstSource = store.nodes.find(n => n.type === 'source');
      if (firstSource) {
        const sourceData = sources?.data?.find((s: any) => s.id === firstSource.data.ref_id);
        if (sourceData?.sample) {
          try { input = JSON.parse(sourceData.sample); } catch(e) {}
        }
      }
    }
    
    // 3. Use existing test input if it looks customized
    if (!input && store.testInput && store.testInput !== '{\n  "payload": "test"\n}') {
      try { input = JSON.parse(store.testInput); } catch(e) {}
    }

    if (input) {
      testMutation.mutate(input);
    } else {
      // Fallback to modal if no valid payload is found
      store.setTestModalOpened(true);
    }
  }, [store.nodes, store.selectedNode, sources, store.testInput, testMutation, store.setTestModalOpened]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      const payload = {
        name: store.name,
        vhost: store.vhost,
        worker_id: store.workerID,
        nodes: store.nodes.map(n => ({
          id: n.id,
          type: n.type,
          ref_id: n.data.ref_id,
          config: n.data,
          x: n.position.x,
          y: n.position.y
        })),
        edges: store.edges.map(e => ({
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
      store.setActive(data.active);
      store.setWorkflowStatus(data.status);
      notifications.show({ 
        title: data.active ? 'Workflow Started' : 'Workflow Stopped', 
        message: `Workflow ${store.name} is now ${data.status.toLowerCase()}`, 
        color: data.active ? 'green' : 'gray' 
      });
      queryClient.invalidateQueries({ queryKey: ['workflow', id] });
    },
    onError: (err: any) => {
      if (err.message?.includes('already running')) {
        store.setActive(true);
        store.setWorkflowStatus('Running');
        queryClient.invalidateQueries({ queryKey: ['workflow', id] });
      }
    }
  });

  useHotkeys([
    ['ctrl+s', (e) => { e.preventDefault(); saveMutation.mutate(); }],
    ['ctrl+enter', (e) => { e.preventDefault(); handleTest(); }],
    ['delete', () => {
       const anySelected = store.nodes.some(n => n.selected) || store.edges.some(e => e.selected);
       if (anySelected) {
          store.setNodes(nds => nds.filter(n => !n.selected));
          store.setEdges(eds => eds.filter(e => !e.selected));
          store.setSelectedNode(null);
       }
    }]
  ]);

  const { incomingPayload, availableFields, sinkSchema } = useMemo(() => {
    let incomingPayload = null;
    let availableFields: string[] = [];
    let sinkSchema = null;

    if (!store.selectedNode) return { incomingPayload, availableFields, sinkSchema };

    // 1. Try to get payload from testResults (if simulation was run)
    if (store.testResults) {
      const edges = store.edges.filter(e => e.target === store.selectedNode?.id);
      if (edges.length > 0) {
        // Collect all upstream payloads
        const mergedPayload = {};
        edges.forEach(edge => {
          const result = store.testResults!.find(r => r.node_id === edge.source);
          if (result && result.payload) {
            deepMergeSim(mergedPayload, result.payload);
          }
        });
        if (Object.keys(mergedPayload).length > 0) {
          incomingPayload = mergedPayload;
          availableFields = getAllKeys(mergedPayload);
        }
      }
    }

    // 2. Try to get payload from selected node's upstream source if it has a sample
    if (!incomingPayload) {
      const edges = store.edges.filter(e => e.target === store.selectedNode?.id);
      if (edges.length > 0) {
        const upstreamNode = store.nodes.find(n => n.id === edges[0].source);
        if (upstreamNode && upstreamNode.type === 'source') {
          const sourceData = sources?.data?.find((s: any) => s.id === upstreamNode.data.ref_id);
          if (sourceData && sourceData.sample) {
            try {
              const sample = JSON.parse(sourceData.sample);
              incomingPayload = sample;
              availableFields = getAllKeys(sample);
            } catch (e) {}
          }
        }
      }
    }

    // 3. Try to get sink schema from downstream sink
    const downstreamEdges = store.edges.filter(e => e.source === store.selectedNode?.id);
    if (downstreamEdges.length > 0) {
      const sinkNode = store.nodes.find(n => n.id === downstreamEdges[0].target);
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
  }, [store.selectedNode, store.edges, store.nodes, store.testResults, sources, sinks]);

  if (isLoading && !isNew) return <Box p="xl" ta="center"><Text>Loading...</Text></Box>;

  return (
    <WorkflowContext.Provider value={{ onPlusClick: handlePlusClick }}>
      <Box style={{ height: 'calc(100vh - 120px)', display: 'flex', flexDirection: 'column' }}>
        <EditorToolbar 
          id={id}
          isNew={isNew}
          onSave={() => saveMutation.mutate()}
          onTest={handleTest}
          onConfigureTest={() => store.setTestModalOpened(true)}
          onToggle={() => toggleMutation.mutate()}
          onClearTest={() => store.setTestResults(null)}
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
                onNodesChange={store.onNodesChange}
                onEdgesChange={store.onEdgesChange}
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
            <Paper withBorder radius="md" h={store.logsOpened ? 250 : 40} style={{ display: 'flex', flexDirection: 'column', transition: 'height 0.2s ease' }}>
               <Group justify="space-between" px="sm" h={40} style={{ borderBottom: store.logsOpened ? '1px solid var(--mantine-color-gray-2)' : 'none', cursor: 'pointer' }} onClick={() => store.setLogsOpened(!store.logsOpened)}>
                  <Group gap="xs">
                     {store.logsOpened ? <IconChevronDown size="1rem" /> : <IconChevronUp size="1rem" />}
                     <Text size="sm" fw={600}>Live Workflow Logs</Text>
                     {store.active && <Badge size="xs" color="green" variant="dot">Streaming</Badge>}
                  </Group>
                  <Group gap="xs">
                     <ActionIcon variant="subtle" size="sm" color="gray" onClick={(e) => { e.stopPropagation(); store.setLogs([]); }}>
                        <IconClearAll size="1rem" />
                     </ActionIcon>
                     <ActionIcon variant="subtle" size="sm" color={store.logsPaused ? 'orange' : 'gray'} onClick={(e) => { e.stopPropagation(); store.setLogsPaused(!store.logsPaused); }}>
                        {store.logsPaused ? <IconPlayerPlay size="1rem" /> : <IconPlayerPause size="1rem" />}
                     </ActionIcon>
                  </Group>
               </Group>
               {store.logsOpened && (
                  <ScrollArea style={{ flex: 1 }} p="xs" viewportRef={logScrollRef}>
                     <Stack gap={4}>
                        {store.logs.map((log, i) => (
                           <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
                              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap', fontFamily: 'monospace' }}>
                                 {new Date(log.timestamp).toLocaleTimeString()}
                              </Text>
                              <Badge size="xs" color={log.level === 'ERROR' ? 'red' : log.level === 'WARN' ? 'orange' : 'blue'} variant="light" style={{ minWidth: 50 }}>
                                 {log.level}
                              </Badge>
                              <Text size="xs" style={{ wordBreak: 'break-all', fontFamily: 'monospace' }}>
                                 {log.message}
                              </Text>
                           </Group>
                        ))}
                        {store.logs.length === 0 && (
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
            if (store.quickAddSource) {
              const sourceNode = store.nodes.find(n => n.id === store.quickAddSource!.nodeId);
              pos = sourceNode ? { x: sourceNode.position.x + 250, y: sourceNode.position.y } : { x: 100, y: 100 };
            } else {
              pos = project({ x: (bounds?.width || 400) / 2, y: (bounds?.height || 400) / 2 });
            }
            addNodeAtPosition(type, refId, label, subType, pos);
            if (store.quickAddSource) store.setDrawerOpened(false);
          }}
          sources={sources?.data || []}
          sinks={sinks?.data || []}
        />

        <Modals 
          onRunSimulation={(input) => testMutation.mutate(input)}
          isTesting={testMutation.isPending}
        />

        <Modal
          opened={store.settingsOpened}
          onClose={() => {
            store.setSettingsOpened(false);
            store.setSelectedNode(null);
          }}
          title={
            <Group gap="xs">
              <ThemeIcon variant="light" color="blue">
                <IconSettings size="1.2rem" />
              </ThemeIcon>
              <Text fw={700}>
                {store.selectedNode?.data?.ref_id === 'new' 
                  ? `Create New ${store.selectedNode?.type?.toUpperCase()}` 
                  : `Configure ${store.selectedNode?.type?.toUpperCase()} Node`}
              </Text>
            </Group>
          }
          fullScreen
          padding="xl"
        >
          <ScrollArea h="calc(100vh - 100px)" offsetScrollbars>
            <Stack gap="md" style={{ width: '100%' }}>
              <Box>
                  {store.selectedNode?.type === 'source' && (
                    <SourceForm 
                      embedded 
                      onSave={handleInlineSave} 
                      onRunSimulation={handleTest}
                      isEditing={store.selectedNode.data.ref_id !== 'new'} 
                      initialData={selectedNodeData} 
                      vhost={store.vhost}
                      workerID={store.workerID}
                    />
                  )}
                  {store.selectedNode?.type === 'sink' && (
                    <SinkForm 
                      embedded 
                      onSave={handleInlineSave} 
                      isEditing={store.selectedNode.data.ref_id !== 'new'} 
                      initialData={selectedNodeData} 
                      vhost={store.vhost}
                      workerID={store.workerID}
                    />
                  )}
                  {store.selectedNode && ['transformation', 'condition', 'switch', 'merge', 'stateful', 'note'].includes(store.selectedNode.type!) && (
                    <Stack gap="sm">
                       <TransformationForm
                         selectedNode={store.selectedNode}
                         updateNodeConfig={store.updateNodeConfig}
                         onRunSimulation={handleTest}
                         availableFields={availableFields}
                         incomingPayload={incomingPayload}
                         sources={sources?.data || []}
                         sinkSchema={sinkSchema}
                       />
                       <Group justify="flex-end" mt="md">
                         <Button variant="light" onClick={() => {
                           store.setSelectedNode(null);
                           store.setSettingsOpened(false);
                         }}>Done</Button>
                       </Group>
                    </Stack>
                  )}
              </Box>

              {(store.selectedNode?.type === 'source' || store.selectedNode?.type === 'sink') && (store.selectedNode?.data?.testResult || store.selectedNode?.data?.lastSample) && (
                <Paper withBorder p="md" bg="gray.0">
                  <Stack gap="xs">
                    <Text fw={700} size="sm">Data Output</Text>
                    <Code block style={{ fontSize: '10px' }}>
                      {JSON.stringify(store.selectedNode.data.testResult?.payload || store.selectedNode.data.lastSample, null, 2)}
                    </Code>
                  </Stack>
                </Paper>
              )}

              <Divider />
              <Button color="red" variant="light" leftSection={<IconTrash size="1rem" />} onClick={() => deleteNode(store.selectedNode!.id)}>
                Remove Node from Canvas
              </Button>
            </Stack>
          </ScrollArea>
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
