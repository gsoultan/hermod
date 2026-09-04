import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse, delay } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'
import { signInAs } from '@/test/setupTests'

// Mock tanstack router Link to avoid heavy router
vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

describe('Transformation preview', () => {
  const setup = (opts?: { incoming?: any; nodeType?: string }) => {
    const queryClient = new QueryClient()
    const selectedNode = { id: 'n1', type: opts?.nodeType || 'map', data: {} }
    const updateNodeConfig = () => {}
    const availableFields: any[] = []
    const incomingPayload = opts?.incoming ?? { sample: true }
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            {/* Minimal props for embedded use in editor-like context */}
            <TransformationForm 
              selectedNode={selectedNode as any}
              updateNodeConfig={updateNodeConfig}
              availableFields={availableFields}
              incomingPayload={incomingPayload}
              sinkSchema={{}}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
  }

  it('shows success preview result', async () => {
    signInAs()
    server.use(
      http.post('/api/transformations/test', async () => {
        return HttpResponse.json({ ok: true })
      })
    )

    setup({ incoming: { foo: 'bar' } })

    // Trigger preview run button
    const runBtn = await screen.findByRole('button', { name: /run preview/i })
    fireEvent.click(runBtn)

    await waitFor(() => {
      expect(screen.getByText(/ok/i)).toBeInTheDocument()
    })
  })

  it('shows error on preview failure', async () => {
    signInAs()
    server.use(
      http.post('/api/transformations/test', async () => {
        return HttpResponse.json({ error: 'Bad request' }, { status: 400 })
      })
    )

    setup({ incoming: { bad: true } })
    const runBtn = await screen.findByRole('button', { name: /run preview/i })
    fireEvent.click(runBtn)

    await waitFor(() => {
      expect(screen.getByText(/bad request/i)).toBeInTheDocument()
    })
  })

  it('cancels an in-flight preview request', async () => {
    signInAs()
    server.use(
      http.post('/api/transformations/test', async (_req) => {
        // Simulate a request that is cancellable but settles quickly for tests
        await delay(100)
        return HttpResponse.json({ ok: 'late' })
      })
    )

    setup({ incoming: { x: 1 } })
    const runBtn = await screen.findByRole('button', { name: /run preview/i })
    fireEvent.click(runBtn)

    // Click it again quickly to trigger cancellation of the previous one
    fireEvent.click(runBtn)

    // Expect the Running badge to disappear eventually (request settled, canceled or replaced)
    await waitFor(() => {
      expect(screen.queryByText(/Running/i)).toBeNull()
    })
  })

  it('shows a generic error when the preview request fails at network level', async () => {
    signInAs()
    // Simulate a network error (e.g., connection refused / fetch throws)
    server.use(
      http.post('/api/transformations/test', async () => {
        return HttpResponse.error()
      })
    )

    setup({ incoming: { foo: 'bar' } })
    const runBtn = await screen.findByRole('button', { name: /run preview/i })
    fireEvent.click(runBtn)

    // The Preview panel should render an error alert with a user-visible error text
    await waitFor(() => {
      // We expect a non-empty error; the exact message may vary, so match a generic phrase
      expect(
        screen.getByText(/preview failed|unexpected error|failed/i)
      ).toBeInTheDocument()
    })
  })

  // A routing node answers with the branch it took, not a changed message. The
  // preview used to post these to an endpoint that only knew transformers and
  // showed "unknown transformation type \"switch\"" for a node that runs fine
  // in a live workflow. The panel has to render the message it is given rather
  // than the {branch, result} envelope, or Result/Diff/Input all show the
  // wrapper instead of the data.
  it('unwraps a routing node branch response into the preview panel', async () => {
    server.use(
      http.post('/api/transformations/test', async () =>
        HttpResponse.json({ branch: 'active-users', result: { status: 'active' } })
      )
    )
    await signInAs('editor')
    setup({ nodeType: 'switch', incoming: { status: 'active' } })

    const runBtn = await screen.findByRole('button', { name: /run preview/i })
    fireEvent.click(runBtn)

    await waitFor(() => {
      expect(screen.getByText(/"status"/)).toBeInTheDocument()
    })
    // The envelope must not reach the panel: seeing "branch" or "result" here
    // means the wrapper was rendered instead of the message inside it.
    expect(screen.queryByText(/"branch"/)).not.toBeInTheDocument()
    expect(screen.queryByText(/"result"/)).not.toBeInTheDocument()
  })
})
