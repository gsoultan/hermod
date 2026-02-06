import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { VHostForm } from '../components/VHostForm';import { IconPlus } from '@tabler/icons-react';
export function AddVHostPage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconPlus size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New VHost</Title>
              <Text size="sm" c="dimmed">Create a new virtual host for logical isolation.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <VHostForm />
        </Paper>
      </Stack>
    </Box>
  );
}
