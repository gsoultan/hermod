import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { Node, Edge } from '@xyflow/react'
import { http, HttpResponse } from 'msw'
import { notificationsStore, cleanNotifications } from '@mantine/notifications'
import { server, signInAs } from '../test/setupTests'
import { apiFetch } from '@/api'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowMutations } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowMutations'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
}))

/**
 * Refreshing AVAILABLE FIELDS on one node has to reach every node after it.
 *
 * The refresh icon stores a fresh sample on the source record, then re-runs the
 * workflow simulation so each node can read the output of the node before it.
 * Two things stood in front of that fresh sample, and a node further down the
 * branch read either one before it ever got to the sample:
 *
 *   - the source node's `lastSample`, a copy the wizard's Test Connection writes
 *     into the node and saving the workflow persists — so it outlives the
 *     table it describes, and the refresh never touched it;
 *   - the previous simulation's results, which the re-run only replaces when it
 *     succeeds. It is refused while the graph has no sink ("no sink node
 *     reachable from any source"), which is exactly while a workflow is being
 *     built.
 */

// What the source returned the day someone pressed Test Connection.
const stale = { operation: 'snapshot', after: { id: 1, retired_col: 'gone' } }
// What the source returns now.
const fresh = { operation: 'snapshot', after: { id: 1, added_col: 'new' } }

// Built the way useWorkflowInitialization hydrates a saved node: the config is
// spread flat onto data, next to ref_id.
const sourceNode = (extra: Record<string, unknown> = {}): Node =>
  ({ id: 'n-src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'orders', ref_id: 'src-1', ...extra } }) as Node
const nodeA = { id: 'n-a', type: 'transformation', position: { x: 250, y: 0 }, data: { label: 'A', transType: 'mapping' } } as Node
const nodeB = { id: 'n-b', type: 'transformation', position: { x: 500, y: 0 }, data: { label: 'B', transType: 'mapping' } } as Node
const edges = [
  { id: 'e1', source: 'n-src', target: 'n-a' },
  { id: 'e2', source: 'n-a', target: 'n-b' },
] as Edge[]

const sourceRecord = (sample: string) => ({
  id: 'src-1', name: 'orders', type: 'sqlite', vhost: 'default',
  config: { path: '/tmp/orders.db', tables: 'orders', use_cdc: 'false' },
  sample,
})

const paths = (fields: { path: string }[]) => fields.map((f) => f.path)

describe('a source node\'s own payload', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {}, testResults: null, selectedNode: null })
  })

  it('is the refreshed stored sample, not the lastSample copy saved with the workflow', () => {
    const src = sourceNode({ lastSample: stale })
    useWorkflowStore.setState({ nodes: [src, nodeA, nodeB], edges })
    const sources = [sourceRecord(JSON.stringify(fresh))]

    const next = renderHook(() => useNodeContext(nodeB, null, sources, []))
    expect(paths(next.result.current.availableFields)).toContain('added_col')
    expect(paths(next.result.current.availableFields)).not.toContain('retired_col')

    // The source node's own panel reads the same thing.
    const own = renderHook(() => useNodeContext(src, null, sources, []))
    expect(paths(own.result.current.availableFields)).toContain('added_col')
    expect(paths(own.result.current.availableFields)).not.toContain('retired_col')
  })

  it('still falls back to lastSample for a source with nothing stored', () => {
    useWorkflowStore.setState({ nodes: [sourceNode({ lastSample: stale }), nodeA, nodeB], edges })

    const { result } = renderHook(() => useNodeContext(nodeB, null, [sourceRecord('')], []))

    expect(paths(result.current.availableFields)).toContain('retired_col')
  })

  it('prefers what the running engine is emitting over the lastSample copy', () => {
    useWorkflowStore.setState({
      nodes: [sourceNode({ lastSample: stale }), nodeA, nodeB],
      edges,
      nodeSamples: { 'n-src': fresh },
    })

    const { result } = renderHook(() => useNodeContext(nodeB, null, [sourceRecord('')], []))

    expect(paths(result.current.availableFields)).toContain('added_col')
    expect(paths(result.current.availableFields)).not.toContain('retired_col')
  })
})

