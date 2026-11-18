import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { installReactFlowLayoutShims } from '../test/reactFlowCanvas'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { WorkflowDetailPage } from '@/pages/workflows/WorkflowDetailPage'
import { DetailFlowCanvas, detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// The test simulation was only in the editor: to see how a message would go
// through a saved flow, an operator had to open it for editing. The read-only
// view runs the same simulation, on the flow as saved, and draws the result on
// its own canvas the way the editor does. It changes nothing in the flow.

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to: _to, params: _params, ...rest }: any) => <a {...rest}>{children}</a>,
  useParams: () => ({ id: 'wf-1' }),
  useNavigate: () => () => {},
  useSearch: () => ({}),
}))
vi.mock('@/pages/workflows/WorkflowDebugger', () => ({ WorkflowDebugger: () => null }))

installReactFlowLayoutShims()

const saved = {
  id: 'wf-1',
  name: 'Orders',
  vhost: 'default',
  active: false,
  max_retries: 5,
  nodes: [
    { id: 'src', type: 'source', ref_id: 'conn-1', config: { label: 'Orders in' }, x: 0, y: 0 },
    { id: 'is-gold', type: 'condition', config: { label: 'Is gold?' }, x: 300, y: 0 },
    { id: 'gold', type: 'transformation', config: { label: 'Gold lane', transType: 'set' }, x: 600, y: 0 },
  ],
  edges: [
    { id: 'e-in', source_id: 'src', target_id: 'is-gold' },
    { id: 'e-true', source_id: 'is-gold', target_id: 'gold', source_handle: 'true', config: { label: 'true' } },
  ],
}

const answer = [
  { node_id: 'src', node_type: 'source', payload: { tier: 'gold' }, taken_edges: ['e-in'] },
  { node_id: 'is-gold', node_type: 'condition', payload: { tier: 'gold' }, branch: 'true', taken_edges: ['e-true'] },
  { node_id: 'gold', node_type: 'transformation', payload: { tier: 'gold', lane: 'gold' } },
]

function serve(sources: any[] = []) {
  const sent: any[] = []
  const reads = { sources: 0 }
  server.use(
    http.get('/api/workflows/wf-1', () => HttpResponse.json(saved)),
    http.get('/api/workflows/wf-1/proposals', () => HttpResponse.json({ data: [] })),
    http.get('/api/sources', () => {
      reads.sources++
      return HttpResponse.json({ data: sources, total: sources.length })
    }),
    http.post('/api/workflows/test', async ({ request }) => {
      sent.push(await request.json())
      return HttpResponse.json(answer)
    }),
  )
  return Object.assign(sent, { reads })
}

function renderDetailPage() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>
          <WorkflowDetailPage />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

afterEach(() => useWorkflowStore.setState({ testResults: null, testRunGraph: null, simReplay: null, testModalOpened: false, testInput: '{\n  "payload": "test"\n}' } as any))

describe('test simulation on the read-only flow view', () => {
  it('runs the saved flow with the input the operator gives, and draws the result', async () => {
    signInAs('Editor')
    const sent = serve()
    const user = userEvent.setup()
    renderDetailPage()

    // No source has a sample, so Test asks for the message to run.
    await user.click(await screen.findByRole('button', { name: /^test$/i }))
    const input = await screen.findByRole('textbox', { name: /input message/i })
    await user.clear(input)
    await user.click(input)
    await user.paste('{"tier": "gold"}')
    await user.click(screen.getByRole('button', { name: /^run simulation$/i }))

    await waitFor(() => expect(sent).toHaveLength(1))
    const body = sent[0]
    expect(body.message).toEqual({ tier: 'gold' })
    // The flow as saved, with what the engine needs to route it: the branch
    // an edge leaves from and the flow's own settings.
    expect(body.workflow.name).toBe('Orders')
    expect(body.workflow.max_retries).toBe(5)
    expect(body.workflow.nodes.map((n: any) => n.id)).toEqual(['src', 'is-gold', 'gold'])
    expect(body.workflow.nodes[0].ref_id).toBe('conn-1')
    expect(body.workflow.edges.find((e: any) => e.id === 'e-true').source_handle).toBe('true')

    expect(await screen.findByRole('region', { name: /simulation result/i })).toBeInTheDocument()
    expect(useWorkflowStore.getState().testResults).toEqual(answer)
  })

  it('runs straight away on a source that has a saved sample', async () => {
    signInAs('Editor')
    const sent = serve([{ id: 'conn-1', name: 'Orders db', type: 'postgres', sample: JSON.stringify({ tier: 'gold' }) }])
    const user = userEvent.setup()
    renderDetailPage()

    const test = await screen.findByRole('button', { name: /^test$/i })
    // The connections are read before Test can use their samples.
    await waitFor(() => expect(sent.reads.sources).toBeGreaterThan(0))
    await user.click(test)

    await waitFor(() => expect(sent).toHaveLength(1))
    expect(sent[0].messages).toEqual({ src: { tier: 'gold' } })
    expect(screen.queryByRole('textbox', { name: /input message/i })).toBeNull()
  })

  it('clears the result from the view', async () => {
    signInAs('Editor')
    serve()
    const user = userEvent.setup()
    renderDetailPage()
    await screen.findByRole('button', { name: /^test$/i })
    useWorkflowStore.getState().setTestResults(answer)

    await user.click(await screen.findByRole('button', { name: /clear simulation/i }))
    expect(useWorkflowStore.getState().testResults).toBeNull()
  })

  it('is not offered to a Viewer, as the server refuses one', async () => {
    signInAs('Viewer')
    serve()
    renderDetailPage()
    await screen.findByText('Orders')
    expect(screen.queryByRole('button', { name: /^test$/i })).toBeNull()
  })
})

describe('the read-only canvas after a simulation', () => {
  const wrap = (ui: React.ReactNode) =>
    render(
      <MantineProvider>
        <ReactFlowProvider>{ui}</ReactFlowProvider>
      </MantineProvider>,
    )

  it('marks what the simulation did at each step', () => {
    useWorkflowStore.setState({ testResults: [{ node_id: 'n-map', node_type: 'transformation', error: 'boom' }] } as any)
    const StepCard = detailNodeTypes.transformation as any
    const { container } = wrap(<StepCard id="n-map" type="transformation" data={{ label: 'Map order' }} />)
    expect(container.querySelector('[data-simulation="error"]')).not.toBeNull()
    expect(screen.getByText(/boom/)).toBeInTheDocument()
  })

  it('sums the run up over the flow', () => {
    useWorkflowStore.setState({ testResults: answer } as any)
    const nodes = saved.nodes.map((n) => ({ id: n.id, type: n.type, position: { x: n.x, y: n.y }, data: { ...n.config } }))
    wrap(<DetailFlowCanvas nodes={nodes} edges={[]} onNodesChange={() => {}} onEdgesChange={() => {}} setNodes={() => {}} />)
    expect(screen.getByRole('region', { name: /simulation result/i })).toBeInTheDocument()
    // The summary takes the live note's place rather than drawing over it.
    expect(screen.queryByTestId('flow-live-note')).toBeNull()
  })
})
