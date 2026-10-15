import { Alert, Button, Grid, Group, Modal, Paper, ScrollArea, Stack, Text, ThemeIcon } from '@mantine/core'
import { IconAlertCircle, IconBulb, IconChartBar, IconCircleCheck, IconDownload } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { datasetFromQuery, datasetsKey } from '@/lib/mlModels'
import { QueryHelp } from '../dataNeeds'
import { useDatasetName } from './datasetForms'
import { DatabaseQueryEditor, useDatabaseRead } from './DatabaseQuery'

// The dialogs lay out by their own width, not the window's: a viewport
// breakpoint cannot tell a wide dialog on a laptop from a narrow one.
export const DIALOG_BREAKPOINTS = { xs: '360px', sm: '560px', md: '860px', lg: '1100px', xl: '1400px' }

/** A side panel of the dataset dialogs: what the data should hold. */
export function GuidePanel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Paper withBorder radius="md" p="md" bg="var(--mantine-color-default-hover)" component="aside" aria-label={title}>
      <Stack gap="sm">
        <Group gap="xs" wrap="nowrap">
          <ThemeIcon variant="light" radius="md" size="md" aria-hidden><IconBulb size="1rem" /></ThemeIcon>
          <Text fw={600} size="sm">{title}</Text>
        </Group>
        {children}
      </Stack>
    </Paper>
  )
}

/**
 * Reads a dataset from one of the vhost's databases: the query is built from
 * the source's tables and columns, or written as SQL, and can be previewed
 * before any row is read. `onAnalyze` checks what was read.
 */
export function QueryModal({ vhost, onClose, onAnalyze }: { vhost: string; onClose: () => void; onAnalyze?: (name: string) => void }) {
  const queryClient = useQueryClient()
  const ds = useDatasetName()
  const db = useDatabaseRead(vhost)
  const fill = useMutation({
    mutationFn: () => datasetFromQuery(vhost, ds.name, db.read.source as string, db.query, db.maxRows),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: datasetsKey(vhost) }),
  })
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ds.check() && db.ready) fill.mutate()
  }
  return (
    <Modal opened onClose={onClose} title="Read a dataset from a database" centered size="72rem"
      scrollAreaComponent={ScrollArea.Autosize}>
      <form onSubmit={submit} noValidate>
        <Stack gap="lg">
          <Grid type="container" breakpoints={DIALOG_BREAKPOINTS} gap="lg">
            <Grid.Col span={{ base: 12, md: 8 }}>
              <DatabaseQueryEditor state={db} lead={ds.input} sqlPlaceholder="SELECT age, plan, churned FROM customers" />
            </Grid.Col>
            <Grid.Col span={{ base: 12, md: 4 }}>
              <GuidePanel title="What should it return?">
                <QueryHelp onUseQuery={db.takeSql} />
              </GuidePanel>
            </Grid.Col>
          </Grid>
          {fill.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="No rows were read">{(fill.error as Error).message}</Alert>
          )}
          {fill.data && (
            <Alert color="green" icon={<IconCircleCheck size="1rem" />} title="Dataset filled">
              Read {fill.data.rows.toLocaleString()} rows into {fill.data.name}. Check the data next: it shows what each
              column holds, what to fix, and which models it suits.
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{fill.data ? 'Close' : 'Cancel'}</Button>
            {fill.data && onAnalyze ? (
              <Button leftSection={<IconChartBar size="1rem" />} onClick={() => onAnalyze(fill.data.name)}>Check this data</Button>
            ) : (
              <Button type="submit" leftSection={<IconDownload size="1rem" />} loading={fill.isPending} disabled={!db.ready}>Read rows</Button>
            )}
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
