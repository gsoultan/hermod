import { Button, Checkbox, Group, NumberInput, Paper, PasswordInput, SimpleGrid, Stack, TextInput, Title } from '@mantine/core';
import { IconBrandDiscord, IconBrandSlack, IconBrandTelegram, IconMail, IconWebhook } from '@tabler/icons-react';

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
