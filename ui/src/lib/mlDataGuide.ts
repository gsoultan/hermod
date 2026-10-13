import type { TrainCheck } from '@/lib/mlModels'
import {
  ROLES, SCENARIOS, csvTemplate, scenarioRoles, type CheckStatus, type RecommendCheck, type RecommendData, type RoleKey,
  type Scenario,
} from '@/lib/mlRecommend'

// ---------------------------------------------------------------------------
// "What data do I need?": for every kind of model Hermod trains, what it
// answers, the columns it needs and the ones that make it better, how much
// data is required and how much is recommended, an example with a CSV
// template, a query for a database, and the usual mistakes. The console's
// guide, the checklist against a chosen dataset and the data guides in docs/
// all come from here (mlDataGuide.test.ts rewrites the docs and reads every
// threshold back from the code that enforces it).
// ---------------------------------------------------------------------------

/** A kind of model: predict a category or a number, recommend from one table, or a recommendation scenario. */
export type GuideKind = 'classification' | 'regression' | 'recommend' | Scenario

export const GUIDE_KINDS: GuideKind[] = ['classification', 'regression', 'recommend', ...SCENARIOS.map((s) => s.value)]

const SCENARIO_KINDS: Scenario[] = SCENARIOS.map((s) => s.value)

export const isScenarioKind = (k: GuideKind): k is Scenario => (SCENARIO_KINDS as string[]).includes(k)

/**
 * The amounts the code enforces (required) or warns at (recommended), and the
 * limits on a dataset. Each names where it is enforced; the test reads them back.
 */
export const LIMITS = {
  /** precheck.py MIN_ROWS, pkg/ml/builtin/data.go minRows: fewer rows with an answer is refused. */
  minRows: 10,
  /** precheck.py FEW_ROWS, data.go fewRows: fewer trains, with a warning that the model is rough. */
  fewRows: 100,
  /** precheck.py MAX_AUTO_CLASSES, data.go maxAutoClasses: more answers than this is warned about. */
  maxAutoClasses: 20,
  /** recommend.py MIN_INTERACTIONS, pkg/ml/builtin/recommend.go minInteractions: fewer usable rows is refused. */
  minInteractions: 10,
  /** scenarios.py MIN_CUSTOMERS_PRODUCTS: customers advised for products, audience and similar. */
  minCustomersProducts: 1000,
  /** scenarios.py MIN_CUSTOMERS_TIMING: customers advised for visits and reorder. */
  minCustomersTiming: 500,
  /** scenarios.py MIN_TX_PER_CUSTOMER_PRODUCTS: orders (customer-days) per customer advised for product scenarios. */
  minOrdersPerCustomerProducts: 3,
  /** scenarios.py MIN_TX_PER_CUSTOMER: orders per customer advised for visits and reorder. */
  minOrdersPerCustomer: 2,
  /** scenarios.py MAX_PRODUCTS: at or above this many products, recommendations get thin. */
  maxProducts: 50_000,
  /** scenarios.py WANTED_HISTORY_DAYS: days of history each scenario wants. */
  wantedHistoryDays: { visits: 14, reorder: 180, products: 365, audience: 365, similar: 365 } as Record<Scenario, number>,
  /** scenarios.py FALLBACK_GAP: the usual gap when no customer has two order days. */
  fallbackGapDays: 30,
  /** pkg/ml/recommend/plan.go likelihood: an order is fully likely up to this many usual gaps... */
  likelyUntilGaps: 1.5,
  /** ...and no longer due, but lost, at this many. */
  lostAtGaps: 3,
  /** pkg/ml/recommend/scenario.go MaxWithinDays: the longest reorder window. */
  maxWithinDays: 366,
  /** internal/ml/datasets.go DefaultMaxQueryRows: a database read stops above this unless a cap is set. */
  defaultMaxQueryRows: 1_000_000,
  /** internal/ml/datasets.go queryChunkRows: rows sent at a time while a query is read. */
  queryChunkRows: 1000,
  /** pkg/ml/builtin/engine.go DefaultMaxTrainRows (HERMOD_ML_BUILTIN_MAX_ROWS): a bigger dataset is sampled. */
  builtinMaxTrainRows: 1_000_000,
  /** pkg/ml/builtin/engine.go DefaultMaxUploadBytes, internal/ml/transport/http/training.go maxDatasetFile. */
  uploadMaxBytes: 200 * 2 ** 20,
  /** internal/storage/ml_blobs.go MaxMLBlobBytes: the most one stored piece holds. */
  blobMaxBytes: 256 * 2 ** 20,
}

/** One column of a guide. `key` is the contract's name (a scenario's "transactions.customer"). */
export interface GuideColumn {
  key: string
  /** The table it belongs to, in words. */
  table: string
  name: string
  /** Its name in the example and the CSV template. */
  header: string
  means: string
  example: string
  /** For an optional column or table: what it adds. */
  why?: string
}

