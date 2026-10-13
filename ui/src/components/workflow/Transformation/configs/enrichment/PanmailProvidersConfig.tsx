import { Select, Stack, Text, TextInput } from '@mantine/core';
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
      <FormRow cols={2}>
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
        Writes a list of providers, each with id, name, type and allowedDomains, into the target field.
      </Text>
    </Stack>
  );
}
