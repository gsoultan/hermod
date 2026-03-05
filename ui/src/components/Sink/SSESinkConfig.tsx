import { TextInput, Stack, Divider, Group, Badge, Text } from '@mantine/core';
import { IconWorld } from '@tabler/icons-react';
import { GenerateToken } from '../GenerateToken';

export function SSESinkConfig({ config, updateConfig }: { config: any; updateConfig: (k: string, v: any) => void }) {
  return (
    <Stack gap="md">
      <TextInput
        label="Stream Name"
        placeholder="e.g. order-updates"
        value={config.stream || ''}
        onChange={(e) => updateConfig('stream', e.currentTarget.value)}
        description="Logical stream name clients will subscribe to"
        required
      />

      <Divider label="Security" labelPosition="center" />

      <GenerateToken
        label="Auth Token"
        value={config.auth_token || ''}
        onChange={(val) => updateConfig('auth_token', val)}
      />
      <Text size="xs" c="dimmed">
        If set, clients must provide this token via 'Authorization: Bearer &lt;token&gt;' header or 'token' query parameter.
      </Text>

      <TextInput
        label="Allowed Origins"
        placeholder="https://app.example.com, http://localhost:3000"
        value={config.allowed_origins || ''}
        onChange={(e) => updateConfig('allowed_origins', e.currentTarget.value)}
        description="Comma-separated list of allowed origins for CORS. Use '*' to allow all (not recommended for production)."
        leftSection={<IconWorld size="1rem" />}
      />

      <Group gap="xs">
        <Badge color="blue" variant="light">SSE</Badge>
        <Badge color="green" variant="light">Real-time</Badge>
        {config.auth_token && <Badge color="orange" variant="light">Secured</Badge>}
      </Group>
    </Stack>
  );
}
