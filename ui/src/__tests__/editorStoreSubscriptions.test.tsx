import { render, renderHook, act } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ReactFlowProvider, type Node } from '@xyflow/react'
import type { ReactNode } from 'react'
import { vi } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { WorkflowModals } from '@/pages/workflows/WorkflowEditor/components/WorkflowModals'
import { useWorkflowHotkeys } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowHotkeys'
import { useWorkflowLayout } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowLayout'
import { useWorkflowEvents } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowEvents'
import { useWorkflowMutations } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowMutations'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {} }))

// WorkflowModals renders Modals on every render of its own, so counting
// Modals' renders counts WorkflowModals'.
const counts = vi.hoisted(() => ({ modals: 0 }))
vi.mock('@/pages/workflows/WorkflowEditor/components/Modals', () => ({
  Modals: () => {
    counts.modals += 1
    return null
  },
}))
vi.mock('@/components/modals/SchemaRegistryModal', () => ({ SchemaRegistryModal: () => null }))
vi.mock('@/components/modals/WorkflowHistoryModal', () => ({ WorkflowHistoryModal: () => null }))
vi.mock('@/pages/workflows/WorkflowEditor/components/LiveStreamInspector', () => ({ LiveStreamInspector: () => null }))
vi.mock('@/pages/workflows/WorkflowEditor/components/AIGeneratorModal', () => ({ AIGeneratorModal: () => null }))
vi.mock('@/pages/workflows/WorkflowEditor/components/AIFixModal', () => ({ AIFixModal: () => null }))

/**
 * The editor's modals and hooks read the store with no selector, so each of
 * them re-rendered the whole editor on every store write: every telemetry
 * frame, every log line, every drag frame. Each now subscribes to the fields
 * it reads, and a write to anything else leaves it alone.
 */

const node = (id: string, x = 0): Node => ({ id, type: 'transformation', position: { x, y: 0 }, data: { label: id } })

// Writes that none of the subjects below read.
const unrelatedWrites = () => {
  act(() => useWorkflowStore.setState({ nodeMetrics: { a: 5 }, edgeThroughput: { 'a->b': 2 } }))
  act(() => useWorkflowStore.setState((s) => ({ logs: [{ message: 'line' } as any, ...s.logs] })))
  act(() => useWorkflowStore.setState({ nodeSamples: { a: { id: 1 } }, sourceStatus: 'running' }))
  act(() => useWorkflowStore.getState().onNodesChange([{ type: 'position', id: 'a', position: { x: 40, y: 0 } }]))
}

const flow = ({ children }: { children: ReactNode }) => <ReactFlowProvider>{children}</ReactFlowProvider>

const renderModals = () =>
  render(
    <MantineProvider>
      <WorkflowModals
        id="wf-1"
        testMutation={{ mutate: () => {}, isPending: false }}
        saveMutation={{ mutate: () => {}, isPending: false }}
        aiFixModalData={null}
        setAIFixModalData={() => {}}
        saveConfirmOpened={false}
        setSaveConfirmOpened={() => {}}
      />
    </MantineProvider>
  )

