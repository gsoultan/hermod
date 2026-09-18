import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { CharMapConfig } from '@/components/workflow/Transformation/configs/data/CharMapConfig'

// This editor wrote the chosen operation under `op`, and char_map.go read
// `operations` and `operation` and never `op`. The operation list came out
// empty, so every Character Map node copied its field through untouched with a
// green node and nothing in the logs. The editor is the only way to build one,
// so that was every node.
//
// The key written here is the key pkg/comm/transformer/core/char_map.go reads.
// A rename on either side puts the node straight back to doing nothing.
describe('char_map operation key', () => {
  const renderConfig = (config: any, updateNodeConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <CharMapConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" fieldPaths={[]} />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  const pick = async (user: ReturnType<typeof userEvent.setup>, label: string) => {
    const select = screen.getByRole('combobox', { name: /operation/i })
    await user.click(select)
    const dropdown = document.getElementById(select.getAttribute('aria-controls') || '') as HTMLElement
    const option = Array.from(dropdown.querySelectorAll('[role="option"]'))
      .find((o) => o.textContent === label) as HTMLElement
    await user.click(option)
  }

  it('writes the operation under the key the node reads', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ field: 'name' })
    await pick(user, 'lowercase')
    expect(updateNodeConfig.mock.calls.at(-1)?.[1].operation).toBe('lowercase')
  })

  // A node stored before the key was aligned still holds `op`, and the backend
  // reads it, so the editor has to show what that node actually does.
  it('shows the operation from a config stored under the old key', () => {
    renderConfig({ field: 'name', op: 'trim' })
    expect(screen.getByRole('combobox', { name: /operation/i })).toHaveValue('Trim whitespace')
  })

  it('shows the operation from a config stored under the new key', () => {
    renderConfig({ field: 'name', operation: 'lowercase' })
    expect(screen.getByRole('combobox', { name: /operation/i })).toHaveValue('lowercase')
  })

  // Editing an old node clears the stale key, so the config does not end up
  // carrying two operations that disagree.
  it('clears the old key when an old config is edited', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ field: 'name', op: 'trim' })
    await pick(user, 'UPPERCASE')
    const patch = updateNodeConfig.mock.calls.at(-1)?.[1]
    expect(patch.operation).toBe('uppercase')
    expect(patch.op).toBeUndefined()
    expect('op' in patch).toBe(true)
  })

  // Every option the control offers must be one the node's switch implements,
  // or it is a selection that silently does nothing.
  it('offers only operations the node implements', async () => {
    const user = userEvent.setup()
    renderConfig({ field: 'name' })
    const select = screen.getByRole('combobox', { name: /operation/i })
    await user.click(select)
    const dropdown = document.getElementById(select.getAttribute('aria-controls') || '') as HTMLElement
    expect(Array.from(dropdown.querySelectorAll('[role="option"]')).map((o) => o.textContent)).toEqual([
      'UPPERCASE', 'lowercase', 'Trim whitespace', 'Trim Left', 'Trim Right',
    ])
  })
})
