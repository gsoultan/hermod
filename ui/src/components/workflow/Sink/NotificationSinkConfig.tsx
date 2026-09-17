import { Alert, TextInput, Textarea } from '@mantine/core';

interface NotificationSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: string) => void;
}

/**
 * The form for the chat sinks: Telegram, Discord and Slack.
 *
 * It used to carry a second copy of the SMTP form as well, which nothing could
 * reach — SinkForm maps `smtp` to SMTPSinkConfig — and which had drifted from
 * the one that is reached. The three types below are what the sink picker sends
 * here.
 */
export function NotificationSinkConfig({ type, config, updateConfig }: NotificationSinkConfigProps) {
  switch (type) {
    case 'telegram':
      return (
        <>
          <TextInput
            label="Bot Token"
            placeholder="123456:ABC-DEF..."
            value={config.bot_token || ''}
            onChange={(e) => updateConfig('bot_token', e.target.value)}
            required
            description="From @BotFather."
          />
          <TextInput
            label="Chat ID"
            placeholder="-100123456789"
            value={config.chat_id || ''}
            onChange={(e) => updateConfig('chat_id', e.target.value)}
            required
            description="The chat, channel or group the bot posts to."
          />
          <Textarea
            label="Template"
            placeholder="Order {{.id}} on {{.table}}"
            value={config.template || ''}
            onChange={(e) => updateConfig('template', e.target.value)}
            description="Go template over the message. Leave empty to send the formatted message."
          />
        </>
      );
    case 'discord':
    case 'slack':
      return <WebhookOrBotForm type={type} config={config} updateConfig={updateConfig} />;
    default:
      // Silence is what hid Discord and Slack: both were mapped here, neither
      // had a branch, and the form rendered nothing at all.
      return (
        <Alert color="orange" title="No form for this sink type">
          {`"${type}" is routed to the chat sink form, which has no fields for it. Configure it through the API, or add its form here.`}
        </Alert>
      );
  }
}

/**
 * Discord and Slack take the same two shapes of credential: a webhook URL, or a
 * bot token with a channel. Either is enough, and the sink says exactly that
 * when it has neither, so the form asks for both and says which it needs.
 */
function WebhookOrBotForm({ type, config, updateConfig }: NotificationSinkConfigProps) {
  const service = type === 'discord' ? 'Discord' : 'Slack';
  const placeholder = type === 'discord'
    ? 'https://discord.com/api/webhooks/…'
    : 'https://hooks.slack.com/services/…';
  return (
    <>
      <Alert color="blue" variant="light">
        {`Either a webhook URL on its own, or a bot token with a channel id. ${service} refuses the message when it has neither.`}
      </Alert>
      <TextInput
        label="Webhook URL"
        placeholder={placeholder}
        value={config.webhook_url || ''}
        onChange={(e) => updateConfig('webhook_url', e.target.value)}
        description="The simplest route: no token, no channel id."
      />
      <TextInput
        label="Bot Token"
        placeholder={type === 'discord' ? 'Bot token' : 'xoxb-…'}
        value={config.token || ''}
        onChange={(e) => updateConfig('token', e.target.value)}
        description="Used with the channel id when there is no webhook URL."
      />
      <TextInput
        label="Channel ID"
        placeholder={type === 'discord' ? '123456789012345678' : 'C0123456789'}
        value={config.channel_id || ''}
        onChange={(e) => updateConfig('channel_id', e.target.value)}
        description="The channel the bot posts to."
      />
    </>
  );
}
