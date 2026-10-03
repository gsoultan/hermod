import { describe, it, expect } from 'vitest'
import { destinationColumns, rowColumns, sendsColumn, toggleColumn } from '@/components/workflow/Sink/fcmData'

describe('the fcm data chips', () => {
  it('offers the row columns, not the change event wrapped around them', () => {
    expect(
      rowColumns([
        { path: 'after', type: 'object' },
        { path: 'after.sku', type: 'string' },
        { path: 'before', type: 'object' },
        { path: 'metadata', type: 'object' },
        { path: 'sku', type: 'string' },
        { path: 'tags', type: 'array' },
        { path: 'total', type: 'number' },
      ]),
    ).toEqual(['sku', 'total'])
  })

  it('keeps the other keys when a column is added or taken out', () => {
    const start = '{"deeplink":"app://x"}'
    const added = toggleColumn(start, 'sku', true)!
    expect(JSON.parse(added)).toEqual({ deeplink: 'app://x', sku: '{{.sku}}' })
    expect(sendsColumn(added, 'sku')).toBe(true)
    expect(JSON.parse(toggleColumn(added, 'sku', false)!)).toEqual({ deeplink: 'app://x' })
  })

  // A key named like the column but holding something else is the operator's
  // own value, and the chip must not claim it.
  it('does not count a key of the same name holding another value', () => {
    expect(sendsColumn('{"sku":"fixed"}', 'sku')).toBe(false)
  })

  it('refuses to rewrite JSON the rows cannot represent', () => {
    expect(toggleColumn('{"nested":{"a":1}}', 'sku', true)).toBeNull()
    expect(toggleColumn('{not json', 'sku', true)).toBeNull()
  })
})

// The backend withholds the column a message is addressed by, because a
// registration token is a capability. A chip that offers it in one click undoes
// that for anyone who ticks every box.
describe('the column a message is addressed by', () => {
  it('is read out of the destination templates, however the field is spelled', () => {
    expect(destinationColumns(['{{.fcm_token}}', '', 'orders-{{ .after.region }}', '{{index . "push id"}}'])).toEqual([
      'fcm_token',
      'region',
      'push id',
    ])
  })
})
