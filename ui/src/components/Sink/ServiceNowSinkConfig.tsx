import { TextInput, Stack } from '@mantine/core';

interface ServiceNowSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

export function ServiceNowSinkConfig({ config, updateConfig }: ServiceNowSinkConfigProps) {
  return (
    <Stack gap="md">
      <TextInput
        label="Instance URL"
        placeholder="https://dev12345.service-now.com"
        required
        value={config.instance_url || ''}
        onChange={(e) => updateConfig('instance_url', e.target.value)}
      />
      <TextInput
        label="Username"
        placeholder="ServiceNow Username"
        required
        value={config.username || ''}
        onChange={(e) => updateConfig('username', e.target.value)}
      />
      <TextInput
        label="Password"
        placeholder="ServiceNow Password"
        required
        type="password"
        value={config.password || ''}
        onChange={(e) => updateConfig('password', e.target.value)}
      />
      <TextInput
        label="Table Name"
        placeholder="e.g. incident, change_request"
        required
        value={config.table || ''}
        onChange={(e) => updateConfig('table', e.target.value)}
      />
    </Stack>
  );
}
