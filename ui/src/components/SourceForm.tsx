import { Button, Group, TextInput, Select, Stack, Alert, Divider, Text, Grid, JsonInput, Badge, Modal, Card, ScrollArea, Tooltip, Fieldset } from '@mantine/core';
import { useSuspenseQuery } from '@tanstack/react-query';
import { notifications } from '@mantine/notifications';
import { useNavigate } from '@tanstack/react-router';
import { apiFetch, getRoleFromToken } from '../api';
import { useVHost } from '../context/VHostContext';
import { useSourceForm } from '../hooks/useSourceForm';
import type { Source, Worker, VHost } from '../types';
import { SourceBasics } from './Source/SourceBasics';
import { SnapshotModal } from './Source/SnapshotModal';
import { SamplePanel } from './Source/SamplePanel';
import { SourceConfigFields } from './Source/SourceConfigFields';
import { SourceSetupInstructions } from './Source/SourceSetupInstructions';
import { FormScriptSnippet } from './Source/FormScriptSnippet';
import { PublicFormLink } from './Source/PublicFormLink';
import { WebhookHistory } from './Source/WebhookHistory';
import { CDCReuseModal } from './Source/CDCReuseModal';
import { FormPreview } from './FormLayoutBuilder';
import { IconBraces, IconCamera, IconCheck, IconLayout, IconSettings } from '@tabler/icons-react';
const API_BASE = '/api';

// API list response shape used across queries
interface ApiListResponse<T> {
  data: T[];
  total: number;
}

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
  onRunSimulation?: (sample?: any) => void;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
  vhost?: string;
  workerID?: string;
}

