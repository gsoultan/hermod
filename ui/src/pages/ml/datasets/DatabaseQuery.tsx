import { useMemo, useState } from 'react'
import {
  Alert, Badge, Button, Code, Group, NumberInput, Paper, SegmentedControl, Select, SimpleGrid, Stack, Text, Textarea,
} from '@mantine/core'
import { IconAlertCircle, IconCode, IconDatabase, IconEye, IconLayoutGrid } from '@tabler/icons-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { listSourceColumns, listSourceTables, previewQuery } from '@/lib/mlModels'
import { LIMITS } from '@/lib/mlDataGuide'
import { buildSelect, EMPTY_BUILT_QUERY, type BuiltQuery } from '@/lib/mlQueryBuilder'
import type { GuideKind } from '@/lib/mlDataGuide'
import { sampleInsights } from '@/lib/mlDataInsights'
import { DataInsights } from './DataInsights'
import { useSQLSources } from './datasetForms'
import { QueryBuilder } from './QueryBuilder'
import { RowsPreview } from './RowsPreview'

/** How many rows a preview reads: the most the server returns, so its read of each column has rows to go on. */
const PREVIEW_ROWS = 100

export type QueryMode = 'build' | 'sql'
type PreviewTab = 'insights' | 'rows'

/** A database read as it is being set up: the source, and the query built or written. */
export interface DatabaseRead {
  source: string | null
  mode: QueryMode
  built: BuiltQuery
  sql: string
  maxRows: number | string
}

const EMPTY_READ: DatabaseRead = { source: null, mode: 'build', built: EMPTY_BUILT_QUERY, sql: '', maxRows: '' }

/**
 * The state of a database read and what it needs from the server: the
 * vhost's database sources, and the chosen source's tables and columns for
 * the builder. `query` is the statement to run, built or written.
 */
export function useDatabaseRead(vhost: string) {
  const [read, setRead] = useState<DatabaseRead>(EMPTY_READ)
  const sources = useSQLSources(vhost)
  const engine = sources.data?.find((s) => s.id === read.source)?.type ?? ''
  const table = read.built.table
  const tables = useQuery({
    queryKey: ['ml-source-tables', vhost, read.source],
    queryFn: ({ signal }) => listSourceTables(vhost, read.source as string, signal),
    enabled: !!read.source && read.mode === 'build',
    retry: false,
    staleTime: 60_000,
  })
  const columns = useQuery({
    queryKey: ['ml-source-columns', vhost, read.source, table],
    queryFn: ({ signal }) => listSourceColumns(vhost, read.source as string, table, signal),
    enabled: !!read.source && !!table,
    retry: false,
    staleTime: 60_000,
  })
  const types = useMemo(() => Object.fromEntries((columns.data ?? []).map((c) => [c.name, c.type])), [columns.data])
  const query = read.mode === 'build' ? buildSelect(engine, read.built, types) : read.sql
  const set = (patch: Partial<DatabaseRead>) => setRead((r) => ({ ...r, ...patch }))
  return {
    vhost, read, set, sources, engine, tables, columns, query,
    ready: !!read.source && !!query.trim(),
    maxRows: typeof read.maxRows === 'number' ? read.maxRows : undefined,
    /** Takes a statement on as SQL to edit, such as an example. */
    takeSql: (sql: string) => set({ mode: 'sql', sql }),
  }
}

export type DatabaseReadState = ReturnType<typeof useDatabaseRead>

/**
 * Where a dataset's rows come from in a database: the source, then the query,
 * built from the source's tables and columns or written as SQL, with a
 * preview of the first rows it returns and the row cap. `lead` sits beside
 * the source, such as the dataset's name.
 */
