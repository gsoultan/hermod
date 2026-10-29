import { describe, expect, it } from 'vitest'
import { feedParams, logDetail, logTitle, logTone } from '@/pages/activity/activityModel'

const names = { workflows: { wf1: 'Billing sync' }, sources: { s1: 'orders-db' }, sinks: { k1: 'snowflake' } }

describe('the activity feed model', () => {
  it('turns the filter into the query the logs endpoint already takes', () => {
    expect(feedParams({ view: 'everything', search: '', page: 1 })).toBe('page=1&limit=30&search=')
    expect(feedParams({ view: 'problems', search: 'sku', page: 2 })).toBe('page=2&limit=30&search=sku&level=ERROR')
    expect(feedParams({ view: 'warnings', search: '', page: 1 })).toBe('page=1&limit=30&search=&level=WARN')
    // The extra filters still narrow it, and an explicit level wins over the view.
    expect(feedParams({ view: 'everything', search: 'a b', page: 1, workflowId: 'wf1', level: 'DEBUG', action: 'stop' }))
      .toBe('page=1&limit=30&search=a%20b&workflow_id=wf1&level=DEBUG&action=stop')
  })

  it('colours a row by what it means: red fails, orange warns, green is a change', () => {
    expect(logTone({ level: 'ERROR' })).toBe('err')
    expect(logTone({ level: 'WARN' })).toBe('warn')
    expect(logTone({ level: 'INFO', action: 'update' })).toBe('ok')
    expect(logTone({ level: 'INFO' })).toBe('info')
    expect(logTone({ level: 'DEBUG' })).toBe('off')
  })

  it('names a row after the flow or connection it is about, by name', () => {
    expect(logTitle({ workflow_id: 'wf1', source_id: 's1' }, names)).toBe('Billing sync')
    expect(logTitle({ source_id: 's1' }, names)).toBe('orders-db')
    expect(logTitle({ sink_id: 'k1' }, names)).toBe('snowflake')
    // A flow that is gone still shows something to search for.
    expect(logTitle({ workflow_id: 'abcdef123456' }, names)).toBe('Flow abcdef12')
    expect(logTitle({}, names)).toBe('Hermod')
    // Changes to other things (a login, a virtual host) carry that thing's id in
    // workflow_id; they read as the person who made them, not as a flow.
    expect(logTitle({ workflow_id: 'user-1', action: 'login', username: 'admin' }, names)).toBe('admin')
    expect(logTitle({ workflow_id: 'vh-1', action: 'create' }, names)).toBe('Hermod')
    expect(logTitle({ workflow_id: 'wf1', action: 'update', username: 'admin' }, names)).toBe('Billing sync')
  })

  it('says who did a change after the message', () => {
    expect(logDetail({ message: 'Workflow updated', action: 'update', username: 'gembit' })).toBe('Workflow updated · by gembit')
    expect(logDetail({ message: 'Sink timed out' })).toBe('Sink timed out')
  })
})
