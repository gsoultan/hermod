import { useState } from 'react';
import {
  Alert, Badge, Box, Code, Group, Paper, ScrollArea, SegmentedControl, SimpleGrid, Stack, Text, ThemeIcon, Tooltip,
} from '@mantine/core';
import { IconCircleCheck, IconCircleX, IconClock } from '@tabler/icons-react';
import { formatDateTime } from '@/utils/dateUtils';

export interface TraceStep {
  node_id: string;
  timestamp?: string;
  duration_ms?: number;
  duration?: number;
  error?: string;
  before?: unknown;
  after?: unknown;
  data?: unknown;
}

/** A payload, as tall as it is up to a cap, then scrolling inside itself. */
function Payload({ label, value }: { label: string; value: unknown }) {
  return (
    <Box miw={0}>
      {label && <Text size="xs" fw={700} mb={4} c="dimmed">{label}</Text>}
      <Paper withBorder p="xs" bg="var(--mantine-color-default-hover)">
        <ScrollArea.Autosize mah={220} type="auto">
          <Code block bg="transparent" p={0} style={{ whiteSpace: 'pre' }}>{JSON.stringify(value ?? {}, null, 2)}</Code>
        </ScrollArea.Autosize>
      </Paper>
    </Box>
  );
}

/**
 * One node a message passed through, on the journey's timeline.
 *
 * `compact` is the phone layout: a narrower gutter and card, and the input and
 * output behind a switch rather than stacked, so a step is one screen's worth
 * of scrolling instead of three.
 */
export function JourneyStep({ step, nodeName, compact }: { step: TraceStep; nodeName?: string; compact: boolean }) {
  const [side, setSide] = useState<'before' | 'after'>('after');
  const duration = step.duration_ms || Math.round((step.duration ?? 0) / 1000000);
  const hasPair = step.before !== undefined || step.after !== undefined;
  const after = step.after ?? step.data;

  return (
    <Box
      style={{
        borderLeft: '2px solid var(--mantine-color-blue-2)',
        paddingLeft: compact ? '1.25rem' : '2rem',
        paddingBottom: compact ? '1rem' : '2rem',
        position: 'relative',
      }}
    >
      <ThemeIcon
        variant="filled"
        size={compact ? 'sm' : 'md'}
        radius="xl"
        color={step.error ? 'red' : 'blue'}
        style={{ position: 'absolute', left: compact ? '-11px' : '-13px', top: 0 }}
      >
        {step.error ? <IconCircleX size="1rem" /> : <IconCircleCheck size="1rem" />}
      </ThemeIcon>

      <Paper withBorder p={compact ? 'sm' : 'md'} radius="md" shadow="xs">
        <Stack gap="xs">
          <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
            <Box miw={0}>
              <Text fw={700} truncate>Node: {nodeName || step.node_id}</Text>
              {nodeName && (
                <Tooltip label="Internal node ID" withArrow>
                  <Text size="xs" c="dimmed" truncate>ID: <Code>{step.node_id}</Code></Text>
                </Tooltip>
              )}
            </Box>
            <Badge leftSection={<IconClock size="0.8rem" />} variant="light" style={{ flex: 'none' }}>
              {duration}ms
            </Badge>
          </Group>

          <Text size="xs" c="dimmed">{formatDateTime(step.timestamp)}</Text>

          {step.error && (
            <Alert color="red" icon={<IconCircleX size="1rem" />} title="Processing Error" p={compact ? 'xs' : undefined}>
              <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{step.error}</Text>
            </Alert>
          )}

          {hasPair ? (
            compact ? (
              <Stack gap={6}>
                <SegmentedControl
                  size="xs"
                  fullWidth
                  value={side}
                  onChange={(v) => setSide(v as 'before' | 'after')}
                  data={[{ value: 'before', label: 'Input' }, { value: 'after', label: 'Output' }]}
                  aria-label="Show the step's input or output"
                />
                <Payload label="" value={side === 'before' ? step.before : after} />
              </Stack>
            ) : (
              <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
                <Payload label="INPUT / BEFORE" value={step.before} />
                <Payload label="OUTPUT / AFTER" value={after} />
              </SimpleGrid>
            )
          ) : step.data !== undefined && (
            <Payload label="DATA" value={step.data} />
          )}
        </Stack>
      </Paper>
    </Box>
  );
}
