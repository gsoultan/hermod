import { useState } from 'react'
import { Alert, Badge, Button, Group, Loader, Paper, Progress, Select, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { JOB_STATUSES, cancelJob, jobFinished, listJobs, mlJobsKey, type MLJob, type MLJobStatus } from '@/lib/mlModels'

/** Jobs shown per page. */
export const JOBS_PAGE = 20

const KIND_LABEL: Record<string, string> = { predict: 'Prediction', recommend: 'Recommendation', train: 'Training' }

const STATUS: Record<MLJobStatus, { label: string; color: string }> = {
  queued: { label: 'Waiting', color: 'gray' },
  running: { label: 'Running', color: 'blue' },
  succeeded: { label: 'Done', color: 'green' },
  failed: { label: 'Failed', color: 'red' },
  cancelled: { label: 'Stopped', color: 'yellow' },
}

const when = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '')

/** What a job is doing or did, in words. */
function jobWords(j: MLJob): string {
  if (j.status === 'failed') return j.error || 'It failed and gave no reason.'
  if (j.status === 'cancelled') return j.progress_message && j.progress_message !== 'Cancelled' ? `Stopped: ${j.progress_message}` : 'Stopped before it finished.'
  if (j.status === 'succeeded') return `Finished ${when(j.finished_at)}`
  if (j.status === 'queued') return 'Waiting to start'
  return j.cancel_requested ? 'Stopping…' : (j.progress_message || 'Running')
}

/**
 * The vhost's asynchronous jobs: predictions, recommendations and trainings
 * sent with "respond": "async", newest first, with how far each has got and,
 * for one that failed, why.
 */
export function JobsPanel({ vhost, canEdit }: { vhost: string; canEdit: boolean }) {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<string | null>(null)
  const [page, setPage] = useState(0)
  const { data, isLoading, error } = useQuery({
    queryKey: [...mlJobsKey(vhost), status ?? '', page],
    queryFn: ({ signal }) => listJobs(vhost, { status: status ?? undefined, limit: JOBS_PAGE, offset: page * JOBS_PAGE }, signal),
    retry: false,
    // Followed while any job shown is still going.
    refetchInterval: (q) => (q.state.data?.data.some((j) => !jobFinished(j.status)) ? 3000 : false),
  })
  const cancel = useMutation({
    mutationFn: (id: string) => cancelJob(vhost, id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: mlJobsKey(vhost) }),
  })
  const jobs = data?.data ?? []
  const total = data?.total ?? 0

  return (
    <Paper p="md" withBorder radius="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-end">
          <div>
            <Title order={4}>Jobs</Title>
            <Text size="sm" c="dimmed">
              Trainings, and predictions sent with &quot;respond&quot;: &quot;async&quot;. A job and its result are kept for 7 days.
            </Text>
          </div>
          <Select label="Status" placeholder="Any" clearable size="xs" w={160}
            data={JOB_STATUSES.map((s) => ({ value: s, label: STATUS[s].label }))}
            value={status} onChange={(v) => { setStatus(v); setPage(0) }} />
        </Group>
        {isLoading ? (
          <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">Loading jobs…</Text></Group>
        ) : error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The jobs could not be loaded">{(error as Error).message}</Alert>
        ) : jobs.length === 0 ? (
          <Text size="sm" c="dimmed">{status ? 'No jobs with this status.' : 'No jobs yet.'}</Text>
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table verticalSpacing="xs" aria-label="Jobs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Model</Table.Th>
                  <Table.Th>Kind</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th>Progress</Table.Th>
                  <Table.Th>Started</Table.Th>
                  {canEdit && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {jobs.map((j) => (
                  <Table.Tr key={j.id}>
                    <Table.Td><Text size="sm" ff="var(--mantine-font-family-monospace)">{j.model}</Text></Table.Td>
                    <Table.Td><Text size="sm">{KIND_LABEL[j.kind] ?? j.kind}</Text></Table.Td>
                    <Table.Td><Badge variant="light" color={STATUS[j.status]?.color ?? 'gray'}>{STATUS[j.status]?.label ?? j.status}</Badge></Table.Td>
                    <Table.Td maw={360}>
                      {!jobFinished(j.status) && (
                        <Progress value={Math.round(j.progress * 100)} size="sm" mb={4} aria-label={`Progress of ${j.model}`} />
                      )}
                      <Text size="xs" c={j.status === 'failed' ? 'red' : 'dimmed'}>{jobWords(j)}</Text>
                    </Table.Td>
                    <Table.Td><Text size="xs">{when(j.created_at)}{j.created_by ? ` by ${j.created_by}` : ''}</Text></Table.Td>
                    {canEdit && (
                      <Table.Td>
                        {!jobFinished(j.status) && !j.cancel_requested && (
                          <Button size="compact-xs" variant="light" color="red" aria-label={`Stop job ${j.id}`}
                            loading={cancel.isPending && cancel.variables === j.id} onClick={() => cancel.mutate(j.id)}>
                            Stop
                          </Button>
                        )}
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {cancel.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The job was not stopped">{(cancel.error as Error).message}</Alert>
        )}
        {total > JOBS_PAGE && (
          <Group justify="flex-end" gap="xs">
            <Text size="xs" c="dimmed">{page * JOBS_PAGE + 1}–{Math.min(total, (page + 1) * JOBS_PAGE)} of {total}</Text>
            <Button size="compact-xs" variant="default" disabled={page === 0} onClick={() => setPage((p) => p - 1)}>Newer</Button>
            <Button size="compact-xs" variant="default" disabled={(page + 1) * JOBS_PAGE >= total} onClick={() => setPage((p) => p + 1)}>Older</Button>
          </Group>
        )}
      </Stack>
    </Paper>
  )
}
