import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Suspense } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { UserForm } from '@/components/forms/UserForm'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return (
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <Suspense fallback={<div>loading</div>}>{children}</Suspense>
      </QueryClientProvider>
    </MantineProvider>
  )
}

/**
 * Any role can be given more than one virtual host.
 *
 * The picker capped Editors and Viewers at one, though nothing behind it does:
 * users.vhosts is a JSON list, the session carries the whole list, and
 * HasVHostAccess checks membership in it. So an Editor who works in both
 * "dev" and "prod" had to be made an Administrator — far more access than
 * asked for — or be given two accounts.
 */
describe('UserForm virtual hosts', () => {
  let saved: any

  beforeEach(() => {
    signInAs()
    saved = undefined
    server.use(
      http.get('/api/vhosts', () =>
        HttpResponse.json({ data: [{ id: 'v1', name: 'dev' }, { id: 'v2', name: 'prod' }], total: 2 })
      ),
      http.put('/api/users/:id', async ({ request }) => {
        saved = await request.json()
        return HttpResponse.json(saved)
      })
    )
  })

  it.each(['Editor', 'Viewer'])('lets a %s have several virtual hosts and saves them all', async (role) => {
    const user = userEvent.setup()
    const record = { id: 'u1', username: 'ada', full_name: 'Ada', email: 'ada@example.com', role, vhosts: ['dev'] }
    render(<UserForm initialData={record as any} isEditing />, { wrapper: Wrapper })

    const picker = await screen.findByLabelText(/virtual hosts they can use/i, { selector: 'input' })
    await user.click(picker)
    await user.click(await screen.findByRole('option', { name: 'prod', hidden: true }))

    const field = picker.closest('.mantine-MultiSelect-root') as HTMLElement
    expect(within(field).getByText('dev')).toBeInTheDocument()
    expect(within(field).getByText('prod')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /save changes/i }))
    await vi.waitFor(() => expect(saved?.vhosts).toEqual(['dev', 'prod']))
  }, 20000)
})
