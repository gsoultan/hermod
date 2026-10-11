import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications, notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider, useMutation, type MutationCache } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { apiFetch } from '@/api'
import { mutationCache } from '@/lib/queryClient'

/**
 * A failed request is reported by apiFetch itself ("Request Failed"), and the
 * mutation cache reports any failed mutation that has no onError of its own
 * ("Action failed"). A mutation without onError whose request failed got both,
 * so one failure showed two red toasts saying the same thing.
 */
function DeleteButton() {
  const mutation = useMutation({
    mutationFn: async () => {
      await apiFetch('/api/workflows/wf1', { method: 'DELETE' })
    },
  })
  return <button onClick={() => mutation.mutate()}>Delete</button>
}

const renderWith = (cache: MutationCache) =>
  render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ mutationCache: cache, defaultOptions: { mutations: { retry: false } } })}>
        <DeleteButton />
      </QueryClientProvider>
    </MantineProvider>,
  )

describe('a failed mutation without onError', () => {
  afterEach(() => notifications.clean())

  it('is reported once', async () => {
    signInAs()
    server.use(http.delete('/api/workflows/wf1', () => HttpResponse.json({ error: 'workflow is locked' }, { status: 409 })))
    const user = userEvent.setup()
    renderWith(mutationCache)

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(screen.getAllByText('workflow is locked').length).toBeGreaterThan(0))
    // Give a second toast the chance to appear before counting.
    await new Promise((r) => setTimeout(r, 100))
    expect(screen.getAllByText('workflow is locked')).toHaveLength(1)
  })

  it('is still reported when the request was silent', async () => {
    signInAs()
    server.use(http.post('/api/workflows/wf1/toggle', () => HttpResponse.json({ error: 'engine offline' }, { status: 500 })))
    function Toggle() {
      const mutation = useMutation({
        mutationFn: async () => {
          await apiFetch('/api/workflows/wf1/toggle', { method: 'POST', silent: true })
        },
      })
      return <button onClick={() => mutation.mutate()}>Toggle</button>
    }
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <Notifications />
        <QueryClientProvider client={new QueryClient({ mutationCache, defaultOptions: { mutations: { retry: false } } })}>
          <Toggle />
        </QueryClientProvider>
      </MantineProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Toggle' }))
    expect(await screen.findByText('engine offline')).toBeInTheDocument()
  })
})
