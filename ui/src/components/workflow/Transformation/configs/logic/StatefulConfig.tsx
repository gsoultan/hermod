import { Select, TextInput, Stack, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface StatefulConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function StatefulConfig({ config, updateNodeConfig, nodeId }: StatefulConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="pink">
        <Text size="sm">Maintain running totals or counts across messages. State is persisted and shared across workers.</Text>
      </Alert>
      <Select
        label="Operation"
        data={[
          { label: 'Increment Count', value: 'count' },
          { label: 'Sum Field', value: 'sum' }
        ]}
        value={config.operation || 'count'}
        onChange={(val) => updateNodeConfig(nodeId, { operation: val })}
      />
      {config.operation === 'sum' && (
        <TextInput
          label="Field to Sum"
          placeholder="e.g. amount, price"
          value={config.field || ''}
          onChange={(e) => updateNodeConfig(nodeId, { field: e.currentTarget.value })}
          required
        />
      )}
      <TextInput
        label="Output Field"
        placeholder="e.g. total_sum, message_count"
        description="Where to store the current state value on the message"
        value={config.outputField || ''}
        onChange={(e) => updateNodeConfig(nodeId, { outputField: e.currentTarget.value })}
        required
      />
    </Stack>
  );
}
