import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { OtherSourceConfig } from '@/components/workflow/Source/OtherSourceConfig'

// A schedule now runs one run at a time and tells each run when the last
// successful one was due (pkg/comm/source/cron/runs.go). The form says so, so a
// query can use {{meta.last_success_at}} instead of a fixed window, and lets the
// operator set how long a run may go before the schedule stops waiting for it.
describe('cron source form', () => {
  it('explains one run at a time and the last successful run', () => {
    render(<MantineProvider><OtherSourceConfig config={{}} updateConfig={() => {}} sourceType="cron" /></MantineProvider>)
    expect(screen.getByText(/one run at a time/i)).toBeInTheDocument()
    expect(screen.getAllByText(/meta\.last_success_at/).length).toBeGreaterThan(0)
  })

  it('sets the longest a run may take', () => {
    const update = vi.fn()
    render(<MantineProvider><OtherSourceConfig config={{}} updateConfig={update} sourceType="cron" /></MantineProvider>)
    fireEvent.change(screen.getByLabelText(/longest a run may take/i), { target: { value: '2h' } })
    expect(update).toHaveBeenCalledWith('max_run_duration', '2h')
  })
})
