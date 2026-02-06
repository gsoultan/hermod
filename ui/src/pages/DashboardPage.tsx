import { IconActivity, IconAlertCircle, IconArrowsExchange, IconGitBranch, IconPackage, IconServer, IconSitemap } from '@tabler/icons-react';
import { Title, Text, Paper, Group, ThemeIcon, Box, Stack, Badge, Table, ScrollArea, ActionIcon, Tooltip, Button, Grid } from '@mantine/core'import { useState, useEffect, useRef } from 'react'
import { apiFetch } from '../api'
import { useNavigate } from '@tanstack/react-router'
import { formatDateTime } from '../utils/dateUtils'
import { DataLineageModal } from '../components/DataLineageModal'
import { notifications } from '@mantine/notifications'
import { useVHost } from '../context/VHostContext'

function ThroughputChart({ data }: { data: number[] }) {
  if (!Array.isArray(data) || data.length === 0) return null;
  const height = 180;
  const max = Math.max(...data, 10);
  const width = 800;
  const step = width / (data.length - 1);
  
  const points = data.map((v, i) => ({ x: i * step, y: height - (v / max) * height }));
  
  let pathD = `M ${points[0].x},${points[0].y}`;
  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[i];
    const p1 = points[i + 1];
    const cpX = (p0.x + p1.x) / 2;
    pathD += ` C ${cpX},${p0.y} ${cpX},${p1.y} ${p1.x},${p1.y}`;
  }

  const areaD = `${pathD} L ${width},${height} L 0,${height} Z`;

  return (
    <Box pos="relative" h={height} w="100%">
        <svg width="100%" height="100%" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ overflow: 'visible' }}>
          <defs>
            <linearGradient id="chartGradient" x1="0%" y1="0%" x2="0%" y2="100%">
              <stop offset="0%" stopColor="var(--mantine-color-indigo-6)" stopOpacity="0.3" />
              <stop offset="100%" stopColor="var(--mantine-color-indigo-6)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d={areaD} fill="url(#chartGradient)" />
          <path d={pathD} fill="none" stroke="var(--mantine-color-indigo-6)" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
          
          <circle cx={points[points.length-1].x} cy={points[points.length-1].y} r="4" fill="white" stroke="var(--mantine-color-indigo-6)" strokeWidth="2" />
          <circle cx={points[points.length-1].x} cy={points[points.length-1].y} r="8" fill="var(--mantine-color-indigo-6)" fillOpacity="0.2" />
        </svg>
    </Box>
  );
}

