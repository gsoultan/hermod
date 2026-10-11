import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi, afterEach, beforeEach } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { LiveLogPanel } from '@/pages/workflows/WorkflowEditor/components/LiveLogPanel'

const initialState = useWorkflowStore.getState()
const originalScrollTo = Element.prototype.scrollTo

const logs = [
  { id: 'l2', level: 'ERROR', message: 'sink refused the row', timestamp: '2026-10-09T10:00:02Z' },
  { id: 'l1', level: 'INFO', message: 'processed', timestamp: '2026-10-09T10:00:01Z', data: 'message_id: m-42, node: n1' },
]

const renderPanel = (handlers: { onTraceClick?: (id: string) => void; onErrorClick?: (log: any) => void } = {}) =>
  render(
    <MantineProvider>
      <LiveLogPanel
        workflowId="wf-1"
        active
        onTraceClick={handlers.onTraceClick ?? (() => {})}
        onErrorClick={handlers.onErrorClick ?? (() => {})}
      />
    </MantineProvider>,
  )

beforeEach(() => {
  useWorkflowStore.setState({ logs: logs as any, logsOpened: true, logsPaused: false })
})
afterEach(() => {
  useWorkflowStore.setState(initialState, true)
  Element.prototype.scrollTo = originalScrollTo
})

describe('live log panel', () => {
  // New lines are added at the top of the list, so following them means
  // staying at the top. The panel scrolled to the bottom on every new line,
  // away from the line that had just arrived.
  it('follows new lines at the top, where they are added', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as any
    // jsdom has no layout, so every scrollHeight is 0 and "the bottom" would
    // be indistinguishable from the top. Give the list some height.
    const heightSpy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(800)
    renderPanel()
    heightSpy.mockRestore()

    expect(scrollTo).toHaveBeenCalled()
    expect(scrollTo.mock.calls.at(-1)?.[0]).toMatchObject({ top: 0 })
  })

  // The rows and the header were click-only divs: nothing to tab to, nothing
  // to press, and no way for a screen reader to know the panel folds.
  it('opens a trace or an error from the keyboard', async () => {
    Element.prototype.scrollTo = (() => {}) as any
    const onTraceClick = vi.fn()
    const onErrorClick = vi.fn()
    const user = userEvent.setup()
    renderPanel({ onTraceClick, onErrorClick })

    screen.getByRole('button', { name: /processed/ }).focus()
    await user.keyboard('{Enter}')
    expect(onTraceClick).toHaveBeenCalledWith('m-42')

    screen.getByRole('button', { name: /sink refused the row/ }).focus()
    await user.keyboard(' ')
    expect(onErrorClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'l2' }))
  })

  it('folds from the keyboard and says whether it is open', async () => {
    Element.prototype.scrollTo = (() => {}) as any
    const user = userEvent.setup()
    renderPanel()

    const header = screen.getByRole('button', { name: /live workflow logs/i })
    expect(header).toHaveAttribute('aria-expanded', 'true')
    header.focus()
    await user.keyboard('{Enter}')
    expect(useWorkflowStore.getState().logsOpened).toBe(false)
    expect(screen.getByRole('button', { name: /live workflow logs/i })).toHaveAttribute('aria-expanded', 'false')
  })
})
