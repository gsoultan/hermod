import { Group, TextInput, PasswordInput } from '@mantine/core'
import type { FC } from 'react'

export type SapSourceConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
}

export const SapSourceConfig: FC<SapSourceConfigProps> = ({ config, updateConfig }) => {
  return (
    <>
      <TextInput
        label="Host"
        placeholder="https://sap-server:443"
        value={config.host || ''}
        onChange={(e) => updateConfig('host', e.target.value)}
        required
      />
      <Group grow>
        <TextInput
          label="SAP Client"
          placeholder="100"
          value={config.client || ''}
          onChange={(e) => updateConfig('client', e.target.value)}
        />
        <TextInput
          label="Poll Interval"
          placeholder="10s"
          value={config.poll_interval || '30s'}
          onChange={(e) => updateConfig('poll_interval', e.target.value)}
        />
      </Group>
      <Group grow>
        <TextInput
          label="Username"
          value={config.username || ''}
          onChange={(e) => updateConfig('username', e.target.value)}
        />
        <PasswordInput
          label="Password"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.target.value)}
        />
      </Group>
      <TextInput
        label="OData Service"
        placeholder="API_PURCHASEORDER_PROCESS_SRV"
        value={config.service || ''}
        onChange={(e) => updateConfig('service', e.target.value)}
        required
      />
      <TextInput
        label="OData Entity"
        placeholder="A_PurchaseOrder"
        value={config.entity || ''}
        onChange={(e) => updateConfig('entity', e.target.value)}
        required
      />
      <TextInput
        label="Filter ($filter)"
        placeholder="PurchaseOrder eq '123'"
        value={config.filter || ''}
        onChange={(e) => updateConfig('filter', e.target.value)}
      />
    </>
  )
}
