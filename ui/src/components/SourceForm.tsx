import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Text, Grid, Title, Code, List, Checkbox, TagsInput, ActionIcon, JsonInput, Badge, Loader, Modal, Card, ScrollArea, Switch } from '@mantine/core';
import { IconCheck, IconInfoCircle, IconRefresh, IconFileImport, IconLink, IconCloud, IconUpload, IconAlertCircle, IconBraces, IconCopy, IconSettings, IconPlayerPlay } from '@tabler/icons-react';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { Tabs, FileButton } from '@mantine/core';
import { notifications } from '@mantine/notifications';

const API_BASE = '/api';

const SOURCE_TYPES = [
  'postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'db2', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite', 'csv',
  'kafka', 'nats', 'redis', 'rabbitmq', 'rabbitmq_queue', 'webhook', 'cron'
];


interface SourceFormProps {
  initialData?: any;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  onRunSimulation?: (sample?: any) => void;
  vhost?: string;
  workerID?: string;
}

export function SourceForm({ initialData, isEditing = false, embedded = false, onSave, onRunSimulation, vhost, workerID }: SourceFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const [source, setSource] = useState<any>({ 
    name: '', 
    type: 'postgres', 
    vhost: '',
    worker_id: '',
    config: { 
      connection_string: '',
      host: '',
      port: '',
      user: '',
      password: '',
      dbname: '',
      tables: '',
      use_cdc: 'true',
      sslmode: 'disable',
      slot_name: 'hermod_slot',
      publication_name: 'hermod_pub',
      reconnect_intervals: '30s'
    }
  });

  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [discoveredTables, setDiscoveredTables] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);
  const [isFetchingTables, setIsFetchingTables] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [sampleData, setSampleData] = useState<any>(null);
  const [isFetchingSample, setIsFetchingSample] = useState(false);
  const [sampleError, setSampleError] = useState<string | null>(null);
  const [testInput, setTestInput] = useState<string>('');
  const [selectedSampleTable, setSelectedSampleTable] = useState<string>('');
  const [showSetup, setShowSetup] = useState(false);

  const isCDC = (type: string) => {
    return ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite', 'mariadb', 'db2', 'csv'].includes(type);
  };

  const useCDCChecked = source.config.use_cdc !== 'false';

  const fetchSample = async (s: any) => {
    let table = selectedSampleTable;
    if (!table && s.config.tables) {
      table = s.config.tables.split(',')[0].trim();
    }
    
    // For non-CDC, check if we have custom JSON input
    if (!isCDC(s.type) && testInput) {
      try {
        const data = JSON.parse(testInput);
        setSampleData(data);
        setSampleError(null);
        return;
      } catch (e) {
        // If invalid JSON, we might want to try backend anyway, or we could set an error
      }
    }

    setIsFetchingSample(true);
    setSampleError(null);
    try {
      const res = await apiFetch(`${API_BASE}/sources/sample`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source: s, table }),
      });
      if (res.ok) {
        const data = await res.json();
        setSampleData(data);
      }
    } catch (e: any) {
      setSampleData(null);
      setSampleError(e.message || 'Failed to fetch sample data');
    } finally {
      setIsFetchingSample(false);
    }
  };

  const handleFileUpload = async (file: File | null) => {
    if (!file) return;
    setUploading(true);
    const formData = new FormData();
    formData.append('file', file);
    try {
      const res = await apiFetch(`${API_BASE}/sources/upload`, {
        method: 'POST',
        body: formData,
      });
      if (res.ok) {
        const data = await res.json();
        updateConfig('file_path', data.path);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setUploading(false);
    }
  };

  const fetchDatabases = async () => {
    setIsFetchingDBs(true);
    try {
      const res = await apiFetch(`${API_BASE}/sources/discover/databases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(source),
      });
      if (res.ok) {
        const dbs = await res.json();
        setDiscoveredDatabases(dbs);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setIsFetchingDBs(false);
    }
  };

  const fetchTables = async (dbName?: string) => {
    setIsFetchingTables(true);
    try {
      const s = { 
        ...source, 
        config: { 
          ...source.config, 
          dbname: dbName || source.config.dbname || source.config.path // SQLite uses path
        } 
      };
      const res = await apiFetch(`${API_BASE}/sources/discover/tables`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(s),
      });
      if (res.ok) {
        const tables = await res.json();
        setDiscoveredTables(tables);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setIsFetchingTables(false);
    }
  };

  useEffect(() => {
    if (initialData) {
      setSource((prev: any) => {
        // Only update if the ID changed or we are initializing a new source
        if (prev.id !== initialData.id || (prev.name === '' && !prev.id)) {
          return {
            ...prev,
            ...initialData,
            config: {
              ...(prev.config || {}),
              ...(initialData.config || {}),
              reconnect_intervals: initialData.config?.reconnect_intervals || initialData.config?.reconnect_interval || prev.config?.reconnect_intervals || '30s',
            }
          };
        }
        return prev;
      });
      if (initialData.sample) {
        try {
          setSampleData(JSON.parse(initialData.sample));
        } catch (e) {
          console.error("Failed to parse sample data", e);
        }
      }
    }
  }, [initialData]);

  useEffect(() => {
    if (embedded) {
      if (vhost) setSource((prev: any) => ({ ...prev, vhost }));
      if (workerID) setSource((prev: any) => ({ ...prev, worker_id: workerID }));
    }
  }, [embedded, vhost, workerID]);

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
      const cleanConfig = Object.fromEntries(
        Object.entries(s.config).filter(([_, v]) => v !== '')
      );
      const res = await apiFetch(`${API_BASE}/sources/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...s, config: cleanConfig }),
      });
      return res.json();
    },
    onSuccess: (_, variables) => {
      setTestResult({ status: 'ok', message: 'Connection successful!' });
      fetchSample(variables);
    },
    onError: () => {
      setTestResult(null); // apiFetch handles notification
      setSampleData(null);
    }
  });

  const submitMutation = useMutation({
    mutationFn: async (s: any) => {
      const cleanConfig = Object.fromEntries(
        Object.entries(s.config).filter(([_, v]) => v !== '')
      );
      
      const res = await apiFetch(`${API_BASE}/sources${isEditing ? `/${initialData.id}` : ''}`, {
        method: isEditing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          ...s, 
          config: cleanConfig,
          sample: sampleData ? JSON.stringify(sampleData) : s.sample 
        }),
      });
      return res.json();
    },
    onSuccess: (data) => {
      if (embedded && onSave) {
        onSave(data);
      } else {
        navigate({ to: '/sources' });
      }
    },
    onError: () => {
        setTestResult(null); // apiFetch handles notification
    }
  });

  const handleSourceChange = (updates: any) => {
    const typeChanged = updates.type && updates.type !== source.type;
    setSource({ ...source, ...updates });
    setTestResult(null);
    setSampleData(null);
    if (typeChanged) {
      setTestInput('');
      setSelectedSampleTable('');
    }
  };

  const updateConfig = (key: string, value: string) => {
    setSource({
      ...source,
      config: { ...source.config, [key]: value }
    });
    setTestResult(null);
    setSampleData(null);
  };

  const renderConfigFields = () => {
    const cdcSwitch = (
      <Switch 
        label="Use CDC" 
        description="Enable Change Data Capture for this source."
        checked={useCDCChecked}
        onChange={(e) => updateConfig('use_cdc', e.target.checked ? 'true' : 'false')}
        mb="md"
      />
    );

    const databaseInput = (
      <Group align="flex-end">
        <Select 
          label="Database" 
          placeholder="Select or type dbname" 
          style={{ flex: 1 }}
          data={[...new Set([...discoveredDatabases, source.config.dbname].filter(Boolean))]}
          value={source.config.dbname} 
          onChange={(val) => {
            updateConfig('dbname', val || '');
            if (val) fetchTables(val);
          }}
          searchable
          onSearchChange={(val) => updateConfig('dbname', val)}
        />
        <ActionIcon variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Fetch Databases">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
    );

    const tablesInput = (
      <Group align="flex-end">
        <TagsInput 
          label="Tables" 
          placeholder="Select tables or type names" 
          style={{ flex: 1 }}
          data={discoveredTables}
          value={source.config.tables ? source.config.tables.split(',').map((s: string) => s.trim()).filter(Boolean) : []} 
          onChange={(val) => updateConfig('tables', val.join(', '))} 
          clearable
        />
        <ActionIcon variant="light" size="lg" onClick={() => fetchTables()} loading={isFetchingTables} disabled={!source.config.dbname && source.type !== 'sqlite'} title="Fetch Tables">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
    );

    const isDatabase = ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'yugabyte', 'mariadb', 'db2', 'cassandra', 'scylladb', 'clickhouse', 'sqlite'].includes(source.type);

    const commonFields = (
      <>
        {isDatabase && cdcSwitch}
        <Group grow>
          <TextInput label="Host" placeholder="localhost" value={source.config.host} onChange={(e) => updateConfig('host', e.target.value)} />
          <TextInput label="Port" placeholder="5432" value={source.config.port} onChange={(e) => updateConfig('port', e.target.value)} />
        </Group>
        <Group grow>
          <TextInput label="User" placeholder="user" value={source.config.user} onChange={(e) => updateConfig('user', e.target.value)} />
          <TextInput label="Password" type="password" placeholder="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
        </Group>
        {databaseInput}
        {tablesInput}
      </>
    );

    if (source.type === 'sqlite') {
      return (
        <>
          {cdcSwitch}
          <TextInput label="DB Path" placeholder="/path/to/hermod.db" value={source.config.path} onChange={(e) => updateConfig('path', e.target.value)} />
          {tablesInput}
        </>
      );
    }

    if (source.type === 'webhook') {
      return (
        <>
          <TextInput 
            label="Webhook Path" 
            placeholder="/api/webhooks/my-source" 
            value={source.config.path} 
            onChange={(e) => updateConfig('path', e.target.value)} 
            description="Relative path for the webhook. Full URL will be: http://hermod-host:8080/api/webhooks/YOUR_PATH"
            required 
          />
          <Select 
            label="HTTP Method" 
            data={['POST', 'PUT', 'GET']} 
            value={source.config.method || 'POST'} 
            onChange={(val) => updateConfig('method', val || 'POST')} 
          />
          <TextInput 
            label="API Key (Optional)" 
            placeholder="Header X-API-Key value" 
            value={source.config.api_key} 
            onChange={(e) => updateConfig('api_key', e.target.value)} 
            description="If provided, requests must include 'X-API-Key' header with this value."
          />
        </>
      );
    }

    if (source.type === 'cron') {
      return (
        <>
          <TextInput 
            label="Cron Schedule" 
            placeholder="*/5 * * * *" 
            value={source.config.schedule} 
            onChange={(e) => updateConfig('schedule', e.target.value)} 
            description="Standard cron expression or '@every 5m'. Example: '0 * * * *' (every hour)."
            required 
          />
            <JsonInput
            label="Static Payload"
            placeholder='{ "action": "trigger", "data": "hello" }'
            value={source.config.payload}
            onChange={(val) => updateConfig('payload', val)}
            description="The message payload that will be emitted when the cron triggers."
            minRows={10}
            formatOnBlur
          />
        </>
      );
    }

    if (source.type === 'csv') {
      return (
        <>
          <Tabs defaultValue={source.config.source_type || 'local'} onChange={(val) => updateConfig('source_type', val || 'local')}>
            <Tabs.List grow mb="md">
              <Tabs.Tab value="local" leftSection={<IconFileImport size="1rem" />}>Local File / Upload</Tabs.Tab>
              <Tabs.Tab value="http" leftSection={<IconLink size="1rem" />}>HTTP URL</Tabs.Tab>
              <Tabs.Tab value="s3" leftSection={<IconCloud size="1rem" />}>S3 Compatible</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="local">
              <Stack gap="xs">
                <TextInput 
                  label="File Path" 
                  placeholder="/path/to/data.csv" 
                  value={source.config.file_path} 
                  onChange={(e) => updateConfig('file_path', e.target.value)} 
                  required 
                />
                <Group justify="center">
                  <FileButton onChange={handleFileUpload} accept="text/csv">
                    {(props) => (
                      <Button {...props} variant="outline" leftSection={<IconUpload size="1rem" />} loading={uploading}>
                        Upload CSV File
                      </Button>
                    )}
                  </FileButton>
                </Group>
              </Stack>
            </Tabs.Panel>

            <Tabs.Panel value="http">
              <Stack gap="xs">
                <TextInput 
                  label="URL" 
                  placeholder="https://example.com/data.csv" 
                  value={source.config.url} 
                  onChange={(e) => updateConfig('url', e.target.value)} 
                  required 
                />
                <TextInput 
                  label="Headers" 
                  placeholder="Authorization: Bearer token, X-Custom: value" 
                  value={source.config.headers} 
                  onChange={(e) => updateConfig('headers', e.target.value)} 
                />
              </Stack>
            </Tabs.Panel>

            <Tabs.Panel value="s3">
              <Stack gap="xs">
                <Group grow>
                  <TextInput label="Region" placeholder="us-east-1" value={source.config.s3_region} onChange={(e) => updateConfig('s3_region', e.target.value)} />
                  <TextInput label="Bucket" placeholder="my-bucket" value={source.config.s3_bucket} onChange={(e) => updateConfig('s3_bucket', e.target.value)} />
                </Group>
                <TextInput label="Key (Path)" placeholder="data/file.csv" value={source.config.s3_key} onChange={(e) => updateConfig('s3_key', e.target.value)} />
                <TextInput label="Endpoint" placeholder="Optional (for S3 compatible storage)" value={source.config.s3_endpoint} onChange={(e) => updateConfig('s3_endpoint', e.target.value)} />
                <Group grow>
                  <TextInput label="Access Key" value={source.config.s3_access_key} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
                  <TextInput label="Secret Key" type="password" value={source.config.s3_secret_key} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
                </Group>
              </Stack>
            </Tabs.Panel>
          </Tabs>

          <Divider my="md" />
          
          <Group grow>
            <TextInput label="Delimiter" placeholder="," value={source.config.delimiter} onChange={(e) => updateConfig('delimiter', e.target.value)} maxLength={1} />
            <Checkbox 
              label="Has Header" 
              checked={source.config.has_header === 'true'} 
              onChange={(e) => updateConfig('has_header', e.target.checked ? 'true' : 'false')} 
              style={{ marginTop: 24 }}
            />
          </Group>
        </>
      );
    }

    if (source.type === 'sqlite') {
      return (
        <>
          <TextInput 
            label="Database File Path" 
            placeholder="hermod.db" 
            value={source.config.connection_string} 
            onChange={(e) => updateConfig('connection_string', e.target.value)} 
            required 
          />
          {tablesInput}
        </>
      );
    }

    if (source.type === 'csv') {
      return (
        <>
          <Select 
            label="CSV Source Type" 
            data={['file', 'http', 's3']} 
            value={source.config.csv_type || 'file'} 
            onChange={(val) => updateConfig('csv_type', val || 'file')} 
          />
          {source.config.csv_type === 'file' && (
            <TextInput label="File Path" placeholder="data.csv" value={source.config.file_path} onChange={(e) => updateConfig('file_path', e.target.value)} required />
          )}
          {source.config.csv_type === 'http' && (
            <TextInput label="URL" placeholder="https://example.com/data.csv" value={source.config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
          )}
          {source.config.csv_type === 's3' && (
            <>
              <TextInput label="Bucket" placeholder="my-bucket" value={source.config.s3_bucket} onChange={(e) => updateConfig('s3_bucket', e.target.value)} required />
              <TextInput label="Key" placeholder="data.csv" value={source.config.s3_key} onChange={(e) => updateConfig('s3_key', e.target.value)} required />
              <TextInput label="Region" placeholder="us-east-1" value={source.config.s3_region} onChange={(e) => updateConfig('s3_region', e.target.value)} />
              <TextInput label="Endpoint" placeholder="https://s3.amazonaws.com" value={source.config.s3_endpoint} onChange={(e) => updateConfig('s3_endpoint', e.target.value)} />
              <Group grow>
                <TextInput label="Access Key" value={source.config.s3_access_key} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
                <TextInput label="Secret Key" type="password" value={source.config.s3_secret_key} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
              </Group>
            </>
          )}
          <TextInput label="Delimiter" placeholder="," value={source.config.delimiter || ','} onChange={(e) => updateConfig('delimiter', e.target.value)} />
          <Checkbox label="Has Header" checked={source.config.has_header !== 'false'} onChange={(e) => updateConfig('has_header', e.target.checked ? 'true' : 'false')} />
        </>
      );
    }

    if (source.type === 'mongodb') {
      return (
        <>
          {cdcSwitch}
          <Group grow>
            <TextInput label="Host" placeholder="localhost" value={source.config.host} onChange={(e) => updateConfig('host', e.target.value)} />
            <TextInput label="Port" placeholder="27017" value={source.config.port} onChange={(e) => updateConfig('port', e.target.value)} />
          </Group>
          <Group grow>
            <TextInput label="User" placeholder="user" value={source.config.user} onChange={(e) => updateConfig('user', e.target.value)} />
            <TextInput label="Password" type="password" placeholder="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <TextInput label="Database" placeholder="my-db" value={source.config.database} onChange={(e) => updateConfig('database', e.target.value)} required />
          <TextInput label="Collection" placeholder="my-collection" value={source.config.collection} onChange={(e) => updateConfig('collection', e.target.value)} required />
          <TextInput 
            label="OR Connection URI (Overrides individual fields)" 
            placeholder="mongodb://..." 
            value={source.config.uri}
            onChange={(e) => updateConfig('uri', e.target.value)}
          />
        </>
      );
    }

    if (source.type === 'cassandra' || source.type === 'scylladb') {
      return (
        <>
          {cdcSwitch}
          <TextInput 
            label="Hosts" 
            placeholder="localhost:9042, localhost:9043" 
            value={source.config.hosts} 
            onChange={(e) => updateConfig('hosts', e.target.value)} 
            description="Comma-separated list of contact points."
            required 
          />
          <TextInput label="Keyspace" value={source.config.dbname} onChange={(e) => updateConfig('dbname', e.target.value)} required />
          {tablesInput}
        </>
      );
    }

    if (source.type === 'postgres' || source.type === 'yugabyte') {
      return (
        <>
          {commonFields}
          <TextInput label="SSL Mode" placeholder="disable" value={source.config.sslmode} onChange={(e) => updateConfig('sslmode', e.target.value)} />
          {useCDCChecked && (
            <Group grow>
              <TextInput label="Slot Name" value={source.config.slot_name} onChange={(e) => updateConfig('slot_name', e.target.value)} />
              <TextInput label="Publication Name" value={source.config.publication_name} onChange={(e) => updateConfig('publication_name', e.target.value)} />
            </Group>
          )}
        </>
      );
    }

    if (source.type === 'mssql') {
      return (
        <>
          {commonFields}
          {useCDCChecked && (
            <Checkbox 
              label="Auto Enable CDC" 
              description="Automatically enable CDC on the database and tables if not already enabled."
              checked={source.config.auto_enable_cdc !== 'false'} 
              onChange={(e) => updateConfig('auto_enable_cdc', e.target.checked ? 'true' : 'false')} 
            />
          )}
        </>
      );
    }

    if (source.type === 'kafka') {
      return (
        <>
          <TextInput label="Brokers" placeholder="localhost:9092" value={source.config.brokers} onChange={(e) => updateConfig('brokers', e.target.value)} required />
          <TextInput label="Topic" placeholder="topic" value={source.config.topic} onChange={(e) => updateConfig('topic', e.target.value)} required />
          <TextInput label="Group ID" placeholder="hermod-group" value={source.config.group_id} onChange={(e) => updateConfig('group_id', e.target.value)} required />
          <Group grow>
            <TextInput label="Username" value={source.config.username} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
        </>
      );
    }

    if (source.type === 'nats') {
      return (
        <>
          <TextInput label="URL" placeholder="nats://localhost:4222" value={source.config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Subject" placeholder="subject" value={source.config.subject} onChange={(e) => updateConfig('subject', e.target.value)} required />
          <Group grow>
            <TextInput label="Queue" placeholder="optional queue name" value={source.config.queue} onChange={(e) => updateConfig('queue', e.target.value)} />
            <TextInput label="Durable Name" placeholder="optional durable name" value={source.config.durable_name} onChange={(e) => updateConfig('durable_name', e.target.value)} description="Recommended for production to avoid data loss on reconnect." />
          </Group>
          <Group grow>
            <TextInput label="Username" value={source.config.username} onChange={(e) => updateConfig('username', e.target.value)} />
            <TextInput label="Password" type="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
          </Group>
          <TextInput label="Token" placeholder="optional token" value={source.config.token} onChange={(e) => updateConfig('token', e.target.value)} />
        </>
      );
    }

    if (source.type === 'redis') {
      return (
        <>
          <TextInput label="Address" placeholder="localhost:6379" value={source.config.addr} onChange={(e) => updateConfig('addr', e.target.value)} required />
          <TextInput label="Password" type="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
          <TextInput label="Stream" placeholder="stream-name" value={source.config.stream} onChange={(e) => updateConfig('stream', e.target.value)} required />
          <TextInput label="Group" placeholder="group-name" value={source.config.group} onChange={(e) => updateConfig('group', e.target.value)} required />
        </>
      );
    }

    if (source.type === 'rabbitmq') {
      return (
        <>
          <TextInput label="URL" placeholder="rabbitmq-stream://guest:guest@localhost:5552" value={source.config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Stream Name" placeholder="stream-name" value={source.config.stream_name} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
          <TextInput label="Consumer Name" placeholder="consumer-name" value={source.config.consumer_name} onChange={(e) => updateConfig('consumer_name', e.target.value)} required />
        </>
      );
    }

    if (source.type === 'rabbitmq_queue') {
      return (
        <>
          <TextInput label="URL" placeholder="amqp://guest:guest@localhost:5672" value={source.config.url} onChange={(e) => updateConfig('url', e.target.value)} required />
          <TextInput label="Queue Name" placeholder="queue-name" value={source.config.queue_name} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
        </>
      );
    }

    const getConnectionPlaceholder = () => {
      switch (source.type) {
        case 'mysql':
        case 'mariadb':
          return 'user:password@tcp(localhost:3306)/dbname';
        case 'mssql':
          return 'sqlserver://user:password@localhost:1433?database=dbname';
        case 'oracle':
          return 'oracle://user:password@localhost:1521/service_name';
        case 'clickhouse':
          return 'clickhouse://user:password@localhost:9000/dbname';
        case 'mongodb':
          return 'mongodb://user:password@localhost:27017';
        default:
          return 'postgres://user:password@localhost:5432/dbname';
      }
    };

    return (
      <>
        {commonFields}
        <TextInput 
          label="OR Connection String (Overrides individual fields)" 
          placeholder={getConnectionPlaceholder()} 
          value={source.config.connection_string}
          onChange={(e) => updateConfig('connection_string', e.target.value)}
        />
      </>
    );
  };


  const renderSetupInstructions = () => {
    if (!useCDCChecked) return null;
    switch (source.type) {
      case 'postgres':
      case 'yugabyte':
        return (
          <Stack gap="xs">
            <Title order={5}>{source.type === 'yugabyte' ? 'YugabyteDB' : 'PostgreSQL'} Setup</Title>
            <Text size="sm">To enable CDC, you need to:</Text>
            <List size="sm" withPadding>
              <List.Item>Set <Code>wal_level = logical</Code> in <Code>postgresql.conf</Code></List.Item>
              <List.Item>Restart the database</List.Item>
              <List.Item>Ensure the user has <Code>REPLICATION</Code> attributes or is a superuser</List.Item>
              <List.Item>Hermod will automatically create the publication and replication slot if they don't exist</List.Item>
            </List>
          </Stack>
        );
      case 'mysql':
      case 'mariadb':
        return (
          <Stack gap="xs">
            <Title order={5}>{source.type === 'mariadb' ? 'MariaDB' : 'MySQL'} Setup</Title>
            <Text size="sm">To enable CDC, you need to:</Text>
            <List size="sm" withPadding>
              <List.Item>Enable binary logging: <Code>log-bin=mysql-bin</Code></List.Item>
              <List.Item>Set <Code>binlog_format=ROW</Code></List.Item>
              <List.Item>Set <Code>binlog_row_image=FULL</Code></List.Item>
              <List.Item>Grant <Code>REPLICATION SLAVE</Code>, <Code>REPLICATION CLIENT</Code>, and <Code>SELECT</Code> permissions to the user</List.Item>
            </List>
          </Stack>
        );
      case 'mongodb':
        return (
          <Stack gap="xs">
            <Title order={5}>MongoDB Setup</Title>
            <Text size="sm">Hermod uses Change Streams which require a Replica Set or Sharded Cluster.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure your MongoDB instance is running as a Replica Set</List.Item>
              <List.Item>The user must have <Code>read</Code> permissions on the databases/collections</List.Item>
            </List>
          </Stack>
        );
      case 'mssql':
        return (
          <Stack gap="xs">
            <Title order={5}>SQL Server Setup</Title>
            <Text size="sm">Enable CDC on the database and tables. You can use the "Auto Enable CDC" option or run these manually:</Text>
            <Code block>
              {`EXEC sys.sp_cdc_enable_db;
GO
EXEC sys.sp_cdc_enable_table
  @source_schema = N'dbo',
  @source_name   = N'MyTable',
  @role_name     = NULL;
GO`}
            </Code>
            <Text size="sm" c="dimmed">Note: SQL Server Agent must be running for CDC to work.</Text>
          </Stack>
        );
      case 'oracle':
        return (
          <Stack gap="xs">
            <Title order={5}>Oracle Setup</Title>
            <Text size="sm">Hermod supports Oracle CDC via LogMiner:</Text>
            <List size="sm" withPadding>
              <List.Item>Enable Archivelog mode</List.Item>
              <List.Item>Enable supplemental logging: <Code>ALTER DATABASE ADD SUPPLEMENTAL LOG DATA;</Code></List.Item>
              <List.Item>Grant <Code>SELECT ANY TRANSACTION</Code> and <Code>EXECUTE_CATALOG_ROLE</Code> to the user</List.Item>
            </List>
          </Stack>
        );
      case 'db2':
        return (
          <Stack gap="xs">
            <Title order={5}>IBM DB2 Setup</Title>
            <Text size="sm">CDC for DB2 is supported via the IBM CDC replication engine or polling.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has <Code>DATAACCESS</Code> or similar permissions</List.Item>
              <List.Item>Tables must have <Code>DATA CAPTURE CHANGES</Code> enabled</List.Item>
            </List>
          </Stack>
        );
      case 'cassandra':
      case 'scylladb':
        return (
          <Stack gap="xs">
            <Title order={5}>{source.type === 'scylladb' ? 'ScyllaDB' : 'Cassandra'} Setup</Title>
            <Text size="sm">CDC must be enabled on the table:</Text>
            <Code block>
              {`ALTER TABLE my_table WITH cdc = true;`}
            </Code>
            <Text size="sm">Hermod will read from the CDC log tables.</Text>
          </Stack>
        );
      case 'clickhouse':
        return (
          <Stack gap="xs">
            <Title order={5}>ClickHouse Setup</Title>
            <Text size="sm">Hermod can read from ClickHouse tables.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has <Code>SELECT</Code> permissions</List.Item>
              <List.Item>CDC is supported via polling or specialized engines if configured</List.Item>
            </List>
          </Stack>
        );
      case 'sqlite':
        return (
          <Stack gap="xs">
            <Title order={5}>SQLite Setup</Title>
            <Text size="sm">Provide the path to the SQLite database file.</Text>
            <List size="sm" withPadding>
              <List.Item>Hermod reads directly from the file</List.Item>
              <List.Item>Ensure the worker process has read permissions on the file and its directory</List.Item>
            </List>
          </Stack>
        );
      case 'kafka':
        return (
          <Stack gap="xs">
            <Title order={5}>Kafka Source</Title>
            <Text size="sm">Consumes messages from a Kafka topic.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the list of brokers and the topic name</List.Item>
              <List.Item>Group ID is used for offset management</List.Item>
              <List.Item>Supports SASL/Plain authentication</List.Item>
            </List>
          </Stack>
        );
      case 'nats':
        return (
          <Stack gap="xs">
            <Title order={5}>NATS Source</Title>
            <Text size="sm">Subscribes to a NATS subject.</Text>
            <List size="sm" withPadding>
              <List.Item>Specify the NATS server URL and Subject</List.Item>
              <List.Item>Optional Queue Group for load balancing across workers</List.Item>
            </List>
          </Stack>
        );
      case 'redis':
        return (
          <Stack gap="xs">
            <Title order={5}>Redis Source</Title>
            <Text size="sm">Reads from a Redis Stream.</Text>
            <List size="sm" withPadding>
              <List.Item>Provide the stream name and consumer group</List.Item>
              <List.Item>Hermod will manage offsets within the group</List.Item>
            </List>
          </Stack>
        );
      case 'rabbitmq':
        return (
          <Stack gap="xs">
            <Title order={5}>RabbitMQ Stream Source</Title>
            <Text size="sm">Reads from a RabbitMQ Stream.</Text>
            <List size="sm" withPadding>
              <List.Item>Requires RabbitMQ 3.9+ with the stream plugin enabled</List.Item>
              <List.Item>URL format: <Code>rabbitmq-stream://guest:guest@localhost:5552</Code></List.Item>
            </List>
          </Stack>
        );
      case 'rabbitmq_queue':
        return (
          <Stack gap="xs">
            <Title order={5}>RabbitMQ Queue Source</Title>
            <Text size="sm">Consumes from a standard RabbitMQ queue (AMQP).</Text>
            <List size="sm" withPadding>
              <List.Item>URL format: <Code>amqp://guest:guest@localhost:5672</Code></List.Item>
              <List.Item>Hermod will consume messages from the specified queue</List.Item>
            </List>
          </Stack>
        );
      case 'csv':
        return (
          <Stack gap="xs">
            <Title order={5}>CSV Source</Title>
            <Text size="sm">Reads data from a CSV file (Local, HTTP, or S3).</Text>
            <List size="sm" withPadding>
              <List.Item><b>Local:</b> Specify path or upload a file.</List.Item>
              <List.Item><b>HTTP:</b> Provide a URL to a CSV file. Optional custom headers for auth.</List.Item>
              <List.Item><b>S3:</b> Connect to AWS S3 or compatible storage (MinIO, etc).</List.Item>
              <List.Item>If "Has Header" is enabled, first row will be used as field names.</List.Item>
            </List>
          </Stack>
        );
      default:
        return (
          <Group gap="xs" c="dimmed">
            <IconInfoCircle size="1.2rem" />
            <Text size="sm">Select a source type to see setup instructions.</Text>
          </Group>
        );
    }
  };

  return (
    <>
      <Modal 
        opened={showSetup} 
        onClose={() => setShowSetup(false)} 
        title={`${source.type.toUpperCase()} Setup Instructions`}
        size="lg"
        radius="md"
      >
        {renderSetupInstructions()}
      </Modal>
      <Grid gutter="md" style={{ minHeight: 'calc(100vh - 160px)' }}>
        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group gap="xs">
                <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700}>1. SAMPLE INPUT</Text>
              </Group>
              <Divider />
              {isCDC(source.type) ? (
                <Select 
                  label="Table to Sample" 
                  placeholder="Select a table" 
                  data={(source.config.tables || '').split(',').map((t: string) => t.trim()).filter(Boolean)}
                  value={selectedSampleTable}
                  onChange={(val) => setSelectedSampleTable(val || '')}
                  description="Select which table to use for the sample data during test connection."
                />
              ) : (
                <Stack gap="xs" style={{ flex: 1 }}>
                  <Group justify="space-between" align="flex-end">
                    <Text size="sm" fw={500}>Mock Sample Data (JSON)</Text>
                    <Button 
                      size="compact-xs" 
                      variant="subtle" 
                      leftSection={<IconBraces size="0.8rem" />}
                      onClick={() => {
                        try {
                          setTestInput(JSON.stringify(JSON.parse(testInput), null, 2));
                        } catch (e) {
                          notifications.show({ title: 'Invalid JSON', message: 'Could not format invalid JSON.', color: 'red' });
                        }
                      }}
                    >
                      Format
                    </Button>
                  </Group>
                  <JsonInput 
                    placeholder='{ "id": 1, "name": "Test" }'
                    value={testInput}
                    onChange={setTestInput}
                    formatOnBlur
                    minRows={18}
                    styles={{ 
                      root: { flex: 1, display: 'flex', flexDirection: 'column' },
                      wrapper: { flex: 1, display: 'flex', flexDirection: 'column' },
                      input: { flex: 1, fontFamily: 'monospace', fontSize: '11px' } 
                    }}
                    description="Provide JSON data to use as a sample for testing transformations if live sampling is not available."
                  />
                </Stack>
              )}
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconSettings size="1.2rem" color="var(--mantine-color-gray-7)" />
                  <Text size="sm" fw={700}>2. CONFIGURATION</Text>
                </Group>
                <Badge variant="dot" color="blue" style={{ textTransform: 'uppercase' }}>{source.type}</Badge>
              </Group>
              <Divider />
              <ScrollArea flex={1} mx="-md" px="md">
                <Stack gap="md" py="xs">
                  {testResult && testResult.status === 'ok' && (
                    <Alert 
                      icon={<IconCheck size="1rem" />} 
                      title="Success" 
                      color="green"
                      withCloseButton
                      onClose={() => setTestResult(null)}
                    >
                      {testResult.message}
                    </Alert>
                  )}
                  <TextInput 
                    label="Name" 
                    placeholder="Production DB" 
                    value={source.name}
                    onChange={(e) => handleSourceChange({ name: e.target.value })}
                    required
                  />
                  {!embedded && (
                    <Select 
                      label="VHost" 
                      placeholder="Select a virtual host" 
                      data={availableVHostsList}
                      value={source.vhost}
                      onChange={(val) => handleSourceChange({ vhost: val || '' })}
                      required
                    />
                  )}
                  {!embedded && (
                    <Select 
                      label="Worker (Optional)" 
                      placeholder="Assign to a specific worker" 
                      data={(workers || []).map((w: any) => ({ value: w.id, label: w.name || w.id }))}
                      value={source.worker_id}
                      onChange={(val) => handleSourceChange({ worker_id: val || '' })}
                      clearable
                    />
                  )}
                  <Group align="flex-end" grow>
                    <Select 
                      label="Type" 
                      data={SOURCE_TYPES} 
                      value={source.type}
                      onChange={(val) => handleSourceChange({ type: val || '' })}
                      required
                    />
                    <Button 
                      variant="light" 
                      color="blue" 
                      leftSection={<IconInfoCircle size="1rem" />}
                      onClick={() => setShowSetup(true)}
                      style={{ flex: 'none' }}
                    >
                      Setup Guide
                    </Button>
                  </Group>
                  
                  <Divider label="Parameters" labelPosition="center" />
                  {renderConfigFields()}

                  <Divider label="Reliability" labelPosition="center" mt="md" />
                  <TextInput 
                    label="Reconnect Intervals" 
                    placeholder="1s, 5s, 30s, 1m" 
                    description="Comma-separated list of durations for reconnection attempts. If one value is provided (e.g., '10s'), it repeats indefinitely. If multiple are provided (e.g., '1s, 1m'), the last one repeats indefinitely."
                    value={source.config.reconnect_intervals || ''} 
                    onChange={(e) => updateConfig('reconnect_intervals', e.target.value)} 
                  />
                </Stack>
              </ScrollArea>

              <Divider mt="md" />
              <Group justify="flex-end" pt="xs">
                {!embedded && <Button variant="outline" size="xs" onClick={() => navigate({ to: '/sources' })}>Cancel</Button>}
                <Button variant="outline" color="blue" size="xs" onClick={() => testMutation.mutate(source)} loading={testMutation.isPending}>Test Connection</Button>
                <Button 
                  size="xs"
                  disabled={testResult?.status !== 'ok' || !source.name || (!embedded && !source.vhost)}
                  onClick={() => {
                    submitMutation.mutate(source);
                  }} 
                  loading={submitMutation.isPending}
                >
                  {isEditing ? 'Save Changes' : (embedded ? 'Confirm' : 'Create Source')}
                </Button>
              </Group>
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconFileImport size="1.2rem" color="var(--mantine-color-green-6)" />
                  <Text size="sm" fw={700}>3. LIVE PREVIEW</Text>
                </Group>
                {sampleData && <Badge color="green" variant="light">Captured</Badge>}
              </Group>
              <Divider />
              
              {isFetchingSample ? (
                <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
                  <Loader size="sm" />
                  <Text size="xs" c="dimmed">Fetching sample data...</Text>
                </Stack>
              ) : sampleError ? (
                <Stack align="center" py="md" gap="xs" style={{ flex: 1, justifyContent: 'center' }}>
                  <IconAlertCircle size="1.5rem" color="red" />
                  <Text size="xs" c="red" ta="center">{sampleError}</Text>
                  <Button size="xs" variant="subtle" color="red" onClick={() => fetchSample(source)}>Retry</Button>
                </Stack>
              ) : sampleData ? (
                <Stack gap="xs" style={{ flex: 1 }}>
                  <Group justify="space-between" align="flex-end">
                    <Text size="sm" fw={500}>Sample Output (JSON)</Text>
                    <Group gap="xs">
                      <Button 
                        size="compact-xs" 
                        variant="subtle" 
                        leftSection={<IconRefresh size="0.8rem" />}
                        onClick={() => fetchSample(source)}
                      >
                        Refresh
                      </Button>
                      <Button 
                        size="compact-xs" 
                        variant="subtle" 
                        leftSection={<IconCopy size="0.8rem" />}
                        onClick={() => {
                          navigator.clipboard.writeText(JSON.stringify(sampleData, null, 2));
                          notifications.show({ title: 'Copied', message: 'Sample data copied to clipboard.', color: 'blue' });
                        }}
                      >
                        Copy
                      </Button>
                      {onRunSimulation && (
                        <Button 
                          size="compact-xs" 
                          variant="light" 
                          color="green"
                          leftSection={<IconPlayerPlay size="0.8rem" />}
                          onClick={() => onRunSimulation(sampleData)}
                        >
                          Run Simulation
                        </Button>
                      )}
                    </Group>
                  </Group>
                  <JsonInput 
                    value={JSON.stringify(sampleData, null, 2)}
                    readOnly
                    styles={{ 
                      root: { flex: 1, display: 'flex', flexDirection: 'column' },
                      wrapper: { flex: 1, display: 'flex', flexDirection: 'column' },
                      input: { flex: 1, fontFamily: 'monospace', fontSize: '11px', backgroundColor: 'var(--mantine-color-gray-0)' } 
                    }}
                  />
                </Stack>
              ) : (
                <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
                  <IconInfoCircle size="2rem" color="var(--mantine-color-gray-4)" />
                  <Text size="xs" c="dimmed" ta="center">Test connection to see sample data from your source.</Text>
                  <Button size="xs" variant="light" onClick={() => fetchSample(source)}>Fetch Sample Now</Button>
                </Stack>
              )}
            </Stack>
          </Card>
        </Grid.Col>
      </Grid>
    </>
  );
}
