import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { DatabaseSourceConfig } from '@/components/workflow/Source/DatabaseSourceConfig'

/**
 * Many workflows can share one PostgreSQL source, and with it one connection
 * pool per Hermod worker. Its size had no field: it could be set only inside a
 * hand-written connection URI, so a source used by a thousand workflows got
 * the default of 50 and the workflows timed out waiting for a connection.
 *
 * Hermod also finds PgBouncer, or any proxy, by itself now, so the PgBouncer
 * choice defaults to detecting it rather than to "not used", which, saved,
 * would have turned detection off.
 */
function renderPostgres(config: Record<string, any>, updateConfig = vi.fn()) {
  render(
    <MantineProvider>
      <DatabaseSourceConfig type="postgres" config={config} updateConfig={updateConfig} />
    </MantineProvider>,
  )
  return updateConfig
}

describe('the PostgreSQL source pool settings', () => {
  it('sets the shared pool size', () => {
    const update = renderPostgres({ host: 'db', port: '5432' })
    fireEvent.change(screen.getByLabelText(/Max connections/i), { target: { value: '200' } })
    expect(update).toHaveBeenCalledWith('pool_max_conns', '200')
  })

  it('says the pool is shared by every workflow on the source', () => {
    renderPostgres({})
    expect(screen.getByText(/shared by every workflow/i)).toBeInTheDocument()
  })

  it('detects PgBouncer by default', () => {
    renderPostgres({})
    expect(screen.getByDisplayValue('Detect automatically')).toBeInTheDocument()
  })

  it('still lets the operator say PgBouncer is or is not used', () => {
    renderPostgres({ pgbouncer: 'true' })
    expect(screen.getByDisplayValue('Used, in transaction or statement mode')).toBeInTheDocument()
  })
})
