import { Stack, Tabs, Group, Select, TextInput, Button, JsonInput, PasswordInput } from '@mantine/core';
import { IconPlayerPlay, IconSettings, IconCode, IconCloud } from '@tabler/icons-react';

interface LookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: string[];
  onTest: () => void;
  testing: boolean;
}

export function LookupConfig({ config, updateNodeConfig, nodeId, onTest, testing }: LookupConfigProps) {
  return (
    <Tabs defaultValue="endpoint">
      <Tabs.List>
        <Tabs.Tab value="endpoint" leftSection={<IconCloud size="1rem" />}>Endpoint</Tabs.Tab>
        <Tabs.Tab value="payload" leftSection={<IconCode size="1rem" />}>Payload</Tabs.Tab>
        <Tabs.Tab value="settings" leftSection={<IconSettings size="1rem" />}>Settings</Tabs.Tab>
      </Tabs.List>

      <Tabs.Panel value="endpoint" pt="xs">
        <Stack gap="sm">
          <Group grow>
            <Select
              label="Method"
              data={['GET', 'POST', 'PUT', 'DELETE', 'PATCH']}
              value={config.method || 'GET'}
              onChange={(val) => updateNodeConfig(nodeId, { method: val || 'GET' })}
            />
            <TextInput
              label="Target Field (Message)"
              placeholder="e.g. enriched_data"
              value={config.targetField || ''}
              onChange={(e) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
            />
          </Group>
          <TextInput
            label="URL"
            placeholder="https://api.example.com/v1/users/{{user_id}}"
            value={config.url || ''}
            onChange={(e) => updateNodeConfig(nodeId, { url: e.currentTarget.value })}
          />
          <TextInput
            label="Response JSON Path"
            placeholder="e.g. data.profile.name (Use '.' for root)"
            value={config.responsePath || ''}
            onChange={(e) => updateNodeConfig(nodeId, { responsePath: e.currentTarget.value })}
          />
          <Button variant="light" color="orange" mt="xs" leftSection={<IconPlayerPlay size="0.8rem" />} onClick={onTest} loading={testing}>
            Test API Call
          </Button>
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="payload" pt="xs">
        <Stack gap="sm">
          <JsonInput label="Headers (JSON)" value={config.headers || ''} onChange={(val) => updateNodeConfig(nodeId, { headers: val })} formatOnBlur minRows={4} />
          <JsonInput label="Query Params (JSON)" value={config.queryParams || ''} onChange={(val) => updateNodeConfig(nodeId, { queryParams: val })} formatOnBlur minRows={4} />
          {config.method !== 'GET' && <JsonInput label="Request Body (JSON)" value={config.body || ''} onChange={(val) => updateNodeConfig(nodeId, { body: val })} formatOnBlur minRows={6} />}
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="settings" pt="xs">
        <Stack gap="sm">
          <Select label="Auth Type" data={[{label: 'None', value: ''}, {label: 'Basic', value: 'basic'}, {label: 'Bearer', value: 'bearer'}]} value={config.authType || ''} onChange={(val) => updateNodeConfig(nodeId, { authType: val || '' })} />
          {config.authType === 'basic' && (
            <Group grow>
              <TextInput label="Username" value={config.username || ''} onChange={(e) => updateNodeConfig(nodeId, { username: e.currentTarget.value })} />
              <PasswordInput label="Password" value={config.password || ''} onChange={(e) => updateNodeConfig(nodeId, { password: e.currentTarget.value })} />
            </Group>
          )}
          {config.authType === 'bearer' && <PasswordInput label="Token" value={config.token || ''} onChange={(e) => updateNodeConfig(nodeId, { token: e.currentTarget.value })} />}
        </Stack>
      </Tabs.Panel>
    </Tabs>
  );
}
