import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server } from '../test/setupTests'
import { RunWithInputModal } from '@/pages/workflows/executions/RunWithInputModal'

const twoSources = {
  id: 'wf-1',
  nodes: [
    { id: 'hook', type: 'source', config: { label: 'Orders webhook' } },
    { id: 'cron', type: 'source', config: { label: 'Nightly batch' } },
    { id: 'out', type: 'sink', config: { label: 'Warehouse' } },
  ],
}
const oneSource = { id: 'wf-1', nodes: [{ id: 'hook', type: 'source', config: {} }, { id: 'out', type: 'sink', config: {} }] }

function renderModal(workflow: any, onOpenRun = vi.fn()) {
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
        <RunWithInputModal opened onClose={() => {}} workflow={workflow} onOpenRun={onOpenRun} />
      </QueryClientProvider>
    </MantineProvider>,
  )
  return onOpenRun
}

const payloadBox = () => screen.getByRole('textbox', { name: /input message/i })
const setPayload = (text: string) => fireEvent.change(payloadBox(), { target: { value: text } })

describe('Run with input', () => {
  it('says plainly that the run is real and its sinks write', () => {
    renderModal(oneSource)
    expect(screen.getByText(/sinks write for real/i)).toBeInTheDocument()
  })

  it('refuses a payload that is not a JSON object', async () => {
    renderModal(oneSource)
    setPayload('{"a": ')
    expect(await screen.findByText(/not valid json/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^run workflow/i })).toBeDisabled()

    setPayload('[1, 2]')
    expect(await screen.findByText(/must be a json object/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^run workflow/i })).toBeDisabled()

    setPayload('{"a": 1}')
    await waitFor(() => expect(screen.getByRole('button', { name: /^run workflow/i })).toBeEnabled())
  })

  it('offers no source picker when there is one source, and sends none', async () => {
    let body: any
    server.use(
      http.post('/api/workflows/wf-1/run', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ workflow_id: 'wf-1', run_id: 'run-9', status: 'completed', steps: [] })
      }),
    )
    const user = userEvent.setup()
    renderModal(oneSource)
    expect(screen.queryByRole('combobox', { name: /start at source/i })).toBeNull()

    setPayload('{"order_id": "A-1"}')
    await user.click(screen.getByRole('button', { name: /^run workflow/i }))
    await waitFor(() => expect(body).toEqual({ message: { order_id: 'A-1' } }))
  })

  it('runs from the chosen source and links the run in Executions', async () => {
    let body: any
    server.use(
      http.post('/api/workflows/wf-1/run', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({
          workflow_id: 'wf-1', run_id: 'run-42', status: 'failed',
          steps: [{ node_id: 'cron', duration: 1000000 }, { node_id: 'out', duration: 2000000, error: 'connection refused' }],
        })
      }),
    )
    const user = userEvent.setup()
    const onOpenRun = renderModal(twoSources)

    await user.click(screen.getByRole('combobox', { name: /start at source/i }))
    await user.click(await screen.findByRole('option', { name: 'Nightly batch', hidden: true }))
    setPayload('{"n": 1}')
    await user.click(screen.getByRole('button', { name: /^run workflow/i }))

    await waitFor(() => expect(body).toEqual({ message: { n: 1 }, source_node_id: 'cron' }))
    expect(await screen.findByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('run-42')).toBeInTheDocument()
    expect(screen.getByText(/connection refused/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /open in executions/i }))
    expect(onOpenRun).toHaveBeenCalledWith('run-42')
  })

  it('shows the server refusal where the run was asked for', async () => {
    server.use(
      http.post('/api/workflows/wf-1/run', () =>
        HttpResponse.json({ error: 'Failed to run workflow: no source node' }, { status: 400 }),
      ),
    )
    const user = userEvent.setup()
    renderModal(oneSource)
    setPayload('{}')
    await user.click(screen.getByRole('button', { name: /^run workflow/i }))
    expect(await screen.findByText(/no source node/)).toBeInTheDocument()
  })
})
