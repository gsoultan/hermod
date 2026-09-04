import { Paper, Title, Container, Box } from '@mantine/core';
import { WorkerForm } from '@/components/forms/WorkerForm';
import { useParams } from '@tanstack/react-router';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';

export function EditWorkerPage() {
  const { workerId } = useParams({ from: '/workers/$workerId/edit' });

  const { data: worker } = useSuspenseQuery({
    queryKey: ['workers', workerId],
    queryFn: async () => {
      const res = await apiFetch(`/api/workers/${workerId}`);
      if (!res.ok) throw new Error('Failed to fetch worker');
      return res.json();
    }
  });

  return (
    <Container size="sm" py="xl">
      <Box className="page-enter">
        <Paper p="xl" withBorder radius="md">
          <Title order={2} mb="xl">Edit Worker</Title>
          <WorkerForm initialData={worker} isEditing />
        </Paper>
      </Box>
    </Container>
  );
}
