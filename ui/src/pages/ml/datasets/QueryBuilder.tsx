import { useState } from 'react'
import {
  ActionIcon, Alert, Button, Chip, Group, Loader, NumberInput, ScrollArea, SegmentedControl, Select, SimpleGrid, Stack, Text,
  TextInput, Tooltip,
} from '@mantine/core'
import { IconAlertCircle, IconFilter, IconKey, IconPlus, IconSearch, IconTable, IconX } from '@tabler/icons-react'
import type { UseQueryResult } from '@tanstack/react-query'
import { FILTER_OPS, filterNeedsValue, type BuiltQuery, type FilterOp, type QueryFilter, type SourceColumn } from '@/lib/mlQueryBuilder'

/** Past this many columns the picker gets a search box. */
const SEARCH_FROM = 12

function SectionTitle({ children, hint }: { children: React.ReactNode; hint?: React.ReactNode }) {
  return (
    <Group justify="space-between" gap="xs" wrap="nowrap">
      <Text size="sm" fw={600}>{children}</Text>
      {hint}
    </Group>
  )
}

/** The columns to read, as chips in the table's order; none picked reads them all. */
function ColumnPicker({ columns, value, onChange }: { columns: SourceColumn[]; value: string[]; onChange: (v: string[]) => void }) {
  const [search, setSearch] = useState('')
  const shown = search ? columns.filter((c) => c.name.toLowerCase().includes(search.toLowerCase())) : columns
  return (
    <Stack gap="xs">
      <SectionTitle hint={
        <Group gap={4} wrap="nowrap">
          <Button size="compact-xs" variant="subtle" onClick={() => onChange(columns.map((c) => c.name))}>All</Button>
          <Button size="compact-xs" variant="subtle" color="gray" disabled={value.length === 0} onClick={() => onChange([])}>Clear</Button>
        </Group>
      }>
        Columns
      </SectionTitle>
      <Text size="xs" c="dimmed">
        {value.length === 0
          ? `All ${columns.length} columns are read. Pick some to read only those, in the order you pick them.`
          : `${value.length} of ${columns.length} columns, in this order: ${value.join(', ')}.`}
      </Text>
      {columns.length > SEARCH_FROM && (
        <TextInput size="xs" placeholder="Find a column" aria-label="Find a column" leftSection={<IconSearch size="0.9rem" />}
          value={search} onChange={(e) => setSearch(e.currentTarget.value)} />
      )}
      <ScrollArea.Autosize mah={168} type="auto" offsetScrollbars>
        <Chip.Group multiple value={value} onChange={onChange}>
          <Group gap={6}>
            {shown.map((c) => (
              <Chip key={c.name} value={c.name} size="sm" radius="sm" variant="outline">
                <Group gap={4} wrap="nowrap" component="span">
                  {c.is_pk && <IconKey size="0.8rem" aria-hidden />}
                  <span>{c.name}</span>
                  {c.type && <Text span size="xs" c="dimmed">{c.type}</Text>}
                </Group>
              </Chip>
            ))}
          </Group>
        </Chip.Group>
      </ScrollArea.Autosize>
    </Stack>
  )
}

function FilterRow({ index, filter, columns, onChange, onRemove }: {
  index: number
  filter: QueryFilter
  columns: string[]
  onChange: (f: QueryFilter) => void
  onRemove: () => void
}) {
  const n = index + 1
  return (
    <Group gap="xs" wrap="nowrap" align="flex-start">
      <Text size="sm" c="dimmed" w={42} pt={6} ta="right" style={{ flexShrink: 0 }}>{index === 0 ? 'where' : 'and'}</Text>
      <Select aria-label={`Filter ${n} column`} placeholder="Column" searchable data={columns} value={filter.column || null}
        onChange={(column) => onChange({ ...filter, column: column ?? '' })} style={{ flex: 3, minWidth: 0 }} />
      <Select aria-label={`Filter ${n} condition`} data={FILTER_OPS} value={filter.op} allowDeselect={false}
        onChange={(op) => onChange({ ...filter, op: (op ?? 'eq') as FilterOp })} style={{ flex: 2, minWidth: 0 }} />
      {filterNeedsValue(filter.op) ? (
        <TextInput aria-label={`Filter ${n} value`} placeholder="Value" value={filter.value}
          onChange={(e) => onChange({ ...filter, value: e.currentTarget.value })} style={{ flex: 3, minWidth: 0 }} />
      ) : (
        <div style={{ flex: 3 }} />
      )}
      <Tooltip label="Remove this filter">
        <ActionIcon variant="subtle" color="gray" mt={4} aria-label={`Remove filter ${n}`} onClick={onRemove}>
          <IconX size="1rem" />
        </ActionIcon>
      </Tooltip>
    </Group>
  )
}