/** An amount, "required" when the code refuses less, "recommended" when it warns. */
export interface GuideMinimum {
  id: string
  what: string
  required?: string
  recommended?: string
}

export interface KindGuide {
  kind: GuideKind
  title: string
  /** The question the model answers, in one sentence. */
  question: string
  /** What one row of the main table is. */
  rows: string
  required: GuideColumn[]
  optional: GuideColumn[]
  minimums: GuideMinimum[]
  example: { table: string; header: string[]; rows: string[][] }
  mistakes: string[]
  /** Words the answers use, for reorder. */
  terms?: Array<{ term: string; meaning: string }>
  /** A query for the main table, reading the example's columns. */
  query: string
  /** The same for a big table: one row per customer, product and day. */
  bigQuery?: string
}

const fmt = (n: number) => n.toLocaleString('en-US')
const atLeast = (n: number) => `at least ${fmt(n)}`
const orMore = (n: number) => `${fmt(n)} or more`
const about = (days: number) => (days < 60 ? `${Math.round(days / 7)} weeks` : `${Math.round(days / 30.44)} months`)
const historyText = (days: number) => `${fmt(days)} days (about ${about(days)}) or more`

// -- predicting ---------------------------------------------------------------

const PREDICT_TABLE = 'Your table'

const predictFeatures = (target: string): GuideColumn[] => [
  { key: 'features', table: PREDICT_TABLE, name: 'Columns to learn from', header: 'tenure_months',
    means: `What you know about each row before the answer, such as months as a customer, plan or monthly spend. At least one besides ${target}.`,
    example: '14' },
]

const PREDICT_OPTIONAL: GuideColumn[] = [
  { key: 'more_features', table: PREDICT_TABLE, name: 'More columns about each row', header: 'support_calls', example: '3',
    means: 'Anything else known before the answer: counts, amounts, categories, dates turned into numbers.',
    why: 'Each column that relates to the answer makes the model more accurate. Hermod tells you which ones did not help.' },
]

const PREDICT_MISTAKES = [
  'An id column (customer id, invoice number) left in: it has a different value on almost every row and cannot help. Hermod unticks it for you.',
  'A column that gives the answer away, known only afterwards (such as "cancelled_on" when predicting who cancels). The model looks perfect and fails on new rows.',
  'Mostly empty columns: when more than 60% of a column is empty there is little to learn from.',
  'Several rows for the same thing: one row per customer (or per order, whatever you predict), not one per event.',
]

const CLASSIFICATION: KindGuide = {
  kind: 'classification',
  title: 'Predict a category',
  question: 'Which of a few answers fits each row, such as yes or no, or which plan a customer will pick?',
  rows: 'One row per thing you predict, such as one per customer, with the answer filled in for the past.',
  required: [
    { key: 'target', table: PREDICT_TABLE, name: 'The answer column', header: 'churned', example: 'yes',
      means: 'What you want Hermod to predict: text, yes/no, or a whole number with at most 20 values.' },
    ...predictFeatures('the answer'),
  ],
  optional: PREDICT_OPTIONAL,
  minimums: [
    { id: 'rows', what: 'Rows with an answer', required: atLeast(LIMITS.minRows), recommended: orMore(LIMITS.fewRows) },
    { id: 'answers', what: 'Different answers', required: 'at least 2 different answers', recommended: `at most ${LIMITS.maxAutoClasses}` },
    { id: 'balance', what: 'The rarest answer', recommended: 'at least a quarter as common as the most common one' },
    { id: 'columns', what: 'Columns to learn from', required: 'at least 1' },
  ],
  example: {
    table: PREDICT_TABLE,
    header: ['tenure_months', 'plan', 'monthly_spend', 'support_calls', 'churned'],
    rows: [['14', 'basic', '29.90', '3', 'yes'], ['48', 'pro', '79.00', '0', 'no'], ['6', 'basic', '29.90', '5', 'yes'], ['30', 'pro', '79.00', '1', 'no']],
  },
  mistakes: [
    'Every row has the same answer: there is nothing to tell apart. Include rows with the other answers too.',
    'Too many different answers (more than 20): group rare ones together, such as "other".',
    ...PREDICT_MISTAKES,
  ],
  query: 'SELECT tenure_months, plan, monthly_spend, support_calls, churned FROM customers',
}

