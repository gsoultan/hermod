import { describe, it, expect } from 'vitest'
import {
  transactionOf,
  transactionNames,
  membersOf,
  commitTogetherProblems,
  requiresEveryMember,
  EVERY_MEMBER_KEY,
  type EditorNode,
} from '@/utils/commitTogether'

/**
 * Sink nodes that commit together.
 *
 * A sink node joins a transaction by naming it in its config. The editor's job
 * is to show who else is in it and to refuse, before the save does, the two
 * things the server refuses: a transaction with one connected member, and a
 * member whose database cannot hold a transaction open for another.
 */
const CAPABLE = ['postgres', 'yugabyte', 'mysql', 'mariadb']

const node = (id: string, type: string, extra: Record<string, any> = {}): EditorNode => ({
  id,
  type: 'sink',
  data: { label: `DB ${id}`, type, ref_id: `ref-${id}`, ...extra },
})

const GRAPH: EditorNode[] = [
  { id: 'S', type: 'source', data: { label: 'Orders', type: 'webhook', ref_id: 'src' } },
  node('A', 'postgres', { transaction: 'orders' }),
  node('B', 'mysql', { transaction: 'orders' }),
  node('C', 'postgres', { transaction: 'audit' }),
  node('D', 'kafka'),
]

describe('transactions in a workflow', () => {
  it('reads the name off a sink node and treats blanks as none', () => {
    expect(transactionOf({ transaction: ' orders ' })).toBe('orders')
    expect(transactionOf({ transaction: '   ' })).toBe('')
    expect(transactionOf({})).toBe('')
    expect(transactionOf({ transaction: 7 })).toBe('')
  })

  it('lists each transaction name once, in first-seen order', () => {
    expect(transactionNames(GRAPH)).toEqual(['orders', 'audit'])
  })

  it('lists the sink nodes of one transaction and nobody else', () => {
    expect(membersOf(GRAPH, 'orders').map((n) => n.id)).toEqual(['A', 'B'])
    expect(membersOf(GRAPH, 'nope')).toEqual([])
  })
})

describe('what stops a transaction from being saved', () => {
  it('finds nothing wrong with two capable members', () => {
    expect(commitTogetherProblems(GRAPH, 'orders', CAPABLE)).toEqual([])
  })

  it('needs a name', () => {
    const problems = commitTogetherProblems(GRAPH, '', CAPABLE)
    expect(problems).toHaveLength(1)
    expect(problems[0].kind).toBe('unnamed')
  })

  it('refuses a transaction with one member and says so by name', () => {
    const problems = commitTogetherProblems(GRAPH, 'audit', CAPABLE)
    expect(problems).toHaveLength(1)
    expect(problems[0].kind).toBe('alone')
    expect(problems[0].message).toMatch(/audit/)
  })

  it('names a member whose database cannot commit together', () => {
    const graph = [...GRAPH, node('K', 'kafka', { transaction: 'orders' })]
    const problems = commitTogetherProblems(graph, 'orders', CAPABLE)
    expect(problems).toHaveLength(1)
    expect(problems[0].kind).toBe('incapable')
    expect(problems[0].nodeId).toBe('K')
    expect(problems[0].message).toMatch(/DB K/)
    expect(problems[0].message).toMatch(/Kafka/i)
  })

  it('says nothing about types until the server has said which can', () => {
    const graph = [...GRAPH, node('K', 'kafka', { transaction: 'orders' })]
    expect(commitTogetherProblems(graph, 'orders', null)).toEqual([])
  })
})

describe('"Every database must get a row"', () => {
  it('is a setting of the transaction, on when any member says so', () => {
    const nodes = [node('A', 'postgres', { transaction: 'orders' }), node('B', 'mysql', { transaction: 'orders', [EVERY_MEMBER_KEY]: true })]
    expect(requiresEveryMember(nodes, 'orders')).toBe(true)
    expect(requiresEveryMember(nodes, 'audit')).toBe(false)
    expect(requiresEveryMember(GRAPH, 'orders')).toBe(false)
  })

  it('reads the server\'s text form too, and ignores a node outside the transaction', () => {
    expect(requiresEveryMember([node('A', 'postgres', { transaction: 'orders', [EVERY_MEMBER_KEY]: 'true' })], 'orders')).toBe(true)
    expect(requiresEveryMember([node('A', 'postgres', { [EVERY_MEMBER_KEY]: true })], 'orders')).toBe(false)
    expect(requiresEveryMember([node('A', 'postgres', { transaction: 'orders', [EVERY_MEMBER_KEY]: 'false' })], 'orders')).toBe(false)
  })
})
