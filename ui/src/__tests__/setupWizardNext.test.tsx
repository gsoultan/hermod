import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server } from '../test/setupTests'
import { SetupPage } from '@/pages/system/SetupPage'

function renderSetup() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <SetupPage onConfigured={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

async function chooseSqlite(user: ReturnType<typeof userEvent.setup>) {
  const input = screen.queryByRole('combobox', { name: /^database type/i, hidden: true })
    ?? screen.getByRole('textbox', { name: /^database type/i, hidden: true })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(within(list).getByRole('option', { name: 'SQLite', hidden: true }))
  await waitFor(() => expect(screen.getByRole('textbox', { name: /db path/i })).toHaveValue('hermod.db'))
}

const nextButton = () => screen.getByRole('button', { name: /next step/i })

describe('setup wizard: database step', () => {
  it('says why Next is disabled before the connection has been tested', async () => {
    const user = userEvent.setup()
    renderSetup()
    await chooseSqlite(user)

    expect(nextButton()).toBeDisabled()
    expect(screen.getByText(/test the connection to continue/i)).toBeInTheDocument()
  })

  it('shows the error when the connection test fails, instead of doing nothing', async () => {
    // The API answers 200 with ok:false for a database it cannot open.
    server.use(http.post('/api/config/database/test', () =>
      HttpResponse.json({ ok: false, error: 'unable to open database file: permission denied' })))
    const user = userEvent.setup()
    renderSetup()
    await chooseSqlite(user)

    await user.click(screen.getByRole('button', { name: /test connection/i }))

    expect(await screen.findByText(/unable to open database file: permission denied/i)).toBeInTheDocument()
    expect(nextButton()).toBeDisabled()
  })

  it('shows the error when the test request itself is rejected', async () => {
    server.use(http.post('/api/config/database/test', () =>
      HttpResponse.json({ error: 'unsupported database type' }, { status: 400 })))
    const user = userEvent.setup()
    renderSetup()
    await chooseSqlite(user)

    await user.click(screen.getByRole('button', { name: /test connection/i }))

    expect(await screen.findByText(/unsupported database type/i)).toBeInTheDocument()
    expect(nextButton()).toBeDisabled()
  })

  it('enables Next once the connection test passes', async () => {
    server.use(http.post('/api/config/database/test', () => HttpResponse.json({ ok: true, error: '' })))
    const user = userEvent.setup()
    renderSetup()
    await chooseSqlite(user)

    await user.click(screen.getByRole('button', { name: /test connection/i }))

    expect(await screen.findByText(/connection successful/i)).toBeInTheDocument()
    await waitFor(() => expect(nextButton()).toBeEnabled())
    expect(screen.queryByText(/test the connection to continue/i)).not.toBeInTheDocument()
  })
})

describe('setup wizard: admin step', () => {
  it('says the password is too weak when that is what keeps Next disabled', async () => {
    server.use(http.post('/api/config/database/test', () => HttpResponse.json({ ok: true, error: '' })))
    const user = userEvent.setup()
    renderSetup()
    await chooseSqlite(user)
    await user.click(screen.getByRole('button', { name: /test connection/i }))
    await waitFor(() => expect(nextButton()).toBeEnabled())
    await user.click(nextButton())

    await user.type(await screen.findByPlaceholderText(/your password/i), 'password')

    expect(nextButton()).toBeDisabled()
    expect(screen.getByText(/password is too weak/i)).toBeInTheDocument()
  })
})
