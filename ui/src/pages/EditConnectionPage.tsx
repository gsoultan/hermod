import { Title, Paper, Stack, Group, Box, Text, Center, Loader } from '@mantine/core';
import { IconRoute } from '@tabler/icons-react';
import { ConnectionForm } from '../components/ConnectionForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';

export function EditConnectionPage() {
  const { connectionId } = useParams({ from: '/connections/$connectionId/edit' });

  const { data: connection, isLoading } = useSuspenseQuery({
    queryKey: ['connections', connectionId],
    queryFn: async () => {
      const res = await apiFetch(`/api/connections/${connectionId}`);
      if (!res.ok) throw new Error('Failed to fetch connection');
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
            <IconRoute size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit Connection: {connection?.name}</Title>
              <Text size="sm" c="dimmed">Update your data flow configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <ConnectionForm initialData={connection} isEditing />
        </Paper>
      </Stack>
    </Box>
  );
}
