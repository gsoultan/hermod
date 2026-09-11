import {
  IconActivity,
  IconAlertCircle,
  IconArrowsExchange,
  IconBolt,
  IconClockHour4,
  IconGitBranch,
  IconPackage,
  IconPlugConnected,
  IconServer,
  IconSitemap,
  IconStack2,
  IconWaveSine,
} from '@tabler/icons-react';
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Grid,
  Group,
  Paper,
  ScrollArea,
  Stack,
  Table,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
} from '@mantine/core';
import { useEffect, useState } from 'react';
import { apiFetch } from '@/api';
import { useNavigate } from '@tanstack/react-router';
import { formatDateTime } from '@/utils/dateUtils';
import { DataLineageModal } from '@/components/modals/DataLineageModal';
import { notifications } from '@mantine/notifications';
import { useVHost } from '@/context/VHostContext';
import type { Workflow } from '@/types';
import { EmptyState } from '@/components/common/EmptyState';
import { useDashboardStream, type StreamState } from '@/hooks/useDashboardStream';
import { formatCount, formatLatency, formatPercent, formatUptime } from '@/utils/metricFormat';

function ThroughputChart({ data }: { data: number[] }) {
  // One point cannot be drawn as a line, and dividing by (length - 1) to place
  // it put every x at Infinity. Both are handled here rather than by hoping the
  // series is never short — it always is, for the first few seconds after load.
  if (!Array.isArray(data) || data.length < 2) {
    return (
      <Box h={180} display="flex" style={{ alignItems: 'center', justifyContent: 'center' }}>
        <Text size="xs" c="dimmed">
          Collecting throughput samples…
        </Text>
      </Box>
    );
  }

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
  const latest = points[points.length - 1];

  return (
    <Box pos="relative" h={height} w="100%">
      <svg
        width="100%"
        height="100%"
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        style={{ overflow: 'visible' }}
        role="img"
        aria-label={`Throughput over the last ${data.length} samples, peaking at ${max.toFixed(1)} messages per second`}
      >
        <defs>
          <linearGradient id="chartGradient" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stopColor="var(--mantine-color-indigo-6)" stopOpacity="0.3" />
            <stop offset="100%" stopColor="var(--mantine-color-indigo-6)" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={areaD} fill="url(#chartGradient)" />
        <path
          d={pathD}
          fill="none"
          stroke="var(--mantine-color-indigo-6)"
          strokeWidth="3"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <circle cx={latest.x} cy={latest.y} r="4" fill="white" stroke="var(--mantine-color-indigo-6)" strokeWidth="2" />
        <circle cx={latest.x} cy={latest.y} r="8" fill="var(--mantine-color-indigo-6)" fillOpacity="0.2" />
      </svg>
    </Box>
  );
}

interface StatCardProps {
  title: string;
  value: string | number;
  icon: React.ElementType;
  color: string;
  description?: string;
}

/**
 * A single metric.
 *
 * There is deliberately no trend badge. The previous version rendered a
 * hardcoded "+5%" whenever throughput was above zero — a number with no source
 * behind it, on the one screen whose entire job is to be believed.
 */
