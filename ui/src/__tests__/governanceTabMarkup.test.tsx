import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { GovernanceTab } from '@/pages/system/settings/GovernanceTab'

describe('Governance tab markup', () => {
  // A block element inside <p> is invalid HTML; React logs it on every visit to Settings.
  it('renders the quota legend without nesting a block inside a paragraph', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const ctx: any = { requestDeleteWorkspace: vi.fn(), openWSModal: vi.fn(), workspaces: [] }
    render(<MantineProvider><GovernanceTab ctx={ctx} /></MantineProvider>)
    expect(screen.getByText(/means no limit/)).toBeInTheDocument()
    const nesting = error.mock.calls.filter((args) => /cannot be a descendant|cannot contain a nested/.test(String(args[0])))
    error.mockRestore()
    expect(nesting).toEqual([])
  })
})
