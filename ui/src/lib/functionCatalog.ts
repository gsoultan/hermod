import catalog from './expressionFunctions.json'

/**
 * One function an expression can call. The list is expressionFunctions.json,
 * which the engine's own test holds to what the engine runs
 * (pkg/infra/evaluator/function_catalog_test.go).
 */
export interface ExpressionFunction {
  name: string
  category: string
  signature: string
  summary: string
  /** Other words to find it by. */
  keywords?: string[]
  /** The placeholders inserted between the brackets. */
  args: string[]
  example: string
  /** What the engine answers for `example`; absent when `volatile`. */
  result?: unknown
  volatile?: boolean
}

export const FUNCTION_CATEGORIES: readonly string[] = catalog.categories
export const EXPRESSION_FUNCTIONS: readonly ExpressionFunction[] = catalog.functions

/** The functions matching `query`, by name, by what they do, or by a keyword. */
export function searchFunctions(query: string): ExpressionFunction[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...EXPRESSION_FUNCTIONS]
  return EXPRESSION_FUNCTIONS.filter((f) =>
    [f.signature, f.category, f.summary, ...(f.keywords ?? [])].some((text) => text.toLowerCase().includes(q))
  )
}

/** `functions` under their categories, in the catalog's order, empty ones left out. */
export function groupByCategory(functions: readonly ExpressionFunction[]): Array<[string, ExpressionFunction[]]> {
  return FUNCTION_CATEGORIES.map((category): [string, ExpressionFunction[]] => [
    category,
    functions.filter((f) => f.category === category),
  ]).filter(([, members]) => members.length > 0)
}

/** An example's result as JSON, so text keeps its quotes and reads as text. */
export function exampleResult(fn: ExpressionFunction): string | null {
  return fn.volatile || fn.result === undefined ? null : JSON.stringify(fn.result)
}

/** Text written like a call that the engine will not run as one. */
export interface NotAFunction {
  name: string
  /**
   * What the engine makes of it: `nothing` for a name it does not know, which
   * evaluates to null, and `text` for a dotted name such as time.now, which is
   * not read as a call at all and is written out as typed.
   */
  becomes: 'nothing' | 'text'
  /** The function probably meant, when there is an obvious one. */
  suggestion?: string
}

// env() is not in the catalog -- it is an older spelling of secret() -- but
// the engine runs it.
const KNOWN_NAMES = new Set([...EXPRESSION_FUNCTIONS.map((f) => f.name.toLowerCase()), 'env'])

function suggestionFor(name: string): string | undefined {
  const last = name.split('.').pop()!.toLowerCase()
  const match =
    EXPRESSION_FUNCTIONS.find((f) => f.name.toLowerCase() === last) ??
    EXPRESSION_FUNCTIONS.find((f) => f.keywords?.includes(last))
  return match?.name
}

/** evaluator.parseArgs: commas outside quotes and brackets. */
function splitArguments(text: string): string[] {
  const args: string[] = []
  let current = ''
  let depth = 0
  let quote = ''
  for (const c of text) {
    if (c === "'" || c === '"') {
      if (!quote) quote = c
      else if (c === quote) quote = ''
    } else if (!quote && c === '(') {
      depth++
    } else if (!quote && c === ')') {
      depth--
    } else if (!quote && c === ',' && depth === 0) {
      args.push(current)
      current = ''
      continue
    }
    current += c
  }
  args.push(current)
  return args
}

/** One expression, read the way evaluator.ParseAndEvaluate decides what it is. */
function inspect(expression: string, found: Map<string, NotAFunction>) {
  const text = expression.trim()
  if (!text.endsWith(')') || text.startsWith('source.')) return
  const quoted = (text.startsWith("'") && text.endsWith("'")) || (text.startsWith('"') && text.endsWith('"'))
  if (quoted) return

  // The bracket that opens the one the text ends with.
  let depth = 0
  let open = -1
  for (let i = text.length - 1; i >= 0; i--) {
    if (text[i] === ')') depth++
    else if (text[i] === '(' && --depth === 0) {
      open = i
      break
    }
  }
  if (open <= 0) return

  const name = text.slice(0, open).trim()
  if (!/^[A-Za-z0-9_]+$/.test(name)) {
    // Not a call to the engine, so the whole value is text. Only worth saying
    // when it was plainly meant as one: time.now(), strings.ToUpper(x).
    if (/^[A-Za-z_][\w.]*$/.test(name)) {
      found.set(name, { name, becomes: 'text', suggestion: suggestionFor(name) })
    }
    return
  }
  if (!KNOWN_NAMES.has(name.toLowerCase())) {
    found.set(name, { name, becomes: 'nothing', suggestion: suggestionFor(name) })
  }
  for (const argument of splitArguments(text.slice(open + 1, -1))) inspect(argument, found)
}

/**
 * What in a `set` / `advanced` value is written like a call but is not one the
 * engine runs.
 *
 * Neither case is an error anywhere: an unknown name evaluates to null and a
 * dotted one is written out as text, so a typo ships as a column of nulls. The
 * engine's side of both is pinned by TestAValueThatCallsNoFunction, which runs
 * the cases this function's own test does.
 */
export function notFunctions(value: string): NotAFunction[] {
  const found = new Map<string, NotAFunction>()
  if (!value.includes('{{')) {
    inspect(value, found)
    return [...found.values()]
  }
  // A template: only its tokens are read, and only one that looks like a call
  // is read as an expression (evaluator.EvaluateField).
  for (const [, inner] of value.matchAll(/\{\{((?:(?!\}\}).)*)\}\}/gs)) {
    const token = inner.trim().replace(/^\./, '')
    if (token.includes('(') && token.endsWith(')')) inspect(token, found)
  }
  return [...found.values()]
}
