import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { DatabaseSourceConfig } from '@/components/workflow/Source/DatabaseSourceConfig'
import { SourceSetupInstructions } from '@/components/workflow/Source/SourceSetupInstructions'

/**
 * Oracle CDC can run two ways: polling for rows above the last id seen, which
 * sees inserts only, or LogMiner, which reads inserts, updates and deletes
 * from the redo logs. The form has to offer the choice and the settings each
 * one reads (`cdc_method`, `pdb_name`, `initial_load` for LogMiner; `id_field`
 * and `poll_interval` for polling), and the setup notes have to match what the
 * reader checks.
 */
function renderOracle(config: Record<string, any>, updateConfig = vi.fn()) {
  render(
    <MantineProvider>
      <DatabaseSourceConfig type="oracle" config={config} updateConfig={updateConfig} />
    </MantineProvider>,
  )
  return updateConfig
}

describe('the Oracle source form', () => {
  it('offers LogMiner as a change capture method', () => {
    const update = renderOracle({ use_cdc: 'true' })
    fireEvent.click(screen.getByRole('radio', { name: /LogMiner/i }))
    expect(update).toHaveBeenCalledWith('cdc_method', 'logminer')
  })

  it('defaults to incremental polling and asks for the id field it polls on', () => {
    renderOracle({ use_cdc: 'true' })
    expect(screen.getByRole('radio', { name: /Incremental/i })).toBeChecked()
    expect(screen.getByLabelText(/Incremental ID Field/i)).toBeInTheDocument()
    expect(screen.getByText(/inserts only/i)).toBeInTheDocument()
  })

  it('asks LogMiner for the PDB and an initial load, not an id field', () => {
    const update = renderOracle({ use_cdc: 'true', cdc_method: 'logminer' })
    expect(screen.queryByLabelText(/Incremental ID Field/i)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/Pluggable database/i), { target: { value: 'FREEPDB1' } })
    expect(update).toHaveBeenCalledWith('pdb_name', 'FREEPDB1')
    fireEvent.click(screen.getByLabelText(/Load existing rows first/i))
    expect(update).toHaveBeenCalledWith('initial_load', 'true')
  })

  it('does not offer a method choice to other databases', () => {
    render(
      <MantineProvider>
        <DatabaseSourceConfig type="mysql" config={{ use_cdc: 'true' }} updateConfig={vi.fn()} />
      </MantineProvider>,
    )
    expect(screen.queryByRole('radio', { name: /LogMiner/i })).not.toBeInTheDocument()
  })
})

describe('the Oracle setup notes', () => {
  it('name the table-level supplemental logging LogMiner checks for', () => {
    render(
      <MantineProvider>
        <SourceSetupInstructions sourceType="oracle" useCDCChecked config={{ cdc_method: 'logminer' }} updateConfig={() => {}} />
      </MantineProvider>,
    )
    expect(screen.getByText(/ADD SUPPLEMENTAL LOG DATA \(ALL\) COLUMNS/)).toBeInTheDocument()
  })

  it('say DB2 has no driver in this build instead of promising CDC', () => {
    render(
      <MantineProvider>
        <SourceSetupInstructions sourceType="db2" useCDCChecked config={{}} updateConfig={() => {}} />
      </MantineProvider>,
    )
    expect(screen.getByText(/no DB2 driver/i)).toBeInTheDocument()
    expect(screen.queryByText(/IBM CDC replication engine/i)).not.toBeInTheDocument()
  })
})
