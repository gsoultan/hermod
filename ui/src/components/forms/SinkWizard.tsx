import { useState } from 'react';
import { Stepper, Button, Group, Stack, TextInput, Card, Text, Divider, Alert, Fieldset } from '@mantine/core';
import { IconCheck, IconDatabase, IconActivity, IconInfoCircle } from '@tabler/icons-react';
import { SinkBasics } from '../workflow/Sink/SinkBasics';
import { RetryPolicyFields } from '../workflow/Sink/RetryPolicyFields';
import { Suspense } from 'react';

interface SinkWizardProps {
  sink: any;
  isEditing: boolean;
  embedded: boolean;
  availableVHostsList: string[];
  workers: any[];
  sinkTypes: any[];
  testMutation: any;
  submitMutation: any;
  testResult: any;
  setTestResult: (res: any) => void;
  updateConfig: (key: string, value: any) => void;
  handleSinkChange: (field: string, value: any) => void;
  onCancel: () => void;
  configComponents: Record<string, any>;
  availableFields?: any[];
  upstreamSource?: any;
}

export function SinkWizard({
  sink,
  isEditing,
  embedded,
  availableVHostsList,
  workers,
  sinkTypes,
  testMutation,
  submitMutation,
  testResult,
  setTestResult,
  updateConfig,
  handleSinkChange,
  onCancel,
  configComponents,
  availableFields,
  upstreamSource
}: SinkWizardProps) {
  const [active, setActive] = useState(0);
  const nextStep = () => setActive((current) => (current < 3 ? current + 1 : current));
  const prevStep = () => setActive((current) => (current > 0 ? current - 1 : current));

  const SelectedConfig = configComponents[sink.type] || configComponents['database'];

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
              <Text fw={600} size="lg">Step 1: Sink Basics</Text>
              <Text size="sm" c="dimmed">Provide a unique name and select the destination type.</Text>
              <Divider />
              <SinkBasics 
                embedded={embedded}
                name={sink.name}
                onChangeName={(val) => handleSinkChange('name', val)}
                vhost={sink.vhost}
                onChangeVHost={(val) => handleSinkChange('vhost', val)}
                workerId={sink.worker_id}
                onChangeWorkerId={(val) => handleSinkChange('worker_id', val)}
                type={sink.type}
                onChangeType={(val) => handleSinkChange('type', val)}
                sequential={sink.sequential}
                onChangeSequential={(val) => handleSinkChange('sequential', val)}
                vhostOptions={availableVHostsList}
                workerOptions={workers.map(w => ({ value: w.id, label: w.name || w.id }))}
                sinkTypes={sinkTypes}
              />
            </Stack>
          </Card>
        </Stepper.Step>

        <Stepper.Step 
          label="Connection" 
          description="Access Details" 
          icon={<IconDatabase size="1.1rem" />}
          completedIcon={<IconCheck size="1.1rem" />}
        >
          <Card withBorder padding="lg" radius="md" mt="md">
            <Stack gap="md">
              <Text fw={600} size="lg">Step 2: Connection Settings</Text>
              <Text size="sm" c="dimmed">Configure how Hermod connects to the destination.</Text>
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
              <Suspense fallback={<Text>Loading config fields...</Text>}>
                {SelectedConfig && (
                  <SelectedConfig 
                    type={sink.type}
                    config={sink.config} 
                    updateConfig={updateConfig} 
                    handleSinkChange={handleSinkChange}
                    availableFields={availableFields}
                    upstreamSource={upstreamSource}
                  />
                )}
              </Suspense>
              <Group justify="flex-end">
                <Button 
                  variant="light" 
                  onClick={() => testMutation.mutate(sink)} 
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
          description="Retries & Batching" 
          icon={<IconActivity size="1.1rem" />}
          completedIcon={<IconCheck size="1.1rem" />}
        >
          <Card withBorder padding="lg" radius="md" mt="md">
            <Stack gap="md">
              <Text fw={600} size="lg">Step 3: Reliability & Performance</Text>
              <Text size="sm" c="dimmed">Tune how messages are delivered and retried.</Text>
              <Divider />
              <RetryPolicyFields 
                maxRetries={sink.config.max_retries}
                retryInterval={sink.config.retry_interval}
                onChangeMaxRetries={(val) => updateConfig('max_retries', val)}
                onChangeRetryInterval={(val) => updateConfig('retry_interval', val)}
              />
              <Fieldset legend="Batching" radius="md">
                 <Stack gap="sm">
                   <TextInput 
                     label="Batch Size" 
                     type="number"
                     placeholder="100"
                     value={sink.config.batch_size || ''}
                     onChange={(e) => updateConfig('batch_size', e.target.value)}
                   />
                   <TextInput 
                     label="Batch Timeout" 
                     placeholder="1s"
                     value={sink.config.batch_timeout || ''}
                     onChange={(e) => updateConfig('batch_timeout', e.target.value)}
                   />
                 </Stack>
              </Fieldset>
            </Stack>
          </Card>
        </Stepper.Step>

        <Stepper.Completed>
          <Card withBorder padding="xl" radius="md" mt="md" bg="var(--mantine-color-blue-light)">
            <Stack align="center" gap="md">
              <IconCheck size="3rem" color="var(--mantine-color-blue-6)" />
              <Text fw={700} size="xl">Ready to Go!</Text>
              <Text ta="center">Your sink is configured and tested. Click below to save it and start using it in workflows.</Text>
              <Button 
                size="lg" 
                onClick={() => submitMutation.mutate(sink)} 
                loading={submitMutation.isPending}
              >
                {isEditing ? 'Update Sink' : 'Create Sink'}
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
              disabled={active === 0 && !sink.name}
            >
              Next Step
            </Button>
          )}
        </Group>
      </Group>
    </Stack>
  );
}
