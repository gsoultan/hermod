import { Stack, TextInput, Alert, Text } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface DeduplicateConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function DeduplicateConfig({ config, updateNodeConfig, nodeId }: DeduplicateConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        <Text size="sm">
          High-speed in-memory deduplication using Bloom Filters. 
          Use this to skip duplicate messages within a rolling window.
        </Text>
      </Alert>
      <TextInput
        label="Key Path"
        placeholder="id"
        description="Data path to the field used for deduplication (e.g. 'order.id')."
        value={config.keyPath || ''}
        onChange={(e) => updateNodeConfig(nodeId, { keyPath: e.currentTarget.value })}
      />
    </Stack>
  );
}
