/// <reference types="node" />
// node builtins are referenced here rather than added to tsconfig.app.json's
// `types`, for the reason matchesConditionParity.test.ts gives.

import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  GUIDE_KINDS, LIMITS, checklistBlocker, dataGuide, guideMarkdown, guideTemplates, missingMappings, predictChecklist,
  recommendChecklist, scenarioChecklist, DOC_BLOCKS, type GuideKind,
} from '@/lib/mlDataGuide'
import {
  NEAR_KM, NEAR_PEERS, NEW_DAYS, ROLES, SCENARIOS, nearbyApplies, newDaysApplies, scenarioRoles, type RecommendCheck, type Scenario,
} from '@/lib/mlRecommend'
import type { TrainCheck } from '@/lib/mlModels'

/**
 * "What data do I need?": one guide per model kind, from one source of truth
 * that the console, the docs and the checklist share. The thresholds are read
 * back from the Go and Python code that enforces them, so the guide cannot
 * drift from what training does.
 */

function repoFile(rel: string): string {
  let dir = process.cwd()
  for (let i = 0; i < 6; i++) {
    const candidate = resolve(dir, rel)
    if (existsSync(candidate)) return candidate
    dir = resolve(dir, '..')
  }
  throw new Error(`could not find ${rel} walking up from ${process.cwd()}`)
}

const read = (rel: string) => readFileSync(repoFile(rel), 'utf8')

/** A constant's value in Go (`name = 1_000`) or Python (`NAME = 1000`), underscores dropped. */
function constant(src: string, name: string): number {
  const m = src.match(new RegExp(`^\\s*(?:const\\s+)?${name}\\s*=\\s*([0-9_]+)(?:\\s*<<\\s*(\\d+))?`, 'm'))
  if (!m) throw new Error(`no constant ${name}`)
  const n = Number(m[1].replaceAll('_', ''))
  return m[2] ? n * 2 ** Number(m[2]) : n
}

describe('the thresholds are the code\'s', () => {
  const scenarios = read('ml/worker/hermod_ml/scenarios.py')
  const recommend = read('ml/worker/hermod_ml/recommend.py')
  const precheck = read('ml/worker/hermod_ml/precheck.py')
  const builtinData = read('pkg/ml/builtin/data.go')
  const builtinRec = read('pkg/ml/builtin/recommend.go')
  const builtinEngine = read('pkg/ml/builtin/engine.go')

  it('predicting: rows, few rows, classes', () => {
    expect(LIMITS.minRows).toBe(constant(precheck, 'MIN_ROWS'))
    expect(LIMITS.minRows).toBe(constant(builtinData, 'minRows'))
    expect(LIMITS.fewRows).toBe(constant(precheck, 'FEW_ROWS'))
    expect(LIMITS.fewRows).toBe(constant(builtinData, 'fewRows'))
    expect(LIMITS.maxAutoClasses).toBe(constant(precheck, 'MAX_AUTO_CLASSES'))
    expect(LIMITS.maxAutoClasses).toBe(constant(builtinData, 'maxAutoClasses'))
  })

  it('recommending: interactions, customers, orders, history, products', () => {
    expect(LIMITS.minInteractions).toBe(constant(recommend, 'MIN_INTERACTIONS'))
    expect(LIMITS.minInteractions).toBe(constant(builtinRec, 'minInteractions'))
    expect(LIMITS.minCustomersProducts).toBe(constant(scenarios, 'MIN_CUSTOMERS_PRODUCTS'))
    expect(LIMITS.minCustomersTiming).toBe(constant(scenarios, 'MIN_CUSTOMERS_TIMING'))
    expect(LIMITS.minOrdersPerCustomerProducts).toBe(constant(scenarios, 'MIN_TX_PER_CUSTOMER_PRODUCTS'))
    expect(LIMITS.minOrdersPerCustomer).toBe(constant(scenarios, 'MIN_TX_PER_CUSTOMER'))
    expect(LIMITS.maxProducts).toBe(constant(scenarios, 'MAX_PRODUCTS'))
    expect(LIMITS.fallbackGapDays).toBe(Number(scenarios.match(/^FALLBACK_GAP = ([\d.]+)/m)?.[1]))
    const wanted = scenarios.match(/^WANTED_HISTORY_DAYS = \{"visits": 2 \* VISIT_PERIOD_DAYS, "reorder": 2 \* 90, "products": (\d+), "audience": (\d+), "similar": (\d+)\}/m)
    expect(wanted, 'WANTED_HISTORY_DAYS changed shape: update LIMITS.wantedHistoryDays').toBeTruthy()
    expect(LIMITS.wantedHistoryDays).toEqual({
      visits: 2 * constant(scenarios, 'VISIT_PERIOD_DAYS'), reorder: 180,
      products: Number(wanted?.[1]), audience: Number(wanted?.[2]), similar: Number(wanted?.[3]),
    })
  })

  it('the reorder window: likely until 1.5 gaps, lost at 3, at most 366 days', () => {
    const plan = read('pkg/ml/recommend/plan.go')
    expect(plan).toContain(`return 1 - clip01((ratio-${LIMITS.likelyUntilGaps})/${LIMITS.lostAtGaps - LIMITS.likelyUntilGaps})`)
    expect(LIMITS.maxWithinDays).toBe(constant(read('pkg/ml/recommend/scenario.go'), 'MaxWithinDays'))
  })

  it('datasets: the query cap, the built-in training cap, the blob and upload limits', () => {
    expect(LIMITS.defaultMaxQueryRows).toBe(constant(read('internal/ml/datasets.go'), 'DefaultMaxQueryRows'))
    expect(LIMITS.queryChunkRows).toBe(constant(read('internal/ml/datasets.go'), 'queryChunkRows'))
    expect(LIMITS.builtinMaxTrainRows).toBe(constant(builtinEngine, 'DefaultMaxTrainRows'))
    expect(LIMITS.uploadMaxBytes).toBe(constant(builtinEngine, 'DefaultMaxUploadBytes'))
    expect(LIMITS.uploadMaxBytes).toBe(constant(read('internal/ml/transport/http/training.go'), 'maxDatasetFile'))
    expect(LIMITS.blobMaxBytes).toBe(constant(read('internal/storage/ml_blobs.go'), 'MaxMLBlobBytes'))
  })
})

