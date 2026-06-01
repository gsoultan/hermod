import { Group, Select, TextInput, PasswordInput } from '@mantine/core'
import type { FC } from 'react'

export type SapSinkConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
}

export const SapSinkConfig: FC<SapSinkConfigProps> = ({ config, updateConfig }) => {
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
        <Select
          label="Protocol"
          data={[
            { value: 'odata', label: 'OData (REST)' },
            { value: 'bapi', label: 'BAPI' },
            { value: 'idoc', label: 'IDOC' },
            { value: 'rfc', label: 'RFC' },
          ]}
          value={config.protocol || 'odata'}
          onChange={(val) => updateConfig('protocol', val)}
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
        label="Service (OData)"
        placeholder="API_PURCHASEORDER_PROCESS_SRV"
        value={config.service || ''}
        onChange={(e) => updateConfig('service', e.target.value)}
        required={config.protocol === 'odata' || !config.protocol}
      />
      <TextInput
        label="Entity / Object Name"
        placeholder="A_PurchaseOrder"
        value={config.entity || ''}
        onChange={(e) => updateConfig('entity', e.target.value)}
        required
      />
      {config.protocol === 'bapi' && (
        <TextInput
          label="BAPI Name"
          value={config.bapi_name || ''}
          onChange={(e) => updateConfig('bapi_name', e.target.value)}
        />
      )}
      {config.protocol === 'idoc' && (
        <TextInput
          label="IDOC Name"
          value={config.idoc_name || ''}
          onChange={(e) => updateConfig('idoc_name', e.target.value)}
        />
      )}
    </>
  )
}
