import { Group, TextInput, PasswordInput, Select, Stack } from '@mantine/core'
import type { FC } from 'react'

export type Dynamics365SinkConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
}

export const Dynamics365SinkConfig: FC<Dynamics365SinkConfigProps> = ({ config, updateConfig }) => {
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
          description="The OData entity set name"
          value={config.entity || ''}
          onChange={(e) => updateConfig('entity', e.target.value)}
          required
        />
        <Select
          label="Operation"
          placeholder="Select operation"
          data={[
            { value: 'create', label: 'Create' },
            { value: 'update', label: 'Update' },
            { value: 'upsert', label: 'Upsert' },
            { value: 'delete', label: 'Delete' },
          ]}
          value={config.operation || 'create'}
          onChange={(val) => updateConfig('operation', val)}
          required
        />
      </Group>
      <TextInput
        label="External ID Field / Primary Key"
        placeholder="accountid"
        description="Required for Update, Upsert, and Delete operations"
        value={config.external_id || ''}
        onChange={(e) => updateConfig('external_id', e.target.value)}
        required={['update', 'upsert', 'delete'].includes(config.operation)}
      />
    </Stack>
  )
}
