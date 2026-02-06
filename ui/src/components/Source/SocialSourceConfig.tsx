import { TextInput, Stack, PasswordInput, Fieldset } from '@mantine/core';

interface SocialSourceConfigProps {
  type: string;
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

export function SocialSourceConfig({ type, config, updateConfig }: SocialSourceConfigProps) {
  if (type === 'discord') {
    return (
      <Stack gap="md">
        <TextInput label="Channel ID" value={config.channel_id || ''} onChange={(e) => updateConfig('channel_id', e.target.value)} required />
        <PasswordInput label="Bot Token" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} required />
      </Stack>
    );
  }

  if (type === 'slack') {
    return (
      <Stack gap="md">
        <TextInput label="Channel ID" value={config.channel_id || ''} onChange={(e) => updateConfig('channel_id', e.target.value)} required />
        <PasswordInput label="Bot Token" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} required />
      </Stack>
    );
  }

  // Generic for Facebook/Instagram/LinkedIn/TikTok
  return (
    <Stack gap="md">
      <Fieldset legend={`${type.charAt(0).toUpperCase() + type.slice(1)} API Settings`}>
        <Stack gap="sm">
          <TextInput label="Account ID / Page ID" value={config.account_id || ''} onChange={(e) => updateConfig('account_id', e.target.value)} required />
          <PasswordInput label="Access Token" value={config.access_token || ''} onChange={(e) => updateConfig('access_token', e.target.value)} required />
          <TextInput label="Poll Interval" placeholder="1h" value={config.poll_interval || '1h'} onChange={(e) => updateConfig('poll_interval', e.target.value)} />
        </Stack>
      </Fieldset>
    </Stack>
  );
}