const REGRESSION: KindGuide = {
  kind: 'regression',
  title: 'Predict a number',
  question: 'What number to expect for each row, such as next month\'s spend or days until delivery?',
  rows: 'One row per thing you predict, with the number filled in for the past.',
  required: [
    { key: 'target', table: PREDICT_TABLE, name: 'The number column', header: 'next_month_spend', example: '84.50',
      means: 'What you want Hermod to estimate. It must hold numbers.' },
    ...predictFeatures('the number'),
  ],
  optional: PREDICT_OPTIONAL,
  minimums: [
    { id: 'rows', what: 'Rows with a number', required: atLeast(LIMITS.minRows), recommended: orMore(LIMITS.fewRows) },
    { id: 'values', what: 'Different values', recommended: '2 or more; with one value there is nothing to learn' },
    { id: 'columns', what: 'Columns to learn from', required: 'at least 1' },
  ],
  example: {
    table: PREDICT_TABLE,
    header: ['tenure_months', 'plan', 'orders_last_90d', 'support_calls', 'next_month_spend'],
    rows: [['14', 'basic', '4', '3', '84.50'], ['48', 'pro', '11', '0', '310.00'], ['6', 'basic', '1', '5', '22.90'], ['30', 'pro', '7', '1', '198.40']],
  },
  mistakes: [
    'The number is stored as text (such as "1.234,50" or "$84"): use a plain number column.',
    'Whole numbers with only a few values (such as 1 to 5) are read as categories; pick "Predict a number" under Advanced if you mean a number.',
    ...PREDICT_MISTAKES,
  ],
  query: 'SELECT tenure_months, plan, orders_last_90d, support_calls, next_month_spend FROM customers',
}

// -- recommending from one table ----------------------------------------------

const REC_TABLE = 'Who had what'

const RECOMMEND: KindGuide = {
  kind: 'recommend',
  title: 'Recommend items to people',
  question: 'Which items is each person likely to pick next, and which items go together?',
  rows: 'One row each time someone had something: an order line, a view, a play.',
  required: [
    { key: 'user', table: REC_TABLE, name: 'Person', header: 'customer_id', example: 'C-1001', means: 'Who: a customer, account or user id.' },
    { key: 'item', table: REC_TABLE, name: 'Item', header: 'product_id', example: 'P-200', means: 'What: a product, article or song id.' },
  ],
  optional: [
    { key: 'strength', table: REC_TABLE, name: 'How much', header: 'quantity', example: '12', means: 'A rating, quantity or count.',
      why: 'Without it, every row counts as one like. Rows of 0 or less are left out.' },
    { key: 'time', table: REC_TABLE, name: 'When', header: 'order_date', example: '2026-09-01', means: 'When it happened.',
      why: 'Hermod then tests on each person\'s latest picks, which is how recommendations are judged in real life.' },
  ],
  minimums: [
    { id: 'rows', what: 'Usable rows', required: atLeast(LIMITS.minInteractions) },
    { id: 'people', what: 'Different people', required: atLeast(2) },
    { id: 'items', what: 'Different items', required: atLeast(2) },
  ],
  example: {
    table: REC_TABLE,
    header: ['customer_id', 'product_id', 'quantity', 'order_date'],
    rows: [['C-1001', 'P-200', '12', '2026-09-01'], ['C-1001', 'P-315', '3', '2026-09-01'], ['C-1002', 'P-200', '24', '2026-09-03'], ['C-1003', 'P-315', '1', '2026-09-04']],
  },
  mistakes: [
    'One row per order instead of one per product on it: Hermod cannot see what was bought.',
    'Different spellings of the same id ("C-1001" and "c-1001 "): they count as two people.',
    'Returns and cancelled orders left in, as rows with a negative quantity.',
  ],
  query: 'SELECT customer_id, product_id, quantity, order_date FROM order_lines',
}

// -- recommendation scenarios -------------------------------------------------

const ORDERS = ROLES.transactions.label

/** The orders example the scenarios share: C-1001 buys P-200 about every 30 days. */
const ORDERS_EXAMPLE = {
  table: ORDERS,
  header: ROLES.transactions.example.header,
  rows: [
    ['C-1001', 'P-200', '2026-07-01', '12', '144.00'],
    ['C-1001', 'P-200', '2026-07-31', '10', '120.00'],
    ['C-1001', 'P-315', '2026-07-31', '3', '57.30'],
    ['C-1002', 'P-200', '2026-08-03', '24', '288.00'],
    ['C-1001', 'P-200', '2026-08-30', '12', '144.00'],
  ],
}

/** Why a scenario wants an optional column of the orders table. */
const ORDER_COLUMN_WHY: Record<Scenario, Record<string, string>> = {
  visits: {
    product: 'Spots products similar customers buy and this one does not.',
    quantity: 'Lets Hermod leave out returns (negative quantities).',
    amount: 'Big customers are judged by money, not only by how often they order.',
  },
  products: {
    date: 'Hermod tests on each customer\'s latest orders and can measure the history.',
    quantity: 'Products bought in larger amounts count as stronger likes.',
    amount: 'Lets Hermod leave out credit notes (negative amounts).',
  },
  reorder: {
    product: 'Names the products each customer is due to buy in the next N days. Without it the list has customers only.',
    quantity: 'Lets Hermod leave out returns (negative quantities).',
    amount: 'Lets Hermod leave out credit notes (negative amounts).',
  },
  audience: {
    date: 'Hermod tests on each customer\'s latest orders and can measure the history.',
    quantity: 'Products bought in larger amounts count as stronger likes.',
    amount: 'Lets Hermod leave out credit notes (negative amounts).',
  },
  similar: {
    date: 'Hermod tests on each customer\'s latest orders and can measure the history.',
    quantity: 'Products bought in larger amounts count as stronger likes.',
    amount: 'Lets Hermod leave out credit notes (negative amounts).',
  },
}

