import { memo, useCallback, useEffect, useMemo } from 'react';
import { useMediaQuery } from '@mantine/hooks';
import {
  ReactFlow,
  Background,
  MiniMap,
  Position,
  Handle,
  MarkerType,
  useReactFlow,
  useStore,
  type Node,
  type Edge
} from '@xyflow/react';
import { Badge, Box, Group, Paper, Stack, Text, ThemeIcon, rem, useMantineColorScheme } from '@mantine/core';
import { useShallow } from 'zustand/react/shallow';
import {
  IconCircles, IconCloud, IconDatabase, IconFileSpreadsheet, IconGitBranch, IconList, IconServer,
  IconPlayerStop, IconPlugConnectedX, IconSettingsAutomation, IconVariable, IconWorld
} from '@tabler/icons-react';
import dagre from 'dagre';
import { CanvasControls } from '@/components/flows/CanvasControls';
import { stepTitle } from '@/components/flows/stepTitle';
import { transformationLabel } from '../nodes/transformationLabel';
import { StepProblem, StepSample, stepStats, useStepLive } from '../nodes/stepLive';
import { useWorkflowStore } from '../store/useWorkflowStore';
import { isEngineLive } from '../live/liveHealth';
import { LiveEdge } from './LiveEdge';

/**
 * A step as the read-only view draws it: the same card as the editor's (see
 * BaseNode and .hermod-step), its kind, its name and one detail line, and on a
 * live run what the editor shows of it: its counts, the newest record behind
 * the eye, and anything wrong with it. Nothing on it edits the flow.
 */
function StepCard({ id, data, color, icon: Icon, kind, title, detail, inbound, outbound, vertical, endpoint }: {
  id: string; data: any;
  color: string; icon: any; kind: string; title: string; detail?: string; inbound?: boolean; outbound?: boolean;
  /** On a phone the flow runs top to bottom, so lines enter at the top and leave at the bottom. */
  vertical?: boolean;
  /** A source or sink reports a status of its own. */
  endpoint?: 'source' | 'sink';
}) {
  const live = useStepLive(id, data);
  const stats = stepStats({ cbStatus: live.cbStatus, metric: live.metric, errorCount: live.errorCount, dlqCount: data.dlqCount, bufferFill: live.bufferFill });
  const status = endpoint === 'source' ? live.sourceStatus : endpoint === 'sink' ? live.sinkStatus : null;
  const ring = live.cbStatus === 'open' ? 'red' : live.problemColor ?? (live.errorCount > 0 ? 'orange' : null);
  return (
    <Paper
      radius="md"
      className="hermod-step"
      style={ring ? { border: `${live.problemColor ? 3 : 2}px solid var(--mantine-color-${ring}-6)` } : undefined}
    >
      {inbound && <Handle type="target" position={vertical ? Position.Top : Position.Left} style={{ visibility: 'hidden' }} />}
      {outbound && <Handle type="source" position={vertical ? Position.Bottom : Position.Right} style={{ visibility: 'hidden' }} />}
      <Stack className="hermod-step-body" gap={4} px={14} py={12}>
        <Group justify="space-between" gap="xs" wrap="nowrap">
          <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
            <ThemeIcon color={color} variant="light" size={22} radius="sm">
              <Icon size="0.85rem" />
            </ThemeIcon>
            <Text size="xs" fw={600} c="dimmed" tt="uppercase" truncate style={{ letterSpacing: rem(0.6) }}>{kind}</Text>
          </Group>
          {status && status !== 'running' && (
            <Badge size="xs" variant="light" color={status.startsWith('error') ? 'red' : 'blue'}>{status}</Badge>
          )}
        </Group>
        <Text size="sm" fw={600} lh={1.3} lineClamp={2} title={title}>{title}</Text>
        {detail && <Text size="xs" c="dimmed" truncate>{detail}</Text>}
        {live.sample != null && <StepSample nodeId={id} sample={live.sample} />}
        {stats.length > 0 && <Group className="hermod-step-stats" gap={6} wrap="wrap">{stats}</Group>}
      </Stack>
      {live.problemColor && live.problemText && <StepProblem color={live.problemColor} text={live.problemText} />}
    </Paper>
  );
}

const SOURCE_ICONS: Record<string, any> = {
  webhook: IconWorld, cron: IconSettingsAutomation, csv: IconFileSpreadsheet, kafka: IconCircles,
  rabbitmq: IconList, rabbitmq_queue: IconList,
};

const SourceNode = memo(({ id, data }: any) => (
  <StepCard
    id={id}
    data={data}
    endpoint="source"
    color="blue"
    icon={SOURCE_ICONS[data.type] ?? IconDatabase}
    kind="Source"
    title={stepTitle(data, 'Source', data.connectionName)}
    detail={data.description || data.type}
    outbound
    vertical={data.vertical}
  />
));

