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
  Switch,
  TextInput,
  Tabs,
} from '@mantine/core';
import { useMemo } from 'react';
import {
  IconInfoCircle,
  IconMathFunction,
  IconClock,
  IconTag,
  IconSettings,
  IconLayoutGrid,
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

  const windowSize = config.window ? parseInt(config.window.replace('s', '')) || 60 : 60;

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

      <Tabs defaultValue="settings" variant="pills" radius="md">
        <Tabs.List mb="md">
          <Tabs.Tab value="settings" leftSection={<IconSettings size={rem(16)} />}>
            Settings
          </Tabs.Tab>
          <Tabs.Tab value="output" leftSection={<IconLayoutGrid size={rem(16)} />}>
            Output
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="settings">
          <Stack gap="md">
            <Card withBorder radius="md" p="md">
              <Stack gap="md">
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
                    value={config.type || 'count'}
                    onChange={(val) => updateNodeConfig(nodeId, { type: val })}
                    size="sm"
                    leftSection={<IconMathFunction size={rem(16)} />}
                  />

                  <NumberInput
                    label="Window Size (seconds)"
                    min={1}
                    value={windowSize}
                    onChange={(val) => updateNodeConfig(nodeId, { window: `${val}s` })}
                    size="sm"
                    leftSection={<IconClock size={rem(16)} />}
                    description="Time window for aggregation."
                  />
                </Group>

                {config.type !== 'count' && (
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

            <Card withBorder radius="md" p="md">
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="sm" fw={600}>Advanced Strategy</Text>
                  <Switch
                    label="Persistent State"
                    checked={config.persistent === true || config.persistent === 'true'}
                    onChange={(e) => updateNodeConfig(nodeId, { persistent: e.currentTarget.checked })}
                    size="xs"
                  />
                </Group>
                
                <Select
                  label="Window Type"
                  data={[
                    { label: 'Session (Reset on inactivity)', value: 'session' },
                    { label: 'Tumbling (Fixed intervals)', value: 'tumbling' },
                    { label: 'Sliding (Continuous update)', value: 'sliding' },
                  ]}
                  value={config.windowType || 'session'}
                  onChange={(val) => updateNodeConfig(nodeId, { windowType: val })}
                  size="sm"
                  description="How the aggregation window moves over time."
                />

                {config.windowType === 'sliding' && (
                  <NumberInput
                    label="Slide Interval (seconds)"
                    min={1}
                    value={config.slide ? parseInt(config.slide.replace('s', '')) || 30 : 30}
                    onChange={(val) => updateNodeConfig(nodeId, { slide: `${val}s` })}
                    size="sm"
                    description="How often the sliding window updates."
                  />
                )}
              </Stack>
            </Card>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="output">
          <Card withBorder radius="md" p="md">
            <Stack gap="md">
              <TextInput
                label="Target Field"
                placeholder="e.g. total_amount"
                value={config.targetField || ''}
                onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
                size="sm"
                description="Where to store the aggregation result in the message."
              />
              <Text size="xs" c="dimmed">
                If left empty, it defaults to <code>{`{field}_{type}`}</code> (e.g. <code>amount_sum</code>).
              </Text>
            </Stack>
          </Card>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