describe('every kind has a complete guide', () => {
  it.each(GUIDE_KINDS)('%s', (kind) => {
    const g = dataGuide(kind)
    expect(g.question).toMatch(/\?$/)
    expect(g.required.length).toBeGreaterThan(0)
    expect(g.minimums.length).toBeGreaterThan(0)
    expect(g.minimums.every((m) => m.required || m.recommended)).toBe(true)
    expect(g.mistakes.length).toBeGreaterThanOrEqual(3)
    expect(g.example.rows.length).toBeGreaterThanOrEqual(3)
    expect(g.example.rows.length).toBeLessThanOrEqual(5)
    for (const row of g.example.rows) expect(row).toHaveLength(g.example.header.length)
    // Every required column of the main table is in its example and its template.
    const main = g.required.filter((c) => c.table === g.example.table)
    for (const c of main) expect(g.example.header).toContain(c.header)
    const [first] = guideTemplates(kind)
    expect(first.csv.split('\n')[0]).toBe(g.example.header.join(','))
    expect(first.csv.split('\n')).toHaveLength(g.example.rows.length + 1)
    // The query reads the example's columns.
    for (const h of g.example.header) expect(g.query).toContain(h)
    expect(g.query).toMatch(/^SELECT /)
  })

  it('a scenario requires exactly the columns its training refuses without', () => {
    for (const s of SCENARIOS.map((x) => x.value)) {
      const want = scenarioRoles(s).filter((r) => r.required)
        .flatMap((r) => r.columns.filter((c) => c.required).map((c) => `${r.role}.${c.key}`)).sort()
      expect(dataGuide(s).required.map((c) => c.key).sort(), s).toEqual(want)
    }
  })

  it('a visit plan has a template for the customers table too, with the salesman', () => {
    const files = guideTemplates('visits')
    expect(files.map((f) => f.file)).toEqual(['visits-orders-template.csv', 'visits-customers-template.csv'])
    expect(files[1].csv.split('\n')[0]).toContain('salesman')
  })

  it('the thresholds are labelled required or recommended', () => {
    const rows = (k: GuideKind, id: string) => dataGuide(k).minimums.find((m) => m.id === id)
    expect(rows('classification', 'rows')).toMatchObject({ required: 'at least 10', recommended: '100 or more' })
    expect(rows('classification', 'answers')?.required).toBe('at least 2 different answers')
    expect(rows('regression', 'rows')).toMatchObject({ required: 'at least 10', recommended: '100 or more' })
    expect(rows('recommend', 'rows')?.required).toBe('at least 10')
    expect(rows('products', 'customers')).toMatchObject({ required: 'at least 2', recommended: '1,000 or more' })
    expect(rows('reorder', 'customers')).toMatchObject({ required: 'at least 2', recommended: '500 or more' })
    expect(rows('reorder', 'history')?.recommended).toBe('180 days (about 6 months) or more')
    expect(rows('visits', 'history')?.recommended).toBe('14 days (about 2 weeks) or more')
    expect(rows('similar', 'orders_per_customer')?.recommended).toBe('3 or more')
    expect(rows('reorder', 'product_days')?.recommended).toMatch(/2 or more different days/)
  })

  it('reorder explains due date, overdue and the next N days in words', () => {
    const terms = Object.fromEntries((dataGuide('reorder').terms ?? []).map((t) => [t.term, t.meaning]))
    expect(Object.keys(terms)).toEqual(['Usual gap', 'Due date', 'Overdue', 'Next N days', 'Products due', 'Enough history'])
    expect(terms['Usual gap']).toMatch(/only one order day.*usual gap of all customers.*30 days/s)
    expect(terms['Due date']).toMatch(/last order.*plus.*usual gap/)
    expect(terms['Overdue']).toMatch(/kept on the list/)
    expect(terms['Next N days']).toMatch(/1 to 366/)
    expect(terms['Next N days']).toMatch(/one and a half.*three/s)
    expect(terms['Products due']).toMatch(/two different days/)
  })
})

