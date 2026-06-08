import { Stack, TextInput, Alert, Text, Card, Group, rem, ThemeIcon } from '@mantine/core';
import { IconInfoCircle, IconCopyOff, IconTag } from '@tabler/icons-react';

interface DeduplicateConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function DeduplicateConfig({ config, updateNodeConfig, nodeId }: DeduplicateConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="blue"
        variant="light"
        radius="md"
        title="Deduplication"
      >
        <Text size="sm">
          Prevent redundant processing using high-speed in-memory deduplication (Bloom Filters).
          Ideal for skipping duplicate messages within a rolling window.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="blue" radius="md">
              <IconCopyOff size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Deduplication Key
            </Text>
          </Group>

          <TextInput
            label="Key Path"
            placeholder="e.g. order.id"
            description="The unique identifier used to detect duplicates."
            value={config.keyPath || ''}
            onChange={(e) => updateNodeConfig(nodeId, { keyPath: e.currentTarget.value })}
            size="sm"
            leftSection={<IconTag size={rem(16)} />}
          />
        </Stack>
      </Card>
    </Stack>
  );
}
