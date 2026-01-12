import { useState } from 'react'
import { Title, Text, TextInput, Button, Paper, Stack, Container, PasswordInput } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { apiFetch } from '../api'

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()
  const { redirect } = useSearch({ from: '/login' })

  const loginMutation = useMutation({
    mutationFn: async (creds: any) => {
      const response = await apiFetch('/api/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(creds),
      })

      if (!response.ok) {
        const errorData = await response.text()
        throw new Error(errorData || 'Failed to login')
      }
      return response.json()
    },
    onSuccess: (data) => {
      localStorage.setItem('hermod_token', data.token)
      navigate({ to: redirect || '/' })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault()
    loginMutation.mutate({ username, password })
  }

  return (
    <Container size="xs" mt={100}>
      <Stack gap="xl">
        <Stack gap="xs" align="center">
          <Title order={1}>Hermod</Title>
          <Text c="dimmed">Sign in to your account</Text>
        </Stack>

        <Paper withBorder p="xl" radius="md">
          <form onSubmit={handleLogin}>
            <Stack gap="md">
              <TextInput
                label="Username"
                placeholder="Your username"
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

              {error && (
                <Text color="red" size="sm">
                  {error}
                </Text>
              )}

              <Button type="submit" fullWidth mt="xl" loading={loginMutation.isPending}>
                Sign In
              </Button>
            </Stack>
          </form>
        </Paper>
      </Stack>
    </Container>
  )
}
