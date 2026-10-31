import { Alert, Badge, Box, Group, Paper, Progress, SimpleGrid, Stack, Table, Text, ThemeIcon, Title } from '@mantine/core';
import { IconActivity, IconAlertTriangle, IconArrowsExchange, IconCircleCheck, IconCircleX, IconClock, IconInbox, IconStack2 } from '@tabler/icons-react';
import type { ReactNode } from 'react';

/** The engine's status frame from /api/ws/status. Durations are nanoseconds. */
export interface EngineStatus {
  workflow_id?: string;
  engine_status?: string;
  processed_count?: number;
  dead_letter_count?: number;
  throughput?: number;
  error_rate?: number;
  backpressure?: number;
  avg_latency?: number;
  lag?: number;
  sink_buffer_fill?: Record<string, number>;
  sink_cb_statuses?: Record<string, string>;
  sink_statuses?: Record<string, string>;
  node_metrics?: Record<string, number>;
  node_error_metrics?: Record<string, number>;
  node_latencies?: Record<string, number>;
}

interface TuningMetricsProps {
  /** Whether the flow is switched on. An off flow has no engine and so no readings. */
  active: boolean;
  status: EngineStatus | null;
  nodeNames: Map<string, string>;
  connectionNames: Record<string, string>;
}

const HIGH_ERROR_RATE = 0.05;
const HIGH_BACKPRESSURE = 0.8;
const MAX_STEPS = 10;

const count = new Intl.NumberFormat();

