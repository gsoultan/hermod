import { Paper } from '@mantine/core';
import { PageHeader } from '@/components/rows';
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
    <>
      <PageHeader title={worker?.name || 'Worker'} lede="Worker" back={{ to: '/settings', label: 'Back to settings', search: { tab: 'workers' } }} />
      <Paper p="xl" withBorder radius="md" maw={720}>
        <WorkerForm initialData={worker} isEditing />
      </Paper>
    </>
  );
}
