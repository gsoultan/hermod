import { memo, useCallback, useEffect, useMemo } from 'react';
import { useMediaQuery } from '@mantine/hooks';
import {
  ReactFlow,
  Background,
  MiniMap,
  Position,
  Handle,
  useReactFlow,
  useStore,
  type Node,
  type Edge
} from '@xyflow/react';
import { Box, Group, Paper, Stack, Text, ThemeIcon, rem, useMantineColorScheme } from '@mantine/core';
import {
  IconCircles, IconCloud, IconDatabase, IconEye, IconFileSpreadsheet, IconGitBranch, IconList, IconServer,
  IconSettingsAutomation, IconVariable, IconWorld
} from '@tabler/icons-react';
import dagre from 'dagre';
import { CanvasControls } from '@/components/flows/CanvasControls';
import { stepTitle } from '@/components/flows/stepTitle';
import { transformationLabel } from '../nodes/transformationLabel';

/**
 * A step as the read-only view draws it: the same card as the editor's (see
 * BaseNode and .hermod-step), its kind, its name and one detail line.
 */
function StepCard({ color, icon: Icon, kind, title, detail, inbound, outbound, vertical }: {
  color: string; icon: any; kind: string; title: string; detail?: string; inbound?: boolean; outbound?: boolean;
  /** On a phone the flow runs top to bottom, so lines enter at the top and leave at the bottom. */
  vertical?: boolean;
}) {
  return (
    <Paper radius="md" className="hermod-step">
      {inbound && <Handle type="target" position={vertical ? Position.Top : Position.Left} style={{ visibility: 'hidden' }} />}
      {outbound && <Handle type="source" position={vertical ? Position.Bottom : Position.Right} style={{ visibility: 'hidden' }} />}
      <Stack gap={4} px={14} py={12}>
        <Group gap={8} wrap="nowrap">
          <ThemeIcon color={color} variant="light" size={22} radius="sm">
            <Icon size="0.85rem" />
          </ThemeIcon>
          <Text size="xs" fw={600} c="dimmed" tt="uppercase" truncate style={{ letterSpacing: rem(0.6) }}>{kind}</Text>
        </Group>
        <Text size="sm" fw={600} lh={1.3} truncate>{title}</Text>
        {detail && <Text size="xs" c="dimmed" truncate>{detail}</Text>}
      </Stack>
    </Paper>
  );
}

const SOURCE_ICONS: Record<string, any> = {
  webhook: IconWorld, cron: IconSettingsAutomation, csv: IconFileSpreadsheet, kafka: IconCircles,
  rabbitmq: IconList, rabbitmq_queue: IconList,
};

const SourceNode = memo(({ data }: any) => (
  <StepCard
    color="blue"
    icon={SOURCE_ICONS[data.type] ?? IconDatabase}
    kind="Source"
    title={stepTitle(data, 'Source', data.connectionName)}
    detail={data.description || data.type}
    outbound
    vertical={data.vertical}
  />
));

const SinkNode = memo(({ data }: any) => {
  const icon = data.type === 'http' ? IconWorld
    : ['s3', 's3-parquet', 'ftp'].includes(data.type) ? IconCloud
    : ['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'clickhouse', 'yugabyte'].includes(data.type) ? IconDatabase
    : IconServer;
  return (
    <StepCard
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

const StepNode = memo(({ data, type }: any) => {
  const kind = kindOf(type, data);
  return (
    <StepCard
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
    withNames.forEach((n) => g.setNode(n.id, { width: 240, height: 76 }));
    edges.forEach((e) => g.setEdge(e.source, e.target));
    dagre.layout(g);
    return withNames.map((n) => {
      const p = g.node(n.id);
      return p ? { ...n, position: { x: p.x - 120, y: p.y - 38 } } : n;
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
      dagreGraph.setNode(node.id, { width: 240, height: 84 });
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
            y: nodeWithPosition.y - 42,
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
      <Group gap={8} wrap="nowrap" className="hermod-canvas-note">
        <IconEye size="0.95rem" aria-hidden />
        <Text size="sm" c="dimmed">Read only. Use Edit flow to change steps.</Text>
      </Group>
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
