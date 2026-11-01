import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { server } from '../test/setupTests'
import { AboutPage } from '@/pages/system/AboutPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

function renderAbout() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AboutPage />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('About page', () => {
  it('states that the copyright belongs to Gembit Soultan Shirazi, all rights reserved', () => {
    renderAbout()
    expect(screen.getByRole('heading', { name: 'About Hermod' })).toBeInTheDocument()
    expect(screen.getByText('© 2025 Gembit Soultan Shirazi. All rights reserved.')).toBeInTheDocument()
    expect(screen.getByText(/proprietary and confidential property of Gembit Soultan Shirazi/)).toBeInTheDocument()
    expect(screen.getByText('Hermod Proprietary License')).toBeInTheDocument()
  })

  it('shows the release and the full build the server reports', async () => {
    server.use(http.get('*/api/version', () => HttpResponse.json({ version: 'v1.2.5-0.20260730072614-816c03f6a612+dirty' })))
    renderAbout()
    expect(await screen.findByText('v1.2.5')).toBeInTheDocument()
    expect(screen.getByText('v1.2.5-0.20260730072614-816c03f6a612+dirty')).toBeInTheDocument()
  })
})
