import { lazy, type ComponentType } from 'react';
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
  const Panel = PANELS[tab] ?? SourcesPage;
  return (
    <HubPage title="Connections" lede="Where data comes from and where it goes." tabs={tabs} value={tab} onChange={setTab}>
      <Panel />
    </HubPage>
  );
}