const SCENARIO_QUESTION: Record<Scenario, string> = {
  visits: 'Which customers should each salesman visit or call next, most urgent first?',
  products: 'Which products should we offer this customer?',
  reorder: 'When will each customer order again, and who will buy in the next 2 weeks or month?',
  audience: 'Which customers are most likely to want this product?',
  similar: 'Which products are most like this one, for a substitute or an upsell?',
}

const SCENARIO_MISTAKES: Record<Scenario, string[]> = {
  visits: [
    'No salesman column on the customers table: a visit plan is made per salesman.',
    'Salesman ids on the customers that differ from those in the salesmen table (such as "rep7" and "REP-7").',
  ],
  products: ['A product id that changes between tables, or leading zeros lost in a spreadsheet ("00881" becomes "881").'],
  reorder: [
    'Only a few weeks of orders: a customer who orders monthly needs several months to show a rhythm.',
    'Leaving out the product column: the list then names the customers but not what they are due to buy.',
  ],
  audience: ['A brand-new product with no sales yet: Hermod finds its buyers from who bought similar products, so give it a few sales first.'],
  similar: ['Too many near-identical products (sizes, colours): group them into one product family first.'],
}

const COMMON_ORDER_MISTAKES = [
  'One row per invoice total instead of one row per product on the invoice.',
  'Returns, cancelled and test orders left in: leave them out in the query.',
  'Dates as text Hermod cannot read: write them as 2026-09-01 (year, month, day).',
  'The same id spelled differently in two tables (spaces, upper and lower case, leading zeros).',
]

const REORDER_TERMS = [
  { term: 'Usual gap',
    meaning: `The middle number of days between a customer's order days. A customer with only one order day has no gap of their own yet, so Hermod uses the usual gap of all customers (${LIMITS.fallbackGapDays} days when no customer has two).` },
  { term: 'Due date', meaning: 'The day of the customer\'s last order plus their usual gap: when they are expected to order again.' },
  { term: 'Overdue', meaning: 'The due date has passed without a new order. Overdue customers are kept on the list and marked, not dropped.' },
  { term: 'Next N days',
    meaning: `The customers whose due date falls before today plus N days (N is 1 to ${LIMITS.maxWithinDays}), overdue ones included. They are ranked by how likely they are to order: fully likely up to one and a half usual gaps since their last order, falling to not at all at three, when the order is probably lost rather than late.` },
  { term: 'Products due',
    meaning: 'Each product\'s date comes from the customer\'s own gap between buying it, so only products they bought on at least two different days get one. A product not bought for three of those gaps counts as one they stopped buying.' },
  { term: 'Enough history',
    meaning: `A customer needs at least 2 order days for a rhythm of their own; 3 or more make it steadier. Give at least ${about(LIMITS.wantedHistoryDays.reorder)} of orders, 12 months or more for customers who order rarely.` },
]

function roleColumn(role: RoleKey, key: string, example?: { header: string[]; rows: string[][] }): GuideColumn {
  const spec = ROLES[role]
  const i = spec.columns.findIndex((c) => c.key === key)
  const c = spec.columns[i]
  const header = spec.example.header[i]
  const rows = example?.header.includes(header) ? example : spec.example
  return {
    key: `${role}.${key}`, table: spec.label, name: c.label, header, means: c.hint,
    example: rows.rows[0][rows.header.indexOf(header)],
  }
}

