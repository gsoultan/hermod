import { useState } from 'react'
import { Title, Text, TextInput, Select, Button, Paper, Stack, Container, Stepper, Group, PasswordInput } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { apiFetch } from '../api'

interface SetupPageProps {
  isConfigured: boolean
  isUserSetup: boolean
  onConfigured: () => void
}

export function SetupPage({ isConfigured, onConfigured }: SetupPageProps) {
  const [active, setActive] = useState(0)
  
  // Set initial step based on configuration status but allow going back
  useState(() => {
    if (isConfigured) {
      setActive(1)
    }
  })
  
  // Storage state
  const [dbType, setDbType] = useState<string | null>('sqlite')
  const [dbConn, setDbConn] = useState('hermod.db')
  
  // User state
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')

  const [error, setError] = useState<string | null>(null)

  const dbMutation = useMutation({
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
      setActive(1)
      setError(null)
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  const userMutation = useMutation({
    mutationFn: async (user: any) => {
      const response = await apiFetch('/api/users', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(user),
      })

      if (!response.ok) {
        throw new Error('Failed to create administrator')
      }
      return response.json()
    },
    onSuccess: () => {
      onConfigured()
      setError(null)
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  const handleDBSetup = () => {
    dbMutation.mutate({ type: dbType, conn: dbConn })
  }

  const handleUserSetup = () => {
    userMutation.mutate({
      username,
      password,
      full_name: fullName,
      email,
    })
  }

  return (
    <Container size="sm" mt={100}>
      <Stack gap="xl">
        <Stack gap="xs" align="center">
          <Title order={1}>Welcome to Hermod</Title>
          <Text c="dimmed">Complete the steps below to get started</Text>
        </Stack>

        <Stepper active={active} onStepClick={setActive} allowNextStepsSelect={false}>
          <Stepper.Step label="Database" description="Storage configuration">
            <Paper withBorder p="xl" radius="md" mt="xl">
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

                {error && (
                  <Text color="red" size="sm">
                    {error}
                  </Text>
                )}

                <Group justify="flex-end">
                  <Button onClick={handleDBSetup} loading={dbMutation.isPending}>
                    Next Step
                  </Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Administrator" description="Create admin account">
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <TextInput
                  label="Username"
                  placeholder="admin"
                  required
                  value={username}
                  onChange={(e) => setUsername(e.currentTarget.value)}
                />
                <PasswordInput
                  label="Password"
                  placeholder="Your password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.currentTarget.value)}
                />
                <TextInput
                  label="Full Name"
                  placeholder="Administrator"
                  value={fullName}
                  onChange={(e) => setFullName(e.currentTarget.value)}
                />
                <TextInput
                  label="Email"
                  placeholder="admin@example.com"
                  value={email}
                  onChange={(e) => setEmail(e.currentTarget.value)}
                />

                {error && (
                  <Text color="red" size="sm">
                    {error}
                  </Text>
                )}

                <Group justify="flex-end">
                  <Button variant="default" onClick={() => setActive(0)}>Back</Button>
                  <Button onClick={handleUserSetup} loading={userMutation.isPending}>
                    Finish Setup
                  </Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>
        </Stepper>
      </Stack>
    </Container>
  )
}
