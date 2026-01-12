import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Paper, Text, Grid, Title, Code, List, Checkbox, TagsInput, ActionIcon } from '@mantine/core';
import { IconCheck, IconInfoCircle, IconRefresh } from '@tabler/icons-react';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';

const API_BASE = '/api';

const SOURCE_TYPES = [
  'postgres', 'mysql', 'mssql', 'oracle', 'mongodb', 'cassandra', 'yugabyte', 'scylladb', 'clickhouse', 'sqlite',
  'kafka', 'nats', 'redis', 'rabbitmq', 'rabbitmq_queue'
];

interface Worker {
  id: string;
  name: string;
}

interface SourceFormProps {
  initialData?: any;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
}

export function SourceForm({ initialData, isEditing = false, embedded = false, onSave }: SourceFormProps) {
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
      sslmode: 'disable',
      slot_name: 'hermod_slot',
      publication_name: 'hermod_pub'
    }
  });

  const [discoveredDatabases, setDiscoveredDatabases] = useState<string[]>([]);
  const [discoveredTables, setDiscoveredTables] = useState<string[]>([]);
  const [isFetchingDBs, setIsFetchingDBs] = useState(false);
  const [isFetchingTables, setIsFetchingTables] = useState(false);

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
      setSource({
        ...initialData,
        config: {
          ...initialData.config,
        }
      });
    }
  }, [initialData]);

  const { data: vhosts } = useSuspenseQuery<any[]>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const { data: workers } = useSuspenseQuery<Worker[]>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return [];
    }
  });

  const availableVHostsList = role === 'Administrator' 
    ? (vhosts || []).map(v => v.name)
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
    onSuccess: () => {
      setTestResult({ status: 'ok', message: 'Connection successful!' });
    },
    onError: () => {
      setTestResult(null); // apiFetch handles notification
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
        body: JSON.stringify({ ...s, config: cleanConfig }),
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
    setSource({ ...source, ...updates });
    setTestResult(null);
  };

  const updateConfig = (key: string, value: string) => {
    setSource({
      ...source,
      config: { ...source.config, [key]: value }
    });
    setTestResult(null);
  };

  const renderConfigFields = () => {
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

    const commonFields = (
      <>
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
          <TextInput label="DB Path" placeholder="/path/to/hermod.db" value={source.config.path} onChange={(e) => updateConfig('path', e.target.value)} />
          {tablesInput}
        </>
      );
    }

    if (source.type === 'postgres') {
      return (
        <>
          {commonFields}
          <TextInput label="SSL Mode" placeholder="disable" value={source.config.sslmode} onChange={(e) => updateConfig('sslmode', e.target.value)} />
          <Group grow>
            <TextInput label="Slot Name" value={source.config.slot_name} onChange={(e) => updateConfig('slot_name', e.target.value)} />
            <TextInput label="Publication Name" value={source.config.publication_name} onChange={(e) => updateConfig('publication_name', e.target.value)} />
          </Group>
        </>
      );
    }

    if (source.type === 'mssql') {
      return (
        <>
          {commonFields}
          <Checkbox 
            label="Auto Enable CDC" 
            description="Automatically enable CDC on the database and tables if not already enabled."
            checked={source.config.auto_enable_cdc !== 'false'} 
            onChange={(e) => updateConfig('auto_enable_cdc', e.target.checked ? 'true' : 'false')} 
          />
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
          <TextInput label="Queue" placeholder="optional queue name" value={source.config.queue} onChange={(e) => updateConfig('queue', e.target.value)} />
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

    return (
      <>
        {commonFields}
        <TextInput 
          label="OR Connection String (Overrides individual fields)" 
          placeholder="postgres://..." 
          value={source.config.connection_string}
          onChange={(e) => updateConfig('connection_string', e.target.value)}
        />
      </>
    );
  };


  const renderSetupInstructions = () => {
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
        return (
          <Stack gap="xs">
            <Title order={5}>MySQL Setup</Title>
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
    <Grid>
      <Grid.Col span={{ base: 12, md: 8 }}>
        <Stack>
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
              data={(workers || []).map(w => ({ value: w.id, label: w.name || w.id }))}
              value={source.worker_id}
              onChange={(val) => handleSourceChange({ worker_id: val || '' })}
              clearable
            />
          )}
          <Select 
            label="Type" 
            data={SOURCE_TYPES} 
            value={source.type}
            onChange={(val) => handleSourceChange({ type: val || '' })}
            required
          />
          
          <Divider label="Configuration" labelPosition="center" />
          {renderConfigFields()}

          <Group justify="flex-end" mt="xl">
            {!embedded && <Button variant="outline" onClick={() => navigate({ to: '/sources' })}>Cancel</Button>}
            <Button variant="outline" color="blue" onClick={() => testMutation.mutate(source)} loading={testMutation.isPending}>Test Connection</Button>
            <Button 
              disabled={testResult?.status !== 'ok' || !source.name || (!embedded && !source.vhost)}
              onClick={() => {
                if (embedded && onSave) {
                  onSave(source);
                } else {
                  submitMutation.mutate(source);
                }
              }} 
              loading={submitMutation.isPending}
            >
              {isEditing ? 'Save Changes' : (embedded ? 'Confirm Source' : 'Create Source')}
            </Button>
          </Group>
        </Stack>
      </Grid.Col>
      <Grid.Col span={{ base: 12, md: 4 }}>
        <Paper withBorder p="md" radius="md" h="100%" bg="gray.0">
          {renderSetupInstructions()}
        </Paper>
      </Grid.Col>
    </Grid>
  );
}
