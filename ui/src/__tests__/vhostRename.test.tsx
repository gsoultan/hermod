import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostForm } from '@/components/forms/VHostForm'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
}))

function renderForm(qc: QueryClient) {
  const record = { id: 'v1', name: 'sales', description: '' }
  return render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostForm initialData={record} isEditing />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

/**
 * Renaming a vhost carries the new name to every workflow, source, sink and
 * user of it. The form says so before the save, shows why a rename was
 * refused, and after one refetches everything, since every list on the page
 * may be holding the old name.
 */
describe('renaming a vhost', () => {
  beforeEach(() => signInAs())

  it('says what a rename moves once the name is changed', async () => {
    const user = userEvent.setup()
    renderForm(new QueryClient())
    expect(screen.queryByText(/workflows, sources, sinks and users/i)).not.toBeInTheDocument()
    const name = await screen.findByLabelText(/name/i)
    await user.clear(name)
    await user.type(name, 'revenue')
    expect(screen.getByText(/workflows, sources, sinks and users/i)).toBeInTheDocument()
  }, 20000)

  it('shows why the server refused a rename', async () => {
    server.use(
      http.put('/api/vhosts/v1', () => HttpResponse.json({ error: 'Another vhost is already named ops' }, { status: 409 })),
    )
    const user = userEvent.setup()
    renderForm(new QueryClient())
    const name = await screen.findByLabelText(/name/i)
    await user.clear(name)
    await user.type(name, 'ops')
    await user.click(screen.getByRole('button', { name: /save changes/i }))
    expect(await screen.findByText('Another vhost is already named ops')).toBeInTheDocument()
  }, 20000)

  it('refetches every query after a rename', async () => {
    server.use(
      http.put('/api/vhosts/v1', () => HttpResponse.json({ id: 'v1', name: 'revenue', description: '' })),
    )
    const qc = new QueryClient()
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    const user = userEvent.setup()
    renderForm(qc)
    const name = await screen.findByLabelText(/name/i)
    await user.clear(name)
    await user.type(name, 'revenue')
    await user.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith())
  }, 20000)
})
