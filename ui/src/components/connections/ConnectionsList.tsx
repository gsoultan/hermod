import { useState } from 'react';
import { ActionIcon, Button, Group, List, Menu, Modal, Pagination, Paper, Select, Stack, Text, ThemeIcon } from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { IconAlertCircle, IconDatabaseImport, IconDots, IconEdit, IconExternalLink, IconFolder, IconTrash } from '@tabler/icons-react';
import { apiFetch } from '@/api';
import { getSessionRole } from '@/auth/session';
import { useVHost } from '@/context/VHostContext';
import { useLiveStatuses } from '@/hooks/useLiveStatuses';
import { LiveStatusIndicator } from '@/components/common/LiveStatusIndicator';
import { useWorkspaceFilterOptions, useWorkspaces } from '@/components/common/WorkspaceSelect';
import { EmptyState, FilterBar, Row, RowList, StatePill } from '@/components/rows';
import type { Sink, Source, Worker, Workflow } from '@/types';
import { connectionDetail, connectionState, liveStatusFor, type ConnectionKind } from './connectionModel';

const PAGE_SIZE = 30;

const WORDS: Record<ConnectionKind, { plural: string; Plural: string; icon: typeof IconDatabaseImport; newTo: string; editTo: string; param: string; lede: string }> = {
  source: {
    plural: 'sources', Plural: 'Sources', icon: IconDatabaseImport, newTo: '/sources/new', editTo: '/sources/$sourceId/edit', param: 'sourceId',
    lede: 'A source is where data comes from: a database whose changes Hermod captures, a queue, an API.',
  },
  sink: {
    plural: 'sinks', Plural: 'Sinks', icon: IconExternalLink, newTo: '/sinks/new', editTo: '/sinks/$sinkId/edit', param: 'sinkId',
    lede: 'A sink is where data goes: a warehouse, a queue, a webhook, a file.',
  },
};

/** "Used by 3 flows", "Used by 1 flow", "Not used". */
export function usedBy(count: number): string {
  if (count === 0) return 'Not used';
  return `Used by ${count} ${count === 1 ? 'flow' : 'flows'}`;
}

type Connection = (Source | Sink) & { worker_id?: string; workspace_id?: string; status?: string; active?: boolean };

/**
 * Sources and sinks as one list: a row per connection with its name, what and
 * where it is, its live state and ⋯ (Edit, Delete). The two pages were the
 * same seven-column table twice; the only differences are the endpoint, the
 * icon and the words, which live in WORDS above.
 */
