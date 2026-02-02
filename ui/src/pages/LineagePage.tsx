import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../api'
import ReactFlow, { 
  Background, 
  Controls, 
  type Node, 
  type Edge,
  MarkerType,
  Handle,
  Position
} from 'reactflow'
import 'reactflow/dist/style.css'
import { 
  Title, 
  Text, 
  Paper, 
  Group, 
  ThemeIcon, 
  Stack,
  Loader,
  Center,
  Badge
} from '@mantine/core'
import { 
  IconDatabase, 
  IconPlug, 
  IconSubtask 
} from '@tabler/icons-react'
import { useMemo } from 'react'
import dagre from 'dagre'

interface LineageEdge {
  source_id: string
  source_name: string
  source_type: string
  sink_id: string
  sink_name: string
  sink_type: string
  workflow_id: string
  workflow_name: string
}

const CustomNode = ({ data }: any) => {
  const Icon = data.type === 'source' ? IconDatabase : data.type === 'sink' ? IconPlug : IconSubtask
  const color = data.type === 'source' ? 'blue' : data.type === 'sink' ? 'green' : 'orange'

  return (
    <Paper shadow="md" p="xs" withBorder style={{ minWidth: 150 }}>
      <Handle type="target" position={Position.Top} />
      <Group gap="xs">
        <ThemeIcon color={color} variant="light" size="sm">
          <Icon size={16} />
        </ThemeIcon>
        <Stack gap={0}>
          <Text size="xs" fw={700} truncate>{data.label}</Text>
          <Badge size="xs" variant="outline" color={color}>{data.type}</Badge>
        </Stack>
      </Group>
      <Handle type="source" position={Position.Bottom} />
    </Paper>
  )
}

const nodeTypes = {
  custom: CustomNode,
}

const getLayoutedElements = (nodes: Node[], edges: Edge[]) => {
  const dagreGraph = new dagre.graphlib.Graph()
  dagreGraph.setDefaultEdgeLabel(() => ({}))
  dagreGraph.setGraph({ rankdir: 'LR' })

  nodes.forEach((node) => {
    dagreGraph.setNode(node.id, { width: 180, height: 60 })
  })

  edges.forEach((edge) => {
    dagreGraph.setEdge(edge.source, edge.target)
  })

  dagre.layout(dagreGraph)

  nodes.forEach((node) => {
    const nodeWithPosition = dagreGraph.node(node.id)
    node.position = {
      x: nodeWithPosition.x - 90,
      y: nodeWithPosition.y - 30,
    }
  })

  return { nodes, edges }
}

export function LineagePage() {
  const { data: lineage, isLoading } = useQuery<LineageEdge[]>({
    queryKey: ['lineage'],
    queryFn: async () => {
      const res = await apiFetch('/api/infra/lineage')
      if (!res.ok) throw new Error('Failed to fetch lineage')
      return res.json()
    }
  })

  const { nodes, edges } = useMemo(() => {
    if (!lineage) return { nodes: [], edges: [] }

    const nodeMap = new Map<string, Node>()
    const edgeList: Edge[] = []

    lineage.forEach((item) => {
      // Source node
      if (!nodeMap.has(item.source_id)) {
        nodeMap.set(item.source_id, {
          id: item.source_id,
          type: 'custom',
          data: { label: item.source_name, type: 'source' },
          position: { x: 0, y: 0 }
        })
      }

      // Workflow node (as an intermediary)
      const wfNodeId = `wf-${item.workflow_id}`
      if (!nodeMap.has(wfNodeId)) {
        nodeMap.set(wfNodeId, {
          id: wfNodeId,
          type: 'custom',
          data: { label: item.workflow_name, type: 'workflow' },
          position: { x: 0, y: 0 }
        })
      }

      // Sink node
      if (!nodeMap.has(item.sink_id)) {
        nodeMap.set(item.sink_id, {
          id: item.sink_id,
          type: 'custom',
          data: { label: item.sink_name, type: 'sink' },
          position: { x: 0, y: 0 }
        })
      }

      // Edges: Source -> Workflow -> Sink
      edgeList.push({
        id: `e-${item.source_id}-${wfNodeId}`,
        source: item.source_id,
        target: wfNodeId,
        markerEnd: { type: MarkerType.ArrowClosed },
        animated: true
      })

      edgeList.push({
        id: `e-${wfNodeId}-${item.sink_id}`,
        source: wfNodeId,
        target: item.sink_id,
        markerEnd: { type: MarkerType.ArrowClosed },
        animated: true
      })
    })

    return getLayoutedElements(Array.from(nodeMap.values()), edgeList)
  }, [lineage])

  if (isLoading) {
    return (
      <Center h="100vh">
        <Loader size="xl" />
      </Center>
    )
  }

  return (
    <Stack h="calc(100vh - 80px)">
      <Title order={2}>Global Data Lineage</Title>
      <Text size="sm" c="dimmed">
        Visualization of data flow from sources to sinks across all workflows.
      </Text>
      
      <Paper withBorder style={{ flex: 1, overflow: 'hidden' }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
        >
          <Background color="#aaa" gap={20} />
          <Controls />
        </ReactFlow>
      </Paper>
    </Stack>
  )
}
