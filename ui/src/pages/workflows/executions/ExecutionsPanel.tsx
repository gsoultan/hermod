import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import {
  Alert, Badge, Button, Grid, Group, Loader, Stack, Table, Text, Title, UnstyledButton,
} from '@mantine/core'
import { IconAlertCircle, IconChevronLeft, IconChevronRight, IconListDetails } from '@tabler/icons-react'
import { formatDateTime } from '@/utils/dateUtils'
import { executionsKey, formatDuration, formatTokens, listExecutions, statusMeta, type Execution } from '@/lib/executions'
import { ExecutionDetail } from './ExecutionDetail'

const PAGE_SIZE = 20

interface ExecutionsPanelProps {
  workflowId: string
  /** Editors and administrators may replay a run; the server enforces it too. */
  canReplay: boolean
  selectedRunId: string | null
  onSelectRun: (runId: string | null) => void
}

/**
 * A workflow's run history: one row per triggering message, newest first, and
 * the selected run's steps beside it.
 *
 * Pages by cursor, as the server does: "older" is "started before the last row
 * on this page". One cursor per page visited, so going back is a pop.
 */
export function ExecutionsPanel({ workflowId, canReplay, selectedRunId, onSelectRun }: ExecutionsPanelProps) {
  const [cursors, setCursors] = useState<string[]>([])
  const cursor = cursors.length > 0 ? cursors[cursors.length - 1] : null

  const { data, isLoading, isFetching, error } = useQuery({
    queryKey: [...executionsKey(workflowId), 'list', cursor],
    queryFn: ({ signal }) => listExecutions(workflowId, { before: cursor, limit: PAGE_SIZE }, signal),
    placeholderData: keepPreviousData,
  })

  const runs: Execution[] = data?.executions ?? []
  const nextBefore = data?.next_before

  return (
    <Grid gap="md" p="md">
      <Grid.Col span={{ base: 12, lg: 5 }}>
        <Stack gap="sm">
          <Group justify="space-between">
            <Title order={4}>Runs</Title>
            {isFetching && !isLoading && <Loader size="xs" aria-label="Refreshing runs" />}
          </Group>
          {error ? (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="Could not load runs">
              {(error as Error).message}
            </Alert>
          ) : isLoading ? (
            <Group justify="center" p="xl"><Loader size="sm" aria-label="Loading runs" /></Group>
          ) : runs.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="xl">
              No runs recorded yet. Runs appear here once a message flows through the workflow and its trace is kept.
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={520}>
              <Table verticalSpacing="xs" highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Run</Table.Th>
                    <Table.Th>Status</Table.Th>
                    <Table.Th>Duration</Table.Th>
                    <Table.Th>Steps</Table.Th>
                    <Table.Th>Tokens</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {runs.map((run) => {
                    const meta = statusMeta(run.status)
                    const selected = run.run_id === selectedRunId
                    return (
                      <Table.Tr
                        key={run.run_id}
                        aria-selected={selected}
                        bg={selected ? 'var(--mantine-primary-color-light)' : undefined}
                      >
                        <Table.Td maw={200}>
                          <UnstyledButton
                            aria-label={`Open run ${run.run_id}`}
                            onClick={() => onSelectRun(run.run_id)}
                            w="100%"
                          >
                            <Text size="sm" fw={600} truncate ff="monospace">{run.run_id}</Text>
                            <Text size="xs" c="dimmed">{formatDateTime(run.started_at)}</Text>
                          </UnstyledButton>
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={meta.color}>{meta.label}</Badge>
                        </Table.Td>
                        <Table.Td style={{ whiteSpace: 'nowrap' }}>
                          <Text size="sm">{formatDuration(run.duration_ms)}</Text>
                        </Table.Td>
                        <Table.Td style={{ whiteSpace: 'nowrap' }}>
                          <Text size="sm">
                            {run.step_count}
                            {run.error_count > 0 && (
                              <Text span size="xs" c="red"> ({run.error_count} failed)</Text>
                            )}
                          </Text>
                        </Table.Td>
                        <Table.Td style={{ whiteSpace: 'nowrap' }}>
                          <Text size="sm">{formatTokens(run.ai)}</Text>
                        </Table.Td>
                      </Table.Tr>
                    )
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
          <Group justify="space-between">
            <Button
              size="xs"
              variant="default"
              leftSection={<IconChevronLeft size="0.9rem" />}
              disabled={cursors.length === 0 || isFetching}
              onClick={() => setCursors((c) => c.slice(0, -1))}
            >
              Newer runs
            </Button>
            <Text size="xs" c="dimmed">Page {cursors.length + 1}</Text>
            <Button
              size="xs"
              variant="default"
              rightSection={<IconChevronRight size="0.9rem" />}
              disabled={!nextBefore || isFetching}
              onClick={() => nextBefore && setCursors((c) => [...c, nextBefore])}
            >
              Older runs
            </Button>
          </Group>
        </Stack>
      </Grid.Col>
      <Grid.Col span={{ base: 12, lg: 7 }}>
        {selectedRunId ? (
          <ExecutionDetail
            workflowId={workflowId}
            runId={selectedRunId}
            canReplay={canReplay}
            onOpenRun={onSelectRun}
          />
        ) : (
          <Stack align="center" justify="center" py={80} gap="xs">
            <IconListDetails size="2.5rem" color="var(--mantine-color-dimmed)" aria-hidden />
            <Text c="dimmed" size="sm">Select a run to see each step and what it produced.</Text>
          </Stack>
        )}
      </Grid.Col>
    </Grid>
  )
}

