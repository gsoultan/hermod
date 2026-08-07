import { Button, Group, JsonInput, NumberInput, PasswordInput, Select, Stack, Tabs, TextInput } from '@mantine/core';
import { IconCloud, IconCode, IconPlayerPlay, IconSettings } from '@tabler/icons-react';

interface APILookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  testLookup?: () => void;
  testing?: boolean;
}

export function APILookupConfig({ config, updateNodeConfig, nodeId, testLookup, testing }: APILookupConfigProps) {
  return (

    <Tabs defaultValue="endpoint">
      <Tabs.List mb="md">
        <Tabs.Tab value="endpoint" leftSection={<IconCloud size="1rem" />}>Endpoint</Tabs.Tab>
        <Tabs.Tab value="payload" leftSection={<IconCode size="1rem" />}>Body/Headers</Tabs.Tab>
        <Tabs.Tab value="settings" leftSection={<IconSettings size="1rem" />}>Auth/Retry</Tabs.Tab>
      </Tabs.List>

      <Tabs.Panel value="endpoint">
        <Stack gap="sm">
          <Group grow>
            <Select
              label="Method"
              data={['GET', 'POST', 'PUT', 'DELETE', 'PATCH']}
              value={config.method || 'GET'}
              onChange={(val: string | null) => updateNodeConfig(nodeId, { method: val || 'GET' })}
            />
            <TextInput
              label="Target Field (Message)"
              placeholder="e.g. enriched_data"
              value={config.targetField || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { targetField: e.target.value })}
            />
          </Group>
          <TextInput
            label="URL"
            placeholder="https://api.example.com/v1/users/{{user_id}}"
            value={config.url || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { url: e.target.value })}
          />
          <TextInput
            label="Response JSON Path"
            placeholder="e.g. data.profile.name (Use '.' for root)"
            value={config.responsePath || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { responsePath: e.target.value })}
          />
          <Button 
            variant="light" 
            color="orange" 
            mt="xs"
            leftSection={<IconPlayerPlay size="0.8rem" />}
            onClick={testLookup}
            loading={testing}
          >
            Test API Call
          </Button>
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="payload">
        <Stack gap="sm">
          <JsonInput
            label="Headers (JSON)"
            placeholder='{"Authorization": "Bearer {{token}}", "X-Api-Key": "secret"}'
            value={config.headers || ''}
            onChange={(val: string) => updateNodeConfig(nodeId, { headers: val })}
            formatOnBlur
            minRows={4}
          />
          <JsonInput
            label="Query Params (JSON)"
            placeholder='{"id": "{{id}}", "ref": "hermod"}'
            value={config.queryParams || ''}
            onChange={(val: string) => updateNodeConfig(nodeId, { queryParams: val })}
            formatOnBlur
            minRows={4}
          />
          {config.method !== 'GET' && (
            <JsonInput
              label="Request Body (JSON)"
              placeholder='{"id": "{{user_id}}", "query": "..."}'
              value={config.body || ''}
              onChange={(val: string) => updateNodeConfig(nodeId, { body: val })}
              formatOnBlur
              minRows={6}
            />
          )}
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="settings">
        <Stack gap="sm">
          <Select
            label="Auth Type"
            data={[{label: 'None', value: ''}, {label: 'Basic', value: 'basic'}, {label: 'Bearer', value: 'bearer'}]}
            value={config.authType || ''}
            onChange={(val: string | null) => updateNodeConfig(nodeId, { authType: val || '' })}
          />
          {config.authType === 'basic' && (
            <Group grow>
              <TextInput
                label="Username"
                value={config.username || ''}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { username: e.target.value })}
              />
              <PasswordInput
                label="Password"
                value={config.password || ''}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { password: e.target.value })}
              />
            </Group>
          )}
          {config.authType === 'bearer' && (
            <PasswordInput
              label="Token"
              value={config.token || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { token: e.target.value })}
            />
          )}
          <Group grow>
            <TextInput
              label="Default Value"
              placeholder="Value if lookup fails"
              value={config.defaultValue || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { defaultValue: e.target.value })}
              description="Used if API call fails or returns no data."
            />
            <TextInput
              label="Timeout"
              placeholder="10s"
              value={config.timeout || ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { timeout: e.target.value })}
            />
          </Group>
          <Group grow>
            <TextInput
               label="Cache TTL"
               placeholder="e.g. 5m, 1h"
               value={config.ttl || ''}
               onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { ttl: e.target.value })}
            />
            <NumberInput
              label="Max Retries"
              value={config.maxRetries || 0}
              onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { maxRetries: val })}
            />
          </Group>
          <TextInput
            label="Retry Delay"
            placeholder="1s"
            value={config.retryDelay || ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { retryDelay: e.target.value })}
          />
        </Stack>
      </Tabs.Panel>
    </Tabs>
  
  );
}
