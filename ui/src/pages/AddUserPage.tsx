import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { UserForm } from '../components/UserForm';import { IconUserPlus } from '@tabler/icons-react';
export function AddUserPage() {
  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconUserPlus size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Add New User</Title>
              <Text size="sm" c="dimmed">Create a new user and assign roles and virtual hosts.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <UserForm />
        </Paper>
      </Stack>
    </Box>
  );
}


