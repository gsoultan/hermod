import { Stack, TextInput, NumberInput, Alert, Card, Group, rem, ThemeIcon, Text } from '@mantine/core';
import { IconInfoCircle, IconGitMerge, IconKey, IconNumbers } from '@tabler/icons-react';

interface JoinConfigProps {
  config: any;
  nodeId: string;
  updateNodeConfig: (nodeId: string, config: any) => void;
  availableFields: any[];
}

export function JoinConfig({ config, nodeId, updateNodeConfig }: JoinConfigProps) {
  const data = config || {};

  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="blue"
        variant="light"
        radius="md"
        title="Stateful Join"
      >
        <Text size="sm">
          Collects multiple messages sharing the same key and merges their payloads once all
          expected sources have arrived.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="blue" radius="md">
              <IconGitMerge size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Join Settings
            </Text>
          </Group>

          <TextInput
            label="Correlation Key Path"
            placeholder="e.g. order_id"
            value={data.key_path || ''}
            onChange={(e) => updateNodeConfig(nodeId, { key_path: e.currentTarget.value })}
            required
            size="sm"
            description="JSON path to extract the common identifier from messages."
            leftSection={<IconKey size={rem(16)} />}
          />

          <NumberInput
            label="Expected Source Count"
            value={data.expected_sources || 2}
            onChange={(val) => updateNodeConfig(nodeId, { expected_sources: val })}
            min={2}
            size="sm"
            description="Number of unique messages required to trigger the join."
            leftSection={<IconNumbers size={rem(16)} />}
          />
        </Stack>
      </Card>
    </Stack>
  );
}
