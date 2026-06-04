import { Stack, Alert, Text, Select, TextInput, NumberInput } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';

interface AggregateConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function AggregateConfig({ config, updateNodeConfig, nodeId }: AggregateConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="pink">
        <Text size="sm">Group messages by a key and perform aggregations like count, sum, or average over a window.</Text>
      </Alert>
      <TextInput
        label="Group By Key"
        placeholder="e.g. user_id, region"
        value={config.groupBy || ''}
        onChange={(e) => updateNodeConfig(nodeId, { groupBy: e.currentTarget.value })}
      />
      <Select
        label="Aggregation Type"
        data={['count', 'sum', 'avg', 'min', 'max']}
        value={config.aggType || 'count'}
        onChange={(val) => updateNodeConfig(nodeId, { aggType: val })}
      />
      {config.aggType !== 'count' && (
        <TextInput
          label="Field to Aggregate"
          placeholder="e.g. amount"
          value={config.field || ''}
          onChange={(e) => updateNodeConfig(nodeId, { field: e.currentTarget.value })}
          required
        />
      )}
      <NumberInput
        label="Window Size (seconds)"
        min={1}
        value={config.windowSize || 60}
        onChange={(val) => updateNodeConfig(nodeId, { windowSize: val })}
      />
    </Stack>
  );
}
