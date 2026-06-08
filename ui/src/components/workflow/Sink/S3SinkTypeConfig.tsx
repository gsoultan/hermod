import { TextInput, Group, Text } from '@mantine/core';

interface S3SinkTypeConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

export function S3SinkTypeConfig({ config, updateConfig }: S3SinkTypeConfigProps) {
  return (
    <>
      <Group grow>
        <TextInput 
          label="Region" 
          placeholder="us-east-1" 
          value={config.region || ''} 
          onChange={(e) => updateConfig('region', e.target.value)} 
          required 
          description="AWS region"
          mih={80}
        />
        <TextInput 
          label="Bucket" 
          placeholder="my-bucket" 
          value={config.bucket || ''} 
          onChange={(e) => updateConfig('bucket', e.target.value)} 
          required 
          description="S3 bucket name"
          mih={80}
        />
      </Group>
      <TextInput label="Key Prefix" placeholder="events/" value={config.key_prefix || ''} onChange={(e) => updateConfig('key_prefix', e.target.value)} />
      <TextInput label="Endpoint (S3-compatible)" placeholder="e.g. http://localhost:9000" value={config.endpoint || ''} onChange={(e) => updateConfig('endpoint', e.target.value)} />
      <Group grow>
        <TextInput 
          label="Access Key" 
          placeholder="Optional" 
          value={config.access_key || ''} 
          onChange={(e) => updateConfig('access_key', e.target.value)} 
          description="AWS access key"
          mih={80}
        />
        <TextInput 
          label="Secret Key" 
          type="password" 
          placeholder="Optional" 
          value={config.secret_key || ''} 
          onChange={(e) => updateConfig('secret_key', e.target.value)} 
          description="AWS secret key"
          mih={80}
        />
      </Group>
      <Group grow>
        <TextInput 
          label="File Extension"
          placeholder=".json or .csv"
          value={config.suffix || ''}
          onChange={(e) => updateConfig('suffix', e.target.value)}
          description="S3 key extension"
          mih={80}
        />
        <TextInput 
          label="Content Type"
          placeholder="e.g. text/csv or application/json"
          value={config.content_type || ''}
          onChange={(e) => updateConfig('content_type', e.target.value)}
          description="S3 content-type"
          mih={80}
        />
      </Group>
      <Text size="sm" c="dimmed">
        Tip: To upload CSV bytes as-is, leave Format empty (pass-through) in the Advanced section and set File Extension to .csv.
      </Text>
    </>
  );
}
