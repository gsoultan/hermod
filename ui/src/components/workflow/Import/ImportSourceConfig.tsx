import { useState } from 'react'
import { apiFetch } from '@/api'
import { SourceConfigFields } from '../Source/SourceConfigFields'
import type { ImportResource } from '@/utils/importBundle'

interface ImportSourceConfigProps {
  source: ImportResource
  updateConfig: (key: string, value: any) => void
  /** Every source the pickers may offer — the bundle's plus this instance's. */
  allSources: any[]
}

/**
 * The connection fields for one source in an import bundle.
 *
 * `SourceConfigFields` wants table and database discovery wired up, and the
 * discovery endpoints take a whole source object rather than a saved id — the
 * same way the Add Source wizard uses them before anything is written. That
 * means an operator can point a bundle's source at their own server here and
 * still pick tables from a list, instead of importing it, discovering it is
 * wrong, and editing it again.
 *
 * Discovery state is per source, which is why this is a component rather than a
 * few props on the card: two sources in one bundle each need their own list.
 */
export function ImportSourceConfig({ source, updateConfig, allSources }: ImportSourceConfigProps) {
  const [discoveredTables, setDiscoveredTables] = useState<string[]>([])
  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([])
  const [isFetchingTables, setIsFetchingTables] = useState(false)
  const [isFetchingDBs, setIsFetchingDBs] = useState(false)

  const fetchDatabases = async () => {
    setIsFetchingDBs(true)
    try {
      const res = await apiFetch('/api/sources/discover/databases', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(source),
        silent: true,
      })
      setDiscoveredDatabases((await res.json()) || [])
    } catch {
      // Discovery is a convenience. A server that cannot be reached yet is the
      // normal state here — the operator is still editing the address — so this
      // must not raise; the fields stay typeable.
      setDiscoveredDatabases([])
    } finally {
      setIsFetchingDBs(false)
    }
  }

  const fetchTables = async (dbName?: string) => {
    setIsFetchingTables(true)
    try {
      const body = {
        ...source,
        config: { ...source.config, dbname: dbName || source.config?.dbname || source.config?.path },
      }
      const res = await apiFetch('/api/sources/discover/tables', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        silent: true,
      })
      setDiscoveredTables((await res.json()) || [])
    } catch {
      setDiscoveredTables([])
    } finally {
      setIsFetchingTables(false)
    }
  }

  return (
    <SourceConfigFields
      source={source as any}
      updateConfig={updateConfig}
      discoveredTables={discoveredTables}
      discoveredDatabases={discoveredDatabases}
      isFetchingTables={isFetchingTables}
      isFetchingDBs={isFetchingDBs}
      fetchTables={fetchTables}
      fetchDatabases={fetchDatabases}
      // Uploading a file mid-import would write it against a source that does
      // not exist yet; the field stays visible but inert, and the canvas offers
      // it once the source is saved.
      handleFileUpload={() => {}}
      uploading={false}
      allSources={allSources as any}
    />
  )
}
