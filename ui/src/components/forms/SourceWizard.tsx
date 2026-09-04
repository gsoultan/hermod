import { useState, type ChangeEvent } from 'react';
import { Stepper, Button, Group, Stack, Card, Text, Divider, Alert, Fieldset, TextInput, Checkbox, ActionIcon, Tooltip } from '@mantine/core';
import { IconCheck, IconDatabase, IconActivity, IconInfoCircle, IconRefresh, IconPlayerPlay, IconDeviceFloppy } from '@tabler/icons-react';
import { SourceBasics } from '../workflow/Source/SourceBasics';
import { SourceConfigFields } from '../workflow/Source/SourceConfigFields';
import { validateName, validateType, validateVHost } from '@/hooks/useEntityBasicsForm';
import { missingConnectionFieldsWithUri } from '@/lib/connectorRequirements';

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

  // Fields the user must supply before a step can be left. Without this the
  // wizard advanced through every step with everything blank and only failed at
  // submit, by which point the step that was wrong is no longer on screen.
  const missingForStep = (step: number): string[] => {
    if (step === 0) {
      // Same validators the fields use, so the Next button and the inline
      // errors always agree about what is wrong.
      return [
        validateName(source.name),
        validateType(source.type),
        validateVHost(source.vhost, !embedded),
      ].filter(Boolean) as string[];
    }
    if (step === 1) {
      // The connection step gets the same treatment Basics got: what this
      // connector minimally needs, named in the user's words, before Next
      // works. One module holds the per-type answer, so the tooltip and the
      // gate cannot disagree.
      return missingConnectionFieldsWithUri('source', source.type, source.config);
    }
    return [];
  };
  const missing = missingForStep(active);

  const nextStep = () => {
    if (missingForStep(active).length > 0) return;
    setActive((current) => (current < 3 ? current + 1 : current));
  };
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
                    <ActionIcon aria-label="Refresh Fields" 
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
              <Text size="sm" c="dimmed">
                The defaults here are safe. If you are unsure, change nothing and go on.
              </Text>
              <Divider />
              <Fieldset legend="Auto-Recovery" radius="md">
                <TextInput
                  label="If the connection drops, retry after…"
                  placeholder="1s, 5s, 30s, 1m"
                  description="Waits between reconnection attempts, shortest first. Leave empty to use the defaults shown."
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
          {/*
            Editing is not a wizard.

            Save used to exist only inside Stepper.Completed, so changing one
            field on an existing source meant clicking Next through every step to
            reach a screen whose only content was this button. The steps stay —
            they are still the clearest way to group the fields — but an existing
            source can be saved from wherever the user happens to be.
          */}
          {isEditing && (
            <Tooltip
              label={missing.length ? `Fix this step first: ${missing.join(', ')}` : ''}
              disabled={missing.length === 0}
              withArrow
            >
              <span>
                <Button
                  onClick={() => submitMutation.mutate(source)}
                  loading={submitMutation.isPending}
                  disabled={missing.length > 0}
                  leftSection={<IconDeviceFloppy size="1rem" />}
                >
                  Update Source
                </Button>
              </span>
            </Tooltip>
          )}
          {active < 3 && (
            <Tooltip
              label={missing.length ? `Required: ${missing.join(', ')}` : ''}
              disabled={missing.length === 0}
              withArrow
            >
              {/* span keeps the tooltip reachable while the button is disabled */}
              <span>
                <Button
                  onClick={nextStep}
                  disabled={missing.length > 0}
                  variant={isEditing ? 'default' : 'filled'}
                >
                  Next Step
                </Button>
              </span>
            </Tooltip>
          )}
        </Group>
      </Group>
    </Stack>
  );
}
