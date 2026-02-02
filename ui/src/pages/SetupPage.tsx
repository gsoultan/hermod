import { useEffect, useMemo, useState } from 'react'
import { Title, Text, TextInput, Select, Button, Paper, Stack, Container, Stepper, Group, PasswordInput, Alert, Progress, Checkbox, NumberInput } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { useMutation } from '@tanstack/react-query'
import { apiFetch } from '../api'

interface SetupPageProps {
  isConfigured: boolean
  onConfigured: () => void
}

export function SetupPage({ isConfigured, onConfigured }: SetupPageProps) {
  const [active, setActive] = useState(0)
  const showStepperDesc = useMediaQuery('(min-width: 62em)')
  const [saving, setSaving] = useState(false)
  
  // Set initial step based on configuration status but allow going back
  useState(() => {
    if (isConfigured) {
      setActive(1)
    }
  })
  
  // Database setup state (local for this wizard session)
  const [dbType, setDbType] = useState<string | null>(null)
  const [dbConn, setDbConn] = useState('')
  // DSN builder toggle + fields
  const [useBuilder, setUseBuilder] = useState(true)
  const [host, setHost] = useState('localhost')
  const [port, setPort] = useState('')
  const [user, setUser] = useState('')
  const [pw, setPw] = useState('')
  const [dbname, setDbname] = useState('hermod')
  // Database list fetch state
  const [availableDBs, setAvailableDBs] = useState<string[]>([])
  const [fetchingDBs, setFetchingDBs] = useState(false)
  const [fetchDBsError, setFetchDBsError] = useState<string | null>(null)
  // DB test state (for gating Next button on DB step)
  const [dbTestOk, setDbTestOk] = useState(false)
  
  // Logging Database setup state
  const [useSeparateLogDb, setUseSeparateLogDb] = useState(false)
  const [logDbType, setLogDbType] = useState<string | null>(null)
  const [logDbConn, setLogDbConn] = useState('')
  
  // User state
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [cryptoMasterKey, setCryptoMasterKey] = useState('')

  // SMTP state
  const [smtpHost, setSmtpHost] = useState('')
  const [smtpPort, setSmtpPort] = useState<number | string>(587)
  const [smtpUser, setSmtpUser] = useState('')
  const [smtpPassword, setSmtpPassword] = useState('')
  const [smtpFrom, setSmtpFrom] = useState('')
  const [smtpSsl, setSmtpSsl] = useState(false)
  const [defaultEmail, setDefaultEmail] = useState('')
  // SMTP test state
  const [smtpTest, setSmtpTest] = useState<{ status: 'ok' | 'error' | 'skipped'; error?: string } | null>(null)

  // Worker setup state
  const [workerName, setWorkerName] = useState('Default Worker')
  const [workerHost, setWorkerHost] = useState(window.location.hostname)
  const [workerPort, setWorkerPort] = useState(8080)

  // Engine settings
  const [maxRetries, setMaxRetries] = useState(3)
  const [retryInterval, setRetryInterval] = useState('1s')
  const [reconnectInterval, setReconnectInterval] = useState('5s')

  // Buffer settings
  const [bufferType, setBufferType] = useState('ring_buffer')
  const [bufferSize, setBufferSize] = useState(1024)
  const [bufferPath, setBufferPath] = useState('')
  const [bufferCompression, setBufferCompression] = useState('none')

  // Secrets settings
  const [secretsType, setSecretsType] = useState('env')
  const [vaultAddress, setVaultAddress] = useState('')
  const [vaultToken, setVaultToken] = useState('')
  const [vaultMount, setVaultMount] = useState('secret')
  const [awsRegion, setAwsRegion] = useState('us-east-1')
  const [azureVaultUrl, setAzureVaultUrl] = useState('')
  const [secretsPrefix, setSecretsPrefix] = useState('HERMOD_')

  // State Store settings
  const [stateStoreType, setStateStoreType] = useState('sqlite')
  const [stateStorePath, setStateStorePath] = useState('hermod_state.db')
  const [stateStoreAddress, setStateStoreAddress] = useState('')
  const [stateStorePassword, setStateStorePassword] = useState('')
  const [stateStoreDB, setStateStoreDB] = useState(0)
  const [stateStorePrefix, setStateStorePrefix] = useState('hermod:')

  // Observability settings
  const [otlpEndpoint, setOtlpEndpoint] = useState('')
  const [otlpProtocol, setOtlpProtocol] = useState('grpc')
  const [otlpInsecure, setOtlpInsecure] = useState(true)
  const [otlpServiceName, setOtlpServiceName] = useState('hermod')

  // Auth (OIDC) settings
  const [oidcEnabled, setOidcEnabled] = useState(false)
  const [oidcIssuer, setOidcIssuer] = useState('')
  const [oidcClientId, setOidcClientId] = useState('')
  const [oidcClientSecret, setOidcClientSecret] = useState('')
  const [oidcRedirect, setOidcRedirect] = useState(`${window.location.origin}/api/auth/callback`)
  const [oidcScopes, setOidcScopes] = useState('openid,profile,email')

  const [error, setError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<{ ok: boolean; error?: string; details?: any; hint?: string } | null>(null)

  const testDbMutation = useMutation({
    mutationFn: async () => {
      const payload: any = { type: dbType, conn: dbConn, crypto_master_key: cryptoMasterKey }
      if (useSeparateLogDb && logDbType && logDbConn) {
        payload.log_type = logDbType
        payload.log_conn = logDbConn
      }
      const response = await apiFetch('/api/config/database/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      const json = await response.json()
      return json
    },
    onSuccess: (res: any) => {
      setTestResult(res)
      setError(null)
      setDbTestOk(Boolean(res?.ok))
    },
    onError: (err: any) => {
      setTestResult({ ok: false, error: err?.message || 'Test failed' })
      setDbTestOk(false)
    }
  })

  // Register initial worker
  const createWorkerMutation = useMutation({
    mutationFn: async (worker: any) => {
      const response = await apiFetch('/api/workers', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(worker),
      })
      if (!response.ok) {
        throw new Error('Failed to register worker')
      }
      return response.json()
    },
  })

  // SMTP test removed from wizard to avoid backend calls before final step
  // SMTP: test current config from the SMTP step
  const testSmtpMutation = useMutation({
    mutationFn: async () => {
      const response = await apiFetch('/api/settings/test-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          smtp_host: smtpHost,
          smtp_port: Number(smtpPort),
          smtp_user: smtpUser,
          smtp_password: smtpPassword,
          smtp_from: smtpFrom,
          smtp_ssl: smtpSsl,
          default_email: defaultEmail,
        }),
      })
      const json = await response.json()
      if (!response.ok) {
        throw new Error(json?.error || 'Failed to test SMTP settings')
      }
      return json as { channel: string; status: string; error?: string }[]
    },
    onSuccess: (results) => {
      const email = Array.isArray(results)
        ? (results as any[]).find((r) => r && r.channel === 'email')
        : null
      if (email) {
        setSmtpTest({ status: email.status as any, error: email.error })
      } else {
        setSmtpTest({ status: 'skipped' })
      }
    },
    onError: (err: any) => {
      setSmtpTest({ status: 'error', error: err?.message || 'SMTP test failed' })
    },
  })

  // Invalidate SMTP test when inputs change
  useEffect(() => {
    setSmtpTest(null)
  }, [smtpHost, smtpPort, smtpUser, smtpPassword, smtpFrom, smtpSsl, defaultEmail])

  // Final step: perform all backend calls in sequence
  async function handleFinalSave() {
    setError(null)
    setSaving(true)
    try {
      // Single-call setup endpoint (first run only)
      const payload: any = {
        db: {
          type: dbType,
          conn: dbConn,
          log_type: useSeparateLogDb ? logDbType : undefined,
          log_conn: useSeparateLogDb ? logDbConn : undefined,
          crypto_master_key: cryptoMasterKey
        },
        admin: { username, password, full_name: fullName, email },
        config: {
          engine: {
            max_retries: maxRetries,
            retry_interval: retryInterval,
            reconnect_interval: reconnectInterval
          },
          buffer: {
            type: bufferType,
            size: bufferSize,
            path: bufferPath,
            compression: bufferCompression
          },
          secrets: {
            type: secretsType,
            vault: secretsType === 'vault' ? { address: vaultAddress, token: vaultToken, mount: vaultMount } : undefined,
            openbao: secretsType === 'openbao' ? { address: vaultAddress, token: vaultToken, mount: vaultMount } : undefined,
            aws: secretsType === 'aws' ? { region: awsRegion } : undefined,
            azure: secretsType === 'azure' ? { vault_url: azureVaultUrl } : undefined,
            env: secretsType === 'env' ? { prefix: secretsPrefix } : undefined
          },
          state_store: {
            type: stateStoreType,
            path: stateStoreType === 'sqlite' ? stateStorePath : undefined,
            address: stateStoreType !== 'sqlite' ? stateStoreAddress : undefined,
            password: stateStoreType !== 'sqlite' ? stateStorePassword : undefined,
            db: stateStoreType === 'redis' ? stateStoreDB : undefined,
            prefix: stateStorePrefix
          },
          observability: {
            otlp: {
              endpoint: otlpEndpoint,
              protocol: otlpProtocol,
              insecure: otlpInsecure,
              service_name: otlpServiceName
            }
          },
          auth: {
            oidc: {
              enabled: oidcEnabled,
              issuer_url: oidcIssuer,
              client_id: oidcClientId,
              client_secret: oidcClientSecret,
              redirect_url: oidcRedirect,
              scopes: oidcScopes.split(',').map(s => s.trim())
            }
          }
        }
      }
      if (smtpHost || smtpUser || smtpPassword || smtpFrom || defaultEmail) {
        payload.smtp = {
          smtp_host: smtpHost,
          smtp_port: Number(smtpPort),
          smtp_user: smtpUser,
          smtp_password: smtpPassword,
          smtp_from: smtpFrom,
          smtp_ssl: smtpSsl,
          default_email: defaultEmail,
        }
      }
      await apiFetch('/api/config/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })

      // Register initial worker after DB/Admin are ready
      try {
        await createWorkerMutation.mutateAsync({
          name: workerName,
          host: workerHost,
          port: Number(workerPort),
          description: 'Initial worker registered during setup'
        })
      } catch (e) {
        console.warn('Failed to register initial worker, will continue anyway', e)
      }

      onConfigured()
    } finally {
      setSaving(false)
    }
  }

  function generateStrongPassword(len = 20) {
    const bytes = new Uint8Array(len)
    if (typeof window !== 'undefined' && window.crypto && window.crypto.getRandomValues) {
      window.crypto.getRandomValues(bytes)
    } else {
      for (let i = 0; i < len; i++) bytes[i] = Math.floor(Math.random() * 256)
    }
    // base64url without padding
    const b64 = btoa(String.fromCharCode(...Array.from(bytes)))
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/g, '')
    // Trim to desired length range 16-40
    const target = Math.max(16, Math.min(40, len))
    return b64.slice(0, target)
  }

  const dsnHelp = useMemo(() => {
    switch (dbType) {
      case 'sqlite':
        return 'Example: hermod.db or C:\\data\\hermod.db. Hermod will create the file if it does not exist.'
      case 'postgres':
        return 'Example: postgres://user:pass@localhost:5432/hermod?sslmode=disable'
      case 'mysql':
      case 'mariadb':
        return 'Example: user:pass@tcp(localhost:3306)/hermod?parseTime=true'
      case 'mongodb':
        return 'Example: mongodb+srv://user:pass@cluster.example.com/hermod?retryWrites=true&w=majority'
      default:
        return ''
    }
  }, [dbType])

  // Narrowing handler to satisfy Select's onChange typing
  const handleDbTypeChange = (value: string | null) => {
    if (
      value === null ||
      value === 'sqlite' ||
      value === 'postgres' ||
      value === 'mysql' ||
      value === 'mariadb' ||
      value === 'mongodb'
    ) {
      setDbType(value)
    } else {
      setDbType(null)
    }
  }

  // Default port by DB type
  function defaultPort(t: string | null): string {
    switch (t) {
      case 'postgres':
        return '5432'
      case 'mysql':
      case 'mariadb':
        return '3306'
      case 'mongodb':
        return '27017'
      default:
        return ''
    }
  }

  // When DB type changes, adjust builder defaults
  useEffect(() => {
    // Generate crypto master key if not set
    if (!cryptoMasterKey) {
      setCryptoMasterKey(generateStrongPassword(32))
    }

    // SQLite does not use builder fields
    if (dbType === 'sqlite') {
      setUseBuilder(false)
      // Provide a convenience default only after user explicitly selects sqlite
      if (!dbConn) setDbConn('hermod.db')
      return
    }
    setUseBuilder(true)
    setPort((p) => p || defaultPort(dbType))
    // Keep previous user/pw/dbname if present
  }, [dbType, cryptoMasterKey])

  // Build DSN when using builder (non-SQLite)
  useEffect(() => {
    if (!useBuilder || !dbType || dbType === 'sqlite') return
    const h = host.trim() || 'localhost'
    const prt = (port || defaultPort(dbType)).trim()
    const u = user.trim()
    const p = pw
    const db = dbname.trim()

    if (dbType === 'postgres') {
      // postgres://user:pass@host:port/db
      const auth = u ? `${encodeURIComponent(u)}${p ? `:${encodeURIComponent(p)}` : ''}@` : ''
      const base = `postgres://${auth}${h}${prt ? `:${prt}` : ''}/${db || 'hermod'}`
      setDbConn(base)
    } else if (dbType === 'mysql' || dbType === 'mariadb') {
      // user:pass@tcp(host:port)/db?parseTime=true
      const auth = u ? `${u}${p ? `:${p}` : ''}@` : ''
      const base = `${auth}tcp(${h}${prt ? `:${prt}` : ''})/${db || 'hermod'}?parseTime=true`
      setDbConn(base)
    } else if (dbType === 'mongodb') {
      // mongodb://user:pass@host:port/db
      const auth = u ? `${encodeURIComponent(u)}${p ? `:${encodeURIComponent(p)}` : ''}@` : ''
      const base = `mongodb://${auth}${h}${prt ? `:${prt}` : ''}/${db || 'hermod'}`
      setDbConn(base)
    }
    // Reset previously fetched DBs when builder fields change
    setAvailableDBs([])
    setFetchDBsError(null)
  }, [useBuilder, dbType, host, port, user, pw, dbname])

  // Invalidate previous DB test when connection-related inputs change
  useEffect(() => {
    setDbTestOk(false)
    setTestResult(null)
  }, [dbType, dbConn, cryptoMasterKey])

  // Build a temporary connection string optimized for listing databases
  function buildConnForFetch(): string {
    if (!dbType) return dbConn
    if (!useBuilder || dbType === 'sqlite') return dbConn
    const h = host.trim() || 'localhost'
    const prt = (port || defaultPort(dbType)).trim()
    const u = user.trim()
    const p = pw
    if (dbType === 'postgres') {
      const auth = u ? `${encodeURIComponent(u)}${p ? `:${encodeURIComponent(p)}` : ''}@` : ''
      return `postgres://${auth}${h}${prt ? `:${prt}` : ''}/postgres`
    }
    if (dbType === 'mysql' || dbType === 'mariadb') {
      const auth = u ? `${u}${p ? `:${p}` : ''}@` : ''
      return `${auth}tcp(${h}${prt ? `:${prt}` : ''})/` // no db selected
    }
    if (dbType === 'mongodb') {
      const auth = u ? `${encodeURIComponent(u)}${p ? `:${encodeURIComponent(p)}` : ''}@` : ''
      return `mongodb://${auth}${h}${prt ? `:${prt}` : ''}` // no db path
    }
    return dbConn
  }

  async function handleFetchDatabases() {
    if (!dbType || dbType === 'sqlite') return
    try {
      setFetchingDBs(true)
      setFetchDBsError(null)
      const response = await apiFetch('/api/config/databases', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: dbType, conn: buildConnForFetch() }),
      })
      const json = await response.json()
      if (!response.ok) {
        throw new Error(json?.error || 'Failed to fetch databases')
      }
      const list = Array.isArray(json?.databases) ? (json.databases as string[]) : []
      setAvailableDBs(list)
    } catch (e: any) {
      setAvailableDBs([])
      setFetchDBsError(e?.message || 'Failed to fetch databases')
    } finally {
      setFetchingDBs(false)
    }
  }

  function passwordScore(pw: string): number {
    let score = 0
    if (pw.length >= 8) score++
    if (/[a-z]/.test(pw)) score++
    if (/[A-Z]/.test(pw)) score++
    if (/[0-9]/.test(pw)) score++
    if (/[^A-Za-z0-9]/.test(pw)) score++
    return score
  }
  const pwScore = passwordScore(password)
  const pwColor = pwScore >= 4 ? 'green' : pwScore >= 3 ? 'yellow' : 'red'
  const pwLabel = pwScore >= 4 ? 'Strong' : pwScore >= 3 ? 'Fair' : 'Weak'
  const canAdminNext = username.trim().length > 0 && password.trim().length > 0 && pwScore >= 3
  const canDbNext = Boolean(dbType) && dbConn.trim().length > 0 && cryptoMasterKey.length >= 16 && dbTestOk &&
    (!useSeparateLogDb || (Boolean(logDbType) && logDbConn.trim().length > 0))

  return (
    <Container size="lg" mt={48}>
      <Stack gap="xl">
        <Stack gap="xs" align="center">
          <Title order={1}>Welcome to Hermod</Title>
          <Text c="dimmed">Complete the steps below to get started</Text>
        </Stack>
        <div style={{ overflowX: 'auto' }}>
          <Stepper active={active} onStepClick={setActive} allowNextStepsSelect={false} size="sm">
          <Stepper.Step label="Database" description={showStepperDesc ? 'Storage configuration' : undefined}>
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
                  onChange={handleDbTypeChange}
                />

                {/* DSN builder vs raw input */}
                {dbType !== 'sqlite' && useBuilder ? (
                  <Stack gap="sm">
                    <Group grow>
                      <TextInput label="Host" placeholder="localhost" value={host} onChange={(e) => setHost(e.currentTarget.value)} />
                      <TextInput label="Port" placeholder={defaultPort(dbType)} value={port} onChange={(e) => setPort(e.currentTarget.value)} />
                    </Group>
                    <Group grow>
                      <TextInput label="User" placeholder="user" value={user} onChange={(e) => setUser(e.currentTarget.value)} />
                      <TextInput label="Password" type="password" placeholder="password" value={pw} onChange={(e) => setPw(e.currentTarget.value)} />
                    </Group>
                    {(dbType === 'postgres' || dbType === 'mysql' || dbType === 'mariadb' || dbType === 'mongodb') ? (
                      <Stack gap="xs">
                        <Group align="end">
                          <TextInput style={{ flex: 1 }} label="Database" placeholder="hermod" value={dbname} onChange={(e) => setDbname(e.currentTarget.value)} />
                          <Button loading={fetchingDBs} onClick={handleFetchDatabases}>Fetch databases</Button>
                        </Group>
                        {fetchDBsError && (
                          <Alert color="red" title="Failed to fetch databases">{fetchDBsError}</Alert>
                        )}
                        {availableDBs.length > 0 && (
                          <Select
                            label="Select a database"
                            placeholder="Choose from server"
                            searchable
                            clearable
                            data={availableDBs.map((d) => ({ value: d, label: d }))}
                            value={dbname || null}
                            onChange={(val) => setDbname(val || '')}
                          />
                        )}
                      </Stack>
                    ) : (
                      <TextInput label="Database" placeholder="hermod" value={dbname} onChange={(e) => setDbname(e.currentTarget.value)} />
                    )}
                    <Text size="xs" c="dimmed">Preview</Text>
                    <TextInput value={dbConn} onChange={(e) => setDbConn(e.currentTarget.value)} />
                    <Group justify="flex-end">
                      <Button variant="subtle" onClick={() => setUseBuilder(false)}>Use raw connection string</Button>
                    </Group>
                  </Stack>
                ) : (
                  <Stack gap="xs">
                    <TextInput
                      label={dbType === 'sqlite' ? 'DB Path' : 'Connection String'}
                      placeholder={dbType === 'sqlite' ? 'hermod.db' : 'postgres://user:pass@localhost:5432/db'}
                      value={dbConn}
                      onChange={(e) => setDbConn(e.currentTarget.value)}
                    />
                    {dbType !== 'sqlite' && (
                      <Group justify="flex-end">
                        <Button variant="subtle" onClick={() => setUseBuilder(true)}>Use DSN builder</Button>
                      </Group>
                    )}
                  </Stack>
                )}

                <Text size="xs" c="dimmed">{dsnHelp}</Text>

                <Checkbox
                  label="Use a separate database for logging"
                  description="Offload audit logs, workflow logs, and message traces to a different database to improve performance."
                  checked={useSeparateLogDb}
                  onChange={(e) => setUseSeparateLogDb(e.currentTarget.checked)}
                />

                {useSeparateLogDb && (
                  <Paper withBorder p="md" radius="sm">
                    <Stack gap="sm">
                      <Select
                        label="Logging Database Type"
                        placeholder="Select database type"
                        data={[
                          { value: 'sqlite', label: 'SQLite' },
                          { value: 'postgres', label: 'PostgreSQL' },
                          { value: 'mysql', label: 'MySQL' },
                          { value: 'mariadb', label: 'MariaDB' },
                          { value: 'mongodb', label: 'MongoDB' },
                        ]}
                        value={logDbType}
                        onChange={(val) => setLogDbType(val)}
                      />
                      <TextInput
                        label={logDbType === 'sqlite' ? 'Logging DB Path' : 'Logging Connection String'}
                        placeholder={logDbType === 'sqlite' ? 'hermod_logs.db' : 'postgres://user:pass@localhost:5432/hermod_logs'}
                        value={logDbConn}
                        onChange={(e) => setLogDbConn(e.currentTarget.value)}
                      />
                    </Stack>
                  </Paper>
                )}

                <Stack gap="xs">
                  <PasswordInput
                    label="Crypto Master Key"
                    description="Used to encrypt sensitive data (secrets, credentials) in the database. MUST be at least 16 characters."
                    placeholder="Minimum 16 characters"
                    required
                    value={cryptoMasterKey}
                    onChange={(e) => setCryptoMasterKey(e.currentTarget.value)}
                  />
                  <Group justify="space-between">
                    <Button variant="light" onClick={() => setCryptoMasterKey(generateStrongPassword(32))}>
                      Generate Strong Key
                    </Button>
                    <Button
                      variant="default"
                      onClick={() => {
                        if (navigator?.clipboard && cryptoMasterKey) navigator.clipboard.writeText(cryptoMasterKey)
                      }}
                      disabled={!cryptoMasterKey}
                    >
                      Copy Key
                    </Button>
                  </Group>
                </Stack>

                {/* Test database connection and gate Next */}
                <Stack gap="xs">
                  {testDbMutation.isPending && <Text size="sm">Testing connection...</Text>}
                  {testDbMutation.isError && testResult?.error && (
                    <Alert color="red" title="Connection failed">{testResult.error}</Alert>
                  )}
                  {dbTestOk && testResult?.ok && (
                    <Alert color="green" title="Connection successful">Database connection looks good.</Alert>
                  )}
                  <Group justify="space-between">
                    <Button variant="default" onClick={() => testDbMutation.mutate()} disabled={!Boolean(dbType) || !(dbConn.trim().length > 0) || cryptoMasterKey.length < 16}>
                      Test Connection
                    </Button>
                    <Button onClick={() => setActive(1)} disabled={!canDbNext}>
                      Next
                    </Button>
                  </Group>
                </Stack>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="SMTP" description={showStepperDesc ? 'Email notifications (Optional)' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text size="sm">Configure SMTP to receive email notifications for workflow failures and system alerts. This step is optional.</Text>
                
                <Group grow>
                  <TextInput label="SMTP Host" placeholder="smtp.example.com" value={smtpHost} onChange={(e) => setSmtpHost(e.currentTarget.value)} />
                  <NumberInput label="SMTP Port" placeholder="587" value={smtpPort} onChange={setSmtpPort} />
                </Group>
                
                <Group grow>
                  <TextInput label="SMTP User" placeholder="user@example.com" value={smtpUser} onChange={(e) => setSmtpUser(e.currentTarget.value)} />
                  <PasswordInput label="SMTP Password" placeholder="password" value={smtpPassword} onChange={(e) => setSmtpPassword(e.currentTarget.value)} />
                </Group>

                <Group grow>
                  <TextInput label="From Email" placeholder="hermod@example.com" value={smtpFrom} onChange={(e) => setSmtpFrom(e.currentTarget.value)} />
                  <TextInput label="Admin Email" placeholder="admin@example.com" value={defaultEmail} onChange={(e) => setDefaultEmail(e.currentTarget.value)} />
                </Group>

                <Checkbox label="Use SSL/TLS" checked={smtpSsl} onChange={(e) => setSmtpSsl(e.currentTarget.checked)} />

                {/* Test SMTP configuration */}
                <Stack gap="xs">
                  {testSmtpMutation.isPending && <Text size="sm">Sending test email...</Text>}
                  {smtpTest?.status === 'ok' && (
                    <Alert color="green" title="SMTP test succeeded">
                      Test email sent successfully{defaultEmail ? ` to ${defaultEmail}` : ''}.
                    </Alert>
                  )}
                  {smtpTest?.status === 'skipped' && (
                    <Alert color="yellow" title="SMTP test skipped">
                      Provide SMTP Host and Admin Email to send a test.
                    </Alert>
                  )}
                  {smtpTest?.status === 'error' && (
                    <Alert color="red" title="SMTP test failed">{smtpTest.error}</Alert>
                  )}
                  <Group justify="space-between">
                    <Button
                      variant="default"
                      onClick={() => testSmtpMutation.mutate()}
                    >
                      Test SMTP
                    </Button>
                  </Group>
                </Stack>

                <Group justify="space-between">
                  <Button variant="default" onClick={() => setActive(0)}>Back</Button>
                  <Group>
                    <Button variant="subtle" onClick={() => setActive(2)}>Skip for now</Button>
                    <Button onClick={() => setActive(2)}>Next</Button>
                  </Group>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Administrator" description={showStepperDesc ? 'Create admin account' : undefined}>
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
                <Group justify="space-between">
                  <Button variant="light" onClick={() => setPassword(generateStrongPassword())}>
                    Generate Strong Password
                  </Button>
                  <Button
                    variant="default"
                    onClick={() => {
                      if (navigator?.clipboard && password) navigator.clipboard.writeText(password)
                    }}
                    disabled={!password}
                  >
                    Copy Password
                  </Button>
                </Group>
                <Group gap={8} align="center">
                  <Progress value={(pwScore / 5) * 100} color={pwColor} style={{ flex: 1 }} aria-label="Password strength" />
                  <Text size="xs" c={pwColor}>{pwLabel}</Text>
                </Group>
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

                <Group justify="space-between">
                  <Button variant="default" onClick={() => setActive(1)}>Back</Button>
                  <Button onClick={() => setActive(3)} disabled={!canAdminNext}>Next</Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Platform" description={showStepperDesc ? 'Engine & Buffer' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text fw={600}>Engine Settings</Text>
                <NumberInput label="Max Retries" value={maxRetries} onChange={(val) => setMaxRetries(Number(val))} />
                <Group grow>
                  <TextInput label="Retry Interval" placeholder="1s" value={retryInterval} onChange={(e) => setRetryInterval(e.currentTarget.value)} />
                  <TextInput label="Reconnect Interval" placeholder="5s" value={reconnectInterval} onChange={(e) => setReconnectInterval(e.currentTarget.value)} />
                </Group>

                <Text fw={600} mt="md">Buffer Settings</Text>
                <Select
                  label="Buffer Type"
                  data={[
                    { value: 'ring_buffer', label: 'Ring Buffer (In-memory)' },
                    { value: 'disk', label: 'Disk Buffer' },
                    { value: 'nats', label: 'NATS' },
                  ]}
                  value={bufferType}
                  onChange={(val) => setBufferType(val || 'ring_buffer')}
                />
                <NumberInput label="Buffer Size" value={bufferSize} onChange={(val) => setBufferSize(Number(val))} />
                {bufferType === 'disk' && (
                  <TextInput label="Buffer Path" placeholder="/tmp/hermod_buffer" value={bufferPath} onChange={(e) => setBufferPath(e.currentTarget.value)} />
                )}
                <Select
                  label="Compression"
                  data={[
                    { value: 'none', label: 'None' },
                    { value: 'gzip', label: 'Gzip' },
                    { value: 'zstd', label: 'Zstd' },
                  ]}
                  value={bufferCompression}
                  onChange={(val) => setBufferCompression(val || 'none')}
                />

                <Group justify="space-between" mt="md">
                  <Button variant="default" onClick={() => setActive(2)}>Back</Button>
                  <Button onClick={() => setActive(4)}>Next</Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Secrets & State" description={showStepperDesc ? 'Security & Persistence' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text fw={600}>Secret Manager</Text>
                <Select
                  label="Type"
                  data={[
                    { value: 'env', label: 'Environment Variables' },
                    { value: 'vault', label: 'HashiCorp Vault' },
                    { value: 'openbao', label: 'OpenBao' },
                    { value: 'aws', label: 'AWS Secrets Manager' },
                    { value: 'azure', label: 'Azure Key Vault' },
                  ]}
                  value={secretsType}
                  onChange={(val) => setSecretsType(val || 'env')}
                />
                {(secretsType === 'vault' || secretsType === 'openbao') && (
                  <Stack gap="sm">
                    <TextInput label="Address" placeholder="https://vault.example.com:8200" value={vaultAddress} onChange={(e) => setVaultAddress(e.currentTarget.value)} />
                    <PasswordInput label="Token" value={vaultToken} onChange={(e) => setVaultToken(e.currentTarget.value)} />
                    <TextInput label="Mount Path" placeholder="secret" value={vaultMount} onChange={(e) => setVaultMount(e.currentTarget.value)} />
                  </Stack>
                )}
                {secretsType === 'aws' && (
                  <TextInput label="AWS Region" placeholder="us-east-1" value={awsRegion} onChange={(e) => setAwsRegion(e.currentTarget.value)} />
                )}
                {secretsType === 'azure' && (
                  <TextInput label="Vault URL" placeholder="https://myvault.vault.azure.net/" value={azureVaultUrl} onChange={(e) => setAzureVaultUrl(e.currentTarget.value)} />
                )}
                {secretsType === 'env' && (
                  <TextInput label="Env Prefix" placeholder="HERMOD_" value={secretsPrefix} onChange={(e) => setSecretsPrefix(e.currentTarget.value)} />
                )}

                <Text fw={600} mt="md">State Store</Text>
                <Select
                  label="Type"
                  data={[
                    { value: 'sqlite', label: 'SQLite (Local)' },
                    { value: 'redis', label: 'Redis' },
                    { value: 'etcd', label: 'Etcd' },
                  ]}
                  value={stateStoreType}
                  onChange={(val) => setStateStoreType(val || 'sqlite')}
                />
                {stateStoreType === 'sqlite' && (
                  <TextInput label="DB Path" placeholder="hermod_state.db" value={stateStorePath} onChange={(e) => setStateStorePath(e.currentTarget.value)} />
                )}
                {stateStoreType !== 'sqlite' && (
                  <Stack gap="sm">
                    <TextInput label="Address" placeholder="localhost:6379" value={stateStoreAddress} onChange={(e) => setStateStoreAddress(e.currentTarget.value)} />
                    <PasswordInput label="Password" value={stateStorePassword} onChange={(e) => setStateStorePassword(e.currentTarget.value)} />
                    {stateStoreType === 'redis' && (
                      <NumberInput label="DB Index" value={stateStoreDB} onChange={(val) => setStateStoreDB(Number(val))} />
                    )}
                  </Stack>
                )}
                <TextInput label="Prefix" placeholder="hermod:" value={stateStorePrefix} onChange={(e) => setStateStorePrefix(e.currentTarget.value)} />

                <Group justify="space-between" mt="md">
                  <Button variant="default" onClick={() => setActive(3)}>Back</Button>
                  <Button onClick={() => setActive(5)}>Next</Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Auth & Observability" description={showStepperDesc ? 'OIDC & OTLP' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text fw={600}>OIDC Authentication</Text>
                <Checkbox label="Enable OIDC" checked={oidcEnabled} onChange={(e) => setOidcEnabled(e.currentTarget.checked)} />
                {oidcEnabled && (
                  <Stack gap="sm">
                    <TextInput label="Issuer URL" placeholder="https://auth.example.com/" value={oidcIssuer} onChange={(e) => setOidcIssuer(e.currentTarget.value)} />
                    <TextInput label="Client ID" value={oidcClientId} onChange={(e) => setOidcClientId(e.currentTarget.value)} />
                    <PasswordInput label="Client Secret" value={oidcClientSecret} onChange={(e) => setOidcClientSecret(e.currentTarget.value)} />
                    <TextInput label="Redirect URL" value={oidcRedirect} onChange={(e) => setOidcRedirect(e.currentTarget.value)} />
                    <TextInput label="Scopes (comma separated)" value={oidcScopes} onChange={(e) => setOidcScopes(e.currentTarget.value)} />
                  </Stack>
                )}

                <Text fw={600} mt="md">Observability (OTLP)</Text>
                <TextInput label="OTLP Endpoint" placeholder="http://localhost:4317" value={otlpEndpoint} onChange={(e) => setOtlpEndpoint(e.currentTarget.value)} />
                <Select
                  label="Protocol"
                  data={[
                    { value: 'grpc', label: 'gRPC' },
                    { value: 'http', label: 'HTTP' },
                  ]}
                  value={otlpProtocol}
                  onChange={(val) => setOtlpProtocol(val || 'grpc')}
                />
                <Checkbox label="Insecure" checked={otlpInsecure} onChange={(e) => setOtlpInsecure(e.currentTarget.checked)} />
                <TextInput label="Service Name" value={otlpServiceName} onChange={(e) => setOtlpServiceName(e.currentTarget.value)} />

                <Group justify="space-between" mt="md">
                  <Button variant="default" onClick={() => setActive(4)}>Back</Button>
                  <Button onClick={() => setActive(6)}>Next</Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          <Stepper.Step label="Worker" description={showStepperDesc ? 'Initial worker' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text size="sm">Register the first worker instance. This worker will process your workflows.</Text>
                <TextInput label="Worker Name" value={workerName} onChange={(e) => setWorkerName(e.currentTarget.value)} required />
                <Group grow>
                  <TextInput label="Host / IP" value={workerHost} onChange={(e) => setWorkerHost(e.currentTarget.value)} required />
                  <NumberInput label="Port" value={workerPort} onChange={(val) => setWorkerPort(Number(val))} required />
                </Group>
                <Group justify="space-between" mt="md">
                  <Button variant="default" onClick={() => setActive(active - 1)}>Back</Button>
                  <Button onClick={() => setActive(active + 1)}>Next Step</Button>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>

          {/* Final step: Confirm & Save (all backend calls happen here) */}
          <Stepper.Step label="Confirm & Save" description={showStepperDesc ? 'Review and apply configuration' : undefined}>
            <Paper withBorder p="xl" radius="md" mt="xl">
              <Stack gap="md">
                <Text>Review your configuration before saving. No changes have been sent to the server yet.</Text>

                <Stack gap={4}>
                  <Text fw={600}>Database</Text>
                  <Text size="sm">Type: {dbType || '-'}</Text>
                  <Text size="sm">Connection: {dbConn}</Text>
                  <Text size="sm">Crypto Key: {cryptoMasterKey ? `${cryptoMasterKey.slice(0,4)}***${cryptoMasterKey.slice(-4)}` : '-'}</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>SMTP (optional)</Text>
                  <Text size="sm">Host: {smtpHost || '-'}</Text>
                  <Text size="sm">Port: {smtpPort || '-'}</Text>
                  <Text size="sm">User: {smtpUser || '-'}</Text>
                  <Text size="sm">From: {smtpFrom || '-'}</Text>
                  <Text size="sm">Admin Email: {defaultEmail || '-'}</Text>
                  <Text size="sm">SSL/TLS: {smtpSsl ? 'Yes' : 'No'}</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>Administrator</Text>
                  <Text size="sm">Username: {username}</Text>
                  <Text size="sm">Full Name: {fullName || '-'}</Text>
                  <Text size="sm">Email: {email || '-'}</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>Platform (Engine & Buffer)</Text>
                  <Text size="sm">Engine: {maxRetries} retries, {retryInterval} interval, {reconnectInterval} reconnect</Text>
                  <Text size="sm">Buffer: {bufferType} ({bufferSize} size, {bufferCompression} compression)</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>Secrets & State</Text>
                  <Text size="sm">Secrets: {secretsType}</Text>
                  <Text size="sm">State Store: {stateStoreType} ({stateStorePrefix})</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>Auth & Observability</Text>
                  <Text size="sm">OIDC: {oidcEnabled ? 'Enabled' : 'Disabled'}</Text>
                  <Text size="sm">OTLP: {otlpEndpoint || 'Not configured'}</Text>
                </Stack>

                <Stack gap={4}>
                  <Text fw={600}>Worker</Text>
                  <Text size="sm">Name: {workerName}</Text>
                  <Text size="sm">Address: {workerHost}:{workerPort}</Text>
                </Stack>

                {/* Optional helpers in final step */}
                {(dbType === 'postgres' || dbType === 'mysql' || dbType === 'mariadb' || dbType === 'mongodb') && (
                  <Stack gap="xs">
                    {availableDBs.length > 0 ? (
                      <Select
                        label="Database"
                        placeholder="Select database"
                        searchable
                        data={availableDBs.map((d) => ({ value: d, label: d }))}
                        value={dbname}
                        onChange={(val) => setDbname(val || '')}
                      />
                    ) : (
                      <TextInput label="Database" placeholder="hermod" value={dbname} onChange={(e) => setDbname(e.currentTarget.value)} />
                    )}
                    <Group justify="flex-end">
                      <Button variant="light" onClick={handleFetchDatabases} loading={fetchingDBs} disabled={!dbType}>
                        Fetch databases
                      </Button>
                    </Group>
                    {fetchDBsError && <Text c="red" size="sm">{fetchDBsError}</Text>}
                  </Stack>
                )}

                {/* Database test has been removed from confirmation step; it exists on the Database step */}

                {error && (
                  <Text c="red" size="sm">{error}</Text>
                )}

                <Group justify="space-between">
                  <Button variant="default" onClick={() => setActive(6)}>Back</Button>
                  <Group>
                    <Button onClick={handleFinalSave} loading={saving} disabled={!canDbNext || !canAdminNext}>
                      Confirm & Save
                    </Button>
                  </Group>
                </Group>
              </Stack>
            </Paper>
          </Stepper.Step>
        </Stepper>
        </div>
      </Stack>
    </Container>
  )
}
