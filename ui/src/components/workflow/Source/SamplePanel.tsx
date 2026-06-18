import { Card, Stack, Group, Badge, Divider, Loader, Text, Button, JsonInput, Tooltip, List, ThemeIcon } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import type { Source } from '@/types';
import type { FC } from 'react';
import {
  IconAlertCircle,
  IconCircleCheck,
  IconCircleDot,
  IconCopy,
  IconFileImport,
  IconInfoCircle,
  IconPlayerPlay,
  IconRefresh,
  IconShieldCheck,
} from '@tabler/icons-react';
import { isNonDestructiveSample, validateSourceForSampling } from './sourceSampling';

interface SamplePanelProps {
  sampleData: any;
  isFetchingSample: boolean;
  sampleError: string | null;
  onRunSimulation?: (sample?: any) => void;
  fetchSample: (s: Source) => void;
  source: Source;
  /** Epoch millis of the last successful sample, used for the "last previewed" hint. */
  lastSampledAt?: number | null;
  /** Whether a Test Connection has succeeded, used to advance the guided stepper. */
  testOk?: boolean;
  /** Whether the source has been saved (has an id), used for the final stepper step. */
  saved?: boolean;
}

/** formatRelativeTime renders a compact, human-friendly "time ago" label. */
function formatRelativeTime(ts: number): string {
  const diffMs = Date.now() - ts;
  const sec = Math.max(0, Math.round(diffMs / 1000));
  if (sec < 5) return 'just now';
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return new Date(ts).toLocaleString();
}

type StepState = 'done' | 'active' | 'todo';

interface StepDef {
  label: string;
  state: StepState;
}

const GuidedStepper: FC<{ steps: StepDef[] }> = ({ steps }) => (
  <Group gap={6} wrap="nowrap" justify="center">
    {steps.map((step, idx) => {
      const color = step.state === 'done' ? 'green' : step.state === 'active' ? 'blue' : 'gray';
      return (
        <Group key={step.label} gap={6} wrap="nowrap">
          <ThemeIcon
            size={18}
            radius="xl"
            variant={step.state === 'todo' ? 'light' : 'filled'}
            color={color}
          >
            {step.state === 'done' ? <IconCircleCheck size="0.8rem" /> : <IconCircleDot size="0.8rem" />}
          </ThemeIcon>
          <Text size="xs" c={step.state === 'todo' ? 'dimmed' : color} fw={step.state === 'active' ? 700 : 500}>
            {step.label}
          </Text>
          {idx < steps.length - 1 && <Divider orientation="vertical" />}
        </Group>
      );
    })}
  </Group>
);

export const SamplePanel: FC<SamplePanelProps> = ({
  sampleData,
  isFetchingSample,
  sampleError,
  onRunSimulation,
  fetchSample,
  source,
  lastSampledAt,
  testOk,
  saved,
}) => {
  const validation = validateSourceForSampling(source);
  const canSample = validation.valid && !isFetchingSample;
  const nonDestructive = isNonDestructiveSample(source.type);

  // Guided steps: Configure → Test → Preview → Save.
  const steps: StepDef[] = [
    { label: 'Configure', state: validation.valid ? 'done' : 'active' },
    {
      label: 'Test',
      state: testOk ? 'done' : validation.valid ? 'active' : 'todo',
    },
    {
      label: 'Preview',
      state: sampleData ? 'done' : testOk ? 'active' : 'todo',
    },
    {
      label: 'Save',
      state: saved ? 'done' : sampleData ? 'active' : 'todo',
    },
  ];

  const renderInvalidReasons = () => (
    <Stack align="center" py="md" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
      <IconInfoCircle size="1.8rem" color="var(--mantine-color-yellow-6)" />
      <Text size="xs" fw={600} ta="center">
        Complete the configuration to preview your data
      </Text>
      <List size="xs" spacing={4} center icon={<IconCircleDot size="0.7rem" color="var(--mantine-color-yellow-6)" />}>
        {validation.issues.map((issue) => (
          <List.Item key={issue}>
            <Text size="xs" c="dimmed">{issue}</Text>
          </List.Item>
        ))}
      </List>
    </Stack>
  );

  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-body)">
      <Stack h="100%">
        <Group justify="space-between" px="xs">
          <Group gap="xs">
            <IconFileImport size="1.2rem" color="var(--mantine-color-green-6)" />
            <Text size="sm" fw={700} c="dimmed">3. LIVE PREVIEW</Text>
          </Group>
          <Group gap="xs">
            {nonDestructive && (
              <Tooltip
                label="Previewing reads existing data without consuming messages or skipping records during a real run."
                withArrow
                multiline
                w={240}
              >
                <Badge color="teal" variant="light" leftSection={<IconShieldCheck size="0.7rem" />}>
                  Non-destructive
                </Badge>
              </Tooltip>
            )}
            {sampleData && <Badge color="green" variant="light">Captured</Badge>}
          </Group>
        </Group>

        <GuidedStepper steps={steps} />
        <Divider />

        {isFetchingSample ? (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <Loader size="sm" />
            <Text size="xs" c="dimmed">Fetching sample data...</Text>
          </Stack>
        ) : !validation.valid ? (
          renderInvalidReasons()
        ) : sampleError ? (
          <Stack align="center" py="md" gap="xs" style={{ flex: 1, justifyContent: 'center' }}>
            <IconAlertCircle size="1.5rem" color="red" />
            <Text size="xs" c="red" ta="center">{sampleError}</Text>
            <Button size="xs" variant="subtle" color="red" onClick={() => fetchSample(source)}>Retry</Button>
          </Stack>
        ) : sampleData ? (
          <Stack gap="xs" style={{ flex: 1 }}>
            <Group justify="space-between" align="flex-end">
              <Stack gap={0}>
                <Text size="sm" fw={500}>Sample Output (JSON)</Text>
                {lastSampledAt ? (
                  <Text size="xs" c="dimmed">Last previewed {formatRelativeTime(lastSampledAt)}</Text>
                ) : null}
              </Stack>
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
                input: { flex: 1, fontFamily: 'monospace', fontSize: '11px', backgroundColor: 'var(--mantine-color-body)' }
              }}
            />
          </Stack>
        ) : (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <IconInfoCircle size="2rem" color="var(--mantine-color-gray-4)" />
            <Text size="xs" c="dimmed" ta="center">Test connection to see sample data from your source.</Text>
            <Tooltip
              label={canSample ? 'Read a single record without consuming data' : validation.issues.join(' ')}
              withArrow
              multiline
              w={240}
              disabled={canSample}
            >
              <Button size="xs" variant="light" disabled={!canSample} onClick={() => fetchSample(source)}>
                Fetch Sample Now
              </Button>
            </Tooltip>
          </Stack>
        )}
      </Stack>
    </Card>
  );
};
