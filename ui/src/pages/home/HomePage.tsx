import { useState } from 'react';
import { Anchor, Button, Group, Stack, Text } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { Link } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { IconAlertTriangle, IconCircleCheck, IconDatabase, IconGitBranch, IconInbox, IconPlug, IconPlus } from '@tabler/icons-react';
import { apiFetch } from '@/api';
import { getSessionRole, getSessionUser } from '@/auth/session';
import { useVHost } from '@/context/VHostContext';
import { useDashboardStream } from '@/hooks/useDashboardStream';
import type { Workflow } from '@/types';
import { PageHeader, Row, RowList } from '@/components/rows';
import { FlowRow } from '@/components/flows/FlowRow';
import { useToggleFlow } from '@/components/flows/useToggleFlow';
import { attentionItems, greeting, isFirstRun, statusLine } from './homeModel';
import { ConnectionBadge, PlatformPanel } from './PlatformPanel';

const FLOWS_ON_HOME = 5;

interface HomePageProps {
  /** The clock, injectable so the greeting can be tested. */
  now?: Date;
}

/**
 * Home: a greeting, one line on how the platform is doing, what needs you,
 * your flows, and the platform metrics underneath. On an empty install it
 * becomes the three steps to a first flow instead.
 */
export function HomePage({ now = new Date() }: HomePageProps) {
  const user = getSessionUser();
  const role = getSessionRole();
  const canEdit = role === 'Administrator' || role === 'Editor';
  const { selectedVHost } = useVHost();
  const { stats, series, connection, error } = useDashboardStream(selectedVHost);
  const mps = series.length > 0 ? series[series.length - 1] : stats.throughput;

  const vh = encodeURIComponent(selectedVHost);
  const { data: workflowsData, isLoading: flowsLoading } = useQuery({
    queryKey: ['workflows', 'home', selectedVHost],
    queryFn: async () => {
      const res = await apiFetch(`/api/workflows?limit=100&vhost=${vh}`, { silent: true });
      return (await res.json()) as { data: Workflow[]; total: number };
    },
    refetchInterval: 30_000,
  });
  const { data: approvals } = useQuery({
    queryKey: ['approvals', 'pending-count'],
    queryFn: async () => {
      const res = await apiFetch('/api/approvals?status=pending&page=1&limit=1', { silent: true });
      return (await res.json()) as { total?: number };
    },
    refetchInterval: 60_000,
  });
  const { data: deadLetters } = useQuery({
    queryKey: ['dead-letters', 'stats', selectedVHost],
    queryFn: async () => {
      const res = await apiFetch(`/api/dead-letters/stats?vhost=${vh}`, { silent: true });
      return (await res.json()) as { queued?: number };
    },
    refetchInterval: 60_000,
  });

  const workflows = workflowsData?.data ?? [];
  const total = workflowsData?.total ?? workflows.length;
  const items = attentionItems({
    workflows,
    stats,
    deadLettersQueued: deadLetters?.queued ?? 0,
    pendingApprovals: approvals?.total ?? 0,
  });
  const firstRun = !flowsLoading && isFirstRun(stats, workflows);

  return (
    <Stack gap="xl">
      <PageHeader
        title={greeting(now, user?.username)}
        lede={statusLine(stats, mps)}
        meta={<ConnectionBadge state={connection} />}
        actions={canEdit && !firstRun && (
          <Button component={Link} to="/workflows/new" leftSection={<IconPlus size="1rem" />}>
            New flow
          </Button>
        )}
      />

      {firstRun ? (
        <GetStarted canEdit={canEdit} />
      ) : (
        <>
          <Section title="Needs you">
            {items.length === 0 ? (
              <Group gap="xs" py="sm">
                <IconCircleCheck size="1.1rem" color="var(--mantine-color-green-6)" aria-hidden />
                <Text size="sm">Everything is running normally</Text>
              </Group>
            ) : (
              <RowList aria-label="Needs you">
                {items.map((item) => (
                  <Row
                    key={item.key}
                    icon={item.tone === 'info' ? IconInbox : IconAlertTriangle}
                    iconTone={item.tone === 'err' ? 'err' : 'default'}
                    title={item.title}
                    detail={item.detail}
                    action={
                      <Button
                        component={Link}
                        to={item.action.to}
                        search={item.action.search as never}
                        size="xs"
                        variant={item.tone === 'err' ? 'filled' : 'light'}
                        color={item.tone === 'err' ? 'red' : undefined}
                      >
                        {item.action.label}
                      </Button>
                    }
                  />
                ))}
              </RowList>
            )}
          </Section>

          <YourFlows flows={workflows} total={total} canEdit={canEdit} loading={flowsLoading} />
        </>
      )}

      <PlatformPanel stats={stats} series={series} error={error} />
    </Stack>
  );
}

