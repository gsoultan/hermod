import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Suspense, type ReactNode } from 'react'
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

/**
 * A sink is often the node right after a transformation, and its mapping reads
 * the fields that arrive there — but unlike every other node it had no refresh
 * control. SinkForm declared `onRefreshFields` and `isRefreshing` and never
 * rendered either, so a sink's fields could only be refreshed from a node
 * upstream of it.
 */
function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <Suspense fallback={<div>loading</div>}>{children}</Suspense>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

describe('SinkForm field refresh', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 }))
    )
  })

  it('offers the refresh control in the workflow editor, and runs it', async () => {
    const onRefreshFields = vi.fn()
    render(
      <SinkForm
        embedded
        onRefreshFields={onRefreshFields}
        availableFields={[{ path: 'email', type: 'string' }, { path: 'tier', type: 'string' }]}
      />,
      { wrapper }
    )

    const refresh = await screen.findByRole('button', { name: 'Refresh sample data and fields' })
    expect(screen.getByTestId('sink-upstream-fields')).toHaveTextContent('2 fields')

    await userEvent.click(refresh)
    expect(onRefreshFields).toHaveBeenCalledTimes(1)
  }, 20000)

  it('shows no refresh control outside the editor', async () => {
    render(<SinkForm />, { wrapper })
    await screen.findByPlaceholderText('NATS Sink')

    expect(screen.queryByRole('button', { name: 'Refresh sample data and fields' })).toBeNull()
  }, 20000)
})
