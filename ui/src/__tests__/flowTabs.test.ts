import { describe, expect, it } from 'vitest'
import { FLOW_TABS, flowTab } from '@/pages/workflows/flowTabs'

describe('the seven tabs of a flow', () => {
  it('are Build, Runs, Traces, Logs, History, Debugger, Tuning, in that order', () => {
    expect(FLOW_TABS.map((t) => t.label)).toEqual(['Build', 'Runs', 'Traces', 'Logs', 'History', 'Debugger', 'Tuning'])
  })
  it('still answer to the old tab names, so links in notifications keep working', () => {
    expect(flowTab('graph')).toBe('build')
    expect(flowTab('executions')).toBe('runs')
    expect(flowTab('proposals')).toBe('tuning')
    expect(flowTab('opt')).toBe('tuning')
    expect(flowTab('debug')).toBe('debugger')
    expect(flowTab('runs')).toBe('runs')
    expect(flowTab(undefined)).toBe('build')
    expect(flowTab('nonsense')).toBe('build')
  })
})
