import { describe, expect, it } from 'vitest'
import { datasetNameFromFile, formatBytes, parseCsvPreview } from '@/lib/mlFilePreview'
import { DATASET_NAME_PATTERN } from '@/lib/mlModels'

describe('a file before it is uploaded', () => {
  it('suggests a dataset name from the file name', () => {
    expect(datasetNameFromFile('customers.csv')).toBe('customers')
    expect(datasetNameFromFile('Sales Report (2024).xlsx')).toBe('Sales_Report_2024')
    expect(datasetNameFromFile('__.parquet')).toBe('')
    const long = datasetNameFromFile(`${'a'.repeat(300)}.csv`)
    expect(long).toHaveLength(128)
    for (const n of ['customers', 'Sales_Report_2024', long]) expect(DATASET_NAME_PATTERN.test(n)).toBe(true)
  })

  it('reads the header and first rows of a CSV, quotes and all', () => {
    const p = parseCsvPreview('id,name,city\r\n1,"Smith, Jo",Jakarta\n2,"say ""hi""",Bandung\n3,x,y\n', { maxRows: 2 })
    expect(p.columns).toEqual(['id', 'name', 'city'])
    expect(p.rows).toEqual([['1', 'Smith, Jo', 'Jakarta'], ['2', 'say "hi"', 'Bandung']])
    expect(p.delimiter).toBe(',')
  })

  it('finds a semicolon or tab delimiter and drops a line cut off by the read', () => {
    const p = parseCsvPreview('a;b\n1;2\n3;4\n5;', { truncated: true })
    expect(p.columns).toEqual(['a', 'b'])
    expect(p.rows).toEqual([['1', '2'], ['3', '4']])
    expect(parseCsvPreview('a\tb\n1\t2').delimiter).toBe('\t')
  })

  it('says nothing for an empty file', () => {
    expect(parseCsvPreview('')).toEqual({ columns: [], rows: [], delimiter: ',' })
  })

  it('writes sizes as people read them', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(5.5 * 2 ** 20)).toBe('5.5 MB')
  })
})
