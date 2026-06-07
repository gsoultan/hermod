import { Stack, TextInput, NumberInput, Alert } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface JoinConfigProps {
  config: any;
  nodeId: string;
  updateNodeConfig: (nodeId: string, config: any) => void;
  availableFields: string[];
}

export function JoinConfig({ config, nodeId, updateNodeConfig }: JoinConfigProps) {
  const data = config || {};

  return (
    <Stack gap="xs">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" title="Stateful Join">
        Waits for multiple messages with the same key and merges them.
      </Alert>
      <TextInput
        label="Join Key Path"
        placeholder="e.g. order_id"
        value={data.key_path || ''}
        onChange={(e) => updateNodeConfig(nodeId, { key_path: e.currentTarget.value })}
        required
        description="JSON path to extract the join key from messages."
      />
      <NumberInput
        label="Expected Sources"
        value={data.expected_sources || 2}
        onChange={(val) => updateNodeConfig(nodeId, { expected_sources: val })}
        min={2}
        description="Number of unique messages to wait for before joining."
      />
    </Stack>
  );
}
