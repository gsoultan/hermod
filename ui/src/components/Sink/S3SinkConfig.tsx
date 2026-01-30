import { Group, Stack, TextInput } from '@mantine/core'

interface S3SinkConfigProps {
  config: any
  updateConfig: (key: string, value: any) => void
}

export default function S3SinkConfig({ config, updateConfig }: S3SinkConfigProps) {
  return (
    <Stack gap="xs">
      <Group grow>
        <TextInput label="Region" placeholder="us-east-1" value={config.s3_region || ''} onChange={(e) => updateConfig('s3_region', e.target.value)} />
        <TextInput label="Bucket" placeholder="my-templates" value={config.s3_bucket || ''} onChange={(e) => updateConfig('s3_bucket', e.target.value)} />
      </Group>
      <TextInput label="Key (Path)" placeholder="emails/welcome.html" value={config.s3_key || ''} onChange={(e) => updateConfig('s3_key', e.target.value)} />
      <TextInput label="Endpoint" placeholder="Optional (for S3 compatible storage)" value={config.s3_endpoint || ''} onChange={(e) => updateConfig('s3_endpoint', e.target.value)} />
      <Group grow>
        <TextInput label="Access Key" value={config.s3_access_key || ''} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
        <TextInput label="Secret Key" type="password" value={config.s3_secret_key || ''} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
      </Group>
    </Stack>
  )
}
