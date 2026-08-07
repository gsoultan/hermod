import { Group, Stack, Alert, Text, Modal, ActionIcon, Tooltip, List } from '@mantine/core';
import { useNavigate } from '@tanstack/react-router';
import { useVHost } from '@/context/VHostContext';
import { useSourceForm } from '@/hooks/useSourceForm';
import type { Source, VHost } from '@/types';
import { SourceWizard } from './SourceWizard';
import { SnapshotModal } from '../workflow/Source/SnapshotModal';
import { CDCReuseModal } from '../workflow/Source/CDCReuseModal';
import { SourceSetupInstructions } from '../workflow/Source/SourceSetupInstructions';
import { IconAlertCircle, IconExternalLink } from '@tabler/icons-react';
import { useSuspenseQuery } from '@tanstack/react-query';
import { apiFetch, getRoleFromToken } from '@/api';

const API_BASE = '/api';
const ADMIN_ROLE = 'Administrator' as const;

const SOURCE_TYPES = [
  { value: 'postgres', label: 'PostgreSQL' , group: 'Databases' },
  { value: 'mysql', label: 'MySQL' , group: 'Databases' },
  { value: 'mariadb', label: 'MariaDB' , group: 'Databases' },
  { value: 'mssql', label: 'SQL Server' , group: 'Databases' },
  { value: 'oracle', label: 'Oracle' , group: 'Databases' },
  { value: 'mongodb', label: 'MongoDB' , group: 'Databases' },
  { value: 'cassandra', label: 'Cassandra' , group: 'Databases' },
  { value: 'sqlite', label: 'SQLite' , group: 'Databases' },
  { value: 'clickhouse', label: 'ClickHouse' , group: 'Databases' },
  { value: 'yugabyte', label: 'YugabyteDB' , group: 'Databases' },
  { value: 'db2', label: 'IBM DB2' , group: 'Databases' },
  { value: 'scylladb', label: 'ScyllaDB' , group: 'Databases' },
  { value: 'eventstore', label: 'Event Store' , group: 'Databases' },
  { value: 'batch_sql', label: 'Batch SQL' , group: 'Databases' },
  { value: 'kafka', label: 'Kafka' , group: 'Messaging & Streams' },
  { value: 'nats', label: 'NATS' , group: 'Messaging & Streams' },
  { value: 'rabbitmq', label: 'RabbitMQ Stream' , group: 'Messaging & Streams' },
  { value: 'rabbitmq_queue', label: 'RabbitMQ Queue' , group: 'Messaging & Streams' },
  { value: 'redis', label: 'Redis Stream' , group: 'Messaging & Streams' },
  { value: 'discord', label: 'Discord' , group: 'Social Media' },
  { value: 'slack', label: 'Slack' , group: 'Social Media' },
  { value: 'twitter', label: 'Twitter (X)' , group: 'Social Media' },
  { value: 'facebook', label: 'Facebook' , group: 'Social Media' },
  { value: 'instagram', label: 'Instagram' , group: 'Social Media' },
  { value: 'linkedin', label: 'LinkedIn' , group: 'Social Media' },
  { value: 'tiktok', label: 'TikTok' , group: 'Social Media' },
  { value: 'sap', label: 'SAP OData' , group: 'Enterprise' },
  { value: 'dynamics365', label: 'Dynamics 365' , group: 'Enterprise' },
  { value: 'mainframe', label: 'Mainframe' , group: 'Enterprise' },
  { value: 'webhook', label: 'Webhook' , group: 'APIs & Triggers' },
  { value: 'form', label: 'Form Submission' , group: 'APIs & Triggers' },
  { value: 'cron', label: 'Cron / Schedule' , group: 'APIs & Triggers' },
  { value: 'file', label: 'File / FTP / S3' , group: 'Files & Storage' },
  { value: 'excel', label: 'Excel (.xlsx)' , group: 'Files & Storage' },
  { value: 'googlesheets', label: 'Google Sheets' , group: 'Files & Storage' },
  { value: 'googleanalytics', label: 'Google Analytics' , group: 'APIs & Triggers' },
  { value: 'firebase', label: 'Firebase' , group: 'APIs & Triggers' },
  { value: 'http', label: 'HTTP Polling' , group: 'APIs & Triggers' },
  { value: 'graphql', label: 'GraphQL' , group: 'APIs & Triggers' },
  { value: 'grpc', label: 'gRPC' , group: 'APIs & Triggers' },
  { value: 'websocket', label: 'WebSocket' , group: 'Messaging & Streams' },
];

