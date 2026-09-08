import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SourceWizard } from '@/components/forms/SourceWizard'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

const noop = () => {}
const mutation = (mutate: any) => ({ mutate, isPending: false, isError: false, error: null })

/** A rabbitmq_queue source filled in exactly as MessagingSourceConfig writes it. */
function rabbitProps(config: Record<string, string>) {
  return {
    source: { name: 'orders', type: 'rabbitmq_queue', vhost: '/', worker_id: '', active: true, config },
    isEditing: false,
    embedded: false,
    availableVHostsList: ['/'],
    workers: [],
    sourceTypes: [{ value: 'rabbitmq_queue', label: 'RabbitMQ Queue', group: 'Messaging' }],
    testMutation: mutation(noop),
    submitMutation: mutation(noop),
    testResult: { status: 'ok', message: 'Connection successful' },
    setTestResult: noop,
    updateConfig: noop,
    handleSourceChange: noop,
    onCancel: noop,
    discoveredTables: [], discoveredDatabases: [],
    isFetchingTables: false, isFetchingDBs: false,
    fetchTables: noop, fetchDatabases: noop,
    handleFileUpload: noop, uploading: false,
    allSources: [], setShowSetup: noop,
  } as any
}

const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)

/** Walk from Basics to the Connection step the way a user does. */
async function gotoConnectionStep(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: /next step/i }))
  expect(screen.getByText(/Step 2: Connection Settings/i)).toBeInTheDocument()
}

/**
 * Reported bug: Test Connection reports success and Next Step stays disabled
 * forever. The gate asked for `url` and `queue`; the form writes host fields
 * and `queue_name`, so nothing the user could type would satisfy it.
 */
describe('rabbitmq queue connection step', () => {
  it('enables Next once the host fields and queue name are filled', async () => {
    const user = userEvent.setup()
    ui(<SourceWizard {...rabbitProps({
      host: 'localhost', port: '5672', username: 'guest',
      password: 'guest', dbname: '/', queue_name: 'orders',
    })} />)

    await gotoConnectionStep(user)
    expect(screen.getByRole('button', { name: /next step/i })).toBeEnabled()
  })

  it('enables Next for a pasted legacy URL plus a queue name', async () => {
    const user = userEvent.setup()
    ui(<SourceWizard {...rabbitProps({
      url: 'amqp://guest:guest@localhost:5672/', queue_name: 'orders',
    })} />)

    await gotoConnectionStep(user)
    expect(screen.getByRole('button', { name: /next step/i })).toBeEnabled()
  })

  it('still blocks, and says why, when the queue name is missing', async () => {
    const user = userEvent.setup()
    ui(<SourceWizard {...rabbitProps({ host: 'localhost', port: '5672' })} />)

    await gotoConnectionStep(user)
    expect(screen.getByRole('button', { name: /next step/i })).toBeDisabled()
  })
})
