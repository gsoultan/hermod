import { describe, expect, it } from 'vitest'
import { buildSelect, EMPTY_BUILT_QUERY, filterNeedsValue, quoteIdent, type BuiltQuery } from '@/lib/mlQueryBuilder'

const orders: BuiltQuery = { ...EMPTY_BUILT_QUERY, table: 'public.orders' }
const types = { id: 'integer', amount: 'numeric(10,2)', country: 'text', paid: 'boolean', note: 'varchar' }

describe('the dataset query builder', () => {
  it('quotes names the way each database does, segment by segment', () => {
    expect(quoteIdent('postgres', 'public.orders')).toBe('"public"."orders"')
    expect(quoteIdent('mysql', 'shop.orders')).toBe('`shop`.`orders`')
    expect(quoteIdent('sqlite', 'order`s')).toBe('`order``s`')
    expect(quoteIdent('mssql', 'dbo.Orders]')).toBe('[dbo].[Orders]]]')
    expect(quoteIdent('oracle', 'hr.employees')).toBe('"HR"."EMPLOYEES"')
    expect(quoteIdent('postgres', 'we"ird')).toBe('"we""ird"')
  })

  it('reads every column when none is picked, and only the picked ones otherwise', () => {
    expect(buildSelect('postgres', orders, types)).toBe('SELECT *\nFROM "public"."orders"')
    expect(buildSelect('postgres', { ...orders, columns: ['id', 'amount'] }, types))
      .toBe('SELECT "id", "amount"\nFROM "public"."orders"')
  })

  it('has no statement until a table is picked', () => {
    expect(buildSelect('postgres', EMPTY_BUILT_QUERY, types)).toBe('')
  })

  it('writes filters as literals the column type takes, and escapes text', () => {
    const q: BuiltQuery = {
      ...orders,
      filters: [
        { column: 'amount', op: 'gte', value: '10.5' },
        { column: 'country', op: 'eq', value: "Cote d'Ivoire" },
        { column: 'note', op: 'contains', value: 'gift' },
        { column: 'paid', op: 'eq', value: 'true' },
        { column: 'id', op: 'neq', value: '12; DROP TABLE orders' },
        { column: 'note', op: 'empty', value: '' },
      ],
    }
    expect(buildSelect('postgres', q, types)).toBe(
      'SELECT *\nFROM "public"."orders"\nWHERE "amount" >= 10.5\n  AND "country" = \'Cote d\'\'Ivoire\'\n'
      + '  AND "note" LIKE \'%gift%\'\n  AND "paid" = TRUE\n  AND "id" <> \'12; DROP TABLE orders\'\n  AND "note" IS NULL',
    )
  })

  it('skips a filter that is not filled in yet', () => {
    const q: BuiltQuery = { ...orders, filters: [{ column: '', op: 'eq', value: 'x' }, { column: 'country', op: 'eq', value: '' }] }
    expect(buildSelect('postgres', q, types)).toBe('SELECT *\nFROM "public"."orders"')
    expect(filterNeedsValue('empty')).toBe(false)
    expect(filterNeedsValue('eq')).toBe(true)
  })

  it('sorts and keeps the first rows in each dialect', () => {
    const q: BuiltQuery = { ...orders, columns: ['id'], sort: { column: 'id', desc: true }, limit: 500 }
    expect(buildSelect('postgres', q, types)).toBe('SELECT "id"\nFROM "public"."orders"\nORDER BY "id" DESC\nLIMIT 500')
    expect(buildSelect('mssql', { ...q, table: 'dbo.orders' }, types)).toBe('SELECT TOP 500 [id]\nFROM [dbo].[orders]\nORDER BY [id] DESC')
    expect(buildSelect('oracle', { ...q, table: 'orders' }, types)).toBe('SELECT "ID"\nFROM "ORDERS"\nORDER BY "ID" DESC\nFETCH FIRST 500 ROWS ONLY')
    expect(buildSelect('mssql', { ...orders, table: 'orders', filters: [{ column: 'paid', op: 'eq', value: 'false' }] }, types))
      .toBe('SELECT *\nFROM [orders]\nWHERE [paid] = 0')
  })
})
