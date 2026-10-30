import { Paper } from '@mantine/core';
import { PageHeader } from '@/components/rows';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SinkForm } from '@/components/forms/SinkForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
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
    <>
      <PageHeader
        title={sink?.name || 'Edit sink'}
        lede="Change how Hermod connects to this sink. Flows that use it pick up the change when they next start."
        back={{ to: '/connections', search: { tab: 'sinks' }, label: 'Back to connections' }}
      />
      <Paper p="xl" withBorder radius="md">
        <Suspense fallback={<FormSkeleton />}>
          <SinkForm initialData={sink} isEditing />
        </Suspense>
      </Paper>
    </>
  );
}
