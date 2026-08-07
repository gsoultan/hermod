import { QueryClient, MutationCache } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'

/**
 * Pulls the most useful message out of whatever a failing request threw.
 *
 * Backend errors arrive in several shapes ({error}, {message}, a bare string),
 * and the least useful thing to show a user is "[object Object]".
 */
export function describeError(error: unknown): string {
  if (!error) return 'Unknown error'
  if (typeof error === 'string') return error
  if (error instanceof Error && error.message) return error.message

  const anyErr = error as Record<string, unknown>
  for (const key of ['error', 'message', 'detail', 'reason']) {
    const value = anyErr?.[key]
    if (typeof value === 'string' && value.trim()) return value
  }
  try {
    return JSON.stringify(error)
  } catch {
    return String(error)
  }
}

/**
 * A mutation that fails must never be silent.
 *
 * Roughly half of this app's mutations had no onError handler, so a rejected
 * request left the button un-spinning and the screen unchanged — visually
 * identical to success. This cache-level handler guarantees feedback for every
 * mutation; individual mutations can still define their own onError, and doing
 * so suppresses this fallback so messages are never doubled up.
 */
export const mutationCache = new MutationCache({
  onError: (error, _variables, _context, mutation) => {
    if (mutation.options.onError) return
    notifications.show({
      title: 'Action failed',
      message: describeError(error),
      color: 'red',
      autoClose: 8000,
    })
  },
})

export const queryClient = new QueryClient({
  mutationCache,
  defaultOptions: {
    queries: {
      // The UI polls live pipeline state; a short stale window keeps that fresh
      // without stampeding the API on every remount.
      staleTime: 5_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
    mutations: {
      // Mutations are user-initiated and often not idempotent — a silent retry
      // could duplicate a create.
      retry: 0,
    },
  },
})
