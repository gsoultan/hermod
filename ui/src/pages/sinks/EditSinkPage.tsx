import { Title, Paper, Stack, Group, Box, Text } from '@mantine/core';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SinkForm } from '@/components/forms/SinkForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
import { IconExternalLink } from '@tabler/icons-react';
export function EditSinkPage() {
  const { sinkId } = useParams({ from: '/sinks/$sinkId/edit' });

  const { data: sink } = useSuspenseQuery({
    queryKey: ['sinks', sinkId],
    queryFn: async () => {
      const res = await apiFetch(`/api/sinks/${sinkId}`);
      if (!res.ok) throw new Error('Failed to fetch sink');
      return res.json();
    }
  });

  return (
    <Box p="md">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Group gap="sm">
            <IconExternalLink size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box>
              <Title order={2} fw={800}>Edit Sink: {sink?.name}</Title>
              <Text size="sm" c="dimmed">Update your data sink configuration.</Text>
            </Box>
          </Group>
        </Paper>

        <Paper p="xl" withBorder radius="md">
          <Suspense fallback={<FormSkeleton />}>
            <SinkForm initialData={sink} isEditing />
          </Suspense>
        </Paper>
      </Stack>
    </Box>
  );
}
