import { useState } from 'react'
import { NumberInput, Select, TextInput, Textarea } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { DATASET_NAME_PATTERN, listSQLSources } from '@/lib/mlModels'
import { LIMITS } from '@/lib/mlDataGuide'

const datasetNameRule = 'Letters, digits, "_", "." and "-".'

/** The dataset name field, with its check; `initial` fills it in. */
export function useDatasetName(initial = '') {
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

/** The vhost's database sources a dataset can be read from. */
export function useSQLSources(vhost: string) {
  return useQuery({
    queryKey: ['ml-sql-sources', vhost],
    queryFn: ({ signal }) => listSQLSources(vhost, signal),
    retry: false,
  })
}

/** A database read: which source, the query, and at most how many rows. */
export interface SourceQuery {
  source: string | null
  query: string
  maxRows: number | string
}

export const EMPTY_SOURCE_QUERY: SourceQuery = { source: null, query: '', maxRows: '' }

export const sourceQueryReady = (v: SourceQuery) => !!v.source && !!v.query.trim()

/**
 * The fields of a database read, shared by reading a dataset once and
 * refreshing it on a schedule. `help` goes between the query and the row cap.
 */
export function SourceQueryFields({ vhost, value, onChange, placeholder, help }: {
  vhost: string
  value: SourceQuery
  onChange: (v: SourceQuery) => void
  placeholder: string
  help?: React.ReactNode
}) {
  const { data: sources = [], isLoading } = useSQLSources(vhost)
  const set = (patch: Partial<SourceQuery>) => onChange({ ...value, ...patch })
  return (
    <>
      <Select label="Database source" required searchable
        placeholder={isLoading ? 'Loading…' : sources.length ? 'Choose a source' : 'No database sources in this vhost'}
        data={sources.map((s) => ({ value: s.id, label: s.name }))} value={value.source} onChange={(source) => set({ source })} />
      <Textarea label="Query" required autosize minRows={4} placeholder={placeholder}
        description="A SELECT, run read-only with the source's own credentials." value={value.query}
        onChange={(e) => set({ query: e.currentTarget.value })} styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
      {help}
      <NumberInput label="At most this many rows" placeholder={String(LIMITS.defaultMaxQueryRows)} min={1} value={value.maxRows}
        onChange={(maxRows) => set({ maxRows })} maw={360}
        description={`Empty reads up to ${LIMITS.defaultMaxQueryRows.toLocaleString('en-US')}. Set more to read more; the vhost's quota still applies.`} />
    </>
  )
}