interface SourceFormProps {
  initialData?: Source;
  isEditing?: boolean;
  embedded?: boolean;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
  onRunSimulation?: (input?: any) => void;
}

export function SourceForm({ 
    initialData, 
    isEditing = false, 
    embedded = false, 
    onSave, 
    vhost, 
    workerID,
    onRefreshFields,
    isRefreshing,
    onRunSimulation
}: SourceFormProps) {
  const { availableVHosts } = useVHost();
  const role = getRoleFromToken();
  const navigate = useNavigate();

  const {
    source,
    testResult,
    setTestResult,
    discoveredDatabases,
    discoveredTables,
    isFetchingDBs,
    isFetchingTables,
    uploading,
    showSetup,
    setShowSetup,
    cdcReusePrompt,
    setCdcReusePrompt,
    selectedSnapshotTables,
    setSelectedSnapshotTables,
    snapshotModalOpened,
    setSnapshotModalOpened,
    hasActiveReferencingWorkflow,
    referencingWorkflows,
    snapshotMutation,
    testMutation,
    submitMutation,
    fetchSample,
    handleFileUpload,
    fetchDatabases,
    fetchTables,
    handleSourceChange,
    updateConfig
  } = useSourceForm({
    initialData,
    isEditing,
    embedded,
    onSave,
    vhost,
    workerID
  });

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

  const vhosts = vhostsResponse?.data || [];
  const workers = workersResponse?.data || [];
  const allSources = sourcesResponse?.data || [];

  const availableVHostsList: string[] = role === ADMIN_ROLE
    ? vhosts.map((v: VHost) => v.name)
    : (availableVHosts || []);

  const useCDCChecked = source.config?.use_cdc !== 'false';

  return (
    <Stack gap="md">
      {hasActiveReferencingWorkflow && (
        <Alert 
          icon={<IconAlertCircle size="1.2rem" />} 
          title="Source in Use" 
          color="orange" 
          variant="light"
        >
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              This source is currently used by {referencingWorkflows.filter(w => w.active).length} active workflow(s).
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
                      <ActionIcon aria-label="Open in new tab" 
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

      <SourceWizard 
        source={source}
        isEditing={isEditing}
        embedded={embedded}
        availableVHostsList={availableVHostsList}
        workers={workers}
        sourceTypes={SOURCE_TYPES}
        testMutation={testMutation}
        submitMutation={submitMutation}
        testResult={testResult}
        setTestResult={setTestResult}
        updateConfig={updateConfig}
        handleSourceChange={handleSourceChange}
        onCancel={() => embedded ? (onSave && onSave(null)) : navigate({ to: '/sources' })}
        discoveredTables={discoveredTables}
        discoveredDatabases={discoveredDatabases}
        isFetchingTables={isFetchingTables}
        isFetchingDBs={isFetchingDBs}
        fetchTables={fetchTables}
        fetchDatabases={fetchDatabases}
        handleFileUpload={handleFileUpload}
        uploading={uploading}
        allSources={allSources}
        setShowSetup={setShowSetup}
        onRefreshFields={onRefreshFields}
        isRefreshing={isRefreshing}
        onRunSimulation={onRunSimulation}
      />

      <Modal 
        opened={showSetup} 
        onClose={() => setShowSetup(false)} 
        title={<Text fw={600}>{`${source.type.toUpperCase()} Setup Instructions`}</Text>}
        size="lg"
        radius="md"
        withCloseButton
      >
        <SourceSetupInstructions 
          sourceType={source.type} 
          useCDCChecked={useCDCChecked} 
          config={source.config} 
          updateConfig={updateConfig} 
        />
      </Modal>

      <SnapshotModal 
        opened={snapshotModalOpened}
        onClose={() => setSnapshotModalOpened(false)}
        source={source as any}
        selectedSnapshotTables={selectedSnapshotTables}
        setSelectedSnapshotTables={setSelectedSnapshotTables}
        snapshotMutation={snapshotMutation}
      />

      <CDCReuseModal 
        cdcReusePrompt={cdcReusePrompt}
        onClose={() => setCdcReusePrompt(null)}
        onAccept={() => {
          setCdcReusePrompt(null);
          setTestResult({ status: 'ok', message: 'Using existing slot/publication.' });
          fetchSample(source);
        }}
      />
    </Stack>
  );
}
