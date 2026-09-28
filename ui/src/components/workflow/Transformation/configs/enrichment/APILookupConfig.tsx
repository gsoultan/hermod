import { useMemo } from 'react';
import { Alert, Badge, Box, Button, Group, NumberInput, PasswordInput, ScrollArea, Select, Stack, Tabs, Text, TextInput } from '@mantine/core';
import { IconAlertTriangle, IconCircleCheck, IconPlayerPlay } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';
import { InsertFieldButton } from './apiLookup/InsertFieldButton';
import { KeyValueEditor } from './apiLookup/KeyValueEditor';
import { RequestBodyEditor } from './apiLookup/RequestBodyEditor';
import { countJsonObjectEntries, fieldToken } from './apiLookup/jsonText';

/** What the last Test API Call said, kept on screen until the next one. */
export interface APILookupTestOutcome {
  ok: boolean;
  message: string;
}

interface APILookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  testLookup?: () => void;
  testing?: boolean;
  /** The node's available field paths, offered wherever a {{ }} token goes. */
  fieldPaths?: string[];
  lastTest?: APILookupTestOutcome | null;
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];

// Suggestions only: any header name can be typed.
const COMMON_HEADERS = [
  'Accept',
  'Authorization',
  'Cache-Control',
  'Content-Type',
  'Idempotency-Key',
  'User-Agent',
  'X-Api-Key',
  'X-Request-Id',
];

const Count = ({ n }: { n: number }) =>
  n > 0 ? (
    <Badge size="xs" variant="light" circle>
      {n}
    </Badge>
  ) : null;

const Dot = ({ on }: { on: boolean }) =>
  on ? <Box w={6} h={6} bg="var(--mantine-color-blue-filled)" style={{ borderRadius: '50%' }} aria-hidden /> : null;

/**
 * An HTTP request, edited the way an HTTP client edits one: method and URL on
 * one line, Test API Call under them whatever tab is open, then Params, Headers,
 * Body, Auth and Settings. The stored config is unchanged -- headers and
 * queryParams are JSON object text, body is text -- so the backend and every
 * saved workflow read it exactly as before.
 */
