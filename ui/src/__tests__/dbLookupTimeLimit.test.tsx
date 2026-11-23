import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DBLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/DBLookupConfig'

// A DB Lookup had no time limit, so a query stuck in PgBouncer's queue or on a
// dead connection waited until the stall watchdog restarted the workflow, and
// the operator saw only "context canceled". The engine now bounds every lookup
// (30s unless the node says otherwise, pkg/comm/transformer/lookup/timeout.go);
// this is where the node says otherwise.
describe('db_lookup time limit', () => {
  const renderAdvanced = async (config: any, updateNodeConfig: any = () => {}) => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <DBLookupConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          availableFields={[]}
          sources={[{ id: 's1', name: 'db', type: 'postgres' }]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    await user.click(screen.getByRole('tab', { name: /advanced/i }))
  }

  it('offers a timeout and writes it to the node', async () => {
    const update = vi.fn()
    await renderAdvanced({ sourceId: 's1' }, update)
    fireEvent.change(screen.getByRole('textbox', { name: 'Timeout' }), { target: { value: '45s' } })
    expect(update).toHaveBeenCalledWith('n1', { timeout: '45s' })
  })

  it('says what an empty timeout means, matching the engine default', async () => {
    await renderAdvanced({ sourceId: 's1' })
    expect(screen.getByTestId('db-lookup-timeout-description')).toHaveTextContent(/empty means 30s/i)
  })

  // The engine fails every message on a timeout it cannot read.
  it('says when a timeout has no unit', async () => {
    await renderAdvanced({ sourceId: 's1', timeout: '30' })
    expect(screen.getByText(/the timeout needs a unit/i)).toBeInTheDocument()
  })
})
