import { render, act, renderHook } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Position, type EdgeProps } from '@xyflow/react'
import type * as XYFlow from '@xyflow/react'
import { vi } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { LiveEdge } from '@/pages/workflows/WorkflowEditor/components/LiveEdge'
import { useWorkflowWebSockets } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowWebSockets'

// An edge draws one BaseEdge per render, so counting BaseEdge's renders counts
// the edge's. The label portal needs a mounted canvas; here it renders inline.
const counts = vi.hoisted(() => ({ base: {} as Record<string, number> }))
vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof XYFlow>()
  return {
    ...actual,
    BaseEdge: (props: { id: string }) => {
      counts.base[props.id] = (counts.base[props.id] ?? 0) + 1
      return null
    },
    EdgeLabelRenderer: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  }
})

/**
 * Each LiveEdge subscribed to the whole nodeMetrics, edgeThroughput and
 * nodeSamples maps, so a telemetry change on any node re-rendered every edge
 * in the graph. An edge now reads only its own entries.
 */

const edgeProps = (id: string, source: string, target: string): EdgeProps =>
  ({
    id,
    source,
    target,
    sourceX: 0,
    sourceY: 0,
    targetX: 100,
    targetY: 0,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
  }) as unknown as EdgeProps

const T = 1_793_000_000_000
const flowAt = (rate: number) => ({ count: rate, at: T, rate, band: (rate >= 100 ? 3 : rate >= 10 ? 2 : 1) as 1 | 2 | 3, movedAt: T })

describe('LiveEdge telemetry subscriptions', () => {
  beforeEach(() => {
    counts.base = {}
    useWorkflowStore.setState({
      workflowStatus: 'running',
      statusFeedStale: false,
      nodes: [],
      nodeMetrics: { a: 1, c: 1 },
      edgeFlows: { ab: flowAt(5), cd: flowAt(5) },
      nodeSamples: { a: { id: 1 }, c: { id: 2 } },
      testResults: null,
      pulseEnabled: true,
    })
  })

  const renderEdges = () =>
    render(
      <MantineProvider>
        <svg>
          <LiveEdge {...edgeProps('ab', 'a', 'b')} />
          <LiveEdge {...edgeProps('cd', 'c', 'd')} />
        </svg>
      </MantineProvider>
    )

  it("does not re-render for another node's telemetry", () => {
    renderEdges()
    const before = counts.base.ab

    act(() => useWorkflowStore.setState((s) => ({ nodeMetrics: { ...s.nodeMetrics, c: 9 } })))
    act(() => useWorkflowStore.setState((s) => ({ edgeFlows: { ...s.edgeFlows, cd: flowAt(500) } })))
    act(() => useWorkflowStore.setState((s) => ({ nodeSamples: { ...s.nodeSamples, c: { id: 3 } } })))

    expect(counts.base.ab).toBe(before)
  })

  it('re-renders when its own flow changes what it shows', () => {
    renderEdges()
    const before = counts.base.ab

    act(() => useWorkflowStore.setState((s) => ({ edgeFlows: { ...s.edgeFlows, ab: flowAt(150) } })))

    expect(counts.base.ab).toBeGreaterThan(before)
  })

  it('does not re-render for a frame that leaves its speed and rate as shown', () => {
    renderEdges()
    const before = counts.base.ab

    // 5.2/s and 5.4/s both read "5/s" at the same speed.
    act(() => useWorkflowStore.setState((s) => ({ edgeFlows: { ...s.edgeFlows, ab: { ...flowAt(5.2), at: T + 1000 } } })))
    act(() => useWorkflowStore.setState((s) => ({ edgeFlows: { ...s.edgeFlows, ab: { ...flowAt(5.4), at: T + 2000 } } })))

    expect(counts.base.ab).toBe(before)
  })
})

/**
 * The status socket replaced each telemetry map whenever its JSON differed,
 * after serialising both maps on every frame. It now keeps every entry that
 * did not change, so an edge reading one entry is not handed a new one.
 */
describe('status socket telemetry', () => {
  const sockets: FakeSocket[] = []
  class FakeSocket {
    onmessage: ((e: MessageEvent) => void) | null = null
    onopen: (() => void) | null = null
    onclose: (() => void) | null = null
    onerror: (() => void) | null = null
    constructor(public url: string) {
      sockets.push(this)
    }
    close() {}
  }
  beforeEach(() => {
    sockets.length = 0
    vi.stubGlobal('WebSocket', FakeSocket)
    useWorkflowStore.setState({ nodeMetrics: {}, edgeFlows: {}, nodeSamples: {} })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const send = (frame: unknown) => {
    const status = sockets.find((s) => s.url.includes('/ws/status'))!
    act(() => status.onmessage!({ data: JSON.stringify(frame) } as MessageEvent))
  }

  it("keeps an unchanged node's entries when another node's changes", () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))

    send({ workflow_id: 'wf-1', node_samples: { a: { id: 1 }, c: { id: 2 } }, node_metrics: { a: 1, c: 1 } })
    const first = useWorkflowStore.getState()
    const sampleA = first.nodeSamples.a
    const metrics = first.nodeMetrics

    send({ workflow_id: 'wf-1', node_samples: { a: { id: 1 }, c: { id: 3 } }, node_metrics: { a: 1, c: 1 } })
    const second = useWorkflowStore.getState()

    expect(second.nodeSamples.a).toBe(sampleA)
    expect(second.nodeSamples.c).toEqual({ id: 3 })
    // Nothing in the metrics changed, so the map itself is the one before.
    expect(second.nodeMetrics).toBe(metrics)
  })

  it('drops an entry the server no longer reports', () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))

    send({ workflow_id: 'wf-1', node_metrics: { a: 1, c: 1 } })
    send({ workflow_id: 'wf-1', node_metrics: { a: 1 } })

    expect(useWorkflowStore.getState().nodeMetrics).toEqual({ a: 1 })
  })
})
