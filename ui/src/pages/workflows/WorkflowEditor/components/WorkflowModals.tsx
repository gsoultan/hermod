import { Modal, Group, Text, Stack, Paper, Button } from '@mantine/core';
import { IconDeviceFloppy, IconRefresh } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { Modals } from './Modals';
import { SchemaRegistryModal } from '@/components/modals/SchemaRegistryModal';
import { WorkflowHistoryModal } from '@/components/modals/WorkflowHistoryModal';
import { LiveStreamInspector } from './LiveStreamInspector';
import { AIGeneratorModal } from './AIGeneratorModal';
import { AIFixModal } from './AIFixModal';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface WorkflowModalsProps {
  id: string;
  testMutation: any;
  saveMutation: any;
  aiFixModalData: any;
  setAIFixModalData: (data: any) => void;
  saveConfirmOpened: boolean;
  setSaveConfirmOpened: (opened: boolean) => void;
}

export function WorkflowModals({
  id,
  testMutation,
  saveMutation,
  aiFixModalData,
  setAIFixModalData,
  saveConfirmOpened,
  setSaveConfirmOpened
}: WorkflowModalsProps) {
  const { 
    schemaRegistryOpened, setSchemaRegistryOpened,
    historyOpened, setHistoryOpened,
    liveStreamOpened, setLiveStreamOpened,
    aiGeneratorOpened, setAIGeneratorOpened,
    setNodes, setEdges, setName
  } = useWorkflowStore();

  return (
    <>
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
    </>
  );
}