function Section({ title, aside, children }: { title: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="baseline">
        <Text fw={700}>{title}</Text>
        {aside}
      </Group>
      {children}
    </Stack>
  );
}

function YourFlows({ flows, total, canEdit, loading }: { flows: Workflow[]; total: number; canEdit: boolean; loading: boolean }) {
  const toggle = useToggleFlow();
  const shown = flows.slice(0, FLOWS_ON_HOME);
  return (
    <Section
      title="Your flows"
      aside={total > 0 && (
        <Anchor component={Link} to="/workflows" size="sm">
          See all {total} {total === 1 ? 'flow' : 'flows'}
        </Anchor>
      )}
    >
      {loading ? (
        <Text size="sm" c="dimmed">Loading…</Text>
      ) : shown.length === 0 ? (
        <Text size="sm" c="dimmed">No flows in this virtual host yet.</Text>
      ) : (
        <RowList aria-label="Your flows">
          {shown.map((flow) => (
            <FlowRow
              key={flow.id}
              flow={flow}
              onToggle={canEdit ? (f) => toggle.mutate({ id: f.id, name: f.name, active: f.active }) : undefined}
              toggling={toggle.isPending && toggle.variables?.id === flow.id}
            />
          ))}
        </RowList>
      )}
    </Section>
  );
}

/** The empty install: three steps, in order, plus a sample to skip them. */
function GetStarted({ canEdit }: { canEdit: boolean }) {
  const queryClient = useQueryClient();
  const [loading, setLoading] = useState(false);

  // The sample scenario was the dashboard's "Bootstrap Enterprise Scenario";
  // same endpoint, a name that says what it does.
  const loadSample = async () => {
    setLoading(true);
    try {
      const res = await apiFetch('/api/infra/bootstrap-scenario', { method: 'POST' });
      if (!res.ok) throw new Error('The sample could not be loaded');
      const data = await res.json().catch(() => ({}));
      notifications.show({ title: 'Sample loaded', message: data.message ?? 'Sources, sinks and flows were created.', color: 'green' });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
    } catch (err) {
      notifications.show({ title: 'Sample not loaded', message: err instanceof Error ? err.message : 'Unknown error', color: 'red' });
    } finally {
      setLoading(false);
    }
  };

  return (
    <Section
      title="Get started"
      aside={canEdit && (
        <Button variant="light" size="xs" onClick={loadSample} loading={loading}>
          Load a sample
        </Button>
      )}
    >
      <RowList aria-label="Get started">
        <Row icon={IconDatabase} title="1. Connect a source" detail="Where the data comes from: a database, a queue, an API." to="/sources/new" />
        <Row icon={IconPlug} title="2. Connect a sink" detail="Where it goes: a warehouse, a topic, a webhook." to="/sinks/new" />
        <Row icon={IconGitBranch} title="3. Make your first flow" detail="Source, the steps in between, sink. Switch it on." to="/workflows/new" />
      </RowList>
      {!canEdit && (
        <Text size="sm" c="dimmed">An editor or administrator sets these up. You can watch them run from here.</Text>
      )}
    </Section>
  );
}