export function SourceForm({ initialData, isEditing = false, embedded = false, onSave, onRunSimulation, onRefreshFields, isRefreshing, vhost, workerID }: SourceFormProps) {
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
    sampleData,
    setSampleData,
    isFetchingSample,
    sampleError,
    testInput,
    setTestInput,
    selectedSampleTable,
    setSelectedSampleTable,
    showSetup,
    setShowSetup,
    formPreviewOpened,
    setFormPreviewOpened,
    cdcReusePrompt,
    setCdcReusePrompt,
    selectedSnapshotTables,
    setSelectedSnapshotTables,
    snapshotModalOpened,
    setSnapshotModalOpened,
    isLoadingRefWf,
    referencingError,
    hasActiveReferencingWorkflow,
    snapshotMutation,
    testMutation,
    submitMutation,
    isCDC,
    isDatabaseSource,
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

  const setupTitleId = 'source-setup-modal-title';
  const setupDescId = 'source-setup-modal-desc';

  const useCDCChecked = source.config?.use_cdc !== 'false';

  // Derive edit mode from either the explicit prop or the presence of an id.
  // This ensures correct labeling when editing from contexts that don't pass isEditing.
  const isEditingResolved = Boolean(isEditing || (source as any)?.id);

  // Only the "Form Submission" source type is allowed to save without a successful Test Connection.
  const testRequired = (source as any)?.type !== 'form';

  const { data: vhostsResponse } = useSuspenseQuery<ApiListResponse<VHost>>({
    queryKey: ['vhosts'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/vhosts`);
      if (res.ok) return res.json();
      return { data: [], total: 0 } as ApiListResponse<VHost>;
    }
  });

  const { data: workersResponse } = useSuspenseQuery<ApiListResponse<Worker>>({
    queryKey: ['workers'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/workers`);
      if (res.ok) return res.json();
      return { data: [], total: 0 } as ApiListResponse<Worker>;
    }
  });

  const { data: sourcesResponse } = useSuspenseQuery<ApiListResponse<Source>>({
    queryKey: ['sources'],
    queryFn: async () => {
      const res = await apiFetch(`${API_BASE}/sources`);
      if (res.ok) return res.json();
      return { data: [], total: 0 } as ApiListResponse<Source>;
    }
  });

  const vhosts = Array.isArray(vhostsResponse?.data) ? vhostsResponse!.data : [];
  const workers = Array.isArray(workersResponse?.data) ? workersResponse!.data : [];
  const allSources = Array.isArray(sourcesResponse?.data) ? sourcesResponse!.data : [];

  const availableVHostsList: string[] = role === ADMIN_ROLE
    ? vhosts.map((v: VHost) => v.name)
    : (availableVHosts || []);

  // (debug logs removed)

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
          <SourceSetupInstructions 
            sourceType={source.type} 
            useCDCChecked={useCDCChecked} 
            config={source.config} 
            updateConfig={updateConfig} 
          />
        </Stack>
      </Modal>

      <SnapshotModal 
        opened={snapshotModalOpened}
        onClose={() => setSnapshotModalOpened(false)}
        source={source as any}
        selectedSnapshotTables={selectedSnapshotTables}
        setSelectedSnapshotTables={setSelectedSnapshotTables}
        snapshotMutation={snapshotMutation}
      />

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
                  data={((source && source.config && typeof source.config.tables === 'string') ? source.config.tables : '')
                    .split(',')
                    .map((t: string) => t.trim())
                    .filter(Boolean)}
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

                  <SourceBasics 
                    source={source as any}
                    handleSourceChange={handleSourceChange}
                    embedded={embedded}
                    availableVHostsList={availableVHostsList}
                    workers={workers}
                    sourceTypes={SOURCE_TYPES}
                    setShowSetup={setShowSetup}
                  />
                  
                  <Fieldset legend="Connection Parameters" radius="md">
                    <SourceConfigFields 
                      source={source as any}
                      updateConfig={updateConfig}
                      discoveredTables={discoveredTables}
                      discoveredDatabases={discoveredDatabases}
                      isFetchingTables={isFetchingTables}
                      isFetchingDBs={isFetchingDBs}
                      fetchTables={fetchTables}
                      fetchDatabases={fetchDatabases}
                      handleFileUpload={handleFileUpload}
                      uploading={uploading}
                      allSources={allSources}
                    />
                  </Fieldset>

                  <Fieldset legend="Reliability" radius="md">
                    <TextInput 
                      label="Reconnect Intervals" 
                      placeholder="1s, 5s, 30s, 1m" 
                      description="Comma-separated list of durations for reconnection attempts. If one value is provided (e.g., '10s'), it repeats indefinitely."
                      value={source.config.reconnect_intervals || ''} 
                      onChange={(e) => updateConfig('reconnect_intervals', e.target.value)} 
                    />
                  </Fieldset>
                </Stack>
              </ScrollArea>

              <Divider mt="md" />
              <Group justify="flex-end" pt="xs">
                {!embedded && <Button variant="outline" size="xs" onClick={() => navigate({ to: '/sources' })}>Cancel</Button>}
                {isEditingResolved && isDatabaseSource(source.type) && (
                  <Tooltip
                    label={
                      !source.id
                        ? 'Save the source first'
                        : referencingError
                          ? 'Unable to determine referencing workflows'
                          : hasActiveReferencingWorkflow
                            ? 'Trigger a one-time snapshot of current table contents'
                            : 'Start a workflow that uses this source to enable snapshots'
                    }
                    withArrow
                    disabled={!isEditingResolved || (!source.id) || (!!hasActiveReferencingWorkflow)}
                  >
                    <Button 
                      variant="outline" 
                      color="orange" 
                      size="xs" 
                      leftSection={<IconCamera size="1rem" />} 
                      onClick={() => {
                        const tables = (((source as any)?.config?.tables as string) || '')
                          .split(',')
                          .map((t: string) => t.trim())
                          .filter(Boolean);
                        setSelectedSnapshotTables(tables);
                        setSnapshotModalOpened(true);
                      }}
                      loading={snapshotMutation.isPending}
                      disabled={!source.id || isLoadingRefWf || !hasActiveReferencingWorkflow}
                    >
                      Run Initial Snapshot
                    </Button>
                  </Tooltip>
                )}
                {(source as any).type !== 'form' && (
                  <Button 
                    variant="outline" 
                    color="blue" 
                    size="xs" 
                    onClick={() => testMutation.mutate(source)} 
                    loading={testMutation.isPending}
                  >
                    Test Connection
                  </Button>
                )}
                <Button 
                  size="xs"
                  // For non-Form sources, require a successful Test Connection before saving.
                  disabled={(testRequired && testResult?.status !== 'ok') || !source.name || (!embedded && !source.vhost)}
                  onClick={() => {
                    submitMutation.mutate(source);
                  }} 
                  loading={submitMutation.isPending}
                >
                  {isEditingResolved ? 'Update Source' : (embedded ? 'Confirm' : 'Create Source')}
                </Button>
              </Group>
            </Stack>
          </Card>
        </Grid.Col>

        <Grid.Col span={{ base: 12, md: 12, lg: 4 }}>
          {(source as any).type === 'form' && (source as any).config?.path ? (
            <Stack gap="md" h="100%">
              <PublicFormLink path={(source as any).config?.path} />
              <FormScriptSnippet path={(source as any).config?.path} />
              <Card withBorder p="md" radius="md">
                <Stack gap="xs">
                  <Text size="sm" fw={600}>Layout Preview</Text>
                  <Button 
                    variant="light" 
                    leftSection={<IconLayout size="1rem" />} 
                    onClick={() => setFormPreviewOpened(true)}
                  >
                    Preview Form Layout
                  </Button>
                </Stack>
              </Card>
            </Stack>
          ) : (source as any).type === 'webhook' && (source as any).config?.path ? (
            <WebhookHistory 
              path={(source as any).config?.path} 
              onReplaySuccess={() => {
                notifications.show({ title: 'Replayed', message: 'Webhook replayed successfully.', color: 'green' });
              }}
              onSelectSample={(body) => {
                 try {
                    setSampleData(JSON.parse(body));
                 } catch (e) {
                    setSampleData(body as any);
                 }
              }}
            />
          ) : (
            <SamplePanel 
              sampleData={sampleData} 
              isFetchingSample={isFetchingSample || !!isRefreshing} 
              sampleError={sampleError} 
              onRunSimulation={onRunSimulation}
              fetchSample={(s) => {
                if (onRefreshFields) {
                  onRefreshFields();
                } else {
                  fetchSample(s);
                }
              }}
              source={source as any}
            />
          )}
        </Grid.Col>
      </Grid>
      <CDCReuseModal 
        cdcReusePrompt={cdcReusePrompt}
        onClose={() => setCdcReusePrompt(null)}
        onAccept={() => {
          setCdcReusePrompt(null);
          setTestResult({ status: 'ok', message: 'Using existing slot/publication.' });
          fetchSample(source);
        }}
      />
      <FormPreview 
        opened={formPreviewOpened} 
        onClose={() => setFormPreviewOpened(false)} 
        fields={Array.isArray((source as any).config?.fields) ? (source as any).config.fields : []} 
        title={(source as any).config?.form_title}
        description={(source as any).config?.form_description}
      />
    </>
  );
}







