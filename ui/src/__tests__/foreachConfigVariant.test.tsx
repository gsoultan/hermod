import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ForeachConfig } from '@/components/workflow/Transformation/configs/logic/ForeachConfig'

// Two different nodes answer to "foreach" and this one editor serves both:
//
//   type: 'foreach'                  -> internal/engine/registry/nodes/control/foreach.go
//                                       splits the message, one per item, each with _item/_index
//   type: 'transformation', foreach  -> pkg/comm/transformer/logic/foreach.go
//                                       keeps one message and materialises the array on it
//
// The panel described only the first. A user who took "Foreach / Fanout" from
// Common Transformations was told downstream nodes would run once per item and
// that _item and _index would be there; neither happens on that path.
describe('ForeachConfig tells the truth about which foreach this is', () => {
  const renderFor = (nodeType: string, config: any = {}, updateNodeConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <ForeachConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          nodeType={nodeType}
        />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  it('describes execution fan-out for the foreach node type', () => {
    renderFor('foreach', { arrayPath: 'lines' })
    expect(screen.getByText(/once for each item/i)).toBeTruthy()
    expect(screen.getByText(/_item/)).toBeTruthy()
  })

  it('does not promise per-item execution for the foreach transformation', () => {
    renderFor('transformation', { arrayPath: 'lines' })
    expect(screen.queryByText(/once for each item/i)).toBeNull()
    // It writes the expanded array onto the same message instead.
    expect(screen.getByText(/_fanout/)).toBeTruthy()
  })

  it('exposes the transformation result field, which the node has no use for', async () => {
    const user = userEvent.setup()
    const update = renderFor('transformation', { arrayPath: 'lines' })

    const input = screen.getByRole('textbox', { name: /result field/i })
    await user.type(input, 'expanded')

    expect(update.mock.calls.at(-1)?.[1].resultField).toBeTruthy()
    // The node-type editor must not offer it: ForeachNode ignores resultField.
    renderFor('foreach', { arrayPath: 'lines' })
    expect(screen.queryAllByRole('textbox', { name: /result field/i })).toHaveLength(1)
  })

  it('still requires an array path on both', () => {
    renderFor('transformation', {})
    expect(screen.getByText(/Array Path is required/i)).toBeTruthy()
  })
})

// The fan-out width is decided by an upstream row, so the node caps it. The cap
// and the opt-out that restores the old (quadratic) cost both have to be
// reachable from the editor: the transformation's own options were previously
// settable only by hand-editing a bundle, and that is the same mistake.
describe('ForeachConfig exposes the fan-out bound', () => {
  const renderNode = (config: any = {}, updateNodeConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <ForeachConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          nodeType="foreach"
        />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  it('offers Max Items on the fan-out node', async () => {
    const user = userEvent.setup()
    const update = renderNode({ arrayPath: 'lines' })

    const max = screen.getByRole('textbox', { name: /max items/i })
    await user.type(max, '250')

    expect(update.mock.calls.at(-1)?.[1].maxItems).toBeTruthy()
  })

  it('offers the keep-source-array opt-in and says what it costs', async () => {
    const user = userEvent.setup()
    const update = renderNode({ arrayPath: 'lines' })

    const keep = screen.getByRole('switch', { name: /carry the source array/i })
    expect((keep as HTMLInputElement).checked).toBe(false)
    await user.click(keep)

    expect(update.mock.calls.at(-1)?.[1].keepSourceArray).toBe(true)
  })

  it('does not offer either on the transformation, which fans out nothing', () => {
    render(
      <MantineProvider>
        <ForeachConfig config={{ arrayPath: 'lines' }} updateNodeConfig={vi.fn()} nodeId="n2" nodeType="transformation" />
      </MantineProvider>
    )
    expect(screen.queryByRole('textbox', { name: /max items/i })).toBeNull()
    expect(screen.queryByRole('switch', { name: /carry the source array/i })).toBeNull()
  })
})