/**
 * The query, built rather than written: a table, its columns, filters, a
 * sort and a row limit. The tables and columns are the source's own, read
 * by the server; `onWriteSql` leaves for the SQL editor when they cannot be.
 */
export function QueryBuilder({ sourceChosen, tables, columns, value, onChange, onWriteSql }: {
  sourceChosen: boolean
  tables: UseQueryResult<string[]>
  columns: UseQueryResult<SourceColumn[]>
  value: BuiltQuery
  onChange: (v: BuiltQuery) => void
  onWriteSql: () => void
}) {
  const set = (patch: Partial<BuiltQuery>) => onChange({ ...value, ...patch })
  if (!sourceChosen) {
    return (
      <Group gap="xs" p="md" justify="center">
        <IconTable size="1.1rem" color="var(--mantine-color-dimmed)" />
        <Text size="sm" c="dimmed">Choose a database source to see its tables.</Text>
      </Group>
    )
  }
  if (tables.error) {
    return (
      <Alert color="orange" variant="light" icon={<IconAlertCircle size="1rem" />} title="The tables could not be listed">
        <Stack gap="xs" align="flex-start">
          <Text size="sm">{(tables.error as Error).message}</Text>
          <Button size="xs" variant="light" onClick={onWriteSql}>Write the SQL instead</Button>
        </Stack>
      </Alert>
    )
  }
  const cols = columns.data ?? []
  const names = cols.map((c) => c.name)
  const filters = value.filters
  return (
    <Stack gap="lg">
      <Select label="Table" placeholder={tables.isLoading ? 'Loading tables…' : 'Choose a table'} searchable
        leftSection={<IconTable size="1rem" />} rightSection={tables.isLoading ? <Loader size="xs" /> : undefined}
        data={tables.data ?? []} value={value.table || null} nothingFoundMessage="No table by that name"
        onChange={(table) => onChange({ table: table ?? '', columns: [], filters: [], sort: undefined, limit: value.limit })} />
      {value.table && (columns.isLoading ? (
        <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">Reading the columns of {value.table}…</Text></Group>
      ) : columns.error ? (
        <Alert color="orange" variant="light" icon={<IconAlertCircle size="1rem" />} title="The columns could not be read">
          {(columns.error as Error).message}
        </Alert>
      ) : (
        <>
          <ColumnPicker columns={cols} value={value.columns} onChange={(columns) => set({ columns })} />
          <Stack gap="xs">
            <SectionTitle>Only rows where</SectionTitle>
            {filters.length === 0 && <Text size="xs" c="dimmed">No filters: every row of {value.table} is read.</Text>}
            {filters.map((f, i) => (
              <FilterRow key={i} index={i} filter={f} columns={names}
                onChange={(next) => set({ filters: filters.map((g, j) => (j === i ? next : g)) })}
                onRemove={() => set({ filters: filters.filter((_, j) => j !== i) })} />
            ))}
            <Group>
              <Button size="xs" variant="light" leftSection={filters.length ? <IconPlus size="0.9rem" /> : <IconFilter size="0.9rem" />}
                onClick={() => set({ filters: [...filters, { column: '', op: 'eq', value: '' }] })}>
                Add a filter
              </Button>
            </Group>
          </Stack>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <Stack gap={6}>
              <Select label="Sort by" placeholder="Database order" clearable searchable data={names} value={value.sort?.column ?? null}
                onChange={(column) => set({ sort: column ? { column, desc: value.sort?.desc ?? false } : undefined })} />
              {value.sort && (
                <SegmentedControl size="xs" aria-label="Sort direction" value={value.sort.desc ? 'desc' : 'asc'}
                  data={[{ value: 'asc', label: 'Smallest first' }, { value: 'desc', label: 'Largest first' }]}
                  onChange={(d) => set({ sort: { column: value.sort!.column, desc: d === 'desc' } })} />
              )}
            </Stack>
            <NumberInput label="Keep the first rows" placeholder="All rows" min={1} value={value.limit ?? ''}
              description="To try a big table on a part of it."
              onChange={(n) => set({ limit: typeof n === 'number' && n > 0 ? n : undefined })} />
          </SimpleGrid>
        </>
      ))}
    </Stack>
  )
}
