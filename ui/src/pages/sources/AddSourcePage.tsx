import { Paper } from '@mantine/core';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SourceForm } from '@/components/forms/SourceForm';
import { PageHeader } from '@/components/rows';

export function AddSourcePage() {
  return (
    <>
      <PageHeader
        title="Add source"
        lede="Pick what to read from, connect to it, then name it."
        back={{ to: '/connections', search: { tab: 'sources' }, label: 'Back to connections' }}
      />
      <Paper p="xl" withBorder radius="md">
        <Suspense fallback={<FormSkeleton />}>
          <SourceForm />
        </Suspense>
      </Paper>
    </>
  );
}
