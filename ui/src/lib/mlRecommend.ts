import { apiFetch } from '@/api'
import type { DatasetColumn, ModelVersion } from '@/lib/mlModels'

// ---------------------------------------------------------------------------
// Recommendation scenarios (ml-plan/phase9-contract.md): what a recommender is
// for, the datasets it reads by role, the health check, rules, serving with
// reasons, feedback, scheduled builds and dataset refresh.
// ---------------------------------------------------------------------------

/** What a recommender answers. Versions from before scenarios behave as "products". */
export type Scenario = 'visits' | 'products' | 'reorder' | 'audience' | 'similar'

export const SCENARIOS: Array<{ value: Scenario; label: string; example: string }> = [
  { value: 'visits', label: 'Customers to visit or call', example: "Next week's visit list for each salesman, most urgent first." },
  { value: 'products', label: 'Products to offer a customer', example: 'Cross-sell on a call: "shops like this one also buy…"' },
  { value: 'reorder', label: 'When will they buy again', example: 'List the customers likely to buy in the next 2 weeks or month, and warn when one who orders every 30 days has not ordered for 41.' },
  { value: 'audience', label: 'Who will want this product', example: 'The 200 customers to tell about the new product line.' },
  { value: 'similar', label: 'Similar products', example: 'Out of stock? Offer the closest substitute.' },
]

export const scenarioLabel = (s: Scenario) => SCENARIOS.find((o) => o.value === s)?.label ?? s

/** The datasets a recommender can read, each with a role. */
export type RoleKey = 'transactions' | 'customers' | 'products' | 'visits' | 'salesmen'

/** What kind of value a role column holds; it steers the automatic guess. */
type ColumnKind = 'id' | 'date' | 'number' | 'flag' | 'text'

export interface RoleColumn {
  /** The contract's key, such as "customer" or "visits_per_day". */
  key: string
  label: string
  hint: string
  kind: ColumnKind
}

export interface RoleSpec {
  label: string
  /** What rows of it look like, in words. */
  what: string
  columns: RoleColumn[]
  /** A few realistic rows, which are also the CSV template. */
  example: { header: string[]; rows: string[][] }
}

