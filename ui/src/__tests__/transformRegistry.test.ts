import { describe, it, expect } from 'vitest'
import {
  TRANSFORM_CONFIGS,
  NODE_TYPE_CONFIGS,
  resolveConfigComponent,
} from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

// The registry replaced a 593-line switch plus a chain of if-statements. These
// tests pin the contract so the dispatcher cannot silently lose a type again.
describe('transformation config registry', () => {
  it('resolves by node type before transformation subtype', () => {
    // A `wait` node is always configured by WaitConfig, whatever subtype it claims.
    const byNodeType = resolveConfigComponent('wait', 'mapping')
    expect(byNodeType).toBe(NODE_TYPE_CONFIGS.wait)
    expect(byNodeType).not.toBe(TRANSFORM_CONFIGS.mapping)
  })

  it('falls back to transformation subtype when node type is generic', () => {
    expect(resolveConfigComponent('transformation', 'mapping')).toBe(TRANSFORM_CONFIGS.mapping)
  })

  it('returns undefined for an unknown type so the caller can show guidance', () => {
    expect(resolveConfigComponent('transformation', 'does_not_exist')).toBeUndefined()
    expect(resolveConfigComponent(undefined, undefined)).toBeUndefined()
  })

  it('maps every registered key to an actual component', () => {
    for (const [key, Component] of Object.entries(TRANSFORM_CONFIGS)) {
      expect(Component, `TRANSFORM_CONFIGS["${key}"] is not a component`).toBeTruthy()
    }
    for (const [key, Component] of Object.entries(NODE_TYPE_CONFIGS)) {
      expect(Component, `NODE_TYPE_CONFIGS["${key}"] is not a component`).toBeTruthy()
    }
  })

  // The palette is what users can actually drop on the canvas. Anything
  // offered there must have an editor, or the node is unconfigurable.
  it('has an editor for every transformation offered in the node palette', () => {
    const paletteSubTypes = NODE_CATEGORIES
      .filter((c) => c.group === 'transformations')
      .flatMap((c) => c.items)
      .filter((i) => i.type === 'transformation' || i.type === 'validator')
      .map((i) => i.subType)
      .filter(Boolean) as string[]

    const missing = [...new Set(paletteSubTypes)].filter(
      (st) => !TRANSFORM_CONFIGS[st] && !NODE_TYPE_CONFIGS[st],
    )

    expect(missing, `palette entries with no config component: ${missing.join(', ')}`).toEqual([])
  })

  it('covers the control-flow node types the palette offers', () => {
    const controlTypes = NODE_CATEGORIES
      .flatMap((c) => c.items)
      .map((i) => i.type)
      .filter((t) => !['source', 'sink', 'transformation', 'validator', 'note'].includes(t))

    const missing = [...new Set(controlTypes)].filter(
      (t) => !NODE_TYPE_CONFIGS[t] && !TRANSFORM_CONFIGS[t] && t !== 'merge',
    )

    expect(missing, `control-flow types with no config component: ${missing.join(', ')}`).toEqual([])
  })
})
