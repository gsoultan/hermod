import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SQLConfig } from '@/components/workflow/Transformation/configs/enrichment/SQLConfig'

// The form was a banner describing a different node ("Enrich your message by
// executing a query"), then one card titled Database Connection that also held
// what to do with returned rows and with unresolved variables, then the query.
// It is now the four decisions in the order they are made: where, what, what
// comes back, and what happens when a value is missing.
describe('execute_sql form layout', () => {
  const ops = { id: 'ops', name: 'ops', type: 'postgres', config: { use_cdc: 'false' } }

  const renderConfig = (config: any) =>
    render(
      <MantineProvider>
        <SQLConfig config={config} updateNodeConfig={vi.fn()} nodeId="n1" sources={[ops]} availableFields={[]} />
      </MantineProvider>
    )

  it('lays the settings out as numbered steps, in the order they are decided', () => {
    renderConfig({ sourceId: 'ops' })
    const titles = screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
    expect(titles).toEqual(['1. Database', '2. Statement', '3. Returned data', '4. Missing values'])
  })

  it('does not describe itself as an enrichment query', () => {
    renderConfig({ sourceId: 'ops' })
    expect(screen.queryByText(/SQL Enrichment/i)).toBeNull()
    expect(screen.queryByText(/enrich your message/i)).toBeNull()
  })

  it('opens the builder for a statement that writes', async () => {
    renderConfig({ sourceId: 'ops' })
    expect(await screen.findByRole('button', { name: /run statement/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'SQL statement' })).toHaveValue('')
  })

  it('asks for a database before a statement', () => {
    renderConfig({})
    expect(screen.getByText(/pick a database/i)).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'SQL statement' })).toBeNull()
  })
})
