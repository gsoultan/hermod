import { Title, Text, Stack, Paper, Select, TextInput, Button, Group } from '@mantine/core'
import { useState, useRef, useEffect } from 'react'
import { useMutation } from '@tanstack/react-query'
import { apiFetch } from '../api'
import { IconDownload, IconUpload } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'

export function SettingsPage() {
  const [dbType, setDbType] = useState<string | null>('sqlite')
  const [dbConn, setDbConn] = useState('')
  const [message, setMessage] = useState<{ type: 'success' | 'error', text: string } | null>(null)

  const saveMutation = useMutation({
    mutationFn: async (config: { type: string | null, conn: string }) => {
      const response = await apiFetch('/api/config/database', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(config),
      })

      if (!response.ok) {
        throw new Error('Failed to save configuration')
      }
      return response.json()
    },
    onSuccess: () => {
      setMessage({ type: 'success', text: 'Configuration saved. Please restart the application for changes to take effect.' })
    },
    onError: (err) => {
      setMessage({ type: 'error', text: err instanceof Error ? err.message : 'An error occurred' })
    }
  })

  const handleSave = () => {
    setMessage(null)
    saveMutation.mutate({ type: dbType, conn: dbConn })
  }

  const fileInputRef = useRef<HTMLInputElement>(null);

  // Prefill DB config from backend (admin-only endpoint)
  useEffect(() => {
    let aborted = false
    ;(async () => {
      try {
        const res = await apiFetch('/api/config/database')
        if (!res.ok) return
        const data = await res.json()
        if (aborted) return
        if (data.type) setDbType(data.type)
        if (typeof data.conn === 'string') setDbConn(data.conn)
      } catch (_) {
        // ignore
      }
    })()
    return () => { aborted = true }
  }, [])

  const handleExport = async () => {
    try {
      const response = await apiFetch('/api/backup/export');
      const blob = await response.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `hermod-config-${new Date().toISOString().split('T')[0]}.json`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
    } catch (err) {
      notifications.show({ title: 'Export Failed', message: 'Failed to download backup', color: 'red' });
    }
  };

  const handleImport = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = async (e) => {
      try {
        const content = e.target?.result as string;
        const response = await apiFetch('/api/backup/import', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: content
        });
        if (response.ok) {
          notifications.show({ title: 'Import Successful', message: 'Configuration has been restored.', color: 'green' });
        } else {
          throw new Error('Import failed');
        }
      } catch (err) {
        notifications.show({ title: 'Import Failed', message: 'Failed to upload or parse backup', color: 'red' });
      }
    };
    reader.readAsText(file);
    event.target.value = ''; // Reset input
  };

  return (
    <Stack>
      <Title order={2}>Settings</Title>
      
      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Database Configuration</Title>
        <Stack gap="md">
          <Select
            label="Database Type"
            placeholder="Select database type"
            data={[
              { value: 'sqlite', label: 'SQLite' },
              { value: 'postgres', label: 'PostgreSQL' },
              { value: 'mysql', label: 'MySQL' },
              { value: 'mariadb', label: 'MariaDB' },
              { value: 'mongodb', label: 'MongoDB' },
            ]}
            value={dbType}
            onChange={setDbType}
          />

          <TextInput
            label="Connection String"
            placeholder={dbType === 'sqlite' ? 'hermod.db' : 'postgres://user:pass@localhost:5432/db'}
            value={dbConn}
            onChange={(e) => setDbConn(e.currentTarget.value)}
          />

          {message && (
            <Text c={message.type === 'success' ? 'green' : 'red'} size="sm">
              {message.text}
            </Text>
          )}

          <Group justify="flex-end">
            <Button onClick={handleSave} loading={saveMutation.isPending}>
              Save Changes
            </Button>
          </Group>
        </Stack>
      </Paper>

      <Paper withBorder p="md" radius="md">
        <Title order={4} mb="md">Maintenance & Backup</Title>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            Export your entire configuration including Sources, Sinks, Workflows, and Transformations.
            You can then import this file to another Hermod instance.
          </Text>
          <Group>
            <Button variant="outline" leftSection={<IconDownload size="1rem" />} onClick={handleExport}>
              Export Configuration
            </Button>
            <Button variant="outline" color="orange" leftSection={<IconUpload size="1rem" />} onClick={() => fileInputRef.current?.click()}>
              Import Configuration
            </Button>
            <input
              type="file"
              ref={fileInputRef}
              style={{ display: 'none' }}
              accept=".json"
              onChange={handleImport}
            />
          </Group>
        </Stack>
      </Paper>
    </Stack>
  )
}
