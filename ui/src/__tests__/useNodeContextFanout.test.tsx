import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// A source whose rows carry a nested list. Everything the editor can say about
// what a foreach node emits has to come out of this array's elements.
const sample = {
  order_id: 42,
  customer: 'acme',
  lines: [
    { sku: 'A-1', qty: 2, price: 9.5 },
    { sku: 'B-7', qty: 1, price: 30 },
  ],
}

const paths = (fields: { path: string }[]) => fields.map(f => f.path)

const resetStore = (nodes: Node[], edges: Edge[], nodeSamples: Record<string, any>) => {
  useWorkflowStore.setState({ nodes, edges, nodeSamples })
}

describe('useNodeContext fan-out schema propagation', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {} })
  })

  it('offers _item/_index downstream of a foreach node', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const fe = { id: 'fe', type: 'foreach', data: { config: { arrayPath: 'lines' } } } as unknown as Node
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    const edges = [
      { id: 'e1', source: 'src', target: 'fe' },
      { id: 'e2', source: 'fe', target: 'snk' },
    ] as Edge[]
    resetStore([src, fe, snk], edges, { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))
    const got = paths(result.current.availableFields)

    expect(got).toContain('_item')
    expect(got).toContain('_index')
    // The element shape is knowable from the sample, and it is the only thing a
    // downstream mapping can actually address.
    expect(got).toContain('_item.sku')
    expect(got).toContain('_item.qty')
    // The row the item came from is still on the message.
    expect(got).toContain('order_id')
  })

  it('offers the materialised array downstream of a foreach/fanout transformation', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const tr = {
      id: 'tr',
      type: 'transformation',
      data: { config: { transType: 'fanout', arrayPath: 'lines', resultField: 'expanded' } },
    } as unknown as Node
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    const edges = [
      { id: 'e1', source: 'src', target: 'tr' },
      { id: 'e2', source: 'tr', target: 'snk' },
    ] as Edge[]
    resetStore([src, tr, snk], edges, { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))
    const got = paths(result.current.availableFields)

    expect(got).toContain('expanded')
  })

  it('defaults the fanout transformation result to _fanout', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const tr = {
      id: 'tr',
      type: 'transformation',
      data: { config: { transType: 'foreach', arrayPath: 'lines' } },
    } as unknown as Node
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    const edges = [
      { id: 'e1', source: 'src', target: 'tr' },
      { id: 'e2', source: 'tr', target: 'snk' },
    ] as Edge[]
    resetStore([src, tr, snk], edges, { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))

    expect(paths(result.current.availableFields)).toContain('_fanout')
  })

  it('offers the collected batch downstream of a collect node', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const fe = { id: 'fe', type: 'foreach', data: { config: { arrayPath: 'lines' } } } as unknown as Node
    const co = { id: 'co', type: 'collect', data: { config: {} } } as unknown as Node
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    const edges = [
      { id: 'e1', source: 'src', target: 'fe' },
      { id: 'e2', source: 'fe', target: 'co' },
      { id: 'e3', source: 'co', target: 'snk' },
    ] as Edge[]
    resetStore([src, fe, co, snk], edges, { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))
    const got = paths(result.current.availableFields)

    expect(got).toContain('_items')
    expect(got).toContain('_count')
  })
})

// useWorkflowInitialization hydrates a saved node as
// `data: { ...node.config, ref_id }` — the config is spread *flat* onto data,
// not nested under data.config, and TransformationForm passes
// `config: selectedNode.data` for exactly that reason. Schema propagation read
// node.data.config, so for any workflow loaded from storage it read an empty
// object: every inferred field that depends on config was missing.
describe('useNodeContext reads the config shape the editor actually stores', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {} })
  })

  const hydrated = (id: string, type: string, config: Record<string, any>) =>
    ({ id, type, data: { ...config, ref_id: 'new' } }) as unknown as Node

  it('expands the item shape from a flat foreach config', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const fe = hydrated('fe', 'foreach', { arrayPath: 'lines' })
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    resetStore([src, fe, snk], [
      { id: 'e1', source: 'src', target: 'fe' },
      { id: 'e2', source: 'fe', target: 'snk' },
    ] as Edge[], { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))

    expect(paths(result.current.availableFields)).toContain('_item.sku')
  })

  it('reads a flat collect target field', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const co = hydrated('co', 'collect', { targetField: 'picked' })
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    resetStore([src, co, snk], [
      { id: 'e1', source: 'src', target: 'co' },
      { id: 'e2', source: 'co', target: 'snk' },
    ] as Edge[], { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))

    expect(paths(result.current.availableFields)).toContain('picked')
  })

  it('reads a flat transformation targetField, which is the pre-existing propagation', () => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const tr = hydrated('tr', 'transformation', { transType: 'db_lookup', targetField: 'enriched' })
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    resetStore([src, tr, snk], [
      { id: 'e1', source: 'src', target: 'tr' },
      { id: 'e2', source: 'tr', target: 'snk' },
    ] as Edge[], { src: sample })

    const { result } = renderHook(() => useNodeContext(snk, null, [], []))

    expect(paths(result.current.availableFields)).toContain('enriched')
  })
})

// ForeachNode drops the iterated array from every message it emits — carrying
// it was what made the fan-out cost quadratic. Offering `lines` downstream of
// the split therefore offers a path that resolves to nothing at run time, which
// is the same silent-empty-mapping the whole Available Fields work is about.
describe('useNodeContext hides the array a foreach consumed', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {} })
  })

  const chain = (foreachConfig: Record<string, any>) => {
    const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const fe = { id: 'fe', type: 'foreach', data: { ...foreachConfig, ref_id: 'new' } } as unknown as Node
    const snk = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    resetStore([src, fe, snk], [
      { id: 'e1', source: 'src', target: 'fe' },
      { id: 'e2', source: 'fe', target: 'snk' },
    ] as Edge[], { src: sample })
    return snk
  }

  it('drops the consumed array downstream', () => {
    const { result } = renderHook(() => useNodeContext(chain({ arrayPath: 'lines' }), null, [], []))
    const got = paths(result.current.availableFields)

    expect(got).not.toContain('lines')
    expect(got).toContain('_item.sku')
    // Everything else on the row still travels with each item.
    expect(got).toContain('order_id')
  })

  it('keeps the array when the node is configured to carry it', () => {
    const { result } = renderHook(() =>
      useNodeContext(chain({ arrayPath: 'lines', keepSourceArray: true }), null, [], [])
    )

    expect(paths(result.current.availableFields)).toContain('lines')
  })
})
