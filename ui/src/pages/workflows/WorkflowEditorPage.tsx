import { 
  useMemo, useRef, useState 
} from 'react';
import { useShallow } from 'zustand/react/shallow';
import { 
  ReactFlowProvider,
  useReactFlow
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { 
  Paper,
  Box, Flex, Text
} from '@mantine/core';
import { useParams } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import type { Source, Sink } from '@/types';
import { apiFetch } from '@/api';
import { useVHost } from '@/context/VHostContext';
import { notifications } from '@mantine/notifications';

// Refactored Components & Hooks
import { useWorkflowStore } from './WorkflowEditor/store/useWorkflowStore';
import { EditorToolbar } from './WorkflowEditor/components/EditorToolbar';
import { FlowCanvas } from './WorkflowEditor/components/FlowCanvas';
import { LiveLogPanel } from './WorkflowEditor/components/LiveLogPanel';
import { SidebarDrawer } from './WorkflowEditor/components/SidebarDrawer';
import { NodeConfigModal } from './WorkflowEditor/components/NodeConfigModal';
import { WorkflowContext } from './WorkflowEditor/nodes/BaseNode';
import { useWorkflowLayout } from './WorkflowEditor/hooks/useWorkflowLayout';
import { useNodeContext } from './WorkflowEditor/hooks/useNodeContext';
import { useWorkflowInitialization } from './WorkflowEditor/hooks/useWorkflowInitialization';
import { useWorkflowWebSockets } from './WorkflowEditor/hooks/useWorkflowWebSockets';
import { useWorkflowMutations } from './WorkflowEditor/hooks/useWorkflowMutations';
import { useWorkflowEvents } from './WorkflowEditor/hooks/useWorkflowEvents';
import { useWorkflowHotkeys } from './WorkflowEditor/hooks/useWorkflowHotkeys';
import { WorkflowHeader } from './WorkflowEditor/components/WorkflowHeader';
import { WorkflowModals } from './WorkflowEditor/components/WorkflowModals';
import { WorkflowNodeSettingsModal } from './WorkflowEditor/components/WorkflowNodeSettingsModal';

const API_BASE = '/api';

function EditorInner() {
  const { id } = useParams({ strict: false }) as any;
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const { screenToFlowPosition, zoomIn, zoomOut, fitView: rfFitView } = useReactFlow();
  const { onLayout } = useWorkflowLayout();
  const { selectedVHost } = useVHost();

  const { 
    vhost, selectedNode, active, logsPaused, quickAddSource,
    testResults, name, workerID,
    workflowStatus, settingsOpened,
    updateNodeConfig, setTestModalOpened,
    setTraceInspectorOpened, setTraceMessageID,
    setQuickAddSource, setDrawerOpened, setDrawerTab,
    setTestResults, setSelectedNode, setSettingsOpened
  } = useWorkflowStore(useShallow(state => ({
    vhost: state.vhost,
    selectedNode: state.selectedNode,
    active: state.active,
    logsPaused: state.logsPaused,
    quickAddSource: state.quickAddSource,
    testResults: state.testResults,
    name: state.name,
    workerID: state.workerID,
    workflowStatus: state.workflowStatus,
    settingsOpened: state.settingsOpened,
    updateNodeConfig: state.updateNodeConfig,
    setTestModalOpened: state.setTestModalOpened,
    setTraceInspectorOpened: state.setTraceInspectorOpened,
    setTraceMessageID: state.setTraceMessageID,
    setQuickAddSource: state.setQuickAddSource,
    setDrawerOpened: state.setDrawerOpened,
    setDrawerTab: state.setDrawerTab,
    setTestResults: state.setTestResults,
    setSelectedNode: state.setSelectedNode,
    setSettingsOpened: state.setSettingsOpened,
  })));

  const [configModalOpen, setConfigModalOpen] = useState(false);
  const [aiFixModalData, setAIFixModalData] = useState<any>(null);
  const [saveConfirmOpened, setSaveConfirmOpened] = useState(false);

  // Custom Hooks
  const { isNew, isLoading } = useWorkflowInitialization(id, selectedVHost);

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

  const {
    testMutation, saveMutation, toggleMutation, rebuildMutation,
    handleTest, handleRefreshFields, handleSave, handleInlineSave
  } = useWorkflowMutations(id, isNew, sources?.data, setSaveConfirmOpened);

  const {
    onNodeClick, onEdgeClick, onDragStart, onDragOver, onDrop, addNodeAtPosition, deleteNode
  } = useWorkflowEvents(reactFlowWrapper, setConfigModalOpen);

  // Node operations
  const handlePlusClick = (nodeId: string, handleId: string | null) => {
    setQuickAddSource({ nodeId, handleId });
    setDrawerOpened(true);
  };

  useWorkflowHotkeys(handleSave, handleTest);

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
        <WorkflowHeader 
          id={id}
          isNew={isNew}
          name={name}
          active={active}
          workflowStatus={workflowStatus}
        />

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
          isRefreshing={testMutation.isPending}
          vhost={vhost}
          workerID={workerID}
          availableFields={availableFields}
          incomingPayload={incomingPayload}
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


