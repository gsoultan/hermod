import { TextInput, Group, Select, Divider, Text, Code, Button, Checkbox } from '@mantine/core';

interface FTPSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

export function FTPSinkConfig({ config, updateConfig }: FTPSinkConfigProps) {
  return (
    <>
      <Group grow>
        <TextInput 
          label="Host" 
          placeholder="ftp.example.com" 
          value={config.host || ''} 
          onChange={(e) => updateConfig('host', e.target.value)} 
          required 
          description="FTP server host"
          mih={80}
        />
        <TextInput 
          label="Port" 
          placeholder="21" 
          value={config.port || ''} 
          onChange={(e) => updateConfig('port', e.target.value)} 
          required 
          description="FTP server port"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput 
          label="Username" 
          placeholder="user" 
          value={config.username || ''} 
          onChange={(e) => updateConfig('username', e.target.value)} 
          description="Login username"
          mih={80}
        />
        <TextInput 
          label="Password" 
          type="password" 
          placeholder="password" 
          value={config.password || ''} 
          onChange={(e) => updateConfig('password', e.target.value)} 
          description="Login password"
          mih={80}
        />
      </Group>
      <Group grow>
        <Select 
          label="TLS"
          placeholder="Use TLS (FTPS)"
          data={[{ value: 'true', label: 'True' }, { value: 'false', label: 'False' }]}
          value={config.tls || 'false'}
          onChange={(value) => updateConfig('tls', value || 'false')}
          description="Encryption mode"
          mih={80}
        />
        <TextInput 
          label="Timeout" 
          placeholder="30s" 
          value={config.timeout || '30s'} 
          onChange={(e) => updateConfig('timeout', e.target.value)} 
          description="Connection timeout"
          mih={80}
        />
      </Group>
      <Divider my="sm" />
      <Text size="sm" c="dimmed">
        Destination path configuration (supports Go templates like <Code>{'{{.table}}'}</Code>, <Code>{'{{.id}}'}</Code>, <Code>{'{{.metadata.key}}'}</Code>)
      </Text>
      <Group grow>
        <TextInput label="Root Directory" placeholder="/uploads" value={config.root_dir || ''} onChange={(e) => updateConfig('root_dir', e.target.value)} />
        <TextInput label="Path Template" placeholder="{{.schema}}/{{.table}}" value={config.path_template || ''} onChange={(e) => updateConfig('path_template', e.target.value)} />
      </Group>
      <Group grow>
        <TextInput label="Filename Template" placeholder="{{.table}}-{{.id}}.json" value={config.filename_template || ''} onChange={(e) => updateConfig('filename_template', e.target.value)} required />
        <Select 
          label="Write Mode"
          placeholder="overwrite or append"
          data={[{ value: 'overwrite', label: 'Overwrite' }, { value: 'append', label: 'Append' }]}
          value={config.write_mode || 'overwrite'}
          onChange={(val) => updateConfig('write_mode', val || 'overwrite')}
        />
      </Group>
      <Group>
        <Button
          variant="light"
          onClick={() => {
            updateConfig('filename_template', config.filename_template || '{{.table}}-{{.id}}.csv');
            updateConfig('write_mode', config.write_mode || 'overwrite');
          }}
        >
          Use CSV preset (one file per message)
        </Button>
        <Button
          variant="light"
          onClick={() => {
            updateConfig('filename_template', '{{.table}}-{{.metadata.partitionDate}}.csv');
            updateConfig('write_mode', 'append');
          }}
        >
          Use CSV preset (append daily file)
        </Button>
      </Group>
      <Checkbox 
        label="Create Missing Directories"
        checked={config.mkdirs !== 'false'}
        onChange={(e) => updateConfig('mkdirs', e.target.checked ? 'true' : 'false')}
        my="sm"
      />
    </>
  );
}
