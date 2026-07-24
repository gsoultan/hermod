import { useState, type ChangeEvent } from 'react';
import { Stepper, Button, Group, Stack, Card, Text, Divider, Alert, Fieldset, TextInput, Checkbox, ActionIcon, Tooltip } from '@mantine/core';
import { IconCheck, IconDatabase, IconActivity, IconInfoCircle, IconRefresh, IconPlayerPlay } from '@tabler/icons-react';
import { SourceBasics } from '../workflow/Source/SourceBasics';
import { SourceConfigFields } from '../workflow/Source/SourceConfigFields';

interface SourceWizardProps {
  source: any;
  isEditing: boolean;
  embedded: boolean;
  availableVHostsList: string[];
  workers: any[];
  sourceTypes: any[];
  testMutation: any;
  submitMutation: any;
  testResult: any;
  setTestResult: (res: any) => void;
  updateConfig: (key: string, value: any) => void;
  handleSourceChange: (updates: any) => void;
  onCancel: () => void;
  discoveredTables: string[];
  discoveredDatabases: string[];
  isFetchingTables: boolean;
  isFetchingDBs: boolean;
  fetchTables: (db?: string) => void;
  fetchDatabases: () => void;
  handleFileUpload: (file: File | null) => void;
  uploading: boolean;
  allSources: any[];
  setShowSetup: (show: boolean) => void;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
  onRunSimulation?: (input?: any) => void;
}

export function SourceWizard({
  source,
  isEditing,
  embedded,
  availableVHostsList,
  workers,
  sourceTypes,
  testMutation,
  submitMutation,
  testResult,
  setTestResult,
  updateConfig,
  handleSourceChange,
  onCancel,
  discoveredTables,
  discoveredDatabases,
  isFetchingTables,
  isFetchingDBs,
  fetchTables,
  fetchDatabases,
  handleFileUpload,
  uploading,
  allSources,
  setShowSetup,
  onRefreshFields,
  isRefreshing,
  onRunSimulation
}: SourceWizardProps) {
  const [active, setActive] = useState(0);
  const nextStep = () => setActive((current) => (current < 3 ? current + 1 : current));
  const prevStep = () => setActive((current) => (current > 0 ? current - 1 : current));

  return (
    <Stack gap="xl">
      <Stepper active={active} onStepClick={setActive} allowNextStepsSelect={false}>
        <Stepper.Step 
          label="Basics" 
          description="Type & Identity" 
          icon={<IconInfoCircle size="1.1rem" />}
          completedIcon={<IconCheck size="1.1rem" />}
        >
          <Card withBorder padding="lg" radius="md" mt="md">
            <Stack gap="md">
              <Text fw={600} size="lg">Step 1: Source Identity</Text>
              <Text size="sm" c="dimmed">Name your source and select the data origin type.</Text>
              <Divider />
              <SourceBasics 
                source={source}
                handleSourceChange={handleSourceChange}
                embedded={embedded}
                availableVHostsList={availableVHostsList}
                workers={workers}
                sourceTypes={sourceTypes}
                setShowSetup={setShowSetup}
              />
            </Stack>
          </Card>
        </Stepper.Step>

        <Stepper.Step 
          label="Connection" 
          description="Access Parameters" 
          icon={<IconDatabase size="1.1rem" />}
          completedIcon={<IconCheck size="1.1rem" />}
        >
          <Card withBorder padding="lg" radius="md" mt="md">
            <Stack gap="md">
              <Text fw={600} size="lg">Step 2: Connection Settings</Text>
              <Text size="sm" c="dimmed">Configure how Hermod connects to the data source.</Text>
              <Divider />
              {testResult && (
                <Alert 
                  color={testResult.status === 'ok' ? 'green' : 'red'} 
                  title={testResult.status === 'ok' ? 'Connected' : 'Connection Failed'}
                  withCloseButton
                  onClose={() => setTestResult(null)}
                >
                  {testResult.message}
                </Alert>
              )}
              <SourceConfigFields 
                source={source}
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
              <Group justify="flex-end">
                {onRefreshFields && (
                  <Tooltip label="Refresh Fields">
                    <ActionIcon 
                      variant="light" 
                      onClick={onRefreshFields} 
                      loading={isRefreshing}
                    >
                      <IconRefresh size="1.1rem" />
                    </ActionIcon>
                  </Tooltip>
                )}
                {onRunSimulation && (
                   <Button 
                    variant="light" 
                    color="green"
                    leftSection={<IconPlayerPlay size="1rem" />}
                    onClick={() => onRunSimulation()}
                   >
                    Simulate
                   </Button>
                )}
                <Button 
                  variant="light" 
                  onClick={() => testMutation.mutate(source)} 
                  loading={testMutation.isPending}
                >
                  Test Connection
                </Button>
              </Group>
            </Stack>
          </Card>
        </Stepper.Step>

        <Stepper.Step 
          label="Reliability" 
          description="Ingestion Tuning" 
          icon={<IconActivity size="1.1rem" />}
          completedIcon={<IconCheck size="1.1rem" />}
        >
          <Card withBorder padding="lg" radius="md" mt="md">
            <Stack gap="md">
              <Text fw={600} size="lg">Step 3: Reliability Settings</Text>
              <Text size="sm" c="dimmed">Define how the source handles disconnections and restarts.</Text>
              <Divider />
              <Fieldset legend="Auto-Recovery" radius="md">
                <TextInput 
                  label="Reconnect Intervals" 
                  placeholder="1s, 5s, 30s, 1m" 
                  description="Backoff strategy for reconnection attempts (comma-separated)."
                  value={source.config.reconnect_intervals || ''} 
                  onChange={(e) => updateConfig('reconnect_intervals', e.target.value)} 
                />
              </Fieldset>
              {source.type === 'postgres' && (
                <Fieldset legend="CDC Maintenance" radius="md">
                  <Checkbox 
                    label="Persistent Replication Slot"
                    description="Keep the replication slot on the server even when the workflow is stopped. Recommended for production."
                    checked={source.config.persistent_slot === 'true'}
                    onChange={(e: ChangeEvent<HTMLInputElement>) => updateConfig('persistent_slot', e.currentTarget.checked ? 'true' : 'false')}
                  />
                </Fieldset>
              )}
            </Stack>
          </Card>
        </Stepper.Step>

        <Stepper.Completed>
          <Card withBorder padding="xl" radius="md" mt="md" bg="var(--mantine-color-blue-light)">
            <Stack align="center" gap="md">
              <IconCheck size="3rem" color="var(--mantine-color-blue-6)" />
              <Text fw={700} size="xl">Source Configured!</Text>
              <Text ta="center">Your source is ready to be used in workflows.</Text>
              <Button 
                size="lg" 
                onClick={() => submitMutation.mutate(source)} 
                loading={submitMutation.isPending}
              >
                {isEditing ? 'Update Source' : 'Create Source'}
              </Button>
            </Stack>
          </Card>
        </Stepper.Completed>
      </Stepper>

      <Group justify="space-between" mt="xl">
        <Button variant="default" onClick={onCancel}>Cancel</Button>
        <Group>
          {active !== 0 && (
            <Button variant="default" onClick={prevStep}>
              Back
            </Button>
          )}
          {active < 3 && (
            <Button 
              onClick={nextStep} 
              disabled={active === 0 && !source.name}
            >
              Next Step
            </Button>
          )}
        </Group>
      </Group>
    </Stack>
  );
}
