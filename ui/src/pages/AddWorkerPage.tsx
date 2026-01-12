import { Paper, Title, Container, Box } from '@mantine/core';
import { WorkerForm } from '../components/WorkerForm';

export function AddWorkerPage() {
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
          <Title order={2} mb="xl">Register New Worker</Title>
          <WorkerForm />
        </Paper>
      </Box>
    </Container>
  );
}
