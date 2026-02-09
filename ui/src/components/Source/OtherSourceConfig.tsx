import { TextInput, Stack, Group, Select, JsonInput, Text, Divider, Button, Badge, List, Paper, Box, Fieldset, SimpleGrid } from '@mantine/core';
import { CronInput } from '../CronInput';
import { SQLQueryBuilder } from '../SQLQueryBuilder';
import { GenerateToken } from '../GenerateToken';
import { FormLayoutBuilder, type FormFieldItem } from '../FormLayoutBuilder';
import { useState } from 'react';
import type { Source } from '../../types';
import { IconLayout } from '@tabler/icons-react';
interface OtherSourceConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  sourceType: string;
  allSources?: Source[];
}

export function OtherSourceConfig({ config, updateConfig, sourceType, allSources = [] }: OtherSourceConfigProps) {
  const [builderOpened, setBuilderOpened] = useState(false);

  const formFields: FormFieldItem[] = Array.isArray(config.fields) ? config.fields : [];

  if (sourceType === 'cron') {
    return (
      <Stack gap="md">
        <CronInput 
          label="Cron Schedule" 
          placeholder="*/5 * * * *" 
          value={config.schedule} 
          onChange={(val) => updateConfig('schedule', val)} 
          description="Standard cron expression or '@every 5m'. Example: '0 * * * *' (every hour)."
          required 
        />
        <JsonInput
          label="Static Payload"
          placeholder='{ "action": "trigger", "data": "hello" }'
          value={config.payload}
          onChange={(val) => updateConfig('payload', val)}
          description="The message payload that will be emitted when the cron triggers."
          minRows={10}
          formatOnBlur
        />
      </Stack>
    );
  }

  if (sourceType === 'webhook' || sourceType === 'form') {
    return (
      <Stack gap="md">
        <TextInput 
          label={`${sourceType === 'webhook' ? 'Webhook' : 'Form'} Path`}
          placeholder={`/api/${sourceType === 'webhook' ? 'webhooks' : 'forms'}/my-source`}
          value={config.path} 
          onChange={(e) => updateConfig('path', e.target.value)} 
          description={`Relative path for the ${sourceType}. Full URL will be: http://hermod-host:8080/api/${sourceType === 'webhook' ? 'webhooks' : 'forms'}/YOUR_PATH.`}
          required 
        />
        {sourceType === 'webhook' && (
          <Select 
            label="HTTP Method" 
            data={['POST', 'PUT', 'GET']} 
            value={config.method || 'POST'} 
            onChange={(val) => updateConfig('method', val || 'POST')} 
          />
        )}
        
        {sourceType === 'form' && (
          <Stack gap="xs">
            <Group justify="space-between">
              <Text size="sm" fw={500}>Form Layout</Text>
              <Badge variant="light">{formFields.length} fields</Badge>
            </Group>
            <Button 
              leftSection={<IconLayout size="1rem" />} 
              variant="light" 
              onClick={() => setBuilderOpened(true)}
            >
              Configure Form Fields
            </Button>
            {formFields.length > 0 && (
              <Paper withBorder p="xs" radius="sm" bg="gray.0">
                <Text size="xs" fw={700} c="dimmed" mb={4}>FIELDS SUMMARY:</Text>
                <Box style={{ maxHeight: '100px', overflowY: 'auto' }}>
                  <List size="xs" spacing={2}>
                    {formFields.map((f, i) => (
                      <List.Item key={f.id || i}>
                        <Group gap={4}>
                          <Text span fw={500}>{f.label || f.name || f.type}</Text>
                          <Badge size="xs" variant="outline">{f.type}</Badge>
                        </Group>
                      </List.Item>
                    ))}
                  </List>
                </Box>
              </Paper>
            )}
            <FormLayoutBuilder 
              opened={builderOpened}
              onClose={() => setBuilderOpened(false)}
              initialFields={formFields}
              initialTitle={config.form_title}
              initialDescription={config.form_description}
              onApply={(fields, title, description) => {
                updateConfig('fields', fields);
                if (title) updateConfig('form_title', title);
                if (description) updateConfig('form_description', description);
                setBuilderOpened(false);
              }}
            />

            <Divider my="sm" label="Submission Parameters" labelPosition="left" />
            <Fieldset radius="md" legend="Parameters to include with submissions">
              <Stack gap="sm">
                <Text size="xs" c="dimmed">These parameters are merged into the submitted payload by the server and/or client embed.</Text>
                <TextInput 
                  label="Include Query Params (comma-separated keys)"
                  placeholder="utm_source, campaign, user_id"
                  value={config.form_param_keys || ''}
                  onChange={(e) => updateConfig('form_param_keys', e.target.value)}
                />
                <JsonInput 
                  label="Extra Params (JSON)"
                  placeholder='{ "app": "marketing-site", "channel": "lead" }'
                  value={config.form_extra_params || ''}
                  onChange={(val) => updateConfig('form_extra_params', val)}
                  minRows={6}
                  formatOnBlur
                />
              </Stack>
            </Fieldset>
          </Stack>
        )}

        <GenerateToken 
          label="API Key (Optional)"
          value={config.api_key || ''}
          onChange={(val) => updateConfig('api_key', val)}
        />
        <Text size="xs" c="dimmed">If provided, requests must include 'X-API-Key' header with this value.</Text>
      </Stack>
    );
  }

  if (sourceType === 'batch_sql') {
    return (
      <Stack gap="md">
        <Select 
          label="Database Source" 
          placeholder="Select source to run queries against"
          data={allSources
            .filter((s: Source) => ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'sqlite', 'clickhouse'].includes(s.type))
            .map((s: Source) => ({ value: s.id, label: `${s.name} (${s.type})` }))}
          value={config.source_id}
          onChange={(val) => updateConfig('source_id', val || '')}
          required
        />
        <CronInput 
          label="Cron Schedule" 
          placeholder="*/5 * * * *" 
          value={config.cron} 
          onChange={(val) => updateConfig('cron', val)} 
          required
          description="Standard cron expression (e.g. */5 * * * * for every 5 minutes)"
        />
        <TextInput 
          label="Incremental Column" 
          placeholder="id or created_at" 
          value={config.incremental_column} 
          onChange={(e) => updateConfig('incremental_column', e.target.value)} 
          description="Column used to track progress between runs"
        />
        <Stack gap="xs">
          <Text size="sm" fw={500}>SQL Queries</Text>
          <Text size="xs" c="dimmed">Use {"{{.last_value}}"} to reference the last seen value from the incremental column.</Text>
          <JsonInput
            placeholder='["SELECT * FROM users WHERE id > {{.last_value}}", "SELECT * FROM orders WHERE id > {{.last_value}}"]'
            value={config.queries}
            onChange={(val) => updateConfig('queries', val)}
            minRows={8}
            autosize
            maxRows={20}
            styles={{ input: { fontFamily: 'monospace', fontSize: '13px' } }}
            formatOnBlur
          />
          {config.source_id && (
            <Stack gap="xs">
              <Divider label="Test Query" labelPosition="center" />
              <SQLQueryBuilder 
                type="source" 
                config={allSources.find((s: Source) => s.id === config.source_id)?.config || {}} 
              />
            </Stack>
          )}
        </Stack>
      </Stack>
    );
  }

  if (sourceType === 'googlesheets') {
    return (
      <Stack gap="md">
        <TextInput 
          label="Spreadsheet ID" 
          placeholder="1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms" 
          value={config.spreadsheet_id} 
          onChange={(e) => updateConfig('spreadsheet_id', e.target.value)} 
          required 
        />
        <TextInput 
          label="Range" 
          placeholder="Sheet1!A1:Z" 
          value={config.range} 
          onChange={(e) => updateConfig('range', e.target.value)} 
          required 
        />
        <JsonInput
          label="Credentials (JSON)"
          placeholder='{ "type": "service_account", ... }'
          value={config.credentials}
          onChange={(val) => updateConfig('credentials', val)}
          minRows={10}
          formatOnBlur
          required
        />
      </Stack>
    );
  }

  if (sourceType === 'googleanalytics') {
    return (
      <Stack gap="md">
        <TextInput 
          label="Property ID" 
          placeholder="123456789" 
          value={config.property_id} 
          onChange={(e) => updateConfig('property_id', e.target.value)} 
          required 
          description="Google Analytics 4 Property ID"
        />
        <JsonInput
          label="Credentials JSON"
          placeholder='{ "type": "service_account", ... }'
          value={config.credentials_json}
          onChange={(val) => updateConfig('credentials_json', val)}
          required
          minRows={5}
          formatOnBlur
        />
        <TextInput 
          label="Metrics" 
          placeholder="activeUsers,sessions" 
          value={config.metrics} 
          onChange={(e) => updateConfig('metrics', e.target.value)} 
          required 
          description="Comma-separated list of metrics"
        />
        <TextInput 
          label="Dimensions" 
          placeholder="city,pagePath" 
          value={config.dimensions} 
          onChange={(e) => updateConfig('dimensions', e.target.value)} 
          required 
          description="Comma-separated list of dimensions"
        />
        <TextInput 
          label="Poll Interval" 
          placeholder="1h" 
          value={config.poll_interval} 
          onChange={(e) => updateConfig('poll_interval', e.target.value)} 
        />
      </Stack>
    );
  }

  if (sourceType === 'firebase') {
    return (
      <Stack gap="md">
        <TextInput 
          label="Project ID" 
          placeholder="my-project-id" 
          value={config.project_id} 
          onChange={(e) => updateConfig('project_id', e.target.value)} 
          required 
        />
        <TextInput 
          label="Collection" 
          placeholder="users" 
          value={config.collection} 
          onChange={(e) => updateConfig('collection', e.target.value)} 
          required 
        />
        <JsonInput
          label="Credentials JSON"
          placeholder='{ "type": "service_account", ... }'
          value={config.credentials_json}
          onChange={(val) => updateConfig('credentials_json', val)}
          required
          minRows={5}
          formatOnBlur
        />
        <TextInput 
          label="Timestamp Field" 
          placeholder="updated_at" 
          value={config.timestamp_field} 
          onChange={(e) => updateConfig('timestamp_field', e.target.value)} 
          description="Field used to track last processed document"
        />
        <TextInput 
          label="Poll Interval" 
          placeholder="1m" 
          value={config.poll_interval} 
          onChange={(e) => updateConfig('poll_interval', e.target.value)} 
        />
      </Stack>
    );
  }

  if (sourceType === 'http') {
    return (
      <Stack gap="md">
        <TextInput label="URL" placeholder="https://api.example.com/data" value={config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
        <Select 
          label="Method" 
          data={['GET', 'POST']} 
          value={config.method || 'GET'} 
          onChange={(val) => updateConfig('method', val || 'GET')} 
        />
        <TextInput 
          label="Poll Interval" 
          placeholder="1m" 
          value={config.poll_interval} 
          onChange={(e) => updateConfig('poll_interval', e.target.value)} 
          description="How often to poll the endpoint (e.g. 1m, 1h, 30s)"
        />
        <TextInput 
          label="Headers (Comma separated)" 
          placeholder="Authorization: Bearer token, Content-Type: application/json" 
          value={config.headers} 
          onChange={(e) => updateConfig('headers', e.target.value)} 
        />
        <TextInput 
          label="Data Path (GJSON)" 
          placeholder="data.items" 
          value={config.data_path} 
          onChange={(e) => updateConfig('data_path', e.target.value)} 
          description="Path to the data in the JSON response using GJSON syntax."
        />
      </Stack>
    );
  }

  if (sourceType === 'graphql') {
    return (
      <Stack gap="md">
        <TextInput label="Endpoint URL" placeholder="https://api.example.com/graphql" value={config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
        <JsonInput
          label="Query"
          placeholder="query { ... }"
          value={config.query}
          onChange={(val) => updateConfig('query', val)}
          minRows={8}
          required
        />
        <TextInput label="Poll Interval" placeholder="1m" value={config.poll_interval} onChange={(e) => updateConfig('poll_interval', e.target.value)} />
      </Stack>
    );
  }

  if (sourceType === 'websocket') {
    return (
      <Stack gap="md">
        <TextInput 
          label="WebSocket URL" 
          placeholder="wss://example.com/stream" 
          value={config.url} 
          onChange={(e) => updateConfig('url', e.target.value)} 
          required 
        />
        <TextInput 
          label="Headers (Comma separated)" 
          placeholder="Authorization: Bearer token, X-Custom: value" 
          value={config.headers} 
          onChange={(e) => updateConfig('headers', e.target.value)} 
        />
        <TextInput 
          label="Subprotocols" 
          placeholder="proto1, proto2" 
          value={config.subprotocols} 
          onChange={(e) => updateConfig('subprotocols', e.target.value)} 
        />
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Connect Timeout" placeholder="10s" value={config.connect_timeout} onChange={(e) => updateConfig('connect_timeout', e.target.value)} />
          <TextInput label="Read Timeout" placeholder="10s" value={config.read_timeout} onChange={(e) => updateConfig('read_timeout', e.target.value)} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Heartbeat Interval" placeholder="30s" value={config.heartbeat_interval} onChange={(e) => updateConfig('heartbeat_interval', e.target.value)} />
          <TextInput label="Reconnect Base" placeholder="1s" value={config.reconnect_base} onChange={(e) => updateConfig('reconnect_base', e.target.value)} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <TextInput label="Reconnect Max" placeholder="30s" value={config.reconnect_max} onChange={(e) => updateConfig('reconnect_max', e.target.value)} />
          <TextInput label="Max Message Bytes" placeholder="1048576" value={config.max_message_bytes} onChange={(e) => updateConfig('max_message_bytes', e.target.value)} />
        </SimpleGrid>
      </Stack>
    );
  }

  return null;
}


