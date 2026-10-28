import { memo, useState, type CSSProperties } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import { HoverCard, Code, Stack, Text, ActionIcon, Tooltip, Badge } from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import {
  edgeSimulationView,
  simulationOutdated,
  type EdgeSimulationState,
} from '@/pages/workflows/WorkflowEditor/simulation/simulationPath';
import { TAKEN_EDGE_MARKER_ID } from '@/pages/workflows/WorkflowEditor/simulation/SimulationOverlay';
import { edgeLiveView, type EdgeLiveState } from '@/pages/workflows/WorkflowEditor/live/liveHealth';
import { REPLAY_HOP_MS } from '@/pages/workflows/WorkflowEditor/simulation/SimulationReplay';
import { IconTrash, IconPlayerPause, IconPlayerPlay } from '@tabler/icons-react';

function formatPreview(val: any): string {
  try {
    if (typeof val === 'string') return val.length > 200 ? val.slice(0, 200) + '…' : val;
    return JSON.stringify(val, null, 2).slice(0, 400);
  } catch {
    return String(val);
  }
}

type Tone = 'muted' | 'warning' | 'error' | 'success' | undefined;

const color = (name: string) => `var(--mantine-color-${name})`;

/** How a live edge is drawn: its line, the colour of its moving dots, and its chip's tone. */
const LIVE_LOOK: Record<EdgeLiveState, { stroke: string; width: number; dash?: string; opacity?: number; tone: Tone }> = {
  idle: { stroke: color('gray-5'), width: 1.5, tone: undefined },
  stale: { stroke: color('gray-5'), width: 1.5, dash: '4 6', tone: 'muted' },
  quiet: { stroke: color('gray-6'), width: 1.5, tone: 'muted' },
  flowing: { stroke: color('blue-6'), width: 2.5, opacity: 0.35, tone: undefined },
  backpressure: { stroke: color('orange-6'), width: 2.5, opacity: 0.4, tone: 'warning' },
  failing: { stroke: color('blue-6'), width: 2.5, opacity: 0.35, tone: 'error' },
  hung: { stroke: color('red-6'), width: 2.5, dash: '6 4', tone: 'error' },
  blocked: { stroke: color('red-6'), width: 2.5, tone: 'error' },
  held: { stroke: color('orange-6'), width: 2, dash: '2 6', tone: 'warning' },
};

const DOTS_COLOR: Partial<Record<EdgeLiveState, string>> = {
  flowing: color('blue-6'),
  failing: color('blue-6'),
  backpressure: color('orange-6'),
};

/** The simulation states drawn as the message having crossed the edge. */
const CROSSED: ReadonlySet<EdgeSimulationState> = new Set(['taken', 'into-error', 'into-filtered']);

function simulationLook(state: EdgeSimulationState, outdated: boolean): CSSProperties {
  if (CROSSED.has(state)) {
    return {
      stroke: color('green-6'),
      strokeWidth: 3.5,
      strokeDasharray: outdated ? '6 5' : 'none',
      opacity: outdated ? 0.45 : 1,
    };
  }
  if (state === 'new') return { stroke: color('gray-6'), strokeWidth: 1.5 };
  if (state === 'crossing') return { stroke: color('gray-5'), strokeWidth: 1.5, opacity: 0.5 };
  // untaken and pending: set back, so the path the message took stands out.
  return { stroke: color('gray-5'), strokeWidth: 1.5, strokeDasharray: '1 6', opacity: 0.35 };
}