// The chain has to keep going past a node the preview could not run. A node
// whose predecessor errored or filtered the sample used to jump straight back
// to the source's stored sample, dropping everything the nodes in between had
// added, even though the preview had their output.
describe('a node after one the preview could not run', () => {
  it('reads the nearest output the preview did produce, not the source again', () => {
    const src = sourceNode()
    const a = { id: 'n-a', type: 'transformation', position: { x: 250, y: 0 }, data: { label: 'A', transType: 'set' } } as Node
    const failing = { id: 'n-b', type: 'transformation', position: { x: 500, y: 0 }, data: { label: 'B', transType: 'db_lookup' } } as Node
    const c = { id: 'n-c', type: 'transformation', position: { x: 750, y: 0 }, data: { label: 'C', transType: 'mapping' } } as Node
    useWorkflowStore.setState({
      nodes: [src, a, failing, c],
      edges: [
        { id: 'e1', source: 'n-src', target: 'n-a' },
        { id: 'e2', source: 'n-a', target: 'n-b' },
        { id: 'e3', source: 'n-b', target: 'n-c' },
      ] as Edge[],
      nodeSamples: {},
    })
    // The shape /api/workflows/test answers with when B fails: an error entry
    // and a filtered one, neither carrying a payload.
    const testResults = [
      { node_id: 'n-src', node_type: 'source', payload: fresh },
      { node_id: 'n-a', node_type: 'transformation', payload: { ...fresh, after: { ...fresh.after, a_tag: 'from A' } } },
      { node_id: 'n-b', node_type: 'transformation', error: 'lookup failed' },
      { node_id: 'n-b', node_type: 'transformation', filtered: true },
      { node_id: 'n-c', node_type: 'transformation', filtered: true },
    ]
    // Older than what the preview ran on, so reading it shows.
    const sources = [sourceRecord(JSON.stringify(stale))]

    const { result } = renderHook(() => useNodeContext(c, testResults, sources, []))
    const got = paths(result.current.availableFields)

    expect(got).toContain('a_tag')
    expect(got).toContain('added_col')
    expect(got).not.toContain('retired_col')
  })
})

describe('refreshing fields on one node reaches the next', () => {
  let storedSample = ''

  // The editor's own assembly: the sources query the page runs, the refresh
  // handler, and the field list of the node after the one being refreshed.
  const useEditor = () => {
    const { data: sources } = useQuery({
      queryKey: ['sources', 'default'],
      queryFn: async () => (await apiFetch('/api/sources')).json(),
    })
    const testResults = useWorkflowStore((s) => s.testResults)
    const mutations = useWorkflowMutations('wf-1', false, sources?.data, () => {})
    const next = useNodeContext(nodeB, testResults, sources?.data || [], [])
    return { sources, mutations, next }
  }

  const render = () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    return renderHook(() => useEditor(), { wrapper })
  }

  const refreshFrom = async (result: { current: ReturnType<typeof useEditor> }) => {
    await act(async () => {
      await result.current.mutations.handleRefreshFields()
    })
    await waitFor(() => expect(result.current.mutations.testMutation.isIdle).toBe(false))
    await waitFor(() => expect(result.current.mutations.testMutation.isPending).toBe(false))
  }

  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/sources', () => HttpResponse.json({ data: [sourceRecord(storedSample)], total: 1 })),
      http.post('/api/sources/sample', () => HttpResponse.json(fresh)),
      http.put('/api/sources/:id/sample', async ({ request }) => {
        storedSample = ((await request.json()) as { sample: string }).sample
        return HttpResponse.json({})
      }),
    )
  })

  // The registry refuses to simulate a graph with no sink, which is the state a
  // workflow is in while its nodes are still being configured.
  const simulationRefused = () =>
    server.use(
      http.post('/api/workflows/test', () =>
        HttpResponse.json({ error: 'Failed to test workflow: no sink node reachable from any source' }, { status: 500 })
      )
    )

  it('when the source node carries an older lastSample and the re-simulation is refused', async () => {
    storedSample = ''
    const src = sourceNode({ lastSample: stale })
    useWorkflowStore.setState({ nodes: [src, nodeA, nodeB], edges, nodeSamples: {}, testResults: null, selectedNode: nodeA })
    simulationRefused()

    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())
    expect(paths(result.current.next.availableFields), 'starting state').toContain('retired_col')

    await refreshFrom(result)

    expect(result.current.mutations.testMutation.isError).toBe(true)
    expect(storedSample, 'the refresh stored the new sample').toContain('added_col')
    expect(paths(result.current.next.availableFields)).toContain('added_col')
    expect(paths(result.current.next.availableFields)).not.toContain('retired_col')
  })

  it('when an earlier simulation ran on the old sample and the re-simulation is refused', async () => {
    storedSample = JSON.stringify(stale)
    useWorkflowStore.setState({
      nodes: [sourceNode(), nodeA, nodeB],
      edges,
      nodeSamples: {},
      selectedNode: nodeA,
      // What the toolbar's Test left behind, run against the old sample.
      testResults: [
        { node_id: 'n-src', node_type: 'source', payload: stale },
        { node_id: 'n-a', node_type: 'transformation', payload: stale },
      ],
    })
    simulationRefused()

    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())
    expect(paths(result.current.next.availableFields), 'starting state').toContain('retired_col')

    await refreshFrom(result)

    expect(result.current.mutations.testMutation.isError).toBe(true)
    expect(paths(result.current.next.availableFields)).toContain('added_col')
    expect(paths(result.current.next.availableFields)).not.toContain('retired_col')
  })

  it('shows the next node what the refreshed node emits when the re-simulation runs', async () => {
    storedSample = JSON.stringify(stale)
    useWorkflowStore.setState({ nodes: [sourceNode(), nodeA, nodeB], edges, nodeSamples: {}, testResults: null, selectedNode: nodeA })
    // Node A's output: the sample plus a column only A produces, so the next
    // node can only offer it by reading A's simulated output.
    const emittedByA = { ...fresh, after: { ...fresh.after, enriched_col: 'from A' } }
    server.use(
      http.post('/api/workflows/test', () =>
        HttpResponse.json([
          { node_id: 'n-src', node_type: 'source', payload: fresh },
          { node_id: 'n-a', node_type: 'transformation', payload: emittedByA },
        ])
      )
    )

    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await refreshFrom(result)

    expect(result.current.mutations.testMutation.isSuccess).toBe(true)
    expect(paths(result.current.next.availableFields)).toContain('enriched_col')
    expect(paths(result.current.next.availableFields)).toContain('added_col')
  })
})

