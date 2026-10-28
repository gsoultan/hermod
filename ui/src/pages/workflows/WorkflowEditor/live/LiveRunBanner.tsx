import { Anchor, Group, Paper, Stack, Text, ThemeIcon } from '@mantine/core';
import { Panel, useReactFlow } from '@xyflow/react';
import { IconAlertTriangle, IconPlugConnectedX } from '@tabler/icons-react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { isEngineLive, liveProblems, type LiveProblem } from './liveHealth';

/** Problems listed by name; the rest are counted. */
const SHOWN = 4;

/**
 * What is wrong with the running workflow, on the canvas where it can't be
 * missed: nodes failing, stuck, hung or stopped, a stalled workflow, and a
 * status feed that has gone quiet. Each problem jumps to its node. Nothing is
 * shown for a healthy run, a stopped workflow, or while a simulation is shown.
 */
export function LiveRunBanner() {
  const { running, stalled, stale, simulating } = useWorkflowStore(
    useShallow((s) => ({
      running: isEngineLive(s.workflowStatus),
      stalled: s.workflowStatus.trim().toLowerCase() === 'stalled',
      stale: s.statusFeedStale,
      simulating: Array.isArray(s.testResults),
    })),
  );
  // Compared item by item, so a frame that changes no problem changes nothing here.
  const problems = useWorkflowStore(
    useShallow((s) => liveProblems(s).map((p) => `${p.severity}|${p.nodeId}|${p.text}`)),
  ).map(parse);
  const { fitView } = useReactFlow();

  if (!running || simulating || (!stalled && !stale && problems.length === 0)) return null;

  const errors = stalled || problems.some((p) => p.severity === 'error');
  const more = problems.length - SHOWN;

  return (
    <Panel position="top-center">
      <Paper
        component="output"
        aria-label="Live run"
        withBorder
        shadow="md"
        radius="md"
        px="sm"
        py={6}
        display="block"
        w="max-content"
        maw="calc(100vw - 2rem)"
        style={{ borderColor: `var(--mantine-color-${errors ? 'red' : 'orange'}-outline)` }}
      >
        <Group gap="sm" align="flex-start" wrap="nowrap">
          <ThemeIcon variant="light" color={stale && !errors ? 'gray' : errors ? 'red' : 'orange'} size="sm" radius="xl" mt={2}>
            {stale && problems.length === 0 && !stalled ? <IconPlugConnectedX size={14} aria-hidden /> : <IconAlertTriangle size={14} aria-hidden />}
          </ThemeIcon>
          <Stack gap={2}>
            {stalled && (
              <Text size="sm" fw={600} c="red">Workflow stalled: messages have stopped making progress.</Text>
            )}
            {stale && (
              <Text size="sm" c="dimmed">Live status lost. Reconnecting; the canvas is not current.</Text>
            )}
            {problems.slice(0, SHOWN).map((p) => (
              <Anchor
                key={p.nodeId}
                component="button"
                type="button"
                size="sm"
                c={p.severity === 'error' ? 'red' : 'orange'}
                ta="left"
                onClick={() => fitView({ nodes: [{ id: p.nodeId }], duration: 300, maxZoom: 1.2 })}
              >
                {p.text}
              </Anchor>
            ))}
            {more > 0 && <Text size="xs" c="dimmed">and {more} more</Text>}
          </Stack>
        </Group>
      </Paper>
    </Panel>
  );
}

function parse(key: string): Pick<LiveProblem, 'severity' | 'nodeId' | 'text'> {
  const [severity, nodeId, ...text] = key.split('|');
  return { severity: severity as LiveProblem['severity'], nodeId, text: text.join('|') };
}
