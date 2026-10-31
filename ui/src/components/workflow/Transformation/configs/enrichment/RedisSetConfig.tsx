import { NumberInput, Select, Stack, Switch, Tabs, Text } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { TemplateField } from '@/components/shared/TemplateField';
import { REDIS_SET_COMMANDS } from '@/components/workflow/Sink/RedisSetSinkConfig';

interface RedisSetConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  /** The node's available field paths, offered wherever a {{ }} token goes. */
  fieldPaths?: string[];
}

/**
 * The Redis Set step, laid out like Redis Lookup: what to write on top, then
 * the connection and the settings on tabs. It writes the same keys as the
 * Redis Set sink (pkg/infra/rediskv), and passes the record on unchanged.
 */
export function RedisSetConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: RedisSetConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch);
  const command = String(config.command || 'set').toLowerCase();
  const onlyIfAbsent = config.onlyIfAbsent === true || config.onlyIfAbsent === 'true';

  return (
    <Stack gap="md">
      <Select
        label="Command"
        data={REDIS_SET_COMMANDS}
        value={command}
        onChange={(val) => set({ command: val || 'set' })}
        allowDeselect={false}
      />
      <TemplateField
        label="Key"
        placeholder="customer:{{.after.id}}"
        value={config.key || ''}
        onChange={(key) => set({ key })}
        availableFields={fieldPaths}
        functions="token"
        mono
      />
      {command === 'hset' && (
        <TemplateField
          label="Hash Field"
          placeholder="Empty writes every field of the value"
          value={config.field || ''}
          onChange={(field) => set({ field })}
          availableFields={fieldPaths}
          functions="token"
          mono
        />
      )}
      <TemplateField
        label="Value"
        placeholder={command === 'hset' && !config.field ? 'Empty writes the record as a hash' : 'Empty writes the whole record'}
        value={config.value || ''}
        onChange={(value) => set({ value })}
        availableFields={fieldPaths}
        functions="token"
        mono
        description="A field such as {{.after.name}}, or JSON such as {&quot;name&quot;: &quot;{{.after.name}}&quot;}. An object field is written as JSON."
      />

      <Tabs
        defaultValue={config.addr ? 'settings' : 'connection'}
        keepMounted={false}
        styles={{ tab: { paddingInline: 'var(--mantine-spacing-xs)' } }}
      >
        <Tabs.List>
          <Tabs.Tab value="connection">Connection</Tabs.Tab>
          <Tabs.Tab value="settings">Settings</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="connection" pt="md">
          <Stack gap="sm">
            <TemplateField
              label="Address"
              placeholder="redis:6379"
              value={config.addr || ''}
              onChange={(addr) => set({ addr })}
              required
              secretsOnly
              mono
              description="host:port. Record values are never read here; a secret or env value is."
            />
            <NumberInput
              label="Database"
              placeholder="0"
              min={0}
              allowDecimal={false}
              value={config.db ?? ''}
              onChange={(val) => set({ db: val === '' ? undefined : Number(val) })}
            />
            <TemplateField
              label="Username"
              placeholder="Optional, for an ACL user"
              value={config.username || ''}
              onChange={(username) => set({ username })}
              secretsOnly
            />
            <TemplateField
              label="Password"
              placeholder="{{secret.REDIS_PASSWORD}}"
              value={config.password || ''}
              onChange={(password) => set({ password })}
              secretsOnly
              masked
              description="Insert a secret rather than pasting the password."
            />
            <Switch
              label="Use TLS"
              checked={config.tls === true || config.tls === 'true'}
              onChange={(e) => set({ tls: e.currentTarget.checked })}
            />
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="settings" pt="md">
          <Stack gap="sm">
            <TextInput
              label="Expire After (TTL)"
              placeholder="e.g. 1h, 30m"
              value={config.ttl || ''}
              onChange={(e) => set({ ttl: e.currentTarget.value })}
              description="Needs a unit. Empty keeps the key until it is overwritten."
            />
            <TextInput
              label="Timeout"
              placeholder="5s"
              value={config.timeout || ''}
              onChange={(e) => set({ timeout: e.currentTarget.value })}
            />
            {command === 'set' && (
              <Switch
                label="Only if the key is absent"
                checked={onlyIfAbsent}
                onChange={(e) => set({ onlyIfAbsent: e.currentTarget.checked })}
              />
            )}
            <Select
              label="When the write fails"
              description="Redis unreachable, a timeout, or a key of the wrong type."
              data={[
                { value: 'fail', label: 'Fail the record' },
                { value: 'passthrough', label: 'Pass the record on anyway' },
              ]}
              value={config.onError === 'passthrough' ? 'passthrough' : 'fail'}
              onChange={(val) => set({ onError: val || 'fail' })}
              allowDeselect={false}
              size="sm"
            />
            <Text size="xs" c="dimmed">
              A record whose key has a {'{{ }}'} token with no value fails rather than writing, so records
              without that field never overwrite one shared key. For Redis as the destination of a workflow,
              use the Redis Set sink, which takes the same settings.
            </Text>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
