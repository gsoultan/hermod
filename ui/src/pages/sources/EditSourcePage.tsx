import { Paper } from '@mantine/core';
import { PageHeader } from '@/components/rows';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SourceForm } from '@/components/forms/SourceForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
export function EditSourcePage() {
  const { sourceId } = useParams({ from: '/sources/$sourceId/edit' });

  const { data: source } = useSuspenseQuery({
    queryKey: ['sources', sourceId],
    queryFn: async () => {
      const res = await apiFetch(`/api/sources/${sourceId}`);
      if (!res.ok) throw new Error('Failed to fetch source');
      return res.json();
    }
  });

  return (
    <>
      <PageHeader
        title={source?.name || 'Edit source'}
        lede="Change how Hermod connects to this source. Flows that use it pick up the change when they next start."
        back={{ to: '/connections', search: { tab: 'sources' }, label: 'Back to connections' }}
      />
      <Paper p="xl" withBorder radius="md">
        <Suspense fallback={<FormSkeleton />}>
          <SourceForm initialData={source} isEditing />
        </Suspense>
      </Paper>
    </>
  );
}