const SinkNode = memo(({ id, data }: any) => {
  const icon = data.type === 'http' ? IconWorld
    : ['s3', 's3-parquet', 'ftp'].includes(data.type) ? IconCloud
    : ['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'clickhouse', 'yugabyte'].includes(data.type) ? IconDatabase
    : IconServer;
  return (
    <StepCard
      id={id}
      data={data}
      endpoint="sink"
      color="green"
      icon={icon}
      kind="Sink"
      title={stepTitle(data, 'Sink', data.connectionName)}
      detail={data.description || data.type}
      inbound
      vertical={data.vertical}
    />
  );
});

/** The editor's name for the kind of step; the kind line is uppercased by CSS. */
function kindOf(type: string | undefined, data: any): string {
  if (data.isGroup) return 'Group';
  if (type === 'transformation' || !type) return transformationLabel(data.transType);
  return type.charAt(0).toUpperCase() + type.slice(1).replace(/[_-]+/g, ' ');
}

const StepNode = memo(({ id, data, type }: any) => {
  const kind = kindOf(type, data);
  return (
    <StepCard
      id={id}
      data={data}
      color="violet"
      icon={data.isGroup ? IconGitBranch : IconVariable}
      kind={kind}
      title={stepTitle(data, kind)}
      detail={data.description}
      inbound
      outbound
      vertical={data.vertical}
    />
  );
});

export const detailNodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: StepNode,
  condition: StepNode,
  switch: StepNode,
  router: StepNode,
  merge: StepNode,
  stateful: StepNode,
  wait: StepNode,
  foreach: StepNode,
  explode: StepNode,
  ml_recommend_build: StepNode,
  ai_classify: StepNode,
  ai_agent: StepNode,
  // Without these React Flow drew a bare default box, with no handles, for
  // node types the editor draws. detailCanvasNodeTypes.test.tsx keeps the
  // two maps in step.
  validator: StepNode,
  approval: StepNode,
  log: StepNode,
  collect: StepNode,
  deduplicate: StepNode,
  note: StepNode,
};

/** Every line is a live one: it moves, at the rate messages cross it, only while they do. */
export const detailEdgeTypes = { default: LiveEdge, live: LiveEdge };

/**
 * A saved flow's edges as the view draws them. Not deletable, which also keeps
 * LiveEdge's breakpoint and remove buttons off. No handle ids: a view card has
 * one way in and one way out. No React Flow `animated`: it ran its own dash on
 * every line of an active flow, whether or not anything was moving.
 */
export function toViewEdges(workflow: { edges?: any[] } | undefined): Edge[] {
  return (workflow?.edges || []).map((edge: any) => ({
    id: edge.id,
    source: edge.source_id,
    target: edge.target_id,
    type: 'live',
    data: edge.config,
    deletable: false,
    markerEnd: { type: MarkerType.ArrowClosed, width: 20, height: 20, color: 'var(--mantine-color-gray-5)' },
  }));
}

/**
 * Whether what the canvas shows is happening now: live while the flow runs and
 * its feed is current, paused when the feed has gone quiet, stopped otherwise.
 * It took the place of a note that said only "Read only", so a running flow
 * and a stopped one looked the same.
 */
export function LiveNote({ compact }: { compact?: boolean }) {
  const { workflowStatus, stale } = useWorkflowStore(useShallow((s) => ({ workflowStatus: s.workflowStatus, stale: s.statusFeedStale })));
  const state = !isEngineLive(workflowStatus) ? 'stopped' : stale ? 'stale' : 'live';
  return (
    <Group gap={8} wrap="nowrap" className="hermod-canvas-note" data-testid="flow-live-note" data-state={state} aria-live="polite">
      {state === 'live' && <span className="hermod-live-dot" aria-hidden />}
      {state === 'stale' && <IconPlugConnectedX size="0.95rem" color="var(--mantine-color-orange-6)" aria-hidden />}
      {state === 'stopped' && <IconPlayerStop size="0.95rem" aria-hidden />}
      <Text size="sm" fw={600} c={state === 'live' ? 'green.7' : state === 'stale' ? 'orange.7' : undefined}>
        {state === 'live' ? 'Live' : state === 'stale' ? 'Live data paused' : 'Stopped'}
      </Text>
      <Text size="sm" c="dimmed" truncate>
        {state === 'stale' ? 'Reconnecting…'
          : compact ? 'Read only.'
          : state === 'live' ? 'Read only. Each step shows its latest record.'
          : 'Read only. Turn it on to see live data.'}
      </Text>
    </Group>
  );
}

/** The smallest a phone draws the flow: below this a step's name cannot be read. */
const PHONE_MIN_ZOOM = 0.7;

interface DetailFlowCanvasProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: any;
  onEdgesChange: any;
  setNodes: (nds: any) => void;
  /** Connection names by id, for steps that have no label of their own. */
  connectionNames?: Record<string, string>;
}

