import { Paper } from '@mantine/core';
import { VHostForm } from '@/components/forms/VHostForm';
import { PageHeader } from '@/components/rows';

export function AddVHostPage() {
  return (
    <>
      <PageHeader title="Add virtual host" lede="A separate space for a group of flows and connections" back={{ to: '/settings', label: 'Back to settings', search: { tab: 'vhosts' } }} />
      <Paper p="xl" withBorder radius="md">
        <VHostForm />
      </Paper>
    </>
  );
}
