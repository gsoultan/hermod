import { Title, Paper, Stack, Group, Box, Text, Center, Loader } from '@mantine/core';import { VHostForm } from '../components/VHostForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';import { IconServer } from '@tabler/icons-react';
export function EditVHostPage() {
  const { vhostId } = useParams({ from: '/vhosts/$vhostId/edit' });

  const { data: vhost, isLoading } = useSuspenseQuery({
    queryKey: ['vhosts', vhostId],
    queryFn: async () => {
      const res = await apiFetch(`/api/vhosts/${vhostId}`);
      if (!res.ok) throw new Error('Failed to fetch vhost');
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
            <IconServer size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit VHost: {vhost?.name}</Title>
              <Text size="sm" c="dimmed">Update virtual host configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <VHostForm initialData={vhost} isEditing />
        </Paper>
      </Stack>
    </Box>
  );
}


