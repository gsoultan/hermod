import { Stack, JsonInput, Alert, Text, Card, Group, rem, ThemeIcon } from '@mantine/core';
import { IconInfoCircle, IconShieldCheck, IconCode } from '@tabler/icons-react';

interface ValidatorConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function ValidatorConfig({ config, updateNodeConfig, nodeId }: ValidatorConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="violet"
        variant="light"
        radius="md"
        title="Schema Validation"
      >
        <Text size="sm">
          Ensure data integrity by validating incoming messages against a JSON schema or a set
          of type-based rules.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="green" radius="md">
              <IconShieldCheck size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Validation Rules
            </Text>
          </Group>

          <Stack gap={4}>
            <Group gap="xs">
              <IconCode size={rem(16)} className="text-gray-400" />
              <Text size="xs" fw={500}>
                JSON Schema or Rules Definition
              </Text>
            </Group>
            <JsonInput
              placeholder='{"field.path": "string", "age": "number"}'
              value={config.schema || ''}
              onChange={(val) => updateNodeConfig(nodeId, { schema: val })}
              formatOnBlur
              minRows={10}
              size="sm"
              styles={{ input: { fontFamily: 'monospace' } }}
            />
            <Text size="10px" c="dimmed">
              Define expected types, patterns, or values for fields in your payload.
            </Text>
          </Stack>
        </Stack>
      </Card>
    </Stack>
  );
}
