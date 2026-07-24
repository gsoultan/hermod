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
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'mariadb', label: 'MariaDB' },
  { value: 'mssql', label: 'SQL Server' },
  { value: 'oracle', label: 'Oracle' },
  { value: 'mongodb', label: 'MongoDB' },
  { value: 'cassandra', label: 'Cassandra' },
  { value: 'sqlite', label: 'SQLite' },
  { value: 'clickhouse', label: 'ClickHouse' },
  { value: 'yugabyte', label: 'YugabyteDB' },
  { value: 'db2', label: 'IBM DB2' },
  { value: 'scylladb', label: 'ScyllaDB' },
  { value: 'eventstore', label: 'Event Store' },
  { value: 'batch_sql', label: 'Batch SQL' },
  { value: 'kafka', label: 'Kafka' },
  { value: 'nats', label: 'NATS' },
  { value: 'rabbitmq', label: 'RabbitMQ Stream' },
  { value: 'rabbitmq_queue', label: 'RabbitMQ Queue' },
  { value: 'redis', label: 'Redis Stream' },
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'twitter', label: 'Twitter (X)' },
  { value: 'facebook', label: 'Facebook' },
  { value: 'instagram', label: 'Instagram' },
  { value: 'linkedin', label: 'LinkedIn' },
  { value: 'tiktok', label: 'TikTok' },
  { value: 'sap', label: 'SAP OData' },
  { value: 'dynamics365', label: 'Dynamics 365' },
  { value: 'mainframe', label: 'Mainframe' },
  { value: 'webhook', label: 'Webhook' },
  { value: 'form', label: 'Form Submission' },
  { value: 'cron', label: 'Cron / Schedule' },
  { value: 'file', label: 'File / FTP / S3' },
  { value: 'excel', label: 'Excel (.xlsx)' },
  { value: 'googlesheets', label: 'Google Sheets' },
  { value: 'googleanalytics', label: 'Google Analytics' },
  { value: 'firebase', label: 'Firebase' },
  { value: 'http', label: 'HTTP Polling' },
  { value: 'graphql', label: 'GraphQL' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'websocket', label: 'WebSocket' },
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
