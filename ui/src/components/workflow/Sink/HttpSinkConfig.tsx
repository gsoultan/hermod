import { TextInput, Select, Stack, Textarea } from '@mantine/core';
import { IconWorld } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface HttpSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The "API / Webhook" sink — `http` in the factory.
 *
 * The four keys here are exactly the four `createSinkBase` reads for this type
 * (internal/factory/factory.go, case "http"). Two of them, compression and
 * timeout, had no input anywhere in the UI, so a sink could only get them by
 * being created through the REST API.
 */
export function HttpSinkConfig({ config, updateConfig }: HttpSinkConfigProps) {
  return (
    <Stack gap="md">
      <FormRow cols={1}>
        <TextInput
          label="Destination URL"
          placeholder="https://api.example.com/ingest"
          value={config.url || ''}
          onChange={(e) => updateConfig('url', e.currentTarget.value)}
          description="Each message is POSTed here. A non-2xx response fails the write and the message is retried."
          leftSection={<IconWorld size="1rem" />}
          required
        />
      </FormRow>

      <FormRow cols={1}>
        <Textarea
          label="Headers"
          placeholder="Authorization: Bearer token, X-Tenant: acme"
          value={config.headers || ''}
          onChange={(e) => updateConfig('headers', e.currentTarget.value)}
          description="Comma-separated Name: value pairs. Content-Type defaults to application/json when you do not set it."
          autosize
          minRows={2}
        />
      </FormRow>

      <FormRow cols={2}>
        <Select
          label="Compression"
          value={config.compression || ''}
          onChange={(value) => updateConfig('compression', value || '')}
          data={[
            { value: '', label: 'None' },
            { value: 'zstd', label: 'Zstandard' },
            { value: 'lz4', label: 'LZ4' },
            { value: 'snappy', label: 'Snappy' },
          ]}
          description="Bodies over 1 KB are compressed and sent with a matching Content-Encoding — but only when the compressed form is actually smaller."
        />
        <TextInput
          label="Timeout"
          placeholder="30s"
          value={config.timeout || ''}
          onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
          description="Bounds the whole exchange. Defaults to 30s; a Go duration such as 5s or 2m."
        />
      </FormRow>
    </Stack>
  );
}
