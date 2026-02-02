import { TextInput, Stack, Select } from '@mantine/core';

interface SalesforceSinkConfigProps {
  form: any;
}

export function SalesforceSinkConfig({ form }: SalesforceSinkConfigProps) {
  return (
    <Stack gap="md">
      <TextInput
        label="Client ID"
        placeholder="Salesforce Connected App Client ID"
        required
        {...form.getInputProps('config.client_id')}
      />
      <TextInput
        label="Client Secret"
        placeholder="Salesforce Connected App Client Secret"
        required
        type="password"
        {...form.getInputProps('config.client_secret')}
      />
      <TextInput
        label="Username"
        placeholder="Salesforce Username"
        required
        {...form.getInputProps('config.username')}
      />
      <TextInput
        label="Password"
        placeholder="Salesforce Password"
        required
        type="password"
        {...form.getInputProps('config.password')}
      />
      <TextInput
        label="Security Token"
        placeholder="Salesforce Security Token"
        required
        type="password"
        {...form.getInputProps('config.security_token')}
      />
      <TextInput
        label="SObject"
        placeholder="e.g. Account, Contact, Lead"
        required
        {...form.getInputProps('config.object')}
      />
      <Select
        label="Operation"
        placeholder="Select operation"
        data={[
          { value: 'insert', label: 'Insert' },
          { value: 'update', label: 'Update' },
          { value: 'upsert', label: 'Upsert' },
          { value: 'delete', label: 'Delete' },
        ]}
        required
        {...form.getInputProps('config.operation')}
      />
      {form.values.config.operation === 'upsert' && (
        <TextInput
          label="External ID Field"
          placeholder="e.g. My_External_Id__c"
          required
          {...form.getInputProps('config.external_id')}
        />
      )}
    </Stack>
  );
}
