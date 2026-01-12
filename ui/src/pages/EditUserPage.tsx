import { Title, Paper, Stack, Group, Box, Text, Center, Loader } from '@mantine/core';
import { IconUsers } from '@tabler/icons-react';
import { UserForm } from '../components/UserForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';

export function EditUserPage() {
  const { userId } = useParams({ from: '/users/$userId/edit' });

  const { data: user, isLoading } = useSuspenseQuery({
    queryKey: ['users', userId],
    queryFn: async () => {
      const res = await apiFetch(`/api/users/${userId}`);
      if (!res.ok) throw new Error('Failed to fetch user');
      return res.json();
    }
  });

  if (isLoading) {
    return (
      <Center h="100vh">
        <Loader size="xl" />
      </Center>
    );
  }

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconUsers size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit User: {user?.username}</Title>
              <Text size="sm" c="dimmed">Update user profile, role, and permissions.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <UserForm initialData={user} isEditing />
        </Paper>
      </Stack>
    </Box>
  );
}