describe('customer category, size and coordinates', () => {
  const optional = (s: Scenario) => Object.fromEntries(dataGuide(s).optional.map((c) => [c.key, c]))

  it('are optional columns that improve products and audience, nearby customers first', () => {
    for (const s of ['products', 'audience'] as const) {
      const o = optional(s)
      expect(o['customers.segment']?.name, s).toBe('Customer category')
      expect(o['customers.size']?.name, s).toBe('Customer size')
      expect(o['customers.segment']?.why, s).toMatch(/few orders/)
      expect(o['customers.size']?.why, s).toMatch(/number.*small, medium and large/)
      expect(o['customers.lat']?.why, s).toMatch(/nearby/)
      expect(o['customers.lon']?.why, s).toMatch(/nearby/)
      expect(dataGuide(s).required.map((c) => c.key), s).not.toContain('customers.segment')
    }
  })

  it('are carried on, and filter, every list of customers', () => {
    for (const s of ['visits', 'reorder'] as const) {
      expect(optional(s)['customers.segment']?.why, s).toMatch(/filter/i)
      expect(optional(s)['customers.size']?.why, s).toMatch(/filter/i)
    }
    expect(optional('similar')['customers.segment']).toBeUndefined()
  })

  it('the nearby radius is the worker\'s', () => {
    const src = read('ml/worker/hermod_ml/scenarios.py')
    expect(NEAR_KM.default).toBe(Number(src.match(/^NEAR_KM = ([\d.]+)/m)?.[1]))
    expect(NEAR_PEERS).toBe(constant(src, 'NEAR_PEERS'))
    expect([NEAR_KM.min, NEAR_KM.max]).toEqual(src.match(/^NEAR_KM_BOUNDS = \(([\d.]+), ([\d.]+)\)/m)?.slice(1).map(Number))
    expect(nearbyApplies('products', { customers: { dataset: 'c', id: 'id', segment: 'kind', lat: 'la', lon: 'lo' } })).toBe(true)
    expect(nearbyApplies('products', { customers: { dataset: 'c', id: 'id', lat: 'la', lon: 'lo' } })).toBe(false)
    expect(nearbyApplies('reorder', { customers: { dataset: 'c', id: 'id', size: 's', lat: 'la', lon: 'lo' } })).toBe(false)
  })
})