function StatCard({ title, value, icon: Icon, color, description, trend }: any) {
    return (
        <Paper withBorder p="md" radius="md" h="100%" style={{ display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
            <Group justify="space-between">
                <div>
                    <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>{title}</Text>
                    <Text size="xl" fw={800} mt={4}>{value}</Text>
                </div>
                <ThemeIcon color={color} variant="light" size="xl" radius="md">
                    <Icon size="1.4rem" />
                </ThemeIcon>
            </Group>
            {(description || trend) && (
                <Group justify="space-between" mt="md">
                    <Text size="xs" c="dimmed">{description}</Text>
                    {trend && (
                        <Badge size="xs" variant="light" color={trend > 0 ? 'green' : 'red'}>
                            {trend > 0 ? '+' : ''}{trend}%
                        </Badge>
                    )}
                </Group>
            )}
        </Paper>
    );
}

export function DashboardPage() {
  const navigate = useNavigate();
  const { selectedVHost } = useVHost();
  const [stats, setStats] = useState({
    active_sources: 0,
    active_sinks: 0,
    active_workflows: 0,
    total_processed: 0,
    total_lag: 0,
    total_errors: 0,
    failed_workflows: 0,
    uptime: 0,
    active_workers: 0,
    total_workflows: 0,
    total_sources: 0,
    total_sinks: 0
  })

  const [recentLogs, setRecentLogs] = useState<any[]>([])
  const [workflows, setWorkflows] = useState<any[]>([])
  const [mps, setMps] = useState(0)
  const [mpsHistory, setMpsHistory] = useState<number[]>(new Array(30).fill(0))
  const [lineageOpened, setLineageOpened] = useState(false)
  const [isBootstrapping, setIsBootstrapping] = useState(false);

  const lastStatsRef = useRef({ time: Date.now(), count: 0 });

  useEffect(() => {
    // Initial fetch
    apiFetch(`/api/dashboard/stats?vhost=${selectedVHost}`)
      .then(res => res.json())
      .then(data => {
        setStats(data);
        lastStatsRef.current = { time: Date.now(), count: data.total_processed };
      })
      .catch(err => console.error('Failed to fetch initial stats', err));

    apiFetch(`/api/logs?limit=10&vhost=${selectedVHost}`)
      .then(res => res.json())
      .then(data => setRecentLogs(data.data || []))
      .catch(err => console.error('Failed to fetch recent logs', err));

    apiFetch(`/api/workflows?limit=100&vhost=${selectedVHost}`)
      .then(res => res.json())
      .then(data => setWorkflows(data.data || []))
      .catch(err => console.error('Failed to fetch workflows', err));

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/ws/dashboard?vhost=${selectedVHost}`;
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
  }, [selectedVHost]);

  const handleBootstrap = async () => {
    setIsBootstrapping(true);
    try {
      const res = await apiFetch('/api/infra/bootstrap-scenario', { method: 'POST' });
      if (res.ok) {
        const data = await res.json();
        notifications.show({
          title: 'Enterprise Scenario Bootstrapped',
          message: data.message,
          color: 'green',
        });
        // Refresh workflows
        const wfRes = await apiFetch('/api/workflows?limit=100');
        const wfData = await wfRes.json();
        setWorkflows(wfData.data || []);
      } else {
        throw new Error('Failed to bootstrap scenario');
      }
    } catch (err) {
      notifications.show({
        title: 'Bootstrap Failed',
        message: err instanceof Error ? err.message : 'Unknown error',
        color: 'red',
      });
    } finally {
      setIsBootstrapping(false);
    }
  };

  const formatNumber = (num: number) => {
    if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
    if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
    return num.toString();
  };

  const renderWidget = (id: string) => {
    switch (id) {
      case 'stats':
        return (
          <Grid grow gutter="sm">
            <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
              <StatCard 
                title="Active Pipelines" 
                value={`${stats.active_workflows} / ${stats.total_workflows}`}
                icon={IconGitBranch}
                color="blue"
                description="Currently running workflows"
              />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
              <StatCard 
                title="Throughput" 
                value={formatNumber(stats.total_processed)}
                icon={IconArrowsExchange}
                color="green"
                description="Total messages processed"
                trend={mps > 0 ? 5 : 0} // Fake trend for visual
              />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
              <StatCard 
                title="System Health" 
                value={stats.total_errors > 0 ? `${stats.total_errors} Errors` : "Healthy"}
                icon={stats.total_errors > 0 ? IconAlertCircle : IconActivity}
                color={stats.total_errors > 0 ? "red" : "teal"}
                description={stats.total_errors > 0 ? "Check logs for details" : "All systems nominal"}
              />
            </Grid.Col>
            <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
              <StatCard 
                title="Node Cluster" 
                value={`${stats.active_workers} Active`}
                icon={IconServer}
                color="indigo"
                description="Operational worker nodes"
              />
            </Grid.Col>
          </Grid>
        );
      case 'mps':
        return (
          <Paper withBorder p="md" radius="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
            <Group justify="space-between" mb="xs">
              <div>
                <Text fw={700} size="sm">Real-time Throughput</Text>
                <Text size="xs" c="dimmed">Messages per second (MPS)</Text>
              </div>
              <Badge variant="filled" color="indigo" size="lg" radius="sm">{mps} MPS</Badge>
            </Group>
            <Box mt="auto" pt="xl">
              <ThroughputChart data={mpsHistory} />
            </Box>
          </Paper>
        );
      case 'workflows':
        return (
          <Paper withBorder p="md" radius="md" h="100%">
            <Group justify="space-between" mb="md">
              <div>
                <Text fw={700} size="sm">Active Pipelines</Text>
                <Text size="xs" c="dimmed">Critical data flows</Text>
              </div>
              <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/workflows' })}>View All</Button>
            </Group>
            <ScrollArea h={300} offsetScrollbars>
                <Table verticalSpacing="xs" highlightOnHover>
                    <Table.Thead>
                        <Table.Tr>
                            <Table.Th>Name</Table.Th>
                            <Table.Th ta="right">Status</Table.Th>
                        </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                        {workflows.slice(0, 10).map(wf => (
                            <Table.Tr key={wf.id} style={{ cursor: 'pointer' }} onClick={() => navigate({ to: `/workflows/$id`, params: { id: wf.id } as any })}>
                                <Table.Td>
                                  <Group gap="xs">
                                    <ThemeIcon size="xs" variant="light" color={wf.active ? 'green' : 'gray'}>
                                      <IconGitBranch size={10} />
                                    </ThemeIcon>
                                    <Text size="sm" fw={500}>{wf.name}</Text>
                                  </Group>
                                </Table.Td>
                                <Table.Td ta="right">
                                    <Badge size="xs" color={wf.active ? 'green' : 'gray'} variant="light">
                                        {wf.active ? 'Active' : 'Inactive'}
                                    </Badge>
                                </Table.Td>
                            </Table.Tr>
                        ))}
                    </Table.Tbody>
                </Table>
            </ScrollArea>
          </Paper>
        );
      case 'logs':
        return (
          <Paper withBorder p="md" radius="md" h="100%">
            <Group justify="space-between" mb="md">
                <div>
                    <Text fw={700} size="sm">System Events</Text>
                    <Text size="xs" c="dimmed">Recent activity logs</Text>
                </div>
                <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/logs' })}>System Logs</Button>
            </Group>
            <ScrollArea h={300} offsetScrollbars>
                <Stack gap={8}>
                    {recentLogs.map(log => (
                        <Paper key={log.id} withBorder p="xs" radius="sm" bg={log.level === 'ERROR' ? 'red.0' : undefined}>
                          <Group wrap="nowrap" gap="xs">
                              <ThemeIcon size="sm" variant="light" color={log.level === 'ERROR' ? 'red' : log.level === 'WARN' ? 'yellow' : 'blue'}>
                                  {log.level === 'ERROR' ? <IconAlertCircle size={14} /> : <IconActivity size={14} />}
                              </ThemeIcon>
                              <Box style={{ flex: 1 }}>
                                  <Text size="xs" fw={500} lineClamp={2}>{log.message}</Text>
                                  <Group justify="space-between" mt={4}>
                                    <Badge size="10px" variant="outline" color={log.level === 'ERROR' ? 'red' : 'gray'}>{log.level}</Badge>
                                    <Text size="10px" c="dimmed">{formatDateTime(log.timestamp)}</Text>
                                  </Group>
                              </Box>
                          </Group>
                        </Paper>
                    ))}
                </Stack>
            </ScrollArea>
          </Paper>
        );
      default:
        return null;
    }
  };

  return (
    <Stack gap="lg" h="100%">
      <Group justify="space-between">
        <Stack gap={0}>
            <Title order={2}>Enterprise Command Center</Title>
            <Text size="sm" c="dimmed">Real-time autonomous operations & data mesh monitoring</Text>
        </Stack>
        <Group>
            <Button 
                variant="light" 
                color="indigo" 
                leftSection={<IconPackage size="1.2rem" />}
                loading={isBootstrapping}
                onClick={handleBootstrap}
            >
                Bootstrap Enterprise Scenario
            </Button>
            <Tooltip label="Global Data Lineage">
                <ActionIcon variant="light" size="lg" onClick={() => setLineageOpened(true)}>
                    <IconSitemap size="1.2rem" />
                </ActionIcon>
            </Tooltip>
        </Group>
      </Group>

      <Box style={{ flex: 1, minHeight: 800 }}>
        <Grid gutter="md">
          <Grid.Col span={12}>
            {renderWidget('stats')}
          </Grid.Col>
          <Grid.Col span={{ base: 12, lg: 8 }}>
            {renderWidget('mps')}
          </Grid.Col>
          <Grid.Col span={{ base: 12, lg: 4 }}>
            {renderWidget('workflows')}
          </Grid.Col>
          <Grid.Col span={12}>
            {renderWidget('logs')}
          </Grid.Col>
        </Grid>
      </Box>

      <DataLineageModal opened={lineageOpened} onClose={() => setLineageOpened(false)} />
    </Stack>
  );
}