export function DatabaseQueryEditor({ state, lead, sqlPlaceholder, guideKind }: {
  state: DatabaseReadState
  lead?: React.ReactNode
  sqlPlaceholder: string
  /** The kind of model the data is for: the builder suggests its columns and the preview checks them. */
  guideKind?: GuideKind
}) {
  const [previewTab, setPreviewTab] = useState<PreviewTab>('insights')
  const { vhost, read, set, sources, engine, tables, columns, query } = state
  const list = sources.data ?? []
  const preview = useMutation({ mutationFn: (q: string) => previewQuery(vhost, read.source as string, q, PREVIEW_ROWS) })
  const stale = preview.data && preview.variables !== query
  const switchMode = (mode: string) => {
    // A builder's statement goes along into the editor the first time, so
    // writing SQL starts from it rather than from nothing.
    if (mode === 'sql' && !read.sql.trim() && query.trim()) set({ mode: 'sql', sql: query })
    else set({ mode: mode as QueryMode })
  }

  return (
    <Stack gap="md">
      <SimpleGrid cols={{ base: 1, sm: lead ? 2 : 1 }} spacing="md">
        {lead}
        <Select label="Database source" required searchable leftSection={<IconDatabase size="1rem" />}
          placeholder={sources.isLoading ? 'Loading…' : list.length ? 'Choose a source' : 'No database sources in this vhost'}
          data={list.map((s) => ({ value: s.id, label: s.name }))} value={read.source}
          onChange={(source) => {
            set({ source, built: EMPTY_BUILT_QUERY })
            preview.reset()
          }} />
      </SimpleGrid>

      <Paper withBorder radius="md" p="md">
        <Stack gap="md">
          <Group justify="space-between" gap="xs">
            <Group gap="xs">
              <Text fw={600}>Query</Text>
              {engine && <Badge size="sm" variant="light" color="gray">{engine}</Badge>}
            </Group>
            <SegmentedControl size="xs" value={read.mode} onChange={switchMode} aria-label="How to make the query" data={[
              { value: 'build', label: <Group gap={6} wrap="nowrap"><IconLayoutGrid size="0.9rem" /><span>Build visually</span></Group> },
              { value: 'sql', label: <Group gap={6} wrap="nowrap"><IconCode size="0.9rem" /><span>Write SQL</span></Group> },
            ]} />
          </Group>
          {read.mode === 'build' ? (
            <>
              <QueryBuilder sourceChosen={!!read.source} tables={tables} columns={columns} value={read.built}
                onChange={(built) => set({ built })} onWriteSql={() => switchMode('sql')} guideKind={guideKind} />
              {query && (
                <Stack gap={6}>
                  <Group justify="space-between" gap="xs">
                    <Text size="xs" fw={600} c="dimmed" tt="uppercase">The query it runs</Text>
                    <Button size="compact-xs" variant="subtle" leftSection={<IconCode size="0.85rem" />}
                      onClick={() => set({ mode: 'sql', sql: query })}>Edit as SQL</Button>
                  </Group>
                  <Code block aria-label="The query it runs" style={{ whiteSpace: 'pre-wrap' }}>{query}</Code>
                </Stack>
              )}
            </>
          ) : (
            <Textarea label="Query" required autosize minRows={8} maxRows={20} placeholder={sqlPlaceholder}
              description="A SELECT, run read-only with the source's own credentials." value={read.sql}
              onChange={(e) => set({ sql: e.currentTarget.value })}
              styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-sm)' } }} />
          )}
        </Stack>
      </Paper>

      <Group justify="space-between" align="flex-end" gap="md">
        <NumberInput label="At most this many rows" placeholder={String(LIMITS.defaultMaxQueryRows)} min={1} value={read.maxRows}
          onChange={(maxRows) => set({ maxRows })} w={{ base: '100%', xs: 380 }}
          description={`Empty reads up to ${LIMITS.defaultMaxQueryRows.toLocaleString('en-US')}; the vhost's quota still applies.`} />
        <Button variant="default" leftSection={<IconEye size="1rem" />} disabled={!state.ready} loading={preview.isPending}
          onClick={() => preview.mutate(query)}>
          Preview rows
        </Button>
      </Group>

      {preview.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The query did not run">{(preview.error as Error).message}</Alert>
      )}
      {preview.data && (
        <Paper withBorder radius="md" p="sm">
          <Stack gap="xs">
            <Group justify="space-between" gap="xs">
              <SegmentedControl size="xs" value={previewTab} onChange={(t) => setPreviewTab(t as PreviewTab)} aria-label="What to show of the preview"
                data={[{ value: 'insights', label: 'What the data says' }, { value: 'rows', label: 'Rows' }]} />
              <Text size="xs" c="dimmed">
                {preview.data.rows.length === 0
                  ? 'The query returns no rows.'
                  : `${preview.data.rows.length} rows, ${preview.data.columns.length} columns${preview.data.more ? '. The query returns more.' : '.'}`}
              </Text>
            </Group>
            {stale && <Text size="xs" c="orange">The query changed after this preview. Preview again to see what it returns now.</Text>}
            {previewTab === 'rows' || !guideKind || preview.data.rows.length === 0 ? (
              <RowsPreview columns={preview.data.columns} rows={preview.data.rows} caption="First rows the query returns" />
            ) : (
              <DataInsights insights={sampleInsights(preview.data.columns, preview.data.rows)} kind={guideKind} rows={preview.data.rows.length} />
            )}
          </Stack>
        </Paper>
      )}
    </Stack>
  )
}
