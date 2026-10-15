import { useState } from 'react'
import { Alert, Button, Group, Modal, Stack, Text, TextInput } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { datasetsKey, useDataset, type DatasetInfo } from '@/lib/mlModels'
import { clearDatasetRefresh, putDatasetRefresh, type DatasetRefresh } from '@/lib/mlRecommend'
import { EMPTY_SOURCE_QUERY, SourceQueryFields, sourceQueryReady } from './datasetForms'

const when = (t: string | undefined) => (t && !t.startsWith('0001-01-01') ? new Date(t).toLocaleString() : 'never')

/**
 * Refills a dataset from a database query on a cron schedule, replacing its
 * rows each time, so a retraining uses fresh data. Shows how the last refresh
 * went. A server older than refreshes shows no refresh and refuses the save.
 */
export function RefreshModal({ vhost, name, onClose }: { vhost: string; name: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const { data: info } = useDataset(vhost, name)
  const current = (info as (DatasetInfo & { refresh?: DatasetRefresh }) | undefined)?.refresh
  const [schedule, setSchedule] = useState<string | null>(null)
  const [read, setRead] = useState(EMPTY_SOURCE_QUERY)
  const cron = schedule ?? current?.schedule ?? '0 5 * * *'
  const done = () => {
    queryClient.invalidateQueries({ queryKey: ['ml-dataset', vhost, name] })
    queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) })
  }
  const save = useMutation({
    mutationFn: () => putDatasetRefresh(vhost, name, {
      schedule: cron.trim(), source_id: read.source as string, query: read.query,
      max_rows: typeof read.maxRows === 'number' ? read.maxRows : 0,
    }),
    onSuccess: done,
  })
  const stop = useMutation({ mutationFn: () => clearDatasetRefresh(vhost, name), onSuccess: done })
  const ready = !!cron.trim() && sourceQueryReady(read)
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ready) save.mutate()
  }
  return (
    <Modal opened onClose={onClose} title={`Refresh ${name} on a schedule`} centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {current ? (
            <Stack gap={2}>
              <Text size="sm">Refreshes on <Text span ff="var(--mantine-font-family-monospace)">{current.schedule}</Text>.</Text>
              <Text size="sm" c="dimmed">Last run: {when(current.last_run)}. Next run: {when(current.next_run)}.</Text>
              {current.last_error && (
                <Alert color="red" variant="light" icon={<IconAlertCircle size="1rem" />} title="The last refresh failed">{current.last_error}</Alert>
              )}
            </Stack>
          ) : (
            <Text size="sm" c="dimmed">Each refresh runs the query and replaces the dataset's rows with what it returns.</Text>
          )}
          <TextInput label="Schedule" required description='When to refresh, as cron: "0 5 * * *" is every day at 05:00.'
            value={cron} onChange={(e) => setSchedule(e.currentTarget.value)} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <SourceQueryFields vhost={vhost} value={read} onChange={setRead} placeholder="SELECT cust_id, sku, order_date, qty, total FROM order_lines" />
          {(save.error || stop.error) && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The schedule was not changed">
              {((save.error || stop.error) as Error).message}
            </Alert>
          )}
          {save.isSuccess && <Alert color="green" title="Saved">{name} refreshes on {cron.trim()}.</Alert>}
          {stop.isSuccess && <Alert color="green" title="Stopped">{name} no longer refreshes by itself.</Alert>}
          <Group justify="flex-end">
            {current && (
              <Button variant="subtle" color="red" loading={stop.isPending} onClick={() => stop.mutate()}>Stop refreshing</Button>
            )}
            <Button variant="default" onClick={onClose}>{save.isSuccess ? 'Close' : 'Cancel'}</Button>
            <Button type="submit" loading={save.isPending} disabled={!ready}>Save</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
