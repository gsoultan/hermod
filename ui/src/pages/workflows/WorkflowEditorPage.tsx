import { 
  useEffect, useMemo, useRef, useState 
} from 'react';
import { useShallow } from 'zustand/react/shallow';
import { 
  ReactFlowProvider,
  useReactFlow
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { 
  Paper,
  Box, Button, Flex, Tabs, Text
} from '@mantine/core';
import { Spotlight, spotlight } from '@mantine/spotlight';
import '@mantine/spotlight/styles.css';
import { NODE_CATEGORIES, paletteItemData } from './WorkflowEditor/constants/nodeCategories';
import { IconPlus, IconSearch } from '@tabler/icons-react';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import type { Source, Sink } from '@/types';
import { apiFetch } from '@/api';
import { useVHost } from '@/context/VHostContext';
import { notifications } from '@mantine/notifications';

// Refactored Components & Hooks
import { useWorkflowStore } from './WorkflowEditor/store/useWorkflowStore';
import { EditorToolbar } from './WorkflowEditor/components/EditorToolbar';
import { FLOW_TABS } from './flowTabs';
import { AddStepPicker } from './WorkflowEditor/picker/AddStepPicker';
import type { StepItem } from './WorkflowEditor/picker/stepChoices';
import { useFlowActions } from './useFlowActions';
import { FlowCanvas } from './WorkflowEditor/components/FlowCanvas';
import { LiveLogPanel } from './WorkflowEditor/components/LiveLogPanel';
import { SidebarDrawer } from './WorkflowEditor/components/SidebarDrawer';
import { NodeConfigDrawer } from './WorkflowEditor/components/NodeConfigDrawer';
import { WorkflowContext } from './WorkflowEditor/nodes/BaseNode';
import { useWorkflowLayout } from './WorkflowEditor/hooks/useWorkflowLayout';
import { useNodeContext } from './WorkflowEditor/hooks/useNodeContext';
import { useAutoSampleUpstream } from './WorkflowEditor/hooks/useAutoSampleUpstream';
import { useWorkflowInitialization } from './WorkflowEditor/hooks/useWorkflowInitialization';
import { useWorkflowWebSockets } from './WorkflowEditor/hooks/useWorkflowWebSockets';
import { useEdgeBreakpoints } from './WorkflowEditor/hooks/useEdgeBreakpoints';
import { useWorkflowMutations } from './WorkflowEditor/hooks/useWorkflowMutations';
import { useWorkflowEvents } from './WorkflowEditor/hooks/useWorkflowEvents';
import { useWorkflowHotkeys } from './WorkflowEditor/hooks/useWorkflowHotkeys';
import { WorkflowModals } from './WorkflowEditor/components/WorkflowModals';
import { WorkflowNodeSettingsModal } from './WorkflowEditor/components/WorkflowNodeSettingsModal';
import { UnsavedChangesGuard } from './WorkflowEditor/components/UnsavedChangesGuard';
import { deadLetterPageFor, wantsDeadLetterQueue } from './WorkflowEditor/utils/deadLetterLink';
import { BOTTOM_TABS_HEIGHT } from '@/components/layout/nav';

const API_BASE = '/api';

function EditorInner() {
  const { id } = useParams({ strict: false }) as any;
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const { screenToFlowPosition, zoomIn, zoomOut, fitView: rfFitView } = useReactFlow();
  const { onLayout } = useWorkflowLayout();
  const { selectedVHost } = useVHost();

  const { 
    vhost, selectedNode, active, logsPaused,
    testResults, workerID,
    workflowStatus, settingsOpened,
    updateNodeConfig, setTestModalOpened,
    setTraceInspectorOpened, setTraceMessageID,
    setQuickAddSource, setDrawerOpened, setDrawerTab,
    setTestResults, setSelectedNode, setSettingsOpened, name
  } = useWorkflowStore(useShallow(state => ({
    name: state.name,
    setDrawerTab: state.setDrawerTab,
    vhost: state.vhost,
    selectedNode: state.selectedNode,
    active: state.active,
    logsPaused: state.logsPaused,
    testResults: state.testResults,
    workerID: state.workerID,
    workflowStatus: state.workflowStatus,
    settingsOpened: state.settingsOpened,
    updateNodeConfig: state.updateNodeConfig,
    setTestModalOpened: state.setTestModalOpened,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setTraceMessageID: state.setTraceMessageID,
    setQuickAddSource: state.setQuickAddSource,
    setDrawerOpened: state.setDrawerOpened,
    setTestResults: state.setTestResults,
    setSelectedNode: state.setSelectedNode,
    setSettingsOpened: state.setSettingsOpened,
  })));

  const navigate = useNavigate();
  const flowActions = useFlowActions(id, name);
  const [pickerOpened, setPickerOpened] = useState(false);
  const hasSource = useWorkflowStore((s) => s.nodes.some((n: any) => n.type === 'source'));
  const { data: installedPlugins } = useQuery<any[]>({
    queryKey: ['marketplace', 'plugins', 'installed'],
    queryFn: async () => {
      const res = await apiFetch('/api/marketplace/plugins');
      const all = await res.json();
      return (Array.isArray(all) ? all : []).filter((p: any) => p.installed);
    },
  });
  const [configModalOpen, setConfigModalOpen] = useState(false);
  const [aiFixModalData, setAIFixModalData] = useState<any>(null);
  const [saveConfirmOpened, setSaveConfirmOpened] = useState(false);

  // Custom Hooks
  const { isNew, isLoading } = useWorkflowInitialization(id, selectedVHost);

  // Dead-letter mail from an earlier release links here with ?dlq=open. The
  // workflow's dead letters are on the Dead Letters page now: send it there.
  useEffect(() => {
    if (id && wantsDeadLetterQueue(window.location.search)) {
      window.location.replace(deadLetterPageFor(id));
    }
  }, [id]);

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

  useWorkflowWebSockets(id, active, logsPaused);
  useEdgeBreakpoints(id);

  const {
    testMutation, saveMutation, toggleMutation, rebuildMutation,
    handleTest, captureSample, handleRefreshFields, isRefreshing, handleSave, handleInlineSave
  } = useWorkflowMutations(id, isNew, sources?.data, setSaveConfirmOpened);

  const {
    onNodeClick, onEdgeClick, onDragStart, onDragOver, onDrop, addNodeAtPosition, deleteNode
  } = useWorkflowEvents(reactFlowWrapper, setConfigModalOpen);

  // Node operations
  // The + on a node and the Add step button both open the picker; the + also
  // remembers where the new step goes.
  const handlePlusClick = (nodeId: string, handleId: string | null) => {
    setQuickAddSource({ nodeId, handleId });
    setPickerOpened(true);
  };

  const addStep = (type: string, refId: string, label: string, subType: string, extraData?: any) => {
    const bounds = reactFlowWrapper.current?.getBoundingClientRect();
    const { nodes, quickAddSource: from } = useWorkflowStore.getState();
    let pos;
    if (from) {
      const sourceNode = nodes.find(n => n.id === from.nodeId);
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
      // Logic/flow nodes (switch, router, condition, merge, ...)
      // open the node settings modal for configuration.
      setDrawerOpened(false);
      setSettingsOpened(true);
    }
  };

  const openPalette = () => {
    setQuickAddSource(null);
    setDrawerTab(hasSource ? 'transformations' : 'sources');
    setDrawerOpened(true);
  };

  useWorkflowHotkeys(handleSave, handleTest);

  const { incomingPayload, availableFields, sinkSchema, upstreamSource, inputSkipped } = useNodeContext(
    selectedNode,
    testResults,
    sources?.data || [],
    sinks?.data || []
  );

  // A node opened with an empty field list means the upstream source has never
  // been sampled. Pull one now rather than leaving the operator to discover the
  // refresh icon the empty-state alert points at.
  const { graphNodes, graphEdges } = useWorkflowStore(useShallow(state => ({
    graphNodes: state.nodes,
    graphEdges: state.edges,
  })));

  useAutoSampleUpstream({
    enabled: settingsOpened || configModalOpen,
    selectedNode,
    nodes: graphNodes,
    edges: graphEdges,
    sources: sources?.data,
    fieldCount: availableFields?.length || 0,
    capture: captureSample,
  });

  const spotlightActions = useMemo(() => {
    const actions: any[] = [];
    NODE_CATEGORIES.forEach(cat => {
      cat.items.forEach(item => {
        actions.push({
          id: `${item.type}-${item.subType}-${item.label}`,
          label: item.label,
          description: item.description,
          leftSection: <item.icon size="1.2rem" color={`var(--mantine-color-${item.color}-6)`} />,
          onClick: () => {
            const bounds = reactFlowWrapper.current?.getBoundingClientRect();
            const pos = screenToFlowPosition({ 
              x: (bounds?.width || 400) / 2, 
              y: (bounds?.height || 400) / 2 
            });
            const node = addNodeAtPosition(item.type, item.refId, item.label, item.subType, pos, paletteItemData(item));
            setSelectedNode(node);
            if (item.type === 'source' || item.type === 'sink' || item.type === 'transformation' || item.type === 'validator') {
              setConfigModalOpen(true);
            } else {
              setDrawerOpened(false);
              setSettingsOpened(true);
            }
          }
        });
      });
    });
    return actions;
  }, [addNodeAtPosition, screenToFlowPosition, setConfigModalOpen, setDrawerOpened, setSelectedNode, setSettingsOpened]);

  if (isLoading && !isNew) return <Box p="xl" ta="center"><Text>Loading...</Text></Box>;

  return (
    <WorkflowContext.Provider value={{ onPlusClick: handlePlusClick }}>
      <Spotlight
        actions={spotlightActions}
        shortcut="mod + shift + K"
        nothingFound="No components found..."
        highlightQuery
        searchProps={{
          leftSection: <IconSearch size="1.2rem" />,
          placeholder: 'Search components to add...',
        }}
      />
      {/* The screen less whatever the shell puts above and below the page.
          Above is Mantine's header offset, which is 0 now that the shell has no
          header; this used to read the header height with a 60px fallback,
          and that fallback left a blank strip under the live log panel once
          the header went. Below, on a phone, are the bottom tabs.
          Viewport-relative on purpose: a percentage height would need every
          ancestor to have a definite height, and ErrorBoundary sits in that
          chain without one, which collapses the canvas to zero. */}
      <Box
        h={{
          base: `calc(100dvh - var(--app-shell-header-offset, 0px) - ${BOTTOM_TABS_HEIGHT})`,
          sm: 'calc(100dvh - var(--app-shell-header-offset, 0px))',
        }}
        style={{
          minHeight: 0,
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        <EditorToolbar 
          id={id}
          isNew={isNew}
          workflowStatus={workflowStatus}
          onSave={handleSave}
          onTest={() => handleTest(null)}
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
          onExport={flowActions.exportJson}
          onClone={flowActions.clone}
          onDelete={flowActions.remove}
          onAutoLayout={() => {
            try {
              onLayout('LR');
              notifications.show({ message: 'Auto-layout applied', color: 'blue' });
            } catch (e: any) {
              notifications.show({ message: e?.message || 'Failed to layout', color: 'red' });
            }
          }}
          onSearch={() => spotlight.open()}
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

        {/* The same seven tabs as the read-only view. Build is this page; the others open the view on that tab. */}
        {!isNew && (
          <Tabs
            value="build"
            mb="sm"
            onChange={(t) => { if (t && t !== 'build') navigate({ to: '/workflows/$id', params: { id }, search: { tab: t } } as any); }}
          >
            <Tabs.List>
              {FLOW_TABS.map((t) => <Tabs.Tab key={t.value} value={t.value}>{t.label}</Tabs.Tab>)}
            </Tabs.List>
          </Tabs>
        )}

        {/* position: relative makes this the containing block for the
            SidebarDrawer, which floats over the canvas instead of consuming
            400px of it as an inline sibling. */}
        <Flex style={{ flex: 1, overflow: 'hidden', position: 'relative' }} gap="md">
          <Box style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', gap: 'var(--mantine-spacing-md)' }}>
            <Paper withBorder radius="md" style={{ flex: 1, position: 'relative' }} ref={reactFlowWrapper}>
              {/* The one way in to the step palette, on the canvas where the steps go. */}
              <Button
                leftSection={<IconPlus size="1rem" />}
                size="sm"
                style={{ position: 'absolute', top: 12, right: 12, zIndex: 5 }}
                onClick={() => { setQuickAddSource(null); setPickerOpened(true); }}
              >
                Add step
              </Button>
              <FlowCanvas 
                onNodeClick={onNodeClick}
                onEdgeClick={onEdgeClick}
                onDragOver={onDragOver}
                onDrop={onDrop}
              />
            </Paper>

            {/* Node Config Drawer (enhanced right-side configuration) */}
            <NodeConfigDrawer 
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
              isRefreshing={isRefreshing || testMutation.isPending}
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
            // The drawer passes the item's icon and colour before its extra
            // data; taking extraData fifth spread the icon component into the
            // new node's data and dropped a plugin's id.
            onAddItem={(type, refId, label, subType, _icon, _color, extraData) => addStep(type, refId, label, subType, extraData)}
            sources={sources?.data || []}
            sinks={sinks?.data || []}
          />
        </Flex>

        <AddStepPicker
          opened={pickerOpened}
          onClose={() => setPickerOpened(false)}
          onPick={(item: StepItem) => addStep(item.type, item.refId, item.label, item.subType, item.pluginID || item.defaults ? { ...(item.pluginID ? { pluginID: item.pluginID } : {}), ...(item.defaults ?? {}) } : undefined)}
          onOpenPalette={openPalette}
          hasSource={hasSource}
          sources={sources?.data || []}
          sinks={sinks?.data || []}
          plugins={installedPlugins || []}
        />

        <WorkflowNodeSettingsModal 
          opened={settingsOpened}
          onClose={() => {
            setSettingsOpened(false);
            setSelectedNode(null);
          }}
          selectedNode={selectedNode}
          selectedNodeData={selectedNodeData}
          handleInlineSave={handleInlineSave}
          handleTest={handleTest}
          handleRefreshFields={handleRefreshFields}
          isRefreshing={isRefreshing || testMutation.isPending}
          vhost={vhost}
          workerID={workerID}
          availableFields={availableFields}
          incomingPayload={incomingPayload}
          inputSkipped={inputSkipped}
          sources={sources?.data || []}
          sinks={sinks?.data || []}
          upstreamSource={upstreamSource}
          setSettingsOpened={setSettingsOpened}
          updateNodeConfig={updateNodeConfig}
          deleteNode={deleteNode}
          sinkSchema={sinkSchema}
        />

        <WorkflowModals 
          id={id}
          testMutation={testMutation}
          saveMutation={saveMutation}
          aiFixModalData={aiFixModalData}
          setAIFixModalData={setAIFixModalData}
          saveConfirmOpened={saveConfirmOpened}
          setSaveConfirmOpened={setSaveConfirmOpened}
        />

        <UnsavedChangesGuard
          onSave={() => saveMutation.mutateAsync({ leaving: true })}
          restartsOnSave={!isNew && active}
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


