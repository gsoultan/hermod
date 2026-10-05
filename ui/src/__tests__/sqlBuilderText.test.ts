import { describe, it, expect } from 'vitest'
import { extractVariables, formatSQL } from '@/components/forms/sqlBuilder/sqlText'

describe('formatSQL', () => {
  it('breaks a select onto its clauses', () => {
    expect(formatSQL('select a from t where x = 1 and y = 2')).toBe(
      'SELECT a\nFROM t\nWHERE x = 1\nAND y = 2'
    )
  })

  // The builder is also where an execute_sql node's write is typed, and the
  // formatter only knew how a SELECT is laid out.
  it('lays out a write the same way', () => {
    expect(
      formatSQL('insert into t (a, b) values ({{.a}}, {{.b}}) on conflict (a) do update set b = excluded.b returning id')
    ).toBe(
      'INSERT INTO t (a, b)\nVALUES ({{.a}}, {{.b}})\nON CONFLICT (a) DO UPDATE\nSET b = excluded.b\nRETURNING id'
    )
    expect(formatSQL('update t set a = 1 where id = {{.id}}')).toBe('UPDATE t\nSET a = 1\nWHERE id = {{.id}}')
    expect(formatSQL('delete from t where id = {{.id}}')).toBe('DELETE FROM t\nWHERE id = {{.id}}')
  })

  // Formatting rewrote whatever matched a keyword, wherever it was. Inside a
  // string literal that is the data: 'salt and pepper' was written to the
  // database with a line break in it, and its double space collapsed.
  it('leaves string literals exactly as typed', () => {
    expect(formatSQL("insert into t (note) values ('salt  and pepper from here')")).toBe(
      "INSERT INTO t (note)\nVALUES ('salt  and pepper from here')"
    )
    expect(formatSQL("select 1 where a = 'it''s  or not'")).toBe("SELECT 1\nWHERE a = 'it''s  or not'")
  })

  it('leaves template tokens alone', () => {
    expect(formatSQL('select 1 where a = {{ .from.set }}')).toBe('SELECT 1\nWHERE a = {{ .from.set }}')
  })

  it('returns an empty statement as it is', () => {
    expect(formatSQL('   ')).toBe('   ')
  })
})

describe('extractVariables', () => {
  it('lists each token once, without its dot, in order of appearance', () => {
    expect(extractVariables('{{.b.c}} {{ .a }} {{.b.c}} {{bare}}')).toEqual(['b.c', 'a', 'bare'])
  })

  it('finds none in plain SQL', () => {
    expect(extractVariables('SELECT 1')).toEqual([])
  })
})
