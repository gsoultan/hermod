import { createContext, useContext, type ReactNode, useState } from 'react';
import { Handle, Position, type Node as FlowNode, type Edge as FlowEdge } from '@xyflow/react';
import { Box, Text, ActionIcon, Tooltip, Paper, Group, Stack, ThemeIcon, rem, Badge, UnstyledButton } from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { nodeSimulationResult } from '@/pages/workflows/WorkflowEditor/simulation/simulationPath';
import { nodeLiveProblem } from '@/pages/workflows/WorkflowEditor/live/liveHealth';
import { SIMULATION_STATUS_STYLE, SimulationStatusBadge } from '@/pages/workflows/WorkflowEditor/simulation/SimulationStatusBadge';
import { IconEye, IconPlus, IconTrash } from '@tabler/icons-react';
import { stepTitle } from '@/components/flows/stepTitle';
import { latestRecord } from './latestRecord';
/** The newest record a step has seen, on one line. */
function samplePreview(sample: unknown): string {
  const latest = latestRecord(sample);
  if (typeof latest === 'string') return latest;
  try {
    return JSON.stringify(latest) ?? String(latest);
  } catch {
    return String(latest);
  }
}

function problemFields(p: ReturnType<typeof nodeLiveProblem>) {
  return {
    problemSeverity: p?.severity ?? null,
    problemText: p ? p.text.charAt(0).toUpperCase() + p.text.slice(1) : null,
  };
}

export const WorkflowContext = createContext<{
  onPlusClick: (nodeId: string, handleId: string | null) => void;
  /** Connection names by id, so a source or sink step without a label is called by the connection it uses. */
  refNames?: Record<string, string>;
} | null>(null);

export const PlusHandle = ({ type, position, id, color, nodeId, style }: any) => {
  const context = useContext(WorkflowContext);
  return (
    <Handle 
      type={type} 
      position={position} 
      id={id}
      className="n8n-handle"
      style={{ 
        width: 20, 
        height: 20, 
        background: 'var(--mantine-color-body)',
        [position === Position.Right ? 'right' : position === Position.Left ? 'left' : position === Position.Bottom ? 'bottom' : 'top']: -10,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        border: `2px solid var(--mantine-color-${color}-6)`,
        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
        zIndex: 100,
        cursor: 'pointer',
        ['--handle-color' as any]: `var(--mantine-color-${color}-6)`,
        ...style
      }}
      onClick={(e) => {
        if (context) {
          e.stopPropagation();
          context.onPlusClick(nodeId, id);
        }
      }}
    >
      <IconPlus size="0.8rem" color={`var(--mantine-color-${color}-6)`} stroke={3} />
    </Handle>
  );
};

export interface Branch {
  /** The handle's id, which an edge drawn from it carries as its branch; null for the step's main output. */
  id: string | null;
  label: string;
  color: string;
}

/**
 * A step's ways out, one row each in the foot of its card: the branch's name,
 * and its handle level with that name on the card's right edge. The rows give
 * the card its height, so a long step name or many branches never put a name
 * under a handle or a handle off the card.
 */
export const BranchOutputs = ({ nodeId, branches }: { nodeId: string; branches: Branch[] }) => (
  <Box className="hermod-branches">
    {branches.map((b) => (
      <Box key={b.id ?? ''} className="hermod-branch">
        <Text size="xs" fw={500} c={b.color === 'gray' ? 'dimmed' : `${b.color}.7`} truncate title={b.label}>{b.label}</Text>
        <PlusHandle type="source" position={Position.Right} id={b.id} nodeId={nodeId} color={b.color} style={{ top: '50%' }} />
      </Box>
    ))}
  </Box>
);

export const TargetHandle = ({ position, color, style }: any) => {
  return (
    <Handle 
      type="target" 
      position={position} 
      style={{ 
        width: 12, 
        height: 12, 
        background: 'var(--mantine-color-body)',
        [position === Position.Left ? 'left' : position === Position.Right ? 'right' : position === Position.Top ? 'top' : 'bottom']: -6,
        border: `2px solid var(--mantine-color-${color}-6)`,
        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
        zIndex: 10,
        ...style
      }} 
    />
  );
};

