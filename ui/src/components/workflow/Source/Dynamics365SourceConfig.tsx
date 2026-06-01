import { Group, TextInput, PasswordInput, Stack } from '@mantine/core'
import type { FC } from 'react'

export type Dynamics365SourceConfigProps = {
  config: Record<string, any>
  updateConfig: (key: string, value: any) => void
}

export const Dynamics365SourceConfig: FC<Dynamics365SourceConfigProps> = ({ config, updateConfig }) => {
  return (
    <Stack gap="sm">
      <TextInput
        label="Resource URL"
        placeholder="https://org.crm.dynamics.com"
        description="The base URL of your Dynamics 365 / Dataverse environment"
        value={config.resource || ''}
        onChange={(e) => updateConfig('resource', e.target.value)}
        required
      />
      <TextInput
        label="Tenant ID"
        placeholder="Microsoft Entra Tenant ID (GUID)"
        value={config.tenant_id || ''}
        onChange={(e) => updateConfig('tenant_id', e.target.value)}
        required
      />
      <Group grow>
        <TextInput
          label="Client ID"
          placeholder="App Registration Client ID"
          value={config.client_id || ''}
          onChange={(e) => updateConfig('client_id', e.target.value)}
          required
        />
        <PasswordInput
          label="Client Secret"
          placeholder="App Registration Client Secret"
          value={config.client_secret || ''}
          onChange={(e) => updateConfig('client_secret', e.target.value)}
          required
        />
      </Group>
      <Group grow>
        <TextInput
          label="Entity Set Name"
          placeholder="accounts"
          description="The OData entity set name to poll"
          value={config.entity || ''}
          onChange={(e) => updateConfig('entity', e.target.value)}
          required
        />
        <TextInput
          label="Poll Interval"
          placeholder="1m"
          value={config.poll_interval || '1m'}
          onChange={(e) => updateConfig('poll_interval', e.target.value)}
        />
      </Group>
      <Group grow>
        <TextInput
          label="ID Field"
          placeholder="modifiedon"
          description="Field to use for delta tracking (must be sortable/filterable)"
          value={config.id_field || ''}
          onChange={(e) => updateConfig('id_field', e.target.value)}
        />
        <TextInput
          label="Filter ($filter)"
          placeholder="statecode eq 0"
          value={config.filter || ''}
          onChange={(e) => updateConfig('filter', e.target.value)}
        />
      </Group>
    </Stack>
  )
}
