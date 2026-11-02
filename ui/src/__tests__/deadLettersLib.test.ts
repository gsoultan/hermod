import { describe, expect, it } from 'vitest'
import { attemptsLabel, checkEdit, deadLetterQuery, decodePayload, failedAtLabel, historyLine, sinceFor, spanLabel, STATUS_LABEL } from '@/lib/deadLetters'

describe('dead letter helpers', () => {
  it('names the fields an edit changed, nested ones by path', () => {
    const original = '{"id":"ord-1","total":"12,50","lines":[{"qty":2}],"gone":true}'
    const edited = '{"id":"ord-1","total":"12.50","lines":[{"qty":3}],"added":1}'
    const check = checkEdit(original, edited)
    expect(check.valid).toBe(true)
    expect(check.changes).toEqual([
      { path: 'added', before: undefined, after: 1 },
      { path: 'gone', before: true, after: undefined },
      { path: 'lines[0].qty', before: 2, after: 3 },
      { path: 'total', before: '12,50', after: '12.50' },
    ])
  })

  it('reports JSON that does not parse, and no changes for the original', () => {
    expect(checkEdit('{"a":1}', '{"a":').valid).toBe(false)
    expect(checkEdit('{"a":1}', '{\n  "a": 1\n}')).toEqual({ valid: true, changes: [] })
  })

  it('builds the list query from the filters, leaving out what is unset', () => {
    const now = new Date('2026-10-14T12:00:00Z')
    const q = deadLetterQuery({ search: ' total ', workflowId: 'wf-1', where: null, when: '24h', status: 'queued', vhost: 'all' }, 2, 25, now)
    expect(Object.fromEntries(q)).toEqual({
      search: 'total', workflow_id: 'wf-1', status: 'queued', since: '2026-10-13T12:00:00.000Z', limit: '25', offset: '50',
    })
    expect(sinceFor('any', now)).toBeUndefined()
    expect(sinceFor('7d', now)).toBe('2026-10-07T12:00:00.000Z')
  })

  it('decodes the payload the API sends as base64', () => {
    expect(decodePayload(btoa('{"total":"12,50"}'))).toBe('{"total":"12,50"}')
    expect(decodePayload(btoa(unescape(encodeURIComponent('{"name":"Zoë"}'))))).toBe('{"name":"Zoë"}')
    expect(decodePayload(undefined)).toBe('')
  })

  it('says where a message failed', () => {
    expect(failedAtLabel({ node_id: 'normalize' }, { normalize: 'Normalize order' })).toBe('Node: Normalize order')
    expect(failedAtLabel({ sink_id: 'snk-9' }, {})).toBe('Sink: snk-9')
  })
})

describe('grouped dead letter helpers', () => {
  it('labels the attempts a record stands for, one for a row from before counting', () => {
    expect(attemptsLabel({ failure_count: 263 })).toBe('×263')
    expect(attemptsLabel({})).toBe('×1')
  })

  it('says how long the attempts spanned, and nothing for a single attempt', () => {
    expect(spanLabel('2026-10-30T17:41:34Z', '2026-10-30T19:22:48Z')).toBe('over 1 h 41 min')
    expect(spanLabel('2026-10-30T17:41:34Z', '2026-10-30T17:42:04Z')).toBe('over 30 s')
    expect(spanLabel('2026-10-30T17:41:34Z', '2026-10-30T17:41:34Z')).toBe('')
    expect(spanLabel(undefined, '2026-10-30T17:41:34Z')).toBe('')
  })

  it('folds repeated failures into one history line', () => {
    expect(historyLine({ at: '2026-10-30T17:41:54Z', action: 'failed_again', count: 262, until: '2026-10-30T19:22:48Z', detail: 'invalid credentials' }))
      .toMatch(/^failed again ×262 until .+ · invalid credentials$/)
    expect(historyLine({ at: '2026-10-30T17:41:34Z', action: 'failed', detail: 'panmail' })).toBe('failed · panmail')
    expect(historyLine({ at: '2026-10-31T08:00:00Z', action: 'replayed', by: 'ada', detail: 'from the failed node' })).toBe('replayed by ada · from the failed node')
  })

  it('names every status the server can send', () => {
    expect(STATUS_LABEL.delivered).toBe('Delivered later')
    expect(STATUS_LABEL.merged).toBe('Merged')
  })
})
