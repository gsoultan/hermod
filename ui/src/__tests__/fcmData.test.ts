import { describe, it, expect } from 'vitest'
import {
  columnToken,
  destinationColumns,
  rowFields,
  sendsColumn,
  setColumns,
  toggleColumn,
} from '@/components/workflow/Sink/fcmData'

const names = (fields: ReturnType<typeof rowFields>) => fields.map((field) => field.name)

describe('the fields the app data list offers', () => {
  it('offers the row columns, not the change event wrapped around them', () => {
    expect(
      names(
        rowFields([
          { path: 'after', type: 'object' },
          { path: 'after.sku', type: 'string' },
          { path: 'before', type: 'object' },
          { path: 'metadata', type: 'object' },
          { path: 'op', type: 'string' },
          { path: 'sku', type: 'string' },
          { path: 'total', type: 'number' },
        ]),
      ),
    ).toEqual(['sku', 'total'])
  })

  // A column added by a step upstream of a CDC source is only known as
  // after.<column>; it is a column all the same, and was missing from the list.
  it('offers a column the editor only knows inside the change event', () => {
    expect(
      names(
        rowFields([
          { path: 'after', type: 'object' },
          { path: 'after.sku', type: 'string' },
          { path: 'sku', type: 'string' },
          { path: 'after.discount', type: 'any (inferred)' },
        ]),
      ),
    ).toEqual(['sku', 'discount'])
  })

  // Wide tables lost every column after the thirtieth, and object and list
  // columns were never offered at all.
  it('offers every column, objects and lists included', () => {
    const wide = Array.from({ length: 45 }, (_, i) => ({ path: `c${i}`, type: 'string' }))
    expect(rowFields(wide)).toHaveLength(45)

    const fields = rowFields([
      { path: 'tags', type: 'array' },
      { path: 'address', type: 'object' },
      { path: 'address.city', type: 'string' },
    ])
    expect(fields).toEqual([
      { name: 'tags', type: 'array', composite: true },
      { name: 'address', type: 'object', composite: true },
    ])
  })

  // `op` and `source` are only envelope keys on a change event.
  it('keeps a plain row column that happens to share an envelope name', () => {
    expect(names(rowFields([{ path: 'source', type: 'string' }]))).toEqual(['source'])
  })
})

describe('the template a ticked field becomes', () => {
  it('reads a column the way the sink can parse it', () => {
    expect(columnToken({ name: 'sku', composite: false })).toBe('{{.sku}}')
    // A Go template cannot write .first name or .order-id.
    expect(columnToken({ name: 'first name', composite: false })).toBe('{{index . "first name"}}')
    expect(columnToken({ name: 'order-id', composite: false })).toBe('{{index . "order-id"}}')
    // An object column is sent as JSON, not as Go's map[k:v].
    expect(columnToken({ name: 'tags', composite: true })).toBe('{{json .tags}}')
    expect(columnToken({ name: 'line items', composite: true })).toBe('{{json (index . "line items")}}')
  })
})

describe('ticking fields', () => {
  const sku = { name: 'sku', composite: false }

  it('keeps the other keys when a column is added or taken out', () => {
    const start = '{"deeplink":"app://x"}'
    const added = toggleColumn(start, sku, true)!
    expect(JSON.parse(added)).toEqual({ deeplink: 'app://x', sku: '{{.sku}}' })
    expect(sendsColumn(added, sku)).toBe(true)
    expect(JSON.parse(toggleColumn(added, sku, false)!)).toEqual({ deeplink: 'app://x' })
  })

  it('counts a key the operator wrote by hand the same way', () => {
    expect(sendsColumn('{"sku":"{{ .sku }}"}', sku)).toBe(true)
    expect(sendsColumn('{"sku":"{{.after.sku}}"}', sku)).toBe(true)
  })

  // A key named like the column but holding something else is the operator's
  // own value, and the tick must not claim it.
  it('does not count a key of the same name holding another value', () => {
    expect(sendsColumn('{"sku":"fixed"}', sku)).toBe(false)
  })

  it('refuses to rewrite JSON the rows cannot represent', () => {
    expect(toggleColumn('{"nested":{"a":1}}', sku, true)).toBeNull()
    expect(toggleColumn('{not json', sku, true)).toBeNull()
  })

  it('selects and clears many at once, leaving other keys alone', () => {
    const fields = [sku, { name: 'tags', composite: true }]
    const all = setColumns('{"deeplink":"app://x"}', fields, true)!
    expect(JSON.parse(all)).toEqual({ deeplink: 'app://x', sku: '{{.sku}}', tags: '{{json .tags}}' })
    expect(JSON.parse(setColumns(all, fields, false)!)).toEqual({ deeplink: 'app://x' })
  })
})

// The backend withholds the column a message is addressed by, because a
// registration token is a capability.
describe('the column a message is addressed by', () => {
  it('is read out of the destination templates, however the field is spelled', () => {
    expect(destinationColumns(['{{.fcm_token}}', '', 'orders-{{ .after.region }}', '{{index . "push id"}}'])).toEqual([
      'fcm_token',
      'region',
      'push id',
    ])
  })
})
