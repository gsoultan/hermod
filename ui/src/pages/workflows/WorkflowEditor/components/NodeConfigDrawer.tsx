import { Drawer, ScrollArea, Title, Group, ActionIcon, rem, Box, Text } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { SourceForm } from '../../../../components/forms/SourceForm';
import { SinkForm } from '../../../../components/forms/SinkForm';
import { TransformationForm } from '../../../../components/forms/TransformationForm';
import { useNodeContext } from '../hooks/useNodeContext';
import { IconX } from '@tabler/icons-react';

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
  const { incomingPayload, availableFields, sinkSchema, upstreamSource } = useNodeContext(
    selectedNode, 
    testResults, 
    sources, 
    sinks
  );

  if (!selectedNode) return null;

  const type = selectedNode.type;
  const isEditing = selectedNode.data?.ref_id !== 'new';
  
  const sourceData = type === 'source' ? (sources.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;
  const sinkData = type === 'sink' ? (sinks.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;

  const title = (() => {
    switch (type) {
      case 'source': return `Configure Source`;
      case 'sink': return `Configure Sink`;
      case 'transformation': return `Configure Transformation`;
      case 'validator': return `Configure Validator`;
      default: return 'Configure Node';
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
  const width = (() => {
    switch (type) {
      case 'transformation':
      case 'validator':
        return 'min(1180px, 78vw)';
      case 'source':
      case 'sink':
        return 'min(880px, 62vw)';
      default:
        return 'min(880px, 62vw)';
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
          />
        )}
      </ScrollArea>
    </Drawer>
  );
}

