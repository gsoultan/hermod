import { useState, useEffect } from 'react';
import { Button, Group, TextInput, Select, Stack, Alert, Divider, Paper, Text, Grid, Title, Code, List } from '@mantine/core';
import { IconCheck, IconAlertCircle, IconInfoCircle } from '@tabler/icons-react';
import { useMutation, useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useNavigate } from '@tanstack/react-router';

const API_BASE = '/api';

const SINK_TYPES = [
  'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'file', 'kafka', 'pulsar', 'kinesis', 'pubsub', 'fcm', 'http', 'stdout',
  'postgres', 'mysql', 'cassandra', 'sqlite', 'clickhouse', 'mongodb'
];

interface Worker {
  id: string;
  name: string;
}

interface SinkFormProps {
  initialData?: any;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
}

export function SinkForm({ initialData, isEditing = false, embedded = false, onSave }: SinkFormProps) {
  const navigate = useNavigate();
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const [testResult, setTestResult] = useState<{ status: 'ok' | 'error', message: string } | null>(null);
  const [sink, setSink] = useState<any>({ 
    name: '', 
    type: 'stdout', 
    vhost: '', 
    worker_id: '',
    config: { format: 'json' }
  });

  useEffect(() => {
    if (initialData) {
      setSink({
        ...initialData
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


  const renderConfigFields = () => {
    const type = sink.type;

    switch (type) {
      case 'nats':
        return (
          <>
            <TextInput label="URL" placeholder="nats://localhost:4222" value={sink.config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Subject" placeholder="hermod.data" value={sink.config.subject || ''} onChange={(e) => updateConfig('subject', e.target.value)} required />
            <Group grow>
              <TextInput label="Username" placeholder="Optional" value={sink.config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
              <TextInput label="Password" type="password" placeholder="Optional" value={sink.config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            </Group>
            <TextInput label="Token" placeholder="Optional" value={sink.config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
          </>
        );
      case 'rabbitmq':
        return (
          <>
            <TextInput label="URL" placeholder="rabbitmq-stream://guest:guest@localhost:5552" value={sink.config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Stream Name" placeholder="hermod-stream" value={sink.config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
          </>
        );
      case 'rabbitmq_queue':
        return (
          <>
            <TextInput label="URL" placeholder="amqp://guest:guest@localhost:5672" value={sink.config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Queue Name" placeholder="hermod-queue" value={sink.config.queue_name || ''} onChange={(e) => updateConfig('queue_name', e.target.value)} required />
          </>
        );
      case 'redis':
        return (
          <>
            <TextInput label="Address" placeholder="localhost:6379" value={sink.config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
            <TextInput label="Password" type="password" placeholder="Optional" value={sink.config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            <TextInput label="Stream" placeholder="hermod-stream" value={sink.config.stream || ''} onChange={(e) => updateConfig('stream', e.target.value)} required />
          </>
        );
      case 'file':
        return (
          <TextInput label="Filename" placeholder="/tmp/hermod.log" value={sink.config.filename || ''} onChange={(e) => updateConfig('filename', e.target.value)} required />
        );
      case 'kafka':
        return (
          <>
            <TextInput label="Brokers" placeholder="localhost:9092,localhost:9093" value={sink.config.brokers || ''} onChange={(e) => updateConfig('brokers', e.target.value)} required />
            <TextInput label="Topic" placeholder="hermod-topic" value={sink.config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
            <Group grow>
              <TextInput label="Username (SASL)" placeholder="Optional" value={sink.config.username || ''} onChange={(e) => updateConfig('username', e.target.value)} />
              <TextInput label="Password (SASL)" type="password" placeholder="Optional" value={sink.config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} />
            </Group>
          </>
        );
      case 'pulsar':
        return (
          <>
            <TextInput label="URL" placeholder="pulsar://localhost:6650" value={sink.config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Topic" placeholder="persistent://public/default/hermod" value={sink.config.topic || ''} onChange={(e) => updateConfig('topic', e.target.value)} required />
            <TextInput label="Token" placeholder="Optional" value={sink.config.token || ''} onChange={(e) => updateConfig('token', e.target.value)} />
          </>
        );
      case 'kinesis':
        return (
          <>
            <TextInput label="Region" placeholder="us-east-1" value={sink.config.region || ''} onChange={(e) => updateConfig('region', e.target.value)} required />
            <TextInput label="Stream Name" placeholder="hermod-stream" value={sink.config.stream_name || ''} onChange={(e) => updateConfig('stream_name', e.target.value)} required />
            <Group grow>
              <TextInput label="Access Key" placeholder="Optional" value={sink.config.access_key || ''} onChange={(e) => updateConfig('access_key', e.target.value)} />
              <TextInput label="Secret Key" type="password" placeholder="Optional" value={sink.config.secret_key || ''} onChange={(e) => updateConfig('secret_key', e.target.value)} />
            </Group>
          </>
        );
      case 'pubsub':
        return (
          <>
            <TextInput label="Project ID" placeholder="my-project" value={sink.config.project_id || ''} onChange={(e) => updateConfig('project_id', e.target.value)} required />
            <TextInput label="Topic ID" placeholder="hermod-topic" value={sink.config.topic_id || ''} onChange={(e) => updateConfig('topic_id', e.target.value)} required />
            <TextInput label="Credentials JSON" placeholder="Optional service account JSON content" value={sink.config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} />
          </>
        );
      case 'fcm':
        return (
          <TextInput label="Credentials JSON" placeholder="Service account JSON content" value={sink.config.credentials_json || ''} onChange={(e) => updateConfig('credentials_json', e.target.value)} required />
        );
      case 'http':
        return (
          <>
            <TextInput label="URL" placeholder="http://localhost:8080/webhook" value={sink.config.url || ''} onChange={(e) => updateConfig('url', e.target.value)} required />
            <TextInput label="Headers" placeholder="Authorization: Bearer token, X-Custom: value" value={sink.config.headers || ''} onChange={(e) => updateConfig('headers', e.target.value)} />
          </>
        );
      case 'postgres':
      case 'mysql':
        return (
          <>
            <Group grow>
              <TextInput label="Host" placeholder="localhost" value={sink.config.host || ''} onChange={(e) => updateConfig('host', e.target.value)} required />
              <TextInput label="Port" placeholder={type === 'postgres' ? "5432" : "3306"} value={sink.config.port || ''} onChange={(e) => updateConfig('port', e.target.value)} required />
            </Group>
            <Group grow>
              <TextInput label="User" placeholder="user" value={sink.config.user || ''} onChange={(e) => updateConfig('user', e.target.value)} required />
              <TextInput label="Password" type="password" placeholder="password" value={sink.config.password || ''} onChange={(e) => updateConfig('password', e.target.value)} required />
            </Group>
            <TextInput label="Database" placeholder="dbname" value={sink.config.dbname || ''} onChange={(e) => updateConfig('dbname', e.target.value)} required />
            {type === 'postgres' && <TextInput label="SSL Mode" placeholder="disable" value={sink.config.sslmode || ''} onChange={(e) => updateConfig('sslmode', e.target.value)} />}
            <TextInput label="OR Connection String" placeholder={type === 'postgres' ? "postgres://..." : "user:pass@tcp(host:port)/dbname"} value={sink.config.connection_string || ''} onChange={(e) => updateConfig('connection_string', e.target.value)} />
          </>
        );
      case 'cassandra':
        return (
          <>
            <TextInput label="Hosts" placeholder="localhost" value={sink.config.hosts || ''} onChange={(e) => updateConfig('hosts', e.target.value)} required />
            <TextInput label="Keyspace" placeholder="keyspace" value={sink.config.keyspace || ''} onChange={(e) => updateConfig('keyspace', e.target.value)} required />
          </>
        );
      case 'sqlite':
        return (
          <TextInput label="DB Path" placeholder="hermod.db" value={sink.config.db_path || ''} onChange={(e) => updateConfig('db_path', e.target.value)} required />
        );
      case 'clickhouse':
        return (
          <>
            <TextInput label="Address" placeholder="localhost:9000" value={sink.config.addr || ''} onChange={(e) => updateConfig('addr', e.target.value)} required />
            <TextInput label="Database" placeholder="default" value={sink.config.database || ''} onChange={(e) => updateConfig('database', e.target.value)} required />
          </>
        );
      case 'mongodb':
        return (
          <>
            <TextInput label="URI" placeholder="mongodb://localhost:27017" value={sink.config.uri || ''} onChange={(e) => updateConfig('uri', e.target.value)} required />
            <TextInput label="Database" placeholder="hermod" value={sink.config.database || ''} onChange={(e) => updateConfig('database', e.target.value)} required />
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
        return (
          <Stack gap="xs">
            <Title order={5}>PostgreSQL Sink</Title>
            <Text size="sm">Hermod can automatically create tables in the target database.</Text>
            <List size="sm" withPadding>
              <List.Item>Ensure the user has <Code>CREATE</Code> and <Code>INSERT</Code> permissions</List.Item>
              <List.Item>If using a connection string, ensure it's in the correct format: <Code>postgres://user:pass@host:port/dbname</Code></List.Item>
            </List>
          </Stack>
        );
      case 'mysql':
        return (
          <Stack gap="xs">
            <Title order={5}>MySQL Sink</Title>
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
    <Grid>
      <Grid.Col span={{ base: 12, md: 8 }}>
        <Stack>
          {testResult && (
            <Alert 
              icon={testResult.status === 'ok' ? <IconCheck size="1rem" /> : <IconAlertCircle size="1rem" />} 
              title={testResult.status === 'ok' ? "Success" : "Error"} 
              color={testResult.status === 'ok' ? "green" : "red"}
              withCloseButton
              onClose={() => setTestResult(null)}
            >
              {testResult.message}
            </Alert>
          )}
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
              data={(workers || []).map(w => ({ value: w.id, label: w.name || w.id }))}
              value={sink.worker_id}
              onChange={(val) => handleSinkChange({ worker_id: val || '' })}
              clearable
            />
          )}
          <Select 
            label="Type" 
            data={SINK_TYPES} 
            value={sink.type}
            onChange={(val) => handleSinkChange({ type: val || '', config: { ...sink.config, format: sink.config.format || 'json' } })}
            required
          />
          
          <Divider label="Configuration" labelPosition="center" />
          {renderConfigFields()}
          <Select 
            label="Format" 
            data={['json']} 
            value={sink.config.format || 'json'}
            onChange={(val) => setSink({ ...sink, config: { ...sink.config, format: val || 'json' } })}
          />


          <Group justify="flex-end" mt="xl">
            {!embedded && <Button variant="outline" onClick={() => navigate({ to: '/sinks' })}>Cancel</Button>}
            <Button variant="outline" color="blue" onClick={() => testMutation.mutate(sink)} loading={testMutation.isPending}>Test Connection</Button>
            <Button 
              disabled={testResult?.status !== 'ok' || !sink.name || (!embedded && !sink.vhost)}
              onClick={() => {
                if (embedded && onSave) {
                  onSave(sink);
                } else {
                  submitMutation.mutate(sink);
                }
              }} 
              loading={submitMutation.isPending}
            >
              {isEditing ? 'Save Changes' : (embedded ? 'Confirm Sink' : 'Create Sink')}
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
