import { useEffect, useState } from 'react';
import {
  ActionIcon, Badge, Box, Button, Code, Divider, Group, Menu, Modal, Pagination, Paper, Popover, ScrollArea, Select, Stack, Text, TextInput,
} from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearch } from '@tanstack/react-router';
import { IconActivity, IconAlertTriangle, IconCircleCheck, IconDots, IconFilter, IconRefresh, IconTrash } from '@tabler/icons-react';
import { apiFetch } from '@/api';
import { useConfirm } from '@/components/common/ConfirmProvider';
import { EmptyState, FilterBar, Row, RowList, StatePill } from '@/components/rows';
import { formatDateTime } from '@/utils/dateUtils';
import { FEED_PAGE_SIZE, feedParams, knownFlow, logDetail, logTitle, logTone, type FeedView, type Names } from './activityModel';

const LEVEL_COLOR: Record<string, string> = { ERROR: 'red', WARN: 'yellow', INFO: 'blue', DEBUG: 'gray' };

function shortTime(ts: string): { label: string; full: string } {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return { label: '', full: '' };
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  return { label: sameDay ? time : `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}`, full: formatDateTime(ts) };
}

function pretty(value: string): string {
  try {
    const t = value.trim();
    if (t.startsWith('{') || t.startsWith('[')) return JSON.stringify(JSON.parse(t), null, 2);
  } catch { /* not JSON: show it as it is */ }
  return value;
}

/** A name lookup for ids in the feed, from the lists the other pages already load. */
function useNames(): Names {
  const list = (path: string) => async () => {
    const res = await apiFetch(`/api/${path}?limit=200`);
    return res.ok ? res.json() : { data: [] };
  };
  const toMap = (resp: any): Record<string, string> =>
    Object.fromEntries(((resp?.data as any[]) || []).map((x) => [x.id, x.name]));
  const { data: wf } = useQuery({ queryKey: ['activity-names', 'workflows'], queryFn: list('workflows'), staleTime: 60_000 });
  const { data: src } = useQuery({ queryKey: ['activity-names', 'sources'], queryFn: list('sources'), staleTime: 60_000 });
  const { data: snk } = useQuery({ queryKey: ['activity-names', 'sinks'], queryFn: list('sinks'), staleTime: 60_000 });
  return { workflows: toMap(wf), sources: toMap(src), sinks: toMap(snk) };
}

/**
 * Activity, newest first: one row per log entry, named after the flow or
 * connection it is about, with what happened under it. Problems and
 * Warnings narrow it to errors or warnings; the flow, level and action
 * filters of the old table are behind Filters. A row opens the whole entry.
 */
