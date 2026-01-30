import { TextInput, Group, Select, Textarea, Button, Stack, Switch } from '@mantine/core';
import { IconAt } from '@tabler/icons-react';

interface NotificationSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: string) => void;
  validateEmailLoading?: boolean;
  validateEmail?: (email: string) => void;
  handlePreview?: () => void;
  previewLoading?: boolean;
}

export function NotificationSinkConfig({ 
  type, config, updateConfig, validateEmailLoading, validateEmail, handlePreview, previewLoading 
}: NotificationSinkConfigProps) {
  switch (type) {
    case 'fcm':
      return (
        <>
          <TextInput label="Server Key" placeholder="FCM Server Key" value={config.server_key || ''} onChange={(e) => updateConfig('server_key', e.target.value)} required />
          <TextInput label="Device Token" placeholder="FCM Device Token" value={config.device_token || ''} onChange={(e) => updateConfig('device_token', e.target.value)} required />
        </>
      );
    case 'smtp':
      return (
        <>
          <Group grow>
            <TextInput label="SMTP Host" placeholder="smtp.gmail.com" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
            <TextInput label="SMTP Port" placeholder="587" value={config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} required />
          </Group>
          <Group grow>
            <TextInput label="Username" placeholder="user@gmail.com" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="App password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="From" placeholder="noreply@hermod.com" value={config.from || ''} onChange={(e) => updateConfig('from', e.target.value)} required />
            <TextInput 
              label="To (Dynamic Template)" 
              placeholder="{{.customer_email}}" 
              value={config.to || ''} 
              onChange={(e) => updateConfig('to', e.target.value)} 
              required 
              rightSection={
                validateEmail && (
                  <Button variant="subtle" size="xs" loading={validateEmailLoading} onClick={() => validateEmail(config.to)}>
                    <IconAt size="1rem" />
                  </Button>
                )
              }
            />
          </Group>
          <TextInput label="Subject Template" placeholder="New order: {{.id}}" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
          <Select 
            label="Template Source" 
            data={[
              { label: 'Inline Content', value: 'inline' },
              { label: 'External URL (GET)', value: 'url' },
              { label: 'Local File', value: 'file' }
            ]} 
            value={config.template_source || 'inline'} 
            onChange={(val) => updateConfig('template_source', val || 'inline')} 
          />
          {config.template_source === 'inline' && (
            <Stack gap={4}>
               <Textarea 
                label="Email Body Template (Go Template)" 
                placeholder="Hello {{.name}}, your order #{{.id}} has been received!" 
                minRows={6} 
                value={config.template || ''} 
                onChange={(e) => updateConfig('template', e.target.value)} 
                description="Supports Go template syntax: {{.field}}, {{range .items}} etc."
              />
              <Group justify="flex-end">
                <Button size="xs" variant="light" loading={previewLoading} onClick={handlePreview}>Preview Template</Button>
              </Group>
            </Stack>
          )}
          {config.template_source === 'url' && (
            <TextInput label="Template URL" placeholder="https://cdn.com/templates/order.html" value={config.template_url || ''} onChange={(e) => updateConfig('template_url', e.target.value)} />
          )}
          {config.template_source === 'file' && (
            <TextInput label="Template Path" placeholder="/etc/hermod/templates/email.html" value={config.template_path || ''} onChange={(e) => updateConfig('template_path', e.target.value)} />
          )}
          <Switch 
            label="Outlook Compatible (Inlined CSS)" 
            checked={config.outlook_compatible === 'true'} 
            onChange={(e) => updateConfig('outlook_compatible', e.currentTarget.checked ? 'true' : 'false')} 
          />
        </>
      );
    case 'telegram':
      return (
        <>
          <TextInput label="Bot Token" placeholder="123456:ABC-DEF..." value={config.bot_token || ''} onChange={(e) => updateConfig('bot_token', e.target.value)} required />
          <TextInput label="Chat ID" placeholder="-100123456789" value={config.chat_id || ''} onChange={(e) => updateConfig('chat_id', e.target.value)} required />
          <TextInput label="Template" placeholder="Message: {{.payload}}" value={config.template || ''} onChange={(e) => updateConfig('template', e.target.value)} />
        </>
      );
    default:
      return null;
  }
}
