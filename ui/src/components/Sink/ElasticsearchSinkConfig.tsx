import { TextInput, Stack, PasswordInput, Group, Autocomplete, ActionIcon, Select } from '@mantine/core'
import { IconInfoCircle, IconRefresh } from '@tabler/icons-react'
import type { FC } from 'react'

export type ElasticsearchSinkConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
  indices: string[]
  discoveredDatabases: string[]
  isFetchingDBs: boolean
  loadingIndices: boolean
  indicesError: string | null
  fetchDatabases: () => void
  discoverIndices: () => void
}

export const ElasticsearchSinkConfig: FC<ElasticsearchSinkConfigProps> = ({
  config,
  updateConfig,
  indices,
  discoveredDatabases,
  isFetchingDBs,
  loadingIndices,
  indicesError,
  fetchDatabases,
  discoverIndices,
}) => {
  return (
    <Stack gap="sm">
      <TextInput
        label="Addresses"
        description="Comma-separated list of Elasticsearch nodes"
        placeholder="http://localhost:9200"
        value={config.addresses || ''}
        onChange={(e) => updateConfig('addresses', e.currentTarget.value)}
        required
      />
      <Group align="flex-end" gap="xs">
        <Autocomplete
          label="Cluster Name"
          placeholder="elasticsearch"
          data={[...new Set([...discoveredDatabases, config.cluster_name].filter(Boolean))]}
          value={config.cluster_name || ''}
          onChange={(val) => {
            updateConfig('cluster_name', val)
            if (val) discoverIndices()
          }}
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Discover cluster info" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Cluster">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
      <Group align="flex-end" gap="xs">
        <Select
          label="Index"
          description="Target index name (supports Go templates)"
          placeholder="my-index-{{.table}}"
          data={indices}
          searchable
          value={config.index || ''}
          onChange={(val) => updateConfig('index', val || '')}
          rightSection={loadingIndices ? <IconInfoCircle size={16} /> : null}
          error={indicesError}
          style={{ flex: 1 }}
        />
        <ActionIcon aria-label="Refresh indices" variant="light" size="lg" onClick={() => discoverIndices()} loading={loadingIndices} title="Refresh Indices">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
      <TextInput
        label="Username"
        placeholder="elastic"
        value={config.username || ''}
        onChange={(e) => updateConfig('username', e.currentTarget.value)}
      />
      <PasswordInput
        label="Password"
        placeholder="password"
        value={config.password || ''}
        onChange={(e) => updateConfig('password', e.currentTarget.value)}
      />
      <PasswordInput
        label="API Key"
        placeholder="base64 encoded API Key"
        value={config.api_key || ''}
        onChange={(e) => updateConfig('api_key', e.currentTarget.value)}
      />
    </Stack>
  )
}
