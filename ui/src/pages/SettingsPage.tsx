import { IconActivity, IconBraces, IconBrandDiscord, IconBrandSlack, IconBrandTelegram, IconCode, IconDatabase, IconDownload, IconFolder, IconHistory, IconLock, IconMail, IconPlus, IconRefresh, IconServer, IconSettings, IconShieldLock, IconTrash, IconUpload, IconWebhook, IconWorld } from '@tabler/icons-react';
import { Title, Text, Stack, Paper, Select, TextInput, Button, Group, PasswordInput, Badge, Code, NumberInput, Checkbox, Tabs, Table, ActionIcon, Modal, Textarea, SimpleGrid, Card, ThemeIcon, Box } from '@mantine/core'
import { useState, useRef, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../api'
import type { Workspace } from '../types'import { notifications } from '@mantine/notifications'
import { formatDateTime } from '../utils/dateUtils'
import { useDisclosure } from '@mantine/hooks'

export function SettingsPage() {
  const queryClient = useQueryClient();
  const [dbType, setDbType] = useState<string | null>('sqlite')
  const [dbConn, setDbConn] = useState('')
  const [message, setMessage] = useState<{ type: 'success' | 'error', text: string } | null>(null)

  // Secret Manager State
  const [secretType, setSecretType] = useState<string>('env')
  const [vaultAddr, setVaultAddr] = useState('')
  const [vaultToken, setVaultToken] = useState('')
  const [vaultMount, setVaultMount] = useState('')
  const [baoAddr, setBaoAddr] = useState('')
  const [baoToken, setBaoToken] = useState('')
  const [baoMount, setBaoMount] = useState('')
  const [awsRegion, setAwsRegion] = useState('')
  const [azureUrl, setAzureUrl] = useState('')
  const [envPrefix, setEnvPrefix] = useState('')

  // Crypto State
  const [cryptoKey, setCryptoKey] = useState('')

  // State Store State
  const [stateType, setStateType] = useState<string>('sqlite')
  const [statePath, setStatePath] = useState('hermod_state.db')
  const [stateAddr, setStateAddr] = useState('')
  const [statePass, setStatePass] = useState('')
  const [stateDB, setStateDB] = useState<number>(0)
  const [statePrefix, setStatePrefix] = useState('hermod:')

  // OTLP State
  const [otlpEndpoint, setOtlpEndpoint] = useState('')
  const [otlpServiceName, setOtlpServiceName] = useState('hermod')
  const [otlpInsecure, setOtlpInsecure] = useState(false)

  // File Storage State
  const [fileStorageType, setFileStorageType] = useState<string>('local')
  const [localDir, setLocalDir] = useState('uploads')
  const [s3Endpoint, setS3Endpoint] = useState('')
  const [s3Region, setS3Region] = useState('us-east-1')
  const [s3Bucket, setS3Bucket] = useState('')
  const [s3AccessKey, setS3AccessKey] = useState('')
  const [s3SecretKey, setS3SecretKey] = useState('')
  const [s3UseSSL, setS3UseSSL] = useState(true)

  // Workspace Management State
  const [wsModalOpened, { open: openWSModal, close: closeWSModal }] = useDisclosure(false)
  const [newWSName, setNewWSName] = useState('')
  const [newWSDesc, setNewWSDesc] = useState('')
  const [maxWorkflows, setMaxWorkflows] = useState(0)
  const [maxCPU, setMaxCPU] = useState(0)
  const [maxMemory, setMaxMemory] = useState(0)
  const [maxThroughput, setMaxThroughput] = useState(0)

  // Notification Settings State
  const [notifSettings, setNotifSettings] = useState({
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
    webhook_url: '',
    base_url: '',
    logs_retention_days: 30,
  })

  const { data: workspaces } = useQuery<any[]>({
    queryKey: ['workspaces'],
    queryFn: async () => {
      const res = await apiFetch('/api/workspaces')
      return res.json()
    }
  })

  const createWSMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/workspaces', {
        method: 'POST',
        body: JSON.stringify({ 
          name: newWSName, 
          description: newWSDesc,
          max_workflows: maxWorkflows,
          max_cpu: maxCPU,
          max_memory: maxMemory,
          max_throughput: maxThroughput
        })
      })
      if (!res.ok) throw new Error('Failed to create workspace')
      return res.json()
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      notifications.show({ title: 'Success', message: 'Workspace created', color: 'green' })
      closeWSModal()
      setNewWSName('')
      setNewWSDesc('')
      setMaxWorkflows(0)
      setMaxCPU(0)
      setMaxMemory(0)
      setMaxThroughput(0)
    },
    onError: (err: any) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

  const deleteWSMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workspaces/${id}`, { method: 'DELETE' })
      if (!res.ok) throw new Error('Failed to delete workspace')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      notifications.show({ title: 'Success', message: 'Workspace deleted', color: 'green' })
    }
  })

  const { data: fileStorageConfig } = useQuery({
    queryKey: ['file-storage-config'],
    queryFn: async () => {
      const res = await apiFetch('/api/config/storage')
      return res.json()
    }
  })

  useEffect(() => {
    if (fileStorageConfig) {
      setFileStorageType(fileStorageConfig.type || 'local')
      setLocalDir(fileStorageConfig.local_dir || 'uploads')
      if (fileStorageConfig.s3) {
        setS3Endpoint(fileStorageConfig.s3.endpoint || '')
        setS3Region(fileStorageConfig.s3.region || 'us-east-1')
        setS3Bucket(fileStorageConfig.s3.bucket || '')
        setS3AccessKey(fileStorageConfig.s3.access_key_id || '')
        setS3SecretKey(fileStorageConfig.s3.secret_access_key || '')
        setS3UseSSL(fileStorageConfig.s3.use_ssl ?? true)
      }
    }
  }, [fileStorageConfig])

  const saveStorageMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/config/storage', {
        method: 'PUT',
        body: JSON.stringify({
          type: fileStorageType,
          local_dir: localDir,
          s3: {
            endpoint: s3Endpoint,
            region: s3Region,
            bucket: s3Bucket,
            access_key_id: s3AccessKey,
            secret_access_key: s3SecretKey,
            use_ssl: s3UseSSL
          }
        })
      })
      if (!res.ok) throw new Error('Failed to update file storage config')
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'File storage configuration updated', color: 'green' })
      queryClient.invalidateQueries({ queryKey: ['file-storage-config'] })
    },
    onError: (err: any) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

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

  const saveSecretsMutation = useMutation({
    mutationFn: async (config: any) => {
      const response = await apiFetch('/api/config/secrets', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      })
      if (!response.ok) throw new Error('Failed to save secret manager configuration')
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Secret Manager configuration updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveCryptoMutation = useMutation({
    mutationFn: async (key: string) => {
      const response = await apiFetch('/api/config/crypto', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ crypto_master_key: key }),
      })
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update encryption key');
      }
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Encryption key updated and rotated in-memory', color: 'green' })
      setCryptoKey('')
    },
    onError: (err: any) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveStateStoreMutation = useMutation({
    mutationFn: async (config: any) => {
      const response = await apiFetch('/api/config/state', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      })
      if (!response.ok) throw new Error('Failed to save state store configuration')
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Global State Store configuration updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveOtlpMutation = useMutation({
    mutationFn: async (config: any) => {
      const response = await apiFetch('/api/config/observability', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      })
      if (!response.ok) throw new Error('Failed to save OTLP configuration')
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'OTLP configuration updated. Please restart Hermod for changes to take effect.', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveNotifMutation = useMutation({
    mutationFn: async (newSettings: typeof notifSettings) => {
      const res = await apiFetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newSettings)
      })
      if (!res.ok) throw new Error('Failed to save notification settings')
    },
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Notification settings updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ title: 'Error', message: err instanceof Error ? err.message : 'Failed to save settings', color: 'red' })
    }
  })

  const testNotifMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/settings/test', { method: 'POST' })
      if (!res.ok) throw new Error('Failed to send test notification')
      return res.json() as Promise<{ results: { channel: string, status: string, error?: string }[] }>
    },
    onSuccess: (data) => {
      const results = Array.isArray(data?.results) ? data.results : []
      const ok = results.filter(r => r.status === 'ok').map(r => r.channel)
      const errs = results.filter(r => r.status === 'error')
      const skipped = results.filter(r => r.status === 'skipped').map(r => r.channel)
      const lines: string[] = []
      if (ok.length) lines.push(`✅ Sent: ${ok.join(', ')}`)
      if (skipped.length) lines.push(`⏭️ Skipped: ${skipped.join(', ')}`)
      if (errs.length) lines.push(`❌ Failed: ${errs.map(e => `${e.channel}${e.error ? ` (${e.error})` : ''}`).join(', ')}`)
      notifications.show({ title: 'Test Notification', message: lines.join('\n') || 'Done', color: errs.length ? 'red' : 'green' })
    },
    onError: (err) => {
      notifications.show({ title: 'Test Failed', message: err instanceof Error ? err.message : 'Unknown error', color: 'red' })
    }
  })

  const handleSave = () => {
    setMessage(null)
    saveMutation.mutate({ type: dbType, conn: dbConn })
  }

  const handleSaveSecrets = () => {
    const config = {
      type: secretType,
      vault: { address: vaultAddr, token: vaultToken, mount: vaultMount },
      openbao: { address: baoAddr, token: baoToken, mount: baoMount },
      aws: { region: awsRegion },
      azure: { vault_url: azureUrl },
      env: { prefix: envPrefix }
    }
    saveSecretsMutation.mutate(config)
  }

  const handleSaveStateStore = () => {
    const config = {
      type: stateType,
      path: statePath,
      address: stateAddr,
      password: statePass,
      db: stateDB,
      prefix: statePrefix
    }
    saveStateStoreMutation.mutate(config)
  }

  const handleSaveOtlp = () => {
    const config = {
      otlp: {
        endpoint: otlpEndpoint,
        service_name: otlpServiceName,
        insecure: otlpInsecure
      }
    }
    saveOtlpMutation.mutate(config)
  }

  const handleGenerateSDK = async (language: string) => {
    try {
      const res = await apiFetch('/api/infra/generate-sdk', {
        method: 'POST',
        body: JSON.stringify({ language })
      });
      if (res.ok) {
        const blob = await res.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        const filename = language === 'go' ? 'hermod_client.go' : 'hermod-client.ts';
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        window.URL.revokeObjectURL(url);
        notifications.show({ title: 'Success', message: `SDK for ${language} generated`, color: 'green' });
      } else {
        throw new Error('Failed to generate SDK');
      }
    } catch (err: any) {
      notifications.show({ title: 'Error', message: err.message, color: 'red' });
    }
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
    
    // Fetch Secrets Config
    ;(async () => {
      try {
        const res = await apiFetch('/api/config/secrets')
        if (!res.ok) return
        const data = await res.json()
        if (aborted) return
        if (data.type) setSecretType(data.type)
        if (data.vault) {
          setVaultAddr(data.vault.address || '')
          setVaultToken(data.vault.token || '')
          setVaultMount(data.vault.mount || '')
        }
        if (data.openbao) {
          setBaoAddr(data.openbao.address || '')
          setBaoToken(data.openbao.token || '')
          setBaoMount(data.openbao.mount || '')
        }
        if (data.aws) setAwsRegion(data.aws.region || '')
        if (data.azure) setAzureUrl(data.azure.vault_url || '')
        if (data.env) setEnvPrefix(data.env.prefix || '')
      } catch (_) {
        // ignore
      }
    })()

    // Fetch State Store Config
    ;(async () => {
      try {
        const res = await apiFetch('/api/config/state')
        if (!res.ok) return
        const data = await res.json()
        if (aborted) return
        if (data.type) setStateType(data.type)
        if (data.path) setStatePath(data.path)
        if (data.address) setStateAddr(data.address)
        if (data.password) setStatePass(data.password)
        if (data.db) setStateDB(data.db)
        if (data.prefix) setStatePrefix(data.prefix)
      } catch (_) {
        // ignore
      }
    })()

    // Fetch OTLP Config
    ;(async () => {
      try {
        const res = await apiFetch('/api/config/observability')
        if (!res.ok) return
        const data = await res.json()
        if (aborted) return
        if (data.otlp) {
          setOtlpEndpoint(data.otlp.endpoint || '')
          setOtlpServiceName(data.otlp.service_name || 'hermod')
          setOtlpInsecure(!!data.otlp.insecure)
        }
      } catch (_) {
        // ignore
      }
    })()

    // Fetch Notification Settings
    ;(async () => {
      try {
        const res = await apiFetch('/api/settings')
        if (!res.ok) return
        const data = await res.json()
        if (aborted) return
        setNotifSettings(prev => ({ ...prev, ...data }))
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
    <Box pb="xl">
      <Stack gap="xs" mb="xl">
        <Title order={2}>Platform Settings</Title>
        <Text c="dimmed" size="sm">Configure and manage your Hermod instance</Text>
      </Stack>

      <Tabs defaultValue="platform" orientation="vertical" variant="pills" styles={{
        root: { display: 'flex', gap: '2rem' },
        list: { width: 220, flexShrink: 0 },
        panel: { flex: 1, minWidth: 0 }
      }}>
        <Tabs.List>
          <Tabs.Tab value="platform" leftSection={<IconServer size="1.1rem" />}>Platform</Tabs.Tab>
          <Tabs.Tab value="connectivity" leftSection={<IconWorld size="1.1rem" />}>Connectivity</Tabs.Tab>
          <Tabs.Tab value="security" leftSection={<IconShieldLock size="1.1rem" />}>Security</Tabs.Tab>
          <Tabs.Tab value="observability" leftSection={<IconActivity size="1.1rem" />}>Observability</Tabs.Tab>
          <Tabs.Tab value="governance" leftSection={<IconHistory size="1.1rem" />}>Governance</Tabs.Tab>
          <Tabs.Tab value="developer" leftSection={<IconCode size="1.1rem" />}>Developer</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="platform">
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconSettings size="1.2rem" color="blue" />
                <Title order={4}>General Settings</Title>
              </Group>
              <Stack gap="md">
                <TextInput
                  label="Base URL"
                  placeholder="http://hermod.example.com"
                  value={notifSettings.base_url}
                  onChange={(e) => setNotifSettings({ ...notifSettings, base_url: e.target.value })}
                  description="The base URL of the Hermod UI, used for generating links in notifications."
                />
                <NumberInput
                  label="Log Retention (Days)"
                  placeholder="30"
                  min={0}
                  value={notifSettings.logs_retention_days}
                  onChange={(val) => setNotifSettings({ ...notifSettings, logs_retention_days: Number(val) })}
                  description="0 means keep logs forever. Applies globally; workflows may override."
                />
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={() => saveNotifMutation.mutate(notifSettings)} loading={saveNotifMutation.isPending}>Save General Settings</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconDatabase size="1.2rem" color="blue" />
                <Title order={4}>Database Configuration</Title>
              </Group>
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
                  <Button variant="light" size="xs" onClick={handleSave} loading={saveMutation.isPending}>Update Database</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconRefresh size="1.2rem" color="teal" />
                <Title order={4}>Global State Store</Title>
                <Badge variant="dot" color="teal">Enterprise</Badge>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure a distributed state store for consistent stateful transformations across multiple worker instances.
                </Text>
                <Select
                  label="Store Type"
                  placeholder="Select type"
                  data={[
                    { value: 'sqlite', label: 'Local SQLite (Standalone)' },
                    { value: 'redis', label: 'Redis (Distributed)' },
                    { value: 'etcd', label: 'Etcd (Distributed)' },
                  ]}
                  value={stateType}
                  onChange={(val) => setStateType(val || 'sqlite')}
                />
                {stateType === 'sqlite' && (
                  <TextInput
                    label="Database Path"
                    placeholder="hermod_state.db"
                    value={statePath}
                    onChange={(e) => setStatePath(e.currentTarget.value)}
                  />
                )}
                {(stateType === 'redis' || stateType === 'etcd') && (
                  <Stack gap="xs">
                    <TextInput
                      label={stateType === 'redis' ? 'Redis Address' : 'Etcd Endpoints'}
                      placeholder={stateType === 'redis' ? 'localhost:6379' : 'localhost:2379'}
                      value={stateAddr}
                      onChange={(e) => setStateAddr(e.currentTarget.value)}
                    />
                    {stateType === 'redis' && (
                      <Group grow>
                        <PasswordInput
                          label="Password"
                          placeholder="Optional"
                          value={statePass}
                          onChange={(e) => setStatePass(e.currentTarget.value)}
                        />
                        <NumberInput
                          label="DB Index"
                          value={stateDB}
                          onChange={(val) => setStateDB(Number(val || 0))}
                        />
                      </Group>
                    )}
                    <TextInput
                      label="Key Prefix"
                      placeholder="hermod:"
                      value={statePrefix}
                      onChange={(e) => setStatePrefix(e.currentTarget.value)}
                    />
                  </Stack>
                )}
                <Group justify="flex-end">
                  <Button variant="light" color="teal" size="xs" onClick={handleSaveStateStore} loading={saveStateStoreMutation.isPending}>Update State Store</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconFolder size="1.2rem" color="orange" />
                <Title order={4}>File Storage</Title>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure where uploaded files (like CSVs for file sources or email templates) are stored.
                </Text>
                <Select
                  label="Storage Type"
                  placeholder="Select type"
                  data={[
                    { value: 'local', label: 'Local Filesystem' },
                    { value: 's3', label: 'S3 Compatible' },
                  ]}
                  value={fileStorageType}
                  onChange={(val) => setFileStorageType(val || 'local')}
                />
                {fileStorageType === 'local' && (
                  <TextInput
                    label="Local Directory"
                    placeholder="uploads"
                    value={localDir}
                    onChange={(e) => setLocalDir(e.currentTarget.value)}
                    description="Relative or absolute path to store files locally on the API server."
                  />
                )}
                {fileStorageType === 's3' && (
                  <Stack gap="xs">
                    <TextInput
                      label="Endpoint"
                      placeholder="https://minio.example.com"
                      value={s3Endpoint}
                      onChange={(e) => setS3Endpoint(e.currentTarget.value)}
                      description="Leave empty for AWS S3. Use URL for MinIO, Wasabi, etc."
                    />
                    <Group grow>
                      <TextInput
                        label="Region"
                        placeholder="us-east-1"
                        value={s3Region}
                        onChange={(e) => setS3Region(e.currentTarget.value)}
                      />
                      <TextInput
                        label="Bucket"
                        placeholder="hermod-uploads"
                        value={s3Bucket}
                        onChange={(e) => setS3Bucket(e.currentTarget.value)}
                      />
                    </Group>
                    <Group grow>
                      <TextInput
                        label="Access Key ID"
                        placeholder="Required"
                        value={s3AccessKey}
                        onChange={(e) => setS3AccessKey(e.currentTarget.value)}
                      />
                      <PasswordInput
                        label="Secret Access Key"
                        placeholder="Required"
                        value={s3SecretKey}
                        onChange={(e) => setS3SecretKey(e.currentTarget.value)}
                      />
                    </Group>
                    <Checkbox
                      label="Use SSL"
                      checked={s3UseSSL}
                      onChange={(e) => setS3UseSSL(e.currentTarget.checked)}
                    />
                  </Stack>
                )}
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={() => saveStorageMutation.mutate()} loading={saveStorageMutation.isPending}>Update File Storage</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconDownload size="1.2rem" color="orange" />
                <Title order={4}>Maintenance & Backup</Title>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Export your entire configuration including Sources, Sinks, Workflows, and Transformations.
                </Text>
                <Group>
                  <Button variant="outline" size="sm" leftSection={<IconDownload size="1rem" />} onClick={handleExport}>
                    Export Config
                  </Button>
                  <Button variant="outline" color="orange" size="sm" leftSection={<IconUpload size="1rem" />} onClick={() => fileInputRef.current?.click()}>
                    Import Config
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
        </Tabs.Panel>

        <Tabs.Panel value="connectivity">
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
        </Tabs.Panel>

        <Tabs.Panel value="security">
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconShieldLock size="1.2rem" color="blue" />
                <Title order={4}>Enterprise Secret Management</Title>
                <Badge variant="dot" color="blue">Enterprise</Badge>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure external secret managers to securely resolve sensitive configuration values (marked with <Code>secret:</Code> prefix).
                </Text>
                <Select
                  label="Manager Type"
                  placeholder="Select manager"
                  data={[
                    { value: 'env', label: 'Environment Variables (Default)' },
                    { value: 'vault', label: 'HashiCorp Vault' },
                    { value: 'openbao', label: 'OpenBao' },
                    { value: 'aws', label: 'AWS Secrets Manager' },
                    { value: 'azure', label: 'Azure Key Vault' },
                  ]}
                  value={secretType}
                  onChange={(val) => setSecretType(val || 'env')}
                />
                {secretType === 'env' && (
                  <TextInput
                    label="Environment Prefix"
                    placeholder="e.g. HERMOD_SECRET_"
                    value={envPrefix}
                    onChange={(e) => setEnvPrefix(e.currentTarget.value)}
                    description="Only env vars starting with this prefix will be searched."
                  />
                )}
                {secretType === 'vault' && (
                  <Stack gap="xs">
                    <TextInput label="Vault Address" value={vaultAddr} onChange={(e) => setVaultAddr(e.currentTarget.value)} />
                    <PasswordInput label="Vault Token" value={vaultToken} onChange={(e) => setVaultToken(e.currentTarget.value)} />
                    <TextInput label="KV Mount Path" value={vaultMount} onChange={(e) => setVaultMount(e.currentTarget.value)} />
                  </Stack>
                )}
                {secretType === 'openbao' && (
                  <Stack gap="xs">
                    <TextInput label="OpenBao Address" value={baoAddr} onChange={(e) => setBaoAddr(e.currentTarget.value)} />
                    <PasswordInput label="OpenBao Token" value={baoToken} onChange={(e) => setBaoToken(e.currentTarget.value)} />
                    <TextInput label="KV Mount Path" value={baoMount} onChange={(e) => setBaoMount(e.currentTarget.value)} />
                  </Stack>
                )}
                {secretType === 'aws' && (
                  <TextInput label="AWS Region" value={awsRegion} onChange={(e) => setAwsRegion(e.currentTarget.value)} />
                )}
                {secretType === 'azure' && (
                  <TextInput label="Key Vault URL" value={azureUrl} onChange={(e) => setAzureUrl(e.currentTarget.value)} />
                )}
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSaveSecrets} loading={saveSecretsMutation.isPending}>Update Secret Manager</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconLock size="1.2rem" color="red" />
                <Title order={4}>Encryption Master Key</Title>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  The Master Key is used to encrypt sensitive data in the database (e.g. SMTP passwords, API tokens).
                  Rotating the key here updates the configuration and applies the new key for future encryptions.
                </Text>
                <PasswordInput
                  label="New Master Key"
                  placeholder="At least 16 characters"
                  value={cryptoKey}
                  onChange={(e) => setCryptoKey(e.currentTarget.value)}
                />
                <Group justify="flex-end">
                  <Button 
                    variant="light" 
                    color="red" 
                    size="xs" 
                    onClick={() => saveCryptoMutation.mutate(cryptoKey)}
                    loading={saveCryptoMutation.isPending}
                    disabled={cryptoKey.length < 16}
                  >
                    Update & Rotate Key
                  </Button>
                </Group>
              </Stack>
            </Paper>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="observability">
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconActivity size="1.2rem" color="blue" />
                <Title order={4}>OpenTelemetry (OTLP) Export</Title>
                <Badge variant="dot" color="blue">Enterprise</Badge>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Export internal metrics and message traces to an OTLP-compatible backend.
                </Text>
                <TextInput
                  label="OTLP Endpoint"
                  placeholder="http://localhost:4318"
                  value={otlpEndpoint}
                  onChange={(e) => setOtlpEndpoint(e.currentTarget.value)}
                  description="HTTP endpoint for OTLP collector. Use 4318 for HTTP/JSON."
                />
                <TextInput
                  label="Service Name"
                  placeholder="hermod"
                  value={otlpServiceName}
                  onChange={(e) => setOtlpServiceName(e.currentTarget.value)}
                />
                <Checkbox
                  label="Insecure (Disable TLS)"
                  checked={otlpInsecure}
                  onChange={(e: any) => setOtlpInsecure(e.currentTarget.checked)}
                />
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSaveOtlp} loading={saveOtlpMutation.isPending}>Update OTLP Config</Button>
                </Group>
              </Stack>
            </Paper>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="governance">
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group justify="space-between" mb="md">
                <Stack gap={0}>
                  <Group gap="xs">
                    <IconFolder size="1.2rem" color="blue" />
                    <Title order={4}>Workspaces</Title>
                  </Group>
                  <Text size="sm" c="dimmed">Organize workflows and secrets into logical containers</Text>
                </Stack>
                <Button size="xs" leftSection={<IconPlus size="1rem" />} onClick={openWSModal}>Create Workspace</Button>
              </Group>

              <Table verticalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Description</Table.Th>
                    <Table.Th>Created</Table.Th>
                    <Table.Th style={{ width: 80 }}>Actions</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {!Array.isArray(workspaces) || workspaces.length === 0 ? (
                    <Table.Tr>
                      <Table.Td colSpan={4}>
                        <Text ta="center" py="xl" c="dimmed">No workspaces created yet</Text>
                      </Table.Td>
                    </Table.Tr>
                  ) : (
                    workspaces.map((ws: Workspace) => (
                      <Table.Tr key={ws.id}>
                        <Table.Td>
                          <Group gap="xs">
                            <ThemeIcon variant="light" color="blue" size="sm">
                              <IconFolder size="0.8rem" />
                            </ThemeIcon>
                            <Text fw={500}>{ws.name}</Text>
                          </Group>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{ws.description || '-'}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{formatDateTime(ws.created_at)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <ActionIcon aria-label="Delete workspace" color="red" variant="subtle" onClick={() => deleteWSMutation.mutate(ws.id)}>
                            <IconTrash size="1rem" />
                          </ActionIcon>
                        </Table.Td>
                      </Table.Tr>
                    ))
                  )}
                </Table.Tbody>
              </Table>
            </Paper>
          </Stack>
        </Tabs.Panel>

        <Tabs.Panel value="developer">
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconCode size="1.2rem" color="blue" />
                <Title order={4}>Client SDK Generation</Title>
              </Group>
              <Text size="sm" c="dimmed" mb="lg">
                Generate lightweight client libraries to easily publish messages to Hermod from your applications.
              </Text>
              
              <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                <Card withBorder padding="lg" radius="md">
                  <Stack align="center" gap="sm">
                    <ThemeIcon size="xl" radius="md" color="blue" variant="light">
                      <IconCode size="1.5rem" />
                    </ThemeIcon>
                    <Text fw={700}>Go Client</Text>
                    <Text size="xs" c="dimmed" ta="center">Native Go library using standard net/http.</Text>
                    <Button variant="light" size="sm" onClick={() => handleGenerateSDK('go')}>Download .go</Button>
                  </Stack>
                </Card>

                <Card withBorder padding="lg" radius="md">
                  <Stack align="center" gap="sm">
                    <ThemeIcon size="xl" radius="md" color="blue" variant="light">
                      <IconBraces size="1.5rem" />
                    </ThemeIcon>
                    <Text fw={700}>TypeScript Client</Text>
                    <Text size="xs" c="dimmed" ta="center">Modern TS client using fetch API.</Text>
                    <Button variant="light" size="sm" onClick={() => handleGenerateSDK('typescript')}>Download .ts</Button>
                  </Stack>
                </Card>
              </SimpleGrid>
            </Paper>
          </Stack>
        </Tabs.Panel>
      </Tabs>

      <Modal opened={wsModalOpened} onClose={closeWSModal} title="Create New Workspace">
        <Stack>
          <TextInput
            label="Workspace Name"
            placeholder="e.g. Production, Marketing"
            required
            value={newWSName}
            onChange={(e) => setNewWSName(e.currentTarget.value)}
          />
          <Textarea
            label="Description"
            placeholder="What is this workspace for?"
            value={newWSDesc}
            onChange={(e) => setNewWSDesc(e.currentTarget.value)}
          />
          <SimpleGrid cols={2}>
            <NumberInput
              label="Max Workflows"
              description="0 for unlimited"
              min={0}
              value={maxWorkflows}
              onChange={(val) => setMaxWorkflows(Number(val))}
            />
            <NumberInput
              label="Max Throughput (msg/s)"
              description="0 for unlimited"
              min={0}
              value={maxThroughput}
              onChange={(val) => setMaxThroughput(Number(val))}
            />
            <NumberInput
              label="Max CPU (Cores)"
              description="0 for unlimited"
              min={0}
              step={0.1}
              decimalScale={1}
              value={maxCPU}
              onChange={(val) => setMaxCPU(Number(val))}
            />
            <NumberInput
              label="Max Memory (MB)"
              description="0 for unlimited"
              min={0}
              value={maxMemory}
              onChange={(val) => setMaxMemory(Number(val))}
            />
          </SimpleGrid>
          <Group justify="flex-end" mt="md">
            <Button variant="outline" color="gray" onClick={closeWSModal}>Cancel</Button>
            <Button onClick={() => createWSMutation.mutate()} loading={createWSMutation.isPending}>Create Workspace</Button>
          </Group>
        </Stack>
      </Modal>
    </Box>
  )
}


