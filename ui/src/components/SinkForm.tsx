import { useState, useEffect, useCallback, useRef } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Text, Grid, Title, Code, List, ActionIcon, Modal, Card, ScrollArea, Badge, Autocomplete, Box, Switch } from '@mantine/core';
import { IconCheck, IconAlertCircle, IconInfoCircle, IconSettings, IconBraces, IconRefresh, IconDatabase, IconList, IconCode, IconPlus } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { useForm, useStore } from '@tanstack/react-form';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { RetryPolicyFields } from './Sink/RetryPolicyFields';
import { SinkBasics } from './Sink/SinkBasics';
import { PostgresSinkConfig } from './Sink/PostgresSinkConfig';
import { QueueSinkConfig } from './Sink/QueueSinkConfig';
import { FTPSinkConfig } from './Sink/FTPSinkConfig';
import { GoogleSheetsSinkConfig } from './Sink/GoogleSheetsSinkConfig';
import { SMTPSinkConfig } from './Sink/SMTPSinkConfig';
import { ElasticsearchSinkConfig } from './Sink/ElasticsearchSinkConfig';
import { FailoverSinkConfig } from './Sink/FailoverSinkConfig';
import { FieldExplorer } from './Transformation/FieldExplorer';

const API_BASE = '/api';

const SINK_TYPES = [
  'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'file', 'kafka', 'pulsar', 'kinesis', 'pubsub', 's3', 's3-parquet', 'fcm', 'smtp', 'telegram', 'http', 'stdout',
  'postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb', 'elasticsearch', 'googlesheets', 'ftp', 'failover', 'eventstore'
];


interface SinkFormProps {
  initialData?: any;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
  availableFields?: string[];
  incomingPayload?: any;
  sinks?: any[];
}

