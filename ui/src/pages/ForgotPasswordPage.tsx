import { useState } from 'react'
import { Title, Text, TextInput, Button, Paper, Stack, Container, Alert } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { apiFetch } from '../api'

export function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

  const forgotPasswordMutation = useMutation({
    mutationFn: async (email: string) => {
      const response = await apiFetch('/api/forgot-password', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ email }),
      })

      if (!response.ok) {
        const errorData = await response.text()
        throw new Error(errorData || 'Failed to process request')
      }
      return response.json()
    },
    onSuccess: (data) => {
      setSuccess(data.message)
      setError(null)
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
      setSuccess(null)
    }
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    forgotPasswordMutation.mutate(email)
  }

  return (
    <Container size="xs" mt={100}>
      <Stack gap="xl">
        <Stack gap="xs" align="center">
          <Title order={1}>Hermod</Title>
          <Text c="dimmed">Reset your password</Text>
        </Stack>

        <Paper withBorder p="xl" radius="md">
          {success ? (
            <Stack gap="md">
              <Alert title="Success" color="green">
                {success}
              </Alert>
              <Button component={Link} to="/login" fullWidth mt="md">
                Back to Login
              </Button>
            </Stack>
          ) : (
            <form onSubmit={handleSubmit}>
              <Stack gap="md">
                <Text size="sm">
                  Enter your email address and we'll send you a new temporary password.
                </Text>
                <TextInput
                  label="Email"
                  placeholder="your@email.com"
                  required
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.currentTarget.value)}
                />

                {error && (
                  <Text color="red" size="sm" role="alert" aria-live="assertive">
                    {error}
                  </Text>
                )}

                <Button type="submit" fullWidth mt="xl" loading={forgotPasswordMutation.isPending}>
                  Send Password
                </Button>

                <Button variant="subtle" component={Link} to="/login" fullWidth>
                  Back to Login
                </Button>
              </Stack>
            </form>
          )}
        </Paper>
      </Stack>
    </Container>
  )
}
