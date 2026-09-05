import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Suspense } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { UserForm } from '@/components/forms/UserForm'
import { VHostForm } from '@/components/forms/VHostForm'

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
 * Edit forms hydrate from `initialData` once per record, not once per object.
 *
 * Both forms copied `initialData` into local state in an effect keyed on the
 * object itself. React Query hands the edit page a new object whenever the
 * record refetches — after this form's own save, or when the list is
 * invalidated by something else on the page — and the effect then overwrote
 * whatever the user had typed since. Same class as the useSourceForm bug
 * fixed earlier: the key is the record's id, not the object's identity.
 */
describe('edit forms hydrate once per record', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [{ id: 'v1', name: 'default' }], total: 1 }))
    )
  })

  it('UserForm keeps in-progress edits when initialData is refetched', async () => {
    const user = userEvent.setup()
    const record = { id: 'u1', username: 'ada', full_name: 'Ada', email: 'ada@example.com', role: 'Editor', vhosts: ['default'] }

    const { rerender } = render(<UserForm initialData={record as any} isEditing />, { wrapper: Wrapper })
    const fullName = await screen.findByLabelText(/full name/i)
    await user.clear(fullName)
    await user.type(fullName, 'Ada Lovelace')
    expect(fullName).toHaveValue('Ada Lovelace')

    // Same record, fresh object — what a background refetch produces.
    rerender(<UserForm initialData={{ ...record } as any} isEditing />)
    expect(screen.getByLabelText(/full name/i)).toHaveValue('Ada Lovelace')
  }, 20000)

  it('UserForm does hydrate when a different record arrives', async () => {
    const a = { id: 'u1', username: 'ada', full_name: 'Ada', email: 'a@x', role: 'Editor', vhosts: [] }
    const b = { id: 'u2', username: 'bob', full_name: 'Bob', email: 'b@x', role: 'Viewer', vhosts: [] }
    const { rerender } = render(<UserForm initialData={a as any} isEditing />, { wrapper: Wrapper })
    expect(await screen.findByLabelText(/full name/i)).toHaveValue('Ada')
    rerender(<UserForm initialData={b as any} isEditing />)
    expect(screen.getByLabelText(/full name/i)).toHaveValue('Bob')
  }, 20000)

  it('VHostForm keeps in-progress edits when initialData is refetched', async () => {
    const user = userEvent.setup()
    const record = { id: 'v1', name: 'default', description: 'Main' }

    const { rerender } = render(<VHostForm initialData={record as any} isEditing />, { wrapper: Wrapper })
    const desc = await screen.findByLabelText(/description/i)
    await user.clear(desc)
    await user.type(desc, 'Production tenant')
    expect(desc).toHaveValue('Production tenant')

    rerender(<VHostForm initialData={{ ...record } as any} isEditing />)
    expect(screen.getByLabelText(/description/i)).toHaveValue('Production tenant')
  }, 20000)
})
