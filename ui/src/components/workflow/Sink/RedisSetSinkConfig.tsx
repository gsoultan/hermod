import { Select, Stack, Switch, Text } from '@mantine/core';
import { PasswordInput, TextInput } from '@/components/shared/ReferenceInputs';
import { TemplateField } from '@/components/shared/TemplateField';
import { FormRow } from '@/components/common/FormRow';

interface RedisSetSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
  /** The fields the editor says reach this sink; offered as `{{ }}` tokens. */
  availableFields?: Array<{ path: string; type?: string }>;
}

// The commands rediskv.ParseSettings accepts (pkg/infra/rediskv/rediskv.go).
export const REDIS_SET_COMMANDS = [
  { value: 'set', label: 'SET (string value)' },
  { value: 'hset', label: 'HSET (hash)' },
];

/**
 * The Redis Set sink: each record goes to a key. The key, hash field and value
 * take record fields as {{ }} tokens; the connection does not. Every value is
 * stored as text, as every sink config is.
 */
export function RedisSetSinkConfig({ config, updateConfig, availableFields = [] }: RedisSetSinkConfigProps) {
  const command = String(config.command || 'set').toLowerCase();

  return (
    <Stack gap="md">
      <FormRow>
        <TextInput
          label="Address"
          placeholder="redis.example.com:6379"
          value={config.addr || ''}
          onChange={(e) => updateConfig('addr', e.currentTarget.value)}
          required
        />
        <TextInput
          label="Database"
          placeholder="0"
          inputMode="numeric"
          value={config.db || ''}
          onChange={(e) => updateConfig('db', e.currentTarget.value)}
        />
      </FormRow>
      <FormRow>
        <TextInput
          label="Username"
          placeholder="Optional, for an ACL user"
          value={config.username || ''}
          onChange={(e) => updateConfig('username', e.currentTarget.value)}
        />
        <PasswordInput
          label="Password"
          placeholder="Optional"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.currentTarget.value)}
        />
      </FormRow>
      <Switch
        label="Use TLS"
        checked={config.tls === 'true'}
        onChange={(e) => updateConfig('tls', e.currentTarget.checked ? 'true' : 'false')}
      />

      <FormRow>
        <Select
          label="Command"
          data={REDIS_SET_COMMANDS}
          value={command}
          onChange={(val) => updateConfig('command', val || 'set')}
          allowDeselect={false}
        />
        <TemplateField
          label="Key"
          placeholder="customer:{{.after.id}}"
          value={config.key || ''}
          onChange={(key) => updateConfig('key', key)}
          availableFields={availableFields}
          functions="token"
          required
          mono
        />
      </FormRow>
      {command === 'hset' && (
        <TemplateField
          label="Hash Field"
          placeholder="Empty writes every field of the value"
          value={config.field || ''}
          onChange={(field) => updateConfig('field', field)}
          availableFields={availableFields}
          functions="token"
          mono
        />
      )}
      <TemplateField
        label="Value"
        placeholder={command === 'hset' && !config.field ? 'Empty writes the record as a hash' : 'Empty writes the whole record'}
        value={config.value || ''}
        onChange={(value) => updateConfig('value', value)}
        availableFields={availableFields}
        functions="token"
        mono
        description="A field such as {{.after.name}}, or JSON such as {&quot;name&quot;: &quot;{{.after.name}}&quot;}. An object field is written as JSON."
      />
      <FormRow>
        <TextInput
          label="Expire After (TTL)"
          placeholder="e.g. 1h, 30m"
          value={config.ttl || ''}
          onChange={(e) => updateConfig('ttl', e.currentTarget.value)}
          description="Needs a unit. Empty keeps the key until it is overwritten."
        />
        {command === 'set' && (
          <Switch
            mt="xl"
            label="Only if the key is absent"
            checked={config.onlyIfAbsent === 'true'}
            onChange={(e) => updateConfig('onlyIfAbsent', e.currentTarget.checked ? 'true' : 'false')}
          />
        )}
      </FormRow>
      <Text size="xs" c="dimmed">
        A record whose key has a {'{{ }}'} token with no value is refused rather than written, so
        records without that field never overwrite one shared key.
      </Text>
    </Stack>
  );
}
