import { Button, Checkbox, Code, Group, NumberInput, Paper, PasswordInput, SimpleGrid, Stack, Text, TextInput, Title } from '@mantine/core';
import { IconBrandDiscord, IconBrandSlack, IconBrandTelegram, IconMail, IconMailForward, IconWebhook } from '@tabler/icons-react';

import type { SettingsController } from './useSettingsController';

/** The "connectivity" tab of Settings. State lives in useSettingsController. */
export function ConnectivityTab({ ctx }: { ctx: SettingsController }) {
  const {
    notifSettings,
    saveNotifMutation,
    setNotifSettings,
    testNotifMutation,
  } = ctx;

  return (
    <>
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconMail size="1.2rem" color="blue" />
                <Title order={4}>SMTP Configuration</Title>
              </Group>
              <Stack gap="md">
                <Group grow>
                  <TextInput
                    label="SMTP Host"
                    placeholder="smtp.example.com"
                    value={notifSettings.smtp_host}
                    onChange={(e) => setNotifSettings({ ...notifSettings, smtp_host: e.target.value })}
                  />
                  <NumberInput
                    label="SMTP Port"
                    placeholder="587"
                    value={notifSettings.smtp_port}
                    onChange={(val) => setNotifSettings({ ...notifSettings, smtp_port: Number(val) })}
                  />
                </Group>
                <Group grow>
                  <TextInput
                    label="SMTP User"
                    placeholder="user@example.com"
                    value={notifSettings.smtp_user}
                    onChange={(e) => setNotifSettings({ ...notifSettings, smtp_user: e.target.value })}
                  />
                  <PasswordInput
                    label="SMTP Password"
                    placeholder="********"
                    value={notifSettings.smtp_password}
                    onChange={(e) => setNotifSettings({ ...notifSettings, smtp_password: e.target.value })}
                  />
                </Group>
                <Group grow>
                  <TextInput
                    label="From Email"
                    placeholder="hermod@example.com"
                    value={notifSettings.smtp_from}
                    onChange={(e) => setNotifSettings({ ...notifSettings, smtp_from: e.target.value })}
                  />
                  <TextInput
                    label="Default Recipient"
                    placeholder="admin@example.com"
                    value={notifSettings.default_email}
                    onChange={(e) => setNotifSettings({ ...notifSettings, default_email: e.target.value })}
                  />
                </Group>
                <Checkbox
                  label="Use SSL/TLS"
                  checked={notifSettings.smtp_ssl}
                  onChange={(e) => setNotifSettings({ ...notifSettings, smtp_ssl: e.currentTarget.checked })}
                />
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="xs">
                <IconMailForward size="1.2rem" color="teal" />
                <Title order={4}>Panmail</Title>
              </Group>
              <Text size="sm" c="dimmed" mb="md">
                Sends alerts through a Panmail gateway. Any field can hold <Code>{'{{secret.NAME}}'}</Code> (a
                global secret) or <Code>{'{{env.NAME}}'}</Code> (the server&apos;s HERMOD_ENV_NAME).
              </Text>
              <Stack gap="md">
                <Group grow>
                  <TextInput
                    label="Gateway URL"
                    placeholder="https://mail.example.com"
                    value={notifSettings.panmail_url}
                    onChange={(e) => setNotifSettings({ ...notifSettings, panmail_url: e.target.value })}
                  />
                  <PasswordInput
                    label="API key"
                    placeholder="{{secret.PANMAIL_API_KEY}}"
                    value={notifSettings.panmail_api_key}
                    onChange={(e) => setNotifSettings({ ...notifSettings, panmail_api_key: e.target.value })}
                  />
                </Group>
                <Group grow>
                  <TextInput
                    label="Provider ID"
                    placeholder="The sending provider, from Panmail's Email Providers page"
                    value={notifSettings.panmail_provider_id}
                    onChange={(e) => setNotifSettings({ ...notifSettings, panmail_provider_id: e.target.value })}
                  />
                  <TextInput
                    label="From"
                    placeholder="alerts@example.com"
                    value={notifSettings.panmail_from}
                    onChange={(e) => setNotifSettings({ ...notifSettings, panmail_from: e.target.value })}
                  />
                </Group>
                <TextInput
                  label="To"
                  placeholder="ops@example.com, oncall@example.com"
                  description="Comma-separated. Leave empty to use the Default Recipient above."
                  value={notifSettings.panmail_to}
                  onChange={(e) => setNotifSettings({ ...notifSettings, panmail_to: e.target.value })}
                />
                <Checkbox
                  label="Mail every dead-lettered message"
                  description="One mail per workflow per minute, naming the workflow, the node or sink, the error and up to 10 of the messages, with a link to the dead-letter queue. The link needs the Base URL on the Platform tab."
                  checked={notifSettings.panmail_dead_letters}
                  onChange={(e) => setNotifSettings({ ...notifSettings, panmail_dead_letters: e.currentTarget.checked })}
                />
                {notifSettings.panmail_dead_letters && (
                  <Checkbox
                    ml="xl"
                    label="Leave message contents out"
                    description="Send the workflow, node and error only, for messages that hold personal or sensitive data."
                    checked={notifSettings.panmail_hide_payload}
                    onChange={(e) => setNotifSettings({ ...notifSettings, panmail_hide_payload: e.currentTarget.checked })}
                  />
                )}
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconBrandTelegram size="1.2rem" color="blue" />
                <Title order={4}>Telegram</Title>
              </Group>
              <Stack gap="md">
                <PasswordInput
                  label="Bot Token"
                  placeholder="123456789:ABCDEF..."
                  value={notifSettings.telegram_token}
                  onChange={(e) => setNotifSettings({ ...notifSettings, telegram_token: e.target.value })}
                />
                <TextInput
                  label="Default Chat ID"
                  placeholder="-100123456789"
                  value={notifSettings.telegram_chat_id}
                  onChange={(e) => setNotifSettings({ ...notifSettings, telegram_chat_id: e.target.value })}
                />
              </Stack>
            </Paper>

            <SimpleGrid cols={2}>
              <Paper withBorder p="md" radius="md">
                <Group gap="xs" mb="md">
                  <IconBrandSlack size="1.2rem" color="red" />
                  <Title order={4}>Slack</Title>
                </Group>
                <TextInput
                  label="Webhook URL"
                  placeholder="https://hooks.slack.com/..."
                  value={notifSettings.slack_webhook}
                  onChange={(e) => setNotifSettings({ ...notifSettings, slack_webhook: e.target.value })}
                />
              </Paper>
              <Paper withBorder p="md" radius="md">
                <Group gap="xs" mb="md">
                  <IconBrandDiscord size="1.2rem" color="indigo" />
                  <Title order={4}>Discord</Title>
                </Group>
                <TextInput
                  label="Webhook URL"
                  placeholder="https://discord.com/api/webhooks/..."
                  value={notifSettings.discord_webhook}
                  onChange={(e) => setNotifSettings({ ...notifSettings, discord_webhook: e.target.value })}
                />
              </Paper>
            </SimpleGrid>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconWebhook size="1.2rem" color="gray" />
                <Title order={4}>Generic Webhook</Title>
              </Group>
              <TextInput
                label="Webhook URL"
                placeholder="https://api.example.com/notifications"
                value={notifSettings.webhook_url}
                onChange={(e) => setNotifSettings({ ...notifSettings, webhook_url: e.target.value })}
                description="Hermod will send a POST request with JSON payload when workflow status changes."
              />
            </Paper>

            <Group justify="space-between">
              <Button variant="outline" size="sm" onClick={() => testNotifMutation.mutate()} loading={testNotifMutation.isPending}>
                Send Test Notification
              </Button>
              <Button size="sm" onClick={() => saveNotifMutation.mutate(notifSettings)} loading={saveNotifMutation.isPending}>
                Save Connectivity Settings
              </Button>
            </Group>
          </Stack>
    </>
  );
}
