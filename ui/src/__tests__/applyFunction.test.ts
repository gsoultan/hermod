import { describe, expect, it } from 'vitest'
import { applyFunction, isInsideToken, type FunctionMode } from '@/lib/expressionInsert'

const upper = { name: 'upper', args: ['value'] }
const replace = { name: 'replace', args: ['value', "'find'", "'with'"] }
const now = { name: 'now', args: [] }

/** Applies fn with the caret at `|`, or the selection between `[` and `]`. */
function apply(marked: string, fn: { name: string; args: string[] }, mode: FunctionMode = 'expression') {
  let start = marked.indexOf('|')
  let end = start
  let value = marked.replace('|', '')
  if (start === -1) {
    start = marked.indexOf('[')
    end = marked.indexOf(']') - 1
    value = marked.replace('[', '').replace(']', '')
  }
  const edit = applyFunction(value, start, end, fn, mode)
  // The same notation back: what is selected afterwards is what the next
  // keystroke, or the next "Insert variable", replaces.
  const { value: v, selectionStart: s, selectionEnd: e } = edit
  return s === e ? `${v.slice(0, s)}|${v.slice(s)}` : `${v.slice(0, s)}[${v.slice(s, e)}]${v.slice(e)}`
}

describe('applyFunction in an expression', () => {
  it('fills an empty value with the call, its first placeholder selected', () => {
    expect(apply('|', upper)).toBe('upper([value])')
    expect(apply('|', replace)).toBe("replace([value], 'find', 'with')")
  })

  it('puts the caret after a call that takes nothing', () => {
    expect(apply('|', now)).toBe('now()|')
  })

  // The row a "+" in Available Fields makes holds source.name and has never
  // had the caret in it. Appending there wrote source.nameupper(value).
  it('wraps a whole value when the caret is at either end of it', () => {
    expect(apply('source.name|', upper)).toBe('upper(source.name)|')
    expect(apply('|source.name', upper)).toBe('upper(source.name)|')
    expect(apply('upper(source.name)|', { name: 'trim', args: ['value'] })).toBe('trim(upper(source.name))|')
  })

  it('selects the next placeholder after wrapping, when there is one', () => {
    expect(apply('source.phone|', replace)).toBe("replace(source.phone, ['find'], 'with')")
  })

  it('wraps what is selected', () => {
    expect(apply('concat([source.first], source.last)', upper)).toBe('concat(upper(source.first)|, source.last)')
  })

  it('inserts at a caret inside the value rather than wrapping all of it', () => {
    expect(apply('concat(source.first, |)', upper)).toBe('concat(source.first, upper([value]))')
  })

  // A value still being typed is not something to wrap: the call would close
  // over an open bracket.
  it('inserts at the end of an unfinished call', () => {
    expect(apply('concat(source.first, |', upper)).toBe('concat(source.first, upper([value])')
    expect(apply("concat('a|", upper)).toBe("concat('aupper([value])")
  })

  it('does not wrap a value in a call that takes nothing', () => {
    expect(apply('source.name|', now)).toBe('source.namenow()|')
    expect(apply('[source.name]', now)).toBe('now()|')
  })

  it('ignores blank space around the value it wraps', () => {
    expect(apply('  source.name  |', upper)).toBe('upper(source.name)|')
  })

  // A value holding {{ is read as a template, where a bare call is just text.
  it('inserts a token into a value that is a template', () => {
    expect(apply('Bearer {{source.token}} |', upper)).toBe('Bearer {{source.token}} {{upper([value])}}')
  })

  it('inserts a bare call inside a token that is still open', () => {
    expect(apply('Hi {{concat(source.a, |)}}', upper)).toBe('Hi {{concat(source.a, upper([value]))}}')
  })
})

describe('applyFunction in a template', () => {
  it('inserts the call as a token', () => {
    expect(apply('|', upper, 'token')).toBe('{{upper([value])}}')
    expect(apply('Hello |!', upper, 'token')).toBe('Hello {{upper([value])}}!')
    expect(apply('at |', now, 'token')).toBe('at {{now()}}|')
  })

  // Inside a call a field is written source.x; a .x token there is text.
  it('wraps a selected token, rewriting its field as an expression reads it', () => {
    expect(apply('Hello [{{.after.name}}]', upper, 'token')).toBe('Hello {{upper(source.after.name)}}|')
    expect(apply('[{{name}}]', upper, 'token')).toBe('{{upper(source.name)}}|')
    expect(apply('[{{source.name}}]', upper, 'token')).toBe('{{upper(source.name)}}|')
    expect(apply('[{{lower(source.name)}}]', upper, 'token')).toBe('{{upper(lower(source.name))}}|')
  })

  it('wraps selected plain text as a quoted literal', () => {
    expect(apply('Hello [world]', upper, 'token')).toBe("Hello {{upper('world')}}|")
    expect(apply("[it's]", upper, 'token')).toBe('{{upper("it\'s")}}|')
  })

  it('never wraps a whole template', () => {
    expect(apply('Hello {{.name}}|', upper, 'token')).toBe('Hello {{.name}}{{upper([value])}}')
  })
})

// A condition's Field is a bare path -- status -- until it is an expression.
// Inside a call that same text is the literal "status".
describe('applyFunction on a field', () => {
  it('wraps a bare path as source.path', () => {
    expect(apply('status|', upper, 'field')).toBe('upper(source.status)|')
    expect(apply('after.user.name|', upper, 'field')).toBe('upper(source.after.user.name)|')
  })

  it('leaves what is already an expression as it is', () => {
    expect(apply('source.status|', upper, 'field')).toBe('upper(source.status)|')
    expect(apply('lower(source.status)|', upper, 'field')).toBe('upper(lower(source.status))|')
  })

  it('fills an empty field with the call', () => {
    expect(apply('|', upper, 'field')).toBe('upper([value])')
  })
})

describe('isInsideToken', () => {
  it('is true between {{ and }}', () => {
    expect(isInsideToken('a {{upper(', 10)).toBe(true)
    expect(isInsideToken('a {{upper()}} b', 6)).toBe(true)
  })

  it('is false outside one', () => {
    expect(isInsideToken('plain', 3)).toBe(false)
    expect(isInsideToken('a {{b}} c', 9)).toBe(false)
    expect(isInsideToken('a {{b}} c', 1)).toBe(false)
  })
})
