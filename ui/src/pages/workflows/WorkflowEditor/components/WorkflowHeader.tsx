import { Paper, Group, Title, Badge, Text, useMantineColorScheme } from '@mantine/core';

interface WorkflowHeaderProps {
  id: string;
  isNew: boolean;
  name: string;
  active: boolean;
  workflowStatus: string;
}

export function WorkflowHeader({ id, isNew, name, active, workflowStatus }: WorkflowHeaderProps) {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

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
              color={active ? 'green' : 'gray'}
              variant="filled"
            >
              {workflowStatus}
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