export const ROLES: Record<RoleKey, RoleSpec> = {
  transactions: {
    label: 'Orders or invoice lines',
    what: 'One row per product on an order or invoice: who bought what, when.',
    columns: [
      { key: 'customer', label: 'Customer', hint: 'The customer or account id.', kind: 'id' },
      { key: 'product', label: 'Product', hint: 'The product, SKU or article id.', kind: 'id' },
      { key: 'date', label: 'Date', hint: 'When it was ordered.', kind: 'date' },
      { key: 'quantity', label: 'Quantity', hint: 'How many.', kind: 'number' },
      { key: 'amount', label: 'Amount', hint: 'What it was worth.', kind: 'number' },
    ],
    example: {
      header: ['customer_id', 'product_id', 'order_date', 'quantity', 'amount'],
      rows: [
        ['C-1001', 'P-200', '2026-09-01', '12', '144.00'],
        ['C-1001', 'P-315', '2026-09-01', '3', '57.30'],
        ['C-1002', 'P-200', '2026-09-03', '24', '288.00'],
      ],
    },
  },
  customers: {
    label: 'Customers',
    what: 'One row per customer, with who looks after them and where they are.',
    columns: [
      { key: 'id', label: 'Customer id', hint: 'The same ids as in the orders.', kind: 'id' },
      { key: 'salesman', label: 'Salesman', hint: 'Who looks after the customer.', kind: 'id' },
      { key: 'territory', label: 'Territory', hint: 'Region, area or route.', kind: 'text' },
      { key: 'segment', label: 'Segment', hint: 'Tier, channel or type.', kind: 'text' },
      { key: 'lat', label: 'Latitude', hint: 'Plans nearby customers on the same day.', kind: 'number' },
      { key: 'lon', label: 'Longitude', hint: 'Plans nearby customers on the same day.', kind: 'number' },
      { key: 'blocked', label: 'Credit hold', hint: 'Yes when the customer must not be offered anything.', kind: 'flag' },
    ],
    example: {
      header: ['customer_id', 'salesman', 'territory', 'segment', 'latitude', 'longitude', 'credit_hold'],
      rows: [
        ['C-1001', 'REP-7', 'North', 'Gold', '52.3702', '4.8952', 'false'],
        ['C-1002', 'REP-7', 'North', 'Silver', '52.3584', '4.9113', 'false'],
        ['C-1003', 'REP-9', 'South', 'Bronze', '51.9244', '4.4777', 'true'],
      ],
    },
  },
  products: {
    label: 'Products',
    what: 'One row per product, with its category, price and whether it can be sold.',
    columns: [
      { key: 'id', label: 'Product id', hint: 'The same ids as in the orders.', kind: 'id' },
      { key: 'category', label: 'Category', hint: 'Product group or family.', kind: 'text' },
      { key: 'price', label: 'Price', hint: 'List price.', kind: 'number' },
      { key: 'margin', label: 'Margin', hint: 'Profit per unit or margin share.', kind: 'number' },
      { key: 'active', label: 'Active', hint: 'Yes when it can be sold (active, in stock).', kind: 'flag' },
    ],
    example: {
      header: ['product_id', 'category', 'price', 'margin', 'active'],
      rows: [
        ['P-200', 'Beverages', '12.00', '3.10', 'true'],
        ['P-315', 'Snacks', '19.10', '6.40', 'true'],
        ['P-322', 'Snacks', '8.50', '1.20', 'false'],
      ],
    },
  },
  visits: {
    label: 'Visits or calls',
    what: 'One row per visit or call: which customer, when, by whom, and how it went.',
    columns: [
      { key: 'customer', label: 'Customer', hint: 'The customer visited or called.', kind: 'id' },
      { key: 'date', label: 'Date', hint: 'When.', kind: 'date' },
      { key: 'salesman', label: 'Salesman', hint: 'Who visited.', kind: 'id' },
      { key: 'outcome', label: 'Outcome', hint: 'Such as ordered, no order, closed.', kind: 'text' },
    ],
    example: {
      header: ['customer_id', 'visit_date', 'salesman', 'outcome'],
      rows: [
        ['C-1001', '2026-09-02', 'REP-7', 'ordered'],
        ['C-1002', '2026-09-02', 'REP-7', 'no order'],
        ['C-1003', '2026-09-04', 'REP-9', 'closed'],
      ],
    },
  },
  salesmen: {
    label: 'Salesmen',
    what: 'One row per salesman, with how many visits they make a day and on which days.',
    columns: [
      { key: 'id', label: 'Salesman id', hint: 'The same ids as on the customers.', kind: 'id' },
      { key: 'visits_per_day', label: 'Visits per day', hint: 'How many visits fit in a day.', kind: 'number' },
      { key: 'working_days', label: 'Working days', hint: 'Such as mon,tue,wed,thu,fri.', kind: 'text' },
    ],
    example: {
      header: ['salesman', 'visits_per_day', 'working_days'],
      rows: [
        ['REP-7', '8', 'mon,tue,wed,thu,fri'],
        ['REP-9', '6', 'mon,tue,thu'],
      ],
    },
  },
}

/** A role as one scenario uses it: required or not, and which of its columns are. */
export interface ScenarioRole {
  role: RoleKey
  required: boolean
  /** Why this scenario wants it, in words. */
  why: string
  columns: Array<RoleColumn & { required: boolean }>
}

const PRODUCT_SCENARIOS: readonly Scenario[] = ['products', 'audience', 'similar']

