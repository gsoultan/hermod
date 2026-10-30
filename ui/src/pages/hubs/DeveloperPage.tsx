import { lazy, type ComponentType } from 'react';
import { HubPage } from '@/components/layout/HubPage';
import { developerTabs } from '@/components/layout/nav';
import { getSessionRole } from '@/auth/session';
import { useHubTab } from './useHubTab';

const SchemasPage = lazy(async () => ({ default: (await import('../system/SchemasPage')).SchemasPage }));
const SecretsPage = lazy(async () => ({ default: (await import('../secrets/SecretsPage')).SecretsPage }));

const PANELS: Record<string, ComponentType> = {
  schemas: SchemasPage,
  secrets: SecretsPage,
};

/** The schema registry and secrets, under Settings › Developer. */
export function DeveloperPage() {
  const tabs = developerTabs(getSessionRole());
  const [tab, setTab] = useHubTab(tabs);
  const Panel = PANELS[tab] ?? SchemasPage;
  return (
    <HubPage title="Developer" lede="Data contracts and the secrets flows refer to." tabs={tabs} value={tab} onChange={setTab}>
      <Panel />
    </HubPage>
  );
}
