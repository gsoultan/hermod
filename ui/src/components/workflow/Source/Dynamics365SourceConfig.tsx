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
          description="Entra App ID"
          mih={80}
        />
        <PasswordInput
          label="Client Secret"
          placeholder="App Registration Client Secret"
          value={config.client_secret || ''}
          onChange={(e) => updateConfig('client_secret', e.target.value)}
          required
          description="Entra Client Secret"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput
          label="Entity Set Name"
          placeholder="accounts"
          description="The OData entity set"
          value={config.entity || ''}
          onChange={(e) => updateConfig('entity', e.target.value)}
          required
          mih={80}
        />
        <TextInput
          label="Poll Interval"
          placeholder="1m"
          value={config.poll_interval || '1m'}
          onChange={(e) => updateConfig('poll_interval', e.target.value)}
          description="Delay between polls"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput
          label="ID Field"
          placeholder="modifiedon"
          description="Delta tracking field"
          value={config.id_field || ''}
          onChange={(e) => updateConfig('id_field', e.target.value)}
          mih={80}
        />
        <TextInput
          label="Filter ($filter)"
          placeholder="statecode eq 0"
          value={config.filter || ''}
          onChange={(e) => updateConfig('filter', e.target.value)}
          description="OData filter expression"
          mih={80}
        />
      </Group>
    </Stack>
  )
}
