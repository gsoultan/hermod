import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { vi, afterEach } from 'vitest'
import { usePreviewTransformation } from '@/pages/workflows/WorkflowEditor/hooks/usePreviewTransformation'

/**
 * The preview hook keeps one AbortController: the newest request's. Every
 * request cleared that slot when it settled, including one that had just been
 * aborted by its successor -- so the abort of request A wiped out B's
 * controller, and from then on nothing could cancel B. Closing the panel left
 * it running, and the next keystroke's preview ran alongside it instead of
 * replacing it.
 */
describe('usePreviewTransformation abort ownership', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('still cancels the newest preview after it superseded an older one', async () => {
    const signals: AbortSignal[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation((_url, init) => {
      const signal = (init as RequestInit).signal as AbortSignal
      signals.push(signal)
      // Never answers; settles only by being aborted, as a slow lookup would.
      return new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      })
    })

    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result, unmount } = renderHook(() => usePreviewTransformation(), { wrapper })

    const vars = (n: number) => ({ transformation: { type: 'set', config: {} }, message: { n } })

    act(() => result.current.run(vars(1)))
    await waitFor(() => expect(signals).toHaveLength(1))

    act(() => result.current.run(vars(2)))
    await waitFor(() => expect(signals).toHaveLength(2))
    // A was aborted by B, and A's settling must not orphan B.
    expect(signals[0].aborted).toBe(true)
    await act(async () => {
      await Promise.resolve()
    })

    unmount()

    expect(signals[1].aborted).toBe(true)
  })
})
