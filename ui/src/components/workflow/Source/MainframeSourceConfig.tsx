import { Group, Select, TextInput, PasswordInput } from '@mantine/core'
import type { FC } from 'react'

export type MainframeSourceConfigProps = {
  config: Record<string, any>
  updateConfig: (key: string, value: any) => void
}

export const MainframeSourceConfig: FC<MainframeSourceConfigProps> = ({ config, updateConfig }) => {
  return (
    <>
      <TextInput
        label="Host"
        placeholder="mainframe.company.com"
        value={config.host || ''}
        onChange={(e) => updateConfig('host', e.target.value)}
        required
      />
      <Group grow>
        <TextInput
          label="Port"
          placeholder="50000"
          value={config.port || ''}
          onChange={(e) => updateConfig('port', e.target.value)}
          description="Mainframe server port"
          mih={80}
        />
        <Select
          label="Type"
          data={[
            { value: 'db2', label: 'DB2' },
            { value: 'vsam', label: 'VSAM' },
          ]}
          value={config.type || 'db2'}
          onChange={(val) => updateConfig('type', val)}
          description="Mainframe storage type"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput
          label="Username"
          value={config.user || ''}
          onChange={(e) => updateConfig('user', e.target.value)}
          description="Mainframe user ID"
          mih={80}
        />
        <PasswordInput
          label="Password"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.target.value)}
          description="Mainframe password"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput
          label="Database"
          placeholder="DB2P"
          value={config.database || ''}
          onChange={(e) => updateConfig('database', e.target.value)}
          description="Target database name"
          mih={80}
        />
        <TextInput
          label="Schema"
          placeholder="SYSIBM"
          value={config.schema || ''}
          onChange={(e) => updateConfig('schema', e.target.value)}
          description="Target schema name"
          mih={80}
        />
      </Group>
      <TextInput
        label="Table / Entity"
        placeholder="USER_TABLE"
        value={config.table || ''}
        onChange={(e) => updateConfig('table', e.target.value)}
      />
      {config.type === 'vsam' && (
        <>
          <TextInput
            label="Dataset Name (DSN)"
            placeholder="PROD.CUSTOMER.VSAM"
            value={config.dataset_name || ''}
            onChange={(e) => updateConfig('dataset_name', e.target.value)}
          />
          <TextInput
            label="Local Bridge File (Optional Mock)"
            placeholder="/path/to/vsam_mock.txt"
            value={config.local_bridge || ''}
            onChange={(e) => updateConfig('local_bridge', e.target.value)}
            description="Hermod will poll lines from this file if specified."
          />
        </>
      )}
      <TextInput
        label="Poll Interval"
        placeholder="10s"
        value={config.interval || '30s'}
        onChange={(e) => updateConfig('interval', e.target.value)}
      />
    </>
  )
}
