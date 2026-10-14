import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Badge, Box, Button, Checkbox, Code, Drawer, Group, Paper, ScrollArea, Select, SimpleGrid, Stack, Table, Tabs, Text,
  TextInput, Title, Tooltip,
} from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { IconCopy, IconDownload, IconMail, IconRefresh, IconSearch } from '@tabler/icons-react'
import { apiFetch } from '@/api'
import { getSessionRole } from '@/auth/session'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { useVHost } from '@/context/VHostContext'
import {
  STATUS_LABEL, deadLetterQuery, decodePayload, failedAtLabel, prettyJSON,
  type DeadLetter, type DeadLetterFilters, type DeadLetterStats, type ReplayOutcome, type When,
} from '@/lib/deadLetters'
import { ReplayDeadLetterModal } from './ReplayDeadLetterModal'

const PAGE_SIZE = 25
// The server replays at most this many in one request.
const MAX_BULK_REPLAY = 50

const STATUS_COLOR: Record<DeadLetter['status'], string> = { queued: 'orange', replayed: 'green', discarded: 'gray' }

interface WorkflowSummary {
  id: string
  name: string
  nodes?: Array<{ id: string; ref_id?: string; config?: Record<string, unknown> }>
}

// nodeNames maps each node id, and each sink's id, to the label it has in the
// editor, so "Node: normalize" reads "Node: Normalize order".
function nodeNames(workflows: WorkflowSummary[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const wf of workflows) {
    for (const n of wf.nodes || []) {
      const label = typeof n.config?.label === 'string' && n.config.label ? n.config.label : ''
      if (!label) continue
      out[n.id] = label
      if (n.ref_id) out[n.ref_id] = label
    }
  }
  return out
}

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

