import {
  IconActivity,
  IconAlertCircle,
  IconArrowsExchange,
  IconBolt,
  IconClockHour4,
  IconGitBranch,
  IconPlugConnected,
  IconServer,
  IconSitemap,
  IconStack2,
  IconWaveSine,
} from '@tabler/icons-react';
import {
  Alert,
  Badge,
  Box,
  Button,
  Grid,
  Group,
  Paper,
  ScrollArea,
  Stack,
  Text,
  ThemeIcon,
  Tooltip,
} from '@mantine/core';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@/api';
import { StatCard } from '@/components/system/StatCard';
import { ClusterResourceCards } from '@/components/system/ClusterResourceCards';
import { useNavigate } from '@tanstack/react-router';
import { formatDateTime } from '@/utils/dateUtils';
import { DataLineageModal } from '@/components/modals/DataLineageModal';
import { useVHost } from '@/context/VHostContext';
import { EmptyState } from '@/components/common/EmptyState';
import type { DashboardStats, StreamState } from '@/hooks/useDashboardStream';
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

/**
 * Says plainly whether the numbers on screen are current.
 *
 * Without this a dropped socket is indistinguishable from a quiet system:
 * both show the same unchanging figures, and only one of them is true.
 */
export function ConnectionBadge({ state }: { state: StreamState }) {
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

interface PlatformPanelProps {
  stats: DashboardStats;
  series: number[];
  error: string | null;
}

/**
 * The platform under the flows: throughput, latency, lag, error rate,
 * backpressure, circuit breakers, the cluster and its machines, connector
 * counts, recent events and the lineage view. Everything the old dashboard
 * showed, below what needs you rather than instead of it.
 */
export function PlatformPanel({ stats, series, error }: PlatformPanelProps) {
  const navigate = useNavigate();
  const { selectedVHost } = useVHost();
  const [lineageOpened, setLineageOpened] = useState(false);

  const mps = series.length > 0 ? series[series.length - 1] : stats.throughput;

  const { data: logsData, error: logsErr } = useQuery({
    queryKey: ['logs', 'recent', selectedVHost],
    queryFn: async () => {
      const res = await apiFetch(`/api/logs?limit=10&vhost=${encodeURIComponent(selectedVHost)}`, { silent: true });
      return res.json();
    },
    refetchInterval: 30_000,
  });
  const recentLogs: any[] = logsData?.data || [];
  // Surfaced rather than logged to the console: an empty events panel and a
  // broken events panel look identical, and only one needs attention.
  const logsError = logsErr ? (logsErr instanceof Error ? logsErr.message : 'Could not load recent events') : null;
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
    <Stack gap="md">
      <Group justify="space-between" align="center">
        <Text fw={700}>Platform</Text>
        <Tooltip label="Data lineage across every flow">
          <Button variant="subtle" size="xs" leftSection={<IconSitemap size="1rem" />} onClick={() => setLineageOpened(true)}>
            Lineage
          </Button>
        </Tooltip>
      </Group>

      {/* A failed stats request used to render as a dashboard of zeros, which
          reads as "healthy and idle" — the most dangerous wrong answer here. */}
      {error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title="Metrics unavailable">
          {error}. The figures below may be out of date.
        </Alert>
      )}

      <Box>
        <Grid gap="md">
          <Grid.Col span={12}>{pipelineCards}</Grid.Col>
          <Grid.Col span={12}>{healthCards}</Grid.Col>
          {/* What the platform is running on. Third row, not first: the
              pipeline is what the reader came for, and the machines under it
              are the next question rather than the opening one. */}
          <Grid.Col span={12}>
            <ClusterResourceCards stats={stats} />
          </Grid.Col>

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

          <Grid.Col span={12}>
            <Paper withBorder p="md" radius="md" h="100%">
              <Group justify="space-between" mb="md">
                <div>
                  <Text fw={700} size="sm">
                    Recent events
                  </Text>
                  <Text size="xs" c="dimmed">
                    Recent activity logs
                  </Text>
                </div>
                <Button variant="subtle" size="xs" onClick={() => navigate({ to: '/activity', search: { tab: 'logs' } as any })}>
                  All logs
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
