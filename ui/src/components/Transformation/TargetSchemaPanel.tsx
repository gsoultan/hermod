import { Alert, Badge, Card, Divider, Group, ScrollArea, Stack, Text } from '@mantine/core';
import { IconAlertCircle, IconDatabase } from '@tabler/icons-react';

interface TargetSchemaPanelProps {
  title?: string;
  fields: string[];
  loading?: boolean;
  error?: string | null;
}

export function TargetSchemaPanel({ title = 'Target Schema', fields, loading, error }: TargetSchemaPanelProps) {
  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%">
      <Stack h="100%">
        <Group gap="xs">
          <IconDatabase size="1.2rem" color="var(--mantine-color-green-7)" />
          <Text size="sm" fw={700}>{title}</Text>
          {loading && <Badge color="blue" variant="light">Loading</Badge>}
        </Group>
        <Divider />
        {error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />}>
            {error}
          </Alert>
        ) : (
          <ScrollArea flex={1}>
            {fields.length === 0 ? (
              <Text size="sm" c="dimmed">No fields detected.</Text>
            ) : (
              <Stack gap={4}>
                {fields.map((f) => (
                  <Group key={f} gap="xs">
                    <Badge size="xs" variant="light" color="gray">field</Badge>
                    <Text size="sm" ff="monospace">{f}</Text>
                  </Group>
                ))}
              </Stack>
            )}
          </ScrollArea>
        )}
      </Stack>
    </Card>
  );
}
