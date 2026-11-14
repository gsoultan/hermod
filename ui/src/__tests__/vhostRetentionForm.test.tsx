import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { VHostRetentionForm } from '@/components/forms/VHostRetentionForm'

/**
 * A vhost sets how long its audit log, its workflows' saved versions and its
 * decided approvals are kept. Nothing purged these three before, so they grew
 * for as long as Hermod ran. A box left empty keeps everything, which is what
 * every vhost did until somebody fills one in.
 */

function renderForm(vhost = 'sales') {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <VHostRetentionForm vhost={vhost} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('a vhost\'s history retention', () => {
  beforeEach(() => signInAs())

  it('shows what the vhost keeps now, and an unset limit as an empty box', async () => {
    server.use(
      http.get('/api/vhosts/sales/retention', () =>
        HttpResponse.json({ audit_log_days: 365, workflow_versions: 0, approval_days: 180 })),
    )
    renderForm()
    expect(await screen.findByLabelText(/audit log/i)).toHaveValue('365')
    expect(screen.getByLabelText(/workflow versions/i)).toHaveValue('')
    expect(screen.getByLabelText(/approvals/i)).toHaveValue('180')
  }, 20000)

  it('saves the three limits for this vhost, an empty box as 0', async () => {
    let saved: unknown
    let savedFor = ''
    server.use(
      http.get('/api/vhosts/:vhost/retention', () =>
        HttpResponse.json({ audit_log_days: 0, workflow_versions: 0, approval_days: 0 })),
      http.put('/api/vhosts/:vhost/retention', async ({ params, request }) => {
        savedFor = params.vhost as string
        saved = await request.json()
        return HttpResponse.json(saved as object)
      }),
    )
    const user = userEvent.setup()
    renderForm('team a')
    await user.type(await screen.findByLabelText(/audit log/i), '90')
    await user.type(screen.getByLabelText(/workflow versions/i), '20')
    await user.click(screen.getByRole('button', { name: /save retention/i }))

    await waitFor(() => expect(saved).toEqual({ audit_log_days: 90, workflow_versions: 20, approval_days: 0 }))
    expect(savedFor).toBe('team a')
    expect(await screen.findByRole('status')).toHaveTextContent('Saved')
  }, 20000)

  it('shows why the server refused a setting', async () => {
    server.use(
      http.get('/api/vhosts/sales/retention', () =>
        HttpResponse.json({ audit_log_days: 0, workflow_versions: 0, approval_days: 0 })),
      http.put('/api/vhosts/sales/retention', () =>
        HttpResponse.json({ error: 'invalid vhost retention: audit_log_days must be between 0 and 3650, got 9999' }, { status: 400 })),
    )
    const user = userEvent.setup()
    renderForm()
    await user.type(await screen.findByLabelText(/audit log/i), '9999')
    await user.click(screen.getByRole('button', { name: /save retention/i }))
    expect(await screen.findByText(/must be between 0 and 3650/i)).toBeInTheDocument()
  }, 20000)

  it('says so when the retention cannot be read, and offers nothing to save over it', async () => {
    server.use(
      http.get('/api/vhosts/sales/retention', () => HttpResponse.json({ error: 'Forbidden' }, { status: 403 })),
    )
    renderForm()
    expect(await screen.findByText(/could not read/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /save retention/i })).not.toBeInTheDocument()
  }, 20000)
})
