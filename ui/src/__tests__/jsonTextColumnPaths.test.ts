import { describe, it, expect } from 'vitest'
import { getValByPath, matchesCondition } from '@/utils/transformationUtils'

// A column can hold JSON *text* rather than a decoded object: a text/varchar
// column holding JSON, MariaDB's JSON (a LONGTEXT alias), or a body that
// arrived as a string. getValByPath has always parsed a string mid-path; the Go
// side only learned to in evaluator.GetValByPath, and these two are twins —
// a condition evaluated here in the editor and again in the engine has to reach
// the same answer, or the preview disagrees with the run.
//
// Pinned on this side too so that "the twins agree" is a test rather than a
// note, and so nobody optimises the parse away as dead weight.

const textColumn = '{"id":"reg-1","nested":{"deep":"yes"},"n":7}'

describe('a path into a JSON text column', () => {
  it('descends into the text', () => {
    expect(getValByPath({ payload: textColumn }, 'payload.id')).toBe('reg-1')
    expect(getValByPath({ payload: textColumn }, 'payload.nested.deep')).toBe('yes')
  })

  it('binds the text when the path ends there', () => {
    expect(getValByPath({ payload: textColumn }, 'payload')).toBe(textColumn)
  })

  it('does not descend into text that is not JSON', () => {
    expect(getValByPath({ note: 'just a note, not json' }, 'note.id')).toBeUndefined()
  })

  it('leaves a decoded object alone', () => {
    expect(getValByPath({ payload: { id: 'reg-1' } }, 'payload.id')).toBe('reg-1')
  })

  it('reaches a condition, which is what the engine evaluates against', () => {
    expect(
      matchesCondition({ payload: textColumn }, { field: 'payload.id', operator: '=', value: 'reg-1' } as any)
    ).toBe(true)
  })
})
