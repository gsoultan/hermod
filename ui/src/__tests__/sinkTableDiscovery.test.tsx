import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Suspense, type ReactNode } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { SinkForm } from '@/components/forms/SinkForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <ConfirmProvider>
          <VHostProvider>
            <Suspense fallback={<div>loading</div>}>{children}</Suspense>
          </VHostProvider>
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

const postgresSink: any = {
  id: 'sk1',
  name: 'warehouse',
  type: 'postgres',
  vhost: '/',
  active: true,
  config: { host: 'db.internal', port: '5432', user: 'app', password: 'pw', dbname: 'shop' },
}

/**
 * Table discovery starts on its own once the connection is filled in. Any
 * other edit made while it was in flight — renaming the sink, say — aborted
 * the request, and the retry that followed was skipped because the connection
 * parameters had not changed since the attempt that was just thrown away. The
 * table picker then stayed empty with nothing to say why.
 */
describe('sink table discovery', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workspaces', () => HttpResponse.json([])),
      http.get('/api/sinks/:id/workflows', () => HttpResponse.json({ data: [], total: 0 })),
    )
  })

  it('still lists the tables when another field is edited while they load', async () => {
    let calls = 0
    let releaseFirst: () => void = () => {}
    const firstHeld = new Promise<void>((resolve) => { releaseFirst = resolve })
    server.use(
      http.post('/api/sinks/discover/tables', async () => {
        calls += 1
        if (calls === 1) await firstHeld
        return HttpResponse.json(['orders', 'customers'])
      }),
    )

    const user = userEvent.setup()
    render(<SinkForm embedded isEditing initialData={postgresSink} />, { wrapper })

    // Wait for the automatic discovery to be in flight, then edit something
    // that has nothing to do with the connection.
    await waitFor(() => expect(calls).toBe(1), { timeout: 10000 })
    await user.type(await screen.findByPlaceholderText('NATS Sink', {}, { timeout: 10000 }), 's')
    releaseFirst()

    await user.click(screen.getByRole('button', { name: /next step/i }))
    const tablePicker = await screen.findByPlaceholderText('Select or type table name', {}, { timeout: 10000 })
    await user.click(tablePicker)

    await waitFor(() => expect(screen.getByText('orders')).toBeInTheDocument(), { timeout: 10000 })
  }, 30000)
})