describe('product details', () => {
  const optional = (s: Scenario) => Object.fromEntries(dataGuide(s).optional.map((c) => [c.key, c]))
  const details = ['brand', 'subcategory', 'pack_size', 'unit', 'launch_date', 'end_date', 'stock', 'substitute']

  it('are the worker\'s product columns', () => {
    const src = read('ml/worker/hermod_ml/scenarios.py')
    const keys = src.match(/"products": \(([^)]*)\)/)?.[1].match(/"(\w+)"/g)?.map((k) => k.slice(1, -1))
    expect(keys).toBeTruthy()
    // name is read when a products table has one; the console shows ids and names without a mapping.
    expect(scenarioRoles('products').find((r) => r.role === 'products')!.columns.map((c) => c.key))
      .toEqual(keys!.filter((k) => k !== 'name'))
  })

  it('are optional columns of products, audience and similar, each saying why', () => {
    for (const s of ['products', 'audience', 'similar'] as const) {
      const o = optional(s)
      for (const d of details) {
        expect(o[`products.${d}`]?.why, `${s} ${d}`).toBeTruthy()
        expect(dataGuide(s).required.map((c) => c.key), s).not.toContain(`products.${d}`)
      }
    }
    expect(optional('products')['products.brand']?.why).toMatch(/Buys a lot of/)
    expect(optional('products')['products.stock']?.why).toMatch(/substitute/)
    expect(optional('products')['products.pack_size']?.why).toMatch(/3 cartons \(40 pcs\)/)
    expect(optional('reorder')['products.brand']).toBeUndefined()
  })

  it('say how much data they need, and the new-product window is the worker\'s', () => {
    const src = read('ml/worker/hermod_ml/scenarios.py')
    const o = optional('products')
    expect(o['products.launch_date']?.why).toContain(`${constant(src, 'NEW_DAYS')} days`)
    expect(o['products.brand']?.why).toContain(`${Math.round(Number(src.match(/^BRAND_REASON = ([\d.]+)/m)?.[1]) * 100)}%`)
    for (const d of details) expect(o[`products.${d}`]?.why, d).toMatch(/Needs|needs|Works|works/)
  })
})

describe('price tier, channel and region', () => {
  const optional = (s: Scenario) => Object.fromEntries(dataGuide(s).optional.map((c) => [c.key, c]))
  const market = ['price_tier', 'channel', 'region']

  it('are the worker\'s customer columns', () => {
    const src = read('ml/worker/hermod_ml/scenarios.py')
    const keys = src.match(/"customers": \(([^)]*)\)/)?.[1].match(/"(\w+)"/g)?.map((k) => k.slice(1, -1))
    expect(ROLES.customers.columns.map((c) => c.key)).toEqual(keys!.filter((k) => k !== 'name'))
  })

  it('make peers in products and audience, and filter every list of customers', () => {
    for (const s of ['products', 'audience'] as const) {
      for (const m of market) expect(optional(s)[`customers.${m}`]?.why, `${s} ${m}`).toMatch(/peers/)
    }
    for (const s of ['visits', 'reorder'] as const) {
      for (const m of market) expect(optional(s)[`customers.${m}`]?.why, `${s} ${m}`).toMatch(/filter/i)
    }
    expect(optional('similar')['customers.price_tier']).toBeUndefined()
    expect(nearbyApplies('products', { customers: { dataset: 'c', id: 'id', channel: 'ch', lat: 'la', lon: 'lo' } })).toBe(true)
  })
})

