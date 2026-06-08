import { Stack, TextInput, Select, Card, Group, rem, ThemeIcon, Text } from '@mantine/core';
import { IconMessage, IconSearch, IconAlertCircle } from '@tabler/icons-react';

interface LogConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function LogConfig({ config, updateNodeConfig, nodeId }: LogConfigProps) {
  return (
    <Card withBorder radius="md" p="md">
      <Stack gap="md">
        <Group gap="xs">
          <ThemeIcon variant="light" color="gray" radius="md">
            <IconMessage size={rem(18)} />
          </ThemeIcon>
          <Stack gap={0}>
            <Text size="sm" fw={600}>
              Logging Configuration
            </Text>
            <Text size="xs" c="dimmed">
              Emit log messages to the worker console for debugging and auditing.
            </Text>
          </Stack>
        </Group>

        <TextInput
          label="Log Template"
          placeholder="e.g. Processing order {{order_id}}..."
          value={config.message || ''}
          onChange={(e) => updateNodeConfig(nodeId, { message: e.currentTarget.value })}
          size="sm"
          description="Supports Go template syntax for dynamic messages."
          leftSection={<IconMessage size={rem(16)} />}
        />

        <Group grow>
          <Select
            label="Level"
            data={[
              { value: 'DEBUG', label: 'Debug' },
              { value: 'INFO', label: 'Info' },
              { value: 'WARN', label: 'Warning' },
              { value: 'ERROR', label: 'Error' },
            ]}
            value={config.level || 'INFO'}
            onChange={(val) => updateNodeConfig(nodeId, { level: val })}
            size="sm"
            leftSection={<IconAlertCircle size={rem(16)} />}
          />
          <TextInput
            label="Data Path (Optional)"
            placeholder="e.g. payload.id"
            description="Specific field to include in logs."
            value={config.path || ''}
            onChange={(e) => updateNodeConfig(nodeId, { path: e.currentTarget.value })}
            size="sm"
            leftSection={<IconSearch size={rem(16)} />}
          />
        </Group>
      </Stack>
    </Card>
  );
}
