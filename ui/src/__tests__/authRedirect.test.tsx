import { render, screen, waitFor } from '@testing-library/react'
import { RouterProvider } from '@tanstack/react-router'
import { router } from '../router'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { signOut } from '@/test/setupTests'

describe('Auth redirect', () => {
  it('redirects unauthenticated users to /login', async () => {
    signOut()
    window.history.pushState({}, '', '/workflows')

    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <ConfirmProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <RouterProvider router={router} />
          </VHostProvider>
        </QueryClientProvider>
      </ConfirmProvider>
      </MantineProvider>
    )

    // Explicit timeout: reaching the login screen means an async beforeLoad, a
    // redirect and two lazy chunks resolving. Testing Library's default 1000ms
    // is a wall-clock assertion, not a behavioural one, and this test failed
    // intermittently under parallel suite load because of it. What is being
    // asserted is that the redirect happens, not that it happens within a
    // second on a loaded machine.
    const loginSubtitle = await screen.findByText(/Sign in to your account/i, undefined, {
      timeout: 5000,
    })
    expect(loginSubtitle).toBeInTheDocument()

    await waitFor(() => {
      expect(window.location.pathname).toBe('/login')
    }, { timeout: 5000 })
  })
})