function StatCard({ title, value, icon: Icon, color, description }: StatCardProps) {
  return (
    <Paper
      withBorder
      p="md"
      radius="md"
      h="100%"
      style={{ display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}
    >
      <Group justify="space-between" wrap="nowrap">
        <div>
          <Text size="xs" c="dimmed" fw={700} style={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>
            {title}
          </Text>
          <Text size="xl" fw={800} mt={4}>
            {value}
          </Text>
        </div>
        <ThemeIcon color={color} variant="light" size="xl" radius="md">
          <Icon size="1.4rem" />
        </ThemeIcon>
      </Group>
      {description && (
        <Text size="xs" c="dimmed" mt="md">
          {description}
        </Text>
      )}
    </Paper>
  );
}

/**
 * Says plainly whether the numbers on screen are current.
 *
 * Without this a dropped socket is indistinguishable from a quiet system:
 * both show the same unchanging figures, and only one of them is true.
 */
function ConnectionBadge({ state }: { state: StreamState }) {
  const presentation: Record<StreamState, { color: string; label: string; tooltip: string }> = {
    connecting: { color: 'gray', label: 'Connecting', tooltip: 'Opening the live stats stream' },
    live: { color: 'green', label: 'Live', tooltip: 'Receiving live updates' },
    offline: {
      color: 'red',
      label: 'Reconnecting',
      tooltip: 'The live stream dropped. These figures are frozen at their last reading and will resume automatically.',
    },
  };
  const { color, label, tooltip } = presentation[state];

  return (
    <Tooltip label={tooltip}>
      <Badge color={color} variant="light" size="lg" radius="sm" role="status">
        {label}
      </Badge>
    </Tooltip>
  );
}

export function DashboardPage() {
  const navigate = useNavigate();
  const { selectedVHost } = useVHost();
  const { stats, series, connection, error } = useDashboardStream(selectedVHost);

  const [recentLogs, setRecentLogs] = useState<any[]>([]);
  const [logsError, setLogsError] = useState<string | null>(null);
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [workflowsError, setWorkflowsError] = useState<string | null>(null);
  const [lineageOpened, setLineageOpened] = useState(false);
  const [isBootstrapping, setIsBootstrapping] = useState(false);

  const mps = series.length > 0 ? series[series.length - 1] : stats.throughput;

  useEffect(() => {
    let cancelled = false;

    apiFetch(`/api/logs?limit=10&vhost=${encodeURIComponent(selectedVHost)}`)
      .then((res) => {
        if (!res.ok) throw new Error(`logs request failed (${res.status})`);
        return res.json();
      })
      .then((data) => {
        if (cancelled) return;
        setRecentLogs(data.data || []);
        setLogsError(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // Surfaced rather than logged to the console: an empty events panel and
        // a broken events panel look identical, and only one needs attention.
        setLogsError(err instanceof Error ? err.message : 'Could not load recent events');
      });

    apiFetch(`/api/workflows?limit=100&vhost=${encodeURIComponent(selectedVHost)}`)
      .then((res) => {
        if (!res.ok) throw new Error(`workflows request failed (${res.status})`);
        return res.json();
      })
      .then((data) => {
        if (cancelled) return;
        setWorkflows(data.data || []);
        setWorkflowsError(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setWorkflowsError(err instanceof Error ? err.message : 'Could not load pipelines');
      });

    return () => {
      cancelled = true;
    };
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

  const pipelineCards = (
    <Grid grow gap="sm">
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Active Pipelines"
          value={`${stats.active_workflows} / ${stats.total_workflows}`}
          icon={IconGitBranch}
          color="blue"
          description={
            stats.failed_workflows > 0
              ? `${stats.failed_workflows} failed`
              : 'Currently running workflows'
          }
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Throughput"
          value={`${mps >= 10 ? Math.round(mps) : Number(mps.toFixed(1))} msg/s`}
          icon={IconArrowsExchange}
          color="green"
          description={`${formatCount(stats.total_processed)} processed in total`}
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Avg Latency"
          value={formatLatency(stats.avg_latency_ms)}
          icon={IconClockHour4}
          color="violet"
          description="End-to-end, across running engines"
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Consumer Lag"
          value={formatCount(stats.total_lag)}
          icon={IconStack2}
          color={stats.total_lag > 0 ? 'orange' : 'teal'}
          description="Messages waiting at the source"
        />
      </Grid.Col>
    </Grid>
  );

  const healthCards = (
    <Grid grow gap="sm">
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Error Rate"
          value={formatPercent(stats.error_rate)}
          icon={stats.total_errors > 0 ? IconAlertCircle : IconActivity}
          color={stats.total_errors > 0 ? 'red' : 'teal'}
          description={
            stats.total_errors > 0
              ? `${formatCount(stats.total_errors)} dead-lettered`
              : 'No dead-lettered messages'
          }
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Backpressure"
          value={formatPercent(stats.backpressure)}
          icon={IconWaveSine}
          color={stats.backpressure >= 0.8 ? 'red' : stats.backpressure >= 0.5 ? 'orange' : 'teal'}
          description="Fullest sink buffer"
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Circuit Breakers"
          value={stats.circuit_breakers_open}
          icon={IconBolt}
          color={stats.circuit_breakers_open > 0 ? 'red' : 'teal'}
          description={stats.circuit_breakers_open > 0 ? 'Sinks tripped open' : 'All sinks accepting writes'}
        />
      </Grid.Col>
      <Grid.Col span={{ base: 12, sm: 6, md: 3 }}>
        <StatCard
          title="Node Cluster"
          value={`${stats.active_workers} active`}
          icon={IconServer}
          color="indigo"
          description={`Control plane up ${formatUptime(stats.uptime)}`}
        />
      </Grid.Col>
    </Grid>
  );

  return (
    <Stack gap="lg" h="100%">
      <Group justify="space-between">
        <Stack gap={4}>
          <Title order={2}>Enterprise Command Center</Title>
          <Group gap="xs">
            <Text size="sm" c="dimmed">
              Real-time autonomous operations &amp; data mesh monitoring
            </Text>
            <ConnectionBadge state={connection} />
          </Group>
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
            <ActionIcon aria-label="View lineage" variant="light" size="lg" onClick={() => setLineageOpened(true)}>
              <IconSitemap size="1.2rem" />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {/* A failed stats request used to render as a dashboard of zeros, which
          reads as "healthy and idle" — the most dangerous wrong answer here. */}
      {error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title="Metrics unavailable">
          {error}. The figures below may be out of date.
        </Alert>
      )}

      <Box style={{ flex: 1, minHeight: 800 }}>
        <Grid gap="md">
          <Grid.Col span={12}>{pipelineCards}</Grid.Col>
          <Grid.Col span={12}>{healthCards}</Grid.Col>

          <Grid.Col span={{ base: 12, lg: 8 }}>
            <Paper withBorder p="md" radius="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
              <Group justify="space-between" mb="xs">
                <div>
                  <Text fw={700} size="sm">
                    Real-time Throughput
                  </Text>
                  <Text size="xs" c="dimmed">
                    Messages per second, sampled every 5s
                  </Text>
                </div>
                <Badge variant="filled" color="indigo" size="lg" radius="sm">
                  {mps >= 10 ? Math.round(mps) : Number(mps.toFixed(1))} MPS
                </Badge>
              </Group>
              <Box mt="auto" pt="xl">
                <ThroughputChart data={series} />
              </Box>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, lg: 4 }}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Group justify="space-between" mb="md">
                <div>
                  <Text fw={700} size="sm">
                    Active Pipelines
                  </Text>
                  <Text size="xs" c="dimmed">
                    Critical data flows
                  </Text>
                </div>
                <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/workflows' })}>
                  View All
                </Button>
              </Group>
              {workflowsError ? (
                <Alert color="red" icon={<IconAlertCircle size={16} />}>
                  {workflowsError}
                </Alert>
              ) : workflows.length === 0 ? (
                /* With no workflows the table rendered as a lone "Name" header
                   above 300px of nothing, which reads as a broken panel rather
                   than an empty one. Say which it is, and offer the way out. */
                <EmptyState
                  compact
                  icon={<IconGitBranch size={20} />}
                  title="No pipelines yet"
                  description="Build a workflow to start moving data between a source and a sink."
                  action={{ label: 'Create workflow', onClick: () => navigate({ to: '/workflows/new' }) }}
                />
              ) : (
                <ScrollArea h={300} offsetScrollbars>
                  <Table.ScrollContainer minWidth={700}>
                    <Table verticalSpacing="xs" highlightOnHover>
                      <Table.Thead>
                        <Table.Tr>
                          <Table.Th>Name</Table.Th>
                          <Table.Th ta="right">Status</Table.Th>
                        </Table.Tr>
                      </Table.Thead>
                      <Table.Tbody>
                        {workflows.slice(0, 10).map((wf) => (
                          <Table.Tr
                            key={wf.id}
                            style={{ cursor: 'pointer' }}
                            onClick={() => navigate({ to: `/workflows/$id`, params: { id: wf.id } as any })}
                          >
                            <Table.Td>
                              <Group gap="xs">
                                <ThemeIcon size="xs" variant="light" color={wf.active ? 'green' : 'gray'}>
                                  <IconGitBranch size={10} />
                                </ThemeIcon>
                                <Text size="sm" fw={500}>
                                  {wf.name}
                                </Text>
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
                  </Table.ScrollContainer>
                </ScrollArea>
              )}
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, lg: 4 }}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Group justify="space-between" mb="md">
                <div>
                  <Text fw={700} size="sm">
                    Connectors
                  </Text>
                  <Text size="xs" c="dimmed">
                    Running against configured
                  </Text>
                </div>
                <ThemeIcon variant="light" color="cyan" size="lg" radius="md">
                  <IconPlugConnected size="1.2rem" />
                </ThemeIcon>
              </Group>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="sm">Sources</Text>
                  <Group gap="xs">
                    <Badge variant="light" color={stats.active_sources > 0 ? 'green' : 'gray'}>
                      {stats.active_sources} running
                    </Badge>
                    <Text size="sm" c="dimmed">
                      of {stats.total_sources}
                    </Text>
                  </Group>
                </Group>
                <Group justify="space-between">
                  <Text size="sm">Sinks</Text>
                  <Group gap="xs">
                    <Badge variant="light" color={stats.active_sinks > 0 ? 'green' : 'gray'}>
                      {stats.active_sinks} running
                    </Badge>
                    <Text size="sm" c="dimmed">
                      of {stats.total_sinks}
                    </Text>
                  </Group>
                </Group>
              </Stack>
            </Paper>
          </Grid.Col>

          <Grid.Col span={{ base: 12, lg: 8 }}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Group justify="space-between" mb="md">
                <div>
                  <Text fw={700} size="sm">
                    System Events
                  </Text>
                  <Text size="xs" c="dimmed">
                    Recent activity logs
                  </Text>
                </div>
                <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/logs' })}>
                  System Logs
                </Button>
              </Group>
              {logsError ? (
                <Alert color="red" icon={<IconAlertCircle size={16} />}>
                  {logsError}
                </Alert>
              ) : recentLogs.length === 0 ? (
                <EmptyState compact icon={<IconActivity size={20} />} title="No recent events" />
              ) : (
                <ScrollArea h={300} offsetScrollbars>
                  <Stack gap={8}>
                    {recentLogs.map((log) => (
                      <Paper
                        key={log.id}
                        withBorder
                        p="xs"
                        radius="sm"
                        bg={log.level === 'ERROR' ? 'var(--mantine-color-red-light)' : undefined}
                      >
                        <Group wrap="nowrap" gap="xs">
                          <ThemeIcon
                            size="sm"
                            variant="light"
                            color={log.level === 'ERROR' ? 'red' : log.level === 'WARN' ? 'yellow' : 'blue'}
                          >
                            {log.level === 'ERROR' ? <IconAlertCircle size={14} /> : <IconActivity size={14} />}
                          </ThemeIcon>
                          <Box style={{ flex: 1 }}>
                            <Text size="xs" fw={500} lineClamp={2}>
                              {log.message}
                            </Text>
                            <Group justify="space-between" mt={4}>
                              <Badge size="xs" variant="outline" color={log.level === 'ERROR' ? 'red' : 'gray'}>
                                {log.level}
                              </Badge>
                              <Text size="xs" c="dimmed">
                                {formatDateTime(log.timestamp)}
                              </Text>
                            </Group>
                          </Box>
                        </Group>
                      </Paper>
                    ))}
                  </Stack>
                </ScrollArea>
              )}
            </Paper>
          </Grid.Col>
        </Grid>
      </Box>

      <DataLineageModal opened={lineageOpened} onClose={() => setLineageOpened(false)} />
    </Stack>
  );
}
