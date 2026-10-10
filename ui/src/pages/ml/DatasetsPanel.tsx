import { useState } from 'react'
import {
  ActionIcon, Alert, Button, Group, Loader, Modal, NumberInput, Paper, ScrollArea, Select, Stack, Table, Text,
  TextInput, Textarea, Title, Tooltip,
} from '@mantine/core'
import { IconAlertCircle, IconDatabase, IconEye, IconTable, IconTrash, IconUpload } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import {
  DATASET_NAME_PATTERN, datasetFromQuery, datasetsKey, deleteDataset, listSQLSources, uploadDataset, useDataset,
  useVHostDatasets,
} from '@/lib/mlModels'

type Dialog = { kind: 'upload' } | { kind: 'query' } | { kind: 'view'; name: string }

const datasetNameRule = 'Letters, digits, "_", "." and "-".'

/**
 * The vhost's datasets: tables of rows a model is trained on, kept on the ML
 * worker. One comes from a CSV or Excel file, or from a query on one of the
 * vhost's database sources; filling one again replaces its rows.
 */
export function DatasetsPanel({ vhost }: { vhost: string }) {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const [dialog, setDialog] = useState<Dialog | undefined>(undefined)
  const { data: datasets = [], isLoading } = useVHostDatasets(vhost)

  const remove = useMutation({
    mutationFn: (name: string) => deleteDataset(vhost, name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) }),
  })
  const askDelete = async (name: string) => {
    const ok = await confirm({
      title: 'Delete dataset',
      message: `Delete ${name} from ${vhost}?`,
      consequence: 'Its rows are gone. Models already trained on it keep working; training on it again fails until it is filled.',
      confirmLabel: 'Delete dataset',
      danger: true,
    })
    if (ok) remove.mutate(name)
  }

  return (
    <Paper p="md" withBorder radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={3} size="h4">Datasets</Title>
            <Text size="sm" c="dimmed">The rows models are trained on. Filling a dataset again replaces its rows.</Text>
          </div>
          <Group gap="xs">
            <Button variant="light" leftSection={<IconUpload size="1rem" />} onClick={() => setDialog({ kind: 'upload' })}>Upload a file</Button>
            <Button variant="light" leftSection={<IconDatabase size="1rem" />} onClick={() => setDialog({ kind: 'query' })}>From a database</Button>
          </Group>
        </Group>
        {remove.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The dataset was not deleted">{(remove.error as Error).message}</Alert>
        )}
        {isLoading ? (
          <Group justify="center" p="md"><Loader size="sm" /></Group>
        ) : datasets.length === 0 ? (
          <EmptyState compact icon={<IconTable size="1.3rem" />} title="No datasets yet"
            description="Upload a CSV or Excel file, or read rows from one of this vhost's databases." />
        ) : (
          <Table.ScrollContainer minWidth={560}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Rows</Table.Th>
                  <Table.Th>Columns</Table.Th>
                  <Table.Th>Updated</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {datasets.map((d) => (
                  <Table.Tr key={d.name}>
                    <Table.Td><Text fw={500} ff="var(--mantine-font-family-monospace)">{d.name}</Text></Table.Td>
                    <Table.Td>{d.rows.toLocaleString()}</Table.Td>
                    <Table.Td><Text size="sm" lineClamp={1}>{d.columns.map((c) => c.name).join(', ')}</Text></Table.Td>
                    <Table.Td><Text size="sm">{new Date(d.updated_at).toLocaleString()}</Text></Table.Td>
                    <Table.Td>
                      <Group justify="flex-end" gap="xs" wrap="nowrap">
                        <Tooltip label="Show rows">
                          <ActionIcon variant="light" radius="md" aria-label={`Show rows of ${d.name}`} onClick={() => setDialog({ kind: 'view', name: d.name })}>
                            <IconEye size="1.1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Delete">
                          <ActionIcon variant="light" color="red" radius="md" aria-label={`Delete dataset ${d.name}`} onClick={() => askDelete(d.name)}>
                            <IconTrash size="1.1rem" />
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      {dialog?.kind === 'upload' && <UploadModal vhost={vhost} onClose={() => setDialog(undefined)} />}
      {dialog?.kind === 'query' && <QueryModal vhost={vhost} onClose={() => setDialog(undefined)} />}
      {dialog?.kind === 'view' && <SampleModal vhost={vhost} name={dialog.name} onClose={() => setDialog(undefined)} />}
    </Paper>
  )
}

function useDatasetName(initial = '') {
  const [name, setName] = useState(initial)
  const [error, setError] = useState<string | null>(null)
  const check = () => {
    const bad = !DATASET_NAME_PATTERN.test(name.trim())
    setError(bad ? `"${name.trim()}" is not a valid name.` : null)
    return !bad
  }
  const input = (
    <TextInput label="Dataset name" description={datasetNameRule} placeholder="customers" required value={name}
      onChange={(e) => { setName(e.currentTarget.value); setError(null) }} error={error} data-autofocus />
  )
  return { name: name.trim(), check, input }
}

function UploadModal({ vhost, onClose }: { vhost: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const ds = useDatasetName()
  const [file, setFile] = useState<File | null>(null)
  const upload = useMutation({
    mutationFn: () => uploadDataset(vhost, ds.name, file as File),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) })
      onClose()
    },
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && file) upload.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Upload a dataset" centered>
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {ds.input}
          <Stack gap={4}>
            <Text size="sm" fw={500}>File</Text>
            <input type="file" aria-label="File" accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
              onChange={(e) => setFile(e.currentTarget.files?.[0] ?? null)} />
            <Text size="xs" c="dimmed">CSV, or Excel .xlsx (the first sheet, with a header row). At most 200 MB.</Text>
          </Stack>
          {upload.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The file was not uploaded">{(upload.error as Error).message}</Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={upload.isPending} disabled={!file}>Upload</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}

function QueryModal({ vhost, onClose }: { vhost: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const ds = useDatasetName()
  const [source, setSource] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [maxRows, setMaxRows] = useState<number | string>('')
  const { data: sources = [], isLoading } = useQuery({
    queryKey: ['ml-sql-sources', vhost],
    queryFn: ({ signal }) => listSQLSources(vhost, signal),
    retry: false,
  })
  const read = useMutation({
    mutationFn: () => datasetFromQuery(vhost, ds.name, source as string, query, typeof maxRows === 'number' ? maxRows : undefined),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) }),
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && source && query.trim()) read.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Read a dataset from a database" centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {ds.input}
          <Select label="Database source" required searchable placeholder={isLoading ? 'Loading…' : sources.length ? 'Choose a source' : 'No database sources in this vhost'}
            data={sources.map((s) => ({ value: s.id, label: s.name }))} value={source} onChange={setSource} />
          <Textarea label="Query" required autosize minRows={4} placeholder="SELECT age, plan, churned FROM customers"
            description="A SELECT, run read-only with the source's own credentials." value={query}
            onChange={(e) => setQuery(e.currentTarget.value)} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <NumberInput label="At most this many rows" placeholder="1000000" min={1} value={maxRows} onChange={setMaxRows} maw={260} />
          {read.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="No rows were read">{(read.error as Error).message}</Alert>
          )}
          {read.data && (
            <Alert color="green" title="Dataset filled">Read {read.data.rows.toLocaleString()} rows into {read.data.name}.</Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{read.data ? 'Close' : 'Cancel'}</Button>
            <Button type="submit" loading={read.isPending} disabled={!source || !query.trim()}>Read rows</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}

function SampleModal({ vhost, name, onClose }: { vhost: string; name: string; onClose: () => void }) {
  const { data, isLoading, error } = useDataset(vhost, name)
  const cols = data?.columns ?? []
  return (
    <Modal opened onClose={onClose} title={`First rows of ${name}`} centered size="xl">
      {isLoading ? (
        <Group justify="center" p="lg"><Loader size="sm" /></Group>
      ) : error ? (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The rows could not be read">{(error as Error).message}</Alert>
      ) : (
        <Stack gap="xs">
          <Text size="sm" c="dimmed">{data?.rows.toLocaleString()} rows; {cols.map((c) => `${c.name} (${c.type})`).join(', ')}</Text>
          <ScrollArea>
            <Table striped fz="xs" withTableBorder>
              <Table.Thead><Table.Tr>{cols.map((c) => <Table.Th key={c.name}>{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
              <Table.Tbody>
                {(data?.sample ?? []).map((row, i) => (
                  <Table.Tr key={i}>{cols.map((c) => <Table.Td key={c.name}>{String(row[c.name] ?? '')}</Table.Td>)}</Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Stack>
      )}
    </Modal>
  )
}