/** Which columns of a role a scenario cannot do without. */
function requiredColumns(scenario: Scenario, role: RoleKey): string[] {
  switch (role) {
    case 'transactions': {
      const cols = ['customer']
      if (PRODUCT_SCENARIOS.includes(scenario)) cols.push('product')
      if (scenario === 'visits' || scenario === 'reorder') cols.push('date')
      return cols
    }
    case 'customers': return scenario === 'visits' ? ['id', 'salesman'] : ['id']
    case 'products': return ['id']
    case 'visits': return ['customer', 'date']
    case 'salesmen': return ['id']
  }
}

const SCENARIO_ROLES: Record<Scenario, Array<{ role: RoleKey; required: boolean; why: string }>> = {
  visits: [
    { role: 'transactions', required: true, why: 'Shows who is due to order and who is worth most.' },
    { role: 'customers', required: true, why: 'Says which salesman looks after each customer.' },
    { role: 'visits', required: false, why: 'Finds customers nobody has visited for a while.' },
    { role: 'salesmen', required: false, why: 'Gives each salesman their own visits per day and working days.' },
    { role: 'products', required: false, why: 'Spots products similar customers buy and this one does not.' },
  ],
  products: [
    { role: 'transactions', required: true, why: 'Shows who bought what, so Hermod learns what goes together.' },
    { role: 'products', required: false, why: 'Lets rules skip inactive products, prefer margin or mix categories.' },
    { role: 'customers', required: false, why: 'Lets rules stay inside a territory.' },
  ],
  reorder: [
    { role: 'transactions', required: true, why: 'Shows how often each customer usually orders.' },
    { role: 'customers', required: false, why: 'Lets rules skip customers on credit hold.' },
  ],
  audience: [
    { role: 'transactions', required: true, why: 'Shows who bought what, so Hermod finds customers like the buyers.' },
    { role: 'customers', required: false, why: 'Lets rules skip customers on credit hold.' },
    { role: 'products', required: false, why: 'Helps with products that have few sales yet.' },
  ],
  similar: [
    { role: 'transactions', required: true, why: 'Shows which products are bought by the same customers.' },
    { role: 'products', required: false, why: 'Lets rules skip inactive products or mix categories.' },
  ],
}

/** The roles a scenario reads, transactions first. */
export function scenarioRoles(scenario: Scenario): ScenarioRole[] {
  return SCENARIO_ROLES[scenario].map((r) => {
    const req = requiredColumns(scenario, r.role)
    return { ...r, columns: ROLES[r.role].columns.map((c) => ({ ...c, required: req.includes(c.key) })) }
  })
}

/** Column-name patterns per role column, tried in order; the first unused match wins. */
const NAME_HINTS: Record<RoleKey, Record<string, RegExp>> = {
  transactions: {
    customer: /cust|client|account|buyer|user|shop|outlet/i,
    product: /prod|sku|item|article|material|part/i,
    date: /date|time|_at$|^at$|day|when|^ts$/i,
    quantity: /qty|quant|units|pcs|pieces|count|volume/i,
    amount: /amount|total|revenue|value|sales|net|gross|price|turnover/i,
  },
  customers: {
    id: /cust|client|account|shop|outlet|^id$/i,
    salesman: /sales|rep|agent|seller|owner|manager/i,
    territory: /territ|region|area|zone|route|district/i,
    segment: /segment|tier|channel|class|type/i,
    lat: /^lat/i,
    lon: /^lon|^lng/i,
    blocked: /block|hold|credit|stop|suspend/i,
  },
  products: {
    id: /prod|sku|item|article|material|^id$/i,
    category: /cat|family|group|class|department/i,
    price: /price|list/i,
    margin: /margin|profit|gm/i,
    active: /active|stock|enabled|available|status/i,
  },
  visits: {
    customer: /cust|client|account|shop|outlet/i,
    date: /date|time|_at$|day|when/i,
    salesman: /sales|rep|agent|seller|owner/i,
    outcome: /outcome|result|status/i,
  },
  salesmen: {
    id: /rep|sales|agent|seller|^id$/i,
    visits_per_day: /per_day|perday|cap|visits/i,
    working_days: /work|days/i,
  },
}

