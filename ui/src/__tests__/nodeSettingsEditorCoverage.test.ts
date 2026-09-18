import { describe, it, expect } from 'vitest'
import { NODE_TYPE_CONFIGS, resolveConfigComponent } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { rendersNodeEditor } from '@/pages/workflows/WorkflowEditor/components/nodeEditorSurfaces'

// A node whose type is not source/sink/transformation/validator is configured
// in WorkflowNodeSettingsModal, and that modal decided what to render from a
// hand-written list of node types. The list had drifted from the registry, so
// nine node types — foreach among them — opened a panel with nothing in it but
// "Remove Node from Canvas". A Foreach (Fan-out) node could not be given its
// arrayPath at all, and workflow validation then flagged it as unconfigured
// with no way to act on that.
describe('every node type with a registered editor can be configured', () => {
  it('renders an editor for every entry in NODE_TYPE_CONFIGS', () => {
    const missing = Object.keys(NODE_TYPE_CONFIGS).filter(t => !rendersNodeEditor(t))
    expect(missing).toEqual([])
  })

  it('renders an editor for every node type in the palette that has one', () => {
    const paletteTypes = new Set<string>()
    NODE_CATEGORIES.forEach(cat =>
      cat.items.forEach((item: any) => paletteTypes.add(item.type))
    )

    const missing = [...paletteTypes].filter(
      t => resolveConfigComponent(t, undefined) && !rendersNodeEditor(t)
    )
    expect(missing).toEqual([])
  })

  it('keeps the types that have a bespoke fallback inside TransformationForm', () => {
    // merge has no registry entry but TransformationForm explains it, and note
    // predates the registry; dropping them would regress those panels.
    expect(rendersNodeEditor('merge')).toBe(true)
    expect(rendersNodeEditor('note')).toBe(true)
  })

  it('does not claim the surfaces that have their own form', () => {
    expect(rendersNodeEditor('source')).toBe(false)
    expect(rendersNodeEditor('sink')).toBe(false)
  })
})