// DeadLettersPage lists every message a workflow parked after its retries ran
// out, whatever its dead-letter sink, and replays or discards them.
export function DeadLettersPage() {
  const qc = useQueryClient()
  const confirm = useConfirm()
  const { selectedVHost } = useVHost()
  const role = getSessionRole()
  const canChange = role === 'Administrator' || role === 'Editor'

  const [search, setSearch] = useState('')
  const [debouncedSearch] = useDebouncedValue(search, 300)
  // A mail about a workflow's dead letters links here with its workflow_id.
  const [workflowId, setWorkflowId] = useState<string | null>(() => new URLSearchParams(window.location.search).get('workflow_id'))
  const [where, setWhere] = useState<string | null>(null)
  const [whenFilter, setWhenFilter] = useState<When>('any')
  const [status, setStatus] = useState<DeadLetterFilters['status']>('queued')
  const [page, setPage] = useState(0)
  const [selected, setSelected] = useState<string[]>([])
  const [openID, setOpenID] = useState<string | null>(null)
  const [replaying, setReplaying] = useState(false)

  const filters: DeadLetterFilters = { search: debouncedSearch, workflowId, where, when: whenFilter, status, vhost: selectedVHost }
  // A new filter starts again at the first page with nothing selected.
  const filterKey = JSON.stringify([debouncedSearch, workflowId, where, whenFilter, status, selectedVHost])
  const [seenFilterKey, setSeenFilterKey] = useState(filterKey)
  if (seenFilterKey !== filterKey) {
    setSeenFilterKey(filterKey)
    setPage(0)
    setSelected([])
  }

  const { data: workflowsResp } = useQuery<{ data: WorkflowSummary[] }>({
    queryKey: ['workflows', 'dead-letter-names', selectedVHost],
    queryFn: async () => (await apiFetch(`/api/workflows?limit=1000${selectedVHost && selectedVHost !== 'all' ? `&vhost=${encodeURIComponent(selectedVHost)}` : ''}`)).json(),
  })
  const workflows = useMemo(() => workflowsResp?.data ?? [], [workflowsResp])
  const names = useMemo(() => nodeNames(workflows), [workflows])

  const { data: stats } = useQuery<DeadLetterStats>({
    queryKey: ['dead-letters', 'stats', selectedVHost],
    queryFn: async () => (await apiFetch(`/api/dead-letters/stats${selectedVHost && selectedVHost !== 'all' ? `?vhost=${encodeURIComponent(selectedVHost)}` : ''}`)).json(),
  })

  const listQuery = deadLetterQuery(filters, page, PAGE_SIZE).toString()
  const { data: list, isFetching, refetch } = useQuery<{ data: DeadLetter[]; total: number }>({
    queryKey: ['dead-letters', 'list', listQuery],
    queryFn: async () => (await apiFetch(`/api/dead-letters?${listQuery}`)).json(),
  })
  const rows = useMemo(() => list?.data ?? [], [list])
  const total = list?.total ?? 0

  const { data: open } = useQuery<DeadLetter>({
    queryKey: ['dead-letters', 'one', openID],
    enabled: !!openID,
    queryFn: async () => (await apiFetch(`/api/dead-letters/${encodeURIComponent(openID!)}`)).json(),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['dead-letters'] })

  const report = (verb: string, results: ReplayOutcome[]) => {
    const ok = results.filter((r) => r.status === verb).length
    const rest = results.length - ok
    notifications.show({
      color: rest ? 'yellow' : 'green',
      title: `${ok} ${verb}`,
      message: rest ? `${rest} could not be ${verb}: ${[...new Set(results.filter((r) => r.status !== verb).map((r) => r.error || r.status.replace('_', ' ')))].join(', ')}` : undefined,
    })
  }

  const discard = useMutation({
    mutationFn: async (ids: string[]) => {
      const res = await apiFetch('/api/dead-letters/discard', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }),
      })
      return (await res.json()).results as ReplayOutcome[]
    },
    onSuccess: (results) => { report('discarded', results); setSelected([]); refresh() },
  })

  const replayMany = useMutation({
    mutationFn: async (ids: string[]) => {
      const res = await apiFetch('/api/dead-letters/replay', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }),
      })
      return (await res.json()).results as ReplayOutcome[]
    },
    onSuccess: (results) => { report('replayed', results); setSelected([]); refresh() },
  })

  const askDiscard = async (ids: string[]) => {
    const ok = await confirm({
      title: ids.length === 1 ? 'Discard this message' : `Discard ${ids.length} messages`,
      message: 'They leave the queue on this page. The copy in the workflow\'s dead-letter sink is not touched.',
      confirmLabel: 'Discard',
    })
    if (ok) discard.mutate(ids)
  }

  const exportCSV = (ids?: string[]) => {
    const q = ids?.length ? `ids=${ids.map(encodeURIComponent).join(',')}` : deadLetterQuery(filters, 0, PAGE_SIZE).toString()
    window.open(`/api/dead-letters/export?${q}`, '_blank', 'noopener')
  }

  const workflowOptions = workflows.map((w) => ({ value: w.id, label: w.name || w.id }))
  const whereOptions = useMemo(() => {
    const seen = new Map<string, string>()
    const wf = workflows.find((w) => w.id === workflowId)
    for (const n of wf?.nodes || []) seen.set(n.id, `Node: ${names[n.id] || n.id}`)
    for (const r of rows) {
      const key = r.node_id || r.sink_id
      if (key && !seen.has(key)) seen.set(key, failedAtLabel(r, names))
    }
    return [...seen].map(([value, label]) => ({ value, label }))
  }, [workflows, workflowId, rows, names])

  const allOnPage = rows.length > 0 && rows.every((r) => selected.includes(r.id))
  const togglePage = () => setSelected(allOnPage ? selected.filter((id) => !rows.some((r) => r.id === id)) : [...new Set([...selected, ...rows.map((r) => r.id)])])
  const toggle = (id: string) => setSelected(selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id])

  const payload = decodePayload(open?.payload)
  const openFailedAt = open ? failedAtLabel(open, names) : ''

  return (
    <Stack gap="lg" p="md">
      <Group justify="space-between" align="flex-start">
        <Box>
          <Title order={2}>Dead letters</Title>
          <Text c="dimmed" size="sm">Messages every workflow parked after its retries ran out. Replay them once the cause is fixed, or discard them.</Text>
        </Box>
        <Group gap="xs">
          <Button variant="default" leftSection={<IconDownload size={16} />} onClick={() => exportCSV()}>Export CSV</Button>
          <Button variant="default" leftSection={<IconMail size={16} />} component="a" href="/settings">Mail settings</Button>
        </Group>
      </Group>

      <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}>
        <StatCard label="In the queue" value={stats?.queued} />
        <StatCard label="Parked in the last 24 hours" value={stats?.last_24h} />
        <Paper withBorder p="md" radius="md">
          <Text size="xs" c="dimmed">Most common error</Text>
          <Text fw={600} lineClamp={2} title={stats?.top_error}>{stats?.top_error || 'None waiting'}</Text>
          {!!stats?.top_error_count && <Text size="xs" c="dimmed">{stats.top_error_count.toLocaleString()} messages</Text>}
        </Paper>
        <StatCard label="Replayed this week" value={stats?.replayed_this_week} />
      </SimpleGrid>

      <Group gap="sm" align="flex-end" wrap="wrap">
        <TextInput label="Search" placeholder="Error, message id, node or sink" leftSection={<IconSearch size={14} />} value={search}
          onChange={(e) => setSearch(e.currentTarget.value)} w={260} />
        <Select label="Workflow" placeholder="All workflows" data={workflowOptions} value={workflowId} onChange={(v) => { setWorkflowId(v); setWhere(null) }}
          clearable searchable w={220} />
        <Select label="Failed at" placeholder="Any node or sink" data={whereOptions} value={where} onChange={setWhere} clearable searchable w={220} />
        <Select label="When" data={[{ value: '24h', label: 'Last 24 hours' }, { value: '7d', label: 'Last 7 days' }, { value: 'any', label: 'Any time' }]}
          value={whenFilter} onChange={(v) => setWhenFilter((v as When) || 'any')} allowDeselect={false} w={150} />
        <Select label="Status" data={[{ value: 'queued', label: 'Waiting' }, { value: 'replayed', label: 'Replayed' }, { value: 'discarded', label: 'Discarded' }, { value: 'all', label: 'All' }]}
          value={status} onChange={(v) => setStatus((v as DeadLetterFilters['status']) || 'queued')} allowDeselect={false} w={140} />
        <Tooltip label="Refresh dead letters">
          <Button variant="subtle" onClick={() => refetch()} loading={isFetching} aria-label="Refresh dead letters"><IconRefresh size={16} /></Button>
        </Tooltip>
      </Group>

      {canChange && selected.length > 0 && (
        <Paper withBorder p="xs" radius="md">
          <Group justify="space-between">
            <Text size="sm" fw={600}>{selected.length} selected</Text>
            <Group gap="xs">
              <Tooltip label="Replay at most 50 at once" disabled={selected.length <= MAX_BULK_REPLAY}>
                <Button size="xs" onClick={() => replayMany.mutate(selected)} loading={replayMany.isPending} disabled={selected.length > MAX_BULK_REPLAY}>
                  Replay {selected.length}
                </Button>
              </Tooltip>
              <Button size="xs" variant="default" onClick={() => exportCSV(selected)}>Export</Button>
              <Button size="xs" variant="default" color="red" onClick={() => askDiscard(selected)}>Discard</Button>
            </Group>
          </Group>
        </Paper>
      )}

      <Paper withBorder radius="md">
        <ScrollArea>
          <Table highlightOnHover verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                {canChange && (
                  <Table.Th w={40}><Checkbox aria-label="Select every message on this page" checked={allOnPage} onChange={togglePage} /></Table.Th>
                )}
                <Table.Th>Parked</Table.Th>
                <Table.Th>Workflow</Table.Th>
                <Table.Th>Failed at</Table.Th>
                <Table.Th>Error</Table.Th>
                <Table.Th>Status</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((r) => (
                <Table.Tr key={r.id} bg={openID === r.id ? 'var(--mantine-color-blue-light)' : undefined}>
                  {canChange && (
                    <Table.Td><Checkbox aria-label={`Select ${r.message_id}`} checked={selected.includes(r.id)} onChange={() => toggle(r.id)} /></Table.Td>
                  )}
                  <Table.Td>
                    <Button variant="subtle" size="compact-sm" onClick={() => setOpenID(r.id)} aria-label={`Open message ${r.message_id}`}>
                      {when(r.failed_at)}
                    </Button>
                  </Table.Td>
                  <Table.Td>{r.workflow_name || r.workflow_id}</Table.Td>
                  <Table.Td>{failedAtLabel(r, names)}</Table.Td>
                  <Table.Td maw={420}><Text size="sm" lineClamp={2}>{r.error}</Text></Table.Td>
                  <Table.Td><Badge color={STATUS_COLOR[r.status]} variant="light">{STATUS_LABEL[r.status]}</Badge></Table.Td>
                </Table.Tr>
              ))}
              {rows.length === 0 && (
                <Table.Tr>
                  <Table.Td colSpan={canChange ? 6 : 5}>
                    <Text c="dimmed" ta="center" py="lg">{isFetching ? 'Loading…' : 'No dead letters match these filters.'}</Text>
                  </Table.Td>
                </Table.Tr>
              )}
            </Table.Tbody>
          </Table>
        </ScrollArea>
        <Group justify="space-between" p="sm">
          <Text size="sm" c="dimmed">
            {total === 0 ? 'Nothing to show' : `Showing ${page * PAGE_SIZE + 1}–${Math.min(total, (page + 1) * PAGE_SIZE)} of ${total.toLocaleString()}`}
          </Text>
          <Group gap="xs">
            <Button size="xs" variant="default" disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</Button>
            <Button size="xs" variant="default" disabled={(page + 1) * PAGE_SIZE >= total} onClick={() => setPage(page + 1)}>Next</Button>
          </Group>
        </Group>
      </Paper>

      <Drawer opened={!!openID} onClose={() => setOpenID(null)} position="right" size="xl"
        title={open ? <Group gap="xs"><Text fw={700}>Message {open.message_id}</Text><Badge color={STATUS_COLOR[open.status]} variant="light">{STATUS_LABEL[open.status]}</Badge></Group> : 'Message'}>
        {open && (
          <Stack gap="md">
            <Text size="sm" c="dimmed">{open.workflow_name || open.workflow_id} · {openFailedAt}</Text>
            <Alert color="red" variant="light" title="Error"><Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{open.error}</Text></Alert>
            <SimpleGrid cols={2} spacing="xs">
              <Detail label="Parked" value={`${when(open.failed_at)}`} />
              <Detail label="Operation" value={open.origin?.operation || '—'} />
              <Detail label="Table" value={[open.origin?.schema, open.origin?.table].filter(Boolean).join('.') || '—'} />
              <Detail label="Size" value={`${open.payload_size.toLocaleString()} bytes${open.truncated ? ' (kept in part)' : ''}`} />
            </SimpleGrid>
            <Button variant="subtle" size="compact-sm" component="a" href={`/workflows/${encodeURIComponent(open.workflow_id)}/edit`} w="fit-content">
              Open the workflow
            </Button>

            <Tabs defaultValue="payload">
              <Tabs.List>
                <Tabs.Tab value="payload">Payload</Tabs.Tab>
                <Tabs.Tab value="before">Before / after</Tabs.Tab>
                <Tabs.Tab value="metadata">Metadata</Tabs.Tab>
                <Tabs.Tab value="history">History</Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="payload" pt="sm">
                <Code block>{prettyJSON(payload) || '(empty)'}</Code>
              </Tabs.Panel>
              <Tabs.Panel value="before" pt="sm">
                <SimpleGrid cols={2}>
                  <Box><Text size="xs" c="dimmed" mb={4}>Before</Text><Code block>{prettyJSON(decodePayload(open.origin?.before)) || '(none)'}</Code></Box>
                  <Box><Text size="xs" c="dimmed" mb={4}>After</Text><Code block>{prettyJSON(payload) || '(empty)'}</Code></Box>
                </SimpleGrid>
              </Tabs.Panel>
              <Tabs.Panel value="metadata" pt="sm">
                <Table withTableBorder>
                  <Table.Tbody>
                    {Object.entries(open.metadata || {}).sort().map(([k, v]) => (
                      <Table.Tr key={k}><Table.Td><Code>{k}</Code></Table.Td><Table.Td>{v}</Table.Td></Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Tabs.Panel>
              <Tabs.Panel value="history" pt="sm">
                <Stack gap={6}>
                  {(open.history || []).map((h, i) => (
                    <Text key={i} size="sm">
                      <b>{when(h.at)}</b> · {h.action.replace('_', ' ')}{h.by ? ` by ${h.by}` : ''}{h.detail ? ` · ${h.detail}` : ''}
                    </Text>
                  ))}
                </Stack>
              </Tabs.Panel>
            </Tabs>

            <Group justify="space-between">
              {canChange && open.status === 'queued' ? (
                <Button variant="default" color="red" onClick={() => askDiscard([open.id])}>Discard</Button>
              ) : <span />}
              <Group gap="xs">
                <Button variant="default" leftSection={<IconCopy size={14} />}
                  onClick={() => { void navigator.clipboard?.writeText(prettyJSON(payload)); notifications.show({ message: 'Copied the payload' }) }}>
                  Copy JSON
                </Button>
                {canChange && open.status === 'queued' && (
                  <>
                    <Button variant="default" onClick={() => setReplaying(true)}>Edit and replay</Button>
                    <Button onClick={() => replayMany.mutate([open.id])} loading={replayMany.isPending} disabled={open.truncated}>Replay</Button>
                  </>
                )}
              </Group>
            </Group>
          </Stack>
        )}
      </Drawer>

      {replaying && open && (
        <ReplayDeadLetterModal
          deadLetter={open}
          payload={payload}
          failedAt={openFailedAt}
          onClose={() => setReplaying(false)}
          onReplayed={(outcome) => { setReplaying(false); report('replayed', [outcome]); refresh() }}
        />
      )}
    </Stack>
  )
}

function StatCard({ label, value }: { label: string; value?: number }) {
  return (
    <Paper withBorder p="md" radius="md">
      <Text size="xs" c="dimmed">{label}</Text>
      <Text fw={700} size="xl">{value === undefined ? '—' : value.toLocaleString()}</Text>
    </Paper>
  )
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Text size="xs" c="dimmed">{label}</Text>
      <Text size="sm">{value}</Text>
    </Box>
  )
}
