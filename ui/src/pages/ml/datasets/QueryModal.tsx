import { useState } from 'react'
import { Alert, Button, Group, Modal, Stack } from '@mantine/core'
import { IconAlertCircle, IconChartBar } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { datasetFromQuery, datasetsKey } from '@/lib/mlModels'
import { QueryHelp } from '../dataNeeds'
import { EMPTY_SOURCE_QUERY, SourceQueryFields, sourceQueryReady, useDatasetName } from './datasetForms'

/** Reads a dataset from one of the vhost's databases; `onAnalyze` checks what was read. */
export function QueryModal({ vhost, onClose, onAnalyze }: { vhost: string; onClose: () => void; onAnalyze?: (name: string) => void }) {
  const queryClient = useQueryClient()
  const ds = useDatasetName()
  const [read, setRead] = useState(EMPTY_SOURCE_QUERY)
  const fill = useMutation({
    mutationFn: () => datasetFromQuery(vhost, ds.name, read.source as string, read.query,
      typeof read.maxRows === 'number' ? read.maxRows : undefined),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) }),
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && sourceQueryReady(read)) fill.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Read a dataset from a database" centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {ds.input}
          <SourceQueryFields vhost={vhost} value={read} onChange={setRead} placeholder="SELECT age, plan, churned FROM customers"
            help={<QueryHelp onUseQuery={(query) => setRead((r) => ({ ...r, query }))} />} />
          {fill.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="No rows were read">{(fill.error as Error).message}</Alert>
          )}
          {fill.data && (
            <Alert color="green" title="Dataset filled">
              Read {fill.data.rows.toLocaleString()} rows into {fill.data.name}. Check the data next: it shows what each
              column holds, what to fix, and which models it suits.
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{fill.data ? 'Close' : 'Cancel'}</Button>
            {fill.data && onAnalyze ? (
              <Button leftSection={<IconChartBar size="1rem" />} onClick={() => onAnalyze(fill.data.name)}>Check this data</Button>
            ) : (
              <Button type="submit" loading={fill.isPending} disabled={!sourceQueryReady(read)}>Read rows</Button>
            )}
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