/**
 * What a refresh sends to the simulation, and what it tells the user.
 *
 * The simulation used to receive one message and give it to every source node,
 * so on a workflow with two sources a refresh on one branch showed its columns
 * on the other; when the capture failed it re-ran with the first source in the
 * workflow, whichever branch was open. And a refused re-run surfaced as two red
 * toasts after a refresh that had worked.
 */
describe('the simulation a refresh runs', () => {
  const ordersOld = { operation: 'snapshot', after: { order_id: 7 } }
  const customersOld = { operation: 'snapshot', after: { email: 'old@example.com' } }
  const customersFresh = { operation: 'snapshot', after: { email: 'new@example.com', tier: 'gold' } }

  const s1 = { id: 'n-s1', type: 'source', position: { x: 0, y: 0 }, data: { label: 'orders', ref_id: 'orders' } } as Node
  const s2 = { id: 'n-s2', type: 'source', position: { x: 0, y: 200 }, data: { label: 'customers', ref_id: 'customers' } } as Node
  const a = { id: 'n-a', type: 'transformation', position: { x: 250, y: 0 }, data: { label: 'A', transType: 'mapping' } } as Node
  const b = { id: 'n-b', type: 'transformation', position: { x: 250, y: 200 }, data: { label: 'B', transType: 'mapping' } } as Node
  const twoBranches = [
    { id: 'e1', source: 'n-s1', target: 'n-a' },
    { id: 'e2', source: 'n-s2', target: 'n-b' },
  ] as Edge[]

  let bodies: any[] = []

  const record = (sampleOk: boolean, simulate: () => Response) => {
    server.use(
      http.get('/api/sources', () => HttpResponse.json({
        data: [
          { id: 'orders', name: 'orders', type: 'sqlite', config: { tables: 'orders' }, sample: JSON.stringify(ordersOld) },
          { id: 'customers', name: 'customers', type: 'sqlite', config: { tables: 'customers' }, sample: JSON.stringify(customersOld) },
        ],
        total: 2,
      })),
      http.post('/api/sources/sample', () => sampleOk
        ? HttpResponse.json(customersFresh)
        : HttpResponse.json({ error: 'dial tcp: connection refused' }, { status: 500 })),
      http.put('/api/sources/:id/sample', () => HttpResponse.json({})),
      http.post('/api/workflows/test', async ({ request }) => {
        bodies.push(await request.json())
        return simulate()
      }),
    )
  }

  const render = () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    return renderHook(() => {
      const { data: sources } = useQuery({
        queryKey: ['sources', 'default'],
        queryFn: async () => (await apiFetch('/api/sources')).json(),
      })
      return { sources, mutations: useWorkflowMutations('wf-1', false, sources?.data, () => {}) }
    }, { wrapper })
  }

  const settle = async (result: { current: { mutations: ReturnType<typeof useWorkflowMutations> } }) => {
    await waitFor(() => expect(bodies.length).toBeGreaterThan(0))
    await waitFor(() => expect(result.current.mutations.testMutation.isPending).toBe(false))
  }

  // What is on screen, once every update has landed.
  const visible = () => {
    const { notifications, queue } = notificationsStore.getState()
    return [...notifications, ...queue]
  }

  beforeEach(() => {
    signInAs()
    bodies = []
    cleanNotifications()
    useWorkflowStore.setState({
      nodes: [s1, s2, a, b], edges: twoBranches, nodeSamples: {}, testResults: null, selectedNode: b,
      testInput: '{\n  "payload": "test"\n}',
    })
  })

  it('seeds each branch from its own source, and asks for a partial preview', async () => {
    record(true, () => HttpResponse.json([]))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    expect(bodies[0].messages).toEqual({ 'n-s1': ordersOld, 'n-s2': customersFresh })
    expect(bodies[0].partial).toBe(true)
    // No shared message: a source with no sample of its own must not be handed
    // this branch's data.
    expect(bodies[0].message).toBeUndefined()
  })

  it('re-runs each branch with its own last sample when the capture fails', async () => {
    record(false, () => HttpResponse.json([]))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    expect(bodies[0].messages).toEqual({ 'n-s1': ordersOld, 'n-s2': customersOld })
  })

  it('reports a refused preview once, in the refresh notification', async () => {
    const reason = 'cycle detected at node B (n-b)'
    record(true, () => HttpResponse.json({ error: `Failed to test workflow: ${reason}` }, { status: 500 }))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    const shown = visible()
    expect(shown.filter((n) => String(n.id).startsWith('error-')), 'apiFetch toast').toEqual([])
    expect(shown.filter((n) => n.title === 'Test Failed'), 'Test Failed toast').toEqual([])
    const refresh = shown.find((n) => n.id === 'refresh-fields')
    expect(String(refresh?.message)).toContain(reason)
    expect(refresh?.loading).toBeFalsy()
  })

  // A node that fails on the new sample is where the chain stops: every node
  // after it can only show what reached it. A green "Fields refreshed" said
  // nothing of the sort.
  it('names a node that failed in the preview', async () => {
    record(true, () => HttpResponse.json([
      { node_id: 'n-s2', node_type: 'source', payload: customersFresh },
      { node_id: 'n-b', node_type: 'transformation', error: 'lookup failed: no row' },
      { node_id: 'n-b', node_type: 'transformation', filtered: true },
    ]))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    const refresh = visible().find((n) => n.id === 'refresh-fields')
    expect(refresh?.color).toBe('orange')
    expect(String(refresh?.message)).toContain('B: lookup failed: no row')
  })

  // Nothing stored and nothing fetched: there is no "last one" to show.
  it('does not claim a last sample the branch never had', async () => {
    record(false, () => HttpResponse.json([]))
    server.use(http.get('/api/sources', () => HttpResponse.json({
      data: [
        { id: 'orders', name: 'orders', type: 'sqlite', config: { tables: 'orders' }, sample: JSON.stringify(ordersOld) },
        { id: 'customers', name: 'customers', type: 'sqlite', config: { tables: 'customers' }, sample: '' },
      ],
      total: 2,
    })))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    const refresh = visible().find((n) => n.id === 'refresh-fields')
    expect(refresh?.title).toBe('Could not refresh fields')
    expect(String(refresh?.message)).toContain('connection refused')
    expect(String(refresh?.message)).not.toContain('last one')
  })

  // A node wired to no source has nothing to sample, so the refresh re-runs
  // the samples the workflow already has — and must not claim a new one.
  it('does not claim a new sample when it had no source to fetch from', async () => {
    let sampled = 0
    record(true, () => HttpResponse.json([]))
    server.use(http.post('/api/sources/sample', () => { sampled += 1; return HttpResponse.json(customersFresh) }))
    const loose = { id: 'n-x', type: 'transformation', position: { x: 500, y: 500 }, data: { label: 'X', transType: 'mapping' } } as Node
    useWorkflowStore.setState({ nodes: [s1, s2, a, b, loose], selectedNode: loose })
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    await act(async () => { await result.current.mutations.handleRefreshFields() })
    await settle(result)

    expect(sampled).toBe(0)
    const refresh = visible().find((n) => n.id === 'refresh-fields')
    expect(String(refresh?.message)).not.toContain('new sample')
  })

  it('lets the toolbar Test seed each source with its own sample, strictly', async () => {
    record(true, () => HttpResponse.json([]))
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    act(() => { result.current.mutations.handleTest(null) })
    await settle(result)

    expect(bodies[0].messages).toEqual({ 'n-s1': ordersOld, 'n-s2': customersOld })
    expect(bodies[0].partial).toBeFalsy()
  })

  // The Configure Test modal's Run Simulation calls mutate() with no argument,
  // and the request builder destructured it, so the button threw before a
  // request was ever sent.
  it('runs the Configure Test modal\'s input when mutate is called bare', async () => {
    record(true, () => HttpResponse.json([]))
    useWorkflowStore.setState({ testInput: '{"custom": 1}' })
    const { result } = render()
    await waitFor(() => expect(result.current.sources).toBeDefined())

    act(() => { result.current.mutations.testMutation.mutate(undefined as any) })
    await settle(result)

    expect(result.current.mutations.testMutation.isError).toBe(false)
    expect(bodies[0].message).toEqual({ custom: 1 })
  })
})
