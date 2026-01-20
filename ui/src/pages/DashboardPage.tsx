import { Title, Text, SimpleGrid, Paper, Group, ThemeIcon, Box, Stack, Grid, Badge, Table, ScrollArea, Divider, ActionIcon, Tooltip, Button } from '@mantine/core'
import { IconDatabase, IconArrowsExchange, IconBroadcast, IconLayoutDashboard, IconPlug, IconList, IconActivity, IconExternalLink, IconAlertTriangle, IconGitBranch } from '@tabler/icons-react'
import { useState, useEffect, useRef } from 'react'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'

function Sparkline({ data, height = 30, color = 'blue' }: { data: number[], height?: number, color?: string }) {
  if (!data || data.length === 0) return null;
  const max = Math.max(...data, 1);
  const width = data.length * 10;
  const points = data.map((v, i) => `${i === 0 ? '' : 'L'} ${i * 10} ${height - (v / (max || 1)) * height}`).join(' ');

  return (
    <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ overflow: 'visible' }}>
      <path
        d={`M ${points}`}
        fill="none"
        stroke={`var(--mantine-color-${color}-filled)`}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function DashboardPage() {
  const navigate = useNavigate();
  const [stats, setStats] = useState({
    active_sources: 0,
    active_sinks: 0,
    active_workflows: 0,
    total_processed: 0
  })

  const [recentLogs, setRecentLogs] = useState<any[]>([])
  const [workflows, setWorkflows] = useState<any[]>([])
  const [mps, setMps] = useState(0)
  const [mpsHistory, setMpsHistory] = useState<number[]>(new Array(30).fill(0))

  const lastStatsRef = useRef({ time: Date.now(), count: 0 });

  useEffect(() => {
    // Initial fetch
    apiFetch('/api/dashboard/stats')
      .then(res => res.json())
      .then(data => {
        setStats(data);
        lastStatsRef.current = { time: Date.now(), count: data.total_processed };
      })
      .catch(err => console.error('Failed to fetch initial stats', err));

    apiFetch('/api/logs?limit=10')
      .then(res => res.json())
      .then(data => setRecentLogs(data.data || []))
      .catch(err => console.error('Failed to fetch recent logs', err));

    apiFetch('/api/workflows?limit=100')
      .then(res => res.json())
      .then(data => setWorkflows(data.data || []))
      .catch(err => console.error('Failed to fetch workflows', err));

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/dashboard`;
    const ws = new WebSocket(wsUrl);
    
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        setStats(data);

        // Calculate MPS
        const now = Date.now();
        const timeDiff = (now - lastStatsRef.current.time) / 1000;
        const countDiff = data.total_processed - lastStatsRef.current.count;
        
        if (timeDiff >= 1) {
            const currentMps = Math.max(0, Math.round(countDiff / timeDiff));
            setMps(currentMps);
            setMpsHistory(prev => [...prev.slice(1), currentMps]);
            lastStatsRef.current = { time: now, count: data.total_processed };
        }
      } catch (err) {
        console.error('Failed to parse dashboard stats', err);
      }
    };

    return () => ws.close();
  }, []);

  const formatNumber = (num: number) => {
    if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
    if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
    return num.toString();
  };

  const getLevelColor = (level: string) => {
    switch (level?.toUpperCase()) {
      case 'ERROR': return 'red';
      case 'WARN': return 'yellow';
      case 'INFO': return 'blue';
      default: return 'gray';
    }
  };

  const statusCount = workflows.reduce((acc: any, curr: any) => {
    const status = curr.status || 'stopped';
    acc[status] = (acc[status] || 0) + 1;
    return acc;
  }, {});

  return (
    <Box p="md" style={{ animation: 'fadeIn 0.5s ease-in-out' }}>
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="gray.0">
          <Group gap="sm">
            <IconLayoutDashboard size="2rem" color="var(--mantine-color-blue-filled)" />
            <Box style={{ flex: 1 }}>
              <Title order={2} fw={800}>Dashboard</Title>
              <Text size="sm" c="dimmed">
                Overview of your Hermod instance. Monitor active data flows and system health.
              </Text>
            </Box>
          </Group>
        </Paper>
      
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="xl">
          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Active Sources</Text>
              <ThemeIcon color="indigo" variant="light" size="lg" radius="md"><IconDatabase size="1.2rem" /></ThemeIcon>
            </Group>
            <Text fw={800} size="32px" mt="md">{stats.active_sources}</Text>
          </Paper>

          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Active Workflows</Text>
              <ThemeIcon color="teal" variant="light" size="lg" radius="md"><IconPlug size="1.2rem" /></ThemeIcon>
            </Group>
            <Text fw={800} size="32px" mt="md">{stats.active_workflows}</Text>
          </Paper>

          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Active Sinks</Text>
              <ThemeIcon color="orange" variant="light" size="lg" radius="md"><IconBroadcast size="1.2rem" /></ThemeIcon>
            </Group>
            <Text fw={800} size="32px" mt="md">{stats.active_sinks}</Text>
          </Paper>

          <Paper p="xl" radius="md" withBorder>
            <Group justify="space-between">
              <Text size="xs" c="dimmed" fw={700} tt="uppercase">Processed</Text>
              <ThemeIcon color="cyan" variant="light" size="lg" radius="md"><IconArrowsExchange size="1.2rem" /></ThemeIcon>
            </Group>
            <Group align="flex-end" gap="xs">
              <Text fw={800} size="32px" mt="md">{formatNumber(stats.total_processed)}</Text>
              <Badge variant="light" color="cyan" mb="xs">{mps} msg/s</Badge>
            </Group>
            <Box mt="sm" h={30}>
              <Sparkline data={mpsHistory} color="cyan" />
            </Box>
          </Paper>
        </SimpleGrid>

        <Grid gutter="md">
          <Grid.Col span={{ base: 12, md: 8 }}>
            <Paper p="md" withBorder radius="md">
              <Group justify="space-between" mb="md">
                <Group gap="xs">
                  <IconList size="1.2rem" />
                  <Title order={4}>Recent Activity</Title>
                </Group>
                <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/logs' })}>View All</Button>
              </Group>
              <ScrollArea h={350}>
                {recentLogs.length > 0 ? (
                  <Table verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>Time</Table.Th>
                        <Table.Th>Level</Table.Th>
                        <Table.Th>Workflow</Table.Th>
                        <Table.Th>Message</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {recentLogs.map((log) => (
                        <Table.Tr key={log.id}>
                          <Table.Td><Text size="xs">{new Date(log.timestamp).toLocaleTimeString()}</Text></Table.Td>
                          <Table.Td><Badge color={getLevelColor(log.level)} size="xs" variant="light">{log.level}</Badge></Table.Td>
                          <Table.Td>
                            <Group gap={4}>
                              <Text size="xs" fw={500}>{log.workflow_id?.split('-')[0]}</Text>
                              <Tooltip label="View Workflow">
                                <ActionIcon variant="subtle" size="xs" onClick={() => navigate({ to: `/workflows/${log.workflow_id}` })}>
                                  <IconExternalLink size="0.8rem" />
                                </ActionIcon>
                              </Tooltip>
                            </Group>
                          </Table.Td>
                          <Table.Td><Text size="xs" truncate maw={300}>{log.message}</Text></Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                ) : (
                  <Box py="xl" style={{ textAlign: 'center' }}>
                    <Text c="dimmed" size="sm">No recent logs found.</Text>
                  </Box>
                )}
              </ScrollArea>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, md: 4 }}>
            <Stack gap="md">
              <Paper p="md" withBorder radius="md">
                <Group gap="xs" mb="md">
                  <IconActivity size="1.2rem" />
                  <Title order={4}>Status Summary</Title>
                </Group>
                <Stack gap="xs">
                  <Group justify="space-between">
                    <Text size="sm">Running</Text>
                    <Badge color="green" variant="filled">{statusCount.running || 0}</Badge>
                  </Group>
                  <Group justify="space-between">
                    <Text size="sm">Stopped</Text>
                    <Badge color="gray" variant="filled">{(statusCount.stopped || 0) + (statusCount.shutdown || 0)}</Badge>
                  </Group>
                  <Group justify="space-between">
                    <Text size="sm">Reconnecting</Text>
                    <Badge color="orange" variant="filled">{statusCount.reconnecting || 0}</Badge>
                  </Group>
                  <Group justify="space-between">
                    <Text size="sm">Error</Text>
                    <Badge color="red" variant="filled">{statusCount.error || 0}</Badge>
                  </Group>
                </Stack>
                <Divider my="md" />
                <Button fullWidth variant="light" leftSection={<IconGitBranch size="1rem" />} onClick={() => navigate({ to: '/workflows' })}>
                  Manage Workflows
                </Button>
              </Paper>

              <Paper p="md" withBorder radius="md" bg="blue.0">
                <Group gap="xs" mb="xs">
                  <IconAlertTriangle size="1.2rem" color="blue" />
                  <Text fw={700} size="sm" c="blue.9">Pro Tip</Text>
                </Group>
                <Text size="xs" c="blue.8">
                  Use nodes and edges in workflows to create complex data processing pipelines.
                </Text>
              </Paper>
            </Stack>
          </Grid.Col>
        </Grid>
      </Stack>
    </Box>
  )
}