describe('returns and quotes', () => {
  const optional = (s: Scenario) => Object.fromEntries(dataGuide(s).optional.map((c) => [c.key, c]))
  const src = read('ml/worker/hermod_ml/scenarios.py')
  const keys = (role: string) => src.match(new RegExp(`"${role}": \\(([^)]*)\\)`))?.[1].match(/"(\w+)"/g)?.map((k) => k.slice(1, -1))

  it('are the worker\'s roles and columns, the required ones its required ones', () => {
    expect(ROLES.returns.columns.map((c) => c.key)).toEqual(keys('returns'))
    expect(ROLES.quotes.columns.map((c) => c.key)).toEqual(keys('quotes'))
    const required = src.match(/^REQUIRED_KEYS[^=]*= \{([\s\S]*?)^\}/m)?.[1] ?? ''
    for (const role of ['returns', 'quotes'] as const) {
      const want = required.match(new RegExp(`"${role}": \\(([^)]*)\\)`))?.[1].match(/"(\w+)"/g)?.map((k) => k.slice(1, -1))
      expect(scenarioRoles('products').find((r) => r.role === role)!.columns.filter((c) => c.required).map((c) => c.key)).toEqual(want)
    }
  })

  it('are optional tables of products and audience, with the worker\'s share and window', () => {
    const share = `${Math.round(Number(src.match(/^RETURN_SHARE = ([\d.]+)/m)?.[1]) * 100)}%`
    const days = `${constant(src, 'QUOTE_DAYS')} days`
    for (const s of ['products', 'audience'] as const) {
      const o = optional(s)
      expect(o.returns?.why, s).toBeTruthy()
      expect(o.quotes?.why, s).toBeTruthy()
      expect(o['returns.quantity']?.why, s).toContain(share)
      expect(o['quotes.outcome']?.why, s).toContain(days)
      expect(dataGuide(s).required.map((c) => c.key), s).not.toContain('returns.customer')
    }
    expect(optional('products')['quotes.date']?.why).toMatch(/Quoted on/)
    expect(optional('reorder').returns).toBeUndefined()
  })

  it('the new-product window is asked within the worker\'s bounds', () => {
    expect(NEW_DAYS.default).toBe(constant(src, 'NEW_DAYS'))
    expect([NEW_DAYS.min, NEW_DAYS.max]).toEqual(src.match(/^NEW_DAYS_BOUNDS = \((\d+), (\d+)\)/m)?.slice(1).map(Number))
    expect(newDaysApplies('products', { products: { dataset: 'p', id: 'id', launch_date: 'l' } })).toBe(true)
    expect(newDaysApplies('similar', { products: { dataset: 'p', id: 'id', launch_date: 'l' } })).toBe(false)
  })
})

describe('the docs are generated from the guide', () => {
  // UPDATE_DOCS=1 bunx vitest run src/__tests__/mlDataGuide.test.ts rewrites them.
  it.each(Object.keys(DOC_BLOCKS))('%s', (rel) => {
    const path = repoFile(rel)
    const text = readFileSync(path, 'utf8')
    const begin = '<!-- data-guide:begin (generated by ui/src/lib/mlDataGuide.ts; do not edit by hand) -->'
    const end = '<!-- data-guide:end -->'
    const want = `${begin}\n${guideMarkdown(DOC_BLOCKS[rel])}\n${end}`
    const at = text.indexOf(begin)
    const to = text.indexOf(end)
    expect(at, `${rel} has no data-guide block`).toBeGreaterThanOrEqual(0)
    const got = text.slice(at, to + end.length)
    if (got !== want && process.env.UPDATE_DOCS) writeFileSync(path, text.slice(0, at) + want + text.slice(to + end.length))
    else expect(got).toBe(want)
  })
})

const trainCheck = (patch: Partial<TrainCheck>): TrainCheck => ({
  task: 'classification', target_kind: 'yes_no', classes: 2, rows: 500, usable_rows: 500, columns: [], warnings: [], ok: true, ...patch,
})

const status = (items: Array<{ id: string; status: string }>) => Object.fromEntries(items.map((i) => [i.id, i.status]))

