/**
 * A form value read as a tree the editor can build: text, a field, a secret,
 * or a function whose arguments are any of the four -- so calls nest.
 *
 * The saved string stays the only thing the engine sees. This reads it in the
 * spelling evaluator.ParseAndEvaluate runs, and writes it back the same way:
 *
 *   field     source.customer.email
 *   secret    secret("API_KEY")             (env("API_KEY") reads the same)
 *   text      "eu-west"  'say "hi"'  -5  true
 *   call      upper(trim(source.name))
 *
 * A value as a whole is written for what resolves it (see ValueMode). Anything
 * the tree cannot show -- text around a token, an operator, a dotted name that
 * is not a call -- parses to null and is left to be edited as text.
 */

export type ValueNode =
  | { kind: 'text'; value: string; /** Written without quotes: a number or a boolean. */ bare?: boolean }
  | { kind: 'field'; path: string }
  | { kind: 'secret'; name: string }
  | { kind: 'call'; fn: string; args: ValueNode[] }

/**
 * How a value as a whole is read by the engine:
 *
 * - `token`: a template. Text is literal, a field or a call is a `{{ }}` token.
 * - `expression`: a Set Fields value. A call is written bare; a field is a token.
 * - `field`: a "field or expression" input. A bare path is a field; text is quoted.
 */
export type ValueMode = 'token' | 'expression' | 'field'

/** How a field and a secret are written at the top of a value, per input. */
export interface RootTokens {
  field?: (path: string) => string
  secret?: (name: string) => string
}

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/
const PATH = /^[^\s{}()"',+*/<>=!&|]+$/
const NUMBER = /^-?\d+(\.\d+)?$/

/** evaluator.parseArgs: commas outside quotes and brackets. */
function splitArguments(text: string): string[] | null {
  const args: string[] = []
  let current = ''
  let depth = 0
  let quote = ''
  for (const c of text) {
    if (quote) {
      if (c === quote) quote = ''
      current += c
      continue
    }
    if (c === "'" || c === '"') quote = c
    else if (c === '(') depth++
    else if (c === ')') depth--
    if (depth < 0) return null
    if (c === ',' && depth === 0) {
      args.push(current.trim())
      current = ''
      continue
    }
    current += c
  }
  if (quote || depth !== 0) return null
  if (current.trim() !== '' || args.length > 0) args.push(current.trim())
  return args
}

/** The index of the `(` that opens the call ending at the last character, or -1. */
function openingParen(expr: string): number {
  let depth = 0
  let quote = ''
  let open = -1
  for (let i = 0; i < expr.length; i++) {
    const c = expr[i]
    if (quote) {
      if (c === quote) quote = ''
      continue
    }
    if (c === "'" || c === '"') quote = c
    else if (c === '(') {
      if (depth === 0) {
        if (open !== -1) return -1
        open = i
      }
      depth++
    } else if (c === ')') {
      depth--
      if (depth < 0) return -1
      if (depth === 0 && i !== expr.length - 1) return -1
    }
  }
  return depth === 0 ? open : -1
}

const unquote = (text: string): string | null => {
  const m = /^"([^"]*)"$|^'([^']*)'$/s.exec(text)
  return m ? (m[1] ?? m[2]) : null
}

/** One expression read into a node, or null when it is not something the tree can show. */
export function parseExpression(raw: string): ValueNode | null {
  const expr = raw.trim()
  if (expr === '') return null

  const literal = unquote(expr)
  if (literal !== null) return { kind: 'text', value: literal }
  if (NUMBER.test(expr) || expr === 'true' || expr === 'false') return { kind: 'text', value: expr, bare: true }

  if (expr.startsWith('source.')) {
    const path = expr.slice(7)
    return PATH.test(path) ? { kind: 'field', path } : null
  }

  if (!expr.endsWith(')')) return null
  const open = openingParen(expr)
  if (open <= 0) return null
  const fn = expr.slice(0, open).trim()
  if (!NAME.test(fn)) return null
  const parts = splitArguments(expr.slice(open + 1, -1))
  if (parts === null) return null

  const lower = fn.toLowerCase()
  if ((lower === 'secret' || lower === 'env') && parts.length === 1) {
    const name = unquote(parts[0])
    if (name !== null) return { kind: 'secret', name }
  }

  const args: ValueNode[] = []
  for (const part of parts) {
    const arg = parseExpression(part)
    if (!arg) return null
    args.push(arg)
  }
  return { kind: 'call', fn, args }
}

