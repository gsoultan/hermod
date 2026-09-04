import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import type { ReactNode } from 'react'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { useSourceForm } from '@/hooks/useSourceForm'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  )
}

const initialData: any = {
  id: 's1',
  name: 'orders-db',
  type: 'postgres',
  vhost: '/',
  worker_id: '',
  active: true,
  config: { host: 'db.internal', port: '5432', tables: 'orders' },
  sample: JSON.stringify({ id: 1, total: 99 }),
}

describe('useSourceForm sample lifecycle', () => {
  beforeEach(() => {
    server.use(
      http.get('/api/sources/:id/workflows', () => HttpResponse.json({ data: [], total: 0 }))
    )
    signInAs()
  })

  /**
   * Changing connection settings invalidates the stored sample — the fields it
   * describes may no longer exist. `updateConfig` clears it deliberately.
   *
   * The hydrate effect listed the whole form-values object in its dependencies
   * and re-parsed `initialData.sample` unconditionally, so it ran on every
   * keystroke and immediately put the stale sample back. The transformation
   * preview downstream then showed fields from a connection the user had
   * already changed.
   */
  it('does not restore the persisted sample after a config change', async () => {
    const { result } = renderHook(() => useSourceForm({ initialData, isEditing: true }), { wrapper })

    await waitFor(() => expect(result.current.sampleData).toEqual({ id: 1, total: 99 }))

    act(() => {
      result.current.updateConfig('host', 'db-replica.internal')
    })

    expect(result.current.sampleData).toBeNull()

    // Give the effect every chance to run again and put it back.
    await new Promise((r) => setTimeout(r, 50))
    expect(result.current.sampleData).toBeNull()
  })

  it('still hydrates the persisted sample on first load', async () => {
    const { result } = renderHook(() => useSourceForm({ initialData, isEditing: true }), { wrapper })
    await waitFor(() => expect(result.current.sampleData).toEqual({ id: 1, total: 99 }))
  })

  it('clears the sample when the source type changes', async () => {
    const { result } = renderHook(() => useSourceForm({ initialData, isEditing: true }), { wrapper })
    await waitFor(() => expect(result.current.sampleData).not.toBeNull())

    act(() => {
      result.current.handleSourceChange({ type: 'mysql' })
    })

    expect(result.current.sampleData).toBeNull()
  })
})
