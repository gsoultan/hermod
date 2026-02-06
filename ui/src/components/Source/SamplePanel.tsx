import { Card, Stack, Group, Badge, Divider, Loader, Text, Button, JsonInput } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import type { Source } from '../../types';
import type { FC } from 'react';
import { IconAlertCircle, IconCopy, IconFileImport, IconInfoCircle, IconPlayerPlay, IconRefresh } from '@tabler/icons-react';
interface SamplePanelProps {
  sampleData: any;
  isFetchingSample: boolean;
  sampleError: string | null;
  onRunSimulation?: (sample?: any) => void;
  fetchSample: (s: Source) => void;
  source: Source;
}

export const SamplePanel: FC<SamplePanelProps> = ({
  sampleData,
  isFetchingSample,
  sampleError,
  onRunSimulation,
  fetchSample,
  source
}) => {
  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%" bg="var(--mantine-color-gray-0)">
      <Stack h="100%">
        <Group justify="space-between" px="xs">
          <Group gap="xs">
            <IconFileImport size="1.2rem" color="var(--mantine-color-green-6)" />
            <Text size="sm" fw={700} c="dimmed">3. LIVE PREVIEW</Text>
          </Group>
          {sampleData && <Badge color="green" variant="light">Captured</Badge>}
        </Group>
        <Divider />
        
        {isFetchingSample ? (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <Loader size="sm" />
            <Text size="xs" c="dimmed">Fetching sample data...</Text>
          </Stack>
        ) : sampleError ? (
          <Stack align="center" py="md" gap="xs" style={{ flex: 1, justifyContent: 'center' }}>
            <IconAlertCircle size="1.5rem" color="red" />
            <Text size="xs" c="red" ta="center">{sampleError}</Text>
            <Button size="xs" variant="subtle" color="red" onClick={() => fetchSample(source)}>Retry</Button>
          </Stack>
        ) : sampleData ? (
          <Stack gap="xs" style={{ flex: 1 }}>
            <Group justify="space-between" align="flex-end">
              <Text size="sm" fw={500}>Sample Output (JSON)</Text>
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
                input: { flex: 1, fontFamily: 'monospace', fontSize: '11px', backgroundColor: 'var(--mantine-color-gray-0)' } 
              }}
            />
          </Stack>
        ) : (
          <Stack align="center" py="xl" gap="sm" style={{ flex: 1, justifyContent: 'center' }}>
            <IconInfoCircle size="2rem" color="var(--mantine-color-gray-4)" />
            <Text size="xs" c="dimmed" ta="center">Test connection to see sample data from your source.</Text>
            <Button size="xs" variant="light" onClick={() => fetchSample(source)}>Fetch Sample Now</Button>
          </Stack>
        )}
      </Stack>
    </Card>
  );
};


