import { lazy, type ComponentType } from 'react';
import { Button } from '@mantine/core';
import { useNavigate } from '@tanstack/react-router';
import { IconPlus } from '@tabler/icons-react';
import { HubPage } from '@/components/layout/HubPage';
import { connectionsTabs } from '@/components/layout/nav';
import { getSessionRole } from '@/auth/session';
import { useHubTab } from './useHubTab';

const SourcesPage = lazy(async () => ({ default: (await import('../sources/SourcesPage')).SourcesPage }));
const SinksPage = lazy(async () => ({ default: (await import('../sinks/SinksPage')).SinksPage }));
const CommunityMarketplace = lazy(async () => ({ default: (await import('../Marketplace/CommunityMarketplace')).CommunityMarketplace }));

const PANELS: Record<string, ComponentType> = {
  sources: SourcesPage,
  sinks: SinksPage,
  community: CommunityMarketplace,
};

/** Sources, sinks and the community connectors, in one place. */
export function ConnectionsPage() {
  const tabs = connectionsTabs(getSessionRole());
  const [tab, setTab] = useHubTab(tabs);
  const navigate = useNavigate();
  const Panel = PANELS[tab] ?? SourcesPage;
  const canEdit = getSessionRole() !== 'Viewer';
  const add = tab === 'sources' ? { label: 'Add source', to: '/sources/new' } : tab === 'sinks' ? { label: 'Add sink', to: '/sinks/new' } : null;
  return (
    <HubPage
      title="Connections"
      lede="Where data comes from and where it goes."
      tabs={tabs}
      value={tab}
      onChange={setTab}
      actions={canEdit && add ? <Button leftSection={<IconPlus size="1rem" />} onClick={() => navigate({ to: add.to })}>{add.label}</Button> : undefined}
    >
      <Panel />
    </HubPage>
  );
}
