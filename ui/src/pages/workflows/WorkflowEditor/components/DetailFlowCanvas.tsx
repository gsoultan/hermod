import { useCallback } from 'react';
import { 
  ReactFlow, 
  Background, 
  Controls, 
  MiniMap,
  type Node,
  type Edge
} from '@xyflow/react';
import { useMantineColorScheme, Box, ActionIcon, Tooltip } from '@mantine/core';
import { IconLayoutGrid } from '@tabler/icons-react';
import dagre from 'dagre';

// Reusing Node components from WorkflowDetailPage logic or similar
import { useMantineColorScheme as useMantineTheme } from '@mantine/core';
import { Paper, Stack, Group, ThemeIcon, Text, Badge } from '@mantine/core';
import { Position, Handle } from '@xyflow/react';
import { 
  IconWorld, IconSettingsAutomation, IconFileSpreadsheet, 
  IconCircles, IconList, IconDatabase, IconGitBranch, IconVariable, IconServer, IconCloud 
} from '@tabler/icons-react';

const SourceNode = ({ data }: any) => {
  const { colorScheme } = useMantineTheme();
  const isDark = colorScheme === 'dark';

  const getIcon = () => {
    if (data.type === 'webhook') return <IconWorld size="0.8rem" />;
    if (data.type === 'cron') return <IconSettingsAutomation size="0.8rem" />;
    if (data.type === 'csv') return <IconFileSpreadsheet size="0.8rem" />;
    if (data.type === 'kafka') return <IconCircles size="0.8rem" />;
    if (data.type === 'rabbitmq' || data.type === 'rabbitmq_queue') return <IconList size="0.8rem" />;
    if (data.type === 'redis') return <IconDatabase size="0.8rem" />;
    return <IconDatabase size="0.8rem" />;
  };

  const getColor = () => {
    if (data.type === 'webhook') return 'cyan';
    if (data.type === 'cron') return 'indigo';
    if (data.type === 'csv') return 'orange';
    if (data.type === 'kafka' || data.type === 'rabbitmq' || data.type === 'rabbitmq_queue' || data.type === 'nats') return 'red';
    if (data.type === 'redis' || data.type === 'pulsar' || data.type === 'kinesis') return 'grape';
    if (['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'db2'].includes(data.type)) return 'teal';
    return 'blue';
  };

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-${getColor()}-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
      }}
    >
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color={getColor()} variant="light" size="sm">
               {getIcon()}
            </ThemeIcon>
            <Text size="xs" fw={700} c={getColor()}>TRIGGER</Text>
          </Group>
          {data.type && <Badge size="xs" variant="light">{data.type}</Badge>}
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const TransformationNode = ({ data }: any) => {
  const { colorScheme } = useMantineTheme();
  const isDark = colorScheme === 'dark';

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-violet-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
      }}
    >
      <Handle type="target" position={Position.Left} style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color="violet" variant="light" size="sm">
              {data.isGroup ? <IconGitBranch size="0.8rem" /> : <IconVariable size="0.8rem" />}
            </ThemeIcon>
            <Text size="xs" fw={700} c="violet">{data.isGroup ? 'GROUP' : 'TRANSFORM'}</Text>
          </Group>
          <Badge size="xs" variant="light" color="violet">{data.type || 'map'}</Badge>
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const SinkNode = ({ data }: any) => {
  const { colorScheme } = useMantineTheme();
  const isDark = colorScheme === 'dark';

  const getIcon = () => {
    if (data.type === 'http') return <IconWorld size="0.8rem" />;
    if (['postgres', 'mysql', 'mssql', 'mongodb', 'sqlite', 'mariadb', 'oracle', 'clickhouse', 'yugabyte'].includes(data.type)) return <IconDatabase size="0.8rem" />;
    if (['s3', 's3-parquet', 'ftp'].includes(data.type)) return <IconCloud size="0.8rem" />;
    return <IconServer size="0.8rem" />;
  };

  return (
    <Paper 
      withBorder 
      p={8} 
      radius="md" 
      shadow="sm"
      style={{ 
        minWidth: 140, 
        maxWidth: 220,
        borderLeft: `4px solid var(--mantine-color-green-6)`,
        backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
      }}
    >
      <Handle type="target" position={Position.Left} style={{ visibility: 'hidden' }} />
      <Stack gap={2}>
        <Group justify="space-between">
          <Group gap={4}>
            <ThemeIcon color="green" variant="light" size="sm">
               {getIcon()}
            </ThemeIcon>
            <Text size="xs" fw={700} c="green.7">SINK</Text>
          </Group>
          {data.type && <Badge size="xs" variant="light" color="green">{data.type}</Badge>}
        </Group>
        <Text size="sm" fw={600} truncate>{data.label || 'Untitled'}</Text>
      </Stack>
    </Paper>
  );
};

const nodeTypes = {
  source: SourceNode,
  sink: SinkNode,
  transformation: TransformationNode,
  condition: TransformationNode,
  switch: TransformationNode,
  router: TransformationNode,
  merge: TransformationNode,
  stateful: TransformationNode,
  wait: TransformationNode,
  foreach: TransformationNode,
};

interface DetailFlowCanvasProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: any;
  onEdgesChange: any;
  setNodes: (nds: any) => void;
}

export function DetailFlowCanvas({ nodes, edges, onNodesChange, onEdgesChange, setNodes }: DetailFlowCanvasProps) {
  const { colorScheme } = useMantineColorScheme();
  const isDark = colorScheme === 'dark';

  const onLayout = useCallback(() => {
    const dagreGraph = new dagre.graphlib.Graph();
    dagreGraph.setDefaultEdgeLabel(() => ({}));
    dagreGraph.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 100 });

    nodes.forEach((node) => {
      dagreGraph.setNode(node.id, { width: 140, height: 70 });
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
            x: nodeWithPosition.x - 70,
            y: nodeWithPosition.y - 35,
          },
        };
      })
    );
  }, [nodes, edges, setNodes]);

  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        fitView
      >
        <Background color={isDark ? 'var(--mantine-color-dark-4)' : 'var(--mantine-color-gray-3)'} gap={20} />
        <Controls />
        <MiniMap 
          nodeColor={(n) => {
            if (n.type === 'source') return 'var(--mantine-color-blue-6)';
            if (n.type === 'sink') return 'var(--mantine-color-green-6)';
            return 'var(--mantine-color-violet-6)';
          }}
          style={{
            backgroundColor: isDark ? 'var(--mantine-color-dark-7)' : 'var(--mantine-color-body)',
          }}
        />
      </ReactFlow>
      <Box style={{ position: 'absolute', right: 10, top: 10, zIndex: 5 }}>
        <Tooltip label="Smart Align">
          <ActionIcon aria-label="Smart align" variant="light" size="lg" color="indigo" onClick={onLayout}>
            <IconLayoutGrid size="1.2rem" />
          </ActionIcon>
        </Tooltip>
      </Box>
    </div>
  );
}
