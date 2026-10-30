import { describe, expect, it } from 'vitest'
import { EMPTY_STATS } from '@/hooks/useDashboardStream'
import { attentionItems, greeting, isFirstRun, statusLine } from '@/pages/home/homeModel'

const wf = (over: Partial<{ id: string; name: string; active: boolean; status: string }>) => ({
  id: 'wf', name: 'Flow', active: true, status: 'running', ...over,
})

describe('greeting', () => {
  it('follows the clock and uses the first name', () => {
    expect(greeting(new Date('2026-10-29T08:00:00'), 'gembit')).toBe('Good morning, gembit')
    expect(greeting(new Date('2026-10-29T13:00:00'), 'gembit')).toBe('Good afternoon, gembit')
    expect(greeting(new Date('2026-10-29T19:00:00'), 'gembit')).toBe('Good evening, gembit')
    expect(greeting(new Date('2026-10-29T19:00:00'), undefined)).toBe('Good evening')
  })
})

describe('status line', () => {
  it('says how many flows run and how fast, in words', () => {
    expect(statusLine({ ...EMPTY_STATS, active_workflows: 18, total_workflows: 21 }, 1184.4)).toBe('18 of 21 flows running · 1,184 messages a second')
    expect(statusLine({ ...EMPTY_STATS, active_workflows: 1, total_workflows: 1 }, 0.4)).toBe('1 of 1 flows running · 0.4 messages a second')
    expect(statusLine({ ...EMPTY_STATS, active_workflows: 0, total_workflows: 3 }, 0)).toBe('0 of 3 flows running · nothing moving')
  })
})

describe('what needs you', () => {
  it('lists a stopped flow first, with the way to fix it', () => {
    const items = attentionItems({
      workflows: [wf({ id: 'a', name: 'Orders to warehouse', status: 'error' }), wf({ id: 'b', name: 'Billing sync' })],
      stats: EMPTY_STATS,
      deadLettersQueued: 0,
      pendingApprovals: 0,
    })
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({
      tone: 'err',
      title: 'Orders to warehouse stopped',
      action: { label: 'Fix it', to: '/workflows/a' },
    })
  })

  it('counts dead letters, approvals and tripped sinks as things to do', () => {
    const items = attentionItems({
      workflows: [],
      stats: { ...EMPTY_STATS, circuit_breakers_open: 2, backpressure: 0.9 },
      deadLettersQueued: 12,
      pendingApprovals: 3,
    })
    expect(items.map((i) => i.title)).toEqual([
      '12 messages are waiting in Dead letters',
      '3 changes waiting for your approval',
      '2 sinks stopped accepting writes',
      'A sink buffer is 90% full',
    ])
    expect(items.map((i) => i.action.label)).toEqual(['Retry them', 'Review', 'See which', 'See which'])
    expect(items[1].action.to).toBe('/activity')
    expect(items[1].action.search).toEqual({ tab: 'approvals' })
  })

  it('is empty when everything runs', () => {
    expect(attentionItems({ workflows: [wf({})], stats: EMPTY_STATS, deadLettersQueued: 0, pendingApprovals: 0 })).toEqual([])
  })

  it('says one message, one change, one sink', () => {
    const items = attentionItems({ workflows: [], stats: { ...EMPTY_STATS, circuit_breakers_open: 1 }, deadLettersQueued: 1, pendingApprovals: 1 })
    expect(items.map((i) => i.title)).toEqual([
      '1 message is waiting in Dead letters',
      '1 change waiting for your approval',
      '1 sink stopped accepting writes',
    ])
  })
})

describe('first run', () => {
  it('is the state before any connection or flow exists', () => {
    expect(isFirstRun({ ...EMPTY_STATS }, [])).toBe(true)
    expect(isFirstRun({ ...EMPTY_STATS, total_sources: 1 }, [])).toBe(false)
    expect(isFirstRun({ ...EMPTY_STATS }, [wf({})])).toBe(false)
  })
})
