import { describe, it, expect } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ConfirmProvider, useConfirm } from '@/components/common/ConfirmProvider'
import { useState } from 'react'

// Harness: a button that opens a confirm and records the resolved value, so the
// tests assert on what the caller actually receives.
function Harness(props: Parameters<ReturnType<typeof useConfirm>>[0]) {
  const confirm = useConfirm()
  const [result, setResult] = useState<string>('pending')
  return (
    <>
      <button onClick={async () => setResult(String(await confirm(props)))}>open</button>
      <span data-testid="result">{result}</span>
    </>
  )
}

const renderHarness = (props: Parameters<ReturnType<typeof useConfirm>>[0]) =>
  render(
    <MantineProvider>
      <ConfirmProvider>
        <Harness {...props} />
      </ConfirmProvider>
    </MantineProvider>,
  )

describe('ConfirmProvider', () => {
  it('resolves true when confirmed', async () => {
    const user = userEvent.setup()
    renderHarness({ title: 'Delete user', message: 'This removes the account.' })

    await user.click(screen.getByText('open'))
    await user.click(await screen.findByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(screen.getByTestId('result')).toHaveTextContent('true'))
  })

  it('resolves false when cancelled', async () => {
    const user = userEvent.setup()
    renderHarness({ title: 'Delete user', message: 'This removes the account.' })

    await user.click(screen.getByText('open'))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.getByTestId('result')).toHaveTextContent('false'))
  })

  // The whole point of typed confirmation: a reflexive click must not be enough
  // to truncate a table.
  it('keeps confirm disabled until the exact text is typed', async () => {
    const user = userEvent.setup()
    renderHarness({
      title: 'Truncate table',
      message: 'This deletes every row.',
      confirmText: 'orders',
      confirmLabel: 'Truncate',
      danger: true,
    })

    await user.click(screen.getByText('open'))
    const confirmBtn = await screen.findByRole('button', { name: 'Truncate' })
    expect(confirmBtn).toBeDisabled()

    const input = screen.getByPlaceholderText('orders')
    await user.type(input, 'order')
    expect(confirmBtn).toBeDisabled()

    await user.type(input, 's')
    await waitFor(() => expect(confirmBtn).toBeEnabled())

    await user.click(confirmBtn)
    await waitFor(() => expect(screen.getByTestId('result')).toHaveTextContent('true'))
  })

  it('renders the consequence callout when provided', async () => {
    const user = userEvent.setup()
    renderHarness({
      title: 'Truncate table',
      message: 'This deletes every row.',
      consequence: 'This cannot be undone.',
    })

    await user.click(screen.getByText('open'))
    expect(await screen.findByText('This cannot be undone.')).toBeInTheDocument()
  })
})
