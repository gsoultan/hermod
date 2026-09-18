import { Stack, TextInput, PasswordInput, JsonInput } from '@mantine/core'
import { FormRow } from '@/components/common/FormRow';

interface S3ParquetSinkConfigProps {
  config: any
  updateConfig: (key: string, value: any) => void
}

/**
 * The parquet sink has its own form because it reads its own config keys.
 *
 * Every other S3 user — the file source, the plain S3 sink — is configured with
 * `s3_region`, `s3_bucket` and so on, and shares S3SinkConfig. The parquet sink
 * reads `region`, `bucket`, `key_prefix`, `access_key`, `secret_key` and
 * `endpoint`, so pointing it at the shared form produced a sink with every
 * field empty, and it had nowhere at all to put the parquet schema it cannot
 * write without.
 */
export default function S3ParquetSinkConfig({ config, updateConfig }: S3ParquetSinkConfigProps) {
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
          placeholder="my-data-lake"
          value={config.bucket || ''}
          onChange={(e) => updateConfig('bucket', e.target.value)}
          description="S3 bucket name"
          mih={80}
        />
      </FormRow>
      <TextInput
        label="Key Prefix"
        placeholder="events/orders/"
        value={config.key_prefix || ''}
        onChange={(e) => updateConfig('key_prefix', e.target.value)}
        description="Each batch is written as a new object under this prefix"
      />
      <TextInput
        label="Endpoint"
        placeholder="Optional (for S3 compatible storage)"
        value={config.endpoint || ''}
        onChange={(e) => updateConfig('endpoint', e.target.value)}
      />
      <FormRow>
        <TextInput
          label="Access Key"
          value={config.access_key || ''}
          onChange={(e) => updateConfig('access_key', e.target.value)}
          description="AWS access key ID"
          mih={80}
        />
        <PasswordInput
          label="Secret Key"
          value={config.secret_key || ''}
          onChange={(e) => updateConfig('secret_key', e.target.value)}
          description="AWS secret access key"
          mih={80}
        />
      </FormRow>
      <JsonInput
        label="Parquet Schema"
        placeholder='{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]}'
        value={config.schema || ''}
        onChange={(val) => updateConfig('schema', val)}
        description="Columns to write, in parquet-go JSON schema form"
        validationError="Not valid JSON"
        autosize
        minRows={4}
      />
      <TextInput
        label="Operation Column"
        placeholder="operation"
        value={config.operation_field || ''}
        onChange={(e) => updateConfig('operation_field', e.target.value)}
        description="Column to record create / update / delete in, so a delete does not land as another insert. Must be declared in the schema above; left blank, 'operation' is used when the schema has it."
      />
    </Stack>
  )
}