function scenarioGuide(s: Scenario): KindGuide {
  const roles = scenarioRoles(s)
  const required: GuideColumn[] = []
  const optional: GuideColumn[] = []
  for (const r of roles) {
    const ex = r.role === 'transactions' ? ORDERS_EXAMPLE : undefined
    if (!r.required) {
      const cols = ROLES[r.role].columns.map((c) => c.label.toLowerCase()).join(', ')
      optional.push({ key: r.role, table: ROLES[r.role].label, name: `${ROLES[r.role].label} table`, header: '',
        means: ROLES[r.role].what, example: cols, why: r.why })
      continue
    }
    for (const c of r.columns) {
      const col = roleColumn(r.role, c.key, ex)
      if (c.required) required.push(col)
      else if (r.role === 'transactions' && ORDER_COLUMN_WHY[s][c.key]) optional.push({ ...col, why: ORDER_COLUMN_WHY[s][c.key] })
      else if (r.role !== 'transactions') optional.push({ ...col, why: c.hint })
    }
  }
  const productLike = s === 'products' || s === 'audience' || s === 'similar'
  const minimums: GuideMinimum[] = [
    { id: 'transactions', what: 'Usable order lines', required: atLeast(LIMITS.minInteractions) },
    { id: 'customers', what: 'Customers', required: atLeast(2),
      recommended: orMore(productLike ? LIMITS.minCustomersProducts : LIMITS.minCustomersTiming) },
    { id: 'orders_per_customer', what: 'Orders per customer, on average (days with an order)',
      recommended: orMore(productLike ? LIMITS.minOrdersPerCustomerProducts : LIMITS.minOrdersPerCustomer) },
    { id: 'history', what: 'History, from the first order to the last',
      required: s === 'visits' || s === 'reorder' ? 'dates on at least 2 different days' : undefined,
      recommended: historyText(LIMITS.wantedHistoryDays[s]) },
  ]
  if (s === 'reorder') {
    minimums.push({ id: 'product_days', what: 'For a product\'s due date', recommended: '2 or more different days a customer bought that product' })
  }
  if (productLike) minimums.push({ id: 'products', what: 'Different products', recommended: `fewer than ${fmt(LIMITS.maxProducts)}` })
  const header = ORDERS_EXAMPLE.header
  const cols = header.join(', ')
  const [customer, product, date, quantity, amount] = header
  return {
    kind: s,
    title: SCENARIOS.find((x) => x.value === s)?.label ?? s,
    question: SCENARIO_QUESTION[s],
    rows: ROLES.transactions.what,
    required, optional, minimums,
    example: ORDERS_EXAMPLE,
    mistakes: [...SCENARIO_MISTAKES[s], ...COMMON_ORDER_MISTAKES],
    terms: s === 'reorder' ? REORDER_TERMS : undefined,
    query: `SELECT ${cols} FROM order_lines`,
    bigQuery: `SELECT ${customer}, ${product}, ${date}, SUM(${quantity}) AS ${quantity}, SUM(${amount}) AS ${amount} ` +
      `FROM order_lines GROUP BY ${customer}, ${product}, ${date}`,
  }
}

const GUIDES: Record<GuideKind, KindGuide> = {
  classification: CLASSIFICATION,
  regression: REGRESSION,
  recommend: RECOMMEND,
  ...Object.fromEntries(SCENARIO_KINDS.map((s) => [s, scenarioGuide(s)])) as Record<Scenario, KindGuide>,
}

export const dataGuide = (kind: GuideKind): KindGuide => GUIDES[kind]

export const guideTitle = (kind: GuideKind) => GUIDES[kind].title

/** The CSV templates of a kind: its main table, and for a visit plan the customers table too. */
export function guideTemplates(kind: GuideKind): Array<{ file: string; csv: string }> {
  const { header, rows } = GUIDES[kind].example
  const main = { file: isScenarioKind(kind) ? `${kind}-orders-template.csv` : `${kind}-template.csv`, csv: [header, ...rows].map((r) => r.join(',')).join('\n') }
  if (!isScenarioKind(kind)) return [main]
  const others = scenarioRoles(kind).filter((r) => r.required && r.role !== 'transactions')
    .map((r) => ({ file: `${kind}-${r.role}-template.csv`, csv: csvTemplate(r.role) }))
  return [main, ...others]
}

// -- databases ----------------------------------------------------------------

/** What to know when reading a dataset from a database, in words. */
export const DATABASE_NOTES: string[] = [
  'Hermod runs only a SELECT (or WITH … SELECT), read-only, with the database source\'s own credentials. Nothing in your database is changed.',
  `It reads at most ${fmt(LIMITS.defaultMaxQueryRows)} rows unless you set "At most this many rows" higher. A query that returns more is stopped with an error, and the dataset keeps only the rows read so far: narrow the query or raise the cap, then read it again.`,
  'An administrator can set lower limits for this vhost under ML quotas (Rows per dataset, Bytes per dataset). The lower of your cap and the quota applies.',
  `Rows are sent ${fmt(LIMITS.queryChunkRows)} at a time, so millions of rows can be read; how many a model can learn from depends on the memory of the machine that trains it.`,
  `On Hermod's built-in engine, a prediction model learns from at most ${fmt(LIMITS.builtinMaxTrainRows)} rows (the setting HERMOD_ML_BUILTIN_MAX_ROWS): from a bigger dataset it takes a random sample of that many, and the trained version says so. The training worker learns from every row.`,
  `The built-in engine stores a dataset in pieces of at most ${LIMITS.blobMaxBytes / 2 ** 20} MB. A database read is stored ${fmt(LIMITS.queryChunkRows)} rows per piece, so that limit does not apply to it. An uploaded file is at most ${LIMITS.uploadMaxBytes / 2 ** 20} MB; a piece too large to keep is refused ("too large to keep") and the dataset stays as it was.`,
]

