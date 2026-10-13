import { Select, Stack, Text } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { TemplateField } from '@/components/shared/TemplateField';
import { FormRow } from '@/components/common/FormRow';

interface PanmailProvidersConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

// Short names the backend maps to panmail-sdk's provider types
// (panmailProviderTypes in pkg/comm/transformer/lookup/panmail_providers.go).
const PROVIDER_TYPES = [
  { label: 'Any', value: '' },
  { label: 'SMTP', value: 'smtp' },
  { label: 'SendGrid', value: 'sendgrid' },
  { label: 'Amazon SES', value: 'ses' },
  { label: 'Postmark', value: 'postmark' },
  { label: 'Mailgun', value: 'mailgun' },
  { label: 'IMAP (inbound only)', value: 'imap' },
  { label: 'POP3 (inbound only)', value: 'pop3' },
];

// The dot-atom local part isMailbox accepts in panmail_providers.go.
const MAILBOX = /^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$/;

function mailboxError(value: unknown): string | undefined {
  const v = String(value ?? '').trim();
  if (v === '') return undefined;
  if (v.includes('@')) return 'Only the part before the @, e.g. noreply; the domains come from the provider.';
  if (v.length > 64 || !MAILBOX.test(v)) return 'Letters, digits, dots and - _ + only, e.g. noreply.';
  return undefined;
}

export function PanmailProvidersConfig({ config, updateNodeConfig, nodeId, fieldPaths = [] }: PanmailProvidersConfigProps) {
  const set = (patch: Record<string, any>) => updateNodeConfig(nodeId, patch);

  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <TemplateField
          label="Gateway URL"
          placeholder="https://mail.example.com"
          value={config.baseUrl || ''}
          onChange={(baseUrl) => set({ baseUrl })}
          required
          secretsOnly
          mono
          description="The panmail gateway's address. Record values are never read here; a secret is."
        />
        <TemplateField
          label="API Key"
          placeholder={'{{secret("PANMAIL_API_KEY")}}'}
          value={config.apiKey || ''}
          onChange={(apiKey) => set({ apiKey })}
          required
          secretsOnly
          masked
          description="Needs the providers:read scope. Insert a secret rather than pasting the key."
        />
      </FormRow>
      <FormRow cols={2}>
        <TemplateField
          label="Name contains"
          placeholder="Optional, e.g. prod or {{.after.region}}"
          value={config.name || ''}
          onChange={(name) => set({ name })}
          availableFields={fieldPaths}
          functions="token"
        />
        <Select
          label="Provider type"
          data={PROVIDER_TYPES}
          value={config.providerType || ''}
          onChange={(val) => set({ providerType: val || '' })}
        />
      </FormRow>
      <FormRow cols={3}>
        <TextInput
          label="From mailbox"
          placeholder="noreply"
          value={config.fromMailbox || ''}
          onChange={(e) => set({ fromMailbox: e.currentTarget.value })}
          error={mailboxError(config.fromMailbox)}
          description="Composes fromEmail: noreply becomes noreply@ each allowed domain."
        />
        <TextInput
          label="Target Field"
          placeholder="panmail_providers"
          value={config.targetField || ''}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
        />
        <TextInput
          label="Cache TTL"
          placeholder="5m"
          value={config.ttl || ''}
          onChange={(e) => set({ ttl: e.currentTarget.value })}
          description="How long a list is reused, e.g. 30s, 5m, 1h. 0 turns the cache off."
        />
      </FormRow>
      <Text size="xs" c="dimmed">
        Writes a list of providers into the target field, each with id, name, type, allowedDomains,
        fromDomains (the domains it may send From), and fromEmail / fromEmails (the From mailbox at
        those domains; the gateway itself stores no From address).
      </Text>
    </Stack>
  );
}