export function ActivityFeed() {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const searchParams = useSearch({ strict: false }) as Record<string, any>;
  const [view, setView] = useState<FeedView>('everything');
  const [search, setSearch] = useState('');
  const [workflowId, setWorkflowId] = useState<string>(searchParams.workflow_id || '');
  const [level, setLevel] = useState<string | null>(null);
  const [action, setAction] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<any>(null);
  const [debouncedSearch] = useDebouncedValue(search, 300);
  const [debouncedWorkflowId] = useDebouncedValue(workflowId, 300);
  const names = useNames();

  // A link from a flow (?workflow_id=) opens the feed on that flow.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setWorkflowId(searchParams.workflow_id || '');
  }, [searchParams.workflow_id]);

  const params = feedParams({ view, search: debouncedSearch, page, workflowId: debouncedWorkflowId, level, action });
  const { data, isFetching, isLoading } = useQuery({
    queryKey: ['logs', params],
    queryFn: async () => {
      const res = await apiFetch(`/api/logs?${params}`);
      if (!res.ok) throw new Error('Failed to fetch logs');
      return res.json() as Promise<{ data: any[]; total: number }>;
    },
    refetchInterval: 5000,
    placeholderData: keepPreviousData,
  });
  const logs = Array.isArray(data?.data) ? data!.data : [];
  const totalPages = Math.ceil((data?.total || 0) / FEED_PAGE_SIZE);
  const narrowed = Boolean(debouncedSearch || workflowId || level || action || view !== 'everything');
  const extraFilters = [workflowId, level, action].filter(Boolean).length;

  const clear = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/logs', { method: 'DELETE' });
      if (!res.ok) throw new Error('Failed to clear logs');
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['logs'] }),
  });

  const reset = (fn: () => void) => { fn(); setPage(1); };

  return (
    <Stack gap={0}>
      <FilterBar
        search={{ value: search, onChange: (v) => reset(() => setSearch(v)), placeholder: 'Search by flow, connection or message' }}
        filter={{
          value: view,
          onChange: (v) => reset(() => setView(v as FeedView)),
          options: [
            { value: 'everything', label: 'Everything' },
            { value: 'problems', label: 'Problems' },
            { value: 'warnings', label: 'Warnings' },
          ],
        }}
      >
        <Popover position="bottom-end" withArrow shadow="md" width={280}>
          <Popover.Target>
            <Button variant="default" leftSection={<IconFilter size="1rem" />}>
              Filters{extraFilters ? ` (${extraFilters})` : ''}
            </Button>
          </Popover.Target>
          <Popover.Dropdown>
            <Stack gap="sm">
              <TextInput label="Flow ID" placeholder="Any flow" value={workflowId} onChange={(e) => { const v = e.currentTarget.value; reset(() => setWorkflowId(v)); }} />
              <Select label="Level" placeholder="Any level" data={['INFO', 'WARN', 'ERROR', 'DEBUG']} value={level} onChange={(v) => reset(() => setLevel(v))} clearable comboboxProps={{ withinPortal: false }} />
              <Select
                label="Action"
                placeholder="Any action"
                data={['create', 'update', 'delete', 'start', 'stop', 'drain_dlq', 'write']}
                value={action}
                onChange={(v) => reset(() => setAction(v))}
                clearable
                comboboxProps={{ withinPortal: false }}
              />
            </Stack>
          </Popover.Dropdown>
        </Popover>
        <Menu position="bottom-end" withArrow shadow="md">
          <Menu.Target>
            <ActionIcon aria-label="More" variant="default" size={36} loading={isFetching && !isLoading}><IconDots size="1rem" /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconRefresh size="1rem" />} onClick={() => queryClient.invalidateQueries({ queryKey: ['logs'] })}>Refresh</Menu.Item>
            <Menu.Item
              color="red"
              leftSection={<IconTrash size="1rem" />}
              onClick={async () => {
                if (await confirm({ title: 'Clear all logs', message: 'Delete every stored log entry?', consequence: 'Historical diagnostics are lost and cannot be recovered.', confirmLabel: 'Clear logs', danger: true })) clear.mutate();
              }}
            >
              Clear all logs
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </FilterBar>

      {!isLoading && logs.length === 0 ? (
        <Paper withBorder radius={12}>
          <EmptyState
            title={narrowed ? 'Nothing matches' : 'Nothing has happened yet'}
            detail={narrowed ? 'Try another search or filter.' : 'Messages received, changed and delivered, and every change to a flow, show up here.'}
          />
        </Paper>
      ) : (
        <RowList aria-label="Activity">
          {logs.map((log) => {
            const tone = logTone(log);
            const time = shortTime(log.timestamp);
            return (
              <Row
                key={log.id ?? `${log.timestamp}-${log.message}`}
                lead={<Text size="xs" c="dimmed" w={64} title={time.full} style={{ fontVariantNumeric: 'tabular-nums' }}>{time.label}</Text>}
                icon={tone === 'err' || tone === 'warn' ? IconAlertTriangle : tone === 'ok' ? IconCircleCheck : IconActivity}
                iconTone={tone === 'err' ? 'err' : 'default'}
                title={logTitle(log, names)}
                detail={logDetail(log)}
                state={tone === 'warn' ? <StatePill tone="warn">Warning</StatePill> : undefined}
                onOpen={() => setSelected(log)}
                action={tone === 'err' && knownFlow(log, names) ? (
                  <Button component={Link} to="/workflows/$id" params={{ id: log.workflow_id } as any} search={{ tab: 'logs' } as any} size="xs" variant="default">
                    Open flow
                  </Button>
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

      <Modal opened={!!selected} onClose={() => setSelected(null)} title={<Text fw={700}>{selected ? logTitle(selected, names) : ''}</Text>} size="xl">
        {selected && (
          <Stack gap="md">
            <Group justify="space-between">
              <Text size="sm">{formatDateTime(selected.timestamp)}</Text>
              <Group gap="xs">
                {selected.action && <Badge variant="outline" radius="xs" style={{ textTransform: 'none' }}>{selected.action}</Badge>}
                <Badge color={LEVEL_COLOR[selected.level] || 'gray'} variant="light">{selected.level}</Badge>
              </Group>
            </Group>
            <Box>
              <Text size="xs" c="dimmed" fw={700} mb={4}>MESSAGE</Text>
              <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{pretty(selected.message || '')}</Code>
            </Box>
            {selected.action && (
              <Box>
                <Text size="xs" c="dimmed" fw={700} mb={4}>BY</Text>
                <Text size="sm">{selected.username || 'System'}</Text>
              </Box>
            )}
            {(selected.workflow_id || selected.source_id || selected.sink_id) && (
              <Group grow align="flex-start">
                {selected.workflow_id && <Box><Text size="xs" c="dimmed" fw={700}>FLOW</Text><Code block fz="xs">{selected.workflow_id}</Code></Box>}
                {selected.source_id && <Box><Text size="xs" c="dimmed" fw={700}>SOURCE</Text><Code block fz="xs">{selected.source_id}</Code></Box>}
                {selected.sink_id && <Box><Text size="xs" c="dimmed" fw={700}>SINK</Text><Code block fz="xs">{selected.sink_id}</Code></Box>}
              </Group>
            )}
            {selected.data && (
              <>
                <Divider />
                <Box>
                  <Text size="xs" c="dimmed" fw={700} mb={4}>DATA</Text>
                  <ScrollArea.Autosize mah="50vh" type="auto">
                    <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{pretty(selected.data)}</Code>
                  </ScrollArea.Autosize>
                </Box>
              </>
            )}
            <Group justify="flex-end">
              {knownFlow(selected, names) && (
                <Button component={Link} to="/workflows/$id" params={{ id: selected.workflow_id } as any} search={{ tab: 'logs' } as any} variant="default">Open flow</Button>
              )}
              <Button variant="light" onClick={() => setSelected(null)}>Close</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  );
}
