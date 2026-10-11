import { renderHook, fireEvent } from '@testing-library/react'
import { vi } from 'vitest'
import { useWorkflowHotkeys } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowHotkeys'

/**
 * The editor's shortcuts were bound to Ctrl only. On macOS, Cmd+S went to the
 * browser, which opened its own Save Page dialog, and Cmd+Enter did nothing.
 * They now answer to Cmd on macOS and Ctrl elsewhere.
 */
describe('editor shortcuts', () => {
  const press = (key: string, mods: { metaKey?: boolean; ctrlKey?: boolean; shiftKey?: boolean }) =>
    fireEvent.keyDown(document.documentElement, { key, ...mods })

  const mount = () => {
    const save = vi.fn()
    const test = vi.fn()
    renderHook(() => useWorkflowHotkeys(save, test))
    return { save, test }
  }

  it('saves with Cmd+S and with Ctrl+S', () => {
    const { save } = mount()

    press('s', { metaKey: true })
    press('s', { ctrlKey: true })

    expect(save).toHaveBeenCalledTimes(2)
  })

  it('tests with Cmd+Enter, and dry-runs with Cmd+Shift+Enter', () => {
    const { test } = mount()

    press('Enter', { metaKey: true })
    press('Enter', { metaKey: true, shiftKey: true })

    expect(test).toHaveBeenNthCalledWith(1, null, false)
    expect(test).toHaveBeenNthCalledWith(2, null, true)
  })
})