const kindFits = (kind: ColumnKind, type: DatasetColumn['type']) =>
  kind === 'number' ? type === 'number' : kind === 'flag' ? type === 'bool' || type === 'string' || type === 'number' : true

/**
 * Hermod's first guess at which dataset column fills each role column: by
 * name, an exact name first, and by type (an amount must be a number). Each
 * dataset column is used once. The person confirms or changes the guesses.
 */
export function guessColumns(role: RoleKey, columns: DatasetColumn[]): Record<string, string> {
  const used = new Set<string>()
  const out: Record<string, string> = {}
  for (const c of ROLES[role].columns) {
    const fits = columns.filter((d) => !used.has(d.name) && kindFits(c.kind, d.type))
    const hit = fits.find((d) => d.name.toLowerCase() === c.key) ?? fits.find((d) => NAME_HINTS[role][c.key]?.test(d.name))
    if (hit) {
      out[c.key] = hit.name
      used.add(hit.name)
    }
  }
  return out
}

/** A role's example as a CSV file, header first. */
export function csvTemplate(role: RoleKey): string {
  const { header, rows } = ROLES[role].example
  return [header, ...rows].map((r) => r.join(',')).join('\n')
}

/** One role as sent: its dataset and the dataset column for each role column. */
export type RoleMapping = { dataset: string } & Record<string, string>
export type RecommendData = Partial<Record<RoleKey, RoleMapping>>

/** A serving rule (contract "Rules"). */
export interface Rule {
  kind: 'filter' | 'exclude' | 'boost' | 'diversity' | 'capacity' | 'pin' | 'block'
  field?: string
  op?: 'eq' | 'in' | 'neq' | 'nin' | 'gt' | 'lt'
  value?: unknown
  from_request?: 'salesman' | 'territory' | null
  days?: number
  weight?: number
  max_per?: number
  per_day?: number
  ids?: string[]
}

/** The rule sentences of the Rules step, as the person set them. */
export interface RuleChoices {
  ownCustomers: boolean
  territory: boolean
  excludeOn: boolean
  excludeDays: number
  skipBlocked: boolean
  onlyActive: boolean
  promoteIds: string[]
  preferMargin: boolean
  maxPerCategoryOn: boolean
  maxPerCategory: number
  visitsPerDay: number
  workingDays: string[]
}

export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const

/** What a boost adds to the score; the server normalises the field. */
const BOOST_WEIGHT = 0.2

export function defaultRuleChoices(scenario: Scenario): RuleChoices {
  const visits = scenario === 'visits'
  return {
    ownCustomers: visits,
    territory: false,
    excludeOn: visits,
    excludeDays: visits ? 7 : 30,
    skipBlocked: true,
    onlyActive: true,
    promoteIds: [],
    preferMargin: false,
    maxPerCategoryOn: false,
    maxPerCategory: 3,
    visitsPerDay: 8,
    workingDays: ['mon', 'tue', 'wed', 'thu', 'fri'],
  }
}

/** Which rule sentences a scenario offers, given what is mapped. */
export function ruleOptions(scenario: Scenario, data: RecommendData) {
  const has = (role: RoleKey, col: string) => !!data[role]?.[col]
  const offersProducts = scenario === 'products' || scenario === 'similar'
  return {
    ownCustomers: scenario === 'visits' && has('customers', 'salesman'),
    // Customer rules apply where the recommended things are customers.
    territory: scenario === 'visits' && has('customers', 'territory'),
    exclude: scenario !== 'similar' && scenario !== 'reorder',
    skipBlocked: scenario !== 'products' && scenario !== 'similar' && has('customers', 'blocked'),
    onlyActive: offersProducts && has('products', 'active'),
    promote: scenario !== 'reorder',
    preferMargin: offersProducts && has('products', 'margin'),
    maxPerCategory: offersProducts && has('products', 'category'),
    capacity: scenario === 'visits',
  }
}

