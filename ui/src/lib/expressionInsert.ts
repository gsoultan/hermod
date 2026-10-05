/**
 * Where a picked function goes in the text being edited.
 *
 * Kept apart from the catalog (expressionFunctions.json) so an input can offer
 * the picker without loading the list until it is opened.
 */

/**
 * How the text is read by the engine, which decides how a call is written:
 *
 * - `expression`: a `set` / `advanced` value. A call is written bare, unless
 *   the value holds `{{` -- that makes it a template, where a call is a token.
 * - `token`: a template. A call is always a `{{ }}` token.
 * - `field`: a "field or expression" input, such as a condition's Field. A
 *   bare path there is a field, but inside a call it is literal text, so
 *   wrapping `status` has to write `source.status`.
 */
export type FunctionMode = 'expression' | 'token' | 'field'

export interface FunctionSnippet {
  name: string
  /** The placeholders inserted between the brackets. */
  args: string[]
}

export interface TextEdit {
  value: string
  selectionStart: number
  selectionEnd: number
}

/** True when `position` is after a `{{` that has not been closed. */
export function isInsideToken(value: string, position: number): boolean {
  const before = value.slice(0, position)
  return before.lastIndexOf('{{') > before.lastIndexOf('}}')
}

/** Every bracket and quote closed: something a call can be wrapped around. */
function isComplete(text: string): boolean {
  let depth = 0
  let quote = ''
  for (const c of text) {
    if (quote) {
      if (c === quote) quote = ''
    } else if (c === "'" || c === '"') {
      quote = c
    } else if (c === '(') {
      depth++
    } else if (c === ')') {
      if (--depth < 0) return false
    }
  }
  return depth === 0 && quote === ''
}

const isCall = (text: string) => text.includes('(') && text.endsWith(')')

/**
 * A token's inner text as an argument. `{{.after.name}}`, `{{name}}` and
 * `{{source.name}}` are one field to the engine (evaluator.fieldToken), but
 * inside a call only `source.` is read as one.
 */
function tokenAsArgument(inner: string): string {
  const text = inner.trim()
  if (text.startsWith('source.') || isCall(text)) return text
  return `source.${text.replace(/^\./, '')}`
}

/** A "field or expression" input's text as an argument. */
function fieldAsArgument(text: string): string {
  const literal = /^(['"]).*\1$/.test(text) || text === 'true' || text === 'false' || !Number.isNaN(Number(text))
  if (text.startsWith('source.') || isCall(text) || literal) return text
  return `source.${text}`
}

const quoted = (text: string) => (text.includes("'") ? `"${text}"` : `'${text}'`)

const WHOLE_TOKEN = /^\{\{((?:(?!\}\}).)*)\}\}$/s

/**
 * Applies `fn` to `value`, given the caret or the selection in it.
 *
 * - A selection becomes the call's first argument.
 * - A caret at either end of a finished expression wraps the whole of it:
 *   `source.name` becomes `upper(source.name)`. That is the row made by "+" in
 *   Available Fields, which has never had the caret anywhere.
 * - Otherwise the call goes in at the caret, with its placeholders.
 *
 * What comes back selected is the next thing to fill in -- a placeholder -- so
 * typing, or "Insert variable", replaces it.
 */
export function applyFunction(
  value: string,
  selectionStart: number,
  selectionEnd: number,
  fn: FunctionSnippet,
  mode: FunctionMode
): TextEdit {
  const start = Math.max(0, Math.min(selectionStart, selectionEnd, value.length))
  const end = Math.min(value.length, Math.max(selectionStart, selectionEnd))

  const template = mode === 'token' || value.includes('{{')
  const asToken = template && !isInsideToken(value, start)

  // `first` replaces the first placeholder; null keeps them all.
  const write = (before: string, first: string | null, after: string): TextEdit => {
    const args = first === null ? fn.args : [first, ...fn.args.slice(1)]
    const head = `${before}${asToken ? '{{' : ''}${fn.name}(`
    const text = `${head}${args.join(', ')})${asToken ? '}}' : ''}`
    const next = first === null ? 0 : 1
    if (next >= args.length) {
      return { value: text + after, selectionStart: text.length, selectionEnd: text.length }
    }
    const from = head.length + args.slice(0, next).reduce((n, a) => n + a.length + 2, 0)
    return { value: text + after, selectionStart: from, selectionEnd: from + args[next].length }
  }

  const takesArguments = fn.args.length > 0
  const before = value.slice(0, start)
  const after = value.slice(end)

  if (end > start) {
    const selected = value.slice(start, end)
    if (!takesArguments) return write(before, null, after)
    if (!asToken) return write(before, selected, after)
    const token = WHOLE_TOKEN.exec(selected)
    return write(before, token ? tokenAsArgument(token[1]) : quoted(selected), after)
  }

  const whole = value.trim()
  const leading = value.length - value.trimStart().length
  const atAnEnd = start <= leading || start >= leading + whole.length
  if (!template && takesArguments && whole !== '' && atAnEnd && isComplete(whole)) {
    return write('', mode === 'field' ? fieldAsArgument(whole) : whole, '')
  }

  return write(before, null, after)
}
