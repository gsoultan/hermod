import { Stack, TextInput } from '@mantine/core'
import { FormRow } from '@/components/common/FormRow';

interface S3SinkConfigProps {
  config: any
  updateConfig: (key: string, value: any) => void
}

/**
 * The keys here are the ones the s3 *sink* factory reads — `region`, `bucket`,
 * `key_prefix`, `access_key`, `secret_key`, `endpoint`. They used to be the
 * `s3_*` set, which is what the s3 *source* reads, so an S3 sink configured
 * from the editor was built with every field empty and SinkWizard's Next button
 * could never enable.
 */
export default function S3SinkConfig({ config, updateConfig }: S3SinkConfigProps) {
  return (
    <Stack gap="xs">
      <FormRow>
        <TextInput 
          label="Region" 
          placeholder="us-east-1" 
          value={config.region || ''} 
          onChange={(e) => updateConfig('region', e.target.value)} 
          description="AWS region"
          mih={80}
        />
        <TextInput 
          label="Bucket" 
          placeholder="my-templates" 
          value={config.bucket || ''} 
          onChange={(e) => updateConfig('bucket', e.target.value)} 
          description="S3 bucket name"
          mih={80}
        />
      </FormRow>
      <TextInput label="Key Prefix" placeholder="events/orders/" value={config.key_prefix || ''} onChange={(e) => updateConfig('key_prefix', e.target.value)} />
      <TextInput label="Endpoint" placeholder="Optional (for S3 compatible storage)" value={config.endpoint || ''} onChange={(e) => updateConfig('endpoint', e.target.value)} />
      <FormRow>
        <TextInput 
          label="Access Key" 
          value={config.access_key || ''} 
          onChange={(e) => updateConfig('access_key', e.target.value)} 
          description="AWS access key ID"
          mih={80}
        />
        <TextInput 
          label="Secret Key" 
          type="password" 
          value={config.secret_key || ''} 
          onChange={(e) => updateConfig('secret_key', e.target.value)} 
          description="AWS secret access key"
          mih={80}
        />
      </FormRow>
    </Stack>
  )
}
