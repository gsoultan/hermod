import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { IconRoute } from '@tabler/icons-react';
import { ConnectionWizard } from '../components/ConnectionWizard';

export function AddConnectionPage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconRoute size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New Connection</Title>
              <Text size="sm" c="dimmed">Follow the steps to configure your data flow.</Text>
            </Box>
          </Group>
        </Paper>
        <Paper p="xl" withBorder radius="md">
          <ConnectionWizard />
        </Paper>
      </Stack>
    </Box>
  );
}
