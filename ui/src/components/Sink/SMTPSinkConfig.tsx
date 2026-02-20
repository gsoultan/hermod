import { useState } from 'react';
import { TextInput, Group, Select, Checkbox, Textarea, Button, Stack, Tabs, ActionIcon, Tooltip, Divider, Text } from '@mantine/core';

import { EmailLayoutBuilder } from '../EmailLayoutBuilder';
import { IconAt, IconBrush, IconCloud, IconLink, IconPlayerPlay, IconTemplate, IconArrowsJoin, IconRefresh, IconShieldLock } from '@tabler/icons-react';
interface SMTPSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: string) => void;
  validateEmailLoading?: boolean;
  handleValidateEmail?: (email: string) => void;
  handlePreview?: () => void;
  previewLoading?: boolean;
}

export function SMTPSinkConfig({ 
  config, updateConfig, validateEmailLoading, handleValidateEmail, handlePreview, previewLoading 
}: SMTPSinkConfigProps) {
  const [builderOpened, setBuilderOpened] = useState(false);

  return (
    <>
      <EmailLayoutBuilder 
        opened={builderOpened} 
        onClose={() => setBuilderOpened(false)} 
        onApply={(html) => updateConfig('template', html)}
        outlookCompatible={config.outlook_compatible === 'true'}
      />
      <Group grow>
        <TextInput label="Host" placeholder="smtp.example.com" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
        <TextInput label="Port" placeholder="587" value={config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} required />
      </Group>
      <Group grow>
        <TextInput label="Username" placeholder="user@example.com" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} required />
        <TextInput label="Password" type="password" placeholder="password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} required />
      </Group>
      <Select 
          label="SSL" 
          placeholder="Select SSL" 
          data={[{ value: 'true', label: 'True' }, { value: 'false', label: 'False' }]} 
          value={config.ssl || 'false'} 
          onChange={(value) => updateConfig('ssl', value || 'false')} 
          required 
      />
      <TextInput 
        label="From" 
        placeholder="sender@example.com" 
        value={config.from || ''} 
        onChange={(e) => updateConfig('from', e.target.value)} 
        required 
        rightSection={
          handleValidateEmail && (
            <Tooltip label="Validate email address">
              <ActionIcon aria-label="Validate email address" onClick={() => handleValidateEmail(config.from)} loading={validateEmailLoading} variant="subtle" color="blue">
                <IconAt size="1rem" />
              </ActionIcon>
            </Tooltip>
          )
        }
      />
      <TextInput 
        label="To" 
        placeholder="recipient1@example.com, {{.email_field}}" 
        value={config.to || ''} 
        onChange={(e) => updateConfig('to', e.target.value)} 
        required 
        description="Comma-separated list of recipients. Supports {{.field}} variables."
      />
      <TextInput 
        label="Subject" 
        placeholder="CDC Alert for {{.table}}" 
        value={config.subject || ''} 
        onChange={(e) => updateConfig('subject', e.target.value)} 
        required 
        description="Supports {{.field}} variables."
      />
      
      <Checkbox 
        label="Outlook Compatible" 
        description="Optimize HTML for Microsoft Outlook and other legacy email clients."
        checked={config.outlook_compatible === 'true'} 
        onChange={(e) => updateConfig('outlook_compatible', e.target.checked ? 'true' : 'false')}
        my="sm"
      />

      <Divider label="Advanced & Reliability" labelPosition="center" my="md" />
      
      <Tabs defaultValue="pool" styles={{ panel: { paddingTop: '1rem' } }}>
        <Tabs.List grow>
          <Tabs.Tab value="pool" leftSection={<IconArrowsJoin size="1rem" />}>SMTP Pool</Tabs.Tab>
          <Tabs.Tab value="retry" leftSection={<IconRefresh size="1rem" />}>Retry Strategy</Tabs.Tab>
          <Tabs.Tab value="security" leftSection={<IconShieldLock size="1rem" />}>Security</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="pool">
          <Stack gap="xs">
            <Checkbox 
              label="Enable Connection Pooling" 
              description="Maintain a pool of idle connections to the SMTP server for better performance."
              checked={config.enable_pool === 'true'} 
              onChange={(e) => updateConfig('enable_pool', e.target.checked ? 'true' : 'false')}
            />
            {config.enable_pool === 'true' && (
              <>
                <Group grow>
                  <TextInput label="Max Idle" placeholder="2" value={config.pool_max_idle || ''} onChange={(e) => updateConfig('pool_max_idle', e.target.value)} description="Max idle connections." />
                  <TextInput label="Max Open" placeholder="0" value={config.pool_max_open || ''} onChange={(e) => updateConfig('pool_max_open', e.target.value)} description="Max total connections (0=unlimited)." />
                </Group>
                <TextInput label="Idle Timeout" placeholder="5m" value={config.pool_idle_timeout || ''} onChange={(e) => updateConfig('pool_idle_timeout', e.target.value)} description="Duration after which idle connections are closed (e.g. 1m, 5m)." />
              </>
            )}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="retry">
           <Stack gap="xs">
              <Group grow>
                <TextInput label="Max Retries" placeholder="3" value={config.retry_max || ''} onChange={(e) => updateConfig('retry_max', e.target.value)} />
                <TextInput label="Multiplier" placeholder="2.0" value={config.retry_multiplier || ''} onChange={(e) => updateConfig('retry_multiplier', e.target.value)} />
              </Group>
              <Group grow>
                <TextInput label="Initial Interval" placeholder="1s" value={config.retry_initial_interval || ''} onChange={(e) => updateConfig('retry_initial_interval', e.target.value)} />
                <TextInput label="Max Interval" placeholder="30s" value={config.retry_max_interval || ''} onChange={(e) => updateConfig('retry_max_interval', e.target.value)} />
              </Group>
           </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="security">
           <Checkbox 
              label="Insecure Skip Verify" 
              description="Skip TLS certificate verification. NOT RECOMMENDED for production unless using self-signed certificates in a trusted network."
              checked={config.insecure_skip_verify === 'true'} 
              onChange={(e) => updateConfig('insecure_skip_verify', e.target.checked ? 'true' : 'false')}
            />
        </Tabs.Panel>
      </Tabs>

      <Divider label="Idempotency (Duplicate Protection)" labelPosition="center" my="md" />

      <Checkbox
        label="Enable Idempotency"
        description="Prevent duplicate emails by using a stable key per message."
        checked={config.enable_idempotency === 'true'}
        onChange={(e) => updateConfig('enable_idempotency', e.target.checked ? 'true' : 'false')}
        my="xs"
      />
      <TextInput
        label="Idempotency Key Template"
        placeholder="e.g. {{.id}}-{{.table}} or {{.metadata.guid}}"
        value={config.idempotency_key_template || ''}
        onChange={(e) => updateConfig('idempotency_key_template', e.target.value)}
        description="Use Go template variables from your message. Leave empty to derive from message content."
      />
      <TextInput
        label="Retention Window (days)"
        placeholder="e.g. 14"
        value={config.idempotency_retention_days || ''}
        onChange={(e) => updateConfig('idempotency_retention_days', e.target.value)}
        description="How long to keep idempotency records for duplicate protection."
      />

      <Divider label="Template Settings" labelPosition="center" my="md" />

      <Tabs defaultValue={config.template_source || 'inline'} onChange={(value) => updateConfig('template_source', value || 'inline')}>
        <Tabs.List grow>
          <Tabs.Tab value="inline" leftSection={<IconTemplate size="1rem" />}>Inline</Tabs.Tab>
          <Tabs.Tab value="url" leftSection={<IconLink size="1rem" />}>URL</Tabs.Tab>
          <Tabs.Tab value="s3" leftSection={<IconCloud size="1rem" />}>Amazon S3</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="inline" pt="md">
          <Stack gap="xs">
            <Group justify="space-between" align="center">
              <Text size="sm" fw={500}>Template Content</Text>
              <Button 
                variant="subtle" 
                size="compact-xs" 
                leftSection={<IconBrush size="0.8rem" />}
                onClick={() => setBuilderOpened(true)}
              >
                Launch Layout Builder
              </Button>
            </Group>
            <Textarea 
              placeholder="Hello {{.name}}, your order #{{.order_id}} has been processed." 
              value={config.template || ''} 
              onChange={(e) => updateConfig('template', e.target.value)} 
              autosize
              minRows={18}
              styles={{
                input: {
                  fontFamily: 'monospace',
                  fontSize: 'var(--mantine-font-size-sm)',
                }
              }}
              description="Supports Go template syntax. You can use standard Go template functions and range over arrays (e.g., {{range .items}})."
            />
            {handlePreview && (
              <Button 
                variant="light" 
                leftSection={<IconPlayerPlay size="1rem" />} 
                onClick={handlePreview} 
                loading={previewLoading}
              >
                Preview Template
              </Button>
            )}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="url" pt="md">
          <TextInput 
            label="Template URL" 
            placeholder="https://example.com/template.html" 
            value={config.template_url || ''} 
            onChange={(e) => updateConfig('template_url', e.target.value)} 
            description="Hermod will fetch this URL for every message. Ensure it's reachable from the worker."
          />
        </Tabs.Panel>

        <Tabs.Panel value="s3" pt="md">
          <Stack gap="xs">
            <Group grow>
              <TextInput label="S3 Region" placeholder="us-east-1" value={config.template_s3_region || ''} onChange={(e) => updateConfig('template_s3_region', e.target.value)} />
              <TextInput label="S3 Bucket" placeholder="my-templates" value={config.template_s3_bucket || ''} onChange={(e) => updateConfig('template_s3_bucket', e.target.value)} />
            </Group>
            <TextInput label="S3 Key" placeholder="path/to/email.html" value={config.template_s3_key || ''} onChange={(e) => updateConfig('template_s3_key', e.target.value)} />
            <Group grow>
              <TextInput label="Access Key" value={config.template_s3_access_key || ''} onChange={(e) => updateConfig('template_s3_access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" value={config.template_s3_secret_key || ''} onChange={(e) => updateConfig('template_s3_secret_key', e.target.value)} />
            </Group>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </>
  );
}