export function SinkForm({ initialData, isEditing = false, embedded = false, onSave, vhost, workerID, availableFields = [], incomingPayload, sinks }: SinkFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const [previewModalOpen, setPreviewModalOpen] = useState(false);
  const [previewResult, setPreviewResult] = useState<{ rendered: string, is_html: boolean } | null>(null);
  // Accessibility: IDs for modal title/description
  const previewTitleId = 'sink-preview-modal-title';
  const previewDescId = 'sink-preview-modal-desc';
  const [previewLoading, setPreviewLoading] = useState(false);
  const [validateEmailLoading, setValidateEmailLoading] = useState(false);
  const form = useForm({
    defaultValues: {
      name: initialData?.name || '', 
      type: initialData?.type || 'stdout', 
      vhost: (embedded ? vhost : (initialData?.vhost || vhost)) || '', 
      worker_id: (embedded ? workerID : (initialData?.worker_id || workerID)) || '',
      config: { 
        format: 'json', 
        max_retries: '3', 
        retry_interval: '1s',
        ...(initialData?.config || {})
      },
      ...(initialData?.id ? { id: initialData.id } : {})
    }
  });

  const sink = useStore(form.store, (state) => state.values);

  const [tables, setTables] = useState<string[]>([]);
  const [loadingTables, setLoadingTables] = useState(false);
  const [tablesError, setTablesError] = useState<string | null>(null);
  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);

  const dbAbortRef = useRef<AbortController | null>(null);
  const tablesAbortRef = useRef<AbortController | null>(null);
  const previewAbortRef = useRef<AbortController | null>(null);

  const fetchDatabases = async () => {
    if (dbAbortRef.current) dbAbortRef.current.abort();
    const controller = new AbortController();
    dbAbortRef.current = controller;
    setIsFetchingDBs(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/databases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
        signal: controller.signal,
      });
      const dbs = await res.json();
      if (!res.ok) throw new Error(dbs.error || 'Failed to discover databases');
      setDiscoveredDatabases(dbs || []);
    } catch (err: any) {
      if (err?.name !== 'AbortError') {
        setTestResult({ status: 'error', message: err.message });
      }
    } finally {
      setIsFetchingDBs(false);
    }
  };

  const discoverTables = useCallback(async () => {
    if (tablesAbortRef.current) tablesAbortRef.current.abort();
    const controller = new AbortController();
    tablesAbortRef.current = controller;
    setLoadingTables(true);
    setTablesError(null);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sink),
        signal: controller.signal,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to discover tables');
      setTables(data || []);
    } catch (err: any) {
      if (err?.name !== 'AbortError') {
        setTablesError(err.message);
      }
    } finally {
      setLoadingTables(false);
    }
  }, [sink]);

  const sinkType = sink.type;
  const host = sink.config?.host;
  const connectionString = sink.config?.connection_string;
  const uri = sink.config?.uri;
  const dbPath = sink.config?.db_path;
  const discoverTablesCb = useCallback(() => discoverTables(), [discoverTables]);

  useEffect(() => {
    const dbTypes = ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'yugabyte', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'];
    const hasConn = Boolean(host || connectionString || uri || dbPath);
    if (dbTypes.includes(sinkType) && hasConn) {
      const timer = setTimeout(() => {
        discoverTablesCb();
      }, 600);
      return () => {
        clearTimeout(timer);
        if (tablesAbortRef.current) tablesAbortRef.current.abort();
      };
    }
  }, [sinkType, host, connectionString, uri, dbPath, discoverTablesCb]);

  const lastInitialDataId = useRef<string | null>(null);

  useEffect(() => {
    if (initialData) {
      // Use ref to ensure we only reset when initialData actually changes its identity or ID
      if (lastInitialDataId.current !== (initialData.id || 'new')) {
        const newValues = {
          ...sink,
          ...initialData,
          config: {
            ...(sink.config || {}),
            ...(initialData.config || {})
          }
        };
        form.reset(newValues);
        lastInitialDataId.current = initialData.id || 'new';
      }
    }
  }, [initialData, form]);

  useEffect(() => {
    if (sinkType === 'stdout') {
      setTestResult({ status: 'ok', message: 'Stdout is always active' });
    } else {
      setTestResult(prev => (prev?.message === 'Stdout is always active' ? null : prev));
    }
  }, [sinkType]);

  const { data: vhostsResponse } = useSuspenseQuery<any>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const { data: sinksResponse } = useSuspenseQuery<any>({
    queryKey: ['sinks', vhost],
    queryFn: async () => {
      const vhostParam = (vhost && vhost !== 'all') ? `?vhost=${vhost}` : '';
      const res = await apiFetch(`${API_BASE}/sinks${vhostParam}`);
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

  const vhosts = Array.isArray(vhostsResponse?.data) ? vhostsResponse.data : [];
  const workers = Array.isArray(workersResponse?.data) ? workersResponse.data : [];

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
      if (vhost && form.getFieldValue('vhost') !== vhost) {
        form.setFieldValue('vhost', vhost);
      }
      if (workerID && form.getFieldValue('worker_id') !== workerID) {
        form.setFieldValue('worker_id', workerID);
      }
    }
  }, [embedded, vhost, workerID, form]);

  const handleSinkChange = (updates: any) => {
    Object.entries(updates).forEach(([key, value]) => {
      form.setFieldValue(key as any, value);
    });
    setTestResult(null);
  };

  const updateConfig = (key: string, value: any) => {
    form.setFieldValue(`config.${key}` as any, value);
    setTestResult(null);
  };


  const handlePreview = async () => {
    if (!sink.config?.template && sink.config?.template_source === 'inline') return;
    if (previewAbortRef.current) previewAbortRef.current.abort();
    const controller = new AbortController();
    previewAbortRef.current = controller;
    setPreviewLoading(true);
    try {
      const res = await apiFetch(`${API_BASE}/sinks/smtp/preview`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          template: sink.config?.template,
          outlook_compatible: sink.config?.outlook_compatible === 'true',
          data: {
            id: "123",
            operation: "create",
            table: "orders",
            schema: "public",
            is_priority: true,
            after: JSON.stringify({
              id: 999,
              customer_name: "John Doe",
              status: "Shipped",
              total_amount: 139.99
            }),
            before: null,
            items: [
              { name: "Wireless Mouse", price: 25.99, qty: 1 },
              { name: "Mechanical Keyboard", price: 89.00, qty: 1 },
              { name: "USB-C Cable", price: 12.50, qty: 2 }
            ],
            metadata: {
              ip: "192.168.1.1",
              source: "postgres"
            }
          }
        }),
        signal: controller.signal,
      });
      const data = await res.json();
      if (res.ok) {
        setPreviewResult(data);
        setPreviewModalOpen(true);
      } else {
        setTestResult({ status: 'error', message: data.error || 'Failed to preview template' });
      }
    } catch (error: any) {
      if (error?.name !== 'AbortError') {
        setTestResult({ status: 'error', message: error.message });
      }
    } finally {
      setPreviewLoading(false);
    }
  };

  useEffect(() => {
    return () => {
      if (dbAbortRef.current) dbAbortRef.current.abort();
      if (tablesAbortRef.current) tablesAbortRef.current.abort();
      if (previewAbortRef.current) previewAbortRef.current.abort();
    };
  }, []);

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
      case 'rabbitmq':
      case 'rabbitmq_queue':
      case 'redis':
      case 'kafka':
      case 'pulsar':
      case 'kinesis':
      case 'pubsub':
        return <QueueSinkConfig type={type} config={config} updateConfig={updateConfig} />;
      case 'file':
        return (
          <TextInput label="Filename" placeholder="/tmp/hermod.log" value={config.filename || ''} onChange={(e) => updateConfig('filename', e.target.value)} required />
        );
      case 's3-parquet':
        return (
          <>
            <Group grow>
              <TextInput label="Region" placeholder="us-east-1" value={config.region || ''} onChange={(e) => updateConfig('region', e.target.value)} required />
              <TextInput label="Bucket" placeholder="my-bucket" value={config.bucket || ''} onChange={(e) => updateConfig('bucket', e.target.value)} required />
            </Group>
            <TextInput label="Key Prefix" placeholder="events/" value={config.key_prefix || ''} onChange={(e) => updateConfig('key_prefix', e.target.value)} />
            <TextInput label="Endpoint (S3-compatible)" placeholder="e.g. http://localhost:9000" value={config.endpoint || ''} onChange={(e) => updateConfig('endpoint', e.target.value)} />
            <Group grow>
              <TextInput label="Access Key" placeholder="Optional" value={config.access_key || ''} onChange={(e) => updateConfig('access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" placeholder="Optional" value={config.secret_key || ''} onChange={(e) => updateConfig('secret_key', e.target.value)} />
            </Group>
            <TextInput 
              label="Parquet Schema (JSON)" 
              placeholder='{"Tag": "name=parquet_go_root, instanceid=1", "Fields": [{"Tag": "name=name, type=BYTE_ARRAY, convertedtype=UTF8"}]}' 
              value={config.schema || ''} 
              onChange={(e) => updateConfig('schema', e.target.value)} 
              required
              description="Define the Parquet schema in xitongsys/parquet-go JSON format."
            />
            <TextInput 
              label="Parallelizer" 
              placeholder="4" 
              value={config.parallelizer || ''} 
              onChange={(e) => updateConfig('parallelizer', e.target.value)} 
              description="Number of parallel goroutines for Parquet writing."
            />
          </>
        );
      case 's3':
        return (
          <>
            <Group grow>
              <TextInput label="Region" placeholder="us-east-1" value={config.region || ''} onChange={(e) => updateConfig('region', e.target.value)} required />
              <TextInput label="Bucket" placeholder="my-bucket" value={config.bucket || ''} onChange={(e) => updateConfig('bucket', e.target.value)} required />
            </Group>
            <TextInput label="Key Prefix" placeholder="events/" value={config.key_prefix || ''} onChange={(e) => updateConfig('key_prefix', e.target.value)} />
            <TextInput label="Endpoint (S3-compatible)" placeholder="e.g. http://localhost:9000" value={config.endpoint || ''} onChange={(e) => updateConfig('endpoint', e.target.value)} />
            <Group grow>
              <TextInput label="Access Key" placeholder="Optional" value={config.access_key || ''} onChange={(e) => updateConfig('access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" placeholder="Optional" value={config.secret_key || ''} onChange={(e) => updateConfig('secret_key', e.target.value)} />
            </Group>
            <Group grow>
              <TextInput 
                label="File Extension"
                placeholder=".json or .csv"
                value={config.suffix || ''}
                onChange={(e) => updateConfig('suffix', e.target.value)}
                description="Set to .csv to store CSV content with .csv keys. Leave empty to default to .json."
              />
              <TextInput 
                label="Content Type"
                placeholder="e.g. text/csv or application/json"
                value={config.content_type || ''}
                onChange={(e) => updateConfig('content_type', e.target.value)}
                description="Optional. Sets the S3 Content-Type metadata."
              />
            </Group>
            <Text size="sm" c="dimmed">
              Tip: To upload CSV bytes as-is, leave Format empty (pass-through) in the Advanced section and set File Extension to .csv.
            </Text>
          </>
        );
      case 'fcm':
        return (
          <TextInput label="Credentials JSON" placeholder="Service account JSON content" value={config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} required />
        );
      case 'googlesheets':
        return <GoogleSheetsSinkConfig config={config} updateConfig={updateConfig} />;
      case 'smtp':
        return (
          <SMTPSinkConfig
            config={config}
            updateConfig={updateConfig}
            validateEmailLoading={validateEmailLoading}
            handleValidateEmail={handleValidateEmail}
            handlePreview={handlePreview}
            previewLoading={previewLoading}
          />
        );
      case 'ftp':
        return <FTPSinkConfig config={config} updateConfig={updateConfig} />;
      case 'failover':
        return (
          <FailoverSinkConfig 
            config={config} 
            sinks={sinks || []} 
            currentSinkId={initialData?.id} 
            updateConfig={updateConfig} 
          />
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
            <Select
              label="Compression"
              placeholder="Select compression"
              data={[
                { value: '', label: 'None' },
                { value: 'lz4', label: 'LZ4' },
                { value: 'snappy', label: 'Snappy' },
                { value: 'zstd', label: 'Zstd' },
              ]}
              value={config.compression || ''}
              onChange={(val) => updateConfig('compression', val || '')}
            />
          </>
        );
      case 'postgres':
      case 'yugabyte':
        return (
          <PostgresSinkConfig
            type={type}
            config={config}
            tables={tables}
            discoveredDatabases={discoveredDatabases}
            isFetchingDBs={isFetchingDBs}
            loadingTables={loadingTables}
            tablesError={tablesError}
            updateConfig={updateConfig}
            fetchDatabases={fetchDatabases}
            discoverTables={discoverTables}
          />
        );
      case 'mysql':
      case 'mariadb':
      case 'mssql':
      case 'oracle':
        return (
          <>
            <Group grow>
              <TextInput label="Host" placeholder="localhost" value={config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
              <TextInput 
                label="Port" 
                placeholder={
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
              <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
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
              <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
            <TextInput 
              label="OR Connection String" 
              placeholder={
                type === 'mysql' || type === 'mariadb' ? "user:pass@tcp(host:port)/dbname" : 
                type === 'mssql' ? "sqlserver://..." :
                type === 'oracle' ? "oracle://..." : ""
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
              <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={() => discoverTables()} loading={loadingTables} title="Refresh Tables">
                <IconRefresh size="1.2rem" />
              </ActionIcon>
            </Group>
          </>
        );
      case 'eventstore':
        return (
          <>
            <Select
              label="Driver"
              placeholder="Select database driver"
              data={[
                { value: 'postgres', label: 'PostgreSQL' },
                { value: 'mysql', label: 'MySQL' },
                { value: 'sqlite', label: 'SQLite' },
                { value: 'mssql', label: 'SQL Server' }
              ]}
              value={config.driver || ''}
              onChange={(val) => updateConfig('driver', val || '')}
              required
            />
            {config.driver === 'sqlite' ? (
              <TextInput label="DB Path" placeholder="eventstore.db" value={config.dsn || ''} onChange={(e) => updateConfig('dsn', e.target.value)} required />
            ) : (
              <TextInput label="DSN / Connection String" placeholder="postgres://user:pass@localhost:5432/eventstore" value={config.dsn || ''} onChange={(e) => updateConfig('dsn', e.target.value)} required />
            )}
            <TextInput 
              label="Stream ID Template" 
              placeholder="{{.table}}:{{.id}}" 
              value={config.stream_id_tpl || ''} 
              onChange={(e) => updateConfig('stream_id_tpl', e.target.value)} 
              description="Leave empty for default (table:id)"
            />
            <TextInput 
              label="Event Type Template" 
              placeholder="{{.operation}}" 
              value={config.event_type_tpl || ''} 
              onChange={(e) => updateConfig('event_type_tpl', e.target.value)} 
              description="Leave empty for default (operation)"
            />
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
              <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Discover Databases">
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
      case 'elasticsearch':
        return (
          <ElasticsearchSinkConfig 
            config={config} 
            updateConfig={updateConfig} 
            indices={tables}
            discoveredDatabases={discoveredDatabases}
            isFetchingDBs={isFetchingDBs}
            loadingIndices={loadingTables}
            indicesError={tablesError}
            fetchDatabases={fetchDatabases}
            discoverIndices={discoverTables}
          />
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
      case 'elasticsearch':
        return (
          <Stack gap="xs">
            <Title order={5}>Elasticsearch Sink</Title>
            <Text size="sm">Indexes documents into Elasticsearch.</Text>
            <List size="sm" withPadding>
              <List.Item>Addresses should be a comma-separated list of nodes</List.Item>
              <List.Item>Index name supports Go templates (e.g. <Code>logs-{"{{.table}}"}</Code>)</List.Item>
              <List.Item>Supports Basic Auth or API Key authentication</List.Item>
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
      case 's3-parquet':
        return (
          <Stack gap="xs">
            <Title order={5}>AWS S3 Parquet Sink</Title>
            <Text size="sm">Persists events as Parquet files to an AWS S3 bucket.</Text>
            <List size="sm" withPadding>
              <List.Item>Writes messages in batches to optimized Parquet files</List.Item>
              <List.Item>Requires a JSON schema definition for the Parquet format</List.Item>
              <List.Item>Supports S3-compatible storage like MinIO via Endpoint URL</List.Item>
            </List>
          </Stack>
        );
      case 's3':
        return (
          <Stack gap="xs">
            <Title order={5}>AWS S3 Sink</Title>
            <Text size="sm">Persists events to an AWS S3 bucket.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the region and bucket name</List.Item>
              <List.Item>Key Prefix is optional (e.g. <Code>events/</Code>)</List.Item>
              <List.Item>Supports S3-compatible storage like MinIO via Endpoint URL</List.Item>
              <List.Item>Supports Environment Variables substitution: <Code>{'${AWS_ACCESS_KEY_ID}'}</Code></List.Item>
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
              <List.Item>
                <Text size="sm" fw={500}>Go Template Support:</Text>
                <Text size="xs" c="dimmed" mb={5}>You can use full Go template syntax in Subject and Body.</Text>
                <Code block style={{ fontSize: '11px', marginBottom: '8px' }}>
{`Hello {{.name}},

Here are your items:
{{range .items}}
- {{.product}}: {{.price}}
{{else}}
- No items found
{{end}}`}
                </Code>
                <Text size="xs" fw={500} c="dimmed" mb={5}>Conditional Logic (If/Else):</Text>
                <Code block style={{ fontSize: '11px' }}>
{`{{if .is_priority}}
[URGENT] High priority order!
{{else}}
Regular order processing.
{{end}}

{{if eq .operation "delete"}}
Record was removed from {{.table}}.
{{else}}
Record was {{.operation}}d in {{.table}}.
{{end}}`}
                </Code>
              </List.Item>
              <List.Item>Dynamic Recipients: Use <Code>{"{{.email_field}}"}</Code> in the "To" field.</List.Item>
              <List.Item>CDC Support: Fields in <Code>after</Code> and <Code>before</Code> JSON strings are automatically unmarshaled and available directly (e.g., <Code>{"{{.id}}"}</Code> instead of <Code>{"{{.after.id}}"}</Code>).</List.Item>
            </List>
          </Stack>
        );
      case 'ftp':
        return (
          <Stack gap="xs">
            <Title order={5}>FTP / FTPS Sink</Title>
            <Text size="sm">Uploads each message as a file to an FTP/FTPS server.</Text>
            <List size="sm" withPadding>
              <List.Item>Set Host and Port (default <Code>21</Code>). Enable <Code>TLS</Code> for FTPS if your server supports it.</List.Item>
              <List.Item>Optional authentication via Username/Password (leave empty for anonymous if allowed).</List.Item>
              <List.Item>
                Destination path supports Go templates. Examples:
                <Code block style={{ fontSize: '11px', marginTop: 6 }}>
{`Path: {{.schema}}/{{.table}}
File: {{.table}}-{{.id}}.json`}
                </Code>
              </List.Item>
              <List.Item>
                Write Mode: <Code>overwrite</Code> replaces existing files; <Code>append</Code> appends to existing files.
              </List.Item>
              <List.Item>
                Enable "Create Missing Directories" to recursively create folders under <Code>Root Directory</Code>.
              </List.Item>
              <List.Item>Timeout accepts Go duration strings (e.g., <Code>30s</Code>).</List.Item>
              <List.Item>Security: FTPS currently uses <Code>InsecureSkipVerify=true</Code>; configure per your policy.</List.Item>
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

  if (embedded) {
    return (
      <>
      <Grid gutter="lg" style={{ minHeight: 'calc(100vh - 180px)' }}>
        {/* Column 1: Source Data */}
        <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
          <Stack gap="md" h="100%">
            <Group gap="xs" px="xs">
              <IconDatabase size="1.2rem" color="var(--mantine-color-blue-6)" />
              <Text size="sm" fw={700} c="dimmed">1. SOURCE DATA</Text>
            </Group>
            
            <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
              <Text size="xs">Use fields from upstream nodes. Click <IconPlus size="0.7rem" /> to copy as Go template variable.</Text>
            </Alert>

            <Card withBorder padding="xs" radius="md">
              <Group justify="space-between" mb="xs">
                <Group gap="xs">
                  <IconList size="1rem" color="var(--mantine-color-gray-6)" />
                  <Text size="xs" fw={700}>AVAILABLE FIELDS</Text>
                </Group>
                <Badge size="xs" variant="light">{availableFields.length}</Badge>
              </Group>
              <FieldExplorer
                availableFields={availableFields}
                incomingPayload={incomingPayload}
                onAdd={(path) => {
                  const val = `{{.${path}}}`;
                  navigator.clipboard.writeText(val);
                  notifications.show({
                    title: 'Field copied',
                    message: `Copied ${val} to clipboard`,
                    color: 'blue',
                  });
                }}
              />
            </Card>

            <Card withBorder padding="xs" radius="md" bg="gray.0">
               <Group gap="xs" mb={4}>
                  <IconCode size="1rem" color="dimmed" />
                  <Text size="10px" fw={700} c="dimmed">RAW PAYLOAD</Text>
               </Group>
               <ScrollArea.Autosize mah={300}>
                  <Code block style={{ fontSize: '10px' }}>
                     {incomingPayload ? JSON.stringify(incomingPayload, null, 2) : 'No input sample available'}
                  </Code>
               </ScrollArea.Autosize>
            </Card>
          </Stack>
        </Grid.Col>

        {/* Column 2: Configuration */}
        <Grid.Col span={{ base: 12, md: 8, lg: 5 }}>
          <Card withBorder shadow="md" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
            <Stack h="100%" gap="md">
              <Group justify="space-between" px="xs">
                <Group gap="xs">
                  <IconSettings size="1.2rem" color="var(--mantine-color-blue-6)" />
                  <Text size="sm" fw={700}>2. CONFIGURATION</Text>
                </Group>
                <Badge variant="light" color="blue" size="lg" style={{ textTransform: 'uppercase' }}>{sink.type}</Badge>
              </Group>
              <Divider />
              <ScrollArea flex={1} mx="-md" px="md">
                <Stack gap="md" py="xs">
                  <SinkBasics
                    embedded={embedded}
                    name={sink.name}
                    onChangeName={(val: string) => handleSinkChange({ name: val })}
                    vhost={sink.vhost}
                    onChangeVHost={(val: string) => handleSinkChange({ vhost: val })}
                    workerId={sink.worker_id}
                    onChangeWorkerId={(val: string) => handleSinkChange({ worker_id: val })}
                    type={sink.type}
                    onChangeType={(val: string) => handleSinkChange({ type: val, config: { ...(sink.config || {}), format: (sink.config || {}).format || 'json' } })}
                    vhostOptions={availableVHostsList}
                    workerOptions={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
                    typeOptions={SINK_TYPES}
                  />
                  
                  <Divider label="Parameters" labelPosition="center" />
                  {renderConfigFields()}
                  
                  <Select 
                    label="Output Format" 
                    data={['json', 'payload']} 
                    value={(sink.config || {}).format || 'json'}
                    onChange={(val) => updateConfig('format', val || 'json')}
                  />
                  
                  <Divider label="Reliability & Batching" labelPosition="center" />
                  <RetryPolicyFields
                    maxRetries={(sink.config || {}).max_retries}
                    retryInterval={(sink.config || {}).retry_interval}
                    onChangeMaxRetries={(val) => updateConfig('max_retries', val)}
                    onChangeRetryInterval={(val) => updateConfig('retry_interval', val)}
                  />

                  <Grid gutter="xs">
                    <Grid.Col span={4}>
                      <TextInput 
                        label="Circuit Threshold" 
                        placeholder="5" 
                        size="xs"
                        description="Failures before opening"
                        value={(sink.config || {}).circuit_threshold || ''} 
                        onChange={(e) => updateConfig('circuit_threshold', e.target.value)} 
                      />
                    </Grid.Col>
                    <Grid.Col span={4}>
                      <TextInput 
                        label="Circuit Interval" 
                        placeholder="1m" 
                        size="xs"
                        description="Failure window"
                        value={(sink.config || {}).circuit_interval || ''} 
                        onChange={(e) => updateConfig('circuit_interval', e.target.value)} 
                      />
                    </Grid.Col>
                    <Grid.Col span={4}>
                      <TextInput 
                        label="Circuit Cool-off" 
                        placeholder="30s" 
                        size="xs"
                        description="Time before retry"
                        value={(sink.config || {}).circuit_cool_off || ''} 
                        onChange={(e) => updateConfig('circuit_cool_off', e.target.value)} 
                      />
                    </Grid.Col>
                  </Grid>

                  <Grid gutter="xs">
                    <Grid.Col span={6}>
                      <TextInput 
                        label="Batch Size" 
                        placeholder="1" 
                        size="xs"
                        value={(sink.config || {}).batch_size || ''} 
                        onChange={(e) => updateConfig('batch_size', e.target.value)} 
                      />
                    </Grid.Col>
                    <Grid.Col span={6}>
                      <TextInput 
                        label="Batch Timeout" 
                        placeholder="100ms" 
                        size="xs"
                        value={(sink.config || {}).batch_timeout || ''} 
                        onChange={(e) => updateConfig('batch_timeout', e.target.value)} 
                      />
                    </Grid.Col>
                  </Grid>

                  <Switch 
                    label="Adaptive Batching" 
                    size="xs"
                    description="Dynamically adjust batch size based on sink performance"
                    checked={(sink.config || {}).adaptive_batching || false} 
                    onChange={(e) => updateConfig('adaptive_batching', e.currentTarget.checked)} 
                  />

                  <Divider label="Backpressure" labelPosition="center" />

                  <Select
                    label="Backpressure Strategy"
                    placeholder="Block (Default)"
                    size="xs"
                    data={[
                      { label: 'Block (Wait)', value: 'block' },
                      { label: 'Drop Oldest', value: 'drop_oldest' },
                      { label: 'Drop Newest', value: 'drop_newest' },
                      { label: 'Sampling', value: 'sampling' },
                      { label: 'Spill to Disk', value: 'spill_to_disk' },
                    ]}
                    value={(sink.config || {}).backpressure_strategy || 'block'}
                    onChange={(val) => updateConfig('backpressure_strategy', val || 'block')}
                  />

                  <Grid gutter="xs">
                    <Grid.Col span={6}>
                      <TextInput 
                        label="Buffer Size" 
                        placeholder="1000" 
                        size="xs"
                        description="Max queued messages"
                        value={(sink.config || {}).backpressure_buffer || ''} 
                        onChange={(e) => updateConfig('backpressure_buffer', e.target.value)} 
                      />
                    </Grid.Col>
                    <Grid.Col span={6}>
                      {(sink.config || {}).backpressure_strategy === 'sampling' && (
                        <TextInput 
                          label="Sampling Rate" 
                          placeholder="0.5" 
                          size="xs"
                          description="0.0 to 1.0 (Keep %)"
                          value={(sink.config || {}).sampling_rate || ''} 
                          onChange={(e) => updateConfig('sampling_rate', e.target.value)} 
                        />
                      )}
                      {(sink.config || {}).backpressure_strategy === 'spill_to_disk' && (
                        <TextInput 
                          label="Spill Max Size" 
                          placeholder="104857600" 
                          size="xs"
                          description="Max spill size in bytes"
                          value={(sink.config || {}).spill_max_size || ''} 
                          onChange={(e) => updateConfig('spill_max_size', e.target.value)} 
                        />
                      )}
                    </Grid.Col>
                  </Grid>

                  {(sink.config || {}).backpressure_strategy === 'spill_to_disk' && (
                    <TextInput 
                      label="Spill Path" 
                      placeholder=".hermod-spill" 
                      size="xs"
                      description="Directory for spill files"
                      value={(sink.config || {}).spill_path || ''} 
                      onChange={(e) => updateConfig('spill_path', e.target.value)} 
                    />
                  )}

                  <Select
                    label="Dead Letter Sink"
                    placeholder="None"
                    size="xs"
                    data={(sinksResponse?.data || [])
                      .filter((s: any) => s.id !== initialData?.id)
                      .map((s: any) => ({ label: s.name, value: s.id }))}
                    value={(sink.config || {}).dlq_sink_id || ''}
                    onChange={(val) => updateConfig('dlq_sink_id', val || '')}
                    clearable
                  />
                </Stack>
              </ScrollArea>
              
              <Divider mt="md" />
              <Group justify="flex-end" pt="xs">
                {sink.type !== 'stdout' && (
                  <Button variant="outline" color="blue" size="xs" onClick={() => testMutation.mutate(sink)} loading={testMutation.isPending}>
                    Test Connection
                  </Button>
                )}
                <Button 
                  size="xs"
                  disabled={!sink.name}
                  onClick={() => {
                    submitMutation.mutate(sink);
                  }} 
                  loading={submitMutation.isPending}
                >
                  Confirm Configuration
                </Button>
              </Group>
            </Stack>
          </Card>
        </Grid.Col>

        {/* Column 3: Guide / Results */}
        <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
            <Stack h="100%">
              <Group gap="xs" px="xs">
                <IconInfoCircle size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700} c="dimmed">3. SETUP GUIDE & STATUS</Text>
              </Group>
              <Divider />
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
        title={<Text id={previewTitleId} fw={600}>Template Preview</Text>} 
        aria-labelledby={previewTitleId}
        aria-describedby={previewDescId}
        fullScreen
        withCloseButton
      >
        <Stack h="calc(100vh - 80px)">
          <Text id={previewDescId} size="sm" c="dimmed">
            This is a preview of your template rendered with sample data.
          </Text>
          {previewResult?.is_html ? (
            <Box flex={1} style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-md)', overflow: 'hidden' }}>
              <iframe 
                srcDoc={previewResult.rendered} 
                title="Preview" 
                style={{ width: '100%', height: '100%', border: 'none' }} 
              />
            </Box>
          ) : (
            <ScrollArea flex={1} style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-md)' }}>
              <Code block>{previewResult?.rendered}</Code>
            </ScrollArea>
          )}
          <Group justify="flex-end">
            <Button onClick={() => setPreviewModalOpen(false)}>Close</Button>
          </Group>
        </Stack>
      </Modal>
      </>
    );
  }

  return (
    <>
      <Grid gutter="lg" style={{ minHeight: 'calc(100vh - 160px)' }}>
        <Grid.Col span={{ base: 12, md: 6, lg: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group gap="xs" px="xs">
                <IconSettings size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700} c="dimmed">1. GENERAL SETTINGS</Text>
              </Group>
              <Divider />
              <Stack gap="sm">
                <SinkBasics
                  embedded={embedded}
                  name={sink.name}
                  onChangeName={(val: string) => handleSinkChange({ name: val })}
                  vhost={sink.vhost}
                  onChangeVHost={(val: string) => handleSinkChange({ vhost: val })}
                  workerId={sink.worker_id}
                  onChangeWorkerId={(val: string) => handleSinkChange({ worker_id: val })}
                  type={sink.type}
                  onChangeType={(val: string) => handleSinkChange({ type: val, config: { ...(sink.config || {}), format: (sink.config || {}).format || 'json' } })}
                  vhostOptions={availableVHostsList}
                  workerOptions={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
                  typeOptions={SINK_TYPES}
                />
                
                <Divider label="Reliability & Batching" labelPosition="center" />
                <RetryPolicyFields
                  maxRetries={(sink.config || {}).max_retries}
                  retryInterval={(sink.config || {}).retry_interval}
                  onChangeMaxRetries={(val) => updateConfig('max_retries', val)}
                  onChangeRetryInterval={(val) => updateConfig('retry_interval', val)}
                />

                <Group grow>
                  <TextInput 
                    label="Circuit Threshold" 
                    placeholder="5" 
                    size="xs"
                    description="Failures before opening"
                    value={(sink.config || {}).circuit_threshold || ''} 
                    onChange={(e) => updateConfig('circuit_threshold', e.target.value)} 
                  />
                  <TextInput 
                    label="Circuit Window" 
                    placeholder="1m" 
                    size="xs"
                    description="Error sliding window"
                    value={(sink.config || {}).circuit_interval || ''} 
                    onChange={(e) => updateConfig('circuit_interval', e.target.value)} 
                  />
                </Group>

                <Group grow>
                  <TextInput 
                    label="Circuit Cool-off" 
                    placeholder="30s" 
                    size="xs"
                    description="Time before retry"
                    value={(sink.config || {}).circuit_cool_off || ''} 
                    onChange={(e) => updateConfig('circuit_cool_off', e.target.value)} 
                  />
                  <TextInput 
                    label="Batch Size" 
                    placeholder="1" 
                    size="xs"
                    description="Max messages per write"
                    value={(sink.config || {}).batch_size || ''} 
                    onChange={(e) => updateConfig('batch_size', e.target.value)} 
                  />
                </Group>

                <Group grow>
                  <TextInput 
                    label="Batch Timeout" 
                    placeholder="100ms" 
                    size="xs"
                    description="Max wait for batch"
                    value={(sink.config || {}).batch_timeout || ''} 
                    onChange={(e) => updateConfig('batch_timeout', e.target.value)} 
                  />
                  <Select
                    label="Dead Letter Sink"
                    placeholder="None"
                    size="xs"
                    description="Target for failed msgs"
                    data={(sinksResponse?.data || [])
                      .filter((s: any) => s.id !== initialData?.id)
                      .map((s: any) => ({ label: s.name, value: s.id }))}
                    value={(sink.config || {}).dlq_sink_id || ''}
                    onChange={(val) => updateConfig('dlq_sink_id', val || '')}
                    clearable
                  />
                </Group>
                <Switch 
                    label="Adaptive Batching" 
                    size="xs"
                    checked={(sink.config || {}).adaptive_batching || false} 
                    onChange={(e) => updateConfig('adaptive_batching', e.currentTarget.checked)} 
                />
              </Stack>
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 6, lg: 4 }}>
          <Card withBorder shadow="md" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group justify="space-between" px="xs">
                <Group gap="xs">
                  <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
                  <Text size="sm" fw={700}>2. PARAMETERS</Text>
                </Group>
                <Badge variant="light" color="blue" size="lg" style={{ textTransform: 'uppercase' }}>{sink.type}</Badge>
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
                    onChange={(val) => updateConfig('format', val || 'json')}
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

        <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
            <Stack h="100%">
              <Group gap="xs" px="xs">
                <IconInfoCircle size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700} c="dimmed">3. SETUP GUIDE</Text>
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
        // Provide explicit labelling for screen readers
        title={<Text id={previewTitleId} fw={600}>Template Preview</Text>} 
        aria-labelledby={previewTitleId}
        aria-describedby={previewDescId}
        fullScreen
        withCloseButton
      >
        <Stack h="calc(100vh - 80px)">
          <Text id={previewDescId} size="sm" c="dimmed">
            This is a preview of your template rendered with sample data.
          </Text>
          {previewResult?.is_html ? (
            <Box flex={1} style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-md)', overflow: 'hidden' }}>
              <iframe 
                srcDoc={previewResult.rendered} 
                title="Preview" 
                style={{ width: '100%', height: '100%', border: 'none' }} 
              />
            </Box>
          ) : (
            <ScrollArea flex={1} style={{ border: '1px solid var(--mantine-color-gray-3)', borderRadius: 'var(--mantine-radius-md)' }}>
              <Code block>{previewResult?.rendered}</Code>
            </ScrollArea>
          )}
          <Group justify="flex-end">
            <Button onClick={() => setPreviewModalOpen(false)}>Close</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
