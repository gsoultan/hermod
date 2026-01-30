import { render, screen, waitFor } from '@testing-library/react'
import { RouterProvider } from '@tanstack/react-router'
import { router } from '../router'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '../context/VHostContext'

describe('Auth redirect', () => {
  it('redirects unauthenticated users to /login', async () => {
    localStorage.removeItem('hermod_token')
    window.history.pushState({}, '', '/workflows')

    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <RouterProvider router={router} />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    const loginSubtitle = await screen.findByText(/Sign in to your account/i)
    expect(loginSubtitle).toBeInTheDocument()

    await waitFor(() => {
      expect(window.location.pathname).toBe('/login')
    })
  })
})
