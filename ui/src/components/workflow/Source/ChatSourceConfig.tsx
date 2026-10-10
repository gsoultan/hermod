import { Code, Divider, Input, PasswordInput, SegmentedControl, SimpleGrid, Stack, Text, TextInput } from '@mantine/core';
import { GenerateToken } from '../../shared/GenerateToken';

interface ChatSourceConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
}

const PLATFORMS = [
  { label: 'Web', value: 'web' },
  { label: 'Slack', value: 'slack' },
  { label: 'Telegram', value: 'telegram' },
];

function hermodOrigin(): string {
  return typeof window !== 'undefined' ? window.location.origin : 'https://hermod.example.com';
}

/**
 * ChatSourceConfig is the chat trigger's source form: the endpoint a web
 * widget, a Slack app or a Telegram bot sends messages to, answered with the
 * workflow's reply.
 *
 * Each platform has its own credential, written under the key the endpoint
 * reads (internal/chat/transport/http). A chat source with no credential
 * answers nobody.
 */
export function ChatSourceConfig({ config, updateConfig }: ChatSourceConfigProps) {
  const platform = config.platform || 'web';
  const path = config.path || '/api/chat/my-chat';
  const endpoint = `${hermodOrigin()}${path}`;

  return (
    <Stack gap="md">
      <TextInput
        label="Chat path"
        placeholder="/api/chat/support"
        value={config.path || ''}
        onChange={(e) => updateConfig('path', e.target.value)}
        description="Where the chat receives messages. It must start with /api/chat/."
        required
      />
      <Input.Wrapper label="Platform" description="Where the messages come from.">
        <SegmentedControl
          mt={6}
          aria-label="Platform"
          value={platform}
          onChange={(value) => updateConfig('platform', value)}
          data={PLATFORMS}
        />
      </Input.Wrapper>

      {platform === 'web' && (
        <>
          <GenerateToken
            label="API key (server to server)"
            value={config.api_key || ''}
            onChange={(val) => updateConfig('api_key', val)}
          />
          <Text size="xs" c="dimmed">
            Your own server sends it as <Code>X-API-Key</Code>. Never put it in a web page.
          </Text>
          <GenerateToken
            label="Widget key (public)"
            value={config.widget_key || ''}
            onChange={(val) => updateConfig('widget_key', val)}
          />
          <TextInput
            label="Allowed origins"
            placeholder="https://www.example.com, https://help.example.com"
            value={config.allowed_origins || ''}
            onChange={(e) => updateConfig('allowed_origins', e.target.value)}
            description="The sites the widget key is accepted from, as scheme://host[:port], comma-separated. Without one, the widget key works nowhere."
          />
          <TextInput
            label="Rate limit"
            placeholder="120"
            value={config.rate_limit || ''}
            onChange={(e) => updateConfig('rate_limit', e.target.value)}
            description="Messages per caller per hour."
          />
          {config.widget_key ? (
            <Stack gap={4}>
              <Text size="sm" fw={500} id="chat-embed-snippet-label">Embed snippet</Text>
              <Code block aria-labelledby="chat-embed-snippet-label">
                {`<script src="${hermodOrigin()}/api/chat/widget.js" async\n        data-endpoint="${endpoint}"\n        data-widget-key="${config.widget_key}"\n        data-title="Chat"></script>`}
              </Code>
              <Text size="xs" c="dimmed">
                Paste it into your site. Under a Content-Security-Policy, allow this Hermod in <Code>script-src</Code> and <Code>connect-src</Code>.
              </Text>
            </Stack>
          ) : null}
        </>
      )}

      {platform === 'slack' && (
        <>
          <PasswordInput
            label="Signing secret"
            value={config.slack_signing_secret || ''}
            onChange={(e) => updateConfig('slack_signing_secret', e.target.value)}
            description="From your Slack app's Basic Information page. Every delivery is checked against it."
            required
          />
          <PasswordInput
            label="Bot token"
            placeholder="xoxb-..."
            value={config.slack_bot_token || ''}
            onChange={(e) => updateConfig('slack_bot_token', e.target.value)}
            description="Used to post the answer (chat:write scope). A secret:NAME reference is resolved from the vhost's secrets."
            required
          />
          <Text size="xs" c="dimmed">
            In Event Subscriptions, set the Request URL to <Code>{endpoint}</Code> and subscribe to <Code>message.im</Code> and{' '}
            <Code>app_mention</Code>. Answers go to the channel, in the thread when the message was in one.
          </Text>
        </>
      )}

      {platform === 'telegram' && (
        <>
          <PasswordInput
            label="Secret token"
            value={config.telegram_secret_token || ''}
            onChange={(e) => updateConfig('telegram_secret_token', e.target.value)}
            description="1 to 256 characters of A-Z, a-z, 0-9, _ and -. Telegram sends it with every update."
            required
          />
          <Text size="xs" c="dimmed">
            Register the webhook with <Code>{`setWebhook?url=${endpoint}&secret_token=<the secret token>`}</Code>. The answer is
            sent back in the webhook's response, so no bot token is needed here.
          </Text>
        </>
      )}

      <Divider my="xs" />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <TextInput
          label="Reply field"
          placeholder="ai_output"
          value={config.reply_field || ''}
          onChange={(e) => updateConfig('reply_field', e.target.value)}
          description="The record field the answer is read from. AI Prompt writes ai_output by default."
        />
        <TextInput
          label="Response timeout"
          placeholder="30s"
          value={config.response_timeout || ''}
          onChange={(e) => updateConfig('response_timeout', e.target.value)}
          description="How long to wait for the workflow. At most 5m."
        />
      </SimpleGrid>
    </Stack>
  );
}