export function DetailFlowCanvas({ nodes, edges, onNodesChange, onEdgesChange, setNodes, connectionNames }: DetailFlowCanvasProps) {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';
  const { zoomIn, zoomOut, fitView, setViewport, getNodesBounds } = useReactFlow();
  const zoom = useStore((s) => s.transform[2]);
  const paneWidth = useStore((s) => s.width);

  const phone = useMediaQuery('(max-width: 47.99em)');

  // On a phone the flow is drawn top to bottom, so it reads straight down
  // without panning sideways. Display only: the view never saves positions.
  const named = useMemo(() => {
    const withNames = nodes.map((n: Node) => {
      const name = connectionNames?.[(n.data as any)?.ref_id];
      return name || phone ? { ...n, data: { ...n.data, ...(name ? { connectionName: name } : {}), vertical: phone || undefined } } : n;
    });
    if (!phone) return withNames;
    const g = new dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: 'TB', nodesep: 24, ranksep: 36 });
    // Each step at its measured height: on a live run a card grows by its
    // latest record and counts, and at a fixed height they overlapped.
    const heightOf = (n: Node) => n.measured?.height ?? 76;
    withNames.forEach((n) => g.setNode(n.id, { width: 240, height: heightOf(n) }));
    edges.forEach((e) => g.setEdge(e.source, e.target));
    dagre.layout(g);
    return withNames.map((n) => {
      const p = g.node(n.id);
      return p ? { ...n, position: { x: p.x - 120, y: p.y - heightOf(n) / 2 } } : n;
    });
  }, [nodes, edges, connectionNames, phone]);

  // A phone opens at the top of the flow, where it starts, at a size that can
  // be read. A flow with many branches is wider than the screen; shrinking it
  // to fit would make every step unreadable, so it stays readable and the
  // other branches are a swipe sideways.
  const placePhone = useCallback(() => {
    if (!named.length || !paneWidth) return;
    const bounds = getNodesBounds(named);
    const z = Math.min(1, Math.max(PHONE_MIN_ZOOM, (paneWidth - 24) / bounds.width));
    const targets = new Set(edges.map((e) => e.target));
    const root = named.find((n) => !targets.has(n.id)) ?? named[0];
    const rootCentre = root.position.x + 120;
    setViewport({ x: paneWidth / 2 - rootCentre * z, y: 60 - bounds.y * z, zoom: z });
  }, [named, edges, paneWidth, getNodesBounds, setViewport]);

  useEffect(() => {
    const t = setTimeout(() => (phone ? placePhone() : fitView({ padding: 0.2 })), 0);
    return () => clearTimeout(t);
  }, [phone, placePhone, fitView]);

  const onLayout = useCallback(() => {
    const dagreGraph = new dagre.graphlib.Graph();
    dagreGraph.setDefaultEdgeLabel(() => ({}));
    dagreGraph.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 100 });

    nodes.forEach((node) => {
      dagreGraph.setNode(node.id, { width: 240, height: node.measured?.height ?? 84 });
    });

    edges.forEach((edge) => {
      dagreGraph.setEdge(edge.source, edge.target);
    });

    dagre.layout(dagreGraph);

    setNodes((nds: Node[]) =>
      nds.map((node: Node) => {
        const nodeWithPosition = dagreGraph.node(node.id);
        return {
          ...node,
          position: {
            x: nodeWithPosition.x - 120,
            y: nodeWithPosition.y - (node.measured?.height ?? 84) / 2,
          },
        };
      })
    );
  }, [nodes, edges, setNodes]);

  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={named}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={detailNodeTypes}
        edgeTypes={detailEdgeTypes}
        nodesConnectable={false}
        fitView
        fitViewOptions={{ padding: 0.2 }}
      >
        <Background color={isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'} gap={20} />
        <MiniMap
          pannable
          nodeColor={(n) => {
            if (n.type === 'source') return 'var(--mantine-color-blue-6)';
            if (n.type === 'sink') return 'var(--mantine-color-green-6)';
            return 'var(--mantine-color-violet-6)';
          }}
          maskColor={isDark ? 'rgba(0, 0, 0, 0.45)' : undefined}
          style={{
            backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
            width: 168,
            height: 96,
            border: '1px solid var(--mantine-color-default-border)',
            borderRadius: 'var(--mantine-radius-md)',
            overflow: 'hidden',
          }}
        />
      </ReactFlow>
      <LiveNote compact={phone} />
      <Box className="hermod-canvas-bottom-left" style={{ bottom: 12 }}>
        <CanvasControls
          zoom={zoom}
          onZoomIn={() => zoomIn()}
          onZoomOut={() => zoomOut()}
          onFit={() => (phone ? placePhone() : fitView({ padding: 0.2 }))}
          onTidy={phone ? undefined : onLayout}
        />
      </Box>
    </div>
  );
}
