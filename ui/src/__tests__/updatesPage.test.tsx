import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '../test/setupTests'
import { UpdatesPage } from '@/pages/system/UpdatesPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

let role = 'Administrator'
vi.mock('@/auth/session', async (orig) => ({ ...(await orig<any>()), getSessionRole: () => role }))

const base = {
  current: 'v1.24.9',
  build: 'v1.24.9 (Enterprise Edition)',
  update_available: false,
  can_install: true,
  platform: 'linux/amd64',
  phase: 'idle',
  checked_at: '2026-11-13T05:00:00Z',
  settings: { mode: 'manual', include_prereleases: false, check_interval_hours: 24 },
}

const available = {
  ...base,
  update_available: true,
  latest: { tag: 'v1.25.0', name: 'v1.25.0', notes: 'Faster CDC.', url: 'https://github.com/gsoultan/hermod/releases/tag/v1.25.0', published_at: '2026-11-12T00:00:00Z', prerelease: false },
}

function renderPage() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <UpdatesPage />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Updates page', () => {
  beforeEach(() => {
    role = 'Administrator'
  })

  it('says the running version is the newest', async () => {
    server.use(http.get('*/api/system/updates', () => HttpResponse.json(base)))
    renderPage()
    expect(await screen.findByText('You are on the newest release')).toBeInTheDocument()
    expect(screen.getByText('v1.24.9')).toBeInTheDocument()
  })

  it('offers an available release and installs it after confirming', async () => {
    let installed = ''
    server.use(
      http.get('*/api/system/updates', () => HttpResponse.json(installed ? { ...available, phase: 'downloading', installing: 'v1.25.0' } : available)),
      http.post('*/api/system/updates/install', async ({ request }) => {
        installed = ((await request.json()) as any).version
        return HttpResponse.json({ ...available, phase: 'downloading', installing: 'v1.25.0' }, { status: 202 })
      }),
    )
    renderPage()
    expect(await screen.findByText('Hermod v1.25.0 is available')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /release notes/i })).toHaveAttribute('href', 'https://github.com/gsoultan/hermod/releases/tag/v1.25.0')
    fireEvent.click(screen.getByRole('button', { name: 'Install v1.25.0' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Install and restart' }))
    await waitFor(() => expect(installed).toBe('v1.25.0'))
    expect(await screen.findByText(/Downloading v1.25.0/)).toBeInTheDocument()
  })

  it('rides out the restart and says when the new version is running', async () => {
    let phase = 'idle'
    server.use(
      http.get('*/api/system/updates', () => {
        if (phase === 'down') return HttpResponse.error()
        if (phase === 'up') return HttpResponse.json({ ...base, current: 'v1.25.0', build: 'v1.25.0 (Enterprise Edition)' })
        return HttpResponse.json(available)
      }),
      http.post('*/api/system/updates/install', () => {
        phase = 'down'
        return HttpResponse.json({ ...available, phase: 'restarting', installing: 'v1.25.0' }, { status: 202 })
      }),
    )
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Install v1.25.0' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Install and restart' }))
    expect(await screen.findByText('Restarting into v1.25.0…')).toBeInTheDocument()
    phase = 'up'
    expect(await screen.findByText('Updated to v1.25.0', {}, { timeout: 8000 })).toBeInTheDocument()
  }, 15000)

  it('explains why this installation cannot update itself and offers no install', async () => {
    server.use(http.get('*/api/system/updates', () => HttpResponse.json({
      ...available, can_install: false, install_blocked: 'Hermod is running in a container. Update by pulling the new image tag.',
    })))
    renderPage()
    expect(await screen.findByText(/running in a container/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Install v1.25.0' })).not.toBeInTheDocument()
  })

  it('reports a rollback', async () => {
    server.use(http.get('*/api/system/updates', () => HttpResponse.json({
      ...base, last_outcome: { result: 'rolled_back', from: 'v1.24.9', to: 'v1.25.0', detail: 'v1.25.0 did not stay up through 3 starts', at: '2026-11-13T05:30:00Z' },
    })))
    renderPage()
    expect(await screen.findByText(/v1.25.0 was rolled back to v1.24.9/)).toBeInTheDocument()
    expect(screen.getByText(/did not stay up through 3 starts/)).toBeInTheDocument()
  })

  it('saves the choice between automatic and manual updates', async () => {
    let saved: any = null
    server.use(
      http.get('*/api/system/updates', () => HttpResponse.json(base)),
      http.put('*/api/system/updates/settings', async ({ request }) => {
        saved = await request.json()
        return HttpResponse.json(saved)
      }),
    )
    renderPage()
    fireEvent.click(await screen.findByRole('radio', { name: /Automatic/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saved).toEqual({ mode: 'automatic', include_prereleases: false, check_interval_hours: 24 }))
  })

  it('checks now on request', async () => {
    let checked = false
    server.use(
      http.get('*/api/system/updates', () => HttpResponse.json(base)),
      http.post('*/api/system/updates/check', () => {
        checked = true
        return HttpResponse.json(available)
      }),
    )
    renderPage()
    await screen.findByText('You are on the newest release')
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }))
    await waitFor(() => expect(checked).toBe(true))
    expect(await screen.findByText('Hermod v1.25.0 is available')).toBeInTheDocument()
  })

  it('tells non-administrators who manages updates', async () => {
    role = 'Editor'
    renderPage()
    expect(await screen.findByText('Updates are managed by administrators')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Check now' })).not.toBeInTheDocument()
  })
})
