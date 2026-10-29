import { describe, expect, it } from 'vitest'
import { connectionDetail, connectionState, liveStatusFor } from '@/components/connections/connectionModel'

describe('a connection row', () => {
  it('takes the worst live word across the workflows that use it', () => {
    const live = {
      wf1: { source_id: 's1', source_status: 'running', sink_statuses: { k1: 'running' } },
      wf2: { source_id: 's1', source_status: 'reconnecting', sink_id: 'k1', sink_status: 'error' },
    }
    expect(liveStatusFor(live, 'source', 's1')).toBe('reconnecting')
    expect(liveStatusFor(live, 'sink', 'k1')).toBe('error')
    expect(liveStatusFor(live, 'source', 'nope')).toBeNull()
  })

  it('reads Online, Offline, Reconnecting or Connecting', () => {
    expect(connectionState({ type: 'postgresql', active: true }, 'running')).toEqual({ tone: 'ok', label: 'Online' })
    expect(connectionState({ type: 'postgresql', active: true }, 'error: refused')).toEqual({ tone: 'err', label: 'Offline' })
    expect(connectionState({ type: 'postgresql', active: true }, 'reconnecting')).toEqual({ tone: 'warn', label: 'Reconnecting' })
    expect(connectionState({ type: 'postgresql', active: true, status: 'connecting' }, null)).toEqual({ tone: 'warn', label: 'Connecting' })
    expect(connectionState({ type: 'postgresql' }, null)).toEqual({ tone: 'off', label: 'Offline' })
    expect(connectionState({ type: 'postgresql', active: true }, null)).toEqual({ tone: 'ok', label: 'Online' })
  })

  it('says what it is and where it lives in one line', () => {
    expect(connectionDetail({ type: 'postgresql', vhost: 'prod', worker_id: 'w1' }, 'worker-eu', 'Sales ops')).toBe('postgresql · prod · worker-eu · Sales ops')
    expect(connectionDetail({ type: 'kafka' })).toBe('kafka · default')
  })
})
