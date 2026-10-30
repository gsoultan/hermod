import { useQuery } from '@tanstack/react-query'
import {
  Alert, Badge, Group, Paper, SimpleGrid, Stack, Table, Text, Title,
} from '@mantine/core'
import { IconAlertCircle, IconAlertTriangle, IconArrowsJoin2 } from '@tabler/icons-react'
import { formatDateTime } from '@/utils/dateUtils'
import { formatDuration } from '@/lib/executions'
import { typeLabel } from '@/utils/txgroup'
import {
  listTransactions, OUTCOMES, transactionsKey,
  type TransactionRun, type WorkflowTransaction,
} from '@/lib/transactions'

/** How often the panel asks again while it is open. Runs arrive as the workflow writes. */
const REFRESH_MS = 10_000

/** How many of the latest runs a transaction shows. The server keeps more. */
const SHOWN_RUNS = 10

/**
 * The runs of the workflow's transactions: the sink nodes that commit
 * together, and Transaction Group nodes. For each, its totals, each database's
 * share, what its log holds now, and its latest runs. Nothing is shown for a
 * workflow without a transaction, so the Runs tab of every other flow stays as
 * it was.
 */
export function TransactionRunsPanel({ workflowId }: { workflowId: string }) {
  const { data, error } = useQuery({
    queryKey: transactionsKey(workflowId),
    queryFn: ({ signal }) => listTransactions(workflowId, signal),
    refetchInterval: REFRESH_MS,
  })

  if (error) {
    return (
      <Alert m="md" mb={0} color="red" icon={<IconAlertCircle size="1rem" />} title="Could not load the transactions">
        {(error as Error).message}
      </Alert>
    )
  }
  if (!data || data.length === 0) return null

  return (
    <Stack gap="md" p="md" pb={0}>
      {data.map((tx) => (
        <TransactionCard key={tx.kind + tx.name + (tx.node_id ?? '')} tx={tx} />
      ))}
    </Stack>
  )
}

