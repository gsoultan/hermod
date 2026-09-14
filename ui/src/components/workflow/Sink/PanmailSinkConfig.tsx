import {
  TextInput,
  PasswordInput,
  Textarea,
  Stack,
  Divider,
  Switch,
  Alert,
  Code,
  Text,
  NumberInput,
} from '@mantine/core';
import { IconMail, IconAlertTriangle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface PanmailSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The panmail sink sends each message as an email through a panmail gateway's
 * API, rather than through its SMTP door as the SMTP sink would.
 *
 * Keys match `createSinkBase` case "panmail" and `pkg/comm/sink/panmail`.
 */
export function PanmailSinkConfig({ config, updateConfig }: PanmailSinkConfigProps) {
  const idempotent = config.enable_idempotency === 'true';

  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <TextInput
          label="Gateway URL"
          placeholder="https://mail.example.com"
          value={config.base_url || ''}
          onChange={(e) => updateConfig('base_url', e.currentTarget.value)}
          description="The gateway's origin, not a path. Plaintext http is refused unless the host is loopback — the API key travels in a header."
          leftSection={<IconMail size="1rem" />}
          required
        />
        <PasswordInput
          label="API key"
          placeholder="Paste the key"
          value={config.api_key || ''}
          onChange={(e) => updateConfig('api_key', e.currentTarget.value)}
          description="Created under Settings → API Keys, with the email:send scope. It carries the tenant."
          required
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Provider ID"
          placeholder="0f8b…"
          value={config.provider_id || ''}
          onChange={(e) => updateConfig('provider_id', e.currentTarget.value)}
          description="From the Email Providers page. Required — the gateway will not guess, because the wrong guess sends from the wrong domain."
          required
        />
        <TextInput
          label="From"
          placeholder="noreply@example.com"
          value={config.from || ''}
          onChange={(e) => updateConfig('from', e.currentTarget.value)}
          description="Must be an address the provider is authorised to send as."
          required
        />
      </FormRow>

      <Text size="xs" c="dimmed">
        Recipients, subject and bodies are Go templates over the message:{' '}
        <Code>{'{{.id}}'}</Code>, <Code>{'{{.table}}'}</Code>, <Code>{'{{.operation}}'}</Code> and
        any field of the row. A rendered recipient containing commas expands into several
        addresses.
      </Text>

      <FormRow cols={1}>
        <TextInput
          label="To"
          placeholder="{{.email}}"
          value={config.to || ''}
          onChange={(e) => updateConfig('to', e.currentTarget.value)}
          description="Comma-separated."
          required
        />
      </FormRow>

      <FormRow cols={2}>
        <TextInput
          label="Cc"
          placeholder="ops@example.com"
          value={config.cc || ''}
          onChange={(e) => updateConfig('cc', e.currentTarget.value)}
        />
        <TextInput
          label="Bcc"
          placeholder="archive@example.com"
          value={config.bcc || ''}
          onChange={(e) => updateConfig('bcc', e.currentTarget.value)}
          description="Blind: the gateway keeps these off the message headers."
        />
      </FormRow>

      <FormRow cols={1}>
        <TextInput
          label="Subject"
          placeholder="Order {{.id}} confirmed"
          value={config.subject || ''}
          onChange={(e) => updateConfig('subject', e.currentTarget.value)}
        />
      </FormRow>

      <Divider my="xs" label="Body" labelPosition="left" />

      <FormRow cols={1}>
        <TextInput
          label="Template ID"
          placeholder="Leave blank to use the bodies below"
          value={config.template_id || ''}
          onChange={(e) => updateConfig('template_id', e.currentTarget.value)}
          description="Renders a template stored in the gateway, with the message as its data. The subject comes from the template unless you set one above."
        />
      </FormRow>

      <Textarea
        label="HTML body"
        placeholder="<p>Thanks, {{.name}}.</p>"
        value={config.html || ''}
        onChange={(e) => updateConfig('html', e.currentTarget.value)}
        autosize
        minRows={3}
        maxRows={12}
      />

      <Textarea
        label="Text body"
        placeholder="Thanks, {{.name}}."
        value={config.text || ''}
        onChange={(e) => updateConfig('text', e.currentTarget.value)}
        description="Send both when you can — the text part is what recipients with images off, screen readers and spam filters read."
        autosize
        minRows={2}
        maxRows={8}
      />

      <Divider my="xs" label="Delivery" labelPosition="left" />

      <FormRow cols={2}>
        <TextInput
          label="Timeout"
          placeholder="30s"
          value={config.timeout || ''}
          onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
          description="Bounds a single send. A Go duration; blank uses 30s."
        />
        <NumberInput
          label="Rate-limit retries"
          placeholder="0"
          min={0}
          value={config.rate_limit_retries === undefined || config.rate_limit_retries === ''
            ? ''
            : Number(config.rate_limit_retries)}
          onChange={(value) => updateConfig('rate_limit_retries', value === '' ? '' : String(value))}
          description="Waits out this many rate-limit refusals inside one write, for as long as the gateway asks. A full backlog is never waited out — it carries no delay."
        />
      </FormRow>

      <Divider my="xs" label="Duplicate suppression" labelPosition="left" />

      <Alert icon={<IconAlertTriangle size="1rem" />} color="yellow" variant="light">
        Sending is not idempotent and the gateway has no de-duplication key. When a send fails
        without an answer — a timeout, a dropped connection — nobody knows whether the mail went.
        With this on, the claim is kept and the retry is suppressed rather than mailing the
        recipient twice. With it off, the retry may deliver a second copy.
      </Alert>

      <Switch
        label="Suppress duplicate sends"
        checked={idempotent}
        onChange={(e) => updateConfig('enable_idempotency', e.currentTarget.checked ? 'true' : 'false')}
      />

      {idempotent && (
        <>
          <FormRow cols={2}>
            <TextInput
              label="Key template"
              placeholder="{{.id}}"
              value={config.idempotency_key_template || ''}
              onChange={(e) => updateConfig('idempotency_key_template', e.currentTarget.value)}
              description="Blank derives the key from the rendered recipients, subject and body."
            />
            <TextInput
              label="Retention"
              placeholder="720h"
              value={config.idempotency_ttl || ''}
              onChange={(e) => updateConfig('idempotency_ttl', e.currentTarget.value)}
              description="Go duration. Blank keeps every claim forever; a claim dropped early is a mail that can be sent again."
            />
          </FormRow>

          <FormRow cols={2}>
            <TextInput
              label="Namespace"
              placeholder="acme"
              value={config.idempotency_namespace || ''}
              onChange={(e) => updateConfig('idempotency_namespace', e.currentTarget.value)}
              description="Separates the claim table from other panmail sinks sharing the database."
            />
            <TextInput
              label="Store DSN"
              placeholder="Leave blank for the local hermod.db"
              value={config.idempotency_dsn || ''}
              onChange={(e) => updateConfig('idempotency_dsn', e.currentTarget.value)}
              description="SQLite. Must outlive the worker, or every restart forgets what it sent."
            />
          </FormRow>
        </>
      )}
    </Stack>
  );
}