/** Tips for a big table, in words. */
export const BIG_TABLE_TIPS: string[] = [
  'Select only the columns the model needs, not SELECT *.',
  'For recommendations, one row per customer, product and day is enough: GROUP BY them and SUM the quantity and amount. Nothing the models use is lost, and the table often gets ten times smaller.',
  'Keep the history the question needs, such as the last 24 months, with a date filter in the WHERE.',
  'Leave out returns, cancelled and test orders in the WHERE.',
  'To know how many rows a query returns, run it with SELECT COUNT(*) in your own database tool first: Hermod does not count before reading.',
]

// -- the checklist ------------------------------------------------------------

/** One line of the checklist against a chosen dataset: what was checked, what was found and the fix. */
export interface ChecklistItem {
  id: string
  label: string
  status: CheckStatus
  detail: string
  fix?: string
}

const RANK: Record<CheckStatus, number> = { green: 0, amber: 1, red: 2 }
const worse = (a: CheckStatus, b: CheckStatus | undefined): CheckStatus => (b && RANK[b] > RANK[a] ? b : a)

/** Why training cannot start: the first red item's fix; null when nothing is red. */
export function checklistBlocker(items: ChecklistItem[]): string | null {
  const red = items.find((i) => i.status === 'red')
  return red ? red.fix ?? red.detail : null
}

const minimumDetail = (have: string, m: GuideMinimum | undefined) =>
  [`You have ${have}.`, m?.required && `Required: ${m.required}.`, m?.recommended && `Recommended: ${m.recommended}.`].filter(Boolean).join(' ')

/** The checklist of a model that predicts a column, from the pre-flight check. */
export function predictChecklist({ target, learnFrom, check }: {
  target: string | null; learnFrom: number; check: TrainCheck | null | undefined
}): ChecklistItem[] {
  const columns: ChecklistItem = !target
    ? { id: 'columns', label: 'Columns', status: 'red', detail: 'No column to predict is chosen.', fix: 'Pick the column to predict.' }
    : learnFrom === 0
      ? { id: 'columns', label: 'Columns', status: 'red', detail: 'No column to learn from is ticked.', fix: 'Tick at least one column to learn from.' }
      : { id: 'columns', label: 'Columns', status: 'green', detail: `Predicts ${target} from ${learnFrom} column${learnFrom === 1 ? '' : 's'}.` }
  if (columns.status === 'red' || !check) return [columns]
  const guide = GUIDES[check.task]
  const rowsMin = guide.minimums.find((m) => m.id === 'rows')
  const n = check.usable_rows
  const rows: ChecklistItem = {
    id: 'rows', label: rowsMin?.what ?? 'Rows', detail: minimumDetail(fmt(n), rowsMin),
    ...(n < LIMITS.minRows
      ? { status: 'red' as const, fix: `Training needs at least ${LIMITS.minRows} rows with a value in ${target}: use a dataset with more rows.` }
      : n < LIMITS.fewRows
        ? { status: 'amber' as const, fix: 'It trains, but a model learned from so few rows is rough: add rows if you have them.' }
        : { status: 'green' as const }),
  }
  const has = (code: string) => check.warnings.some((w) => w.code === code && (!w.column || w.column === target))
  let answers: ChecklistItem
  if (check.task === 'classification') {
    const label = 'Different answers'
    const detail = check.classes ? `${check.classes} different answers.` : ''
    answers = has('constant')
      ? { id: 'answers', label, status: 'red', detail: 'Every row has the same answer.',
          fix: 'A yes/no or category needs at least two different answers: pick another column, or add rows with the other answers.' }
      : has('many_classes')
        ? { id: 'answers', label, status: 'amber', detail, fix: `More than ${LIMITS.maxAutoClasses} answers: group rare ones together.` }
        : has('imbalanced')
          ? { id: 'answers', label, status: 'amber', detail: `${detail} One answer is rare.`.trim(),
              fix: 'Hermod weighs the rare answer so it counts; more rows with it help.' }
          : { id: 'answers', label, status: 'green', detail }
  } else {
    answers = has('constant')
      ? { id: 'answers', label: 'Different values', status: 'amber', detail: 'Every row has the same number.', fix: 'Pick a column whose number changes from row to row.' }
      : { id: 'answers', label: 'Different values', status: 'green', detail: 'The number changes from row to row.' }
  }
  return [columns, rows, answers]
}

