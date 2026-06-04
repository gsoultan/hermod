import { TextInput, Stack, Alert, Text } from '@mantine/core';
import { IconHistory, IconInfoCircle } from '@tabler/icons-react';

interface WaitConfigProps {
  config: any;
  updateNodeConfig: (nodeId: string, config: any) => void;
  nodeId: string;
}

export function WaitConfig({ config, updateNodeConfig, nodeId }: WaitConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        <Text size="sm">
          Pause the workflow for a specific duration. 
          Durations longer than 30 seconds will automatically suspend the message to the database, 
          freeing up worker resources.
        </Text>
      </Alert>
      <TextInput
        label="Duration"
        placeholder="e.g. 10s, 5m, 2h, 1d"
        description="Go duration format"
        value={config.duration || ''}
        onChange={(e) => updateNodeConfig(nodeId, { duration: e.currentTarget.value })}
        leftSection={<IconHistory size="1rem" />}
        required
      />
    </Stack>
  );
}
