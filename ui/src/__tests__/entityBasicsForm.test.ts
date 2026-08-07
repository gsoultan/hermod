import { describe, it, expect } from 'vitest'
import { validateName, validateType, validateVHost } from '@/hooks/useEntityBasicsForm'

// These rules previously did not exist: forms relied on the HTML `required`
// attribute, so anything it let through failed server-side after submit.
describe('validateName', () => {
  it('rejects empty and whitespace-only names', () => {
    expect(validateName('')).toBe('Name is required')
    expect(validateName('   ')).toBe('Name is required')
  })

  it('rejects names that are too short or too long', () => {
    expect(validateName('a')).toMatch(/at least 2/)
    expect(validateName('x'.repeat(65))).toMatch(/64 characters or fewer/)
  })

  it('accepts a reasonable name', () => {
    expect(validateName('Production DB')).toBeUndefined()
    expect(validateName('ab')).toBeUndefined()
    expect(validateName('x'.repeat(64))).toBeUndefined()
  })

  // These characters break DSNs, URLs and shell quoting further down the stack.
  it('rejects quotes and backslashes', () => {
    expect(validateName('bad"name')).toMatch(/quotes or backslashes/)
    expect(validateName("bad'name")).toMatch(/quotes or backslashes/)
    expect(validateName('bad\\name')).toMatch(/quotes or backslashes/)
    expect(validateName('bad`name')).toMatch(/quotes or backslashes/)
  })

  it('trims before measuring length', () => {
    expect(validateName('  ab  ')).toBeUndefined()
    expect(validateName('  a  ')).toMatch(/at least 2/)
  })
})

describe('validateType', () => {
  it('requires a selection', () => {
    expect(validateType('')).toBe('Select a type')
    expect(validateType('   ')).toBe('Select a type')
  })

  it('accepts any non-empty type', () => {
    expect(validateType('postgres')).toBeUndefined()
  })
})

describe('validateVHost', () => {
  it('is required only when the field is shown', () => {
    expect(validateVHost('', true)).toBe('Select a virtual host')
    // Embedded in the workflow editor the VHost comes from the workflow, so
    // demanding it there would block a valid form.
    expect(validateVHost('', false)).toBeUndefined()
  })

  it('accepts a chosen vhost', () => {
    expect(validateVHost('default', true)).toBeUndefined()
  })
})
