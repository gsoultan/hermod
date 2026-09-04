import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SourceWizard } from '@/components/forms/SourceWizard'
import { SinkWizard } from '@/components/forms/SinkWizard'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

const noop = () => {}
const mutation = (mutate: any) => ({ mutate, isPending: false, isError: false, error: null })

function sourceProps(over: Record<string, any> = {}) {
  return {
    source: { name: 'orders', type: 'postgres', vhost: '/', worker_id: '', active: true,
      config: { host: 'db', port: '5432', user: 'u', password: 'p', dbname: 'd', tables: 't' } },
    isEditing: false,
    embedded: false,
    availableVHostsList: ['/'],
    workers: [],
    sourceTypes: [{ value: 'postgres', label: 'PostgreSQL', group: 'Databases' }],
    testMutation: mutation(noop),
    submitMutation: mutation(noop),
    testResult: null,
    setTestResult: noop,
    updateConfig: noop,
    handleSourceChange: noop,
    onCancel: noop,
    discoveredTables: [], discoveredDatabases: [],
    isFetchingTables: false, isFetchingDBs: false,
    fetchTables: noop, fetchDatabases: noop,
    handleFileUpload: noop, uploading: false,
    allSources: [], setShowSetup: noop,
    ...over,
  } as any
}

function sinkProps(over: Record<string, any> = {}) {
  return {
    sink: { name: 'out', type: 'stdout', vhost: '/', worker_id: '', config: {} },
    isEditing: false,
    embedded: false,
    availableVHostsList: ['/'],
    workers: [],
    sinkTypes: [{ value: 'stdout', label: 'Stdout', group: 'APIs & Triggers' }],
    testMutation: mutation(noop),
    submitMutation: mutation(noop),
    testResult: null,
    setTestResult: noop,
    updateConfig: noop,
    handleSinkChange: noop,
    onCancel: noop,
    configComponents: {},
    ...over,
  } as any
}

const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)

/**
 * The Save action lived only inside Stepper.Completed, so changing one field on
 * an existing source meant clicking Next three times to reach a step whose only
 * content was a Save button. Creating is a wizard; editing is not.
 */
describe('wizard save while editing', () => {
  it('offers Save on the first step when editing a source', () => {
    ui(<SourceWizard {...sourceProps({ isEditing: true })} />)
    expect(screen.getByRole('button', { name: /update source/i })).toBeInTheDocument()
  })

  it('offers Save on the first step when editing a sink', () => {
    ui(<SinkWizard {...sinkProps({ isEditing: true })} />)
    expect(screen.getByRole('button', { name: /update sink/i })).toBeInTheDocument()
  })

  it('submits the entity from the persistent Save button', async () => {
    const mutate = vi.fn()
    const user = userEvent.setup()
    ui(<SourceWizard {...sourceProps({ isEditing: true, submitMutation: mutation(mutate) })} />)

    await user.click(screen.getByRole('button', { name: /update source/i }))
    expect(mutate).toHaveBeenCalledTimes(1)
  })

  it('does not offer Save while creating — that is what the wizard is for', () => {
    ui(<SourceWizard {...sourceProps({ isEditing: false })} />)
    expect(screen.queryByRole('button', { name: /create source/i })).toBeNull()
    expect(screen.getByRole('button', { name: /next step/i })).toBeInTheDocument()
  })
})
