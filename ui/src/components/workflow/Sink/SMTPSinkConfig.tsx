import { useState } from 'react';
import { FormRow } from '@/components/common/FormRow';
import { TextInput, Group, Select, Checkbox, Textarea, Button, Stack, Tabs, ActionIcon, Tooltip, Divider, Text, Code, List, Modal, Alert } from '@mantine/core';
import { apiFetch } from '@/api';

import { EmailLayoutBuilder } from '../../forms/EmailLayoutBuilder';
import { IconAt, IconBrush, IconCloud, IconLink, IconPlayerPlay, IconTemplate, IconArrowsJoin, IconRefresh, IconShieldLock } from '@tabler/icons-react';
interface SMTPSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: string) => void;
  validateEmailLoading?: boolean;
  handleValidateEmail?: (email: string) => void;
  /**
   * The row the editor says reaches this sink — the same sample its field
   * picker is built from. The preview renders against it, rather than against
   * an example of a row nobody has.
   */
  incomingPayload?: any;
}

/** What POST /api/sinks/smtp/preview answers with. */
interface SmtpPreview {
  subject: string;
  from: string;
  to: string[];
  body: string;
  html: boolean;
  sample: Record<string, unknown>;
}

/** A row the preview can render against: an object, not a list or a scalar. */
function asSampleRow(value: any): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  return JSON.stringify(value, null, 2);
}

export function SMTPSinkConfig({ 
  config, updateConfig, validateEmailLoading, handleValidateEmail, incomingPayload 
}: SMTPSinkConfigProps) {
  const [builderOpened, setBuilderOpened] = useState(false);
  const [previewOpened, setPreviewOpened] = useState(false);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [preview, setPreview] = useState<SmtpPreview | null>(null);
  // The row the template renders against. Empty means "whatever the server
  // uses as an example", and the first response fills it in so it can be edited
  // into the operator's own row.
  const [sampleText, setSampleText] = useState('');

  // Seeded when the preview opens rather than in useState: the editor's sample
  // often arrives after this form has mounted.
  const openPreview = () => {
    const row = sampleText.trim() ? sampleText : asSampleRow(incomingPayload);
    if (row !== sampleText) setSampleText(row);
    setPreviewOpened(true);
    void runPreview(row);
  };

  const runPreview = async (sample: string) => {
    let row: unknown;
    if (sample.trim()) {
      try {
        row = JSON.parse(sample);
      } catch {
        setPreview(null);
        setPreviewError('The sample row is not valid JSON.');
        return;
      }
    }
    setPreviewBusy(true);
    setPreviewError(null);
    try {
      // silent: the failure belongs in the modal next to the template that
      // caused it, not in a toast that outlives the question.
      const res = await apiFetch('/api/sinks/smtp/preview', {
        method: 'POST',
        silent: true,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'smtp', config, sample: row }),
      });
      const data: SmtpPreview = await res.json();
      setPreview(data);
      if (!sample.trim() && data.sample) {
        setSampleText(JSON.stringify(data.sample, null, 2));
      }
    } catch (err: any) {
      setPreview(null);
      setPreviewError(err?.data?.error || err?.message || 'The preview failed.');
    } finally {
      setPreviewBusy(false);
    }
  };

  return (
    <>
      <EmailLayoutBuilder 
        opened={builderOpened} 
        onClose={() => setBuilderOpened(false)} 
        onApply={(html) => updateConfig('template', html)}
        outlookCompatible={config.outlook_compatible === 'true'}
      />
      <Modal
        opened={previewOpened}
        onClose={() => setPreviewOpened(false)}
        title="Template preview"
        size="xl"
      >
        <Stack gap="sm">
          <Textarea
            label="Sample row"
            description="The message the template is rendered against. Edit it to see your own row."
            value={sampleText}
            onChange={(e) => setSampleText(e.target.value)}
            autosize
            minRows={4}
            maxRows={10}
            styles={{ input: { fontFamily: 'monospace', fontSize: 'var(--mantine-font-size-xs)' } }}
          />
          <Group justify="flex-end">
            <Button size="xs" variant="light" loading={previewBusy} onClick={() => void runPreview(sampleText)}>
              Render
            </Button>
          </Group>
          {previewError && (
            <Alert color="red" title="The template did not render">{previewError}</Alert>
          )}
          {preview && !previewError && (
            <Stack gap={6}>
              <Text size="sm"><Text span fw={600}>Subject: </Text>{preview.subject}</Text>
              <Text size="sm"><Text span fw={600}>To: </Text>{(preview.to || []).join(', ')}</Text>
              {preview.html ? (
                <iframe
                  title="Rendered email"
                  srcDoc={preview.body}
                  style={{ width: '100%', height: 360, border: '1px solid var(--mantine-color-gray-3)', borderRadius: 8, background: '#fff' }}
                />
              ) : (
                <Code block>{preview.body}</Code>
              )}
            </Stack>
          )}
        </Stack>
      </Modal>
      <FormRow>
        <TextInput 
          label="Host" 
          placeholder="smtp.example.com" 
          value={config.host || ''} 
          onChange={(e) => updateConfig('host', e.target.value)} 
          required 
          description="SMTP server host"
          mih={80}
        />
        <TextInput 
          label="Port" 
          placeholder="587" 
          value={config.port || ''} 
          onChange={(e) => updateConfig('port', e.target.value)} 
          required 
          description="SMTP server port"
          mih={80}
        />
      </FormRow>
      <FormRow>
        <TextInput 
          label="Username" 
          placeholder="user@example.com" 
          value={config.username || ''} 
          onChange={(e) => updateConfig('username', e.target.value)} 
          required 
          description="Login username"
          mih={80}
        />
        <TextInput 
          label="Password" 
          type="password" 
          placeholder="password" 
          value={config.password || ''} 
          onChange={(e) => updateConfig('password', e.target.value)} 
          required 
          description="Login password"
          mih={80}
        />
      </FormRow>
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
        description="Comma-separated list of recipients. Supports {{.field}} variables and the date helpers below."
      />
      <TextInput 
        label="Subject" 
        placeholder="CDC Alert for {{.table}}" 
        value={config.subject || ''} 
        onChange={(e) => updateConfig('subject', e.target.value)} 
        required 
        description="Supports {{.field}} variables and the date helpers below."
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
                <FormRow>
                  <TextInput 
                    label="Max Idle" 
                    placeholder="2" 
                    value={config.pool_max_idle || ''} 
                    onChange={(e) => updateConfig('pool_max_idle', e.target.value)} 
                    description="Maximum idle connections" 
                    mih={80}
                  />
                  <TextInput 
                    label="Max Open" 
                    placeholder="0" 
                    value={config.pool_max_open || ''} 
                    onChange={(e) => updateConfig('pool_max_open', e.target.value)} 
                    description="Maximum total connections" 
                    mih={80}
                  />
                </FormRow>
                <TextInput 
                  label="Idle Timeout" 
                  placeholder="5m" 
                  value={config.pool_idle_timeout || ''} 
                  onChange={(e) => updateConfig('pool_idle_timeout', e.target.value)} 
                  description="Timeout for idle connections" 
                />
              </>
            )}
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="retry">
           <Stack gap="xs">
              <FormRow>
                <TextInput label="Max Retries" placeholder="3" value={config.retry_max || ''} onChange={(e) => updateConfig('retry_max', e.target.value)} />
                <TextInput label="Multiplier" placeholder="2.0" value={config.retry_multiplier || ''} onChange={(e) => updateConfig('retry_multiplier', e.target.value)} />
              </FormRow>
              <FormRow>
                <TextInput label="Initial Interval" placeholder="1s" value={config.retry_initial_interval || ''} onChange={(e) => updateConfig('retry_initial_interval', e.target.value)} />
                <TextInput label="Max Interval" placeholder="30s" value={config.retry_max_interval || ''} onChange={(e) => updateConfig('retry_max_interval', e.target.value)} />
              </FormRow>
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
            <TemplateDateHelp />
            <Button 
              variant="light" 
              leftSection={<IconPlayerPlay size="1rem" />} 
              onClick={openPreview} 
              loading={previewBusy}
            >
              Preview Template
            </Button>
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
            <FormRow>
              <TextInput label="S3 Region" placeholder="us-east-1" value={config.template_s3_region || ''} onChange={(e) => updateConfig('template_s3_region', e.target.value)} />
              <TextInput label="S3 Bucket" placeholder="my-templates" value={config.template_s3_bucket || ''} onChange={(e) => updateConfig('template_s3_bucket', e.target.value)} />
            </FormRow>
            <TextInput label="S3 Key" placeholder="path/to/email.html" value={config.template_s3_key || ''} onChange={(e) => updateConfig('template_s3_key', e.target.value)} />
            <TextInput
              label="Endpoint"
              placeholder="https://minio.example.com"
              value={config.template_s3_endpoint || ''}
              onChange={(e) => updateConfig('template_s3_endpoint', e.target.value)}
              description="Only for S3-compatible storage. Leave empty for Amazon S3."
            />
            <FormRow>
              <TextInput label="Access Key" value={config.template_s3_access_key || ''} onChange={(e) => updateConfig('template_s3_access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" value={config.template_s3_secret_key || ''} onChange={(e) => updateConfig('template_s3_secret_key', e.target.value)} />
            </FormRow>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </>
  );
}


