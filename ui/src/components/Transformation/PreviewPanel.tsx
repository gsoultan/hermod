import { Alert, Badge, Button, Card, Code, Divider, Group, ScrollArea, Stack, Text } from '@mantine/core';
import { IconAlertCircle, IconEye, IconPlayerPlay } from '@tabler/icons-react';

interface PreviewPanelProps {
  title?: string;
  loading?: boolean;
  error?: string | null;
  result?: unknown;
  onRun?: () => void;
}

export function PreviewPanel({ title = 'Preview', loading, error, result, onRun }: PreviewPanelProps) {
  const isArray = Array.isArray(result);
  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%">
      <Stack h="100%">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <IconEye size="1.2rem" color="var(--mantine-color-blue-7)" />
            <Text size="sm" fw={700} c="dimmed">{title}</Text>
            {loading && <Badge color="blue" variant="light">Running</Badge>}
            {isArray && (
              <Badge color="gray" variant="light">{(result as any[]).length} items</Badge>
            )}
          </Group>
          <Button size="xs" variant="light" leftSection={<IconPlayerPlay size="1rem" />} onClick={onRun} loading={!!loading}>
            Run Preview
          </Button>
        </Group>
        <Divider />
        {error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />}>{error}</Alert>
        ) : (
          <ScrollArea flex={1}>
            <Code block>
              {result ? JSON.stringify(result, null, 2) : '// No preview yet'}
            </Code>
          </ScrollArea>
        )}
      </Stack>
    </Card>
  );
}