export const LiveEdge = memo((props: EdgeProps) => {
  const {
    id, source, target, sourceX, sourceY, targetX, targetY,
    sourcePosition, targetPosition, markerEnd, selected, data,
    deletable
  } = props;
  const [edgePath, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });

  const [hovered, setHovered] = useState(false);

  const pulseEnabled = useWorkflowStore(s => s.pulseEnabled);
  // Each edge reads only what decides its own look, compared field by field,
  // so a status frame re-renders an edge only when its state, speed or chip
  // text changes.
  const live = useWorkflowStore(useShallow(s => edgeLiveView(s, { id, target })));
  // While a simulation is shown it decides how the edge is drawn.
  const simulation = useWorkflowStore(useShallow(s => edgeSimulationView(s.testResults, s.testRunGraph, s.simReplay, id)));
  const outdated = useWorkflowStore(s => simulationOutdated(s.testResults, s.testRunGraph, s.nodes, s.edges));
  const hopMs = useWorkflowStore(s => REPLAY_HOP_MS / s.replaySpeed);
  const srcSample = useWorkflowStore(s => s.nodeSamples[source]);
  // The message that crossed this edge in the simulation: what its source emitted.
  const simulatedPayload = useWorkflowStore(s =>
    simulation ? (s.testResults?.find((r: any) => r.node_id === source && r.payload) as any)?.payload : undefined
  );
  const setEdges = useWorkflowStore(s => s.setEdges);
  const hasBreakpoint = !!(data as any)?.breakpoint;

  const samples = Array.isArray(srcSample) ? srcSample : (srcSample ? [srcSample] : []);

  let pathStyle: CSSProperties;
  let chip: { text: string; tone: Tone } | null = null;
  let tail: string | null = null;
  let dots: string | null = null;
  let crossing = false;
  let markerUrl = markerEnd;

  if (simulation) {
    pathStyle = simulationLook(simulation.state, outdated);
    if (CROSSED.has(simulation.state)) markerUrl = `url(#${TAKEN_EDGE_MARKER_ID})`;
    if (simulation.state === 'into-error') {
      tail = color('red-6');
      chip = { text: `${simulation.hop} · failed here`, tone: 'error' };
    } else if (simulation.state === 'into-filtered') {
      chip = { text: `${simulation.hop} · filtered`, tone: 'muted' };
    } else if (simulation.state === 'taken') {
      chip = { text: String(simulation.hop), tone: 'success' };
    } else if (simulation.state === 'new') {
      chip = { text: 'new', tone: 'muted' };
    } else if (simulation.state === 'crossing') {
      crossing = true;
    }
  } else {
    const look = LIVE_LOOK[live.state];
    pathStyle = {
      stroke: hasBreakpoint && live.state === 'idle' ? color('orange-6') : look.stroke,
      strokeWidth: look.width,
      strokeDasharray: look.dash ?? (hasBreakpoint && live.state === 'idle' ? '2 6' : 'none'),
      opacity: look.opacity ?? 1,
    };
    if (live.label) chip = { text: live.label, tone: look.tone };
    if (pulseEnabled && live.band > 0 && DOTS_COLOR[live.state]) dots = DOTS_COLOR[live.state]!;
    if (live.state === 'failing') tail = color('red-6');
  }
  if (selected) pathStyle = { ...pathStyle, strokeWidth: Number(pathStyle.strokeWidth ?? 1.5) + 1.5 };

  const dotColor = hasBreakpoint
    ? (selected ? color('orange-7') : color('orange-5'))
    : simulation && CROSSED.has(simulation.state)
      ? color('green-6')
      : (selected ? color('blue-7') : color('blue-5'));
  const dimmed = simulation?.state === 'untaken' || simulation?.state === 'pending';
  const previews = simulation ? (simulatedPayload !== undefined ? [simulatedPayload] : []) : samples;

  return (
    <>
      {/* The keyframes and classes live in index.css: one copy for the page
          rather than a <style> element per edge. */}
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerUrl}
        data-simulation={simulation ? (CROSSED.has(simulation.state) ? 'taken' : simulation.state) : undefined}
        data-simulation-end={simulation?.state === 'into-error' ? 'error' : simulation?.state === 'into-filtered' ? 'filtered' : undefined}
        data-hop={simulation?.hop ?? undefined}
        data-outdated={simulation && outdated ? 'true' : undefined}
        data-flow-state={simulation ? undefined : live.state}
        data-flow-band={simulation ? undefined : live.band}
        style={{
          ...pathStyle,
          cursor: 'pointer',
          strokeLinecap: 'round',
          strokeLinejoin: 'round'
        }}
      />
      {dots && (
        <path
          d={edgePath}
          className={`hermod-edge-dots band-${live.band}${live.state === 'backpressure' ? ' backpressure' : ''}`}
          style={{ stroke: dots }}
          aria-hidden="true"
        />
      )}
      {tail && (
        <path d={edgePath} pathLength={100} className="hermod-edge-tail" style={{ stroke: tail, strokeWidth: 3.5 }} aria-hidden="true" />
      )}
      {crossing && (
        <path
          d={edgePath}
          pathLength={100}
          className="hermod-edge-crossing"
          style={{ stroke: color('green-6'), strokeWidth: 3.5, ['--hermod-hop-ms' as string]: `${hopMs}ms` }}
          aria-hidden="true"
        />
      )}
      <EdgeLabelRenderer>
        <div
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            pointerEvents: 'all',
            display: 'flex',
            alignItems: 'center',
            gap: '6px'
          }}
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
        >
          <HoverCard withArrow shadow="md" position="top" offset={6} withinPortal>
            <HoverCard.Target>
              {chip ? (
                <span className="hermod-edge-chip" data-testid={`edge-chip-${id}`} data-tone={chip.tone} style={{ cursor: 'help' }}>
                  {chip.text}
                </span>
              ) : (
                <div style={{
                  width: 12,
                  height: 12,
                  borderRadius: 999,
                  background: dotColor,
                  cursor: 'help',
                  opacity: dimmed ? 0.35 : 0.8,
                  border: selected ? '2px solid white' : 'none',
                  boxShadow: '0 0 4px rgba(0,0,0,0.2)'
                }} />
              )}
            </HoverCard.Target>
            <HoverCard.Dropdown>
              <Stack gap="xs" maw={360}>
                <Text fw={700} size="xs">
                  {simulation ? 'Message on this edge in the simulation' : `Recent messages (${samples.length})`}
                </Text>
                {hasBreakpoint && <Badge size="xs" color="orange" variant="light">Breakpoint</Badge>}
                {previews.length === 0 ? (
                  <Text size="xs" c="dimmed">{simulation ? 'No message crossed this edge' : 'No samples yet'}</Text>
                ) : (
                  previews.slice(0, 5).map((s: any, i: number) => (
                    <Code key={i} block style={{ fontSize: 11 }}>
                      {formatPreview(s)}
                    </Code>
                  ))
                )}
                {!simulation && live.label && (
                  <Text size="xs" c="dimmed">Now: {live.label}</Text>
                )}
              </Stack>
            </HoverCard.Dropdown>
          </HoverCard>

          {(selected || hovered) && deletable !== false && (
            <div style={{ display: 'flex', gap: 6 }}>
              <Tooltip label={hasBreakpoint ? 'Remove breakpoint' : 'Add breakpoint'} position="right" withArrow>
                <ActionIcon aria-label="Start"
                  color={hasBreakpoint ? 'orange' : 'gray'}
                  variant={hasBreakpoint ? 'filled' : 'light'}
                  size="sm"
                  radius="xl"
                  onClick={(e) => {
                    e.stopPropagation();
                    setEdges((eds) => eds.map((edge) => edge.id === id ? {
                      ...edge,
                      data: { ...(edge.data || {}), breakpoint: !hasBreakpoint }
                    } : edge));
                  }}
                  style={{ boxShadow: '0 0 4px rgba(0,0,0,0.2)' }}
                >
                  {hasBreakpoint ? <IconPlayerPlay size="0.9rem" /> : <IconPlayerPause size="0.9rem" />}
                </ActionIcon>
              </Tooltip>
              <Tooltip label="Remove connection" position="right" withArrow>
                <ActionIcon aria-label="Remove connection"
                  color="red"
                  variant="filled"
                  size="sm"
                  radius="xl"
                  onClick={(e) => {
                    e.stopPropagation();
                    setEdges((eds) => eds.filter((edge) => edge.id !== id));
                  }}
                  style={{ boxShadow: '0 0 4px rgba(0,0,0,0.2)' }}
                >
                  <IconTrash size="0.9rem" />
                </ActionIcon>
              </Tooltip>
            </div>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  );
});

LiveEdge.displayName = 'LiveEdge';
