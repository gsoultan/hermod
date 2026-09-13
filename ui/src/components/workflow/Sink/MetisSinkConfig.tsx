import {
  TextInput,
  PasswordInput,
  Select,
  Stack,
  Divider,
  Switch,
  Alert,
  Code,
  Text,
} from '@mantine/core';
import { IconSitemap, IconAlertTriangle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface MetisSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The metis sink drives a BPMN 2.0 workflow engine: each message starts a
 * process, correlates a message into one already waiting, or broadcasts a
 * signal.
 *
 * Keys match `createSinkBase` case "metis" and `pkg/comm/sink/metis`. The three
 * actions each need a different name field, so only the one in play is shown —
 * and it is the only one `connectorRequirements` gates on.
 */
export function MetisSinkConfig({ config, updateConfig }: MetisSinkConfigProps) {
  const action = config.action || 'start_process';
  const idempotent = config.enable_idempotency === 'true';

  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <TextInput
          label="Engine URL"
          placeholder="https://bpm.example.com"
          value={config.base_url || ''}
          onChange={(e) => updateConfig('base_url', e.currentTarget.value)}
          description="The engine's origin, not a path — /api/v1 is added for you. Plaintext http is refused unless the host is loopback, because the token travels in a header."
          leftSection={<IconSitemap size="1rem" />}
          required
        />
        <TextInput
          label="Project ID"
          placeholder="0f8b1c2d-…"
          value={config.project_id || ''}
          onChange={(e) => updateConfig('project_id', e.currentTarget.value)}
          description="Required. The engine refuses an empty project rather than widening the call to the whole organization."
          required
        />
      </FormRow>

      <Divider label="Authentication" labelPosition="center" />

      <Text size="xs" c="dimmed">
        Supply a username and password, or a token from a secret store. Prefer the password for a
        long-running pipeline: the sink logs in again when the token expires, which a static token
        cannot do. Whoever the credential belongs to is who the engine records as the actor — it
        does not accept an override.
      </Text>

      <FormRow cols={2}>
        <TextInput
          label="Username"
          placeholder="hermod-service"
          value={config.username || ''}
          onChange={(e) => updateConfig('username', e.currentTarget.value)}
        />
        <PasswordInput
          label="Password"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.currentTarget.value)}
        />
      </FormRow>

      <FormRow cols={2}>
        <PasswordInput
          label="Token"
          placeholder="Paste a token instead of a password"
          value={config.token || ''}
          onChange={(e) => updateConfig('token', e.currentTarget.value)}
          description="Used as-is. It is not refreshed, so an expiring token stops the sink."
        />
        <TextInput
          label="Organization ID"
          placeholder="Leave blank unless the account has several"
          value={config.organization_id || ''}
          onChange={(e) => updateConfig('organization_id', e.currentTarget.value)}
        />
      </FormRow>

      <Divider label="What each message does" labelPosition="center" />

      <Select
        label="Action"
        value={action}
        onChange={(value) => updateConfig('action', value || 'start_process')}
        data={[
          { value: 'start_process', label: 'Start a process instance' },
          { value: 'send_message', label: 'Send a message to a waiting instance' },
          { value: 'broadcast_signal', label: 'Broadcast a signal to the project' },
        ]}
        description="Start begins a new run. A message answers one run that is blocked waiting for it. A signal reaches every run listening, and has no single addressee."
        allowDeselect={false}
      />

      {action === 'start_process' && (
        <TextInput
          label="Definition key"
          placeholder="order-fulfilment"
          value={config.definition_key || ''}
          onChange={(e) => updateConfig('definition_key', e.currentTarget.value)}
          description="The process ID from the BPMN diagram — the key, not the definition's ID, so a redeploy takes effect without editing this. Templated."
          required
        />
      )}

      {action === 'send_message' && (
        <FormRow cols={2}>
          <TextInput
            label="Message name"
            placeholder="payment-received"
            value={config.message_name || ''}
            onChange={(e) => updateConfig('message_name', e.currentTarget.value)}
            description="The BPMN message this correlates. Templated."
            required
          />
          <TextInput
            label="Correlation key"
            placeholder="{{.order_id}}"
            value={config.correlation_key || ''}
            onChange={(e) => updateConfig('correlation_key', e.currentTarget.value)}
            description="Selects which waiting instance receives it. Templated. Blank reaches every subscription for the name, which is rarely what a pipeline wants."
          />
        </FormRow>
      )}

      {action === 'broadcast_signal' && (
        <TextInput
          label="Signal name"
          placeholder="price-list-changed"
          value={config.signal_name || ''}
          onChange={(e) => updateConfig('signal_name', e.currentTarget.value)}
          description="Reaches every instance in the project waiting on it. Templated."
          required
        />
      )}

      <TextInput
        label="Variable fields"
        placeholder="Leave blank to send every field"
        value={config.variable_fields || ''}
        onChange={(e) => updateConfig('variable_fields', e.currentTarget.value)}
        description="Comma-separated. Narrows what becomes process variables; blank sends the row plus the envelope (id, operation, table, schema), with the row's own columns winning on a clash."
      />

      <TextInput
        label="Timeout"
        placeholder="30s"
        value={config.timeout || ''}
        onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
        description="Bounds one call. Blank uses the client's 30s default."
      />

      <Divider label="Duplicate processes" labelPosition="center" />

      <Alert icon={<IconAlertTriangle size="1rem" />} color="yellow" variant="light">
        Starting a process is not idempotent and the engine has no de-duplication key. When a call
        fails without a usable answer — a timeout, or a 5xx that may have committed the instance
        first — nobody knows whether the process started. With this on, the claim is kept and the
        retry is suppressed. With it off, the retry may start a second instance of somebody's
        business process.
      </Alert>

      <Switch
        label="Suppress duplicate calls"
        checked={idempotent}
        onChange={(e) => updateConfig('enable_idempotency', e.currentTarget.checked ? 'true' : 'false')}
      />

      {idempotent && (
        <>
          <TextInput
            label="Idempotency key template"
            placeholder="order-{{.order_id}}"
            value={config.idempotency_key_template || ''}
            onChange={(e) => updateConfig('idempotency_key_template', e.currentTarget.value)}
            description="Blank derives the key from the message and the resolved call. Set it to a business key so two events about one order start one process."
          />
          <FormRow cols={2}>
            <TextInput
              label="Claim TTL"
              placeholder="720h"
              value={config.idempotency_ttl || ''}
              onChange={(e) => updateConfig('idempotency_ttl', e.currentTarget.value)}
              description="Claims older than this are swept hourly. Blank never sweeps, which only grows — but a claim deleted early is a process that can start twice."
            />
            <TextInput
              label="Namespace"
              placeholder="Leave blank unless two metis sinks share a database"
              value={config.idempotency_namespace || ''}
              onChange={(e) => updateConfig('idempotency_namespace', e.currentTarget.value)}
              description="Suffixes the claim table, so two sinks cannot suppress each other's calls."
            />
          </FormRow>
          <TextInput
            label="Claim store DSN"
            placeholder="Leave blank to use Hermod's own database"
            value={config.idempotency_dsn || ''}
            onChange={(e) => updateConfig('idempotency_dsn', e.currentTarget.value)}
          />
        </>
      )}

      <Text size="xs" c="dimmed">
        Templated fields take the message: <Code>{'{{.order_id}}'}</Code> for a column,{' '}
        <Code>{'{{.table}}'}</Code> and <Code>{'{{.operation}}'}</Code> for the envelope.
      </Text>
    </Stack>
  );
}
