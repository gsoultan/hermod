import { Modal, Group, ThemeIcon, Text, Box, Title, ScrollArea, Stack, Button, Paper, Code, Divider } from '@mantine/core';
import { IconSettings, IconTrash } from '@tabler/icons-react';
import { SourceForm } from '@/components/forms/SourceForm';
import { SinkForm } from '@/components/forms/SinkForm';
import { TransformationForm } from '@/components/forms/TransformationForm';
import type { Node } from '@xyflow/react';
import type { Source, Sink } from '@/types';

interface WorkflowNodeSettingsModalProps {
  opened: boolean;
  onClose: () => void;
  selectedNode: Node | null;
  selectedNodeData: any;
  handleInlineSave: (data: any) => void;
  handleTest: (input?: any) => void;
  handleRefreshFields: () => Promise<void>;
  isRefreshing: boolean;
  vhost: string;
  workerID: string;
  availableFields: any[];
  incomingPayload: any;
  sinks: Sink[];
  upstreamSource: any;
  setSettingsOpened: (opened: boolean) => void;
  updateNodeConfig: (id: string, config: any) => void;
  deleteNode: (id: string) => void;
  sinkSchema: any;
}

export function WorkflowNodeSettingsModal({
  opened, onClose, selectedNode, selectedNodeData,
  handleInlineSave, handleTest, handleRefreshFields, isRefreshing,
  vhost, workerID, availableFields, incomingPayload, sinks, upstreamSource,
  setSettingsOpened, updateNodeConfig, deleteNode, sinkSchema
}: WorkflowNodeSettingsModalProps) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
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
                isRefreshing={isRefreshing}
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
                sinks={sinks}
                upstreamSource={upstreamSource}
                onRefreshFields={handleRefreshFields}
                isRefreshing={isRefreshing}
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
                  sources={sinks} 
                  sinkSchema={sinkSchema}
                  onRefreshFields={handleRefreshFields}
                  isRefreshing={isRefreshing}
                />
                <Group justify="flex-end" mt="md">
                   <Button variant="light" onClick={() => {
                     setSettingsOpened(false);
                   }}>Done</Button>
                </Group>
              </Stack>
            )}
          </Box>

          {(selectedNode?.type === 'source' || selectedNode?.type === 'sink') && (selectedNode?.data?.testResult || selectedNode?.data?.lastSample) ? (
            <Paper withBorder p="md" bg="var(--mantine-color-body)">
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
  );
}
