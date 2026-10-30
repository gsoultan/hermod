import { lazy, type ComponentType } from 'react';
import { HubPage } from '@/components/layout/HubPage';
import { activityTabs } from '@/components/layout/nav';
import { getSessionRole } from '@/auth/session';
import { useHubTab } from './useHubTab';

const LogsPage = lazy(async () => ({ default: (await import('../monitoring/LogsPage')).LogsPage }));
const DeadLettersPage = lazy(async () => ({ default: (await import('../monitoring/DeadLettersPage')).DeadLettersPage }));
const ApprovalsPage = lazy(async () => ({ default: (await import('../workflows/ApprovalsPage')).ApprovalsPage }));
const GlobalHealthPage = lazy(async () => ({ default: (await import('../monitoring/GlobalHealthPage')).default }));
const AuditLogsPage = lazy(async () => ({ default: (await import('../monitoring/AuditLogsPage')).AuditLogsPage }));
const LineagePage = lazy(async () => ({ default: (await import('../monitoring/LineagePage')).LineagePage }));
const ComplianceDashboard = lazy(async () => ({ default: (await import('../monitoring/ComplianceDashboard')).ComplianceDashboard }));
const ModelsPage = lazy(async () => ({ default: (await import('../ml/ModelsPage')).ModelsPage }));
const AIBudgetPage = lazy(async () => ({ default: (await import('../ai/AIBudgetPage')).AIBudgetPage }));

const PANELS: Record<string, ComponentType> = {
  logs: LogsPage,
  'dead-letters': DeadLettersPage,
  approvals: ApprovalsPage,
  health: GlobalHealthPage,
  audit: AuditLogsPage,
  lineage: LineagePage,
  compliance: ComplianceDashboard,
  ml: ModelsPage,
  'ai-budget': AIBudgetPage,
};

/** What happened and what is happening: logs, dead letters, health and insights. */
export function ActivityPage() {
  const tabs = activityTabs(getSessionRole());
  const [tab, setTab] = useHubTab(tabs);
  const Panel = PANELS[tab] ?? LogsPage;
  return (
    <HubPage title="Activity" lede="What happened, what failed, and what the platform noticed." tabs={tabs} value={tab} onChange={setTab}>
      <Panel />
    </HubPage>
  );
}