/** The checklist of a one-table recommender, from the dataset's row count and the columns picked. */
export function recommendChecklist({ rows, user, item, time }: {
  rows: number | undefined; user: string | null; item: string | null; time: string | null
}): ChecklistItem[] {
  const out: ChecklistItem[] = [
    user && item
      ? { id: 'columns', label: 'Columns', status: 'green', detail: `People in ${user}, items in ${item}.` }
      : { id: 'columns', label: 'Columns', status: 'red', detail: 'The person or the item column is not chosen.',
          fix: 'Pick the column that says who (the person) and the one that says what (the item).' },
  ]
  if (rows !== undefined) {
    const m = RECOMMEND.minimums.find((x) => x.id === 'rows')
    out.push(rows < LIMITS.minInteractions
      ? { id: 'rows', label: 'Rows', status: 'red', detail: minimumDetail(fmt(rows), m),
          fix: `Training needs at least ${LIMITS.minInteractions} rows; this dataset has ${fmt(rows)}.` }
      : { id: 'rows', label: 'Rows', status: 'green', detail: minimumDetail(fmt(rows), m) })
  }
  out.push(time
    ? { id: 'time', label: 'When', status: 'green', detail: `Tested on each person's latest picks (${time}).` }
    : { id: 'time', label: 'When', status: 'amber', detail: 'No "when" column: a random share of picks is held back to test on.',
        fix: 'Add a "when" column if you have one: Hermod then tests on each person\'s latest picks.' })
  return out
}

/** What a scenario still needs mapped, in words: "Customers: a dataset", "Orders or invoice lines: Date". */
export function missingMappings(scenario: Scenario, data: RecommendData): string[] {
  const out: string[] = []
  for (const r of scenarioRoles(scenario)) {
    const m = data[r.role]
    const label = ROLES[r.role].label
    if (!m?.dataset) {
      if (r.required) out.push(`${label}: a dataset`)
      continue
    }
    for (const c of r.columns) if (c.required && !m[c.key]) out.push(`${label}: ${c.label}`)
  }
  return out
}

function mappingFix(scenario: Scenario, data: RecommendData): string | undefined {
  for (const r of scenarioRoles(scenario)) {
    const m = data[r.role]
    const label = ROLES[r.role].label
    const need = r.columns.filter((c) => c.required).map((c) => c.label)
    if (!m?.dataset) {
      if (r.required) return `Choose a dataset for ${label}, and its ${need.join(' and ')} column${need.length === 1 ? '' : 's'}.`
      continue
    }
    const col = r.columns.find((c) => c.required && !m[c.key])
    if (col) return `Choose the ${col.label} column of ${label}.`
  }
  return undefined
}

/**
 * The checklist of a scenario: the columns mapped, then (once the data health
 * check has run) the column types and the amounts against the guide's
 * minimums. A status is the worse of what the guide's thresholds say and
 * what the check said for the same thing. The words are the guide's: the
 * check's own messages are listed beside it, so they are not repeated here.
 */
export function scenarioChecklist(scenario: Scenario, data: RecommendData, check: RecommendCheck | null | undefined): ChecklistItem[] {
  const server = (code: string) => check?.checks.find((c) => c.code === code)
  const missing = missingMappings(scenario, data)
  const role = server('missing_role')
  const columns: ChecklistItem = missing.length
    ? { id: 'columns', label: 'Columns mapped', status: 'red', detail: `Still needed: ${missing.join('; ')}.`, fix: mappingFix(scenario, data) }
    : role && role.status !== 'green'
      ? { id: 'columns', label: 'Columns mapped', status: role.status, detail: 'A table or column you mapped is not in its dataset; the check below says which.',
          fix: 'Pick a dataset and columns that exist.' }
      : { id: 'columns', label: 'Columns mapped', status: 'green', detail: 'Every column this question needs is mapped.' }
  if (!check) return [columns]

  const guide = GUIDES[scenario]
  const min = (id: string) => guide.minimums.find((m) => m.id === id)
  const s = check.stats
  const out: ChecklistItem[] = [columns]
  const types = server('bad_column_type')
  out.push(types && types.status !== 'green'
    ? { id: 'types', label: 'Column types', status: types.status, detail: 'A column holds the wrong kind of values; the check below says which.',
        fix: 'Pick a column of the right kind (dates such as 2026-09-01, plain numbers), or clean its values.' }
    : { id: 'types', label: 'Column types', status: 'green', detail: 'Dates read as dates and numbers as numbers.' })

  const tx = server('few_transactions_per_customer')
  const txStatus = worse(s.transactions < LIMITS.minInteractions ? 'red' : 'green', tx?.status === 'red' ? 'red' : undefined)
  out.push({ id: 'transactions', label: min('transactions')?.what ?? '', status: txStatus, detail: minimumDetail(fmt(s.transactions), min('transactions')),
    fix: txStatus === 'red' ? 'Fill the orders dataset with more history.' : undefined })

  const productLike = scenario === 'products' || scenario === 'audience' || scenario === 'similar'
  const wantCustomers = productLike ? LIMITS.minCustomersProducts : LIMITS.minCustomersTiming
  const cust = server('few_customers')
  const custStatus = worse(s.customers < 2 ? 'red' : s.customers < wantCustomers ? 'amber' : 'green', cust?.status)
  out.push({ id: 'customers', label: 'Customers', status: custStatus, detail: minimumDetail(fmt(s.customers), min('customers')),
    fix: custStatus === 'red' ? 'Check the customer column and that the dataset is filled.'
      : custStatus === 'amber' ? 'It trains, but answers will be rougher: add more customers\' history if you have it.' : undefined })

  const wantOrders = productLike ? LIMITS.minOrdersPerCustomerProducts : LIMITS.minOrdersPerCustomer
  const ordStatus = worse(s.tx_per_customer < wantOrders ? 'amber' : 'green', tx?.status === 'amber' ? 'amber' : undefined)
  out.push({ id: 'orders_per_customer', label: 'Orders per customer', status: ordStatus,
    detail: minimumDetail(Number(s.tx_per_customer.toFixed(1)).toLocaleString('en-US'), min('orders_per_customer')),
    fix: ordStatus === 'amber' ? 'Use a longer history, or order lines rather than invoice totals.' : undefined })

  const hist = server('short_history')
  const hasDate = !!data.transactions?.date
  const days = s.months_of_history * 30.44
  const histComputed: CheckStatus = !hasDate ? (scenario === 'visits' || scenario === 'reorder' ? 'red' : 'amber')
    : days < LIMITS.wantedHistoryDays[scenario] ? 'amber' : 'green'
  const histStatus = worse(histComputed, hist?.status)
  out.push({ id: 'history', label: 'History', status: histStatus,
    detail: hasDate ? minimumDetail(`${Number(s.months_of_history.toFixed(1)).toLocaleString('en-US')} months`, min('history')) : 'No date column is mapped.',
    fix: histStatus === 'green' ? undefined : !hasDate ? 'Map the date column of the orders.'
      : histStatus === 'red' ? 'Load orders from more than one day; a longer history makes the answers better.'
        : 'Load a longer history if you have it.' })

  if (scenario === 'reorder') {
    out.push(data.transactions?.product
      ? { id: 'product', label: 'Products due', status: 'green', detail: 'Each customer\'s list will name the products they are due to buy.' }
      : { id: 'product', label: 'Products due', status: 'amber', detail: 'No product column: the list will name customers but not what they are due to buy.',
          fix: 'Map the product column to see which products each customer is due for.' })
  }
  if (productLike) {
    const many = server('too_many_products')
    const st = worse(s.products >= LIMITS.maxProducts ? 'amber' : 'green', many?.status)
    out.push({ id: 'products', label: 'Products', status: st, detail: minimumDetail(fmt(s.products), min('products')),
      fix: st === 'green' ? undefined : 'Recommend a product group instead, or leave out inactive products.' })
  }
  return out
}

