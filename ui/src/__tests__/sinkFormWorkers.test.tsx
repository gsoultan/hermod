import { render, screen, waitFor } from '@testing-library/react'
import { Suspense } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { SinkForm } from '@/components/forms/SinkForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

function harness() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const Wrapper = ({ children }: { children: React.ReactNode }) => (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <Suspense fallback={<div>loading</div>}>{children}</Suspense>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
  return { Wrapper, queryClient }
}

/**
 * SinkForm fetched its worker list with a bare useEffect and a dynamic import of
 * the api module: no cache, no dedupe, no abort, and a fresh request on every
 * mount — while SourceForm read the same list through React Query under the
 * `['workers']` key. Two mechanisms for one resource, one of which also set
 * state after unmount.
 */
describe('SinkForm worker list', () => {
  beforeEach(() => {
    signInAs()
  })

  it('reads workers through the shared query cache rather than refetching per mount', async () => {
    let calls = 0
    server.use(
      http.get('/api/workers', () => {
        calls += 1
        return HttpResponse.json({ data: [{ id: 'w1', name: 'worker-one' }], total: 1 })
      }),
      http.get('/api/vhosts', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 }))
    )

    const { Wrapper } = harness()
    const first = render(<SinkForm />, { wrapper: Wrapper })
    await waitFor(() => expect(calls).toBe(1))
    first.unmount()

    // Same client, so a remount inside staleTime must be served from cache.
    render(<SinkForm />, { wrapper: Wrapper })
    await screen.findByPlaceholderText('NATS Sink')
    await new Promise((r) => setTimeout(r, 100))
    expect(calls).toBe(1)
  }, 20000)

  it('renders without crashing when the worker list fails', async () => {
    server.use(
      http.get('/api/workers', () => new HttpResponse(null, { status: 500 })),
      http.get('/api/vhosts', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 }))
    )

    const { Wrapper } = harness()
    render(<SinkForm />, { wrapper: Wrapper })
    // The form is still usable; only the optional worker picker is empty.
    expect(await screen.findByPlaceholderText('NATS Sink')).toBeInTheDocument()
  }, 20000)
})
