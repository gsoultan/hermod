import {
  Stack,
  Alert,
  Text,
  Select,
  TextInput,
  NumberInput,
  Card,
  Group,
  rem,
  Divider,
} from '@mantine/core';
import {
  IconInfoCircle,
  IconMathFunction,
  IconClock,
  IconTag,
  IconSettings,
} from '@tabler/icons-react';

interface AggregateConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function AggregateConfig({ config, updateNodeConfig, nodeId }: AggregateConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="pink"
        variant="light"
        radius="md"
        title="Aggregation"
      >
        <Text size="sm">
          Group messages by a key and perform aggregations like count, sum, or average over a
          rolling window.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <IconSettings size={rem(18)} className="text-pink-500" />
            <Text size="sm" fw={600}>
              Aggregation Settings
            </Text>
          </Group>

          <TextInput
            label="Group By Key"
            placeholder="e.g. user_id, region"
            value={config.groupBy || ''}
            onChange={(e) => updateNodeConfig(nodeId, { groupBy: e.currentTarget.value })}
            description="Field path to use as the grouping key."
            size="sm"
            leftSection={<IconTag size={rem(16)} />}
          />

          <Divider variant="dashed" />

          <Group grow align="flex-start">
            <Select
              label="Aggregation Type"
              data={[
                { label: 'Count', value: 'count' },
                { label: 'Sum', value: 'sum' },
                { label: 'Average', value: 'avg' },
                { label: 'Minimum', value: 'min' },
                { label: 'Maximum', value: 'max' },
              ]}
              value={config.aggType || 'count'}
              onChange={(val) => updateNodeConfig(nodeId, { aggType: val })}
              size="sm"
              leftSection={<IconMathFunction size={rem(16)} />}
            />

            <NumberInput
              label="Window Size (seconds)"
              min={1}
              value={config.windowSize || 60}
              onChange={(val) => updateNodeConfig(nodeId, { windowSize: val })}
              size="sm"
              leftSection={<IconClock size={rem(16)} />}
              description="Time window for aggregation."
            />
          </Group>

          {config.aggType !== 'count' && (
            <TextInput
              label="Field to Aggregate"
              placeholder="e.g. amount"
              value={config.field || ''}
              onChange={(e) => updateNodeConfig(nodeId, { field: e.currentTarget.value })}
              required
              size="sm"
              description="The numeric field to perform aggregation on."
            />
          )}
        </Stack>
      </Card>
    </Stack>
  );
}
