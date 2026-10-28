import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowInitialization } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowInitialization'

/**
 * The store outlives the editor page, and opening a workflow replaced its
 * graph and settings but not what the last one had been showing. Workflow B
 * opened with A's live logs, source and sink status, node metrics and samples,
 * and with A's node still selected. Opening a workflow now clears them.
 */

const workflowB = (label = 'Customers') => ({
  id: 'wf-b',
  name: 'Workflow B',
  vhost: 'default',
  nodes: [{ id: 'src', type: 'source', ref_id: 'src-b', x: 0, y: 0, config: { label } }],
  edges: [],
})

const fromWorkflowA = () => ({
  name: 'Workflow A',
  logs: [{ message: 'A is running', level: 'info' } as any],
  sourceStatus: 'running',
  sinkStatuses: { 'sink-a': 'connected' },
  nodeMetrics: { src: 120 },
  nodeErrorMetrics: { src: 3 },
  nodeSamples: { src: { order_id: 7 } },
  sinkCBStatuses: { 'sink-a': 'open' },
  sinkBufferFill: { 'sink-a': 40 },
  workflowDeadLetterCount: 5,
  edgeFlows: { e1: { count: 40, at: 1, rate: 12, band: 2 as const, movedAt: 1 } },
  nodeErrorFlows: { src: { count: 3, at: 1, rate: 1, band: 1 as const, movedAt: 1 } },
  nodeBusy: { src: { in_flight: 1, since_progress_ms: 50_000 } },
  nodeHeld: { src: 1 },
  statusFeedStale: true,
  selectedNode: { id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders' } },
})

function mount(id: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderHook(() => useWorkflowInitialization(id, 'default'), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
  return client
}

describe('opening another workflow', () => {
  let current: ReturnType<typeof workflowB>

  beforeEach(() => {
    signInAs()
    current = workflowB()
    server.use(http.get('/api/workflows/wf-b', () => HttpResponse.json(current)))
    useWorkflowStore.setState(fromWorkflowA())
  })

  it("does not show the last workflow's live state or selection", async () => {
    mount('wf-b')

    await waitFor(() => expect(useWorkflowStore.getState().name).toBe('Workflow B'))
    const s = useWorkflowStore.getState()
    expect(s.logs).toEqual([])
    expect(s.sourceStatus).toBe('')
    expect(s.sinkStatuses).toEqual({})
    expect(s.nodeMetrics).toEqual({})
    expect(s.nodeErrorMetrics).toEqual({})
    expect(s.nodeSamples).toEqual({})
    expect(s.sinkCBStatuses).toEqual({})
    expect(s.sinkBufferFill).toEqual({})
    expect(s.workflowDeadLetterCount).toBe(0)
    expect(s.edgeFlows).toEqual({})
    expect(s.nodeErrorFlows).toEqual({})
    expect(s.nodeBusy).toEqual({})
    expect(s.nodeHeld).toEqual({})
    expect(s.statusFeedStale).toBe(false)
    expect(s.selectedNode).toBeNull()
  })

  it("keeps the open workflow's live state when it is reloaded from the server", async () => {
    const client = mount('wf-b')
    await waitFor(() => expect(useWorkflowStore.getState().name).toBe('Workflow B'))

    // The status socket reports B's telemetry, then B is saved elsewhere.
    act(() => useWorkflowStore.setState({ nodeMetrics: { src: 9 }, sourceStatus: 'running' }))
    current = workflowB('Customers v2')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-b'] }))

    await waitFor(() =>
      expect(useWorkflowStore.getState().nodes.find((n) => n.id === 'src')?.data.label).toBe('Customers v2')
    )
    expect(useWorkflowStore.getState().nodeMetrics).toEqual({ src: 9 })
    expect(useWorkflowStore.getState().sourceStatus).toBe('running')
  })
})
