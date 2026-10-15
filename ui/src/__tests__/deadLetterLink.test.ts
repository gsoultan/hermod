import { describe, expect, it } from 'vitest'
import { deadLetterPageFor, wantsDeadLetterQueue } from '@/pages/workflows/WorkflowEditor/utils/deadLetterLink'

// Dead-letter mail sent by an earlier release linked to the editor with
// ?dlq=open. The workflow's dead letters are on the Dead Letters page now, so
// the editor sends that link on there.
describe('dead letter link', () => {
  it('recognises the link', () => {
    expect(wantsDeadLetterQueue('?dlq=open')).toBe(true)
    expect(wantsDeadLetterQueue('?tab=settings&dlq=open')).toBe(true)
    expect(wantsDeadLetterQueue('')).toBe(false)
    expect(wantsDeadLetterQueue('?dlq=closed')).toBe(false)
  })

  it('points at the Dead Letters page for the workflow', () => {
    expect(deadLetterPageFor('wf 1')).toBe('/dead-letters?workflow_id=wf+1')
  })
})
