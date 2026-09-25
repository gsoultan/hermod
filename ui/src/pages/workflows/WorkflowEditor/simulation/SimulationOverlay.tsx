import { Badge, Box, Button, Group, Paper, Text, ThemeIcon, Tooltip } from '@mantine/core';
import { Panel } from '@xyflow/react';
import { IconEraser, IconRoute } from '@tabler/icons-react';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { simulationCounts, simulationFailures, simulationStatusWord, type SimulationNodeStatus } from './simulationPath';
import { SIMULATION_STATUS_STYLE } from './SimulationStatusBadge';

/** The arrowhead on an edge the simulated message travelled along. */
export const TAKEN_EDGE_MARKER_ID = 'hermod-simulation-taken-arrow';

const ORDER: SimulationNodeStatus[] = ['passed', 'filtered', 'error', 'skipped'];

/**
 * The last simulation, summed up on the canvas: how many nodes ended each way,
 * which ones failed and why, and a way to clear it. Nothing is drawn until a
 * simulation has run.
 *
 * It also defines the arrowhead the path's edges point with. The edges' own
 * markers take their colour from the saved edge, which is grey for a workflow
 * that is not running, so a green path would end in grey arrows.
 */
export function SimulationOverlay() {
  // Both are plain values or short lists, compared shallowly, so dragging a
  // node or a telemetry frame does not re-render the summary.
  const counts = useWorkflowStore(useShallow((s) => simulationCounts(s.testResults, s.nodes)));
  const failures = useWorkflowStore(useShallow((s) => simulationFailures(s.testResults, s.nodes)));
  const setTestResults = useWorkflowStore((s) => s.setTestResults);

  if (!counts) return null;

  return (
    <>
      <svg aria-hidden="true" focusable="false" style={{ position: 'absolute', width: 0, height: 0 }}>
        <defs>
          <marker
            id={TAKEN_EDGE_MARKER_ID}
            viewBox="-10 -10 20 20"
            markerWidth="10"
            markerHeight="10"
            orient="auto-start-reverse"
            refX="0"
            refY="0"
          >
            <polyline
              points="-5,-4 0,0 -5,4 -5,-4"
              strokeLinecap="round"
              strokeLinejoin="round"
              style={{ stroke: 'var(--mantine-color-green-6)', fill: 'var(--mantine-color-green-6)', strokeWidth: 1 }}
            />
          </marker>
        </defs>
      </svg>

      <Panel position="top-center">
        {/* max-content: a centred panel is offset by half the canvas, so it is
            otherwise only given half the canvas's width and wrapped onto a second
            row that covered the nodes beneath it. */}
        <Paper
          component="section"
          aria-label="Simulation result"
          withBorder
          shadow="md"
          radius="md"
          px="sm"
          py={4}
          w="max-content"
          maw="calc(100vw - 2rem)"
        >
          <Group gap="sm" wrap="wrap" justify="center">
            <Group gap={6} wrap="nowrap">
              <ThemeIcon variant="light" color="green" size="sm" radius="xl">
                <IconRoute size={14} aria-hidden />
              </ThemeIcon>
              <Text size="sm" fw={600}>Simulation result</Text>
            </Group>

            <Group gap={6} wrap="wrap">
              {ORDER.filter((status) => counts[status] > 0).map((status) => {
                const { color, icon: Icon } = SIMULATION_STATUS_STYLE[status];
                const badge = (
                  <Badge color={color} variant="light" leftSection={<Icon size={12} stroke={3} aria-hidden />}>
                    {counts[status]} {simulationStatusWord(status)}
                  </Badge>
                );
                if (status !== 'error' || failures.length === 0) {
                  return <Box key={status} component="span">{badge}</Box>;
                }
                return (
                  <Tooltip key={status} label={failures.join('\n')} multiline maw={360} withArrow style={{ whiteSpace: 'pre-line' }}>
                    {badge}
                  </Tooltip>
                );
              })}
            </Group>

            <Group gap={6} wrap="nowrap" aria-hidden>
              <Box w={18} h={3} bg="green.6" style={{ borderRadius: 2 }} />
              <Text size="xs" c="dimmed">path taken</Text>
            </Group>

            <Button
              size="compact-sm"
              variant="subtle"
              color="gray"
              leftSection={<IconEraser size={14} aria-hidden />}
              aria-label="Clear simulation"
              onClick={() => setTestResults(null)}
            >
              Clear
            </Button>
          </Group>
        </Paper>
      </Panel>
    </>
  );
}
