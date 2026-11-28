import { Paper, Tabs } from '@mantine/core';
import { PageHeader } from '@/components/rows';
import { Suspense, useState } from 'react';
import { FormSkeleton } from '@/components/common/FormSkeleton';
import { SourceForm } from '@/components/forms/SourceForm';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { useParams } from '@tanstack/react-router';
import { useSessionStore } from '@/auth/session';
import { SourceDiagnosis } from '@/components/connections/SourceDiagnosis';

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

  const role = useSessionStore((s) => s.user?.role);
  const canEdit = role === 'Administrator' || role === 'Editor';
  // Only PostgreSQL can be diagnosed so far.
  const diagnosable = source?.type === 'postgres';
  // ?tab=diagnosis opens it directly, as a Tuning recommendation links here.
  const [tab, setTab] = useState(() => new URLSearchParams(window.location.search).get('tab') === 'diagnosis' ? 'diagnosis' : 'settings');
  const form = (
    <Suspense fallback={<FormSkeleton />}>
      <SourceForm initialData={source} isEditing />
    </Suspense>
  );

  return (
    <>
      <PageHeader
        title={source?.name || 'Edit source'}
        lede="Change how Hermod connects to this source. Flows that use it pick up the change when they next start."
        back={{ to: '/connections', search: { tab: 'sources' }, label: 'Back to connections' }}
      />
      <Paper p={{ base: 'md', sm: 'xl' }} withBorder radius="md">
        {diagnosable ? (
          <Tabs value={tab} onChange={(v) => setTab(v ?? 'settings')} keepMounted={false}>
            <Tabs.List mb="lg">
              <Tabs.Tab value="settings">Settings</Tabs.Tab>
              <Tabs.Tab value="diagnosis">Diagnosis</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="settings">{form}</Tabs.Panel>
            <Tabs.Panel value="diagnosis"><SourceDiagnosis sourceId={sourceId} canCheck={canEdit} /></Tabs.Panel>
          </Tabs>
        ) : form}
      </Paper>
    </>
  );
}
