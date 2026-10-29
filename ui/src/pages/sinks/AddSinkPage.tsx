import { Paper } from '@mantine/core';
import { Suspense } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SinkForm } from '@/components/forms/SinkForm';
import { PageHeader } from '@/components/rows';

export function AddSinkPage() {
  return (
    <>
      <PageHeader
        title="Add sink"
        lede="Pick where data goes, connect to it, then name it."
        back={{ to: '/connections', search: { tab: 'sinks' }, label: 'Back to connections' }}
      />
      <Paper p="xl" withBorder radius="md">
        <Suspense fallback={<FormSkeleton />}>
          <SinkForm />
        </Suspense>
      </Paper>
    </>
  );
}
