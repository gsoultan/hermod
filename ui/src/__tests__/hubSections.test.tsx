import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { Suspense, type ComponentType } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { AIBudgetPage } from '@/pages/ai/AIBudgetPage'
import { AuditLogsPage } from '@/pages/monitoring/AuditLogsPage'
import { CommunityMarketplace } from '@/pages/Marketplace/CommunityMarketplace'
import { ComplianceDashboard } from '@/pages/monitoring/ComplianceDashboard'
import { DeadLettersPage } from '@/pages/monitoring/DeadLettersPage'
import GlobalHealthPage from '@/pages/monitoring/GlobalHealthPage'
import { LineagePage } from '@/pages/monitoring/LineagePage'
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { SchemasPage } from '@/pages/system/SchemasPage'
import { SecretsPage } from '@/pages/secrets/SecretsPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params: _p, search: _s, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
  useNavigate: () => () => {},
  useSearch: () => ({}),
  useRouter: () => ({ navigate: () => {} }),
}))

/**
 * Each of these opens as a tab of Activity, Developer or Connections. The page
 * already carries its title, so the tab is headed by its own label as a plain
 * second-level heading, not by a second big title in a coloured panel.
 */
const TABS: Array<[string, ComponentType, string, RegExp]> = [
  ['AI budget', AIBudgetPage, 'AI budget', /^AI budget$/],
  ['Audit log', AuditLogsPage, 'Audit log', /^Audit Logs$/],
  ['Community', CommunityMarketplace, 'Community', /^Community Marketplace$/],
  ['Compliance', ComplianceDashboard, 'Compliance', /^Compliance Dashboard$/],
  ['Dead letters', DeadLettersPage, 'Dead letters', /^Dead letters$/],
  ['Mesh health', GlobalHealthPage, 'Mesh health', /^Global Mesh Health$/],
  ['Lineage', LineagePage, 'Lineage', /^Data lineage$/],
  ['Machine learning', ModelsPage, 'Machine learning', /^Machine Learning$/],
  ['Schema registry', SchemasPage, 'Schema registry', /^Global Schema Registry$/],
  ['Secrets', SecretsPage, 'Secrets', /^Secrets$/],
]

beforeEach(() => {
  signInAs('Administrator')
  server.use(
    http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/audit-logs', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/dead-letters', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/dead-letters/stats', () => HttpResponse.json({})),
    http.get('/api/schemas', () => HttpResponse.json([])),
    http.get('/api/marketplace/plugins', () => HttpResponse.json([])),
    http.get('/api/infra/mesh-health', () => HttpResponse.json([])),
    http.get('/api/*', () => HttpResponse.json({})),
  )
})

describe('a tab inside a page', () => {
  it.each(TABS)('%s is headed by its tab label, once', async (_name, Page, heading, old) => {
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <VHostProvider>
            <ConfirmProvider>
              <Suspense fallback={null}><Page /></Suspense>
            </ConfirmProvider>
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>,
    )
    expect(await screen.findByRole('heading', { level: 2, name: heading })).toBeInTheDocument()
    expect(screen.queryAllByRole('heading').filter((h) => old.test(h.textContent ?? '') && h.textContent !== heading)).toEqual([])
  })
})

describe('Mesh health', () => {
  it('shows no made-up data quality alert', async () => {
    server.use(http.get('/api/infra/mesh-health', () => HttpResponse.json([])))
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <GlobalHealthPage />
        </QueryClientProvider>
      </MantineProvider>,
    )
    await screen.findByRole('heading', { level: 2 })
    expect(screen.queryByText(/data quality/i)).toBeNull()
    expect(screen.queryByText(/Transaction Processing/)).toBeNull()
  })
})
