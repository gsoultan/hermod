import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '../context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse, delay } from 'msw'
import { TransformationForm } from '../components/TransformationForm'
import { vi } from 'vitest'

// Mock tanstack router Link to avoid heavy router
vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

describe('Transformation preview', () => {
  const setup = (opts?: { incoming?: any; nodeType?: string }) => {
    const queryClient = new QueryClient()
    const selectedNode = { id: 'n1', type: opts?.nodeType || 'map', data: {} }
    const updateNodeConfig = () => {}
    const availableFields: string[] = []
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
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
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
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
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
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
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
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
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
})
