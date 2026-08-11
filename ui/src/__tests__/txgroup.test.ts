import { describe, it, expect } from 'vitest'
import {
  parseMembers,
  formatMembers,
  eligibleSinks,
  ineligibleSinks,
  isValidPreparedAge,
  validateTxGroup,
  type SinkOption,
} from '@/utils/txgroup'

// A transactional group is a sink that names other sinks as members. Every rule
// here exists because breaking it produces a group that saves cleanly and then
// refuses to start when the workflow runs — which may be days later.

const sinks: SinkOption[] = [
  { id: 'pg1', name: 'Orders', type: 'postgres' },
  { id: 'pg2', name: 'Ledger', type: 'postgres' },
  { id: 'yb1', name: 'Analytics', type: 'yugabyte' },
  { id: 'k1', name: 'Events', type: 'kafka' },
  { id: 'my1', name: 'Legacy', type: 'mysql' },
  { id: 'grp', name: 'Existing group', type: 'txgroup' },
]

const capable = ['postgres', 'yugabyte']

describe('parseMembers', () => {
  it('reads the stored comma-separated form', () => {
    expect(parseMembers('a,b,c')).toEqual(['a', 'b', 'c'])
  })

  it('tolerates spacing and empty entries, which hand-edited JSON has', () => {
    expect(parseMembers(' a , , b ,')).toEqual(['a', 'b'])
  })

  it('treats a missing or non-string value as no members', () => {
    expect(parseMembers(undefined)).toEqual([])
    expect(parseMembers(null)).toEqual([])
    expect(parseMembers(42)).toEqual([])
  })

  it('round-trips through formatMembers', () => {
    expect(parseMembers(formatMembers(['a', 'b']))).toEqual(['a', 'b'])
  })
})

describe('eligibleSinks', () => {
  // The whole point of the picker. Only PostgreSQL and Yugabyte implement
  // two-phase commit; offering anything else builds a group that cannot start.
  it('offers only sinks that can take part in two-phase commit', () => {
    expect(eligibleSinks(sinks, capable).map((s) => s.id)).toEqual(['pg1', 'pg2', 'yb1'])
  })

  it('never offers the group being edited', () => {
    expect(eligibleSinks(sinks, capable, 'pg1').map((s) => s.id)).toEqual(['pg2', 'yb1'])
  })

  it('never offers another group, which the coordinator does not model', () => {
    expect(eligibleSinks(sinks, capable).some((s) => s.type === 'txgroup')).toBe(false)
  })

  it('offers nothing when the backend reports no capable types, rather than guessing', () => {
    expect(eligibleSinks(sinks, [])).toEqual([])
  })
})

describe('ineligibleSinks', () => {
  // Someone who just created a Kafka sink and cannot find it needs to know why,
  // or they cannot tell a missing feature from their own mistake.
  it('names what was left out so its absence is explained', () => {
    expect(ineligibleSinks(sinks, capable).map((s) => s.id)).toEqual(['k1', 'my1'])
  })

  it('does not count the group itself as something excluded', () => {
    expect(ineligibleSinks(sinks, capable).some((s) => s.type === 'txgroup')).toBe(false)
  })
})

describe('isValidPreparedAge', () => {
  it('accepts Go durations', () => {
    for (const d of ['15m', '1h', '90s', '1h30m', '500ms']) {
      expect(isValidPreparedAge(d)).toBe(true)
    }
  })

  it('accepts empty, because the engine supplies a default', () => {
    expect(isValidPreparedAge('')).toBe(true)
    expect(isValidPreparedAge('   ')).toBe(true)
  })

  it('rejects what the engine would reject at startup', () => {
    for (const d of ['15', 'fifteen minutes', '15 m', '-5m', '15min']) {
      expect(isValidPreparedAge(d)).toBe(false)
    }
  })
})

describe('validateTxGroup', () => {
  it('passes a group of two real members', () => {
    expect(validateTxGroup({ members: 'pg1,pg2' }, sinks)).toEqual([])
  })

  it('refuses fewer than two members', () => {
    const problems = validateTxGroup({ members: 'pg1' }, sinks)
    expect(problems).toHaveLength(1)
    expect(problems[0].field).toBe('members')
    expect(problems[0].message).toContain('at least two')
  })

  it('refuses an empty group', () => {
    expect(validateTxGroup({}, sinks)[0].field).toBe('members')
  })

  // A member deleted after the group was built is the case that turns a working
  // workflow into a broken one without anybody editing it.
  it('names members that no longer exist', () => {
    const problems = validateTxGroup({ members: 'pg1,gone' }, sinks)
    expect(problems.some((p) => p.message.includes('gone'))).toBe(true)
  })

  it('reports a malformed duration against its own field', () => {
    const problems = validateTxGroup({ members: 'pg1,pg2', max_prepared_age: 'soon' }, sinks)
    expect(problems).toHaveLength(1)
    expect(problems[0].field).toBe('max_prepared_age')
  })

  it('reports every problem at once rather than one per save', () => {
    const problems = validateTxGroup({ members: 'pg1', max_prepared_age: 'soon' }, sinks)
    expect(problems.map((p) => p.field).sort()).toEqual(['max_prepared_age', 'members'])
  })
})
