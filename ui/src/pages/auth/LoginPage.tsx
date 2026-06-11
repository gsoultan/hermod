import { IconActivity, IconArrowsLeftRight, IconCloudUpload, IconDatabase, IconLock, IconLogin, IconPuzzle, IconRocket, IconShield, IconUser } from '@tabler/icons-react';
import { useEffect, useState } from 'react'
import { Text, TextInput, Button, Paper, Stack, Container, PasswordInput, Group, Anchor, Divider, Box, Center, useMantineColorScheme, SimpleGrid, Title, ThemeIcon, rem, Badge, Image } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { useNavigate, useSearch, Link } from '@tanstack/react-router'
import { apiFetch } from '@/api'
import { setToken } from '../../auth/storage'

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [twoFactorRequired, setTwoFactorRequired] = useState(false);
  const [twoFactorCode, setTwoFactorCode] = useState('');
  const [userId, setUserId] = useState<string | null>(null);
  const [pendingToken, setPendingToken] = useState<string | null>(null);
  // First-time 2FA enrollment state (2FA enabled but no secret registered)
  const [enrollRequired, setEnrollRequired] = useState(false);
  const [enrollSecret, setEnrollSecret] = useState<string | null>(null);
  const [enrollURL, setEnrollURL] = useState<string | null>(null);
  // Scannable QR code (data URL) generated locally from the otpauth enroll URL
  const [enrollQrDataUrl, setEnrollQrDataUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()
  const { redirect } = useSearch({ from: '/login' })
  const { colorScheme } = useMantineColorScheme();
  const dark = colorScheme === 'dark';

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
      if (data.two_factor_required) {
        setTwoFactorRequired(true);
        setUserId(data.user_id);
        setPendingToken(data.pending_token);
        return;
      }
      if (data.two_factor_enroll_required) {
        setEnrollRequired(true);
        setUserId(data.user_id);
        setPendingToken(data.pending_token);
        return;
      }
      setToken(data.token)
      navigate({ to: redirect || '/' })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  const login2FAMutation = useMutation({
    mutationFn: async (data: any) => {
      const response = await apiFetch('/api/auth/2fa/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(data),
      })

      if (!response.ok) {
        const errorData = await response.text()
        throw new Error(errorData || 'Failed to verify 2FA code')
      }
      return response.json()
    },
    onSuccess: (data) => {
      setToken(data.token)
      navigate({ to: redirect || '/' })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault()
    if (twoFactorRequired) {
      login2FAMutation.mutate({ user_id: userId, pending_token: pendingToken, code: twoFactorCode })
    } else if (enrollRequired) {
      // For enrollment, we verify using the just-provisioned secret and user-provided code
      if (!userId || !pendingToken || !enrollSecret) {
        setError('Enrollment session is missing required data')
        return
      }
      verifyEnrollMutation.mutate({ user_id: userId, pending_token: pendingToken, secret: enrollSecret, code: twoFactorCode })
    } else {
      loginMutation.mutate({ username, password })
    }
  }

  // Start enrollment automatically when required
  useEffect(() => {
    if (enrollRequired && userId && pendingToken && !enrollSecret && !setupEnrollMutation.isPending && !setupEnrollMutation.isSuccess) {
      setupEnrollMutation.mutate({ user_id: userId, pending_token: pendingToken })
    }
  }, [enrollRequired, userId, pendingToken, enrollSecret])

  // Generate the QR code locally from the otpauth URL so it can be scanned by
  // authenticator apps (Google Authenticator, Authy, Apple/iOS Passwords, etc.).
  // Generating locally avoids external services that may be blocked by CSP/network.
  useEffect(() => {
    let cancelled = false
    async function gen() {
      if (!enrollURL) {
        setEnrollQrDataUrl(null)
        return
      }
      try {
        const QR = await import('qrcode')
        const dataUrl = await QR.toDataURL(enrollURL, { width: 200, margin: 1, errorCorrectionLevel: 'M' })
        if (!cancelled) setEnrollQrDataUrl(dataUrl)
      } catch {
        if (!cancelled) setEnrollQrDataUrl(null)
      }
    }
    gen()
    return () => {
      cancelled = true
    }
  }, [enrollURL])

  // Begin enrollment (no session): fetch secret + otpauth URL
  const setupEnrollMutation = useMutation({
    mutationFn: async (data: any) => {
      const response = await apiFetch('/api/auth/2fa/setup/pending', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data),
      })
      if (!response.ok) {
        const errorData = await response.text()
        throw new Error(errorData || 'Failed to start 2FA enrollment')
      }
      return response.json()
    },
    onSuccess: (data) => {
      setEnrollSecret(data.secret)
      setEnrollURL(data.url)
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  // Verify enrollment (and complete login)
  const verifyEnrollMutation = useMutation({
    mutationFn: async (data: any) => {
      const response = await apiFetch('/api/auth/2fa/verify/pending', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data),
      })
      if (!response.ok) {
        const errorData = await response.text()
        throw new Error(errorData || 'Failed to verify 2FA enrollment')
      }
      return response.json()
    },
    onSuccess: (data) => {
      setToken(data.token)
      navigate({ to: redirect || '/' })
    },
    onError: (err) => {
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  })

  return (
    <Box style={{ minHeight: '100vh', overflow: 'hidden' }}>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing={0}>
        {/* Left Side: Animation & Branding */}
        <Box 
          visibleFrom="md"
          style={{ 
            height: '100vh',
            background: dark 
              ? 'linear-gradient(135deg, var(--mantine-color-indigo-9) 0%, var(--mantine-color-dark-8) 100%)'
              : 'linear-gradient(135deg, var(--mantine-color-indigo-6) 0%, var(--mantine-color-cyan-6) 100%)',
            position: 'relative',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexDirection: 'column',
            overflow: 'hidden',
            color: 'white'
          }}
        >
          {/* Animated Background Elements */}
          <Box 
            style={{ 
              position: 'absolute', 
              top: 0, 
              left: 0, 
              right: 0, 
              bottom: 0, 
              zIndex: 1,
              opacity: 0.2
            }}
          >
            {[...Array(6)].map((_, i) => (
              <Box
                key={i}
                style={{
                  position: 'absolute',
                  top: `${Math.random() * 100}%`,
                  left: `${Math.random() * 100}%`,
                  animation: `float ${10 + Math.random() * 20}s infinite ease-in-out`,
                  animationDelay: `${-Math.random() * 20}s`,
                }}
              >
                {i % 3 === 0 ? <IconDatabase size={rem(60)} /> : i % 3 === 1 ? <IconCloudUpload size={rem(60)} /> : <IconActivity size={rem(60)} />}
              </Box>
            ))}
          </Box>

          <style>
            {`
              @keyframes float {
                0%, 100% { transform: translate(0, 0) rotate(0deg); }
                33% { transform: translate(30px, -50px) rotate(10deg); }
                66% { transform: translate(-20px, 20px) rotate(-10deg); }
              }
              @keyframes pulse-glow {
                0%, 100% { transform: scale(1); opacity: 0.5; }
                50% { transform: scale(1.2); opacity: 0.8; }
              }
              @keyframes orbit {
                from { transform: rotate(0deg) translateX(150px) rotate(0deg); }
                to { transform: rotate(360deg) translateX(150px) rotate(-360deg); }
              }
              @keyframes orbit2 {
                from { transform: rotate(180deg) translateX(120px) rotate(-180deg); }
                to { transform: rotate(540deg) translateX(120px) rotate(-540deg); }
              }
            `}
          </style>

          <Box style={{ zIndex: 2, textAlign: 'center' }}>
            <Center mb="xl">
              <Box style={{ position: 'relative' }}>
                 <Box 
                  style={{ 
                    position: 'absolute', 
                    top: -20, 
                    left: -20, 
                    right: -20, 
                    bottom: -20, 
                    background: 'var(--mantine-color-white)', 
                    borderRadius: '50%', 
                    opacity: 0.1,
                    animation: 'pulse-glow 3s infinite'
                  }} 
                />
                <ThemeIcon 
                  size={120} 
                  radius={40} 
                  variant="white" 
                  c="indigo.6"
                  style={{ boxShadow: '0 20px 40px rgba(0,0,0,0.2)' }}
                >
                  <IconRocket size={rem(70)} stroke={1.5} />
                </ThemeIcon>
                
                {/* Orbiting icons */}
                <Box style={{ position: 'absolute', top: '50%', left: '50%', animation: 'orbit 15s linear infinite' }}>
                   <IconPuzzle size={30} style={{ color: 'white' }} />
                </Box>
                <Box style={{ position: 'absolute', top: '50%', left: '50%', animation: 'orbit2 20s linear infinite' }}>
                   <IconArrowsLeftRight size={24} style={{ color: 'white' }} />
                </Box>
              </Box>
            </Center>

            <Title order={1} size={rem(64)} fw={900} style={{ letterSpacing: '-2px' }}>
              Hermod
            </Title>
            <Text size="xl" fw={500} opacity={0.9} mt="xs">
              Enterprise Data Orchestration
            </Text>
            <Stack gap="xs" mt={50}>
               <Group gap="sm" justify="center">
                 <Badge variant="dot" color="white" size="lg">Reliable</Badge>
                 <Badge variant="dot" color="white" size="lg">Scalable</Badge>
                 <Badge variant="dot" color="white" size="lg">Real-time</Badge>
               </Group>
            </Stack>
          </Box>

          <Box mt={100} style={{ zIndex: 2 }}>
            <Text size="sm" opacity={0.7}>
              Powering modern data infrastructure
            </Text>
          </Box>
        </Box>

        {/* Right Side: Login Form */}
        <Box 
          style={{ 
            height: '100vh',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: dark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-gray-0)',
          }}
        >
          <Container size="xs" w="100%" px="xl">
            <Stack gap="xl">
              <Box hiddenFrom="md" style={{ textAlign: 'center' }}>
                <Group justify="center" gap="xs" mb="xs">
                  <IconRocket size={32} color="var(--mantine-color-indigo-6)" />
                  <Text component="h2" size="xl" fw={800} variant="gradient" gradient={{ from: 'indigo', to: 'cyan' }}>Hermod</Text>
                </Group>
                <Text c="dimmed" size="sm">Enterprise Data Orchestration</Text>
              </Box>

              <Stack gap={5}>
                <Title order={2} fw={800}>Welcome back</Title>
                <Text c="dimmed" size="sm">Please enter your details to sign in</Text>
              </Stack>

              <Paper withBorder p={40} radius="lg" shadow="md" style={{ position: 'relative', overflow: 'hidden' }}>
                <Box 
                  style={{ 
                    position: 'absolute', 
                    top: 0, 
                    left: 0, 
                    right: 0, 
                    height: '4px', 
                    background: 'linear-gradient(90deg, var(--mantine-color-indigo-6), var(--mantine-color-cyan-6))' 
                  }} 
                />
                
                <form onSubmit={handleLogin}>
                  <Stack gap="lg">
                    {!twoFactorRequired && !enrollRequired ? (
                      <>
                        <TextInput
                          label="Username"
                          placeholder="Your username"
                          required
                          size="md"
                          leftSection={<IconUser size="1.1rem" stroke={1.5} />}
                          value={username}
                          onChange={(e) => setUsername(e.currentTarget.value)}
                        />
                        <Stack gap={5}>
                          <PasswordInput
                            label="Password"
                            placeholder="Your password"
                            required
                            size="md"
                            leftSection={<IconLock size="1.1rem" stroke={1.5} />}
                            value={password}
                            onChange={(e) => setPassword(e.currentTarget.value)}
                          />
                          <Group justify="flex-end">
                            <Anchor component={Link} to="/forgot-password" size="sm" fw={500}>
                              Forgot password?
                            </Anchor>
                          </Group>
                        </Stack>
                      </>
                    ) : twoFactorRequired ? (
                      <Stack gap="md">
                        <Title order={4} ta="center">Two-Factor Authentication</Title>
                        <Text size="sm" c="dimmed" ta="center">
                          Please enter the 6-digit code from your authenticator app.
                        </Text>
                        <TextInput
                          label="Verification Code"
                          placeholder="000000"
                          required
                          size="md"
                          maxLength={6}
                          leftSection={<IconShield size="1.1rem" stroke={1.5} />}
                          value={twoFactorCode}
                          onChange={(e) => setTwoFactorCode(e.currentTarget.value)}
                        />
                        <Anchor 
                          component="button" 
                          type="button" 
                          size="sm" 
                          onClick={() => setTwoFactorRequired(false)}
                        >
                          Back to login
                        </Anchor>
                      </Stack>
                    ) : (
                      <Stack gap="md">
                        <Title order={4} ta="center">Set up Two-Factor Authentication</Title>
                        <Text size="sm" c="dimmed" ta="center">
                          2FA is enabled for your account but not yet registered. Scan the QR code below with your authenticator app (Google Authenticator, Authy, Microsoft Authenticator, Apple/iOS Passwords, etc.), then enter the 6-digit code.
                        </Text>
                        {enrollURL && (
                          <Box style={{ display: 'flex', justifyContent: 'center' }} p="md" bg="var(--mantine-color-default-hover)">
                            {enrollQrDataUrl ? (
                              <Image src={enrollQrDataUrl} w={200} h={200} alt="2FA QR code" />
                            ) : (
                              <Text size="sm" c="dimmed">Generating QR code…</Text>
                            )}
                          </Box>
                        )}
                        {enrollSecret && (
                          <Text size="sm" ta="center">Can't scan? Enter this secret manually: <strong>{enrollSecret}</strong></Text>
                        )}
                        {enrollURL && (
                          <Anchor href={enrollURL} size="xs" c="dimmed" ta="center">
                            Open in Authenticator app (otpauth link)
                          </Anchor>
                        )}
                        <TextInput
                          label="Verification Code"
                          placeholder="000000"
                          required
                          size="md"
                          maxLength={6}
                          leftSection={<IconShield size="1.1rem" stroke={1.5} />}
                          value={twoFactorCode}
                          onChange={(e) => setTwoFactorCode(e.currentTarget.value)}
                        />
                        <Group justify="space-between">
                          <Anchor 
                            component="button" 
                            type="button" 
                            size="sm" 
                            onClick={() => { setEnrollRequired(false); setEnrollSecret(null); setEnrollURL(null); setTwoFactorCode(''); }}
                          >
                            Back to login
                          </Anchor>
                          <Button 
                            type="submit"
                            loading={verifyEnrollMutation.isPending || setupEnrollMutation.isPending}
                          >
                            Verify & Enable 2FA
                          </Button>
                        </Group>
                      </Stack>
                    )}

                    {error && (
                      <Paper withBorder p="xs" radius="sm" bg="red.0" c="red.9" style={{ borderColor: 'var(--mantine-color-red-2)' }}>
                        <Text size="sm" role="alert" aria-live="assertive" fw={500}>
                          {error}
                        </Text>
                      </Paper>
                    )}

                    {!enrollRequired && (
                      <Button 
                        type="submit" 
                        fullWidth 
                        size="md"
                        loading={loginMutation.isPending || login2FAMutation.isPending}
                        leftSection={<IconLogin size="1.2rem" stroke={1.5} />}
                      >
                        {twoFactorRequired ? 'Verify Code' : 'Sign In'}
                      </Button>
                    )}

                    {!twoFactorRequired && !enrollRequired && (
                      <>
                        <Divider label="or continue with" labelPosition="center" />
                        <Button 
                          variant="default" 
                          fullWidth 
                          size="md"
                          leftSection={<IconShield size="1.2rem" stroke={1.5} />}
                          onClick={() => window.location.href = '/api/auth/oidc'}
                        >
                          SSO (OIDC)
                        </Button>
                      </>
                    )}
                  </Stack>
                </form>
              </Paper>

              <Group justify="center" gap="xl" mt="xl">
                <Text size="xs" c="dimmed">
                  &copy; {new Date().getFullYear()} Hermod Project
                </Text>
                <Anchor href="https://github.com/hermod-project/hermod" target="_blank" size="xs" c="dimmed">
                  GitHub
                </Anchor>
                <Anchor href="/docs" size="xs" c="dimmed">
                  Docs
                </Anchor>
              </Group>
            </Stack>
          </Container>
        </Box>
      </SimpleGrid>
    </Box>
  )
}


