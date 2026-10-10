import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { WorkflowDetailPage } from '@/pages/workflows/WorkflowDetailPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to: _to, params: _params, ...rest }: any) => <a {...rest}>{children}</a>,
  useParams: () => ({ id: 'wf-1' }),
  useNavigate: () => () => {},
}))
// The canvas and the debugger are not what is under test, and both are heavy.
vi.mock('@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas', () => ({ DetailFlowCanvas: () => null }))
vi.mock('@/pages/workflows/WorkflowDebugger', () => ({ WorkflowDebugger: () => null }))

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

const run = {
  run_id: 'run-1', workflow_id: 'wf-1', started_at: '2026-10-09T10:00:00Z', duration_ms: 5, status: 'succeeded',
  step_count: 1, error_count: 0, ai: { calls: 0, input_tokens: 0, output_tokens: 0 },
  steps: [{ node_id: 'src', node_type: 'source', timestamp: '2026-10-09T10:00:00Z', duration_ms: 5 }],
}

function useWorkflow() {
  server.use(
    http.get('/api/workflows/wf-1', () =>
      HttpResponse.json({ id: 'wf-1', name: 'Orders', vhost: 'default', active: false, nodes: [{ id: 'src', type: 'source', config: {} }], edges: [] }),
    ),
    http.get('/api/workflows/wf-1/executions', () => HttpResponse.json({ executions: [run] })),
    http.get('/api/workflows/wf-1/executions/run-1', () => HttpResponse.json(run)),
    http.get('/api/workflows/wf-1/proposals', () => HttpResponse.json({ data: [] })),
  )
}

describe('Workflow detail: Executions tab', () => {
  it('shows the run history and lets an editor replay', async () => {
    signInAs('Editor')
    useWorkflow()
    const user = userEvent.setup()
    renderDetailPage()

    await user.click(await screen.findByRole('tab', { name: /executions/i }))
    await user.click(await screen.findByRole('button', { name: /open run run-1/i }))
    expect(await screen.findByRole('button', { name: /^replay/i })).toBeInTheDocument()
  })

  it('does not offer Replay to a Viewer', async () => {
    signInAs('Viewer')
    useWorkflow()
    const user = userEvent.setup()
    renderDetailPage()

    await user.click(await screen.findByRole('tab', { name: /executions/i }))
    await user.click(await screen.findByRole('button', { name: /open run run-1/i }))
    await screen.findByRole('heading', { name: 'Run' })
    expect(screen.queryByRole('button', { name: /^replay/i })).toBeNull()
  })
})
