import { useState, useEffect, useRef } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Text, Grid, Title, Code, List, Checkbox, TagsInput, ActionIcon, JsonInput, Badge, Loader, Modal, Card, ScrollArea, Switch } from '@mantine/core';
import { IconCheck, IconInfoCircle, IconRefresh, IconFileImport, IconLink, IconCloud, IconUpload, IconAlertCircle, IconBraces, IconCopy, IconSettings, IconPlayerPlay, IconPlus, IconHistory, IconChevronRight } from '@tabler/icons-react';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { useForm, useStore } from '@tanstack/react-form';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import { Tabs, FileButton } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { SapSourceConfig } from './Source/SapSourceConfig';
import { MainframeSourceConfig } from './Source/MainframeSourceConfig';
import { GenerateToken } from './GenerateToken';

const API_BASE = '/api';

const SOURCE_TYPES = [
  'postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'db2', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite', 'csv',
  'kafka', 'nats', 'redis', 'rabbitmq', 'rabbitmq_queue', 'webhook', 'form', 'cron', 'googlesheets', 'batch_sql', 'eventstore', 'graphql', 'grpc', 'sap', 'mainframe'
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
  const form = useForm({
    defaultValues: {
      name: initialData?.name || '', 
      type: initialData?.type || 'postgres', 
      vhost: (embedded ? vhost : (initialData?.vhost || vhost)) || '', 
      worker_id: (embedded ? workerID : (initialData?.worker_id || workerID)) || '',
      active: initialData?.active ?? true,
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
        reconnect_intervals: '30s',
        ...(initialData?.config || {})
      },
      ...(initialData?.id ? { id: initialData.id } : {})
    }
  });

  const source = useStore(form.store, (state) => state.values);

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
  // Accessibility: IDs for setup modal title/description
  const setupTitleId = 'source-setup-modal-title';
  const setupDescId = 'source-setup-modal-desc';
  // CDC reuse prompt state (PostgreSQL)
  const [cdcReusePrompt, setCdcReusePrompt] = useState<null | {
    slot?: { name: string; exists: boolean; active?: boolean; hermod_in_use: boolean };
    publication?: { name: string; exists: boolean; hermod_in_use: boolean };
  }>(null);

  const isCDC = (type: string) => {
    return ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite', 'mariadb', 'db2', 'csv'].includes(type);
  };

  const useCDCChecked = source.config?.use_cdc !== 'false';

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
          dbname: dbName || source.config?.dbname || source.config?.path // SQLite uses path
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

  const lastInitialDataId = useRef<string | null>(null);

  useEffect(() => {
    if (initialData) {
      if (lastInitialDataId.current !== (initialData.id || 'new')) {
        const newValues = {
          ...source,
          ...initialData,
          config: {
            ...(source.config || {}),
            ...(initialData.config || {}),
            reconnect_intervals: initialData.config?.reconnect_intervals || initialData.config?.reconnect_interval || source.config?.reconnect_intervals || '30s',
          }
        };
        form.reset(newValues);
        lastInitialDataId.current = initialData.id || 'new';
      }
      if (initialData.sample) {
        try {
          setSampleData(JSON.parse(initialData.sample));
        } catch (e) {
          console.error("Failed to parse sample data", e);
        }
      }
    }
  }, [initialData, form]);

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

  const { data: sourcesResponse } = useSuspenseQuery<any>({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (res.ok) return res.json();
      return { data: [], total: 0 };
    }
  });

  const vhosts = Array.isArray(vhostsResponse?.data) ? vhostsResponse.data : [];
  const workers = Array.isArray(workersResponse?.data) ? workersResponse.data : [];
  const allSources = Array.isArray(sourcesResponse?.data) ? sourcesResponse.data : [];

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
    onError: (e: any) => {
      const data = e?.data;
      if (e?.status === 409 && data?.code === 'CDC_REUSE_PROMPT') {
        setCdcReusePrompt({ slot: data.slot, publication: data.publication });
        return;
      }
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
    Object.entries(updates).forEach(([key, value]) => {
      form.setFieldValue(key as any, value);
    });
    setTestResult(null);
    setSampleData(null);
    if (typeChanged) {
      setTestInput('');
      setSelectedSampleTable('');
    }
  };

  const updateConfig = (key: string, value: string) => {
    form.setFieldValue(`config.${key}` as any, value);
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
        <ActionIcon aria-label="Discover databases" variant="light" size="lg" onClick={() => fetchDatabases()} loading={isFetchingDBs} title="Fetch Databases">
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
        <ActionIcon aria-label="Refresh tables" variant="light" size="lg" onClick={() => fetchTables()} loading={isFetchingTables} disabled={!source.config.dbname && source.type !== 'sqlite'} title="Fetch Tables">
          <IconRefresh size="1.2rem" />
        </ActionIcon>
      </Group>
    );

    const isDatabase = ['postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'yugabyte', 'mariadb', 'db2', 'cassandra', 'scylladb', 'clickhouse', 'sqlite', 'eventstore'].includes(source.type);

    const commonFields = (
      <>
        {isDatabase && source.type !== 'eventstore' && cdcSwitch}
        {source.type === 'eventstore' && (
          <Select
            label="Driver"
            placeholder="Select database driver"
            data={[
              { value: 'postgres', label: 'PostgreSQL' },
              { value: 'mysql', label: 'MySQL' },
              { value: 'sqlite', label: 'SQLite' },
              { value: 'sqlserver', label: 'SQL Server' },
            ]}
            value={source.config.driver}
            onChange={(val) => updateConfig('driver', val || '')}
            required
            mb="md"
          />
        )}
        <Group grow>
          <TextInput 
            label="Host" 
            placeholder="localhost" 
            value={source.config.host} 
            onChange={(e) => updateConfig('host', e.target.value)} 
            description="IP address or hostname of the database server"
          />
          <TextInput 
            label="Port" 
            placeholder="5432" 
            value={source.config.port} 
            onChange={(e) => updateConfig('port', e.target.value)} 
            description="Port number (e.g. 5432 for Postgres)"
          />
        </Group>
        <Group grow>
          <TextInput label="User" placeholder="user" value={source.config.user} onChange={(e) => updateConfig('user', e.target.value)} />
          <TextInput label="Password" type="password" placeholder="password" value={source.config.password} onChange={(e) => updateConfig('password', e.target.value)} />
        </Group>
        {databaseInput}
        {source.type === 'eventstore' ? (
          <>
            <TextInput 
              label="From Offset" 
              placeholder="0" 
              value={source.config.from_offset} 
              onChange={(e) => updateConfig('from_offset', e.target.value)} 
            />
            <TextInput 
              label="Stream ID Filter" 
              placeholder="Leave empty for all streams" 
              value={source.config.stream_id} 
              onChange={(e) => updateConfig('stream_id', e.target.value)} 
            />
            <TextInput 
              label="Poll Interval" 
              placeholder="1s" 
              value={source.config.poll_interval} 
              onChange={(e) => updateConfig('poll_interval', e.target.value)} 
            />
          </>
        ) : (source.type !== 'eventstore' && tablesInput)}
        {isDatabase && source.type !== 'eventstore' && source.config.use_cdc !== 'false' && (
          <Group grow>
            <TextInput 
              label="ID Field (for Polling/Delta)" 
              placeholder="id" 
              value={source.config.id_field} 
              onChange={(e) => updateConfig('id_field', e.target.value)} 
              description="Incremental field to track changes"
            />
            <TextInput 
              label="Poll Interval" 
              placeholder="5s" 
              value={source.config.poll_interval} 
              onChange={(e) => updateConfig('poll_interval', e.target.value)} 
              description="How often to check for new data"
            />
          </Group>
        )}
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

    if (source.type === 'googlesheets') {
      return (
        <>
          <TextInput 
            label="Spreadsheet ID" 
            placeholder="1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms" 
            value={source.config.spreadsheet_id} 
            onChange={(e) => updateConfig('spreadsheet_id', e.target.value)} 
            required 
          />
          <TextInput 
            label="Range" 
            placeholder="Sheet1!A1:Z" 
            value={source.config.range} 
            onChange={(e) => updateConfig('range', e.target.value)} 
            required 
          />
          <JsonInput
            label="Credentials JSON"
            placeholder='{ "type": "service_account", ... }'
            value={source.config.credentials_json}
            onChange={(val: string) => updateConfig('credentials_json', val)}
            required
            minRows={5}
            formatOnBlur
          />
          <TextInput 
            label="Poll Interval" 
            placeholder="1m" 
            value={source.config.poll_interval} 
            onChange={(e) => updateConfig('poll_interval', e.target.value)} 
          />
        </>
      );
    }

    if (source.type === 'batch_sql') {
      return (
        <>
          <Select 
            label="Database Source" 
            placeholder="Select source to run queries against"
            data={allSources
              .filter((s: any) => ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'sqlite', 'clickhouse'].includes(s.type))
              .map((s: any) => ({ value: s.id, label: `${s.name} (${s.type})` }))}
            value={source.config.source_id}
            onChange={(val) => updateConfig('source_id', val || '')}
            required
            mb="md"
          />
          <TextInput 
            label="Cron Schedule" 
            placeholder="*/5 * * * *" 
            value={source.config.cron} 
            onChange={(e) => updateConfig('cron', e.target.value)} 
            required
            description="Standard cron expression (e.g. */5 * * * * for every 5 minutes)"
            mb="md"
          />
          <TextInput 
            label="Incremental Column" 
            placeholder="id or created_at" 
            value={source.config.incremental_column} 
            onChange={(e) => updateConfig('incremental_column', e.target.value)} 
            description="Column used to track progress between runs"
            mb="md"
          />
          <Stack gap="xs">
            <Text size="sm" fw={500}>SQL Queries</Text>
            <Text size="xs" c="dimmed">Use {"{{.last_value}}"} to reference the last seen value from the incremental column.</Text>
            <JsonInput
              placeholder='["SELECT * FROM users WHERE id > {{.last_value}}", "SELECT * FROM orders WHERE id > {{.last_value}}"]'
              value={source.config.queries}
              onChange={(val) => updateConfig('queries', val)}
              minRows={4}
              formatOnBlur
              mb="md"
            />
          </Stack>
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
            description="Relative path for the webhook. Full URL will be: http://hermod-host:8080/api/webhooks/YOUR_PATH. Supports LZ4, Snappy, and Zstd compression via Content-Encoding header."
            required 
          />
          <Select 
            label="HTTP Method" 
            data={['POST', 'PUT', 'GET']} 
            value={source.config.method || 'POST'} 
            onChange={(val) => updateConfig('method', val || 'POST')} 
          />
          <Stack gap="xs">
            <GenerateToken 
              label="API Key (Optional)"
              value={source.config.api_key || ''}
              onChange={(val) => updateConfig('api_key', val)}
            />
            <Text size="xs" c="dimmed">If provided, requests must include 'X-API-Key' header with this value.</Text>
          </Stack>
        </>
      );
    }

    if (source.type === 'graphql') {
      return (
        <>
          <TextInput 
            label="GraphQL Path" 
            placeholder="/api/graphql/my-source" 
            value={source.config.path} 
            onChange={(e) => updateConfig('path', e.target.value)} 
            description="Relative path for the GraphQL endpoint. Full URL will be: http://hermod-host:8080/api/graphql/YOUR_PATH"
            required 
          />
          <Stack gap="xs">
            <GenerateToken 
              label="API Key (Optional)"
              value={source.config.api_key || ''}
              onChange={(val) => updateConfig('api_key', val)}
            />
            <Text size="xs" c="dimmed">If provided, requests must include 'X-API-Key' header with this value.</Text>
          </Stack>
        </>
      );
    }

    if (source.type === 'grpc') {
      return (
        <>
          <TextInput 
            label="gRPC Source Path" 
            placeholder="/grpc/default" 
            value={source.config.path} 
            onChange={(e) => updateConfig('path', e.target.value)} 
            description="Logical path used to route gRPC Publish calls. Set this in your PublishRequest.path field."
            required 
          />
          <TextInput 
            label="gRPC Address" 
            value="hermod-host:50051" 
            readOnly
            description="The gRPC server is listening on port 50051. Use service 'hermod.source.grpc.v1.SourceService' and method 'Publish'."
          />
        </>
      );
    }

    if (source.type === 'form') {
      return (
        <>
          <TextInput 
            label="Form Endpoint Path" 
            placeholder="/api/forms/contact-us" 
            value={source.config.path} 
            onChange={(e) => updateConfig('path', e.target.value)} 
            description="Relative path for the form endpoint. Full URL will be: http://hermod-host:8080/api/forms/YOUR_PATH"
            required 
          />
          <Select 
            label="HTTP Method" 
            data={['POST', 'GET']} 
            value={source.config.method || 'POST'} 
            onChange={(val) => updateConfig('method', val || 'POST')} 
          />
          <Stack gap="xs">
            <GenerateToken 
              label="Secret (Optional)"
              value={source.config.secret || ''}
              onChange={(val) => updateConfig('secret', val)}
            />
            <Text size="xs" c="dimmed">If provided, requests should include 'X-Form-Signature' header. JSON, x-www-form-urlencoded, and multipart are supported.</Text>
            <Text size="xs" c="dimmed">Example cURL: curl -X POST -F name=John -F email=john@example.com http://localhost:8080{source.config.path || '/api/forms/your-path'}</Text>
          </Stack>

          <Divider my="md" label="Public Form (Auto-generated)" />
          <Stack gap="sm">
            <Text size="sm" c="dimmed">Configure fields below; Hermod will generate a beautiful public form at the URL shown. You can embed or share it.</Text>

            {/* Field Builder */}
            <FieldBuilder 
              value={safeParseJSON(source.config.fields) || []}
              onChange={(list) => updateConfig('fields', JSON.stringify(list))}
            />

            <Grid>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <Switch 
                  label="Allow public submissions (no signature required)"
                  checked={(source.config.allow_public_form || 'false') === 'true'}
                  onChange={(e) => updateConfig('allow_public_form', e.currentTarget.checked ? 'true' : 'false')}
                />
              </Grid.Col>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <Switch 
                  label="Bot protection (token + honeypot)"
                  checked={(source.config.enable_bot_protection ?? 'true') !== 'false'}
                  onChange={(e) => updateConfig('enable_bot_protection', e.currentTarget.checked ? 'true' : 'false')}
                />
              </Grid.Col>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <TextInput 
                  label="Redirect URL (optional)" 
                  placeholder="https://example.com/thanks" 
                  value={source.config.redirect_url || ''}
                  onChange={(e) => updateConfig('redirect_url', e.target.value)}
                />
              </Grid.Col>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <TextInput 
                  label="Min submit time (ms)"
                  placeholder="1200"
                  value={source.config.bot_min_submit_ms ?? ''}
                  onChange={(e) => updateConfig('bot_min_submit_ms', e.target.value)}
                  description="Helps block bots by requiring a minimal time before submit."
                />
              </Grid.Col>
              <Grid.Col span={12}>
                <TextInput 
                  label="Success Message" 
                  placeholder="Thank you! Your submission has been received."
                  value={source.config.success_message || ''}
                  onChange={(e) => updateConfig('success_message', e.target.value)}
                />
              </Grid.Col>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <TextInput 
                  label="Turnstile Site Key"
                  placeholder="0x4AAAAAA..."
                  value={source.config.turnstile_site_key || ''}
                  onChange={(e) => updateConfig('turnstile_site_key', e.target.value)}
                  description="Required for Cloudflare Turnstile bot protection."
                />
              </Grid.Col>
              <Grid.Col span={{ base: 12, sm: 6 }}>
                <TextInput 
                  label="Turnstile Secret Key"
                  placeholder="0x4AAAAAA..."
                  value={source.config.turnstile_secret || ''}
                  onChange={(e) => updateConfig('turnstile_secret', e.target.value)}
                  description="Keep this secret. Used for backend verification."
                  type="password"
                />
              </Grid.Col>
            </Grid>

            <PublicFormLink path={source.config.path} />
          </Stack>
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

    if (source.type === 'sap') {
      return <SapSourceConfig config={source.config} updateConfig={updateConfig} />;
    }

    if (source.type === 'mainframe') {
      return <MainframeSourceConfig config={source.config} updateConfig={updateConfig} />;
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
      case 'sap':
        return (
          <Stack gap="xs">
            <Title order={5}>SAP OData Setup</Title>
            <Text size="sm">Poll data from SAP via OData services.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the OData service is activated in <Code>/IWFND/MAINT_SERVICE</Code>.</List.Item>
              <List.Item>The user needs authorizations to call the OData service and read the data.</List.Item>
              <List.Item>For delta polling, use the <Code>$filter</Code> parameter with a timestamp field.</List.Item>
            </List>
          </Stack>
        );
      case 'mainframe':
        return (
          <Stack gap="xs">
            <Title order={5}>Mainframe Integration</Title>
            <Text size="sm">Connect to Mainframe systems (Z/OS) via DB2 or VSAM wrappers.</Text>
            <List size="sm" withPadding>
              <List.Item>For DB2, ensure the IBM DB2 driver is accessible.</List.Item>
              <List.Item>For VSAM, a REST/OData wrapper is recommended for modern connectivity.</List.Item>
              <List.Item>Specify the schema and table for DB2 sources.</List.Item>
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
      case 'eventstore':
        return (
          <Stack gap="xs">
            <Title order={5}>Event Store Source</Title>
            <Text size="sm">Replays events from the Hermod Event Store.</Text>
            <List size="sm" withPadding>
              <List.Item>Used for rebuilding projections (CQRS)</List.Item>
              <List.Item>Select the database driver and connection details</List.Item>
              <List.Item>Specify <Code>From Offset</Code> to start replaying from a specific point</List.Item>
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
        // Provide explicit labelling for screen readers
        title={<Text id={setupTitleId} fw={600}>{`${source.type.toUpperCase()} Setup Instructions`}</Text>}
        aria-labelledby={setupTitleId}
        aria-describedby={setupDescId}
        size="lg"
        radius="md"
        withCloseButton
      >
        <Stack>
          <Text id={setupDescId} size="sm" c="dimmed">
            Follow these steps to configure the selected source type.
          </Text>
          {renderSetupInstructions()}
        </Stack>
      </Modal>
      <Grid gutter="lg" style={{ minHeight: 'calc(100vh - 180px)' }}>
        <Grid.Col span={{ base: 12, md: 4, lg: 3 }}>
          <Card withBorder shadow="sm" radius="md" p="md" h="100%">
            <Stack h="100%">
              <Group gap="xs" px="xs">
                <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
                <Text size="sm" fw={700} c="dimmed">1. SAMPLE INPUT</Text>
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

        <Grid.Col span={{ base: 12, md: 8, lg: 5 }}>
          <Card withBorder shadow="md" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
            <Stack h="100%" gap="md">
              <Group justify="space-between" px="xs">
                <Group gap="xs">
                  <IconSettings size="1.2rem" color="var(--mantine-color-blue-6)" />
                  <Text size="sm" fw={700}>2. CONFIGURATION</Text>
                </Group>
                <Badge variant="light" color="blue" size="lg" style={{ textTransform: 'uppercase' }}>{source.type}</Badge>
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

        <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
          {source.type === 'webhook' && source.config.path ? (
            <WebhookHistory 
              path={source.config.path} 
              onReplaySuccess={() => {
                notifications.show({ title: 'Replayed', message: 'Webhook replayed successfully.', color: 'green' });
              }}
              onSelectSample={(body) => {
                 try {
                    setSampleData(JSON.parse(body));
                 } catch (e) {
                    setSampleData(body);
                 }
              }}
            />
          ) : (
            <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
              <Stack h="100%">
                <Group justify="space-between" px="xs">
                  <Group gap="xs">
                    <IconFileImport size="1.2rem" color="var(--mantine-color-green-6)" />
                    <Text size="sm" fw={700} c="dimmed">3. LIVE PREVIEW</Text>
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
          )}
        </Grid.Col>
      </Grid>
      <Modal
        opened={!!cdcReusePrompt}
        onClose={() => setCdcReusePrompt(null)}
        title="Reuse existing CDC objects?"
      >
        <Stack>
          <Text size="sm">
            We found CDC objects in your database matching the provided names. You can reuse them or cancel and change the names.
          </Text>
          {cdcReusePrompt?.slot?.exists && (
            <Alert color={cdcReusePrompt.slot.active ? 'yellow' : 'blue'} title="Replication Slot">
              Name: <Code>{cdcReusePrompt.slot.name}</Code><br />
              Active: {String(!!cdcReusePrompt.slot.active)}<br />
              Referenced by Hermod: {String(!!cdcReusePrompt.slot.hermod_in_use)}
            </Alert>
          )}
          {cdcReusePrompt?.publication?.exists && (
            <Alert color="blue" title="Publication">
              Name: <Code>{cdcReusePrompt.publication.name}</Code><br />
              Referenced by Hermod: {String(!!cdcReusePrompt.publication.hermod_in_use)}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCdcReusePrompt(null)}>
              Cancel
            </Button>
            <Button
              color="blue"
              onClick={() => {
                // Accept reuse: keep current names, mark test OK, and fetch sample
                setCdcReusePrompt(null);
                setTestResult({ status: 'ok', message: 'Using existing slot/publication.' });
                fetchSample(source);
              }}
            >
              Use existing
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

// ----------------- Helpers for Form Source -----------------
type FormFieldItem = {
  // Common
  type:
    | 'text'
    | 'number'
    | 'date'
    | 'datetime'
    | 'image'
    | 'multiple'
    | 'one'
    | 'email'
    | 'date_range'
    | 'scale'
    // Layout-only items
    | 'heading'
    | 'text_block'
    | 'divider'
    | 'page_break';

  // Input fields
  name?: string;
  label?: string;
  required?: boolean;
  options?: string[];
  placeholder?: string;
  help?: string;
  number_kind?: 'integer' | 'float';
  render?: 'select' | 'radio';
  // email options
  verify_email?: boolean;
  reject_if_invalid?: boolean;
  // scale options
  min?: number;
  max?: number;
  step?: number;
  // date_range labels
  start_label?: string;
  end_label?: string;

  // Layout metadata
  section?: string; // logical group label (shown as heading when changes)
  width?: 'auto' | 'half' | 'full'; // affects public form grid

  // Layout-only content
  content?: string; // for heading/text_block
  level?: 1 | 2 | 3; // for heading size
};

function safeParseJSON(input: any): any {
  if (!input) return null;
  if (typeof input === 'object') return input;
  try {
    return JSON.parse(String(input));
  } catch {
    return null;
  }
}

function FieldBuilder({ value, onChange }: { value: FormFieldItem[]; onChange: (v: FormFieldItem[]) => void }) {
  const [draft, setDraft] = useState<FormFieldItem>({
    type: 'text',
    name: '',
    label: '',
    required: false,
    options: [],
    number_kind: 'float',
    render: 'select',
    verify_email: false,
    reject_if_invalid: false,
    section: '',
    width: 'auto',
    content: '',
    level: 2,
  });

  const isLayoutOnly = (t: FormFieldItem['type']) => ['heading', 'text_block', 'divider', 'page_break'].includes(t);

  const addField = () => {
    // For input fields, name is required; for layout-only items it's not
    if (!isLayoutOnly(draft.type) && !draft.name) return;
    const list = [...(value || []), { ...draft, options: draft.options || [] }];
    onChange(list);
    setDraft({
      type: 'text',
      name: '',
      label: '',
      required: false,
      options: [],
      number_kind: 'float',
      render: 'select',
      verify_email: false,
      reject_if_invalid: false,
      section: '',
      width: 'auto',
      content: '',
      level: 2,
    });
  };
  const removeAt = (idx: number) => {
    const list = [...(value || [])];
    list.splice(idx, 1);
    onChange(list);
  };

  const moveUp = (idx: number) => {
    if (idx <= 0) return;
    const list = [...(value || [])];
    const tmp = list[idx - 1];
    list[idx - 1] = list[idx];
    list[idx] = tmp;
    onChange(list);
  };
  const moveDown = (idx: number) => {
    const list = [...(value || [])];
    if (idx >= list.length - 1) return;
    const tmp = list[idx + 1];
    list[idx + 1] = list[idx];
    list[idx] = tmp;
    onChange(list);
  };

  return (
    <Stack gap="sm">
      <Stack gap={6}>
        <Title order={5}>Fields</Title>
        {(value && value.length > 0) ? (
          <Stack gap={8}>
            {value.map((f, i) => (
              <Group key={i} justify="space-between" align="center">
                <Group gap="xs">
                  <Badge variant="light">{f.type}</Badge>
                  {(['heading','text_block','divider','page_break'] as const).includes(f.type as any) ? (
                    <>
                      {f.type === 'heading' && <Text fw={700}>Heading: {f.content || '(empty)'}</Text>}
                      {f.type === 'text_block' && <Text c="dimmed">Text: {f.content || '(empty)'}</Text>}
                      {f.type === 'divider' && <Text c="dimmed">Divider</Text>}
                      {f.type === 'page_break' && <Badge color="violet">Page Break</Badge>}
                    </>
                  ) : (
                    <>
                      <Text fw={600}>{f.label || f.name}</Text>
                      <Text c="dimmed" size="sm">name: <Code>{f.name}</Code></Text>
                      {f.required && <Badge color="red" variant="filled">required</Badge>}
                      {f.section && <Badge variant="dot" color="gray">section: {f.section}</Badge>}
                      {f.width && f.width !== 'auto' && <Badge variant="light">{f.width}</Badge>}
                    </>
                  )}
                </Group>
                <Group gap={4}>
                  <ActionIcon variant="subtle" onClick={() => moveUp(i)} aria-label="Move up">↑</ActionIcon>
                  <ActionIcon variant="subtle" onClick={() => moveDown(i)} aria-label="Move down">↓</ActionIcon>
                  <ActionIcon color="red" variant="subtle" onClick={() => removeAt(i)} aria-label="Remove">✕</ActionIcon>
                </Group>
              </Group>
            ))}
          </Stack>
        ) : (
          <Text size="sm" c="dimmed">No fields yet. Add your first field below.</Text>
        )}
      </Stack>

      <Divider label="Add Field or Layout" />
      <Grid>
        <Grid.Col span={{ base: 12, sm: 6 }}>
          <Select
            label="Type"
            data={[
              { value: 'text', label: 'Text' },
              { value: 'number', label: 'Number' },
              { value: 'date', label: 'Date' },
              { value: 'datetime', label: 'Date & Time' },
              { value: 'image', label: 'Image Upload' },
              { value: 'multiple', label: 'Multiple Choice (multi-select)' },
              { value: 'one', label: 'Single Choice' },
              { value: 'email', label: 'Email' },
              { value: 'date_range', label: 'Date Range' },
              { value: 'scale', label: 'Linear Scale' },
              { value: 'heading', label: 'Heading (layout)' },
              { value: 'text_block', label: 'Text block (layout)' },
              { value: 'divider', label: 'Divider (layout)' },
              { value: 'page_break', label: 'Page break (layout)' },
            ]}
            value={draft.type}
            onChange={(val) => setDraft({ ...draft, type: (val as any) || 'text' })}
            required
          />
        </Grid.Col>
        {!isLayoutOnly(draft.type) && (
          <>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="Name" placeholder="full_name" value={draft.name || ''} onChange={(e) => setDraft({ ...draft, name: e.target.value })} required />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="Label" placeholder="Full Name" value={draft.label || ''} onChange={(e) => setDraft({ ...draft, label: e.target.value })} />
            </Grid.Col>
          </>
        )}
        {draft.type === 'heading' && (
          <>
            <Grid.Col span={{ base: 12, sm: 8 }}>
              <TextInput label="Heading text" placeholder="Section title" value={draft.content || ''} onChange={(e) => setDraft({ ...draft, content: e.target.value })} required />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 4 }}>
              <Select label="Level" data={[{ value: '1', label: 'H1' }, { value: '2', label: 'H2' }, { value: '3', label: 'H3' }]} value={String(draft.level || 2)} onChange={(v) => setDraft({ ...draft, level: (v ? Number(v) : 2) as 1|2|3 })} />
            </Grid.Col>
          </>
        )}
        {draft.type === 'text_block' && (
          <Grid.Col span={12}>
            <TextInput label="Text content" placeholder="Some helpful text for users" value={draft.content || ''} onChange={(e) => setDraft({ ...draft, content: e.target.value })} />
          </Grid.Col>
        )}
        {draft.type === 'number' && (
          <Grid.Col span={{ base: 12, sm: 6 }}>
            <Select label="Number Kind" data={[{ value: 'integer', label: 'Integer' }, { value: 'float', label: 'Float' }]} value={draft.number_kind} onChange={(v) => setDraft({ ...draft, number_kind: (v as any) || 'float' })} />
          </Grid.Col>
        )}
        {draft.type === 'email' && (
          <>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <Checkbox mt={26} label="Verify mailbox exists (SMTP)" checked={!!draft.verify_email} onChange={(e) => setDraft({ ...draft, verify_email: e.currentTarget.checked })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <Checkbox mt={26} label="Reject if invalid" checked={!!draft.reject_if_invalid} onChange={(e) => setDraft({ ...draft, reject_if_invalid: e.currentTarget.checked })} />
            </Grid.Col>
          </>
        )}
        {draft.type === 'date_range' && (
          <>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="Start label" placeholder="Start" value={draft.start_label || ''} onChange={(e) => setDraft({ ...draft, start_label: e.target.value })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="End label" placeholder="End" value={draft.end_label || ''} onChange={(e) => setDraft({ ...draft, end_label: e.target.value })} />
            </Grid.Col>
          </>
        )}
        {draft.type === 'scale' && (
          <>
            <Grid.Col span={{ base: 12, sm: 4 }}>
              <TextInput type="number" label="Min" placeholder="0" value={draft.min as any || ''} onChange={(e) => setDraft({ ...draft, min: e.target.value === '' ? undefined : Number(e.target.value) })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 4 }}>
              <TextInput type="number" label="Max" placeholder="10" value={draft.max as any || ''} onChange={(e) => setDraft({ ...draft, max: e.target.value === '' ? undefined : Number(e.target.value) })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 4 }}>
              <TextInput type="number" label="Step" placeholder="1" value={draft.step as any || ''} onChange={(e) => setDraft({ ...draft, step: e.target.value === '' ? undefined : Number(e.target.value) })} />
            </Grid.Col>
          </>
        )}
        {(draft.type === 'one' || draft.type === 'multiple') && (
          <Grid.Col span={12}>
            <TagsInput label="Options" placeholder="Add options" value={draft.options || []} onChange={(opts) => setDraft({ ...draft, options: opts })} />
          </Grid.Col>
        )}
        {draft.type === 'one' && (
          <Grid.Col span={{ base: 12, sm: 6 }}>
            <Select label="Single Choice UI" data={[{ value: 'select', label: 'Dropdown' }, { value: 'radio', label: 'Radio buttons' }]} value={draft.render || 'select'} onChange={(v) => setDraft({ ...draft, render: (v as any) || 'select' })} />
          </Grid.Col>
        )}
        {!isLayoutOnly(draft.type) && (
          <>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="Placeholder" placeholder="Enter value…" value={draft.placeholder || ''} onChange={(e) => setDraft({ ...draft, placeholder: e.target.value })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <Checkbox mt={26} label="Required" checked={!!draft.required} onChange={(e) => setDraft({ ...draft, required: e.currentTarget.checked })} />
            </Grid.Col>
            <Grid.Col span={12}>
              <TextInput label="Help text" placeholder="Shown under the field to help users." value={draft.help || ''} onChange={(e) => setDraft({ ...draft, help: e.target.value })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <TextInput label="Section (optional)" placeholder="e.g., Contact Info" value={draft.section || ''} onChange={(e) => setDraft({ ...draft, section: e.target.value })} />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6 }}>
              <Select label="Width" data={[{ value: 'auto', label: 'Auto' }, { value: 'half', label: 'Half (2-column)' }, { value: 'full', label: 'Full' }]} value={draft.width || 'auto'} onChange={(v) => setDraft({ ...draft, width: (v as any) || 'auto' })} />
            </Grid.Col>
          </>
        )}
      </Grid>

      <Group justify="flex-end">
        <Button leftSection={<IconPlus size={16} />} onClick={addField}>Add field</Button>
      </Group>
    </Stack>
  );
}

function PublicFormLink({ path }: { path?: string }) {
  const publicPath = (path || '').startsWith('/api/forms/') ? (path || '').slice('/api/forms/'.length) : (path || '').replace(/^\//, '');
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const url = publicPath ? `${origin}/forms/${publicPath}` : '';
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>Public Form URL</Text>
      <Group wrap="nowrap">
        <TextInput readOnly value={url || 'Set the endpoint path to see the URL'} style={{ flex: 1 }} />
        <Group gap={8}>
          <Button variant="light" disabled={!url} onClick={() => { if (url) navigator.clipboard.writeText(url); notifications.show({ message: 'Copied form URL', color: 'green' }); }}>Copy</Button>
          <Button variant="outline" disabled={!url} onClick={() => { if (url) window.open(url, '_blank'); }}>Open</Button>
        </Group>
      </Group>
      <Text size="xs" c="dimmed">Users can submit the form without custom headers when "Allow public submissions" is enabled.</Text>
    </Stack>
  );
}

function WebhookHistory({ 
  path, 
  onReplaySuccess, 
  onSelectSample 
}: { 
  path: string, 
  onReplaySuccess: () => void, 
  onSelectSample: (body: string) => void 
}) {
  const { data: history, refetch, isLoading } = useSuspenseQuery({
    queryKey: ['webhook-history', path],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/webhooks/requests?path=${encodeURIComponent(path)}&limit=10`);
      return res.json();
    }
  });

  const replayMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetch(`${API_BASE}/webhooks/requests/${id}/replay`, {
        method: 'POST'
      });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    onSuccess: () => {
      onReplaySuccess();
    }
  });

  const requests = history?.data || [];

  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
      <Stack h="100%">
        <Group justify="space-between" px="xs">
          <Group gap="xs">
            <IconHistory size="1.2rem" color="var(--mantine-color-blue-6)" />
            <Text size="sm" fw={700} c="dimmed">3. WEBHOOK HISTORY</Text>
          </Group>
          <ActionIcon variant="subtle" size="sm" onClick={() => refetch()} loading={isLoading}>
            <IconRefresh size="1rem" />
          </ActionIcon>
        </Group>
        <Divider />
        
        {requests.length === 0 ? (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <IconInfoCircle size="2rem" color="var(--mantine-color-gray-4)" />
            <Text size="xs" c="dimmed" ta="center">No recent webhook requests found for this path.</Text>
          </Stack>
        ) : (
          <ScrollArea flex={1}>
            <Stack gap="xs">
              {requests.map((req: any) => (
                <Card key={req.id} withBorder p="xs" radius="sm">
                  <Stack gap={4}>
                    <Group justify="space-between">
                      <Group gap={6}>
                        <Badge size="xs" color="blue" variant="filled">{req.method}</Badge>
                        <Text size="xs" fw={700}>{new Date(req.timestamp).toLocaleString()}</Text>
                      </Group>
                      <Group gap={4}>
                        <ActionIcon 
                          size="xs" 
                          variant="light" 
                          color="green" 
                          onClick={() => replayMutation.mutate(req.id)}
                          loading={replayMutation.isPending && replayMutation.variables === req.id}
                          title="Replay"
                        >
                          <IconPlayerPlay size="0.8rem" />
                        </ActionIcon>
                        <ActionIcon 
                          size="xs" 
                          variant="light" 
                          color="blue" 
                          onClick={() => onSelectSample(req.body)}
                          title="Use as Sample"
                        >
                          <IconChevronRight size="0.8rem" />
                        </ActionIcon>
                      </Group>
                    </Group>
                    <Code block style={{ fontSize: '9px', maxHeight: '100px', overflow: 'hidden' }}>
                      {typeof req.body === 'string' ? req.body : JSON.stringify(req.body, null, 2)}
                    </Code>
                  </Stack>
                </Card>
              ))}
            </Stack>
          </ScrollArea>
        )}
      </Stack>
    </Card>
  );
}
