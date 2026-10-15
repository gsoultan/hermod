import { useState } from 'react'
import { TextInput } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { DATASET_NAME_PATTERN, listSQLSources } from '@/lib/mlModels'

const datasetNameRule = 'Letters, digits, "_", "." and "-".'

/**
 * The dataset name field, with its check; `initial` fills it in. `suggest`
 * fills it in too, until someone types a name of their own.
 */
export function useDatasetName(initial = '') {
  const [name, setName] = useState(initial)
  const [typed, setTyped] = useState(!!initial)
  const [error, setError] = useState<string | null>(null)
  const check = () => {
    const bad = !DATASET_NAME_PATTERN.test(name.trim())
    setError(bad ? `"${name.trim()}" is not a valid name.` : null)
    return !bad
  }
  const input = (
    <TextInput label="Dataset name" description={datasetNameRule} placeholder="customers" required value={name}
      onChange={(e) => { setName(e.currentTarget.value); setTyped(true); setError(null) }} error={error} data-autofocus />
  )
  const suggest = (suggested: string) => {
    if (!typed || !name.trim()) {
      setName(suggested)
      setError(null)
    }
  }
  return { name: name.trim(), check, input, suggest }
}

/** The vhost's database sources a dataset can be read from. */
export function useSQLSources(vhost: string) {
  return useQuery({
    queryKey: ['ml-sql-sources', vhost],
    queryFn: ({ signal }) => listSQLSources(vhost, signal),
    retry: false,
  })
}
