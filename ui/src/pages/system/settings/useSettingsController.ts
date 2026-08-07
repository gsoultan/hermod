import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useUnsavedChanges, isDirty } from '@/hooks/useUnsavedChanges';
import { apiFetch } from '@/api';
import { notifications } from '@mantine/notifications';
import { useDisclosure } from '@mantine/hooks';


/**
 * All Settings state, queries and mutations in one place.
 *
 * SettingsPage was a single 1,294-line component holding 41 useState hooks and
 * 11 mutations across six tabs. The setup is unchanged here — only its location
 * — so behaviour is identical while each tab becomes independently readable.
 */

export function useSettingsController() {
  const queryClient = useQueryClient();
  const [dbType, setDbType] = useState<string | null>('sqlite')
  const [dbConn, setDbConn] = useState('')
  const [logDbType, setLogDbType] = useState<string | null>(null)
  const [logDbConn, setLogDbConn] = useState('')
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

  // Settings holds dozens of independent fields across six tabs with no single
  // save, so leaving the page used to discard everything silently. Snapshot the
  // whole form and compare it with the last saved state to know when that has
  // actually happened. Transient UI-only fields (message, new workspace inputs)
  // are excluded so they never trigger a false warning.
  const formSnapshot = useMemo(
    () => ({ dbType, dbConn, logDbType, logDbConn, secretType, vaultAddr, vaultToken, vaultMount, baoAddr, baoToken, baoMount, awsRegion, azureUrl, envPrefix, cryptoKey, stateType, statePath, stateAddr, statePass, stateDB, statePrefix, otlpEndpoint, otlpServiceName, otlpInsecure, fileStorageType, localDir, maxWorkflows, maxCPU, maxMemory, maxThroughput, notifSettings }),
    [dbType, dbConn, logDbType, logDbConn, secretType, vaultAddr, vaultToken, vaultMount, baoAddr, baoToken, baoMount, awsRegion, azureUrl, envPrefix, cryptoKey, stateType, statePath, stateAddr, statePass, stateDB, statePrefix, otlpEndpoint, otlpServiceName, otlpInsecure, fileStorageType, localDir, maxWorkflows, maxCPU, maxMemory, maxThroughput, notifSettings],
  );
  const formSnapshotRef = useRef(formSnapshot);
  formSnapshotRef.current = formSnapshot;
  const savedSnapshot = useRef<string | null>(null);
  useEffect(() => {
    // Establish the baseline once the loaded config has populated the fields.
    if (savedSnapshot.current === null) savedSnapshot.current = JSON.stringify(formSnapshot);
  }, [formSnapshot]);
  const hasUnsavedChanges = savedSnapshot.current !== null && isDirty(formSnapshot, JSON.parse(savedSnapshot.current));
  useUnsavedChanges(hasUnsavedChanges);

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
        }),
        silent: true,
      })
      if (!res.ok) throw new Error('Failed to create workspace')
      return res.json()
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      notifications.show({ id: 'ws-created', title: 'Success', message: 'Workspace created', color: 'green' })
      closeWSModal()
      setNewWSName('')
      setNewWSDesc('')
      setMaxWorkflows(0)
      setMaxCPU(0)
      setMaxMemory(0)
      setMaxThroughput(0)
    },
    onError: (err: any) => {
      notifications.show({ id: 'ws-create-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const deleteWSMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`/api/workspaces/${id}`, { method: 'DELETE', silent: true })
      if (!res.ok) throw new Error('Failed to delete workspace')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      notifications.show({ id: 'ws-deleted', title: 'Success', message: 'Workspace deleted', color: 'green' })
    },
    onError: (err: any) => {
      notifications.show({ id: 'ws-delete-error', title: 'Error', message: err.message, color: 'red' })
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
        }),
        silent: true,
      })
      if (!res.ok) throw new Error('Failed to update file storage config')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'storage-saved', title: 'Success', message: 'File storage configuration updated', color: 'green' })
      queryClient.invalidateQueries({ queryKey: ['file-storage-config'] })
    },
    onError: (err: any) => {
      notifications.show({ id: 'storage-save-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveMutation = useMutation({
    mutationFn: async (config: { type: string | null, conn: string, log_type?: string | null, log_conn?: string }) => {
      const response = await apiFetch('/api/config/database', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(config),
        silent: true,
      })

      if (!response.ok) {
        throw new Error('Failed to save configuration')
      }
      return response.json()
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
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
        silent: true,
      })
      if (!response.ok) throw new Error('Failed to save secret manager configuration')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'secrets-saved', title: 'Success', message: 'Secret Manager configuration updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ id: 'secrets-save-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveCryptoMutation = useMutation({
    mutationFn: async (key: string) => {
      const response = await apiFetch('/api/config/crypto', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ crypto_master_key: key }),
        silent: true,
      })
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to update encryption key');
      }
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'crypto-saved', title: 'Success', message: 'Encryption key updated and rotated in-memory', color: 'green' })
      setCryptoKey('')
    },
    onError: (err: any) => {
      notifications.show({ id: 'crypto-save-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveStateStoreMutation = useMutation({
    mutationFn: async (config: any) => {
      const response = await apiFetch('/api/config/state', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
        silent: true,
      })
      if (!response.ok) throw new Error('Failed to save state store configuration')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'state-store-saved', title: 'Success', message: 'Global State Store configuration updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ id: 'state-store-save-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveOtlpMutation = useMutation({
    mutationFn: async (config: any) => {
      const response = await apiFetch('/api/config/observability', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
        silent: true,
      })
      if (!response.ok) throw new Error('Failed to save OTLP configuration')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'otlp-saved', title: 'Success', message: 'OTLP configuration updated. Please restart Hermod for changes to take effect.', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ id: 'otlp-save-error', title: 'Error', message: err.message, color: 'red' })
    }
  })

  const saveNotifMutation = useMutation({
    mutationFn: async (newSettings: typeof notifSettings) => {
      const res = await apiFetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newSettings),
        silent: true,
      })
      if (!res.ok) throw new Error('Failed to save notification settings')
    },
    onSuccess: () => {
      savedSnapshot.current = JSON.stringify(formSnapshotRef.current);
      notifications.show({ id: 'notif-settings-saved', title: 'Success', message: 'Notification settings updated', color: 'green' })
    },
    onError: (err) => {
      notifications.show({ id: 'notif-settings-save-error', title: 'Error', message: err instanceof Error ? err.message : 'Failed to save settings', color: 'red' })
    }
  })

  const testNotifMutation = useMutation({
    mutationFn: async () => {
      const res = await apiFetch('/api/settings/test', { method: 'POST', silent: true })
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
    saveMutation.mutate({ 
      type: dbType, 
      conn: dbConn,
      log_type: logDbType,
      log_conn: logDbConn
    })
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
        if (data.log_type) setLogDbType(data.log_type)
        if (typeof data.log_conn === 'string') setLogDbConn(data.log_conn)
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


  return {
    awsRegion, azureUrl, baoAddr, baoMount, baoToken, closeWSModal, createWSMutation, cryptoKey, dbConn, dbType, deleteWSMutation, envPrefix, fileInputRef, fileStorageConfig, fileStorageType, formSnapshot, formSnapshotRef, handleExport, handleGenerateSDK, handleImport, handleSave, handleSaveOtlp, handleSaveSecrets, handleSaveStateStore, hasUnsavedChanges, localDir, logDbConn, logDbType, maxCPU, maxMemory, maxThroughput, maxWorkflows, message, newWSDesc, newWSName, notifSettings, openWSModal, otlpEndpoint, otlpInsecure, otlpServiceName, queryClient, s3AccessKey, s3Bucket, s3Endpoint, s3Region, s3SecretKey, s3UseSSL, saveCryptoMutation, saveMutation, saveNotifMutation, saveOtlpMutation, saveSecretsMutation, saveStateStoreMutation, saveStorageMutation, savedSnapshot, secretType, setAwsRegion, setAzureUrl, setBaoAddr, setBaoMount, setBaoToken, setCryptoKey, setDbConn, setDbType, setEnvPrefix, setFileStorageType, setLocalDir, setLogDbConn, setLogDbType, setMaxCPU, setMaxMemory, setMaxThroughput, setMaxWorkflows, setMessage, setNewWSDesc, setNewWSName, setNotifSettings, setOtlpEndpoint, setOtlpInsecure, setOtlpServiceName, setS3AccessKey, setS3Bucket, setS3Endpoint, setS3Region, setS3SecretKey, setS3UseSSL, setSecretType, setStateAddr, setStateDB, setStatePass, setStatePath, setStatePrefix, setStateType, setVaultAddr, setVaultMount, setVaultToken, stateAddr, stateDB, statePass, statePath, statePrefix, stateType, testNotifMutation, vaultAddr, vaultMount, vaultToken, workspaces, wsModalOpened,
  };
}

export type SettingsController = ReturnType<typeof useSettingsController>;
