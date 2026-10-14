import { vi } from 'vitest'
import { openDeadLetterQueueFromLink, wantsDeadLetterQueue } from '@/pages/workflows/WorkflowEditor/utils/deadLetterLink'

// The dead-letter mail links to /workflows/<id>/edit?dlq=open. The editor reads
// that and opens the queue's inspector on the workflow's dead-letter sink.
describe('wantsDeadLetterQueue', () => {
  it('is true for the link the dead-letter mail sends', () => {
    expect(wantsDeadLetterQueue('?dlq=open')).toBe(true)
    expect(wantsDeadLetterQueue('?tab=x&dlq=open')).toBe(true)
  })

  it('is false for any other address', () => {
    expect(wantsDeadLetterQueue('')).toBe(false)
    expect(wantsDeadLetterQueue('?dlq=closed')).toBe(false)
  })
})

describe('openDeadLetterQueueFromLink', () => {
  const sink = { id: 'snk-dlq', name: 'Failed orders', type: 'postgres', config: { table: 'dlq' } }

  it('opens the inspector on the dead-letter sink', async () => {
    const open = vi.fn()
    const fetchSink = vi.fn().mockResolvedValue(sink)

    const opened = await openDeadLetterQueueFromLink({ search: '?dlq=open', deadLetterSinkID: 'snk-dlq', fetchSink, open })

    expect(opened).toBe(true)
    expect(fetchSink).toHaveBeenCalledWith('snk-dlq')
    expect(open).toHaveBeenCalledWith({ id: 'snk-dlq', name: 'Failed orders', type: 'postgres', config: { table: 'dlq' } })
  })

  it('does nothing without the link', async () => {
    const open = vi.fn()
    const fetchSink = vi.fn()
    expect(await openDeadLetterQueueFromLink({ search: '', deadLetterSinkID: 'snk-dlq', fetchSink, open })).toBe(false)
    expect(fetchSink).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })

  it('does nothing for a workflow with no dead-letter sink', async () => {
    const open = vi.fn()
    const fetchSink = vi.fn()
    expect(await openDeadLetterQueueFromLink({ search: '?dlq=open', deadLetterSinkID: '', fetchSink, open })).toBe(false)
    expect(open).not.toHaveBeenCalled()
  })

  it('does nothing when the sink cannot be read', async () => {
    const open = vi.fn()
    const fetchSink = vi.fn().mockRejectedValue(new Error('403'))
    expect(await openDeadLetterQueueFromLink({ search: '?dlq=open', deadLetterSinkID: 'snk-dlq', fetchSink, open })).toBe(false)
    expect(open).not.toHaveBeenCalled()
  })
})
