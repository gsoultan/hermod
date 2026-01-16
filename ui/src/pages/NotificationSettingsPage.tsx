import { Title, Stack, Paper, TextInput, Button, Group, NumberInput, Checkbox, Text } from '@mantine/core'
import { useState, useEffect } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { apiFetch } from '../api'
import { notifications } from '@mantine/notifications'

export function NotificationSettingsPage() {
  const [settings, setSettings] = useState({
    smtp_host: '',
    smtp_port: 587,
    smtp_user: '',
    smtp_password: '',
    smtp_from: '',
    smtp_ssl: false,
    default_email: '',
    telegram_token: '',
    telegram_chat_id: '',
    slack_webhook: '',
    discord_webhook: '',
    webhook_url: ''
  })

  const { data, isLoading } = useQuery({
    queryKey: ['settings'],
    queryFn: async () => {
      const res = await apiFetch('/api/settings')
      if (!res.ok) throw new Error('Failed to fetch settings')
      return res.json()
    }
  })

  useEffect(() => {
    if (data) {
      setSettings(prev => ({ ...prev, ...data }))
    }
  }, [data])

  const saveMutation = useMutation({
    mutationFn: async (newSettings: typeof settings) => {
      const res = await apiFetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newSettings)
      })
      if (!res.ok) throw new Error('Failed to save settings')
    },
    onSuccess: () => {
      notifications.show({
        title: 'Settings Saved',
        message: 'Notification settings have been updated.',
        color: 'green'
      })
    },
    onError: (err) => {
      notifications.show({
        title: 'Error',
        message: err instanceof Error ? err.message : 'Failed to save settings',
        color: 'red'
      })
    }
  })

  const handleSave = () => {
    saveMutation.mutate(settings)
  }

  if (isLoading) return <Text>Loading...</Text>

  return (
    <Stack>
      <Title order={2}>Notification Settings</Title>
      
      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">SMTP Configuration</Title>
        <Stack gap="md">
          <Group grow>
            <TextInput
              label="SMTP Host"
              placeholder="smtp.example.com"
              value={settings.smtp_host}
              onChange={(e) => setSettings({ ...settings, smtp_host: e.target.value })}
            />
            <NumberInput
              label="SMTP Port"
              placeholder="587"
              value={settings.smtp_port}
              onChange={(val) => setSettings({ ...settings, smtp_port: Number(val) })}
            />
          </Group>

          <Group grow>
            <TextInput
              label="SMTP User"
              placeholder="user@example.com"
              value={settings.smtp_user}
              onChange={(e) => setSettings({ ...settings, smtp_user: e.target.value })}
            />
            <TextInput
              label="SMTP Password"
              type="password"
              placeholder="********"
              value={settings.smtp_password}
              onChange={(e) => setSettings({ ...settings, smtp_password: e.target.value })}
            />
          </Group>

          <Group grow>
            <TextInput
              label="From Email"
              placeholder="hermod@example.com"
              value={settings.smtp_from}
              onChange={(e) => setSettings({ ...settings, smtp_from: e.target.value })}
            />
            <TextInput
              label="Default Recipient Email"
              placeholder="admin@example.com"
              value={settings.default_email}
              onChange={(e) => setSettings({ ...settings, default_email: e.target.value })}
            />
          </Group>

          <Checkbox
            label="Use SSL/TLS"
            checked={settings.smtp_ssl}
            onChange={(e) => setSettings({ ...settings, smtp_ssl: e.currentTarget.checked })}
          />
        </Stack>
      </Paper>

      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Telegram Configuration</Title>
        <Stack gap="md">
          <TextInput
            label="Bot Token"
            placeholder="123456789:ABCDEF..."
            type="password"
            value={settings.telegram_token}
            onChange={(e) => setSettings({ ...settings, telegram_token: e.target.value })}
          />
          <TextInput
            label="Default Chat ID"
            placeholder="-100123456789"
            value={settings.telegram_chat_id}
            onChange={(e) => setSettings({ ...settings, telegram_chat_id: e.target.value })}
          />
        </Stack>
      </Paper>

      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Slack Configuration</Title>
        <Stack gap="md">
          <TextInput
            label="Webhook URL"
            placeholder="https://hooks.slack.com/services/..."
            value={settings.slack_webhook}
            onChange={(e) => setSettings({ ...settings, slack_webhook: e.target.value })}
          />
        </Stack>
      </Paper>

      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Discord Configuration</Title>
        <Stack gap="md">
          <TextInput
            label="Webhook URL"
            placeholder="https://discord.com/api/webhooks/..."
            value={settings.discord_webhook}
            onChange={(e) => setSettings({ ...settings, discord_webhook: e.target.value })}
          />
        </Stack>
      </Paper>

      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Generic Webhook</Title>
        <Stack gap="md">
          <TextInput
            label="Webhook URL"
            placeholder="https://api.example.com/notifications"
            value={settings.webhook_url}
            onChange={(e) => setSettings({ ...settings, webhook_url: e.target.value })}
          />
          <Text size="xs" c="dimmed">Hermod will send a POST request with JSON payload to this URL when a connection status changes.</Text>
        </Stack>
      </Paper>

      <Group justify="flex-end">
        <Button onClick={handleSave} loading={saveMutation.isPending}>
          Save Settings
        </Button>
      </Group>
    </Stack>
  )
}