const quoted = (text: string) => (text.includes('"') ? `'${text}'` : `"${text}"`)

/** A node written as an expression -- the spelling inside a call. */
export function formatExpression(node: ValueNode): string {
  switch (node.kind) {
    case 'text':
      return node.bare ? node.value : quoted(node.value)
    case 'field':
      return `source.${node.path}`
    case 'secret':
      return `secret(${quoted(node.name)})`
    case 'call':
      // A function not picked yet is what it wraps.
      if (!node.fn) return node.args[0] ? formatExpression(node.args[0]) : ''
      return `${node.fn}(${node.args.map(formatExpression).join(', ')})`
  }
}

const TOKEN = /^\{\{\s*((?:(?!\}\}).)*?)\s*\}\}$/s
const WHOLE_SECRET = /^secret:([^\s{}]+)$/

/** A token's inner text: a bare path is a field there; anything else is an expression. */
function parseTokenInner(inner: string): ValueNode | null {
  const secret = WHOLE_SECRET.exec(inner)
  if (secret) return { kind: 'secret', name: secret[1] }
  if (!inner.includes('(') && !inner.startsWith('source.') && PATH.test(inner) && !NUMBER.test(inner)) {
    return { kind: 'field', path: inner.replace(/^\./, '') }
  }
  const node = parseExpression(inner)
  // A quoted literal in a token is not something any form writes.
  return node && node.kind !== 'text' ? node : null
}

/**
 * A saved value read into a node for the given mode; null when it has to be
 * edited as text (an empty value reads as empty text).
 */
export function parseValue(value: unknown, mode: ValueMode): ValueNode | null {
  const raw = value === undefined || value === null ? '' : String(value)
  const trimmed = raw.trim()

  const secret = WHOLE_SECRET.exec(trimmed)
  if (secret) return { kind: 'secret', name: secret[1] }

  if (trimmed.includes('{{')) {
    const token = TOKEN.exec(trimmed)
    if (!token || token[1].includes('{{')) return null
    return parseTokenInner(token[1])
  }

  if (mode === 'token') return { kind: 'text', value: raw }

  if (mode === 'field') {
    if (trimmed === '') return { kind: 'text', value: '' }
    if (!trimmed.includes('(') && !trimmed.startsWith('source.') && PATH.test(trimmed) && !NUMBER.test(trimmed)) {
      return { kind: 'field', path: trimmed }
    }
    return parseExpression(trimmed)
  }

  // expression: a call or a source path is an expression; anything else is
  // literal text, which ParseAndEvaluate hands back as typed.
  if (trimmed.endsWith(')') || trimmed.startsWith('source.')) {
    const node = parseExpression(trimmed)
    if (node) return node
  }
  return { kind: 'text', value: raw }
}

const defaultFieldToken = (path: string) => `{{.${path.replace(/^\./, '')}}}`
const defaultSecretToken = (name: string) => `{{secret(${quoted(name)})}}`

/** A node written back as a saved value for the given mode. */
export function formatValue(node: ValueNode, mode: ValueMode, tokens: RootTokens = {}): string {
  if (node.kind === 'call' && !node.fn) return node.args[0] ? formatValue(node.args[0], mode, tokens) : ''
  const fieldToken = tokens.field ?? defaultFieldToken
  const secretToken = tokens.secret ?? defaultSecretToken

  if (mode === 'field') {
    switch (node.kind) {
      case 'field':
        return node.path
      case 'text':
        return node.value === '' ? '' : formatExpression(node)
      case 'secret':
        return tokens.secret ? tokens.secret(node.name) : formatExpression(node)
      case 'call':
        return formatExpression(node)
    }
  }

  switch (node.kind) {
    case 'text':
      return node.value
    case 'field':
      return node.path ? fieldToken(node.path) : ''
    case 'secret':
      return node.name ? secretToken(node.name) : ''
    case 'call':
      return mode === 'token' ? `{{${formatExpression(node)}}}` : formatExpression(node)
  }
}
