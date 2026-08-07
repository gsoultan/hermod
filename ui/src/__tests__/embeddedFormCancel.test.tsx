import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { Suspense } from 'react'
import { vi } from 'vitest'

// The editor mounts these forms inside a drawer/modal, not a route. Stub the
// router so the forms can render without a RouterProvider.
const navigateSpy = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateSpy,
  Link: (props: any) => <button {...props} />,
}))

import { SourceForm } from '@/components/forms/SourceForm'
import { SinkForm } from '@/components/forms/SinkForm'

function Harness({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
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

describe('embedded Source/Sink form Cancel', () => {
  beforeEach(() => {
    navigateSpy.mockClear()
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sources', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/sinks', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
    )
  })

  // Cancel used to be wired to `onSave(null)`. The workflow editor's save
  // handler dereferences its argument (`updatedData.name`), so clicking Cancel
  // threw a TypeError, the drawer never closed and the button looked dead.
  // Cancel must be its own signal and must never reach the save path.
  it('SourceForm: Cancel calls onCancel and never onSave', async () => {
    const onSave = vi.fn()
    const onCancel = vi.fn()

    render(
      <Harness>
        <SourceForm embedded onSave={onSave} onCancel={onCancel} />
      </Harness>
    )

    const cancel = await screen.findByRole('button', { name: /^cancel$/i })
    fireEvent.click(cancel)

    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
    expect(onSave).not.toHaveBeenCalled()
  })

  it('SinkForm: Cancel calls onCancel and never onSave', async () => {
    const onSave = vi.fn()
    const onCancel = vi.fn()

    render(
      <Harness>
        <SinkForm embedded onSave={onSave} onCancel={onCancel} />
      </Harness>
    )

    const cancel = await screen.findByRole('button', { name: /^cancel$/i })
    fireEvent.click(cancel)

    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
    expect(onSave).not.toHaveBeenCalled()
  })

  // Standalone (routed) usage must keep navigating away on Cancel.
  it('SourceForm: Cancel navigates to /sources when not embedded', async () => {
    render(
      <Harness>
        <SourceForm />
      </Harness>
    )

    const cancel = await screen.findByRole('button', { name: /^cancel$/i })
    fireEvent.click(cancel)

    await waitFor(() =>
      expect(navigateSpy).toHaveBeenCalledWith({ to: '/sources' })
    )
  })

  it('SinkForm: Cancel navigates to /sinks when not embedded', async () => {
    render(
      <Harness>
        <SinkForm />
      </Harness>
    )

    const cancel = await screen.findByRole('button', { name: /^cancel$/i })
    fireEvent.click(cancel)

    await waitFor(() =>
      expect(navigateSpy).toHaveBeenCalledWith({ to: '/sinks' })
    )
  })

  // Defence in depth: even if some caller still routes Cancel through onSave,
  // the editor's inline-save handler must not crash on a null payload.
  it('SourceForm: embedded Cancel without an onCancel prop does not call onSave with null', async () => {
    const onSave = vi.fn()

    render(
      <Harness>
        <SourceForm embedded onSave={onSave} />
      </Harness>
    )

    const cancel = await screen.findByRole('button', { name: /^cancel$/i })
    fireEvent.click(cancel)

    await waitFor(() => {
      expect(onSave).not.toHaveBeenCalledWith(null)
    })
  })
})
