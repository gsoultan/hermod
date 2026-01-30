import { ActionIcon, Autocomplete, Group, Select, TextInput } from '@mantine/core'
import { IconInfoCircle, IconRefresh } from '@tabler/icons-react'
import type { FC } from 'react'

export type PostgresSinkConfigProps = {
  type: 'postgres' | 'yugabyte'
  config: any
  tables: string[]
  discoveredDatabases: string[]
  isFetchingDBs: boolean
  loadingTables: boolean
  tablesError: string | null
  updateConfig: (key: string, value: any) => void
  fetchDatabases: () => void
  discoverTables: () => void
}

export const PostgresSinkConfig: FC<PostgresSinkConfigProps> = ({
  type,
  config,
  tables,
  discoveredDatabases,
  isFetchingDBs,
  loadingTables,
  tablesError,
  updateConfig,
  fetchDatabases,
  discoverTables,
}) => {
  return (
    <>
      <Group grow>
        <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
        <TextInput
          label="Port"
          placeholder={type === 'postgres' || type === 'yugabyte' ? '5432' : '5432'}
          value={config.port || ''}
          onChange={(e) => updateConfig('port', e.target.value)}
          required
        />
      </Group>
      <Group grow>
        <TextInput label="User" placeholder="user" value={config.user || ''} onChange={(e) => updateConfig('user', e.target.value)} required />
        <TextInput label="Password" type="password" placeholder="password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} required />
      </Group>
      <Group align="flex-end" gap="xs">
        <Autocomplete
          label="Database"
          placeholder="dbname"
          data={[...new Set([...discoveredDatabases, config.dbname].filter(Boolean))]}
          value={config.dbname || ''}
          onChange={(val) => {
            updateConfig('dbname', val)
            if (val) discoverTables()
          }}
          required
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
      <Group align="flex-end" gap="xs">
        <Select
          label="Target Table"
          placeholder="Select or type table name"
          data={tables}
          searchable
          value={config.table || ''}
          onChange={(val) => updateConfig('table', val || '')}
          rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
          error={tablesError}
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
      {(type === 'postgres' || type === 'yugabyte') && (
        <TextInput label="SSL Mode" placeholder="disable" value={config.sslmode || ''} onChange={(e) => updateConfig('sslmode', e.target.value)} />
      )}
      <TextInput
        label="OR Connection String"
        placeholder={type === 'postgres' || type === 'yugabyte' ? 'postgres://...' : 'postgres://...'}
        value={config.connection_string || ''}
        onChange={(e) => updateConfig('connection_string', e.target.value)}
      />
    </>
  )
}
