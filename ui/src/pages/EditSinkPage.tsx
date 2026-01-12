import { Title, Paper, Stack, Group, Box, Text, Center, Loader } from '@mantine/core';
import { IconExternalLink } from '@tabler/icons-react';
import { SinkForm } from '../components/SinkForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';
import { useParams } from '@tanstack/react-router';

export function EditSinkPage() {
  const { sinkId } = useParams({ from: '/sinks/$sinkId/edit' });

  const { data: sink, isLoading } = useSuspenseQuery({
    queryKey: ['sinks', sinkId],
    queryFn: async () => {
      const res = await apiFetch(`/api/sinks/${sinkId}`);
      if (!res.ok) throw new Error('Failed to fetch sink');
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
            <IconExternalLink size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit Sink: {sink?.name}</Title>
              <Text size="sm" c="dimmed">Update your data sink configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <SinkForm initialData={sink} isEditing />
        </Paper>
      </Stack>
    </Box>
  );
}
