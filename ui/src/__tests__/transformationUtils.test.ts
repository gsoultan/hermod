import { describe, it, expect } from 'vitest'
import { 
  getValByPath, 
  getAllKeys, 
  setValByPath, 
  matchesCondition, 
  preparePayload, 
  deepMergeSim,
  type Condition 
} from '../utils/transformationUtils'

describe('getValByPath', () => {
  const testCases: { name: string; obj: any; path: string; expected: any }[] = [
    { name: 'nested object path', obj: { a: { b: { c: 5 } } }, path: 'a.b.c', expected: 5 },
    { name: 'array index in path', obj: { arr: [{ x: 1 }, { x: 2 }, { x: 3 }] }, path: 'arr.1.x', expected: 2 },
    { name: 'missing path returns undefined', obj: { a: { b: 1 } }, path: 'a.c', expected: undefined },
    { name: 'empty path returns undefined', obj: { a: 1 }, path: '', expected: undefined },
    { name: 'JSON string auto-parse', obj: { s: '{"foo": {"bar": 42}}' }, path: 's.foo.bar', expected: 42 },
    { name: 'top-level key', obj: { name: 'test' }, path: 'name', expected: 'test' },
    { name: 'null object', obj: null, path: 'a', expected: null },
    { name: 'array at root', obj: [1, 2, 3], path: '1', expected: 2 },
  ]

  testCases.forEach(({ name, obj, path, expected }) => {
    it(name, () => {
      expect(getValByPath(obj, path)).toEqual(expected)
    })
  })
})

describe('getAllKeys', () => {
  const testCases: { name: string; obj: any; expected: string[] }[] = [
    { 
      name: 'flat object', 
      obj: { a: 1, b: 2 }, 
      expected: ['a', 'b'] 
    },
    { 
      name: 'nested object', 
      obj: { a: { b: 1 } }, 
      expected: ['a', 'a.b'] 
    },
    { 
      name: 'deeply nested', 
      obj: { a: { b: { c: 1 } } }, 
      expected: ['a', 'a.b', 'a.b.c'] 
    },
    { 
      name: 'with array (arrays not expanded)', 
      obj: { items: [1, 2], name: 'test' }, 
      expected: ['items', 'name'] 
    },
    { 
      name: 'empty object', 
      obj: {}, 
      expected: [] 
    },
    { 
      name: 'null input', 
      obj: null, 
      expected: [] 
    },
  ]

  testCases.forEach(({ name, obj, expected }) => {
    it(name, () => {
      expect(getAllKeys(obj)).toEqual(expected)
    })
  })
})

describe('setValByPath', () => {
  const testCases: { name: string; initial: any; path: string; value: any; expected: any }[] = [
    { 
      name: 'set top-level key', 
      initial: {}, 
      path: 'name', 
      value: 'test', 
      expected: { name: 'test' } 
    },
    { 
      name: 'set nested key (creates path)', 
      initial: {}, 
      path: 'a.b.c', 
      value: 42, 
      expected: { a: { b: { c: 42 } } } 
    },
    { 
      name: 'overwrite existing value', 
      initial: { a: 1 }, 
      path: 'a', 
      value: 2, 
      expected: { a: 2 } 
    },
    { 
      name: 'append to array with -1', 
      initial: { arr: [1, 2] }, 
      path: 'arr.-1', 
      value: 3, 
      expected: { arr: [1, 2, 3] } 
    },
    { 
      name: 'set array index', 
      initial: { arr: [1, 2, 3] }, 
      path: 'arr.1', 
      value: 99, 
      expected: { arr: [1, 99, 3] } 
    },
  ]

  testCases.forEach(({ name, initial, path, value, expected }) => {
    it(name, () => {
      const obj = JSON.parse(JSON.stringify(initial))
      setValByPath(obj, path, value)
      expect(obj).toEqual(expected)
    })
  })
})