/**
 * TemplateDateHelp documents the date and time helpers a template is rendered
 * with, next to the box they are typed into. Go's reference layout is the part
 * nobody guesses right, so it is spelled out rather than linked.
 */
function TemplateDateHelp() {
  return (
    <Stack gap={4} mt="xs">
      <Text size="xs" fw={500} c="dimmed">Dates and time zones</Text>
      <List size="xs" c="dimmed" spacing={2} withPadding>
        <List.Item>
          <Code>{'{{.created_at.Format "2006-01-02"}}'}</Code> — format a date or timestamp column.
          The layout is a date written out: <Code>2006</Code> year, <Code>01</Code> month,
          <Code>02</Code> day, <Code>15:04:05</Code> time.
        </List.Item>
        <List.Item>
          <Code>{'{{.start_at.In "Asia/Jakarta"}}'}</Code> — move a timestamp to another zone.
          <Code>{'{{.start_at.In (time.LoadLocation "Asia/Jakarta")}}'}</Code> does the same.
          Both read a column whether it arrives as text or as a database timestamp.
        </List.Item>
        <List.Item>
          <Code>{'{{date "02 Jan 2006" .created_at}}'}</Code> and{' '}
          <Code>{'{{dateInZone "2006-01-02 15:04" "Asia/Jakarta" .created_at}}'}</Code> — the same
          as functions, which also read epoch numbers. <Code>{'{{now.Format "15:04"}}'}</Code> is
          the send time.
        </List.Item>
      </List>
      <Text size="xs" c="dimmed">
        Column methods work in every template. The <Code>time</Code>, <Code>now</Code>,{' '}
        <Code>date</Code> and <Code>dateInZone</Code> functions are available in Subject, To, the
        idempotency key and this inline template — a template fetched from URL or S3 is rendered
        without them.
      </Text>
    </Stack>
  );
}