/** The rules the choices stand for, in the contract's shape; nothing for a column that is not mapped. */
export function rulesFromChoices(scenario: Scenario, c: RuleChoices, data: RecommendData): Rule[] {
  const can = ruleOptions(scenario, data)
  const rules: Rule[] = []
  if (can.ownCustomers && c.ownCustomers) rules.push({ kind: 'filter', field: 'customers.salesman', op: 'eq', from_request: 'salesman' })
  if (can.territory && c.territory) rules.push({ kind: 'filter', field: 'customers.territory', op: 'eq', from_request: 'territory' })
  if (can.exclude && c.excludeOn && c.excludeDays > 0) rules.push({ kind: 'exclude', days: c.excludeDays })
  if (can.skipBlocked && c.skipBlocked) rules.push({ kind: 'filter', field: 'customers.blocked', op: 'neq', value: true })
  if (can.onlyActive && c.onlyActive) rules.push({ kind: 'filter', field: 'products.active', op: 'eq', value: true })
  if (can.promote && c.promoteIds.length) rules.push({ kind: 'boost', ids: c.promoteIds, weight: BOOST_WEIGHT })
  if (can.preferMargin && c.preferMargin) rules.push({ kind: 'boost', field: 'products.margin', weight: BOOST_WEIGHT })
  if (can.maxPerCategory && c.maxPerCategoryOn && c.maxPerCategory > 0) {
    rules.push({ kind: 'diversity', field: 'products.category', max_per: c.maxPerCategory })
  }
  if (can.capacity) {
    // The salesmen table's working days win over the default ones.
    rules.push(data.salesmen?.working_days
      ? { kind: 'capacity', per_day: c.visitsPerDay }
      : { kind: 'capacity', per_day: c.visitsPerDay, value: c.workingDays })
  }
  return rules
}

/** The visit-planning signals and how much each counts. */
export type WeightKey = 'reorder_due' | 'churn_risk' | 'value' | 'opportunity' | 'neglect'
export type Weights = Record<WeightKey, number>

export const DEFAULT_WEIGHTS: Weights = { reorder_due: 0.35, churn_risk: 0.2, value: 0.2, opportunity: 0.1, neglect: 0.15 }

export const WEIGHT_QUESTIONS: Array<{ key: WeightKey; question: string }> = [
  { key: 'reorder_due', question: 'How much should overdue orders count?' },
  { key: 'churn_risk', question: 'How much should a customer who may be leaving count?' },
  { key: 'value', question: 'How much should big customers count?' },
  { key: 'opportunity', question: 'How much should products similar customers buy, and this one does not, count?' },
  { key: 'neglect', question: 'How much should customers nobody visited for a while count?' },
]

/** The body of a scenario training (contract "Training"). */
export interface ScenarioTrainSpec {
  dataset: string
  target: ''
  task: 'recommendation'
  scenario: Scenario
  data: RecommendData
  weights?: Weights
  rules: Rule[]
  algorithm: string
  go_live: unknown
}

export type CheckStatus = 'green' | 'amber' | 'red'

/** What the data health check says (POST .../ml/recommend-check). */
export interface RecommendCheck {
  ok: boolean
  checks: Array<{ code: string; status: CheckStatus; message: string; fix: string }>
  stats: { customers: number; products: number; transactions: number; months_of_history: number; tx_per_customer: number }
}

const mlUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/ml`
const modelUrl = (vhost: string, name: string) => `${mlUrl(vhost)}/models/${encodeURIComponent(name)}`
const datasetUrl = (vhost: string, name: string) => `${mlUrl(vhost)}/datasets/${encodeURIComponent(name)}`

/** A route this server does not have: it is older than the scenarios. */
export const isMissingRoute = (error: unknown) => [404, 405, 501].includes((error as { status?: number })?.status ?? 0)

const postJSON = (url: string, body: unknown, signal?: AbortSignal) =>
  apiFetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal, silent: true })

/**
 * Checks the data of a scenario training before it runs. Answers null from a
 * server older than the check.
 */
export async function recommendCheck(
  vhost: string, body: { task: 'recommendation'; scenario: Scenario; data: RecommendData }, signal?: AbortSignal,
): Promise<RecommendCheck | null> {
  try {
    const res = await postJSON(`${mlUrl(vhost)}/recommend-check`, body, signal)
    return (await res.json()) as RecommendCheck
  } catch (error) {
    if (isMissingRoute(error)) return null
    throw error
  }
}

/**
 * Whether the server trains scenarios: it has the health check route. An
 * empty check is refused by a server that has it (400) and not found by one
 * that does not; anything else that fails is read as the older server too,
 * so the dialog falls back to the one-table recommender rather than break.
 */
export async function scenariosSupported(vhost: string, signal?: AbortSignal): Promise<boolean> {
  try {
    await postJSON(`${mlUrl(vhost)}/recommend-check`, { task: 'recommendation' }, signal)
    return true
  } catch (error) {
    const status = (error as { status?: number })?.status
    return !!status && !isMissingRoute(error)
  }
}

/** A product a reorder customer is due to buy again. */
export interface DueProduct {
  id: string
  due_date: string
  overdue?: boolean
  fields?: Record<string, unknown>
}

/**
 * One recommended thing, with its reasons (contract "Item"). A reorder item
 * has its due date, whether that date has passed, and the products due.
 */
export interface ScenarioItem {
  id: string
  score: number
  rank: number
  reasons?: Array<{ code: string; text: string }>
  fields?: Record<string, unknown>
  due_date?: string
  overdue?: boolean
  products?: DueProduct[]
}

/** Who or what a recommendation is for; the scenario decides which keys apply. */
export type RecommendFor =
  | { customer: string } | { product: string } | { salesman: string } | { customers: string[] } | { all_customers: true }

/** The windows a reorder question offers, in days: "who will buy in the next 2 weeks". */
export const DUE_WINDOWS: Array<{ days: number; label: string }> = [
  { days: 14, label: '2 weeks' },
  { days: 30, label: '1 month' },
]

/** The longest window a reorder call takes, in days. */
export const MAX_WITHIN_DAYS = 366

export interface RecommendRequest {
  for: RecommendFor
  period?: { from: string; to: string }
  /** For reorder: only the customers due to buy within this many days, overdue ones included. */
  within_days?: number
  k?: number
  rules?: Rule[]
  exclude_seen?: boolean
  respond?: 'sync' | 'async'
  callback_url?: string
}

export interface RecommendResponse {
  recommendation_id: string
  results: Array<{ for: string; items: ScenarioItem[]; plan?: Array<{ date: string; items: ScenarioItem[] }> }>
}

/** Asks a scenario model (POST .../models/{name}/recommend); a 404 means an older server. */
export async function recommendScenario(vhost: string, name: string, body: RecommendRequest): Promise<RecommendResponse> {
  const res = await postJSON(`${modelUrl(vhost, name)}/recommend`, body)
  return (await res.json()) as RecommendResponse
}

export type FeedbackOutcome = 'shown' | 'accepted' | 'dismissed' | 'visited' | 'ordered'

export async function postFeedback(
  vhost: string, name: string, recommendationId: string, events: Array<{ id: string; outcome: FeedbackOutcome; at?: string }>,
): Promise<void> {
  await postJSON(`${modelUrl(vhost, name)}/feedback`, { recommendation_id: recommendationId, events })
}

export interface FeedbackSummary {
  shown: number
  accepted: number
  dismissed: number
  visited: number
  ordered: number
  acceptance_rate: number
}

export const feedbackSummaryKey = (vhost: string, name: string, days: number) => ['ml-feedback', vhost, name, days] as const

/** What people did with the model's recommendations; null from an older server. */
export async function getFeedbackSummary(vhost: string, name: string, days: number, signal?: AbortSignal): Promise<FeedbackSummary | null> {
  try {
    const res = await apiFetch(`${modelUrl(vhost, name)}/feedback/summary?days=${days}`, { signal, silent: true })
    return (await res.json()) as FeedbackSummary
  } catch (error) {
    if (isMissingRoute(error)) return null
    throw error
  }
}

/** Who a build makes recommendations for. */
export type BuildFor = 'all_salesmen' | 'all_customers' | 'all_products'

/** A model's scheduled build into a table (PUT .../models/{name}/build). */
export interface BuildSchedule {
  schedule: string
  for: BuildFor
  period_days?: number
  within_days?: number
  k: number
  target: { source_id: string; table: string; mode: 'replace' | 'append' }
}

/** Who a scenario's build is for: each salesman's plan, each customer's list, or each product's. */
export const buildForScenario = (s: Scenario): BuildFor =>
  s === 'visits' ? 'all_salesmen' : s === 'audience' || s === 'similar' ? 'all_products' : 'all_customers'

export async function putBuild(vhost: string, name: string, build: BuildSchedule): Promise<void> {
  await apiFetch(`${modelUrl(vhost, name)}/build`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(build), silent: true,
  })
}

/** When a dataset refills itself from its database source (GET .../datasets/{name} refresh). */
export interface DatasetRefresh {
  schedule: string
  last_run?: string
  last_error?: string
  next_run?: string
}

export async function putDatasetRefresh(
  vhost: string, name: string, body: { schedule: string; source_id: string; query: string; max_rows: number },
): Promise<void> {
  await apiFetch(`${datasetUrl(vhost, name)}/refresh`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), silent: true,
  })
}

export async function clearDatasetRefresh(vhost: string, name: string): Promise<void> {
  await apiFetch(`${datasetUrl(vhost, name)}/refresh`, { method: 'DELETE', silent: true })
}

/** A baseline a scenario version is compared with. */
export type BaselineName = 'most_overdue' | 'biggest' | 'popular'

export const BASELINE_WORDS: Record<BaselineName, string> = {
  most_overdue: "the 'most overdue first' list",
  biggest: "the 'biggest customers first' list",
  popular: 'recommending the most popular products',
}

/** A recommendation version trained for a scenario: the fields the contract adds. */
export type ScenarioVersion = Omit<ModelVersion, 'samples'> & {
  scenario?: Scenario
  roles?: RecommendData
  baselines_named?: Array<{ name: BaselineName; metrics: Record<string, number> }>
  health?: RecommendCheck
  samples?: unknown[]
  rules?: Rule[]
  weights?: Weights
}

/** Whether a version was trained for a scenario rather than from one table. */
export const isScenarioVersion = (v: unknown): v is ScenarioVersion & { scenario: Scenario } =>
  !!v && typeof (v as ScenarioVersion).scenario === 'string' && !!(v as ScenarioVersion).scenario

/** The public route an application calls to ask a scenario model. */
export const recommendServingUrl = (vhost: string, name: string) =>
  `/api/ml/serve/${encodeURIComponent(vhost)}/${encodeURIComponent(name)}/recommend`

/** The request an application sends for a scenario, with placeholders for the ids. */
export function exampleRequest(scenario: Scenario, k = 10): RecommendRequest {
  switch (scenario) {
    case 'visits': return { for: { salesman: '<salesman id>' }, period: { from: '<YYYY-MM-DD>', to: '<YYYY-MM-DD>' }, k: 40 }
    case 'products': return { for: { customer: '<customer id>' }, k, exclude_seen: true }
    case 'reorder': return { for: { customer: '<customer id>' }, k }
    case 'audience': return { for: { product: '<product id>' }, k: 100 }
    case 'similar': return { for: { product: '<product id>' }, k }
  }
}

/** A date as YYYY-MM-DD in local time. */
export function isoDay(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** The next `days` days from tomorrow, the contract's default plan period. */
export function nextPeriod(days = 7, from = new Date()): { from: string; to: string } {
  const start = new Date(from.getFullYear(), from.getMonth(), from.getDate() + 1)
  const end = new Date(start.getFullYear(), start.getMonth(), start.getDate() + days - 1)
  return { from: isoDay(start), to: isoDay(end) }
}
