import {
  Stack,
  Tabs,
  Group,
  Select,
  TextInput,
  Button,
  JsonInput,
  PasswordInput,
  rem,
  Text,
  Alert,
  Card,
} from '@mantine/core';
import {
  IconPlayerPlay,
  IconInfoCircle,
  IconWorld,
  IconLock,
  IconBox,
} from '@tabler/icons-react';

interface LookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
  onTest: () => void;
  testing: boolean;
}

export function LookupConfig({
  config,
  updateNodeConfig,
  nodeId,
  onTest,
  testing,
}: LookupConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="blue"
        variant="light"
        radius="md"
        title="HTTP Enrichment"
      >
        <Text size="sm">
          Call an external API to enrich your data. Supports dynamic URLs and response mapping.
        </Text>
      </Alert>

      <Tabs defaultValue="endpoint" variant="pills" radius="md">
        <Tabs.List grow mb="md">
          <Tabs.Tab value="endpoint" leftSection={<IconWorld size={rem(16)} />}>
            Connection
          </Tabs.Tab>
          <Tabs.Tab value="payload" leftSection={<IconBox size={rem(16)} />}>
            Payload
          </Tabs.Tab>
          <Tabs.Tab value="settings" leftSection={<IconLock size={rem(16)} />}>
            Auth & Advanced
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="endpoint">
          <Card withBorder radius="md" p="md">
            <Stack gap="md">
              <Group grow align="flex-start">
                <Select
                  label="HTTP Method"
                  data={['GET', 'POST', 'PUT', 'DELETE', 'PATCH']}
                  value={config.method || 'GET'}
                  onChange={(val) => updateNodeConfig(nodeId, { method: val || 'GET' })}
                  size="sm"
                />
                <TextInput
                  label="Output Field"
                  placeholder="enriched_data"
                  value={config.targetField || ''}
                  onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
                  description="Where to store the result"
                  size="sm"
                />
              </Group>

              <TextInput
                label="Target URL"
                placeholder="https://api.example.com/v1/users/{{user_id}}"
                value={config.url || ''}
                onChange={(e) => updateNodeConfig(nodeId, { url: e.currentTarget.value })}
                description="Supports Go template syntax for dynamic values."
                size="sm"
              />

              <TextInput
                label="Extract from Response (JSON Path)"
                placeholder="data.profile.name (Use '.' for root)"
                value={config.responsePath || ''}
                onChange={(e) => updateNodeConfig(nodeId, { responsePath: e.currentTarget.value })}
                description="Optional path to select specific part of the response."
                size="sm"
              />

              <Button
                variant="filled"
                color="indigo"
                mt="sm"
                leftSection={<IconPlayerPlay size={rem(16)} />}
                onClick={onTest}
                loading={testing}
                fullWidth
              >
                Test Endpoint
              </Button>
            </Stack>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="payload">
          <Card withBorder radius="md" p="md">
            <Stack gap="md">
              <JsonInput
                label="HTTP Headers"
                placeholder='{"Content-Type": "application/json"}'
                value={config.headers || ''}
                onChange={(val) => updateNodeConfig(nodeId, { headers: val })}
                formatOnBlur
                minRows={4}
                size="sm"
                styles={{ input: { fontFamily: 'monospace' } }}
              />
              <JsonInput
                label="Query Parameters"
                placeholder='{"api_key": "secret"}'
                value={config.queryParams || ''}
                onChange={(val) => updateNodeConfig(nodeId, { queryParams: val })}
                formatOnBlur
                minRows={4}
                size="sm"
                styles={{ input: { fontFamily: 'monospace' } }}
              />
              {config.method !== 'GET' && (
                <JsonInput
                  label="Request Body"
                  placeholder='{"id": "{{user_id}}"}'
                  value={config.body || ''}
                  onChange={(val) => updateNodeConfig(nodeId, { body: val })}
                  formatOnBlur
                  minRows={6}
                  size="sm"
                  styles={{ input: { fontFamily: 'monospace' } }}
                />
              )}
            </Stack>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="settings">
          <Card withBorder radius="md" p="md">
            <Stack gap="md">
              <Select
                label="Authentication Type"
                data={[
                  { label: 'None', value: '' },
                  { label: 'Basic Auth', value: 'basic' },
                  { label: 'Bearer Token', value: 'bearer' },
                ]}
                value={config.authType || ''}
                onChange={(val) => updateNodeConfig(nodeId, { authType: val || '' })}
                size="sm"
              />

              {config.authType === 'basic' && (
                <Stack gap="xs">
                  <TextInput
                    label="Username"
                    value={config.username || ''}
                    onChange={(e) => updateNodeConfig(nodeId, { username: e.currentTarget.value })}
                    size="sm"
                  />
                  <PasswordInput
                    label="Password"
                    value={config.password || ''}
                    onChange={(e) => updateNodeConfig(nodeId, { password: e.currentTarget.value })}
                    size="sm"
                  />
                </Stack>
              )}

              {config.authType === 'bearer' && (
                <PasswordInput
                  label="Bearer Token"
                  value={config.token || ''}
                  onChange={(e) => updateNodeConfig(nodeId, { token: e.currentTarget.value })}
                  size="sm"
                />
              )}
            </Stack>
          </Card>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
