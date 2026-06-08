import {
  SimpleGrid,
  Select,
  TextInput,
  Divider,
  Stack,
  Card,
  Group,
  Text,
  rem,
  ThemeIcon,
} from '@mantine/core';
import { IconAlertTriangle, IconSettings, IconDatabase } from '@tabler/icons-react';

interface ErrorHandlingConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function ErrorHandlingConfig({ config, updateNodeConfig, nodeId }: ErrorHandlingConfigProps) {
  return (
    <Card withBorder radius="md" p="md" mt="md">
      <Stack gap="md">
        <Group gap="xs">
          <ThemeIcon variant="light" color="orange" radius="md">
            <IconAlertTriangle size={rem(18)} />
          </ThemeIcon>
          <Stack gap={0}>
            <Text size="sm" fw={600}>
              Error Handling
            </Text>
            <Text size="xs" c="dimmed">
              Configure how this node responds to unexpected errors.
            </Text>
          </Stack>
        </Group>

        <Divider variant="dashed" />

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <Select
            label="On Error Strategy"
            description="The action to take when an error occurs."
            data={[
              { label: 'Fail Workflow', value: 'fail' },
              { label: 'Continue Processing', value: 'continue' },
              { label: 'Drop Message', value: 'drop' },
            ]}
            value={config.onError || 'fail'}
            onChange={(val) => updateNodeConfig(nodeId, { onError: val || 'fail' })}
            size="sm"
            leftSection={<IconSettings size={rem(16)} />}
          />
          <TextInput
            label="Error Status Field"
            placeholder="e.g. _error_details"
            value={config.statusField || ''}
            onChange={(e) => updateNodeConfig(nodeId, { statusField: e.target.value })}
            description="Field to store error information."
            size="sm"
            leftSection={<IconDatabase size={rem(16)} />}
          />
        </SimpleGrid>
      </Stack>
    </Card>
  );
}