// -- the docs -----------------------------------------------------------------

/** The docs that hold a generated guide, and the kinds each covers. */
export const DOC_BLOCKS: Record<string, GuideKind[]> = {
  'docs/ml.md': GUIDE_KINDS,
  'docs/ml-recommendations-data-guide.md': SCENARIO_KINDS,
}

const cell = (s: string) => s.replaceAll('|', '\\|')
const mdTable = (head: string[], rows: string[][]) =>
  [`| ${head.join(' | ')} |`, `|${head.map(() => '---').join('|')}|`, ...rows.map((r) => `| ${r.map(cell).join(' | ')} |`)].join('\n')

/** The guide of `kinds` as Markdown, with what to know when reading from a database. */
export function guideMarkdown(kinds: GuideKind[]): string {
  const parts: string[] = []
  for (const kind of kinds) {
    const g = GUIDES[kind]
    parts.push(`### ${g.title}`, '', `*${g.question}* ${g.rows}`, '')
    parts.push('**Required columns**', '', mdTable(['Table', 'Column', 'What it means', 'Example'],
      g.required.map((c) => [c.table, c.name, c.means, c.example ? `\`${c.example}\`` : ''])), '')
    if (g.optional.length) {
      parts.push('**Optional, and what they add**', '', mdTable(['Table', 'Column', 'Why'],
        g.optional.map((c) => [c.table, c.name, c.why ?? ''])), '')
    }
    parts.push('**How much data**', '', mdTable(['', 'Required', 'Recommended'],
      g.minimums.map((m) => [m.what, m.required ?? '—', m.recommended ?? '—'])), '')
    if (g.terms) {
      parts.push('**What the answers mean**', '', ...g.terms.map((t) => `- **${t.term}.** ${t.meaning}`), '')
    }
    parts.push('**Example**', '', mdTable(g.example.header, g.example.rows), '')
    parts.push('**From a database**', '', '```sql', g.query, ...(g.bigQuery ? ['-- a big table, one row per customer, product and day:', g.bigQuery] : []), '```', '')
    parts.push('**Common mistakes**', '', ...g.mistakes.map((m) => `- ${m}`), '')
  }
  parts.push('### Reading a dataset from a database', '', ...DATABASE_NOTES.map((n) => `- ${n}`), '',
    '**Big tables**', '', ...BIG_TABLE_TIPS.map((n) => `- ${n}`))
  return parts.join('\n')
}
