import { 
  Alert, Button, Group, JsonInput, Modal, Stack, Text 
} from '@mantine/core';
import { IconBraces, IconInfoCircle, IconPlayerPlay } from '@tabler/icons-react';
import { useWorkflowStore } from '../store/useWorkflowStore';

interface ModalsProps {
  onRunSimulation: (input?: any) => void;
  isTesting: boolean;
}

export function Modals({ onRunSimulation, isTesting }: ModalsProps) {
  const store = useWorkflowStore();

  return (
    <Modal
      opened={store.testModalOpened}
      onClose={() => store.setTestModalOpened(false)}
      title={
        <Group gap="xs">
          <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
          <Text fw={700}>Run Flow Simulation</Text>
        </Group>
      }
      size="lg"
    >
      <Stack gap="md">
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
          Simulation allows you to test your workflow logic without actually sending data to sinks.
          Provide a sample JSON payload to see how it moves through the nodes.
        </Alert>
        
        <JsonInput
          label="Input Message (JSON)"
          placeholder='{"id": 1, "status": "active"}'
          validationError="Invalid JSON"
          formatOnBlur
          autosize
          minRows={8}
          maxRows={15}
          value={store.testInput}
          onChange={store.setTestInput}
        />

        <Group justify="flex-end" mt="md">
          <Button variant="light" onClick={() => store.setTestModalOpened(false)}>Cancel</Button>
          <Button 
            leftSection={<IconPlayerPlay size="1rem" />} 
            onClick={() => onRunSimulation()}
            loading={isTesting}
          >
            Run Simulation
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
