import { describe, it, expect } from 'vitest'
import { buildTemplates, keywordsFor } from '@/components/forms/sqlBuilder/templates'

// The builder offered fourteen SELECT keywords to a node whose whole purpose is
// to write, and nothing that knew the table the operator had just clicked on.
// A template is the statement for that table, with a {{ }} token per column:
// the Variables panel then says which of them the sample message answers.
const orders = {
  table: 'orders',
  columns: [
    { name: 'id', type: 'bigint', is_pk: true, is_identity: true },
    { name: 'code', type: 'text' },
    { name: 'amount', type: 'numeric' },
  ],
}

const sqlOf = (templates: { id: string; sql: string }[], id: string) =>
  templates.find((t) => t.id === id)?.sql

describe('write templates', () => {
  it('inserts every column the database does not generate, and returns the row', () => {
    const t = buildTemplates('write', { engine: 'postgres', ...orders })
    expect(sqlOf(t, 'insert')).toBe('INSERT INTO orders (code, amount)\nVALUES ({{.code}}, {{.amount}})')
    expect(sqlOf(t, 'insert-returning')).toBe(
      'INSERT INTO orders (code, amount)\nVALUES ({{.code}}, {{.amount}})\nRETURNING *'
    )
  })

  it('updates and deletes by the primary key', () => {
    const t = buildTemplates('write', { engine: 'postgres', ...orders })
    expect(sqlOf(t, 'update')).toBe('UPDATE orders\nSET code = {{.code}}, amount = {{.amount}}\nWHERE id = {{.id}}')
    expect(sqlOf(t, 'delete')).toBe('DELETE FROM orders\nWHERE id = {{.id}}')
  })

  it('upserts in the dialect of the database', () => {
    expect(sqlOf(buildTemplates('write', { engine: 'postgres', ...orders }), 'upsert')).toBe(
      'INSERT INTO orders (id, code, amount)\nVALUES ({{.id}}, {{.code}}, {{.amount}})\n' +
        'ON CONFLICT (id) DO UPDATE\nSET code = EXCLUDED.code, amount = EXCLUDED.amount'
    )
    expect(sqlOf(buildTemplates('write', { engine: 'mysql', ...orders }), 'upsert')).toBe(
      'INSERT INTO orders (id, code, amount)\nVALUES ({{.id}}, {{.code}}, {{.amount}})\n' +
        'ON DUPLICATE KEY UPDATE code = VALUES(code), amount = VALUES(amount)'
    )
  })

  // A template that cannot run is worse than no template.
  it('offers only what the database can run', () => {
    const ids = (engine: string) => buildTemplates('write', { engine, ...orders }).map((t) => t.id)
    expect(ids('mysql')).not.toContain('insert-returning')
    expect(ids('oracle')).not.toContain('insert-returning')
    expect(ids('oracle')).not.toContain('upsert')
    expect(ids('mssql')).not.toContain('upsert')
    expect(ids('mariadb')).toContain('insert-returning')
    expect(ids('sqlite')).toContain('upsert')
  })

  it('returns rows the SQL Server way', () => {
    expect(sqlOf(buildTemplates('write', { engine: 'mssql', ...orders }), 'insert-returning')).toBe(
      'INSERT INTO orders (code, amount)\nOUTPUT inserted.*\nVALUES ({{.code}}, {{.amount}})'
    )
  })

  // The token is the path the editor lists for that column, so a CDC sample's
  // `after.code` is used as it is offered rather than as a bare name that the
  // Variables panel would call missing.
  it('binds a column to the message field of the same name', () => {
    const t = buildTemplates('write', {
      engine: 'postgres',
      ...orders,
      fields: ['operation', 'after.code', 'amount', 'after.note.amount'],
    })
    expect(sqlOf(t, 'insert')).toBe('INSERT INTO orders (code, amount)\nVALUES ({{.after.code}}, {{.amount}})')
  })

  // {{.created_at}} with nothing behind it binds NULL, and an explicit NULL
  // beats the column's default: the template would have written rows with no
  // timestamp into a column that fills itself.
  it('leaves a column to its default unless the message has a value for it', () => {
    const audited = {
      table: 'orders',
      columns: [
        { name: 'code', type: 'text' },
        { name: 'status', type: 'text', default: "'new'::text" },
        { name: 'created_at', type: 'timestamptz', default: 'now()' },
      ],
    }
    expect(sqlOf(buildTemplates('write', { engine: 'postgres', ...audited }), 'insert')).toBe(
      'INSERT INTO orders (code)\nVALUES ({{.code}})'
    )
    expect(
      sqlOf(buildTemplates('write', { engine: 'postgres', ...audited, fields: ['code', 'status'] }), 'insert')
    ).toBe('INSERT INTO orders (code, status)\nVALUES ({{.code}}, {{.status}})')
  })

  it('falls back to id, then to the first column, when no key is known', () => {
    const noKey = { table: 'log', columns: [{ name: 'line' }, { name: 'id' }] }
    expect(sqlOf(buildTemplates('write', { engine: 'postgres', ...noKey }), 'delete')).toBe(
      'DELETE FROM log\nWHERE id = {{.id}}'
    )
    const noId = { table: 'log', columns: [{ name: 'line' }, { name: 'at' }] }
    expect(sqlOf(buildTemplates('write', { engine: 'postgres', ...noId }), 'delete')).toBe(
      'DELETE FROM log\nWHERE line = {{.line}}'
    )
  })

  it('is still a statement before any table is picked', () => {
    const t = buildTemplates('write', { engine: 'postgres' })
    expect(sqlOf(t, 'insert')).toBe(
      'INSERT INTO table_name (column_a, column_b)\nVALUES ({{.column_a}}, {{.column_b}})'
    )
  })
})

describe('read templates', () => {
  it('selects the table by its key', () => {
    const t = buildTemplates('read', { engine: 'postgres', ...orders })
    expect(sqlOf(t, 'select')).toBe('SELECT id, code, amount\nFROM orders\nWHERE id = {{.id}}')
    expect(t.map((x) => x.id)).not.toContain('insert')
  })
})

describe('keywords', () => {
  it('are the ones the statement kind uses', () => {
    expect(keywordsFor('write')).toEqual(expect.arrayContaining(['INSERT INTO', 'VALUES', 'RETURNING']))
    expect(keywordsFor('write')).not.toContain('GROUP BY')
    expect(keywordsFor('read')).toEqual(expect.arrayContaining(['SELECT', 'FROM', 'GROUP BY']))
  })
})
