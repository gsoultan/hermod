import { Drawer, ScrollArea, Title, Group, ActionIcon, rem, Box, Text } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { SourceForm } from '../../../../components/forms/SourceForm';
import { SinkForm } from '../../../../components/forms/SinkForm';
import { TransformationForm } from '../../../../components/forms/TransformationForm';
import { useNodeContext } from '../hooks/useNodeContext';
import { IconX } from '@tabler/icons-react';
import { CommitTogetherSection } from '../../../../components/workflow/Sink/CommitTogetherSection';
import { useWorkflowStore } from '../store/useWorkflowStore';

type NodeConfigDrawerProps = {
  opened: boolean;
  onClose: () => void;
  selectedNode: any | null;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
  testResults?: any[] | null;
  sources?: any[];
  sinks?: any[];
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
};

export function NodeConfigDrawer({ 
  opened, onClose, selectedNode, updateNodeConfig, onSave, vhost, workerID, 
  testResults = null, sources = [], sinks = [],
  onRefreshFields, isRefreshing
}: NodeConfigDrawerProps) {
  const { incomingPayload, availableFields, sinkSchema, upstreamSource, inputSkipped } = useNodeContext(
    selectedNode, 
    testResults, 
    sources, 
    sinks
  );
  // The other sink nodes, for "Commit together": who else is in the transaction.
  const graphNodes = useWorkflowStore(state => state.nodes);

  if (!selectedNode) return null;

  const type = selectedNode.type;
  const isEditing = selectedNode.data?.ref_id !== 'new';
  
  const sourceData = type === 'source' ? (sources.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;
  const sinkData = type === 'sink' ? (sinks.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;

  const title = (() => {
    switch (type) {
      case 'source': return 'Set up the source';
      case 'sink': return 'Set up the sink';
      case 'transformation': return 'Set up the step';
      case 'validator': return 'Set up the validator';
      default: return 'Set up the step';
    }
  })();

  const handleSave = (cfg: any) => {
    if (!selectedNode) return;
    // A save with no payload is not a save. Cancel is delivered through
    // onCancel now, but guard here too so a stray null can never be written
    // into the node config or reported to the user as a successful save.
    if (cfg == null) {
      onClose();
      return;
    }
    if (onSave) {
      onSave(cfg);
    } else {
      updateNodeConfig(selectedNode.id, cfg);
    }
    notifications.show({ message: 'Configuration saved.', color: 'green' });
    onClose();
  };

  // size="xl" is a flat 780px on every screen, which left a transformation —
  // script editor, field mapper and live preview stacked in one column — with
  // less room than the source form that only asks for a name. Scale with the
  // viewport instead, and give the denser node types more of it. The min()
  // keeps a laptop honest; the ch/px floor keeps a large monitor from stretching
  // form rows to an unreadable width.
  //
  // The share of the viewport also needs a floor. 62vw of a 390px phone is
  // 242px, which left a source's Name input at 110px and cut every placeholder
  // in half. clamp() keeps the share on wide screens and gives a narrow one
  // the whole width: below the floor the drawer is simply 100vw.
  const width = (() => {
    switch (type) {
      case 'transformation':
      case 'validator':
        return 'clamp(min(100vw, 720px), 78vw, 1180px)';
      case 'source':
      case 'sink':
      default:
        return 'clamp(min(100vw, 560px), 62vw, 880px)';
    }
  })();

  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      size={width}
      withCloseButton={false}
      styles={{
        header: { display: 'none' },
        body: { padding: 0 }
      }}
    >
      <Box p="md" style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}>
        <Group justify="space-between">
          <Group gap="xs">
            <Title order={4}>{title}</Title>
            <Text size="sm" c="dimmed">{selectedNode.data?.label}</Text>
          </Group>
          <ActionIcon aria-label="Close" variant="subtle" color="gray" onClick={onClose}>
            <IconX style={{ width: rem(18), height: rem(18) }} />
          </ActionIcon>
        </Group>
      </Box>

      <ScrollArea h="calc(100vh - 70px)" p="md" offsetScrollbars>
        {type === 'source' && (
          <SourceForm
            initialData={sourceData}
            onSave={handleSave}
            onCancel={onClose}
            vhost={vhost}
            workerID={workerID}
            embedded
            isEditing={isEditing}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
            onRunSimulation={onRefreshFields}
          />
        )}
        {type === 'sink' && (
          <CommitTogetherSection
            nodeId={selectedNode.id}
            data={selectedNode.data || {}}
            nodes={graphNodes as any}
            updateNodeConfig={updateNodeConfig}
          />
        )}
        {type === 'sink' && (
          <SinkForm
            initialData={sinkData}
            onSave={handleSave}
            onCancel={onClose}
            vhost={vhost}
            workerID={workerID}
            availableFields={availableFields}
            incomingPayload={incomingPayload}
            embedded
            isEditing={isEditing}
            sinks={sinks}
            upstreamSource={upstreamSource}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
          />
        )}
        {(type === 'transformation' || type === 'validator') && (
          <TransformationForm 
            selectedNode={selectedNode} 
            updateNodeConfig={updateNodeConfig} 
            availableFields={availableFields} 
            incomingPayload={incomingPayload}
            sources={sources}
            sinkSchema={sinkSchema}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
            inputSkipped={inputSkipped}
          />
        )}
      </ScrollArea>
    </Drawer>
  );
}

