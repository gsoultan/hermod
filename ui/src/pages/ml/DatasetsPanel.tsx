import { useState } from 'react'
import { ActionIcon, Alert, Button, Group, Loader, Paper, Stack, Table, Text, Title, Tooltip } from '@mantine/core'
import { IconAlertCircle, IconChartBar, IconClock, IconDatabase, IconEye, IconPlugConnected, IconSend, IconTable, IconTrash, IconUpload } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import { datasetsKey, deleteDataset, useVHostDatasets } from '@/lib/mlModels'
import type { TrainStart } from '@/lib/mlDataProfile'
import { AnalysisModal, type AnalysisTab } from './datasets/AnalysisModal'
import { QueryModal } from './datasets/QueryModal'
import { RefreshModal } from './datasets/RefreshModal'
import { UploadModal } from './datasets/UploadModal'
import { SendToMimirModal } from './datasets/SendToMimirModal'

// UploadModal is where it always was for the training steps that open it.
export { UploadModal }

type Dialog =
  | { kind: 'upload' }
  | { kind: 'query' }
  | { kind: 'analyze'; name: string; tab?: AnalysisTab }
  | { kind: 'refresh'; name: string }
  | { kind: 'mimir'; name: string }

/** The Datasets heading's id: Getting started moves focus to it. */
export const DATASETS_HEADING_ID = 'ml-datasets-heading'

/**
 * The vhost's datasets: tables of rows a model is trained on. One comes from
 * a CSV, Excel or Parquet file, or from a query on one of the vhost's
 * database sources; filling one again replaces its rows. Each has one main
 * action, Analyze, which checks the data and leads to training what it
 * suits (`onTrain`); its rows, refresh schedule and deletion sit beside it.
 * A new dataset is analyzed as soon as it is uploaded.
 *
 * Without an engine there is nowhere to keep them: `blockedReason` says why,
 * the panel stays on the page with its buttons off, and `onSetup` opens the
 * worker setup guide.
 */
export function DatasetsPanel({ vhost, blockedReason, onSetup, onTrain }: {
  vhost: string
  blockedReason?: string
  onSetup?: () => void
  onTrain?: (start: TrainStart) => void
}) {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const [dialog, setDialog] = useState<Dialog | undefined>(undefined)
  const blocked = !!blockedReason
  const { data: datasets = [], isLoading } = useVHostDatasets(vhost, !blocked)
  const reasonId = 'ml-datasets-blocked'
  const close = () => setDialog(undefined)
  const analyze = (name: string, tab?: AnalysisTab) => setDialog({ kind: 'analyze', name, tab })

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
    <Paper p="md" withBorder radius="md" component="section" aria-labelledby={DATASETS_HEADING_ID}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={3} size="h4" id={DATASETS_HEADING_ID} tabIndex={-1}>Datasets</Title>
            <Text size="sm" c="dimmed">
              Get data in, then press Analyze to check it: what each column holds, what to fix, and which models it suits.
            </Text>
          </div>
          <Group gap="xs">
            <Button variant="light" leftSection={<IconUpload size="1rem" />} disabled={blocked}
              aria-describedby={blocked ? reasonId : undefined} onClick={() => setDialog({ kind: 'upload' })}>Upload a file</Button>
            <Button variant="light" leftSection={<IconDatabase size="1rem" />} disabled={blocked}
              aria-describedby={blocked ? reasonId : undefined} onClick={() => setDialog({ kind: 'query' })}>From a database</Button>
          </Group>
        </Group>
        {remove.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The dataset was not deleted">{(remove.error as Error).message}</Alert>
        )}
        {blocked ? (
          <EmptyState compact icon={<IconPlugConnected size="1.3rem" />} title="Datasets need the training worker"
            description={<span id={reasonId}>{blockedReason} The worker keeps the datasets, so connect it first: step 1 of Getting started.</span>}
            action={onSetup ? { label: 'How to connect the worker', onClick: onSetup } : undefined} />
        ) : isLoading ? (
          <Group justify="center" p="md"><Loader size="sm" /><Text size="sm" c="dimmed">Loading datasets…</Text></Group>
        ) : datasets.length === 0 ? (
          <EmptyState compact icon={<IconTable size="1.3rem" />} title="No datasets yet"
            description="Upload a CSV, Excel or Parquet file, or read rows from one of this virtual host's databases. One row is one example, such as one customer or one order line."
            action={{ label: 'Upload a file', onClick: () => setDialog({ kind: 'upload' }) }} />
        ) : (
          <Table.ScrollContainer minWidth={620}>
            <Table verticalSpacing="xs" aria-label="Datasets">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Rows</Table.Th>
                  <Table.Th>Columns</Table.Th>
                  <Table.Th>Updated</Table.Th>
                  <Table.Th><Text span size="sm" fw={700} style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>Actions</Text></Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {datasets.map((d) => (
                  <Table.Tr key={d.name}>
                    <Table.Td><Text fw={500} ff="var(--mantine-font-family-monospace)">{d.name}</Text></Table.Td>
                    <Table.Td>{d.rows.toLocaleString()}</Table.Td>
                    <Table.Td><Text size="sm" lineClamp={1} title={d.columns.map((c) => c.name).join(', ')}>{d.columns.length}: {d.columns.map((c) => c.name).join(', ')}</Text></Table.Td>
                    <Table.Td><Text size="sm">{new Date(d.updated_at).toLocaleString()}</Text></Table.Td>
                    <Table.Td>
                      <Group justify="flex-end" gap="xs" wrap="nowrap">
                        <Button size="xs" variant="light" leftSection={<IconChartBar size="1rem" />} aria-label={`Analyze ${d.name}`}
                          onClick={() => analyze(d.name)}>Analyze</Button>
                        <Tooltip label="Show rows">
                          <ActionIcon variant="subtle" radius="md" aria-label={`Show rows of ${d.name}`} onClick={() => analyze(d.name, 'rows')}>
                            <IconEye size="1.1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Refresh on a schedule">
                          <ActionIcon variant="subtle" radius="md" aria-label={`Refresh ${d.name} on a schedule`} onClick={() => setDialog({ kind: 'refresh', name: d.name })}>
                            <IconClock size="1.1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Send to mimir-server">
                          <ActionIcon variant="subtle" radius="md" aria-label={`Send ${d.name} to mimir-server`} onClick={() => setDialog({ kind: 'mimir', name: d.name })}>
                            <IconSend size="1.1rem" />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Delete">
                          <ActionIcon variant="subtle" color="red" radius="md" aria-label={`Delete dataset ${d.name}`} onClick={() => askDelete(d.name)}>
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
      {dialog?.kind === 'upload' && <UploadModal vhost={vhost} onClose={close} onUploaded={(info) => analyze(info.name)} />}
      {dialog?.kind === 'mimir' && (
        <SendToMimirModal vhost={vhost} name={dialog.name} columns={datasets.find((d) => d.name === dialog.name)?.columns} onClose={close} />
      )}
      {dialog?.kind === 'query' && <QueryModal vhost={vhost} onClose={close} onAnalyze={(name) => analyze(name)} />}
      {dialog?.kind === 'analyze' && (
        <AnalysisModal vhost={vhost} name={dialog.name} initialTab={dialog.tab} onClose={close}
          onTrain={onTrain ? (start) => { close(); onTrain(start) } : undefined} />
      )}
      {dialog?.kind === 'refresh' && <RefreshModal vhost={vhost} name={dialog.name} onClose={close} />}
    </Paper>
  )
}
