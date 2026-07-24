import { useState, lazy, useEffect } from 'react';
import { Stack, Alert, Text, Group, ActionIcon, Tooltip, List } from '@mantine/core';
import { useVHost } from '@/context/VHostContext';
import { useNavigate } from '@tanstack/react-router';
import type { Sink } from '@/types';
import { IconAlertCircle, IconExternalLink } from '@tabler/icons-react';
import { useSinkForm } from '@/hooks/useSinkForm';
import { SinkWizard } from './SinkWizard';
import { getRoleFromToken } from '@/api';

// Lazy load config components
const PostgresSinkConfig = lazy(() => import('../workflow/Sink/PostgresSinkConfig').then(m => ({ default: m.PostgresSinkConfig })));
const DatabaseSinkConfig = lazy(() => import('../workflow/Sink/DatabaseSinkConfig').then(m => ({ default: m.DatabaseSinkConfig })));
const QueueSinkConfig = lazy(() => import('../workflow/Sink/QueueSinkConfig').then(m => ({ default: m.QueueSinkConfig })));
const FTPSinkConfig = lazy(() => import('../workflow/Sink/FTPSinkConfig').then(m => ({ default: m.FTPSinkConfig })));
const GoogleSheetsSinkConfig = lazy(() => import('../workflow/Sink/GoogleSheetsSinkConfig').then(m => ({ default: m.GoogleSheetsSinkConfig })));
const SMTPSinkConfig = lazy(() => import('../workflow/Sink/SMTPSinkConfig').then(m => ({ default: m.SMTPSinkConfig })));
const SSESinkConfig = lazy(() => import('../workflow/Sink/SSESinkConfig').then(m => ({ default: m.SSESinkConfig })));
const ElasticsearchSinkConfig = lazy(() => import('../workflow/Sink/ElasticsearchSinkConfig').then(m => ({ default: m.ElasticsearchSinkConfig })));
const SnowflakeSinkConfig = lazy(() => import('../workflow/Sink/SnowflakeSinkConfig').then(m => ({ default: m.SnowflakeSinkConfig })));
const SalesforceSinkConfig = lazy(() => import('../workflow/Sink/SalesforceSinkConfig').then(m => ({ default: m.SalesforceSinkConfig })));
const ServiceNowSinkConfig = lazy(() => import('../workflow/Sink/ServiceNowSinkConfig').then(m => ({ default: m.ServiceNowSinkConfig })));
const PineconeSinkConfig = lazy(() => import('../workflow/Sink/PineconeSinkConfig').then(m => ({ default: m.PineconeSinkConfig })));
const MilvusSinkConfig = lazy(() => import('../workflow/Sink/MilvusSinkConfig').then(m => ({ default: m.MilvusSinkConfig })));
const PgvectorSinkConfig = lazy(() => import('../workflow/Sink/PgvectorSinkConfig').then(m => ({ default: m.PgvectorSinkConfig })));
const FailoverSinkConfig = lazy(() => import('../workflow/Sink/FailoverSinkConfig').then(m => ({ default: m.FailoverSinkConfig })));
const SapSinkConfig = lazy(() => import('../workflow/Sink/SapSinkConfig').then(m => ({ default: m.SapSinkConfig })));
const Dynamics365SinkConfig = lazy(() => import('../workflow/Sink/Dynamics365SinkConfig').then(m => ({ default: m.Dynamics365SinkConfig })));
const S3SinkConfig = lazy(() => import('../workflow/Sink/S3SinkConfig'));
const NotificationSinkConfig = lazy(() => import('../workflow/Sink/NotificationSinkConfig').then(m => ({ default: m.NotificationSinkConfig })));

const SINK_TYPES = [
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'mariadb', label: 'MariaDB' },
  { value: 'mssql', label: 'SQL Server' },
  { value: 'oracle', label: 'Oracle' },
  { value: 'mongodb', label: 'MongoDB' },
  { value: 'sqlite', label: 'SQLite' },
  { value: 'clickhouse', label: 'ClickHouse' },
  { value: 'salesforce', label: 'Salesforce' },
  { value: 'servicenow', label: 'ServiceNow' },
  { value: 'elasticsearch', label: 'Elasticsearch' },
  { value: 'yugabyte', label: 'YugabyteDB' },
  { value: 'snowflake', label: 'Snowflake' },
  { value: 'sap', label: 'SAP' },
  { value: 'dynamics365', label: 'Dynamics 365' },
  { value: 'eventstore', label: 'Event Store' },
  { value: 'pgvector', label: 'Pgvector' },
  { value: 'pinecone', label: 'Pinecone' },
  { value: 'milvus', label: 'Milvus' },
  { value: 'kafka', label: 'Kafka' },
  { value: 'mqtt', label: 'MQTT' },
  { value: 'nats', label: 'NATS' },
  { value: 'rabbitmq', label: 'RabbitMQ Stream' },
  { value: 'rabbitmq_queue', label: 'RabbitMQ Queue' },
  { value: 'redis', label: 'Redis Stream' },
  { value: 'pubsub', label: 'Google Pub/Sub' },
  { value: 'kinesis', label: 'AWS Kinesis' },
  { value: 'pulsar', label: 'Apache Pulsar' },
  { value: 'http', label: 'API / Webhook' },
  { value: 'smtp', label: 'SMTP (Email)' },
  { value: 'telegram', label: 'Telegram' },
  { value: 'fcm', label: 'Firebase (FCM)' },
  { value: 'file', label: 'File' },
  { value: 'stdout', label: 'Stdout' },
  { value: 'sse', label: 'Server-Sent Events (SSE)' },
  { value: 'websocket', label: 'WebSocket' },
  { value: 'googlesheets', label: 'Google Sheets' },
  { value: 's3', label: 'AWS S3' },
  { value: 's3-parquet', label: 'AWS S3 Parquet' },
  { value: 'ftp', label: 'FTP / FTPS' },
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'twitter', label: 'Twitter (X)' },
  { value: 'facebook', label: 'Facebook' },
  { value: 'instagram', label: 'Instagram' },
  { value: 'linkedin', label: 'LinkedIn' },
  { value: 'tiktok', label: 'TikTok' },
  { value: 'failover', label: 'Failover Group' },
];

