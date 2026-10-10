import { useState } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ExecutionsPanel } from '@/pages/workflows/executions/ExecutionsPanel'

const WF = 'wf-1'

function execution(overrides: Record<string, unknown> = {}) {
  return {
    run_id: 'run-new',
    workflow_id: WF,
    started_at: '2026-10-09T10:00:00Z',
    duration_ms: 1250,
    status: 'succeeded',
    step_count: 2,
    error_count: 0,
    ai: { calls: 1, input_tokens: 120, output_tokens: 30 },
    steps: [
      { node_id: 'src', node_type: 'source', label: 'Orders webhook', timestamp: '2026-10-09T10:00:00Z', duration_ms: 1, ai: { calls: 0, input_tokens: 0, output_tokens: 0 } },
      { node_id: 'ai1', node_type: 'ai_prompt', label: 'Summarise', timestamp: '2026-10-09T10:00:01Z', duration_ms: 1200, ai: { calls: 1, input_tokens: 120, output_tokens: 30 } },
    ],
    ...overrides,
  }
}

function Harness({ canReplay }: { canReplay: boolean }) {
  const [run, setRun] = useState<string | null>(null)
  return <ExecutionsPanel workflowId={WF} canReplay={canReplay} selectedRunId={run} onSelectRun={setRun} />
}

function renderPanel(canReplay = true) {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>
          <Harness canReplay={canReplay} />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Workflow executions', () => {
  it('lists runs with status, duration and token totals, and pages to older runs', async () => {
    const befores: (string | null)[] = []
    server.use(
      http.get(`/api/workflows/${WF}/executions`, ({ request }) => {
        const url = new URL(request.url)
        befores.push(url.searchParams.get('before'))
        if (!url.searchParams.get('before')) {
          return HttpResponse.json({ executions: [execution()], next_before: '2026-10-09T09:59:59.123456789Z' })
        }
        return HttpResponse.json({ executions: [execution({ run_id: 'run-old', status: 'failed', error_count: 1, ai: { calls: 0, input_tokens: 0, output_tokens: 0 } })] })
      }),
    )
    const user = userEvent.setup()
    renderPanel()

    const row = (await screen.findByText('run-new')).closest('tr') as HTMLElement
    expect(within(row).getByText('Succeeded')).toBeInTheDocument()
    expect(within(row).getByText('1.25 s')).toBeInTheDocument()
    expect(within(row).getByText('120 in · 30 out')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /older runs/i }))
    expect(await screen.findByText('run-old')).toBeInTheDocument()
    expect(befores).toContain('2026-10-09T09:59:59.123456789Z')
    // The last page has no cursor, so there is nowhere older to go.
    expect(screen.getByRole('button', { name: /older runs/i })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: /newer runs/i }))
    expect(await screen.findByText('run-new')).toBeInTheDocument()
  })

  it('shows a run with each step and its output', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/executions`, () => HttpResponse.json({ executions: [execution()] })),
      http.get(`/api/workflows/${WF}/executions/run-new`, () =>
        HttpResponse.json(
          execution({
            steps: [
              { node_id: 'src', node_type: 'source', label: 'Orders webhook', timestamp: '2026-10-09T10:00:00Z', duration_ms: 1, ai: { calls: 0, input_tokens: 0, output_tokens: 0 }, output: { order_id: 'A-17' } },
              { node_id: 'snk', node_type: 'sink', timestamp: '2026-10-09T10:00:01Z', duration_ms: 3, error: 'connection refused', ai: { calls: 0, input_tokens: 0, output_tokens: 0 } },
            ],
          }),
        ),
      ),
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: /open run run-new/i }))

    expect(await screen.findByText('Orders webhook')).toBeInTheDocument()
    expect(screen.getByText(/"order_id": "A-17"/)).toBeInTheDocument()
    expect(screen.getByText('connection refused')).toBeInTheDocument()
    // A step whose node has no label is named by its id.
    expect(screen.getByText('snk')).toBeInTheDocument()
  })

  it('hides Replay from a role that cannot run workflows', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/executions`, () => HttpResponse.json({ executions: [execution()] })),
      http.get(`/api/workflows/${WF}/executions/run-new`, () => HttpResponse.json(execution())),
    )
    const user = userEvent.setup()
    renderPanel(false)

    await user.click(await screen.findByRole('button', { name: /open run run-new/i }))
    await screen.findByText('Summarise')
    expect(screen.queryByRole('button', { name: /^replay/i })).toBeNull()
  })

  it('replays a run only after confirmation, and opens the new run', async () => {
    let replays = 0
    server.use(
      http.get(`/api/workflows/${WF}/executions`, () => HttpResponse.json({ executions: [execution()] })),
      http.get(`/api/workflows/${WF}/executions/run-new`, () => HttpResponse.json(execution())),
      http.get(`/api/workflows/${WF}/executions/run-replayed`, () => HttpResponse.json(execution({ run_id: 'run-replayed' }))),
      http.post(`/api/workflows/${WF}/executions/run-new/replay`, () => {
        replays += 1
        return HttpResponse.json({ workflow_id: WF, run_id: 'run-replayed', status: 'completed', steps: [], replay_of: 'run-new' })
      }),
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: /open run run-new/i }))
    await user.click(await screen.findByRole('button', { name: /^replay/i }))
    // The confirmation says that sinks write again.
    expect(await screen.findByText(/sinks write again/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await new Promise((r) => setTimeout(r, 30))
    expect(replays).toBe(0)

    await user.click(screen.getByRole('button', { name: /^replay/i }))
    await user.click(await screen.findByRole('button', { name: 'Replay run' }))
    await waitFor(() => expect(replays).toBe(1))
    expect(await screen.findByText(/run-replayed/, { selector: 'code' })).toBeInTheDocument()
  })

  it('explains a run that cannot be replayed', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/executions`, () => HttpResponse.json({ executions: [execution()] })),
      http.get(`/api/workflows/${WF}/executions/run-new`, () => HttpResponse.json(execution())),
      http.post(`/api/workflows/${WF}/executions/run-new/replay`, () =>
        HttpResponse.json({ error: "This run's trace has no input recorded at a source node, so it cannot be replayed" }, { status: 409 }),
      ),
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: /open run run-new/i }))
    await user.click(await screen.findByRole('button', { name: /^replay/i }))
    await user.click(await screen.findByRole('button', { name: 'Replay run' }))
    expect(await screen.findByText(/no input recorded/i)).toBeInTheDocument()
  })

  it('says so when a workflow has no runs yet', async () => {
    server.use(http.get(`/api/workflows/${WF}/executions`, () => HttpResponse.json({ executions: [] })))
    renderPanel()
    expect(await screen.findByText(/no runs recorded yet/i)).toBeInTheDocument()
  })
})
