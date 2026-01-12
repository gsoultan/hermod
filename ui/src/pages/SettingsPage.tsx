import { Title, Text, Stack, Paper, Select, TextInput, Button, Group } from '@mantine/core'
import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { apiFetch } from '../api'

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
    </Stack>
  )
}
