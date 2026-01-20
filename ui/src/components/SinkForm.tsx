import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Paper, Text, Grid, Title, Code, List, Tabs, Textarea, ActionIcon, Tooltip, Modal, Card, ScrollArea, Badge, Autocomplete } from '@mantine/core';
import { IconCheck, IconAlertCircle, IconInfoCircle, IconTemplate, IconLink, IconCloud, IconPlayerPlay, IconAt, IconSettings, IconBraces, IconRefresh } from '@tabler/icons-react';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';

const API_BASE = '/api';

const SINK_TYPES = [
  'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'file', 'kafka', 'pulsar', 'kinesis', 'pubsub', 'fcm', 'smtp', 'telegram', 'http', 'stdout',
  'postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'
];


interface SinkFormProps {
  initialData?: any;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
}

export function SinkForm({ initialData, isEditing = false, embedded = false, onSave, vhost, workerID }: SinkFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const [previewModalOpen, setPreviewModalOpen] = useState(false);
  const [previewResult, setPreviewResult] = useState<{ rendered: string, is_html: boolean } | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [validateEmailLoading, setValidateEmailLoading] = useState(false);
  const [sink, setSink] = useState<any>({ 
    name: '', 
    type: 'stdout', 
    vhost: '', 
    worker_id: '',
    config: { format: 'json', max_retries: '3', retry_interval: '1s' }
  });

  const [tables, setTables] = useState<string[]>([]);
  const [loadingTables, setLoadingTables] = useState(false);
  const [tablesError, setTablesError] = useState<string | null>(null);

  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);

  const fetchDatabases = async () => {
    setIsFetchingDBs(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/databases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
      });
      const dbs = await res.json();
      if (!res.ok) throw new Error(dbs.error || 'Failed to discover databases');
      setDiscoveredDatabases(dbs || []);
    } catch (err: any) {
      setTestResult({ status: 'error', message: err.message });
    } finally {
      setIsFetchingDBs(false);
    }
  };

  const discoverTables = async () => {
    setLoadingTables(true);
    setTablesError(null);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to discover tables');
      setTables(data || []);
    } catch (err: any) {
      setTablesError(err.message);
    } finally {
      setLoadingTables(false);
    }
  };

  useEffect(() => {
    const dbTypes = ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'];
    if (dbTypes.includes(sink.type) && sink.config.host || sink.config.connection_string || sink.config.uri || sink.config.db_path) {
      // Small delay to allow user to finish typing
      const timer = setTimeout(() => {
         discoverTables();
      }, 1000);
      return () => clearTimeout(timer);
    }
  }, [sink.type, sink.config.host, sink.config.connection_string, sink.config.uri, sink.config.db_path]);

  useEffect(() => {
    if (initialData) {
      setSink((prev: any) => {
        // Only update if the ID changed or we are initializing a new sink
        if (prev.id !== initialData.id || (prev.name === '' && !prev.id)) {
          return {
            ...prev,
            ...initialData,
            config: {
              ...(prev.config || {}),
              ...(initialData.config || {})
            }
          };
        }
        return prev;
      });
    }
  }, [initialData]);

  useEffect(() => {
    if (sink.type === 'stdout') {
      setTestResult({ status: 'ok', message: 'Stdout is always active' });
    }
  }, [sink.type]);

  const { data: vhostsResponse } = useSuspenseQuery<any>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const { data: workersResponse } = useSuspenseQuery<any>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const vhosts = vhostsResponse?.data || [];
  const workers = workersResponse?.data || [];

  const availableVHostsList = role === 'Administrator' 
    ? (vhosts || []).map((v: any) => v.name)
    : availableVHosts;

  const testMutation = useMutation({
    mutationFn: async (s: any) => {
      const res = await apiFetch(`${API_BASE}/sinks/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Connection failed');
      return data;
    },
    onSuccess: () => {
      setTestResult({ status: 'ok', message: 'Connection successful!' });
    },
    onError: (error: Error) => {
      setTestResult({ status: 'error', message: error.message });
    }
  });

  const submitMutation = useMutation({
    mutationFn: async (s: any) => {
      const res = await apiFetch(`${API_BASE}/sinks${isEditing ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `Failed to ${isEditing ? 'update' : 'create'} sink`);
      }
      return res.json();
    },
    onSuccess: (data) => {
      if (embedded && onSave) {
        onSave(data);
      } else {
        navigate({ to: '/sinks' });
      }
    },
    onError: (error: Error) => {
      setTestResult({ status: 'error', message: error.message });
    }
  });

  useEffect(() => {
    if (embedded) {
      if (vhost) setSink((prev: any) => ({ ...prev, vhost }));
      if (workerID) setSink((prev: any) => ({ ...prev, worker_id: workerID }));
    }
  }, [embedded, vhost, workerID]);

  const handleSinkChange = (updates: any) => {
    setSink({ ...sink, ...updates });
    setTestResult(null);
  };

  const updateConfig = (key: string, value: string) => {
    setSink({
      ...sink,
      config: { ...sink.config, [key]: value }
    });
    setTestResult(null);
  };


  const handlePreview = async () => {
    if (!sink.config?.template && sink.config?.template_source === 'inline') return;
    setPreviewLoading(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/smtp/preview`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          template: sink.config?.template,
          data: {
            id: "123",
            name: "John Doe",
            order_id: "ORD-999",
            status: "Shipped"
          }
        }),
      });
      const data = await res.json();
      if (res.ok) {
        setPreviewResult(data);
        setPreviewModalOpen(true);
      } else {
        setTestResult({ status: 'error', message: data.error || 'Failed to preview template' });
      }
    } catch (error: any) {
      setTestResult({ status: 'error', message: error.message });
    } finally {
      setPreviewLoading(false);
    }
  };

  const handleValidateEmail = async (email: string) => {
    if (!email) return;
    setValidateEmailLoading(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/smtp/validate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          email,
          host: sink.config?.host,
          port: parseInt(sink.config?.port || '0'),
          username: sink.config?.username,
          password: sink.config?.password,
          ssl: sink.config?.ssl === 'true'
        }),
      });
      const data = await res.json();
      if (res.ok) {
        setTestResult({ status: 'ok', message: 'Email address is valid and reachable!' });
      } else {
        setTestResult({ status: 'error', message: data.error || 'Email validation failed' });
      }
    } catch (error: any) {
      setTestResult({ status: 'error', message: error.message });
    } finally {
      setValidateEmailLoading(false);
    }
  };

  const renderConfigFields = () => {
    const type = sink.type;
    const config = sink.config || {};

    switch (type) {
      case 'nats':
        return (
          <>
            <TextInput label="URL" placeholder="nats://localhost:4222" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Subject" placeholder="hermod.data" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
            <Group grow>
              <TextInput label="Username" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
              <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            </Group>
            <TextInput label="Token" placeholder="Optional" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
          </>
        );
      case 'rabbitmq':
        return (
          <>
            <TextInput label="URL" placeholder="rabbitmq-stream://guest:guest@localhost:5552" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Stream Name" placeholder="hermod-stream" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
          </>
        );
      case 'rabbitmq_queue':
        return (
          <>
            <TextInput label="URL" placeholder="amqp://guest:guest@localhost:5672" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Queue Name" placeholder="hermod-queue" value={config.queue_name || ''} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
          </>
        );
      case 'redis':
        return (
          <>
            <TextInput label="Address" placeholder="localhost:6379" value={config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
            <TextInput label="Password" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            <TextInput label="Stream" placeholder="hermod-stream" value={config.stream || ''} onChange={(e) => updateConfig('stream', e.target.value)} required />
          </>
        );
      case 'file':
        return (
          <TextInput label="Filename" placeholder="/tmp/hermod.log" value={config.filename || ''} onChange={(e) => updateConfig('filename', e.target.value)} required />
        );
      case 'kafka':
        return (
          <>
            <TextInput label="Brokers" placeholder="localhost:9092,localhost:9093" value={config.brokers || ''} onChange={(e) => updateConfig('brokers', e.target.value)} required />
            <TextInput label="Topic" placeholder="hermod-topic" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
            <Group grow>
              <TextInput label="Username (SASL)" placeholder="Optional" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
              <TextInput label="Password (SASL)" type="password" placeholder="Optional" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            </Group>
          </>
        );
      case 'pulsar':
        return (
          <>
            <TextInput label="URL" placeholder="pulsar://localhost:6650" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Topic" placeholder="persistent://public/default/hermod" value={config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
            <TextInput label="Token" placeholder="Optional" value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
          </>
        );
      case 'kinesis':
        return (
          <>
            <TextInput label="Region" placeholder="us-east-1" value={config.region || ''} onChange={(e) => updateConfig('region', e.target.value)} required />
            <TextInput label="Stream Name" placeholder="hermod-stream" value={config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
            <Group grow>
              <TextInput label="Access Key" placeholder="Optional" value={config.access_key || ''} onChange={(e) => updateConfig('access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" placeholder="Optional" value={config.secret_key || ''} onChange={(e) => updateConfig('secret_key', e.target.value)} />
            </Group>
          </>
        );
      case 'pubsub':
        return (
          <>
            <TextInput label="Project ID" placeholder="my-project" value={config.project_id || ''} onChange={(e) => updateConfig('project_id', e.target.value)} required />
            <TextInput label="Topic ID" placeholder="hermod-topic" value={config.topic_id || ''} onChange={(e) => updateConfig('topic_id', e.target.value)} required />
            <TextInput label="Credentials JSON" placeholder="Optional service account JSON content" value={config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} />
          </>
        );
      case 'fcm':
        return (
          <TextInput label="Credentials JSON" placeholder="Service account JSON content" value={config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} required />
        );
      case 'smtp':
        return (
          <>
            <Group grow>
              <TextInput label="Host" placeholder="smtp.example.com" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
              <TextInput label="Port" placeholder="587" value={config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} required />
            </Group>
            <Group grow>
              <TextInput label="Username" placeholder="user@example.com" value={config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} required />
              <TextInput label="Password" type="password" placeholder="password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} required />
            </Group>
            <Select 
                label="SSL" 
                placeholder="Select SSL" 
                data={[{ value: 'true', label: 'True' }, { value: 'false', label: 'False' }]} 
                value={config.ssl || 'false'} 
                onChange={(value) => updateConfig('ssl', value || 'false')} 
                required 
            />
            <TextInput 
              label="From" 
              placeholder="sender@example.com" 
              value={config.from || ''} 
              onChange={(e) => updateConfig('from', e.target.value)} 
              required 
              rightSection={
                <Tooltip label="Validate email address">
                  <ActionIcon onClick={() => handleValidateEmail(config.from)} loading={validateEmailLoading} variant="subtle" color="blue">
                    <IconAt size="1rem" />
                  </ActionIcon>
                </Tooltip>
              }
            />
            <TextInput label="To" placeholder="recipient1@example.com, recipient2@example.com" value={config.to || ''} onChange={(e) => updateConfig('to', e.target.value)} required />
            <TextInput label="Subject" placeholder="CDC Alert" value={config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
            
            <Divider label="Template Settings" labelPosition="center" my="md" />

            <Tabs defaultValue={config.template_source || 'inline'} onChange={(value) => updateConfig('template_source', value || 'inline')}>
              <Tabs.List grow>
                <Tabs.Tab value="inline" leftSection={<IconTemplate size="1rem" />}>Inline</Tabs.Tab>
                <Tabs.Tab value="url" leftSection={<IconLink size="1rem" />}>URL</Tabs.Tab>
                <Tabs.Tab value="s3" leftSection={<IconCloud size="1rem" />}>Amazon S3</Tabs.Tab>
              </Tabs.List>

              <Tabs.Panel value="inline" pt="md">
                <Stack gap="xs">
                  <Textarea 
                    label="Template" 
                    placeholder="Hello {{.name}}, your order #{{.order_id}} has been processed." 
                    value={config.template || ''} 
                    onChange={(e) => updateConfig('template', e.target.value)} 
                    autosize
                    minRows={12}
                    description="Supports Go template syntax. Both HTML and Plain Text are automatically detected."
                  />
                  <Button 
                    variant="light" 
                    leftSection={<IconPlayerPlay size="1rem" />} 
                    onClick={handlePreview}
                    loading={previewLoading}
                    disabled={!config.template}
                  >
                    Preview Template
                  </Button>
                </Stack>
              </Tabs.Panel>

              <Tabs.Panel value="url" pt="md">
                <TextInput 
                  label="Template URL" 
                  placeholder="https://example.com/templates/welcome.html" 
                  value={config.template_url || ''} 
                  onChange={(e) => updateConfig('template_url', e.target.value)} 
                />
              </Tabs.Panel>

              <Tabs.Panel value="s3" pt="md">
                <Stack gap="xs">
                  <Group grow>
                    <TextInput label="Region" placeholder="us-east-1" value={config.s3_region || ''} onChange={(e) => updateConfig('s3_region', e.target.value)} />
                    <TextInput label="Bucket" placeholder="my-templates" value={config.s3_bucket || ''} onChange={(e) => updateConfig('s3_bucket', e.target.value)} />
                  </Group>
                  <TextInput label="Key (Path)" placeholder="emails/welcome.html" value={config.s3_key || ''} onChange={(e) => updateConfig('s3_key', e.target.value)} />
                  <TextInput label="Endpoint" placeholder="Optional (for S3 compatible storage)" value={config.s3_endpoint || ''} onChange={(e) => updateConfig('s3_endpoint', e.target.value)} />
                  <Group grow>
                    <TextInput label="Access Key" value={config.s3_access_key || ''} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
                    <TextInput label="Secret Key" type="password" value={config.s3_secret_key || ''} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
                  </Group>
                </Stack>
              </Tabs.Panel>
            </Tabs>
          </>
        );
      case 'telegram':
        return (
          <>
            <TextInput label="Bot Token" placeholder="123456789:ABCDEF..." value={config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} required />
            <TextInput label="Chat ID" placeholder="-100123456789" value={config.chat_id || ''} onChange={(e) => updateConfig('chat_id', e.target.value)} required />
          </>
        );
      case 'http':
        return (
          <>
            <TextInput label="URL" placeholder="http://localhost:8080/webhook" value={config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Headers" placeholder="Authorization: Bearer token, X-Custom: value" value={config.headers || ''} onChange={(e) => updateConfig('headers', e.target.value)} />
          </>
        );
      case 'postgres':
      case 'mysql':
      case 'mariadb':
      case 'mssql':
      case 'oracle':
      case 'yugabyte':
        return (
          <>
            <Group grow>
              <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
              <TextInput 
                label="Port" 
                placeholder={
                  type === 'postgres' || type === 'yugabyte' ? "5432" : 
                  type === 'mysql' || type === 'mariadb' ? "3306" : 
                  type === 'mssql' ? "1433" : 
                  type === 'oracle' ? "1521" : "5432"
                } 
                value={config.port || ''} 
                onChange={(e) => updateConfig('port', e.target.value)} 
                required 
              />
            </Group>
            <Group grow>
              <TextInput label="User" placeholder="user" value={config.user || ''} onChange={(e) => updateConfig('user', e.target.value)} required />
              <TextInput label="Password" type="password" placeholder="password" value={config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} required />
            </Group>
            <Group align="flex-end" gap="xs">
              <Autocomplete 
                label="Database" 
                placeholder="dbname" 
                data={[...new Set([...discoveredDatabases, config.dbname].filter(Boolean))]} 
                value={config.dbname || ''} 
                onChange={(val) => {
                  updateConfig('dbname', val);
                  if (val) discoverTables();
                }} 
                required 
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
            <Group align="flex-end" gap="xs">
              <Select 
                label="Target Table" 
                placeholder="Select or type table name" 
                data={tables} 
                searchable 
                value={config.table || ''} 
                onChange={(val) => updateConfig('table', val || '')} 
                rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
                error={tablesError}
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
            {(type === 'postgres' || type === 'yugabyte') && <TextInput label="SSL Mode" placeholder="disable" value={config.sslmode || ''} onChange={(e) => updateConfig('sslmode', e.target.value)} />}
            <TextInput 
              label="OR Connection String" 
              placeholder={
                type === 'postgres' || type === 'yugabyte' ? "postgres://..." : 
                type === 'mysql' || type === 'mariadb' ? "user:pass@tcp(host:port)/dbname" : 
                type === 'mssql' ? "sqlserver://..." :
                type === 'oracle' ? "oracle://..." : "postgres://..."
              } 
              value={config.connection_string || ''} 
              onChange={(e) => updateConfig('connection_string', e.target.value)} 
            />
          </>
        );
      case 'cassandra':
        return (
          <>
            <TextInput label="Hosts" placeholder="localhost" value={config.hosts || ''} onChange={(e) => updateConfig('hosts', e.target.value)} required />
            <TextInput label="Keyspace" placeholder="keyspace" value={config.keyspace || ''} onChange={(e) => updateConfig('keyspace', e.target.value)} required />
          </>
        );
      case 'sqlite':
        return (
          <>
            <TextInput label="DB Path" placeholder="hermod.db" value={config.connection_string || config.db_path || ''} onChange={(e) => updateConfig('connection_string', e.target.value)} required />
            <Group align="flex-end" gap="xs">
              <Select 
                label="Target Table" 
                placeholder="Select or type table name" 
                data={tables} 
                searchable 
                value={config.table || ''} 
                onChange={(val) => updateConfig('table', val || '')} 
                rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
                error={tablesError}
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
          </>
        );
      case 'clickhouse':
        return (
          <>
            <TextInput label="Address" placeholder="localhost:9000" value={config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
            <Group align="flex-end" gap="xs">
              <Autocomplete 
                label="Database" 
                placeholder="default" 
                data={[...new Set([...discoveredDatabases, config.database].filter(Boolean))]} 
                value={config.database || ''} 
                onChange={(val) => {
                  updateConfig('database', val);
                  if (val) discoverTables();
                }} 
                required 
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
            <Group align="flex-end" gap="xs">
              <Select 
                label="Target Table" 
                placeholder="Select or type table name" 
                data={tables} 
                searchable 
                value={config.table || ''} 
                onChange={(val) => updateConfig('table', val || '')} 
                rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
                error={tablesError}
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
          </>
        );
      case 'mongodb':
        return (
          <>
            <TextInput label="URI" placeholder="mongodb://localhost:27017" value={config.uri || ''} onChange={(e) => updateConfig('uri', e.target.value)} required />
            <Group align="flex-end" gap="xs">
              <Autocomplete 
                label="Database" 
                placeholder="hermod" 
                data={[...new Set([...discoveredDatabases, config.database].filter(Boolean))]} 
                value={config.database || ''} 
                onChange={(val) => {
                  updateConfig('database', val);
                  if (val) discoverTables();
                }} 
                required 
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
            <Group align="flex-end" gap="xs">
              <Select 
                label="Target Collection" 
                placeholder="Select or type collection name" 
                data={tables} 
                searchable 
                value={config.table || config.collection || ''} 
                onChange={(val) => {
                  updateConfig('table', val || '');
                  updateConfig('collection', val || '');
                }} 
                rightSection={loadingTables ? <IconInfoCircle size={16} /> : null}
                error={tablesError}
                style={{ flex: 1 }}
              />
              <ActionIcon variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Collections">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
          </>
        );
      case 'stdout':
      default:
        return <Text size="sm" c="dimmed">No additional configuration required for stdout.</Text>;
    }
  };


  const renderSetupInstructions = () => {
    switch (sink.type) {
      case 'postgres':
      case 'yugabyte':
      case 'mssql':
      case 'oracle':
        return (
          <Stack gap="xs">
            <Title order={5}>{sink.type.charAt(0).toUpperCase() + sink.type.slice(1)} Sink</Title>
            <Text size="sm">Hermod can automatically create tables in the target database.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has <Code>CREATE</Code> and <Code>INSERT</Code> permissions</List.Item>
              <List.Item>Check the connection string format for your database type.</List.Item>
            </List>
          </Stack>
        );
      case 'mysql':
      case 'mariadb':
        return (
          <Stack gap="xs">
            <Title order={5}>{sink.type === 'mariadb' ? 'MariaDB' : 'MySQL'} Sink</Title>
            <Text size="sm">Hermod can automatically create tables in the target database.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has <Code>CREATE</Code> and <Code>INSERT</Code> permissions</List.Item>
              <List.Item>Connection string: <Code>user:pass@tcp(host:port)/dbname</Code></List.Item>
            </List>
          </Stack>
        );
      case 'clickhouse':
        return (
          <Stack gap="xs">
            <Title order={5}>ClickHouse Sink</Title>
            <Text size="sm">Hermod uses the ClickHouse native protocol.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has permissions to create databases and tables</List.Item>
              <List.Item>Address should be the native port (usually <Code>9000</Code>)</List.Item>
            </List>
          </Stack>
        );
      case 'mongodb':
        return (
          <Stack gap="xs">
            <Title order={5}>MongoDB Sink</Title>
            <Text size="sm">Inserts events as documents in a collection.</Text>
            <List size="sm" withPadding>
              <List.Item>Specify the collection in the transformation or use the source table name</List.Item>
              <List.Item>URI format: <Code>mongodb://user:pass@host:27017</Code></List.Item>
            </List>
          </Stack>
        );
      case 'cassandra':
        return (
          <Stack gap="xs">
            <Title order={5}>Cassandra Sink</Title>
            <Text size="sm">Inserts events into Cassandra tables.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the keyspace exists</List.Item>
              <List.Item>Hermod will attempt to create tables if they don't exist</List.Item>
            </List>
          </Stack>
        );
      case 'sqlite':
        return (
          <Stack gap="xs">
            <Title order={5}>SQLite Sink</Title>
            <Text size="sm">Writes events to a local SQLite database.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the absolute path to the database file</List.Item>
              <List.Item>Ensure the worker has write permissions for the file and directory</List.Item>
            </List>
          </Stack>
        );
      case 'nats':
        return (
          <Stack gap="xs">
            <Title order={5}>NATS Sink</Title>
            <Text size="sm">Publishes events to a NATS subject.</Text>
            <List size="sm" withPadding>
              <List.Item>Verify the NATS URL and Subject are correct</List.Item>
              <List.Item>If using auth, provide Username/Password or Token</List.Item>
            </List>
          </Stack>
        );
      case 'rabbitmq':
        return (
          <Stack gap="xs">
            <Title order={5}>RabbitMQ Stream Sink</Title>
            <Text size="sm">Publishes events to a RabbitMQ Stream.</Text>
            <List size="sm" withPadding>
              <List.Item>URL format: <Code>rabbitmq-stream://guest:guest@localhost:5552</Code></List.Item>
              <List.Item>Requires RabbitMQ 3.9+ with the stream plugin</List.Item>
            </List>
          </Stack>
        );
      case 'rabbitmq_queue':
        return (
          <Stack gap="xs">
            <Title order={5}>RabbitMQ Queue Sink</Title>
            <Text size="sm">Publishes events to a standard RabbitMQ queue (AMQP).</Text>
            <List size="sm" withPadding>
              <List.Item>The queue will be declared automatically if it doesn't exist</List.Item>
              <List.Item>URL format: <Code>amqp://guest:guest@localhost:5672</Code></List.Item>
            </List>
          </Stack>
        );
      case 'redis':
        return (
          <Stack gap="xs">
            <Title order={5}>Redis Sink</Title>
            <Text size="sm">Publishes events to a Redis Stream.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the Redis address and stream name</List.Item>
            </List>
          </Stack>
        );
      case 'kafka':
        return (
          <Stack gap="xs">
            <Title order={5}>Kafka Sink</Title>
            <Text size="sm">Publishes events to a Kafka topic.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the broker list and topic</List.Item>
              <List.Item>Supports SASL/Plain authentication</List.Item>
            </List>
          </Stack>
        );
      case 'file':
        return (
          <Stack gap="xs">
            <Title order={5}>File Sink</Title>
            <Text size="sm">Appends events to a local file.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the path is writable by the worker</List.Item>
              <List.Item>Events are written as JSON lines by default</List.Item>
            </List>
          </Stack>
        );
      case 'pulsar':
        return (
          <Stack gap="xs">
            <Title order={5}>Pulsar Sink</Title>
            <Text size="sm">Publishes events to an Apache Pulsar topic.</Text>
            <List size="sm" withPadding>
              <List.Item>URL format: <Code>pulsar://localhost:6650</Code></List.Item>
            </List>
          </Stack>
        );
      case 'kinesis':
        return (
          <Stack gap="xs">
            <Title order={5}>AWS Kinesis Sink</Title>
            <Text size="sm">Publishes events to an AWS Kinesis Data Stream.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the region and stream name</List.Item>
              <List.Item>Requires AWS credentials with <Code>kinesis:PutRecord</Code> permission</List.Item>
            </List>
          </Stack>
        );
      case 'pubsub':
        return (
          <Stack gap="xs">
            <Title order={5}>Google Cloud Pub/Sub Sink</Title>
            <Text size="sm">Publishes events to a GCP Pub/Sub topic.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the Project ID and Topic ID</List.Item>
              <List.Item>Ensure the worker has access (via environment or credentials JSON)</List.Item>
            </List>
          </Stack>
        );
      case 'fcm':
        return (
          <Stack gap="xs">
            <Title order={5}>Firebase Cloud Messaging Sink</Title>
            <Text size="sm">Sends notifications via FCM.</Text>
            <List size="sm" withPadding>
              <List.Item>Requires a Service Account JSON with FCM permissions</List.Item>
              <List.Item>Events should contain the target device token and notification payload</List.Item>
            </List>
          </Stack>
        );
      case 'smtp':
        return (
          <Stack gap="xs">
            <Title order={5}>SMTP Sink</Title>
            <Text size="sm">Sends CDC messages as emails using the gsmail library.</Text>
            <List size="sm" withPadding>
              <List.Item>Configure your SMTP server host and port</List.Item>
              <List.Item>Provide valid username and password for authentication</List.Item>
              <List.Item>Multiple recipients in "To" field should be comma-separated</List.Item>
              <List.Item>SSL/TLS is supported</List.Item>
            </List>
          </Stack>
        );
      case 'telegram':
        return (
          <Stack gap="xs">
            <Title order={5}>Telegram Sink</Title>
            <Text size="sm">Sends CDC events as messages to a Telegram chat.</Text>
            <List size="sm" withPadding>
              <List.Item>Create a bot via <Code>@BotFather</Code> to get a Token</List.Item>
              <List.Item>Get your Chat ID (you can use <Code>@userinfobot</Code> or similar)</List.Item>
              <List.Item>The bot must be a member of the chat if it's a group</List.Item>
            </List>
          </Stack>
        );
      case 'http':
        return (
          <Stack gap="xs">
            <Title order={5}>HTTP/Webhook Sink</Title>
            <Text size="sm">Sends events to an external HTTP endpoint.</Text>
            <List size="sm" withPadding>
              <List.Item>Events are sent via <Code>POST</Code> requests</List.Item>
              <List.Item>Supports custom headers for authentication</List.Item>
            </List>
          </Stack>
        );
      case 'stdout':
        return (
          <Stack gap="xs">
            <Title order={5}>Standard Output Sink</Title>
            <Text size="sm">Prints events to the worker's standard output.</Text>
            <List size="sm" withPadding>
              <List.Item>Useful for debugging and logging</List.Item>
            </List>
          </Stack>
        );
      default:
        return (
          <Group gap="xs" c="dimmed">
            <IconInfoCircle size="1.2rem" />
            <Text size="sm">Select a sink type to see setup instructions.</Text>
          </Group>
        );
    }
  };

  return (
    <>
      <Grid gutter="md" grow style={{ minHeight: 'calc(100vh - 160px)' }}>
        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group gap="xs">
                <IconSettings size="1.2rem" color="var(--mantine-color-gray-7)" />
                <Text size="sm" fw={700}>1. GENERAL SETTINGS</Text>
              </Group>
              <Divider />
              <Stack gap="sm">
                <TextInput 
                  label="Name" 
                  placeholder="NATS Sink" 
                  value={sink.name}
                  onChange={(e) => handleSinkChange({ name: e.target.value })}
                  required
                />
                {!embedded && (
                  <Select 
                    label="VHost" 
                    placeholder="Select a virtual host" 
                    data={availableVHostsList}
                    value={sink.vhost}
                    onChange={(val) => handleSinkChange({ vhost: val || '' })}
                    required
                  />
                )}
                {!embedded && (
                  <Select 
                    label="Worker (Optional)" 
                    placeholder="Assign to a specific worker" 
                    data={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
                    value={sink.worker_id}
                    onChange={(val) => handleSinkChange({ worker_id: val || '' })}
                    clearable
                  />
                )}
                <Select 
                  label="Type" 
                  data={SINK_TYPES} 
                  value={sink.type}
                  onChange={(val) => handleSinkChange({ type: val || '', config: { ...(sink.config || {}), format: (sink.config || {}).format || 'json' } })}
                  required
                />

                <Divider label="Reliability & Batching" labelPosition="center" />
                <Group grow>
                  <TextInput 
                    label="Max Retries" 
                    placeholder="3" 
                    size="xs"
                    value={(sink.config || {}).max_retries || ''} 
                    onChange={(e) => updateConfig('max_retries', e.target.value)} 
                  />
                  <TextInput 
                    label="Batch Size" 
                    placeholder="1" 
                    size="xs"
                    value={(sink.config || {}).batch_size || ''} 
                    onChange={(e) => updateConfig('batch_size', e.target.value)} 
                  />
                </Group>
                <TextInput 
                  label="Retry Interval" 
                  placeholder="100ms, 1s, 1d" 
                  size="xs"
                  value={(sink.config || {}).retry_interval || ''} 
                  onChange={(e) => updateConfig('retry_interval', e.target.value)} 
                />
              </Stack>
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
                  <Text size="sm" fw={700}>2. PARAMETERS</Text>
                </Group>
                <Badge variant="dot" color="blue" style={{ textTransform: 'uppercase' }}>{sink.type}</Badge>
              </Group>
              <Divider />
              <ScrollArea flex={1} mx="-md" px="md">
                <Stack gap="md" py="xs">
                  {testResult && (
                    <Alert 
                      icon={testResult.status === 'ok' ? <IconCheck size="1rem" /> : <IconAlertCircle size="1rem" />} 
                      title={testResult.status === 'ok' ? "Connection Success" : "Connection Error"} 
                      color={testResult.status === 'ok' ? "green" : "red"}
                      withCloseButton
                      onClose={() => setTestResult(null)}
                    >
                      {testResult.message}
                    </Alert>
                  )}
                  {renderConfigFields()}
                  <Select 
                    label="Output Format" 
                    data={['json', 'payload']} 
                    value={(sink.config || {}).format || 'json'}
                    onChange={(val) => setSink({ ...sink, config: { ...(sink.config || {}), format: val || 'json' } })}
                  />
                </Stack>
              </ScrollArea>
              
              <Divider mt="md" />
              <Group justify="flex-end" pt="xs">
                {!embedded && <Button variant="outline" size="xs" onClick={() => navigate({ to: '/sinks' })}>Cancel</Button>}
                {sink.type !== 'stdout' && (
                  <Button variant="outline" color="blue" size="xs" onClick={() => testMutation.mutate(sink)} loading={testMutation.isPending}>
                    Test Connection
                  </Button>
                )}
                <Button 
                  size="xs"
                  disabled={!sink.name || (!embedded && !sink.vhost)}
                  onClick={() => {
                    submitMutation.mutate(sink);
                  }} 
                  loading={submitMutation.isPending}
                >
                  {isEditing ? 'Save Changes' : (embedded ? 'Confirm' : 'Create Sink')}
                </Button>
              </Group>
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="gray.0">
            <Stack h="100%">
              <Group gap="xs">
                <IconInfoCircle size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700}>3. SETUP GUIDE</Text>
              </Group>
              <Divider />
              <ScrollArea flex={1}>
                {renderSetupInstructions()}
              </ScrollArea>
            </Stack>
          </Card>
        </Grid.Col>
      </Grid>

      <Modal 
        opened={previewModalOpen} 
        onClose={() => setPreviewModalOpen(false)} 
        title="Template Preview" 
        size="lg"
      >
        <Stack>
          <Text size="sm" c="dimmed">
            This is a preview of your template rendered with sample data.
          </Text>
          <Paper withBorder p="md" bg="gray.0">
            {previewResult?.is_html ? (
              <div dangerouslySetInnerHTML={{ __html: previewResult.rendered }} />
            ) : (
              <Code block>{previewResult?.rendered}</Code>
            )}
          </Paper>
          <Group justify="flex-end">
            <Button onClick={() => setPreviewModalOpen(false)}>Close</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