export function APILookupConfig({
  config,
  updateNodeConfig,
  nodeId,
  testLookup,
  testing,
  fieldPaths = [],
  lastTest,
}: APILookupConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch);
  const method = config.method || 'GET';

  // Mirrors resolveMissPolicy in pkg/comm/transformer/lookup/onmiss.go, inference
  // included: an unset onMiss means "default" when a defaultValue is configured
  // and "passthrough" otherwise. Showing anything else would name a policy the
  // pipeline is not running.
  const hasDefaultValue = String(config.defaultValue || '') !== '';
  const missPolicy = useMemo(() => {
    const chosen = String(config.onMiss || '').trim().toLowerCase();
    if (chosen === 'fail' || chosen === 'default' || chosen === 'passthrough') return chosen;
    return hasDefaultValue ? 'default' : 'passthrough';
  }, [config.onMiss, hasDefaultValue]);

  return (
    <Stack gap="md">
      <Group gap="xs" align="flex-end" wrap="nowrap">
        <Select
          label="Method"
          data={METHODS}
          value={method}
          onChange={(val) => set({ method: val || 'GET' })}
          allowDeselect={false}
          w={112}
        />
        <TextInput
          label="URL"
          placeholder="https://api.example.com/v1/users/{{.after.user_id}}"
          value={config.url || ''}
          onChange={(e) => set({ url: e.currentTarget.value })}
          style={{ flex: 1, minWidth: 0 }}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
          rightSection={
            fieldPaths.length > 0 ? (
              <InsertFieldButton
                compact
                label="Insert field into URL"
                fieldPaths={fieldPaths}
                onPick={(path) => set({ url: (config.url || '') + fieldToken(path) })}
              />
            ) : undefined
          }
        />
      </Group>

      <FormRow>
        <TextInput
          label="Target Field (Message)"
          placeholder="e.g. enriched_data"
          value={config.targetField || ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
        <TextInput
          label="Response JSON Path"
          placeholder="e.g. data.profile.name (Use '.' for root)"
          value={config.responsePath || ''}
          onChange={(e) => set({ responsePath: e.currentTarget.value })}
        />
      </FormRow>

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
        Test API Call
      </Button>

      {lastTest && (
        <Alert
          data-testid="api-lookup-test-result"
          variant="light"
          color={lastTest.ok ? 'green' : 'red'}
          icon={lastTest.ok ? <IconCircleCheck size="1rem" /> : <IconAlertTriangle size="1rem" />}
          title={lastTest.ok ? 'Last test succeeded' : 'Last test failed'}
          p="sm"
        >
          {/* Capped, so a large response does not push the request below the fold. */}
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

      {/* Tighter tabs: at the default padding the fifth tab wrapped onto a second
          line in the editor's middle column. */}
      <Tabs
        defaultValue={method === 'GET' ? 'params' : 'body'}
        keepMounted={false}
        styles={{ tab: { paddingInline: 'var(--mantine-spacing-xs)' } }}
      >
        <Tabs.List>
          <Tabs.Tab value="params" rightSection={<Count n={countJsonObjectEntries(config.queryParams || '')} />}>
            Params
          </Tabs.Tab>
          <Tabs.Tab value="headers" rightSection={<Count n={countJsonObjectEntries(config.headers || '')} />}>
            Headers
          </Tabs.Tab>
          <Tabs.Tab value="body" rightSection={<Dot on={String(config.body || '').trim() !== ''} />}>
            Body
          </Tabs.Tab>
          <Tabs.Tab value="auth" rightSection={<Dot on={!!config.authType} />}>
            Auth
          </Tabs.Tab>
          <Tabs.Tab value="settings">Settings</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="params" pt="md">
          <KeyValueEditor
            noun="Query param"
            jsonLabel="Query Params (JSON)"
            jsonPlaceholder='{"id": "{{.after.user_id}}", "ref": "hermod"}'
            emptyHint="No query params. Each one is added to the URL and encoded, {{ }} fields included."
            value={config.queryParams || ''}
            onChange={(queryParams) => set({ queryParams })}
            fieldPaths={fieldPaths}
          />
        </Tabs.Panel>

        <Tabs.Panel value="headers" pt="md">
          <KeyValueEditor
            noun="Header"
            jsonLabel="Headers (JSON)"
            jsonPlaceholder='{"Authorization": "Bearer {{.after.token}}", "X-Api-Key": "secret"}'
            emptyHint="No headers. A JSON body is sent as application/json unless you set a Content-Type here."
            value={config.headers || ''}
            onChange={(headers) => set({ headers })}
            keySuggestions={COMMON_HEADERS}
            caseInsensitiveKeys
            fieldPaths={fieldPaths}
          />
        </Tabs.Panel>

        <Tabs.Panel value="body" pt="md">
          <RequestBodyEditor
            value={config.body || ''}
            onChange={(body) => set({ body })}
            method={method}
            fieldPaths={fieldPaths}
          />
        </Tabs.Panel>

        <Tabs.Panel value="auth" pt="md">
          <Stack gap="sm">
            <Select
              label="Auth Type"
              data={[
                { label: 'None', value: '' },
                { label: 'Basic', value: 'basic' },
                { label: 'Bearer', value: 'bearer' },
              ]}
              value={config.authType || ''}
              onChange={(val) => set({ authType: val || '' })}
            />
            {config.authType === 'basic' && (
              <FormRow>
                <TextInput
                  label="Username"
                  value={config.username || ''}
                  onChange={(e) => set({ username: e.currentTarget.value })}
                />
                <PasswordInput
                  label="Password"
                  value={config.password || ''}
                  onChange={(e) => set({ password: e.currentTarget.value })}
                />
              </FormRow>
            )}
            {config.authType === 'bearer' && (
              <PasswordInput
                label="Token"
                value={config.token || ''}
                onChange={(e) => set({ token: e.currentTarget.value })}
              />
            )}
            {!config.authType && (
              <Text size="xs" c="dimmed">
                No credentials are added. An Authorization header set on the Headers tab is still sent.
              </Text>
            )}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="settings" pt="md">
          <Stack gap="sm">
            <FormRow>
              <TextInput
                label="Default Value"
                placeholder="Value if lookup fails"
                value={config.defaultValue || ''}
                onChange={(e) => set({ defaultValue: e.currentTarget.value })}
                description="Used if API call fails or returns no data."
              />
              <TextInput
                label="Timeout"
                placeholder="10s"
                value={config.timeout || ''}
                onChange={(e) => set({ timeout: e.currentTarget.value })}
              />
            </FormRow>
            <FormRow>
              <TextInput
                label="Cache TTL"
                placeholder="e.g. 5m, 1h"
                value={config.ttl || ''}
                onChange={(e) => set({ ttl: e.currentTarget.value })}
                description={
                  <span data-testid="api-lookup-ttl-description">
                    Needs a unit. Leave empty for the 5m default, or set 0 to disable caching.
                  </span>
                }
              />
              <NumberInput
                label="Max Retries"
                value={config.maxRetries || 0}
                onChange={(val: string | number | undefined) => set({ maxRetries: val })}
              />
            </FormRow>
            <TextInput
              label="Retry Delay"
              placeholder="1s"
              value={config.retryDelay || ''}
              onChange={(e) => set({ retryDelay: e.currentTarget.value })}
            />

            <Select
              label="When the lookup returns nothing"
              description="A miss is not automatically a bug, but it should be a decision — without one, an enriched message and an un-enriched one reach the sink looking identical."
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
              <Alert color="orange" variant="light" data-testid="api-lookup-miss-warning">
                <Text size="xs">
                  Nothing will be written on a miss: this policy needs a <b>Default Value</b>. Without one it behaves
                  exactly like passing the message through.
                </Text>
              </Alert>
            )}
            <Text size="xs" c="dimmed">
              This covers a call that succeeded and returned nothing at the response path. A request that{' '}
              <i>failed</i> — a timeout, or a non-2xx status — fails the message unless a Default Value is set, and
              the <b>Fail</b> policy above overrides that.
            </Text>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
