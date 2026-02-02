import { useState, useEffect } from 'react';
import { 
  Paper, Title, Text, Group, Stack, SimpleGrid, RingProgress, 
  Table, Badge, Box, ThemeIcon, ScrollArea, LoadingOverlay
} from '@mantine/core';
import { 
  IconShieldLock, IconFingerprint, IconCheck,
  IconLock, IconEyeOff
} from '@tabler/icons-react';
import { apiFetch } from '../api';

interface PIIStat {
  discoveries: Record<string, number>;
  last_updated: string;
}

export function ComplianceDashboard() {
  const [stats, setStats] = useState<Record<string, PIIStat>>({});
  const [loading, setLoading] = useState(true);
  const [workflows, setWorkflows] = useState<any[]>([]);

  useEffect(() => {
    const fetchData = async () => {
      try {
        const [statsRes, wfRes] = await Promise.all([
          apiFetch('/api/workflows/pii-stats'),
          apiFetch('/api/workflows?limit=100')
        ]);
        
        const statsData = await statsRes.json();
        const wfData = await wfRes.json();
        
        setStats(statsData);
        setWorkflows(wfData.data || []);
      } catch (e) {
        console.error('Failed to fetch compliance stats', e);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
    const interval = setInterval(fetchData, 30000);
    return () => clearInterval(interval);
  }, []);

  const totalDiscoveries = Object.values(stats).reduce((acc, curr) => {
    return acc + Object.values(curr.discoveries).reduce((a, b) => a + b, 0);
  }, 0);

  const discoveryByType = Object.values(stats).reduce((acc: Record<string, number>, curr) => {
    Object.entries(curr.discoveries).forEach(([type, count]) => {
      acc[type] = (acc[type] || 0) + count;
    });
    return acc;
  }, {});

  const protectedWorkflows = workflows.filter(wf => {
    return wf.nodes?.some((n: any) => n.type === 'transformation' && n.config?.transType === 'mask');
  }).length;

  const protectionRate = workflows.length > 0 ? (protectedWorkflows / workflows.length) * 100 : 0;

  return (
    <Box p="md" style={{ position: 'relative', minHeight: '400px' }}>
      <LoadingOverlay visible={loading} />
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="blue.0">
          <Group gap="sm">
            <IconShieldLock size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Compliance Dashboard</Title>
              <Text size="sm" c="dimmed">
                Monitor PII/PHI exposure and data protection coverage across all workflows.
              </Text>
            </Box>
          </Group>
        </Paper>

        <SimpleGrid cols={{ base: 1, md: 3 }} spacing="lg">
          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Data Protection Coverage</Text>
              <ThemeIcon color="teal" variant="light" size="lg" radius="md"><IconLock size="1.2rem" /></ThemeIcon>
            </Group>
            <Group mt="md">
              <RingProgress
                size={80}
                roundCaps
                thickness={8}
                sections={[{ value: protectionRate, color: 'teal' }]}
                label={
                  <Text size="xs" ta="center" fw={700}>
                    {Math.round(protectionRate)}%
                  </Text>
                }
              />
              <Box>
                <Text fw={800} size="xl">{protectedWorkflows} / {workflows.length}</Text>
                <Text size="xs" c="dimmed">Workflows with Masking</Text>
              </Box>
            </Group>
          </Paper>

          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Total PII Discoveries</Text>
              <ThemeIcon color="orange" variant="light" size="lg" radius="md"><IconFingerprint size="1.2rem" /></ThemeIcon>
            </Group>
            <Text fw={800} size="32px" mt="md">{totalDiscoveries.toLocaleString()}</Text>
            <Text size="xs" c="dimmed">Across all active pipelines</Text>
          </Paper>

          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Compliance Status</Text>
              <ThemeIcon color={totalDiscoveries > 0 ? 'blue' : 'green'} variant="light" size="lg" radius="md">
                {totalDiscoveries > 0 ? <IconShieldLock size="1.2rem" /> : <IconCheck size="1.2rem" />}
              </ThemeIcon>
            </Group>
            <Box mt="md">
              <Badge size="lg" color={totalDiscoveries > 0 ? 'blue' : 'green'} variant="light" leftSection={<IconShieldLock size="0.8rem" />}>
                {totalDiscoveries > 0 ? 'Discovery Active' : 'Compliant'}
              </Badge>
              <Text size="xs" c="dimmed" mt="xs">Monitoring {Object.keys(discoveryByType).length} data types</Text>
            </Box>
          </Paper>
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg">
          <Paper p="md" withBorder radius="md">
            <Title order={4} mb="md">Discoveries by Type</Title>
            <ScrollArea h={300}>
              <Table verticalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Data Type</Table.Th>
                    <Table.Th ta="right">Count</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {Object.entries(discoveryByType).sort((a, b) => b[1] - a[1]).map(([type, count]) => (
                    <Table.Tr key={type}>
                      <Table.Td>
                        <Group gap="xs">
                          <ThemeIcon size="xs" color="blue" variant="light"><IconEyeOff size="0.6rem" /></ThemeIcon>
                          <Text size="sm" fw={500}>{type}</Text>
                        </Group>
                      </Table.Td>
                      <Table.Td ta="right"><Text size="sm" fw={700}>{count.toLocaleString()}</Text></Table.Td>
                    </Table.Tr>
                  ))}
                  {Object.keys(discoveryByType).length === 0 && (
                    <Table.Tr>
                      <Table.Td colSpan={2} ta="center" py="xl">
                        <Text c="dimmed" size="sm">No PII discovered yet.</Text>
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Table.Tbody>
              </Table>
            </ScrollArea>
          </Paper>

          <Paper p="md" withBorder radius="md">
            <Title order={4} mb="md">Top Exposed Workflows</Title>
            <ScrollArea h={300}>
              <Table verticalSpacing="sm">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Workflow</Table.Th>
                    <Table.Th ta="right">Discoveries</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {Object.entries(stats).sort((a, b) => {
                    const countA = Object.values(a[1].discoveries).reduce((x, y) => x + y, 0);
                    const countB = Object.values(b[1].discoveries).reduce((x, y) => x + y, 0);
                    return countB - countA;
                  }).map(([id, stat]) => {
                    const wf = workflows.find(w => w.id === id);
                    const count = Object.values(stat.discoveries).reduce((a, b) => a + b, 0);
                    return (
                      <Table.Tr key={id}>
                        <Table.Td>
                          <Stack gap={0}>
                            <Text size="sm" fw={500}>{wf?.name || id}</Text>
                            <Text size="xs" c="dimmed">Last update: {new Date(stat.last_updated).toLocaleTimeString()}</Text>
                          </Stack>
                        </Table.Td>
                        <Table.Td ta="right">
                          <Badge color="orange" variant="light">{count.toLocaleString()}</Badge>
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                  {Object.keys(stats).length === 0 && (
                    <Table.Tr>
                      <Table.Td colSpan={2} ta="center" py="xl">
                        <Text c="dimmed" size="sm">No active PII monitoring data.</Text>
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Table.Tbody>
              </Table>
            </ScrollArea>
          </Paper>
        </SimpleGrid>
      </Stack>
    </Box>
  );
}
