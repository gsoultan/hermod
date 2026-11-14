import { Stack, Select } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';

/**
 * The Salesforce sink's fields, in the shape every sink form takes: the
 * config and a setter (SinkWizard.tsx renders them all that way). It used to
 * expect a Mantine form object nothing passes, so choosing Salesforce on Add
 * sink threw and the page fell to the error screen.
 */
interface SalesforceSinkConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

const OPERATIONS = [
  { value: 'insert', label: 'Insert' },
  { value: 'update', label: 'Update' },
  { value: 'upsert', label: 'Upsert' },
  { value: 'delete', label: 'Delete' },
];

export function SalesforceSinkConfig({ config, updateConfig }: SalesforceSinkConfigProps) {
  const cfg = config ?? {};
  const text = (key: string) => ({
    value: cfg[key] ?? '',
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => updateConfig(key, e.currentTarget.value),
  });
  return (
    <Stack gap="md">
      <TextInput label="Client ID" placeholder="Salesforce Connected App Client ID" required {...text('client_id')} />
      <TextInput
        label="Client Secret"
        placeholder="Salesforce Connected App Client Secret"
        required
        type="password"
        {...text('client_secret')}
      />
      <TextInput label="Username" placeholder="Salesforce Username" required {...text('username')} />
      <TextInput label="Password" placeholder="Salesforce Password" required type="password" {...text('password')} />
      <TextInput
        label="Security Token"
        placeholder="Salesforce Security Token"
        required
        type="password"
        {...text('security_token')}
      />
      <TextInput label="SObject" placeholder="e.g. Account, Contact, Lead" required {...text('object')} />
      <Select
        label="Operation"
        placeholder="Select operation"
        data={OPERATIONS}
        required
        value={cfg.operation ?? null}
        onChange={(v) => updateConfig('operation', v ?? '')}
      />
      {cfg.operation === 'upsert' && (
        <TextInput
          label="External ID Field"
          placeholder="e.g. My_External_Id__c"
          required
          {...text('external_id')}
        />
      )}
    </Stack>
  );
}