function formatDuration(ns: number | undefined): string {
  if (!ns || ns <= 0) return '—';
  const ms = ns / 1_000_000;
  if (ms < 1) return `${Math.round(ns / 1000)} µs`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

const percent = (v: number, digits = 0) => `${(v * 100).toFixed(digits)}%`;

function Metric({ label, value, unit, icon, color, alert }: { label: string; value: string; unit?: string; icon: ReactNode; color: string; alert?: boolean }) {
  return (
    <Paper withBorder p={{ base: 'sm', sm: 'md' }} radius="md" data-metric={label}>
      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed" fw={700} tt="uppercase">{label}</Text>
        {/* Off on a phone, where two tiles share a row and the number needs the width. */}
        <ThemeIcon color={color} variant="light" size="sm" visibleFrom="sm" aria-hidden>{icon}</ThemeIcon>
      </Group>
      <Text size="xl" fw={700} c={alert ? color : undefined} style={{ whiteSpace: 'nowrap' }}>
        {value}
        {unit && <Text span size="sm" fw={500} c="dimmed"> {unit}</Text>}
      </Text>
    </Paper>
  );
}

function breakerColor(state: string | undefined) {
  if (state === 'open') return 'red';
  if (state === 'half-open') return 'yellow';
  return 'green';
}

/**
 * The live half of the Tuning tab: what the running engine reports, read from
 * its status frame. Every figure here comes from the frame; none is invented.
 */
export function TuningMetrics({ active, status, nodeNames, connectionNames }: TuningMetricsProps) {
  if (!active) {
    return <Text c="dimmed" size="sm">This flow is off. Turn the flow on to see live metrics.</Text>;
  }
  if (!status) {
    return <Text c="dimmed" size="sm">Waiting for the first reading from the engine…</Text>;
  }

  const errorRate = status.error_rate ?? 0;
  const backpressure = status.backpressure ?? 0;
  const throughput = status.throughput ?? 0;

  const sinkIds = Array.from(new Set([
    ...Object.keys(status.sink_buffer_fill ?? {}),
    ...Object.keys(status.sink_cb_statuses ?? {}),
  ]));

  const nodeIds = Array.from(new Set([
    ...Object.keys(status.node_metrics ?? {}),
    ...Object.keys(status.node_latencies ?? {}),
    ...Object.keys(status.node_error_metrics ?? {}),
  ]));
  const steps = nodeIds
    .map((id) => ({
      id,
      name: nodeNames.get(id) ?? id,
      runs: status.node_metrics?.[id] ?? 0,
      errors: status.node_error_metrics?.[id] ?? 0,
      latency: status.node_latencies?.[id] ?? 0,
    }))
    // Slowest first, then the busiest: the step worth tuning is at the top.
    .sort((a, b) => b.latency - a.latency || b.errors - a.errors || b.runs - a.runs)
    .slice(0, MAX_STEPS);

  const healthy = errorRate <= HIGH_ERROR_RATE && backpressure <= HIGH_BACKPRESSURE;

  return (
    <Stack gap="lg">
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="md">
        <Metric label="Throughput" value={throughput.toFixed(1)} unit="msg/s" icon={<IconActivity size={16} />} color="blue" />
        <Metric label="Error rate" value={percent(errorRate, 2)} icon={<IconCircleX size={16} />} color="red" alert={errorRate > HIGH_ERROR_RATE} />
        <Metric label="Backpressure" value={percent(backpressure)} icon={<IconArrowsExchange size={16} />} color="orange" alert={backpressure > HIGH_BACKPRESSURE} />
        <Metric label="Avg latency" value={formatDuration(status.avg_latency)} icon={<IconClock size={16} />} color="grape" />
        <Metric label="Processed" value={count.format(status.processed_count ?? 0)} icon={<IconStack2 size={16} />} color="teal" />
        <Metric label="Dead-lettered" value={count.format(status.dead_letter_count ?? 0)} icon={<IconInbox size={16} />} color="yellow" alert={(status.dead_letter_count ?? 0) > 0} />
      </SimpleGrid>

      <Stack gap="xs">
        {backpressure > HIGH_BACKPRESSURE && (
          <Alert icon={<IconAlertTriangle size={16} />} title="Sink buffers are filling up" color="orange">
            A sink is taking messages slower than they arrive. Hermod raises the batch size of a sink whose buffer is more than 80% full.
          </Alert>
        )}
        {errorRate > HIGH_ERROR_RATE && (
          <Alert icon={<IconAlertTriangle size={16} />} title={`${percent(errorRate, 1)} of messages are failing`} color="red">
            The steps below show where. Suggested fixes appear below once a failure repeats.
          </Alert>
        )}
        {healthy && (
          <Group gap="xs" c="green">
            <IconCircleCheck size={16} aria-hidden />
            <Text size="sm">{throughput > 0 ? 'Running within normal limits.' : 'Running within normal limits. No messages in the last second.'}</Text>
          </Group>
        )}
      </Stack>

      {sinkIds.length > 0 && (
        <Box>
          <Title order={5} mb="xs">Sinks</Title>
          <Table.ScrollContainer minWidth={320}>
            <Table aria-label="Sinks" verticalSpacing="xs" layout="fixed">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Sink</Table.Th>
                  <Table.Th w="35%">Buffer</Table.Th>
                  <Table.Th w={96}>Breaker</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {sinkIds.map((id) => {
                  const fill = status.sink_buffer_fill?.[id] ?? 0;
                  const breaker = status.sink_cb_statuses?.[id];
                  return (
                    <Table.Tr key={id}>
                      <Table.Td><Text size="sm" truncate="end" title={connectionNames[id] ?? id}>{connectionNames[id] ?? id}</Text></Table.Td>
                      <Table.Td>
                        <Group gap="xs" wrap="nowrap">
                          <Progress value={fill * 100} color={fill > HIGH_BACKPRESSURE ? 'orange' : 'blue'} style={{ flex: 1 }} aria-hidden />
                          <Text size="xs" w={36} ta="right">{percent(fill)}</Text>
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Badge variant="light" size="sm" color={breakerColor(breaker)}>{breaker || 'closed'}</Badge>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Box>
      )}

      {steps.length > 0 && (
        <Box>
          <Title order={5} mb="xs">Steps</Title>
          <Table.ScrollContainer minWidth={320}>
            <Table aria-label="Steps" verticalSpacing="xs" layout="fixed">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Step</Table.Th>
                  <Table.Th ta="right" w={80}>Avg</Table.Th>
                  <Table.Th ta="right" w={72}>Runs</Table.Th>
                  <Table.Th ta="right" w={84}>Failures</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {steps.map((s) => (
                  <Table.Tr key={s.id}>
                    <Table.Td><Text size="sm" truncate="end" title={s.name}>{s.name}</Text></Table.Td>
                    <Table.Td ta="right"><Text size="sm" style={{ whiteSpace: 'nowrap' }}>{formatDuration(s.latency)}</Text></Table.Td>
                    <Table.Td ta="right"><Text size="sm">{count.format(s.runs)}</Text></Table.Td>
                    <Table.Td ta="right"><Text size="sm" c={s.errors > 0 ? 'red' : undefined}>{count.format(s.errors)}</Text></Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Box>
      )}
    </Stack>
  );
}