const configComponents: Record<string, any> = {
  postgres: PostgresSinkConfig,
  mysql: DatabaseSinkConfig,
  mariadb: DatabaseSinkConfig,
  mssql: DatabaseSinkConfig,
  oracle: DatabaseSinkConfig,
  yugabyte: DatabaseSinkConfig,
  sqlite: DatabaseSinkConfig,
  clickhouse: DatabaseSinkConfig,
  snowflake: SnowflakeSinkConfig,
  elasticsearch: ElasticsearchSinkConfig,
  kafka: QueueSinkConfig,
  nats: QueueSinkConfig,
  redis: QueueSinkConfig,
  rabbitmq: QueueSinkConfig,
  rabbitmq_queue: QueueSinkConfig,
  pubsub: QueueSinkConfig,
  kinesis: QueueSinkConfig,
  pulsar: QueueSinkConfig,
  ftp: FTPSinkConfig,
  googlesheets: GoogleSheetsSinkConfig,
  smtp: SMTPSinkConfig,
  sse: SSESinkConfig,
  salesforce: SalesforceSinkConfig,
  servicenow: ServiceNowSinkConfig,
  pinecone: PineconeSinkConfig,
  milvus: MilvusSinkConfig,
  pgvector: PgvectorSinkConfig,
  failover: FailoverSinkConfig,
  sap: SapSinkConfig,
  dynamics365: Dynamics365SinkConfig,
  s3: S3SinkConfig,
  's3-parquet': S3SinkConfig,
  telegram: NotificationSinkConfig,
  fcm: NotificationSinkConfig,
  discord: NotificationSinkConfig,
  slack: NotificationSinkConfig,
  database: DatabaseSinkConfig,
};

interface SinkFormProps {
  initialData?: Sink;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
  availableFields?: any[];
  incomingPayload?: any;
  sinks?: Sink[];
  upstreamSource?: any;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
}

export function SinkForm({ 
    initialData, 
    isEditing = false, 
    embedded = false, 
    onSave, 
    vhost, 
    workerID,
    availableFields,
    upstreamSource
}: SinkFormProps) {
  const navigate = useNavigate();
  const role = getRoleFromToken();
  const { availableVHosts } = useVHost();
  
  const {
    sink,
    testResult,
    setTestResult,
    testMutation,
    submitMutation,
    updateConfig,
    handleSinkChange,
    hasActiveReferencingWorkflow,
    referencingWorkflows
  } = useSinkForm({
    initialData,
    isEditing,
    embedded,
    onSave: (data) => {
        if (!embedded) navigate({ to: '/sinks' });
        if (onSave) onSave(data);
    },
    vhost,
    workerID
  });

  const availableVHostsList = role === 'Administrator' 
    ? (availableVHosts || []).map((v: any) => typeof v === 'string' ? v : v.name)
    : (availableVHosts || []);

  const [workers, setWorkers] = useState<any[]>([]);

  // Fetch workers for selection
  useEffect(() => {
    import('@/api').then(({ apiFetch }) => {
        apiFetch('/api/workers').then(res => res.json()).then(data => {
            setWorkers(data.data || []);
        });
    });
  }, []);

  return (
    <Stack gap="md">
      {hasActiveReferencingWorkflow && (
        <Alert 
          icon={<IconAlertCircle size="1.2rem" />} 
          title="Sink in Use" 
          color="orange" 
          variant="light"
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              This sink is currently used by {referencingWorkflows.filter(w => w.active).length} active workflow(s).
            </Text>
            <Text size="xs">
              To prevent data inconsistency or connection errors, you must stop the following workflows before making changes:
            </Text>
            <List size="xs" withPadding>
              {referencingWorkflows.filter(w => w.active).map(wf => (
                <List.Item key={wf.id}>
                  <Group gap={4}>
                    <Text size="xs" fw={600}>{wf.name}</Text>
                    <Tooltip label="View Workflow">
                      <ActionIcon 
                        variant="subtle" 
                        size="xs" 
                        onClick={() => window.open(`/workflows/${wf.id}`, '_blank')}
                      >
                        <IconExternalLink size="0.8rem" />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                </List.Item>
              ))}
            </List>
          </Stack>
        </Alert>
      )}

      <SinkWizard 
        sink={sink}
        isEditing={isEditing}
        embedded={embedded}
        availableVHostsList={availableVHostsList}
        workers={workers}
        sinkTypes={SINK_TYPES}
        testMutation={testMutation}
        submitMutation={submitMutation}
        testResult={testResult}
        setTestResult={setTestResult}
        updateConfig={updateConfig}
        handleSinkChange={handleSinkChange}
        onCancel={() => embedded ? (onSave && onSave(null)) : navigate({ to: '/sinks' })}
        configComponents={configComponents}
        availableFields={availableFields}
        upstreamSource={upstreamSource}
      />
    </Stack>
  );
}