function TransactionCard({ tx }: { tx: WorkflowTransaction }) {
  const names = new Map(tx.members.map((m) => [m.id, m.name || m.id]))
  const nameOf = (id: string) => names.get(id) ?? id
  const stats = tx.stats
  const memberStats = new Map((stats.members ?? []).map((m) => [m.id, m]))
  const recent = (stats.recent ?? []).slice(0, SHOWN_RUNS)
  const pending = tx.pending ?? []

  return (
    <Paper withBorder radius="md" p="md" data-testid={`transaction-${tx.name}`}>
      <Stack gap="sm">
        <Group gap="xs" wrap="wrap">
          <IconArrowsJoin2 size="1.1rem" aria-hidden />
          <Title order={5}>Transaction “{tx.name}”</Title>
          <Badge variant="light" color="gray">{tx.kind === 'group' ? 'Transaction Group' : 'Commit together'}</Badge>
          {tx.require_every_member && <Badge variant="light" color="grape">Every database must get a row</Badge>}
        </Group>

        {tx.problem && (
          <Alert color="yellow" icon={<IconAlertTriangle size="1rem" />}>
            {tx.problem}
          </Alert>
        )}

        <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
          <Stat id="committed" label="Committed" value={stats.committed} />
          <Stat id="rolled_back" label="Rolled back" value={stats.rolled_back} />
          <Stat id="refused" label="Refused: missing row" value={stats.refused} />
          <Stat id="pending" label="In doubt now" value={pending.length} warn={pending.length > 0} />
        </SimpleGrid>

        {pending.length > 0 && (
          <Alert color="orange" icon={<IconAlertTriangle size="1rem" />} title="In doubt now">
            <Text size="sm">
              These are prepared and not yet resolved, so their databases still hold the rows&apos; locks. A decided
              commit is finished by recovery when the workflow starts; anything else is rolled back by the reaper once it
              passes its age limit.
            </Text>
            <Stack gap={2} mt="xs" data-testid="pending-list">
              {pending.map((p) => (
                <Text size="sm" key={p.tx_id}>
                  {p.state === 'committing' ? 'Commit decided' : 'No decision yet'} · started {formatDateTime(p.started_at)} ·
                  waiting on {(p.participants ?? []).map(nameOf).join(', ') || 'no database'}
                </Text>
              ))}
            </Stack>
          </Alert>
        )}

        <Table.ScrollContainer minWidth={560}>
          <Table verticalSpacing={4} aria-label="Databases">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Database</Table.Th>
                <Table.Th>Committed</Table.Th>
                <Table.Th>Rolled back</Table.Th>
                <Table.Th>No row</Table.Th>
                <Table.Th>Last error</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {tx.members.map((m) => {
                const s = memberStats.get(m.id)
                return (
                  <Table.Tr key={m.id}>
                    <Table.Td>
                      <Text size="sm" fw={500}>{m.name || m.id}</Text>
                      {m.sink_type && <Text size="xs" c="dimmed">{typeLabel(m.sink_type)}</Text>}
                    </Table.Td>
                    <Table.Td>{s?.committed ?? 0}</Table.Td>
                    <Table.Td>{s?.rolled_back ?? 0}</Table.Td>
                    <Table.Td>{s?.missing ?? 0}</Table.Td>
                    <Table.Td>
                      {s?.last_error ? (
                        <Text size="xs" c="red" lineClamp={2} title={s.last_error}>
                          {s.last_error}
                          {s.last_error_at && <Text span c="dimmed"> · {formatDateTime(s.last_error_at)}</Text>}
                        </Text>
                      ) : (
                        <Text size="xs" c="dimmed">None</Text>
                      )}
                    </Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>

        {recent.length === 0 ? (
          <Text size="sm" c="dimmed">
            No runs yet. Runs appear here once the workflow writes to this transaction.
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={620}>
            <Table verticalSpacing={4} aria-label="Latest runs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>When</Table.Th>
                  <Table.Th>Outcome</Table.Th>
                  <Table.Th>Databases</Table.Th>
                  <Table.Th>Took</Table.Th>
                  <Table.Th>Reason</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {recent.map((run, i) => (
                  <RunRow key={run.at + i} run={run} nameOf={nameOf} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Paper>
  )
}

function Stat({ id, label, value, warn = false }: { id: string; label: string; value: number; warn?: boolean }) {
  return (
    <Paper withBorder radius="sm" p="xs" data-testid={`stat-${id}`}>
      <Text size="xs" c="dimmed">{label}</Text>
      <Text size="lg" fw={700} c={warn ? 'orange' : undefined}>{value.toLocaleString()}</Text>
    </Paper>
  )
}

function RunRow({ run, nameOf }: { run: TransactionRun; nameOf: (id: string) => string }) {
  const outcome = OUTCOMES[run.outcome] ?? { label: run.outcome, color: 'gray' }
  return (
    <Table.Tr>
      <Table.Td><Text size="xs">{formatDateTime(run.at)}</Text></Table.Td>
      <Table.Td><Badge size="sm" variant="light" color={outcome.color}>{outcome.label}</Badge></Table.Td>
      <Table.Td>
        <Text size="xs">
          {run.members.map((m, i) => (
            <Text span key={m.id} c={m.failed || m.missing ? 'red' : undefined} fw={m.failed || m.missing ? 600 : undefined}>
              {i > 0 && ', '}
              {m.missing ? `${nameOf(m.id)}: no row` : `${nameOf(m.id)} ×${m.rows}${m.failed ? ' (failed)' : ''}`}
            </Text>
          ))}
        </Text>
      </Table.Td>
      <Table.Td><Text size="xs">{run.outcome === 'refused' ? 'Not written' : formatDuration(run.duration_ms)}</Text></Table.Td>
      <Table.Td>
        {run.error ? <Text size="xs" c="dimmed" lineClamp={2} title={run.error}>{run.error}</Text> : <Text size="xs" c="dimmed">None</Text>}
      </Table.Td>
    </Table.Tr>
  )
}
