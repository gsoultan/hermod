import { describe, it, expect } from 'vitest'
import { describeError } from '@/lib/queryClient'

// A failed request must produce a sentence a human can act on. The failure mode
// this guards against is "[object Object]" reaching a notification.
describe('describeError', () => {
  it('passes through a plain string', () => {
    expect(describeError('boom')).toBe('boom')
  })

  it('uses Error.message', () => {
    expect(describeError(new Error('connection refused'))).toBe('connection refused')
  })

  it('reads the backend {error} shape', () => {
    expect(describeError({ error: 'sink is in use' })).toBe('sink is in use')
  })

  it('reads the {message} shape', () => {
    expect(describeError({ message: 'invalid config' })).toBe('invalid config')
  })

  it('prefers error over other keys', () => {
    expect(describeError({ error: 'primary', message: 'secondary' })).toBe('primary')
  })

  it('never returns [object Object]', () => {
    const result = describeError({ code: 500, nested: { a: 1 } })
    expect(result).not.toContain('[object Object]')
    expect(result.length).toBeGreaterThan(0)
  })

  it('handles null and undefined', () => {
    expect(describeError(null)).toBe('Unknown error')
    expect(describeError(undefined)).toBe('Unknown error')
  })

  it('ignores blank string fields', () => {
    expect(describeError({ error: '   ', message: 'real message' })).toBe('real message')
  })
})