export const BaseNode = ({ id, type, color, icon: Icon, children, data, selected, badge }: {
  id: string, 
  type: string, 
  color: string, 
  icon: any, 
  children: ReactNode, 
  data: any,
  selected?: boolean,
  /** Something the node wants said about itself on the canvas, under its description. */
  badge?: ReactNode
}) => {
  const context = useContext(WorkflowContext);

  // Actions are stable for the store's lifetime, so one shallow selection costs
  // nothing to compare and replaces five separate subscriptions per node.
  const { setSampleInspectorOpened, setSampleNodeId, setNodes, setEdges, setSelectedNode } =
    useWorkflowStore(
      useShallow(state => ({
        setSampleInspectorOpened: state.setSampleInspectorOpened,
        setSampleNodeId: state.setSampleNodeId,
        setNodes: state.setNodes,
        setEdges: state.setEdges,
        setSelectedNode: state.setSelectedNode,
      }))
    );

  const [hovered, setHovered] = useState(false);

  // One shallow subscription for this node's live telemetry instead of eight.
  //
  // Zustand evaluates every selector on every set(), so with N nodes on canvas
  // these were 11N selector runs per store write — and the store is written on
  // every telemetry frame. Selecting the scalars together under useShallow means
  // one comparison per node, and a re-render only when this node's own numbers
  // move rather than whenever any node's do.
  const live = useWorkflowStore(
    useShallow(state => ({
      metric: state.nodeMetrics[id],
      errorCount: state.nodeErrorMetrics[id],
      sample: state.nodeSamples[id],
      cbStatus: state.sinkCBStatuses[data.ref_id],
      bufferFill: state.sinkBufferFill[data.ref_id],
      sourceStatus: state.sourceStatus,
      sinkStatus: state.sinkStatuses[data.ref_id],
      // The same object until the next run, so a telemetry frame does not
      // re-render the node on its account.
      simulation: nodeSimulationResult(state.testResults, id),
      // What is wrong here on a live run, as plain fields so useShallow
      // compares them; null fields when nothing is.
      ...problemFields(state.testResults ? null : nodeLiveProblem(state, id)),
    }))
  );

  const metric = live.metric ?? data.metric;
  const errorCount = live.errorCount ?? data.errorCount;
  const sample = live.sample ?? data.sample;
  const cbStatus = live.cbStatus ?? data.cbStatus;
  const bufferFill = live.bufferFill ?? data.bufferFill;
  const { sourceStatus, sinkStatus, simulation, problemSeverity, problemText } = live;
  const problemColor = problemSeverity === 'error' ? 'red' : problemSeverity === 'warning' ? 'orange' : null;

  const nodeStatus = type === 'Source' ? sourceStatus : (type === 'Sink' ? sinkStatus : null);

  const healthColor = errorCount > 0 ? (errorCount / (metric + errorCount) > 0.1 ? 'red' : 'orange') : color;
  // While a simulation is shown, the ring says what it did here. A node the
  // message never reached fades back, so the ones it did reach read as a path;
  // hovering or selecting it brings it forward again.
  const simulationColor = simulation ? SIMULATION_STATUS_STYLE[simulation.status].color : null;
  const reached = simulation && simulation.status !== 'skipped';
  const faded = simulation?.status === 'skipped' && !hovered && !selected;
  const borderStyle = 'solid';
  const borderWidth = errorCount > 0 ? '3px' : '2px';
  
  // Reliability Indicators
  const cbOpen = cbStatus === 'open';
  const cbHalfOpen = cbStatus === 'half-open';
  const bufferHigh = bufferFill > 0.8;

  const onDelete = (e: React.MouseEvent) => {
    e.stopPropagation();
    setNodes((nds: FlowNode[]) => nds.filter((n: FlowNode) => n.id !== id));
    setEdges((eds: FlowEdge[]) => eds.filter((edge: FlowEdge) => edge.source !== id && edge.target !== id));
    setSelectedNode(null);
  };

  // What the step has done and anything wrong with it, as one line in the
  // card rather than bubbles hung off its edges.
  const stats: ReactNode[] = [];
  if (cbOpen) stats.push(<Badge key="cb" size="xs" variant="light" color="red">Circuit open</Badge>);
  if (cbHalfOpen) stats.push(<Badge key="cb" size="xs" variant="light" color="orange">Circuit half-open</Badge>);
  if (metric > 0) stats.push(<Text key="m" size="xs" c="dimmed">{`${metric.toLocaleString()} processed`}</Text>);
  if (errorCount > 0) stats.push(<Text key="e" size="xs" c="red.7">{`${errorCount.toLocaleString()} ${errorCount === 1 ? 'error' : 'errors'}`}</Text>);
  if (data.dlqCount > 0) stats.push(<Text key="d" size="xs" c="orange.7">{`${data.dlqCount.toLocaleString()} failed`}</Text>);
  if (bufferFill > 0) {
    stats.push(
      <Text key="b" size="xs" c={bufferHigh ? 'red.7' : 'dimmed'}>{`Buffer ${Math.round(bufferFill * 100)}%`}</Text>,
    );
  }

  const title = stepTitle(data, type, data.ref_id ? context?.refNames?.[data.ref_id] : undefined);
  const ringColor = cbOpen ? 'red' : (simulationColor ?? problemColor ?? (errorCount > 0 ? healthColor : null));

  return (
    <Paper
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      radius="md"
      className="hermod-step"
      data-selected={selected || undefined}
      style={{
        border: ringColor
          ? `${problemColor ? '3px' : borderWidth} ${borderStyle} var(--mantine-color-${ringColor}-6)`
          : undefined,
        boxShadow: reached ? `0 0 0 4px var(--mantine-color-${simulationColor}-light)` : undefined,
        // Greyed rather than faded hard: its label has to stay readable, since
        // the node is still one the operator may want to open.
        opacity: faded ? 0.65 : 1,
        filter: faded ? 'grayscale(1)' : undefined,
      }}
    >
      <Stack className="hermod-step-body" gap={4} px={14} py={12}>
        <Group justify="space-between" gap="xs" wrap="nowrap">
          <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
            <ThemeIcon color={color} variant="light" size={22} radius="sm">
              <Icon size="0.85rem" />
            </ThemeIcon>
            <Text size="xs" fw={600} c="dimmed" tt="uppercase" truncate style={{ letterSpacing: rem(0.6) }}>
              {type}
            </Text>
          </Group>
          <Group gap={4} wrap="nowrap">
            {nodeStatus && nodeStatus !== 'running' && (
              <Badge 
                size="xs" 
                color={nodeStatus.startsWith('error') ? 'red' : 'blue'} 
                variant="light"
              >
                {nodeStatus}
              </Badge>
            )}
          </Group>
        </Group>

        <Text fw={600} size="sm" lh={1.3} lineClamp={2} title={title}>{title}</Text>
        {data.description && <Text size="xs" c="dimmed" lineClamp={1}>{data.description}</Text>}
        {badge && <Group gap={4}>{badge}</Group>}
        {sample != null && (
          <UnstyledButton
            className="hermod-step-sample"
            aria-label="Latest record, open in full"
            onClick={(e) => {
              e.stopPropagation();
              setSampleNodeId(id);
              setSampleInspectorOpened(true);
            }}
          >
            <IconEye size="0.85rem" aria-hidden />
            <Text component="span" size="xs" ff="monospace" truncate>{samplePreview(sample)}</Text>
          </UnstyledButton>
        )}
        {stats.length > 0 && (
          <Group className="hermod-step-stats" gap={6} wrap="wrap">
            {stats}
          </Group>
        )}
      </Stack>

      {(hovered || selected) && (
        <Tooltip label="Remove node" position="top" withArrow>
          <ActionIcon aria-label="Remove node" 
            variant="filled" 
            color="red" 
            size="sm" 
            radius="xl"
            style={{ 
              position: 'absolute', 
              top: -10, 
              right: -10, 
              zIndex: 110,
              boxShadow: '0 2px 4px rgba(0,0,0,0.2)'
            }}
            onClick={onDelete}
          >
            <IconTrash size="0.8rem" />
          </ActionIcon>
        </Tooltip>
      )}

      {children}

      {/* This corner used to read data.testResult, which nothing has set since
          the refactor that dropped the simulation's canvas styling -- so a run
          changed nothing here while the toast said the path was highlighted. */}
      {simulation && <SimulationStatusBadge result={simulation} />}

      {/* On a live run, what is wrong here, above the node where it is seen
          first: stuck, hung, failing, stopped or falling behind. */}
      {problemColor && problemText && (
        <Badge
          role="note"
          aria-label={`Problem: ${problemText}`}
          color={problemColor}
          variant="filled"
          size="sm"
          radius="sm"
          style={{ position: 'absolute', top: -12, left: 10, zIndex: 105, textTransform: 'none', maxWidth: 'calc(100% - 20px)' }}
        >
          {problemText}
        </Badge>
      )}
    </Paper>
  );
};


