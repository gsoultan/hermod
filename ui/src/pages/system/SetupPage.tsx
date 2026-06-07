import { useEffect, useMemo, useState } from 'react'
import { Title, Text, TextInput, Select, Button, Paper, Stack, Container, Group, PasswordInput, Alert, Progress, Checkbox, NumberInput, Stepper, SimpleGrid, ActionIcon, Tooltip } from '@mantine/core'
import { useMutation } from '@tanstack/react-query'
import { IconDatabase, IconUser, IconSettings, IconPlug, IconCheck, IconCopy, IconRefresh, IconSearch, IconEye, IconEyeOff } from '@tabler/icons-react'
import { apiFetch } from '@/api'
import { copyToClipboard, generateStrongPassword } from '@/utils/cryptoUtils'

interface SetupPageProps {
  onConfigured: () => void
}

export function SetupPage({ onConfigured }: SetupPageProps) {
  const [saving, setSaving] = useState(false)
  const [activeStep, setActiveStep] = useState(0)
  const nextStep = () => setActiveStep((current) => (current < 4 ? current + 1 : current))
  const prevStep = () => setActiveStep((current) => (current > 0 ? current - 1 : current))
  
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
  const [passwordVisible, setPasswordVisible] = useState(false)
  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [cryptoMasterKey, setCryptoMasterKey] = useState('')
  const [keyVisible, setKeyVisible] = useState(false)

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
      value === 'pebble' ||
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

    // SQLite/Pebble does not use builder fields
    if (dbType === 'sqlite' || dbType === 'pebble') {
      setUseBuilder(false)
      // Provide a convenience default only after user explicitly selects it
      if (!dbConn) setDbConn(dbType === 'sqlite' ? 'hermod.db' : 'hermod_pebble')
      return
    }
    setUseBuilder(true)
    setPort((p) => p || defaultPort(dbType))
    // Keep previous user/pw/dbname if present
  }, [dbType, cryptoMasterKey, dbConn])

  // Build DSN when using builder (non-SQLite/Pebble)
  useEffect(() => {
    if (!useBuilder || !dbType || dbType === 'sqlite' || dbType === 'pebble') return
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
    if (!useBuilder || dbType === 'sqlite' || dbType === 'pebble') return dbConn
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
    if (!dbType || dbType === 'sqlite' || dbType === 'pebble') return
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
    <Container size="md" py={48}>
      <Stack gap="xl">
        <Stack gap="xs" align="center">
          <Title order={1}>Welcome to Hermod</Title>
          <Text c="dimmed">Complete the configuration below to get started</Text>
        </Stack>

        <Paper withBorder p="xl" radius="md">
          <Stepper active={activeStep} onStepClick={setActiveStep} allowNextStepsSelect={false} size="sm">
            {/* Step 0: Database */}
            <Stepper.Step label="Database" description="Storage" icon={<IconDatabase size={18} />}>
              <Stack gap="md">
                <Title order={3}>Database Configuration</Title>
                <Text size="sm" c="dimmed">Configure the primary database for system configuration and state.</Text>

                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <Select
                    label="Database Type"
                    description="Select the database engine to use."
                    placeholder="Select database type"
                    data={[
                      { value: 'sqlite', label: 'SQLite' },
                      { value: 'pebble', label: 'Pebble' },
                      { value: 'postgres', label: 'PostgreSQL' },
                      { value: 'mysql', label: 'MySQL' },
                      { value: 'mariadb', label: 'MariaDB' },
                      { value: 'mongodb', label: 'MongoDB' },
                    ]}
                    value={dbType}
                    onChange={handleDbTypeChange}
                    required
                  />
                  <PasswordInput
                    label="Crypto Master Key"
                    description="Used to encrypt secrets. Minimum 16 chars."
                    placeholder="Minimum 16 characters"
                    required
                    value={cryptoMasterKey}
                    onChange={(e) => setCryptoMasterKey(e.currentTarget.value)}
                    visible={keyVisible}
                    onVisibilityChange={setKeyVisible}
                    rightSectionWidth={92}
                    rightSection={
                      <Group gap={0} mr={5}>
                        <Tooltip label="Generate Key">
                          <ActionIcon variant="subtle" color="gray" onClick={() => setCryptoMasterKey(generateStrongPassword(32))}>
                            <IconRefresh size={16} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Copy Key">
                          <ActionIcon variant="subtle" color="gray" onClick={() => { if (cryptoMasterKey) copyToClipboard(cryptoMasterKey) }} disabled={!cryptoMasterKey}>
                            <IconCopy size={16} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label={keyVisible ? "Hide Key" : "Show Key"}>
                          <ActionIcon variant="subtle" color="gray" onClick={() => setKeyVisible(!keyVisible)}>
                            {keyVisible ? <IconEyeOff size={16} /> : <IconEye size={16} />}
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    }
                  />
                </SimpleGrid>

                {/* DSN builder vs raw input */}
                {(dbType !== 'sqlite' && dbType !== 'pebble') && useBuilder ? (
                  <Stack gap="sm">
                    <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                      <TextInput label="Host" description="Database server address." placeholder="localhost" value={host} onChange={(e) => setHost(e.currentTarget.value)} />
                      <TextInput label="Port" description="Connection port." placeholder={defaultPort(dbType)} value={port} onChange={(e) => setPort(e.currentTarget.value)} />
                      <TextInput label="User" description="Auth username." placeholder="user" value={user} onChange={(e) => setUser(e.currentTarget.value)} />
                      <TextInput label="Password" type="password" description="Auth password." placeholder="password" value={pw} onChange={(e) => setPw(e.currentTarget.value)} />
                    </SimpleGrid>

                    {(dbType === 'postgres' || dbType === 'mysql' || dbType === 'mariadb' || dbType === 'mongodb') ? (
                      <Stack gap="xs">
                        <Group align="end">
                          <TextInput style={{ flex: 1 }} label="Database" description="Name of the database/schema." placeholder="hermod" value={dbname} onChange={(e) => setDbname(e.currentTarget.value)} />
                          <Button loading={fetchingDBs} onClick={handleFetchDatabases} size="sm" leftSection={<IconSearch size={16} />}>Fetch</Button>
                        </Group>
                        {fetchDBsError && (
                          <Alert color="red" title="Failed to fetch databases">{fetchDBsError}</Alert>
                        )}
                        {availableDBs.length > 0 && (
                          <Select
                            label="Select a database"
                            description="Choose an existing database from the server."
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
                      <TextInput label="Database" description="Database name." placeholder="hermod" value={dbname} onChange={(e) => setDbname(e.currentTarget.value)} />
                    )}
                    <TextInput label="Connection String Preview" description="Auto-generated DSN." value={dbConn} onChange={(e) => setDbConn(e.currentTarget.value)} />
                    <Group justify="flex-end">
                      <Button variant="subtle" size="xs" onClick={() => setUseBuilder(false)}>Use raw connection string</Button>
                    </Group>
                  </Stack>
                ) : (
                  <Stack gap="xs">
                    <TextInput
                      label={(dbType === 'sqlite' || dbType === 'pebble') ? 'DB Path' : 'Connection String'}
                      description={(dbType === 'sqlite' || dbType === 'pebble') ? 'Path to DB file/directory.' : 'Full connection URI.'}
                      placeholder={(dbType === 'sqlite' || dbType === 'pebble') ? (dbType === 'sqlite' ? 'hermod.db' : 'hermod_pebble') : 'postgres://user:pass@localhost:5432/db'}
                      value={dbConn}
                      onChange={(e) => setDbConn(e.currentTarget.value)}
                    />
                    {(dbType !== 'sqlite' && dbType !== 'pebble') && (
                      <Group justify="flex-end">
                        <Button variant="subtle" size="xs" onClick={() => setUseBuilder(true)}>Use DSN builder</Button>
                      </Group>
                    )}
                  </Stack>
                )}

                <Text size="xs" c="dimmed">{dsnHelp}</Text>

                <Checkbox
                  label="Use a separate database for logging"
                  description="Offload logs to a different database to improve performance."
                  checked={useSeparateLogDb}
                  onChange={(e) => setUseSeparateLogDb(e.currentTarget.checked)}
                />

                {useSeparateLogDb && (
                  <Paper withBorder p="md" radius="sm">
                    <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                      <Select
                        label="Logging Database Type"
                        description="Engine for audit and activity logs."
                        placeholder="Select type"
                        data={[
                          { value: 'sqlite', label: 'SQLite' },
                          { value: 'pebble', label: 'Pebble' },
                          { value: 'postgres', label: 'PostgreSQL' },
                          { value: 'mysql', label: 'MySQL' },
                          { value: 'mariadb', label: 'MariaDB' },
                          { value: 'mongodb', label: 'MongoDB' },
                        ]}
                        value={logDbType}
                        onChange={(val) => setLogDbType(val)}
                      />
                      <TextInput
                        label={(logDbType === 'sqlite' || logDbType === 'pebble') ? 'Logging DB Path' : 'Logging Connection String'}
                        description={(logDbType === 'sqlite' || logDbType === 'pebble') ? 'File/directory path for logs.' : 'Connection string for logs.'}
                        placeholder={(logDbType === 'sqlite' || logDbType === 'pebble') ? (logDbType === 'sqlite' ? 'hermod_logs.db' : 'hermod_logs_pebble') : 'postgres://...'}
                        value={logDbConn}
                        onChange={(e) => setLogDbConn(e.currentTarget.value)}
                      />
                    </SimpleGrid>
                  </Paper>
                )}

                {/* Test database connection */}
                <Stack gap="xs">
                  {testDbMutation.isPending && <Text size="sm">Testing connection...</Text>}
                  {testDbMutation.isError && testResult?.error && (
                    <Alert color="red" title="Connection failed">{testResult.error}</Alert>
                  )}
                  {dbTestOk && testResult?.ok && (
                    <Alert color="green" title="Connection successful">Database connection looks good.</Alert>
                  )}
                  <Button variant="outline" onClick={() => testDbMutation.mutate()} disabled={!Boolean(dbType) || !(dbConn.trim().length > 0) || cryptoMasterKey.length < 16}>
                    Test Connection
                  </Button>
                </Stack>
              </Stack>
            </Stepper.Step>

            {/* Step 1: Administrator */}
            <Stepper.Step label="Admin" description="Account" icon={<IconUser size={18} />}>
              <Stack gap="md">
                <Title order={3}>Administrator Account</Title>
                <Text size="sm" c="dimmed">Create the first administrative user for the platform.</Text>
                
                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <TextInput
                    label="Username"
                    description="Administrative login ID."
                    placeholder="admin"
                    required
                    value={username}
                    onChange={(e) => setUsername(e.currentTarget.value)}
                  />
                  <PasswordInput
                    label="Password"
                    description="Secure access password."
                    placeholder="Your password"
                    required
                    value={password}
                    onChange={(e) => setPassword(e.currentTarget.value)}
                    visible={passwordVisible}
                    onVisibilityChange={setPasswordVisible}
                    rightSectionWidth={92}
                    rightSection={
                      <Group gap={0} mr={5}>
                        <Tooltip label="Generate Password">
                          <ActionIcon variant="subtle" color="gray" onClick={() => setPassword(generateStrongPassword())}>
                            <IconRefresh size={16} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label="Copy Password">
                          <ActionIcon variant="subtle" color="gray" onClick={() => { if (password) copyToClipboard(password) }} disabled={!password}>
                            <IconCopy size={16} />
                          </ActionIcon>
                        </Tooltip>
                        <Tooltip label={passwordVisible ? "Hide Password" : "Show Password"}>
                          <ActionIcon variant="subtle" color="gray" onClick={() => setPasswordVisible(!passwordVisible)}>
                            {passwordVisible ? <IconEyeOff size={16} /> : <IconEye size={16} />}
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    }
                  />
                </SimpleGrid>
                
                <Group gap={8} align="center">
                  <Progress value={(pwScore / 5) * 100} color={pwColor} style={{ flex: 1 }} aria-label="Password strength" />
                  <Text size="xs" c={pwColor}>{pwLabel}</Text>
                </Group>

                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <TextInput
                    label="Full Name"
                    description="Display name for the user."
                    placeholder="Administrator"
                    value={fullName}
                    onChange={(e) => setFullName(e.currentTarget.value)}
                  />
                  <TextInput
                    label="Email"
                    description="User contact email."
                    placeholder="admin@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.currentTarget.value)}
                  />
                </SimpleGrid>
              </Stack>
            </Stepper.Step>

            {/* Step 2: Infrastructure */}
            <Stepper.Step label="System" description="Config" icon={<IconSettings size={18} />}>
              <Stack gap="md">
                <Title order={4}>Engine Settings</Title>
                <SimpleGrid cols={3} spacing="md" verticalSpacing="md">
                  <NumberInput label="Max Retries" description="Maximum retry attempts." value={maxRetries} onChange={(val) => setMaxRetries(Number(val))} />
                  <TextInput label="Retry Interval" description="Delay between retries." placeholder="1s" value={retryInterval} onChange={(e) => setRetryInterval(e.currentTarget.value)} />
                  <TextInput label="Reconnect Interval" description="Broker reconnect delay." placeholder="5s" value={reconnectInterval} onChange={(e) => setReconnectInterval(e.currentTarget.value)} />
                </SimpleGrid>

                <Title order={4} mt="xs">Buffer Configuration</Title>
                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <Select
                    label="Buffer Type"
                    description="Message storage mechanism."
                    data={[
                      { value: 'ring_buffer', label: 'Ring Buffer (In-memory)' },
                      { value: 'disk', label: 'Disk Buffer' },
                      { value: 'nats', label: 'NATS' },
                    ]}
                    value={bufferType}
                    onChange={(val) => setBufferType(val || 'ring_buffer')}
                  />
                  <NumberInput label="Buffer Size" description="Max message capacity." value={bufferSize} onChange={(val) => setBufferSize(Number(val))} />
                  <Select
                    label="Compression"
                    description="Buffered data compression."
                    data={[
                      { value: 'none', label: 'None' },
                      { value: 'gzip', label: 'Gzip' },
                      { value: 'zstd', label: 'Zstd' },
                    ]}
                    value={bufferCompression}
                    onChange={(val) => setBufferCompression(val || 'none')}
                  />
                  {bufferType === 'disk' && (
                    <TextInput label="Buffer Path" description="File path for disk buffer." placeholder="/tmp/hermod_buffer" value={bufferPath} onChange={(e) => setBufferPath(e.currentTarget.value)} />
                  )}
                </SimpleGrid>

                <Title order={4} mt="xs">Secrets & State</Title>
                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <Select
                    label="Secrets Manager"
                    description="Provider for credentials."
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
                  <Select
                    label="State Store"
                    description="Engine for workflow state."
                    data={[
                      { value: 'sqlite', label: 'SQLite (Local)' },
                      { value: 'redis', label: 'Redis' },
                      { value: 'etcd', label: 'Etcd' },
                    ]}
                    value={stateStoreType}
                    onChange={(val) => setStateStoreType(val || 'sqlite')}
                  />
                </SimpleGrid>

                <Paper withBorder p="md" radius="sm">
                  <Stack gap="sm">
                    {/* Secrets specific fields */}
                    {(secretsType === 'vault' || secretsType === 'openbao') && (
                      <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                        <TextInput label="Address" description="Vault server URL." placeholder="https://vault:8200" value={vaultAddress} onChange={(e) => setVaultAddress(e.currentTarget.value)} />
                        <PasswordInput label="Token" description="Vault access token." value={vaultToken} onChange={(e) => setVaultToken(e.currentTarget.value)} />
                        <TextInput label="Mount Path" description="KV engine mount." placeholder="secret" value={vaultMount} onChange={(e) => setVaultMount(e.currentTarget.value)} />
                      </SimpleGrid>
                    )}
                    {secretsType === 'aws' && (
                      <TextInput label="AWS Region" description="Target AWS region." placeholder="us-east-1" value={awsRegion} onChange={(e) => setAwsRegion(e.currentTarget.value)} />
                    )}
                    {secretsType === 'azure' && (
                      <TextInput label="Vault URL" description="Azure Vault base URL." placeholder="https://myvault.vault.azure.net/" value={azureVaultUrl} onChange={(e) => setAzureVaultUrl(e.currentTarget.value)} />
                    )}
                    {secretsType === 'env' && (
                      <TextInput label="Env Prefix" description="Prefix for env vars." placeholder="HERMOD_" value={secretsPrefix} onChange={(e) => setSecretsPrefix(e.currentTarget.value)} />
                    )}

                    {/* State store specific fields */}
                    {stateStoreType === 'sqlite' && (
                      <TextInput label="State DB Path" description="Path to state database." placeholder="hermod_state.db" value={stateStorePath} onChange={(e) => setStateStorePath(e.currentTarget.value)} />
                    )}
                    {stateStoreType !== 'sqlite' && (
                      <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                        <TextInput label="Address" description="Connection address." placeholder="localhost:6379" value={stateStoreAddress} onChange={(e) => setStateStoreAddress(e.currentTarget.value)} />
                        <PasswordInput label="Password" description="Auth password." value={stateStorePassword} onChange={(e) => setStateStorePassword(e.currentTarget.value)} />
                        {stateStoreType === 'redis' && (
                          <NumberInput label="DB Index" description="Redis DB number." value={stateStoreDB} onChange={(val) => setStateStoreDB(Number(val))} />
                        )}
                      </SimpleGrid>
                    )}
                    <TextInput label="Prefix" description="Key prefix for state." placeholder="hermod:" value={stateStorePrefix} onChange={(e) => setStateStorePrefix(e.currentTarget.value)} />
                  </Stack>
                </Paper>
              </Stack>
            </Stepper.Step>

            {/* Step 3: Connectors */}
            <Stepper.Step label="Connectors" description="Integration" icon={<IconPlug size={18} />}>
              <Stack gap="md">
                <Title order={4}>SMTP Notifications (Optional)</Title>
                <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                  <TextInput label="SMTP Host" description="Mail server hostname." placeholder="smtp.example.com" value={smtpHost} onChange={(e) => setSmtpHost(e.currentTarget.value)} />
                  <NumberInput label="SMTP Port" description="Mail server port." placeholder="587" value={smtpPort} onChange={setSmtpPort} />
                  <TextInput label="SMTP User" description="Mail auth username." placeholder="user@example.com" value={smtpUser} onChange={(e) => setSmtpUser(e.currentTarget.value)} />
                  <PasswordInput label="SMTP Password" description="Mail auth password." placeholder="password" value={smtpPassword} onChange={(e) => setSmtpPassword(e.currentTarget.value)} />
                  <TextInput label="From Email" description="Sender address." placeholder="hermod@example.com" value={smtpFrom} onChange={(e) => setSmtpFrom(e.currentTarget.value)} />
                  <TextInput label="Admin Email" description="Default alert recipient." placeholder="admin@example.com" value={defaultEmail} onChange={(e) => setDefaultEmail(e.currentTarget.value)} />
                </SimpleGrid>
                <Checkbox label="Use SSL/TLS" description="Enable secure connection." checked={smtpSsl} onChange={(e) => setSmtpSsl(e.currentTarget.checked)} />
                
                <Stack gap="xs">
                  {testSmtpMutation.isPending && <Text size="sm">Sending test email...</Text>}
                  {smtpTest?.status === 'ok' && <Alert color="green">SMTP Test Succeeded</Alert>}
                  {smtpTest?.status === 'error' && <Alert color="red">{smtpTest.error}</Alert>}
                  <Button variant="outline" size="sm" onClick={() => testSmtpMutation.mutate()}>Test SMTP</Button>
                </Stack>

                <Title order={4} mt="md">Auth & Observability</Title>
                <Checkbox label="Enable OIDC" description="Use OpenID Connect for auth." checked={oidcEnabled} onChange={(e) => setOidcEnabled(e.currentTarget.checked)} />
                {oidcEnabled && (
                  <SimpleGrid cols={2} spacing="md" verticalSpacing="md">
                    <TextInput label="Issuer URL" description="Discovery endpoint." value={oidcIssuer} onChange={(e) => setOidcIssuer(e.currentTarget.value)} />
                    <TextInput label="Client ID" description="Application ID." value={oidcClientId} onChange={(e) => setOidcClientId(e.currentTarget.value)} />
                    <PasswordInput label="Client Secret" description="Application secret." value={oidcClientSecret} onChange={(e) => setOidcClientSecret(e.currentTarget.value)} />
                    <TextInput label="Redirect URL" description="Auth callback URL." value={oidcRedirect} onChange={(e) => setOidcRedirect(e.currentTarget.value)} />
                    <TextInput label="Scopes" description="Requested permissions." value={oidcScopes} onChange={(e) => setOidcScopes(e.currentTarget.value)} />
                  </SimpleGrid>
                )}

                <SimpleGrid cols={2} spacing="md" mt="xs" verticalSpacing="md">
                  <TextInput label="OTLP Endpoint" description="Collector address." placeholder="http://localhost:4317" value={otlpEndpoint} onChange={(e) => setOtlpEndpoint(e.currentTarget.value)} />
                  <Select
                    label="Protocol"
                    description="Traces transmission."
                    data={[{ value: 'grpc', label: 'gRPC' }, { value: 'http', label: 'HTTP' }]}
                    value={otlpProtocol}
                    onChange={(val) => setOtlpProtocol(val || 'grpc')}
                  />
                  <TextInput label="Service Name" description="Hermod service name." value={otlpServiceName} onChange={(e) => setOtlpServiceName(e.currentTarget.value)} />
                  <Checkbox label="Insecure OTLP" description="Disable TLS for collector." checked={otlpInsecure} onChange={(e) => setOtlpInsecure(e.currentTarget.checked)} mt={30} />
                </SimpleGrid>
              </Stack>
            </Stepper.Step>

            {/* Step 4: Finalize */}
            <Stepper.Step label="Finalize" description="Deploy" icon={<IconCheck size={18} />}>
              <Stack gap="md">
                <Title order={4}>Initial Worker Registration</Title>
                <SimpleGrid cols={3} spacing="md" verticalSpacing="md">
                  <TextInput label="Worker Name" description="Unique name." value={workerName} onChange={(e) => setWorkerName(e.currentTarget.value)} required />
                  <TextInput label="Host / IP" description="Network address." value={workerHost} onChange={(e) => setWorkerHost(e.currentTarget.value)} required />
                  <NumberInput label="Port" description="Listen port." value={workerPort} onChange={(val) => setWorkerPort(Number(val))} required />
                </SimpleGrid>

                <Title order={4} mt="md">Service Installation Commands</Title>
                <Text size="sm" c="dimmed">Run these commands after saving to install Hermod as a background service.</Text>
                <SimpleGrid cols={1} spacing="xs">
                  <TextInput
                    label="Windows (PowerShell)"
                    readOnly
                    value="hermod.exe -mode standalone -service install"
                    rightSection={<Button variant="subtle" size="xs" onClick={() => copyToClipboard('hermod.exe -mode standalone -service install')}>Copy</Button>}
                    rightSectionWidth={70}
                  />
                  <TextInput
                    label="Linux (systemd)"
                    readOnly
                    value="./hermod -mode standalone -service install"
                    rightSection={<Button variant="subtle" size="xs" onClick={() => copyToClipboard('./hermod -mode standalone -service install')}>Copy</Button>}
                    rightSectionWidth={70}
                  />
                </SimpleGrid>

                {error && <Alert color="red" title="Setup Error" mt="md">{error}</Alert>}
                <Text fw={600} mt="md">Please review all settings. Clicking "Confirm & Save" will apply the configuration and initialize the database.</Text>
              </Stack>
            </Stepper.Step>
          </Stepper>

          <Group justify="center" mt="xl">
            {activeStep > 0 && (
              <Button variant="default" onClick={prevStep}>Back</Button>
            )}
            {activeStep < 4 ? (
              <Button onClick={nextStep} disabled={(activeStep === 0 && !canDbNext) || (activeStep === 1 && !canAdminNext)}>
                Next Step
              </Button>
            ) : (
              <Button size="lg" onClick={handleFinalSave} loading={saving} disabled={!canDbNext || !canAdminNext}>
                Confirm & Save Configuration
              </Button>
            )}
          </Group>
        </Paper>
      </Stack>
    </Container>
  )
}
