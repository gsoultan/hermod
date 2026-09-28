import { PasswordInput, Select, Stack, Text, TextInput } from '@mantine/core';

interface PanmailProvidersConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
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

export function PanmailProvidersConfig({ config, updateNodeConfig, nodeId }: PanmailProvidersConfigProps) {
  const set = (patch: Record<string, any>) => updateNodeConfig(nodeId, patch);

  return (
    <Stack gap="xs">
      <TextInput
        label="Gateway URL"
        placeholder="https://mail.example.com"
        value={config.baseUrl || ''}
        onChange={(e) => set({ baseUrl: e.currentTarget.value })}
        required
        description="The panmail gateway's address. Record values are not allowed here; {{env.NAME}} is."
      />
      <PasswordInput
        label="API Key"
        value={config.apiKey || ''}
        onChange={(e) => set({ apiKey: e.currentTarget.value })}
        required
        description={'Needs the providers:read scope. Prefer {{secret("PANMAIL_API_KEY")}} over pasting the key.'}
      />
      <TextInput
        label="Name contains"
        placeholder="Optional, e.g. prod or {{.after.region}}"
        value={config.name || ''}
        onChange={(e) => set({ name: e.currentTarget.value })}
      />
      <Select
        label="Provider type"
        data={PROVIDER_TYPES}
        value={config.providerType || ''}
        onChange={(val) => set({ providerType: val || '' })}
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
      <Text size="xs" c="dimmed">
        Writes a list of providers, each with id, name, type and allowedDomains, into the target field.
      </Text>
    </Stack>
  );
}