describe('the checklist against a chosen dataset', () => {
  it('predict: good data is green', () => {
    const items = predictChecklist({ target: 'churned', learnFrom: 4, check: trainCheck({}) })
    expect(status(items)).toEqual({ columns: 'green', rows: 'green', answers: 'green' })
    expect(checklistBlocker(items)).toBeNull()
  })

  it('predict: nothing to predict or learn from is red, with the fix', () => {
    const items = predictChecklist({ target: null, learnFrom: 0, check: undefined })
    expect(status(items)).toEqual({ columns: 'red' })
    expect(checklistBlocker(items)).toBe('Pick the column to predict.')
    expect(checklistBlocker(predictChecklist({ target: 'y', learnFrom: 0, check: undefined }))).toBe('Tick at least one column to learn from.')
  })

  it('predict: too few rows is red, a few amber; one answer red, many or lopsided amber', () => {
    expect(status(predictChecklist({ target: 'y', learnFrom: 2, check: trainCheck({ usable_rows: 9, ok: false }) })).rows).toBe('red')
    expect(status(predictChecklist({ target: 'y', learnFrom: 2, check: trainCheck({ usable_rows: 99 }) })).rows).toBe('amber')
    const one = predictChecklist({ target: 'y', learnFrom: 2, check: trainCheck({ ok: false, warnings: [{ code: 'constant', message: 'x', column: 'y' }] }) })
    expect(status(one).answers).toBe('red')
    expect(checklistBlocker(one)).toMatch(/two different answers/)
    const lopsided = trainCheck({ warnings: [{ code: 'imbalanced', message: 'x', column: 'y' }] })
    expect(status(predictChecklist({ target: 'y', learnFrom: 2, check: lopsided })).answers).toBe('amber')
    const number = trainCheck({ task: 'regression', target_kind: 'number', warnings: [{ code: 'constant', message: 'x', column: 'y' }] })
    expect(status(predictChecklist({ target: 'y', learnFrom: 2, check: number })).answers).toBe('amber')
  })

  it('recommend: the person and item columns, and at least 10 rows', () => {
    expect(status(recommendChecklist({ rows: 5000, user: 'u', item: 'i', time: 't' }))).toEqual({ columns: 'green', rows: 'green', time: 'green' })
    const none = recommendChecklist({ rows: 5000, user: null, item: 'i', time: null })
    expect(checklistBlocker(none)).toBe('Pick the column that says who (the person) and the one that says what (the item).')
    const small = recommendChecklist({ rows: 6, user: 'u', item: 'i', time: null })
    expect(status(small)).toEqual({ columns: 'green', rows: 'red', time: 'amber' })
    expect(checklistBlocker(small)).toMatch(/at least 10/)
  })

  const okCheck: RecommendCheck = {
    ok: true,
    checks: [{ code: 'few_customers', status: 'green', message: '', fix: '' }],
    stats: { customers: 1200, products: 300, transactions: 50000, months_of_history: 13, tx_per_customer: 8 },
  }

  it('scenario: good data is green against the minimums', () => {
    const data = { transactions: { dataset: 'orders', customer: 'c', product: 'p', date: 'd' } }
    const items = scenarioChecklist('reorder', data, okCheck)
    expect(status(items)).toEqual({
      columns: 'green', types: 'green', transactions: 'green', customers: 'green', orders_per_customer: 'green', history: 'green', product: 'green',
    })
    expect(items.find((i) => i.id === 'customers')?.detail).toBe('You have 1,200. Required: at least 2. Recommended: 500 or more.')
  })

  it('scenario: a missing column is red and names it', () => {
    expect(missingMappings('visits', { transactions: { dataset: 'orders', customer: 'c' } as never }))
      .toEqual(['Orders or invoice lines: Date', 'Customers: a dataset'])
    const items = scenarioChecklist('visits', { transactions: { dataset: 'orders', customer: 'c', date: 'd' } }, undefined)
    expect(status(items)).toEqual({ columns: 'red' })
    expect(checklistBlocker(items)).toBe('Choose a dataset for Customers, and its Customer id and Salesman columns.')
  })

  it('scenario: too little data is amber or red with the numbers', () => {
    const small: RecommendCheck = {
      ok: false,
      checks: [{ code: 'bad_column_type', status: 'red', message: 'The amount holds text', fix: 'Pick a numeric column.' }],
      stats: { customers: 40, products: 12, transactions: 8, months_of_history: 1.5, tx_per_customer: 1.1 },
    }
    const items = scenarioChecklist('products', { transactions: { dataset: 'o', customer: 'c', product: 'p' } }, small)
    expect(status(items)).toMatchObject({
      types: 'red', transactions: 'red', customers: 'amber', orders_per_customer: 'amber', history: 'amber',
    })
    expect(checklistBlocker(items)).toMatch(/^Pick a column of the right kind/)
    // Without a product column, reorder still lists customers: amber, not red.
    const noProduct = scenarioChecklist('reorder', { transactions: { dataset: 'o', customer: 'c', date: 'd' } }, okCheck)
    expect(status(noProduct).product).toBe('amber')
    expect(checklistBlocker(noProduct)).toBeNull()
  })

  it('scenario: the server\'s status wins when it is worse, in the guide\'s words', () => {
    const check: RecommendCheck = {
      ...okCheck, ok: false,
      checks: [{ code: 'short_history', status: 'red', message: 'Every transaction is on the same day', fix: 'Load more than one day of transactions.' }],
    }
    const items = scenarioChecklist('reorder', { transactions: { dataset: 'o', customer: 'c', product: 'p', date: 'd' } }, check)
    expect(status(items).history).toBe('red')
    expect(checklistBlocker(items)).toBe('Load orders from more than one day; a longer history makes the answers better.')
  })
})
