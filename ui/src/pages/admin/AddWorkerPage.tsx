import { Paper, Title, Container, Box } from '@mantine/core';
import { WorkerForm } from '@/components/forms/WorkerForm';

export function AddWorkerPage() {
  return (
    <Container size="sm" py="xl">
      <Box className="page-enter">
        <Paper p="xl" withBorder radius="md">
          <Title order={2} mb="xl">Register New Worker</Title>
          <WorkerForm />
        </Paper>
      </Box>
    </Container>
  );
}
