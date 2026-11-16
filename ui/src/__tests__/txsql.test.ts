import { describe, it, expect } from 'vitest'
import {
  parseStatements,
  formatStatements,
  parseSteps,
  formatSteps,
  statementProblems,
  statementWarnings,
  returnedColumns,
  referenceHints,
  stepProblems,
  discoveryType,
  newStatement,
  newStep,
} from '@/utils/txsql'

describe('the transactional SQL statement list', () => {
  it('round-trips through the stored JSON text', () => {
    const list = [newStatement(0), { ...newStatement(1), name: 'items', forEach: 'items' }]
    expect(parseStatements(formatStatements(list))).toEqual(list)
  })

  it('reads a missing or broken list as empty rather than throwing', () => {
    expect(parseStatements(undefined)).toEqual([])
    expect(parseStatements('not json')).toEqual([])
    expect(parseSteps('{"a":1}')).toEqual([])
  })

  it('leaves out the form-only keys when it stores a statement', () => {
    const text = formatStatements([{ name: 'a', sql: 'SELECT 1', forEach: '', as: '', expect: '' }])
    expect(JSON.parse(text)).toEqual([{ name: 'a', sql: 'SELECT 1' }])
  })
})

describe('the statement checks match what the server refuses', () => {
  it('refuses a reference to a statement that runs later, naming where to move it', () => {
    const problems = statementProblems([
      { name: 'a', sql: 'UPDATE t SET x = {{b.id}}' },
      { name: 'b', sql: 'INSERT INTO t (y) VALUES (1) RETURNING id' },
    ])
    expect(problems[0]).toMatch(/runs later/)
    expect(problems[1]).toBeUndefined()
  })

  it('refuses a duplicate name, a bad name, empty SQL and schema changes', () => {
    const problems = statementProblems([
      { name: 'a', sql: 'SELECT 1' },
      { name: 'a', sql: 'SELECT 2' },
      { name: 'Bad Name', sql: 'SELECT 3' },
      { name: 'c', sql: '   ' },
      { name: 'd', sql: 'DROP TABLE orders' },
      { name: 'e', sql: 'SELECT 1; SELECT 2' },
    ])
    expect(problems[1]).toMatch(/more than once/)
    expect(problems[2]).toMatch(/lower-case/)
    expect(problems[3]).toMatch(/no SQL/)
    expect(problems[4]).toMatch(/schema changes/)
    expect(problems[5]).toMatch(/more than one statement/)
  })

  it('refuses a reference to a statement that runs once per item', () => {
    const problems = statementProblems([
      { name: 'items', sql: 'INSERT INTO t (a) VALUES ({{item}}) RETURNING id', forEach: 'list' },
      { name: 'b', sql: 'UPDATE t SET x = {{items.id}}' },
    ])
    expect(problems[1]).toMatch(/once per item/)
  })
})

describe('the statement warnings', () => {
  it('warns about DO NOTHING with RETURNING, which hands back no row on a conflict', () => {
    const w = statementWarnings({ name: 'a', sql: 'INSERT INTO t (a) VALUES ({{a}}) ON CONFLICT DO NOTHING RETURNING id' })
    expect(w.join(' ')).toMatch(/DO NOTHING/)
  })

  it('warns that a plain insert is repeated on redelivery, and not an upsert', () => {
    expect(statementWarnings({ name: 'a', sql: 'INSERT INTO t (a) VALUES ({{a}})' }).join(' ')).toMatch(/redelivered/)
    expect(statementWarnings({ name: 'a', sql: 'INSERT INTO t (a) VALUES ({{a}}) ON CONFLICT (a) DO UPDATE SET a = EXCLUDED.a' })).toEqual([])
    expect(statementWarnings({ name: 'a', sql: 'INSERT INTO t (a) VALUES ({{a}}) ON DUPLICATE KEY UPDATE a = VALUES(a)' })).toEqual([])
    expect(statementWarnings({ name: 'a', sql: 'UPDATE t SET a = 1' })).toEqual([])
  })
})

describe('the columns a later statement can use', () => {
  it('reads RETURNING and OUTPUT lists', () => {
    expect(returnedColumns('INSERT INTO t (a) VALUES (1) RETURNING id, created_at')).toEqual(['id', 'created_at'])
    expect(returnedColumns('INSERT INTO t (a) OUTPUT INSERTED.id, inserted.[code] VALUES (1)')).toEqual(['id', 'code'])
    expect(returnedColumns('UPDATE t SET a = 1')).toEqual([])
  })

  it('offers each earlier statement’s columns, and the MySQL insert id', () => {
    const list = [
      { name: 'order', sql: 'INSERT INTO orders (a) VALUES (1) RETURNING id' },
      { name: 'items', sql: 'INSERT INTO items (a) VALUES (1) RETURNING id', forEach: 'items' },
      { name: 'audit', sql: 'UPDATE c SET x = 1' },
    ]
    expect(referenceHints(list, 2, 'postgres')).toEqual(['{{order.id}}'])
    expect(referenceHints([{ name: 'p', sql: 'INSERT INTO p (a) VALUES (1)' }, { name: 'c', sql: '' }], 1, 'mysql')).toEqual(['{{p.id}}'])
  })

  // Oracle hands column names back in capitals; the sink lower-cases them.
  it('offers Oracle columns lower-cased, as the sink reads them', () => {
    const list = [{ name: 'p', sql: 'INSERT INTO p (a) VALUES (1) RETURNING ID, "Created_At"' }, { name: 'c', sql: '' }]
    expect(referenceHints(list, 1, 'oracle')).toEqual(['{{p.id}}', '{{p.created_at}}'])
  })
})

describe('the Guided step checks', () => {
  it('asks for a table, a column, and a key to match on before it can upsert', () => {
    expect(stepProblems([{ ...newStep(0), table: '' }])[0]).toMatch(/table/i)
    expect(stepProblems([{ ...newStep(0), table: 'orders', columns: [] }])[0]).toMatch(/column/i)
    expect(stepProblems([{ ...newStep(0), table: 'orders', columns: [{ column: 'a', value: '{{a}}' }], match: [] }])[0]).toMatch(/match/i)
  })

  it('accepts a complete step and round-trips it', () => {
    const steps = [{ ...newStep(0), table: 'orders', match: ['a'], columns: [{ column: 'a', value: '{{a}}' }] }]
    expect(stepProblems(steps)).toEqual([undefined])
    expect(parseSteps(formatSteps(steps))).toEqual(steps)
  })

  it('wants each value to be one field', () => {
    const steps = [{ ...newStep(0), table: 'orders', match: ['a'], columns: [{ column: 'a', value: 'abc' }] }]
    expect(stepProblems(steps)[0]).toMatch(/one field/)
  })
})

describe('discovery', () => {
  it('asks the database sink of the same driver for its tables', () => {
    expect(discoveryType('postgres')).toBe('postgres')
    expect(discoveryType('sqlserver')).toBe('mssql')
    expect(discoveryType('')).toBeUndefined()
  })
})
