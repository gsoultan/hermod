import { Paper } from '@mantine/core';
import { PageHeader } from '@/components/rows';
import { VHostForm } from '@/components/forms/VHostForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
export function EditVHostPage() {
  const { vhostId } = useParams({ from: '/vhosts/$vhostId/edit' });

  const { data: vhost } = useSuspenseQuery({
    queryKey: ['vhosts', vhostId],
    queryFn: async () => {
      const res = await apiFetch(`/api/vhosts/${vhostId}`);
      if (!res.ok) throw new Error('Failed to fetch vhost');
      return res.json();
    }
  });

  return (
    <>
      <PageHeader title={vhost?.name || 'Virtual host'} lede="Virtual host" back={{ to: '/settings', label: 'Back to settings', search: { tab: 'vhosts' } }} />
      <Paper p="xl" withBorder radius="md">
        <VHostForm initialData={vhost} isEditing />
      </Paper>
    </>
  );
}
