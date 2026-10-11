import { memo, useMemo } from 'react'
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from 'dagre'
import { Badge, Group, Stack, Text, ThemeIcon, UnstyledButton, useComputedColorScheme } from '@mantine/core'
import {
  IconBrain, IconDatabase, IconFile, IconMessages, IconPlug, IconSearch, IconSubtask, IconTable, IconWorld,
} from '@tabler/icons-react'
import type { LineageGraph, LineageNode } from '@/lib/lineage'

const NODE_W = 200
const NODE_H = 56

type Tone = 'root' | 'impacted' | 'dim' | 'plain'

interface AssetData extends Record<string, unknown> {
  node: LineageNode
  tone: Tone
  onPick: (id: string) => void
}

/** The icon of an asset kind. */
function kindIcon(kind: string) {
  switch (kind) {
    case 'workflow': return <IconSubtask size={16} />
    case 'table': case 'database': case 'collection': return <IconTable size={16} />
    case 'topic': case 'stream': case 'queue': case 'subject': case 'channel': return <IconMessages size={16} />
    case 'index': return <IconSearch size={16} />
    case 'file': case 'object': case 'sheet': return <IconFile size={16} />
    case 'endpoint': case 'entity': case 'process': return <IconWorld size={16} />
    case 'model': case 'dataset': return <IconBrain size={16} />
    case 'connector': return <IconPlug size={16} />
    default: return <IconDatabase size={16} />
  }
}

const kindColor = (kind: string) => (kind === 'workflow' ? 'orange' : kind === 'model' || kind === 'dataset' ? 'grape' : kind === 'connector' ? 'gray' : 'blue')

/** How an asset reads in a sentence: "postgres table", "workflow". */
export const kindWords = (n: LineageNode) => (n.kind === 'workflow' ? 'workflow' : [n.system, n.kind].filter(Boolean).join(' '))

const toneBorder: Record<Tone, string> = {
  root: '2px solid var(--mantine-color-red-filled)',
  impacted: '2px solid var(--mantine-color-yellow-filled)',
  dim: '1px solid var(--mantine-color-default-border)',
  plain: '1px solid var(--mantine-color-default-border)',
}

/**
 * One asset or workflow. The whole card is a button, so the graph can be
 * walked with Tab and an asset chosen with Enter or Space; React Flow's own
 * node focus is turned off so there is one tab stop per node.
 */
const AssetNode = memo(function AssetNode({ data }: NodeProps<Node<AssetData>>) {
  const { node, tone, onPick } = data
  const icon = kindIcon(node.kind)
  const color = kindColor(node.kind)
  return (
    <>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <UnstyledButton
        // nopan: pressing a node chooses it rather than starting a pan.
        className="nodrag nopan"
        onClick={() => onPick(node.id)}
        aria-label={`Show what depends on ${node.label} (${kindWords(node)})`}
        aria-pressed={tone === 'root'}
        title={node.locator ? `${node.locator}` : undefined}
        style={{
          width: NODE_W,
          minHeight: NODE_H,
          padding: '6px 10px',
          borderRadius: 8,
          background: 'var(--mantine-color-body)',
          border: toneBorder[tone],
          opacity: tone === 'dim' ? 0.45 : 1,
          boxShadow: 'var(--mantine-shadow-xs)',
        }}
      >
        <Group gap={8} wrap="nowrap">
          <ThemeIcon color={color} variant="light" size="md" radius="sm" aria-hidden>
            {icon}
          </ThemeIcon>
          <Stack gap={0} style={{ minWidth: 0 }}>
            <Text size="xs" fw={700} truncate>{node.label}</Text>
            <Group gap={4} wrap="nowrap">
              <Badge size="xs" variant="outline" color={color} tt="none">{kindWords(node)}</Badge>
              {node.kind === 'workflow' && node.active === false && <Badge size="xs" variant="light" color="gray">stopped</Badge>}
            </Group>
          </Stack>
        </Group>
      </UnstyledButton>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </>
  )
})

const nodeTypes = { asset: AssetNode }

function layout(graph: LineageGraph): Map<string, { x: number; y: number }> {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 24, ranksep: 70 })
  graph.nodes.forEach((n) => g.setNode(n.id, { width: NODE_W, height: NODE_H }))
  graph.edges.forEach((e) => g.setEdge(e.from, e.to))
  dagre.layout(g)
  const out = new Map<string, { x: number; y: number }>()
  graph.nodes.forEach((n) => {
    const p = g.node(n.id)
    out.set(n.id, { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 })
  })
  return out
}

interface Props {
  graph: LineageGraph
  selected: string | null
  /** The root and everything downstream of it, when an impact is shown. */
  impacted: Set<string> | null
  onPick: (id: string) => void
}

/** The lineage graph, laid out left to right the way data flows. */
export function LineageGraphView({ graph, selected, impacted, onPick }: Props) {
  const colorMode = useComputedColorScheme('dark')
  const positions = useMemo(() => layout(graph), [graph])

  const nodes = useMemo<Node<AssetData>[]>(() => graph.nodes.map((n) => {
    let tone: Tone = 'plain'
    if (impacted) tone = n.id === selected ? 'root' : impacted.has(n.id) ? 'impacted' : 'dim'
    // A fixed size: the layout already assumed it, and a node with a known
    // size is drawn without waiting to be measured.
    return {
      id: n.id, type: 'asset', position: positions.get(n.id) ?? { x: 0, y: 0 },
      width: NODE_W, height: NODE_H, data: { node: n, tone, onPick },
    }
  }), [graph, positions, impacted, selected, onPick])

  const edges = useMemo<Edge[]>(() => graph.edges.map((e, i) => {
    const hot = !!impacted && impacted.has(e.from) && impacted.has(e.to)
    return {
      id: `${i}:${e.from}->${e.to}:${e.relation}`,
      source: e.from,
      target: e.to,
      animated: hot,
      label: e.relation === 'reads' || e.relation === 'writes' ? undefined : e.relation.replace('_', ' '),
      markerEnd: { type: MarkerType.ArrowClosed },
      style: hot
        ? { stroke: 'var(--mantine-color-yellow-filled)', strokeWidth: 2 }
        : { opacity: impacted ? 0.35 : 1 },
    }
  }), [graph, impacted])

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      colorMode={colorMode}
      nodesDraggable={false}
      nodesConnectable={false}
      nodesFocusable={false}
      edgesFocusable={false}
      elementsSelectable={false}
      // Without a click handler React Flow sets pointer-events: none on
      // nodes that cannot be dragged or selected, and the button inside
      // could not be pressed with a mouse.
      onNodeClick={(_, n) => onPick(n.id)}
      fitView
      minZoom={0.2}
      aria-label="Lineage graph"
    >
      <Background gap={20} />
      <Controls showInteractive={false} />
    </ReactFlow>
  )
}
