import { Paper, Group, Title, Badge, Text, useMantineColorScheme } from '@mantine/core';
import { normalizeWorkflowStatus } from '@/utils/workflowStatus';

interface WorkflowHeaderProps {
  id: string;
  isNew: boolean;
  name: string;
  active: boolean;
  workflowStatus: string;
}

export function WorkflowHeader({ id, isNew, name, workflowStatus }: WorkflowHeaderProps) {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  const status = normalizeWorkflowStatus(workflowStatus);

  return (
    <Paper
      withBorder
      radius="md"
      p="md"
      mb="sm"
      shadow="xs"
      style={{
        background: isDark
          ? 'linear-gradient(180deg, var(--mantine-color-dark-6), var(--mantine-color-dark-5))'
          : 'linear-gradient(180deg, var(--mantine-color-gray-0), var(--mantine-color-white))',
      }}
    >
      <Group justify="space-between" align="center">
        <Group gap="sm">
          <Title order={3} style={{ lineHeight: 1.2 }}>
            {isNew ? 'New Workflow' : (name || 'Untitled Workflow')}
          </Title>
          {!isNew && (
            <Badge
              color={status.color}
              variant="filled"
            >
              {status.label}
            </Badge>
          )}
        </Group>
        {!isNew && (
          <Text size="sm" c="dimmed">
            ID: {id}
          </Text>
        )}
      </Group>
    </Paper>
  );
}