describe('matchesCondition', () => {
  const testCases: { name: string; payload: any; condition: Condition; expected: boolean }[] = [
    { 
      name: 'equals string match', 
      payload: { status: 'active' }, 
      condition: { field: 'status', operator: '=', value: 'active' }, 
      expected: true 
    },
    { 
      name: 'equals string no match', 
      payload: { status: 'inactive' }, 
      condition: { field: 'status', operator: '=', value: 'active' }, 
      expected: false 
    },
    { 
      name: 'not equals', 
      payload: { status: 'active' }, 
      condition: { field: 'status', operator: '!=', value: 'inactive' }, 
      expected: true 
    },
    { 
      name: 'greater than numeric', 
      payload: { count: 10 }, 
      condition: { field: 'count', operator: '>', value: 5 }, 
      expected: true 
    },
    { 
      name: 'less than numeric', 
      payload: { count: 3 }, 
      condition: { field: 'count', operator: '<', value: 5 }, 
      expected: true 
    },
    { 
      name: 'greater than or equal', 
      payload: { count: 5 }, 
      condition: { field: 'count', operator: '>=', value: 5 }, 
      expected: true 
    },
    { 
      name: 'contains substring', 
      payload: { message: 'hello world' }, 
      condition: { field: 'message', operator: 'contains', value: 'world' }, 
      expected: true 
    },
    { 
      name: 'contains no match', 
      payload: { message: 'hello' }, 
      condition: { field: 'message', operator: 'contains', value: 'world' }, 
      expected: false 
    },
    { 
      name: 'nested field path', 
      payload: { user: { role: 'admin' } }, 
      condition: { field: 'user.role', operator: '=', value: 'admin' }, 
      expected: true 
    },
  ]

  testCases.forEach(({ name, payload, condition, expected }) => {
    it(name, () => {
      expect(matchesCondition(payload, condition)).toBe(expected)
    })
  })
})

describe('preparePayload', () => {
  const testCases: { name: string; input: any; expectedKeys: string[] }[] = [
    { 
      name: 'parses JSON in after field', 
      input: { after: '{"name": "test", "id": 1}' }, 
      expectedKeys: ['after', 'name', 'id'] 
    },
    { 
      name: 'hoists after fields to root', 
      input: { table: 'users', after: { name: 'John', age: 30 } }, 
      expectedKeys: ['table', 'after', 'name', 'age'] 
    },
    { 
      name: 'does not overwrite existing keys', 
      input: { name: 'original', after: { name: 'from_after' } }, 
      expectedKeys: ['name', 'after'] 
    },
    { 
      name: 'handles null input', 
      input: null, 
      expectedKeys: [] 
    },
  ]

  testCases.forEach(({ name, input, expectedKeys }) => {
    it(name, () => {
      const result = preparePayload(input)
      if (input === null) {
        expect(result).toBeNull()
      } else {
        expect(Object.keys(result).sort()).toEqual(expectedKeys.sort())
      }
    })
  })

  it('does not overwrite existing root keys with after fields', () => {
    const input = { name: 'original', after: { name: 'from_after', extra: 'value' } }
    const result = preparePayload(input)
    expect(result.name).toBe('original')
    expect(result.extra).toBe('value')
  })
})

describe('deepMergeSim', () => {
  const testCases: { name: string; dst: any; src: any; strategy: string; expected: any }[] = [
    { 
      name: 'deep merge nested objects', 
      dst: { a: { b: 1 } }, 
      src: { a: { c: 2 } }, 
      strategy: 'deep', 
      expected: { a: { b: 1, c: 2 } } 
    },
    { 
      name: 'overwrite replaces entirely', 
      dst: { a: { b: 1, c: 2 } }, 
      src: { a: { d: 3 } }, 
      strategy: 'overwrite', 
      expected: { a: { d: 3 } } 
    },
    { 
      name: 'if_missing keeps existing', 
      dst: { a: 1, b: 2 }, 
      src: { a: 99, c: 3 }, 
      strategy: 'if_missing', 
      expected: { a: 1, b: 2, c: 3 } 
    },
    { 
      name: 'shallow merge top-level', 
      dst: { a: { x: 1 } }, 
      src: { a: { y: 2 }, b: 3 }, 
      strategy: 'shallow', 
      expected: { a: { y: 2 }, b: 3 } 
    },
  ]

  testCases.forEach(({ name, dst, src, strategy, expected }) => {
    it(name, () => {
      const target = JSON.parse(JSON.stringify(dst))
      deepMergeSim(target, src, strategy)
      expect(target).toEqual(expected)
    })
  })
})
