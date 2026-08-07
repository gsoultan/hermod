import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useUnsavedChanges, isDirty } from '@/hooks/useUnsavedChanges'

describe('isDirty', () => {
  it('is false for identical primitives', () => {
    expect(isDirty('a', 'a')).toBe(false)
    expect(isDirty(1, 1)).toBe(false)
  })

  // The important case: form state is rebuilt on every keystroke, so a
  // reference check would report dirty even when the content is unchanged.
  it('compares structurally, not by reference', () => {
    expect(isDirty({ name: 'x' }, { name: 'x' })).toBe(false)
    expect(isDirty(['a', 'b'], ['a', 'b'])).toBe(false)
  })

  it('detects a changed field', () => {
    expect(isDirty({ name: 'x' }, { name: 'y' })).toBe(true)
  })

  it('detects an added field', () => {
    expect(isDirty({ a: 1, b: 2 }, { a: 1 })).toBe(true)
  })

  it('treats typing and undoing as clean', () => {
    const saved = { field: 'hello' }
    let current = { field: 'hello!' }
    expect(isDirty(current, saved)).toBe(true)
    current = { field: 'hello' }
    expect(isDirty(current, saved)).toBe(false)
  })

  it('falls back to dirty for non-serialisable values', () => {
    const cyclic: any = { a: 1 }
    cyclic.self = cyclic
    expect(isDirty(cyclic, { a: 1 })).toBe(true)
  })
})

describe('useUnsavedChanges', () => {
  it('registers a beforeunload listener only while dirty', () => {
    const listeners = new Set<string>()
    const addSpy = window.addEventListener.bind(window)
    const removeSpy = window.removeEventListener.bind(window)

    window.addEventListener = ((type: string, ...rest: any[]) => {
      listeners.add(type)
      return addSpy(type as any, ...(rest as [any]))
    }) as typeof window.addEventListener
    window.removeEventListener = ((type: string, ...rest: any[]) => {
      listeners.delete(type)
      return removeSpy(type as any, ...(rest as [any]))
    }) as typeof window.removeEventListener

    try {
      const clean = renderHook(() => useUnsavedChanges(false))
      expect(listeners.has('beforeunload')).toBe(false)
      clean.unmount()

      const dirty = renderHook(() => useUnsavedChanges(true))
      expect(listeners.has('beforeunload')).toBe(true)

      // Unmounting must detach, or a saved form would keep warning.
      dirty.unmount()
      expect(listeners.has('beforeunload')).toBe(false)
    } finally {
      window.addEventListener = addSpy as typeof window.addEventListener
      window.removeEventListener = removeSpy as typeof window.removeEventListener
    }
  })
})