export function ConnectionsList({ kind }: { kind: ConnectionKind }) {
  const words = WORDS[kind];
  const queryClient = useQueryClient();
  const { selectedVHost } = useVHost();
  const canEdit = getSessionRole() !== 'Viewer';
  const [search, setSearch] = useState('');
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [page, setPage] = useState(1);
  const [workspace, setWorkspace] = useState('all');
  const [toDelete, setToDelete] = useState<Connection | null>(null);
  const workspaces = useWorkspaces();
  const workspaceOptions = useWorkspaceFilterOptions();
  const { statuses: liveStatuses, connected } = useLiveStatuses();

  const { data: response, isLoading } = useQuery({
    queryKey: [words.plural, page, debouncedSearch, selectedVHost, workspace],
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const vhostParam = selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const wsParam = workspace !== 'all' ? `&workspace_id=${workspace}` : '';
      const res = await apiFetch(`/api/${words.plural}?page=${page}&limit=${PAGE_SIZE}&search=${encodeURIComponent(debouncedSearch)}${vhostParam}${wsParam}`);
      if (!res.ok) throw new Error(`Failed to fetch ${words.plural}`);
      return res.json() as Promise<{ data: Connection[]; total: number }>;
    },
    staleTime: 30_000,
    refetchInterval: false,
  });
  const items = response?.data || [];
  const total = response?.total || 0;
  const totalPages = Math.ceil(total / PAGE_SIZE);

  const { data: workersResponse } = useQuery({
    queryKey: ['workers-lite'],
    queryFn: async () => {
      const res = await apiFetch('/api/workers?limit=200');
      return res.ok ? res.json() : { data: [], total: 0 };
    },
    staleTime: 60_000,
    refetchInterval: false,
  });
  const workers: Worker[] = (workersResponse as { data: Worker[] })?.data || [];
  const workerName = (id?: string) => (id ? workers.find((w) => w.id === id)?.name || id : null);
  const workspaceName = (id?: string) => (id ? workspaces.find((w) => w.id === id)?.name || id : null);

  // The flows that use each connection: a row says how many, and a delete
  // names the running ones it would stop.
  const { data: workflowsResponse } = useQuery({
    queryKey: ['workflows-for-delete', selectedVHost],
    queryFn: async () => {
      const vhostParam = selectedVHost && selectedVHost !== 'all' ? `&vhost=${selectedVHost}` : '';
      const res = await apiFetch(`/api/workflows?limit=200${vhostParam}`);
      return res.ok ? res.json() : { data: [], total: 0 };
    },
    staleTime: 60_000,
    refetchInterval: false,
  });
  const workflows: Workflow[] = (workflowsResponse as { data: Workflow[] })?.data || [];
  const usersOf = (id: string) => workflows.filter((wf) => wf.nodes?.some((n: any) => n.type === kind && n.ref_id === id));
  const runningUsers = toDelete ? usersOf(toDelete.id).filter((wf) => wf.active) : [];

  const remove = useMutation({
    mutationFn: async (id: string) => {
      await apiFetch(`/api/${words.plural}/${id}`, { method: 'DELETE' });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [words.plural] }),
  });

  return (
    <Stack gap={0}>
      <FilterBar search={{ value: search, onChange: (v) => { setSearch(v); setPage(1); }, placeholder: `Search ${words.plural}` }}>
        <Select
          aria-label="Filter by workspace"
          data={workspaceOptions}
          value={workspace}
          onChange={(v) => { setWorkspace(v || 'all'); setPage(1); }}
          leftSection={<IconFolder size="1rem" stroke={1.5} />}
          allowDeselect={false}
          w={200}
        />
        <LiveStatusIndicator connected={connected} />
      </FilterBar>

      {!isLoading && items.length === 0 ? (
        <Paper withBorder radius={12}>
          <EmptyState
            title={search || workspace !== 'all' ? `No ${words.plural} match` : `No ${words.plural} yet`}
            detail={search || workspace !== 'all' ? 'Try another name or workspace.' : words.lede}
            action={canEdit && !search && workspace === 'all' ? { label: `Add ${kind}`, to: words.newTo } : undefined}
          />
        </Paper>
      ) : (
        <RowList aria-label={words.Plural}>
          {items.map((item) => {
            const live = liveStatusFor(liveStatuses, kind, item.id);
            const state = connectionState(item, live);
            return (
              <Row
                key={item.id}
                icon={words.icon}
                iconTone={state.tone === 'err' ? 'err' : 'default'}
                title={item.name}
                detail={connectionDetail(item, workerName(item.worker_id), workspaceName(item.workspace_id))}
                state={
                  <Group gap="md" wrap="nowrap">
                    <StatePill tone={state.tone}>{state.label}</StatePill>
                    <Text size="sm" c="dimmed" style={{ whiteSpace: 'nowrap' }}>{usedBy(usersOf(item.id).length)}</Text>
                  </Group>
                }
                to={canEdit ? words.editTo : undefined}
                params={{ [words.param]: item.id }}
                action={canEdit ? (
                  <Menu position="bottom-end" withArrow shadow="md">
                    <Menu.Target>
                      <ActionIcon aria-label={`More for ${item.name}`} variant="subtle" color="gray"><IconDots size="1rem" /></ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      <Menu.Item component={Link} to={words.editTo} params={{ [words.param]: item.id } as any} leftSection={<IconEdit size="1rem" />}>Edit</Menu.Item>
                      <Menu.Item color="red" leftSection={<IconTrash size="1rem" />} onClick={() => setToDelete(item)}>Delete</Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                ) : undefined}
              />
            );
          })}
        </RowList>
      )}

      {totalPages > 1 && (
        <Group justify="center" mt="md">
          <Pagination total={totalPages} value={page} onChange={setPage} />
        </Group>
      )}

      <Modal opened={!!toDelete} onClose={() => setToDelete(null)} title={<Text fw={700}>Delete {kind}</Text>} centered>
        <Stack gap="md">
          <Text size="sm">
            Delete <b>{toDelete?.name}</b>? This cannot be undone.
          </Text>
          {runningUsers.length > 0 && (
            <Paper withBorder p="sm" bg="var(--mantine-color-red-light)">
              <Group gap="xs" mb="xs">
                <IconAlertCircle size="1.1rem" color="var(--mantine-color-red-6)" />
                <Text size="sm" fw={600}>These running flows use it and will be turned off</Text>
              </Group>
              <List size="sm" spacing={4} icon={<ThemeIcon color="red" size={16} radius="xl"><IconAlertCircle size="0.8rem" /></ThemeIcon>}>
                {runningUsers.map((wf) => <List.Item key={wf.id}>{wf.name}</List.Item>)}
              </List>
            </Paper>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setToDelete(null)}>Cancel</Button>
            <Button color="red" onClick={() => { if (toDelete) { remove.mutate(toDelete.id); setToDelete(null); } }}>
              Delete {kind}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
