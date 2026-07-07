import {
  Stack,
  Alert,
  Text,
  Select,
  NumberInput,
  Card,
  Group,
  rem,
  Divider,
  Autocomplete,
} from '@mantine/core';
import { useMemo } from 'react';
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
  availableFields: any[];
}

export function AggregateConfig({ config, updateNodeConfig, nodeId, availableFields = [] }: AggregateConfigProps) {
  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

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

          <Autocomplete
            label="Group By Key"
            placeholder="e.g. user_id, region"
            data={fieldPaths}
            value={config.groupBy || ''}
            onChange={(val) => updateNodeConfig(nodeId, { groupBy: val })}
            description="Field or expression to use as the grouping key (e.g. lower(source.region))."
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
            <Autocomplete
              label="Field to Aggregate"
              placeholder="e.g. amount"
              data={fieldPaths}
              value={config.field || ''}
              onChange={(val) => updateNodeConfig(nodeId, { field: val })}
              required
              size="sm"
              description="Numeric field or expression to aggregate (e.g. toint(source.amount))."
            />
          )}
        </Stack>
      </Card>
    </Stack>
  );
}
