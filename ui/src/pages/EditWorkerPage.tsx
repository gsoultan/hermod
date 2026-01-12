import { Paper, Title, Container, Center, Loader, Box } from '@mantine/core';
import { WorkerForm } from '../components/WorkerForm';
import { useParams } from '@tanstack/react-router';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '../api';

export function EditWorkerPage() {
  const { workerId } = useParams({ from: '/workers/$workerId/edit' });

  const { data: worker, isLoading } = useSuspenseQuery({
    queryKey: ['workers', workerId],
    queryFn: async () => {
      const res = await apiFetch(`/api/workers/${workerId}`);
      if (!res.ok) throw new Error('Failed to fetch worker');
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
    <Container size="sm" py="xl">
      <Box style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
        <style>
          {`
            @keyframes fadeIn {
              from { opacity: 0; transform: translateY(10px); }
              to { opacity: 1; transform: translateY(0); }
            }
          `}
        </style>
        <Paper p="xl" withBorder radius="md">
          <Title order={2} mb="xl">Edit Worker</Title>
          <WorkerForm initialData={worker} isEditing />
        </Paper>
      </Box>
    </Container>
  );
}
