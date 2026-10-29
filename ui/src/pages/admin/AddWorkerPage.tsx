import { Paper } from '@mantine/core';
import { WorkerForm } from '@/components/forms/WorkerForm';
import { PageHeader } from '@/components/rows';

export function AddWorkerPage() {
  return (
    <>
      <PageHeader title="Register worker" lede="Register the machine here, then start Hermod on it with the command you get" back={{ to: '/settings', label: 'Back to settings', search: { tab: 'workers' } }} />
      <Paper p="xl" withBorder radius="md" maw={720}>
        <WorkerForm />
      </Paper>
    </>
  );
}