describe('editor store subscriptions', () => {
  beforeEach(() => {
    counts.modals = 0
    useWorkflowStore.setState({
      nodes: [node('a'), node('b', 200)],
      edges: [],
      logs: [],
      nodeMetrics: {},
      edgeThroughput: {},
      nodeSamples: {},
      sourceStatus: '',
      selectedNode: null,
      testResults: null,
      quickAddSource: null,
      historyOpened: false,
    })
  })

  it('WorkflowModals does not re-render for telemetry, logs or a drag', () => {
    renderModals()
    const before = counts.modals

    unrelatedWrites()

    expect(counts.modals).toBe(before)
  })

  it('WorkflowModals still re-renders for a field it shows', () => {
    renderModals()
    const before = counts.modals

    act(() => useWorkflowStore.getState().setHistoryOpened(true))

    expect(counts.modals).toBeGreaterThan(before)
  })

  it('useWorkflowHotkeys does not re-render for unrelated writes', () => {
    let renders = 0
    renderHook(() => {
      renders += 1
      useWorkflowHotkeys(() => {}, () => {})
    })
    const before = renders

    unrelatedWrites()

    expect(renders).toBe(before)
  })

  it('useWorkflowLayout does not re-render for unrelated writes', () => {
    let renders = 0
    renderHook(
      () => {
        renders += 1
        return useWorkflowLayout()
      },
      { wrapper: flow }
    )
    const before = renders

    unrelatedWrites()

    expect(renders).toBe(before)
  })

  it('useWorkflowEvents does not re-render for unrelated writes', () => {
    let renders = 0
    renderHook(
      () => {
        renders += 1
        return useWorkflowEvents({ current: null }, () => {})
      },
      { wrapper: flow }
    )
    const before = renders

    unrelatedWrites()

    expect(renders).toBe(before)
  })

  it('useWorkflowMutations does not re-render for unrelated writes', () => {
    const client = new QueryClient()
    let renders = 0
    renderHook(
      () => {
        renders += 1
        return useWorkflowMutations('wf-1', false, [], () => {})
      },
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <QueryClientProvider client={client}>{children}</QueryClientProvider>
        ),
      }
    )
    const before = renders

    unrelatedWrites()

    expect(renders).toBe(before)
  })
})

/**
 * onNodesChange and onEdgesChange run on every drag frame. Their no-op guard
 * serialised every node and edge twice, sample rows and all, to find out
 * whether the change did anything. It now compares only what a React Flow
 * change can touch.
 */
describe('onNodesChange and onEdgesChange', () => {
  let serialised = 0
  const heavy = (): Node =>
    ({
      id: 'big',
      type: 'source',
      position: { x: 0, y: 0 },
      data: {
        label: 'orders',
        lastSample: {
          toJSON() {
            serialised += 1
            return { after: { id: 1 } }
          },
        },
      },
    }) as unknown as Node

  beforeEach(() => {
    serialised = 0
    useWorkflowStore.setState({
      nodes: [heavy(), { ...node('a'), selected: false }],
      edges: [
        {
          id: 'e1',
          source: 'big',
          target: 'a',
          selected: false,
          data: {
            lastSample: {
              toJSON() {
                serialised += 1
                return 1
              },
            },
          },
        } as any,
      ],
    })
  })

  it('moves a dragged node without serialising the graph', () => {
    useWorkflowStore.getState().onNodesChange([{ type: 'position', id: 'a', position: { x: 50, y: 10 }, dragging: true }])

    const moved = useWorkflowStore.getState().nodes.find((n) => n.id === 'a')
    expect(moved?.position).toEqual({ x: 50, y: 10 })
    expect(serialised).toBe(0)
  })

  it('keeps the same nodes when a change alters nothing', () => {
    const before = useWorkflowStore.getState().nodes

    useWorkflowStore.getState().onNodesChange([{ type: 'position', id: 'a', position: { x: 0, y: 0 } }])
    useWorkflowStore.getState().onNodesChange([{ type: 'select', id: 'a', selected: false }])

    expect(useWorkflowStore.getState().nodes).toBe(before)
    expect(serialised).toBe(0)
  })

  it('keeps the same nodes when a measurement repeats', () => {
    useWorkflowStore.getState().onNodesChange([
      { type: 'dimensions', id: 'a', dimensions: { width: 100, height: 40 }, setAttributes: true },
    ])
    const before = useWorkflowStore.getState().nodes

    useWorkflowStore.getState().onNodesChange([
      { type: 'dimensions', id: 'a', dimensions: { width: 100, height: 40 }, setAttributes: true },
    ])

    expect(useWorkflowStore.getState().nodes).toBe(before)
  })

  it('selects an edge, and keeps the same edges when nothing changed', () => {
    const before = useWorkflowStore.getState().edges

    useWorkflowStore.getState().onEdgesChange([{ type: 'select', id: 'e1', selected: false }])
    expect(useWorkflowStore.getState().edges).toBe(before)

    useWorkflowStore.getState().onEdgesChange([{ type: 'select', id: 'e1', selected: true }])
    expect(useWorkflowStore.getState().edges[0].selected).toBe(true)
    expect(serialised).toBe(0)
  })
})
