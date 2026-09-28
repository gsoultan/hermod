import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { server } from '../test/setupTests'
import { TransformationForm } from '@/components/forms/TransformationForm'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

// Test API Call reported through a toast that closed in seconds, and a refused
// request raised two of them -- apiFetch's own and the form's. The reason a
// request was refused, which now names the tokens that went out empty, is the
// longest text the form produces, so it stays on screen until the next test.

const node = {
  id: 'lookup',
  type: 'transformation',
  data: {
    transType: 'api_lookup',
    method: 'POST',
    url: 'https://sessions.example.test/v1/sessions',
    body: '{"created_by_id": "{{.after.user_id}}"}',
    targetField: 'session',
  },
}
const incoming = { after: { user_id: '07581be3-9ecd-5da5-865e-34ab8aae1fec' }, operation: 'snapshot' }

function renderForm(props: Record<string, unknown> = {}) {
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient()}>
        <VHostProvider>
          <TransformationForm
            selectedNode={node as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={incoming}
            sinkSchema={{}}
            {...props}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

describe('api_lookup Test API Call outcome', () => {
  it('keeps a refusal on screen, with the tokens that went out empty', async () => {
    const error =
      'Failed to test transformation: api lookup failed: api lookup returned status 400: ' +
      '{"code":"invalid_argument","message":"invalid request body"}; ' +
      'these tokens had no value and were sent empty: {{.after.user_id}}'
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ error }, { status: 500 })))
    renderForm()

    fireEvent.click(screen.getByRole('button', { name: /test api call/i }))

    const result = await screen.findByTestId('api-lookup-test-result')
    expect(result).toHaveTextContent(/last test failed/i)
    expect(result).toHaveTextContent('these tokens had no value and were sent empty: {{.after.user_id}}')
    // Once, not twice: the form reports it, apiFetch does not add a second toast.
    expect(screen.queryAllByText(/request failed/i)).toHaveLength(0)
  })

  it('shows what a successful call wrote to the target field', async () => {
    server.use(
      http.post('/api/transformations/test', () =>
        HttpResponse.json({ after: { user_id: 'u-1', session: { token: 'tok-1' } }, operation: 'snapshot' })
      )
    )
    renderForm()

    fireEvent.click(screen.getByRole('button', { name: /test api call/i }))

    const result = await screen.findByTestId('api-lookup-test-result')
    expect(result).toHaveTextContent(/last test succeeded/i)
    expect(result).toHaveTextContent('"session"')
    expect(result).toHaveTextContent('tok-1')
  })
})

// The operator's Test API Call succeeded while the refresh and Run Simulation
// failed: with no run to read from, the node's input was the source sample, and
// the node before it -- the one that broke the message -- had never run on it.
// Nothing on screen said so.
describe('input that skipped the nodes before this one', () => {
  beforeEach(() => {
    server.use(http.post('/api/transformations/test', () => HttpResponse.json(incoming)))
  })

  it('says how many nodes did not run on it, and runs them', () => {
    const onRefreshFields = vi.fn()
    renderForm({ inputSkipped: 1, onRefreshFields })

    const notice = screen.getByTestId('upstream-not-run-notice')
    // "No output", not "not run": a node that ran and failed or dropped the
    // sample leaves the same gap, and re-running it is not always the fix.
    expect(notice).toHaveTextContent(/the node before this one has no output for this sample yet/i)
    fireEvent.click(screen.getByRole('button', { name: /run it on the sample/i }))
    expect(onRefreshFields).toHaveBeenCalledTimes(1)
  })

  it('says nothing when the input is what the node before it emitted', () => {
    renderForm({ inputSkipped: 0, onRefreshFields: vi.fn() })
    expect(screen.queryByTestId('upstream-not-run-notice')).toBeNull()
  })
})
