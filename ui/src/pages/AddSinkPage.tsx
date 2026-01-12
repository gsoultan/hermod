import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { IconExternalLink } from '@tabler/icons-react';
import { SinkForm } from '../components/SinkForm';

export function AddSinkPage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconExternalLink size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New Sink</Title>
              <Text size="sm" c="dimmed">Configure a new destination for your data streams.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <SinkForm />
        </Paper>
      </Stack>
    </Box>
  );
}
