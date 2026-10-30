import { useMemo } from 'react';
import { Alert, Button, NumberInput, ScrollArea, Select, Stack, Switch, Tabs, Text } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { IconAlertTriangle, IconCircleCheck, IconPlayerPlay } from '@tabler/icons-react';
import { TemplateField } from '@/components/shared/TemplateField';
import type { APILookupTestOutcome } from './APILookupConfig';

interface RedisLookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  testLookup?: () => void;
  testing?: boolean;
  /** The node's available field paths, offered wherever a {{ }} token goes. */
  fieldPaths?: string[];
  lastTest?: APILookupTestOutcome | null;
}

// The reads redisCommands accepts in pkg/comm/transformer/lookup/redis_lookup.go.
const COMMANDS = [
  { value: 'get', label: 'GET (string value)' },
  { value: 'hget', label: 'HGET (one hash field)' },
  { value: 'hgetall', label: 'HGETALL (whole hash)' },
];

/**
 * A Redis read, laid out like API Lookup: what to read on top, Test under it,
 * then the connection and the settings on tabs. The key and field take record
 * values as {{ }} tokens; the connection takes secrets and env only, so a
 * record can never choose the server or the password (ResolveTemplateScoped).
 */
export function RedisLookupConfig({
  config,
  updateNodeConfig,
  nodeId,
  testLookup,
  testing,
  fieldPaths = [],
  lastTest,
}: RedisLookupConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch);
  const command = String(config.command || 'get').toLowerCase();

  // Mirrors resolveMissPolicy in pkg/comm/transformer/lookup/onmiss.go,
  // inference included, so the editor never names a policy the node is not
  // running.
  const hasDefaultValue = String(config.defaultValue || '') !== '';
  const missPolicy = useMemo(() => {
    const chosen = String(config.onMiss || '').trim().toLowerCase();
    if (chosen === 'fail' || chosen === 'default' || chosen === 'passthrough') return chosen;
    return hasDefaultValue ? 'default' : 'passthrough';
  }, [config.onMiss, hasDefaultValue]);

  return (
    <Stack gap="md">
      <Select
        label="Command"
        data={COMMANDS}
        value={command}
        onChange={(val) => set({ command: val || 'get' })}
        allowDeselect={false}
      />
      <TemplateField
        label="Key"
        placeholder="customer:{{.after.customer_id}}"
        value={config.key || ''}
        onChange={(key) => set({ key })}
        availableFields={fieldPaths}
        functions="token"
        mono
      />

      {command === 'hget' && (
        <TemplateField
          label="Hash Field"
          placeholder="{{.after.sku}}"
          value={config.field || ''}
          onChange={(field) => set({ field })}
          availableFields={fieldPaths}
          functions="token"
          mono
        />
      )}

      <TextInput
        label="Target Field (Message)"
        placeholder="e.g. customer"
        value={config.targetField || ''}
        onChange={(e) => set({ targetField: e.currentTarget.value })}
      />
      <TextInput
        label="Value JSON Path"
        placeholder="e.g. profile.name (empty for the whole value)"
        value={config.valuePath || ''}
        onChange={(e) => set({ valuePath: e.currentTarget.value })}
        description="Reads into a JSON object value, or into a hash."
      />

      <Button
        variant="light"
        color="orange"
        fullWidth
        leftSection={<IconPlayerPlay size="0.8rem" />}
        onClick={testLookup}
        // Never takes focus from a field it sits beside, so no blur can
        // re-render the form between mousedown and mouseup and eat the click.
        onMouseDown={(e) => e.preventDefault()}
        loading={testing}
      >
        Test Redis Lookup
      </Button>

      {lastTest && (
        <Alert
          data-testid="redis-lookup-test-result"
          variant="light"
          color={lastTest.ok ? 'green' : 'red'}
          icon={lastTest.ok ? <IconCircleCheck size="1rem" /> : <IconAlertTriangle size="1rem" />}
          title={lastTest.ok ? 'Last test succeeded' : 'Last test failed'}
          p="sm"
        >
          <ScrollArea.Autosize mah={180} type="auto" offsetScrollbars>
            <Text
              size="xs"
              style={{
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                fontFamily: 'var(--mantine-font-family-monospace)',
              }}
            >
              {lastTest.message}
            </Text>
          </ScrollArea.Autosize>
        </Alert>
      )}

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
              label="Default Value"
              placeholder="Value if the key is not found"
              value={config.defaultValue || ''}
              onChange={(e) => set({ defaultValue: e.currentTarget.value })}
            />
            <TextInput
              label="Flatten Into"
              placeholder="e.g. . or customer_"
              value={config.flattenInto || ''}
              onChange={(e) => set({ flattenInto: e.currentTarget.value })}
              description="Also writes each field of an object value: . for the top level."
            />
            <TextInput
              label="Cache TTL"
              placeholder="e.g. 30s, 5m"
              value={config.ttl || ''}
              onChange={(e) => set({ ttl: e.currentTarget.value })}
              description="Needs a unit. Leave empty for the 1m default, or set 0 to read Redis every time."
            />
            <TextInput
              label="Timeout"
              placeholder="5s"
              value={config.timeout || ''}
              onChange={(e) => set({ timeout: e.currentTarget.value })}
            />

            <Select
              label="When the key is not found"
              description="A miss is not automatically a bug, but it should be a decision. Without one, an enriched message and an un-enriched one reach the sink looking identical."
              data={[
                { value: 'passthrough', label: 'Pass the message through unchanged' },
                { value: 'default', label: 'Write the default value' },
                { value: 'fail', label: 'Fail the message' },
              ]}
              value={missPolicy}
              onChange={(val) => set({ onMiss: val || 'passthrough' })}
              allowDeselect={false}
              size="sm"
            />

            {missPolicy === 'default' && !hasDefaultValue && (
              <Alert color="orange" variant="light" data-testid="redis-lookup-miss-warning">
                <Text size="xs">
                  Nothing will be written on a miss: this policy needs a <b>Default Value</b>. Without one it behaves
                  exactly like passing the message through.
                </Text>
              </Alert>
            )}
            <Text size="xs" c="dimmed">
              A key whose {'{{ }}'} token has no value on the record is a miss too, and Redis is not asked. A read that{' '}
              <i>failed</i> (Redis unreachable, a timeout, or a key of the wrong type) fails the message unless a Default
              Value is set, and the <b>Fail</b> policy above overrides that.
            </Text>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
