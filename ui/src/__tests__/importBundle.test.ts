import { describe, it, expect } from 'vitest'
import {
  parseImportBundle,
  nodeTransType,
  collectReviewNodes,
  detectConflicts,
  applyConflictDecisions,
  nameCollisions,
  suggestFreeName,
  type ImportBundle,
} from '../utils/importBundle'

const seq = () => {
  let n = 0
  return () => `new-${++n}`
}

describe('parseImportBundle', () => {
  it('rejects text that is not JSON', () => {
    const r = parseImportBundle('not json')
    expect('error' in r && r.error).toMatch(/JSON/i)
  })

  it('rejects JSON that is not a workflow or a bundle', () => {
    const r = parseImportBundle('{"hello":"world"}')
    expect('error' in r && r.error).toBeTruthy()
  })

  it('reads a bundle', () => {
    const r = parseImportBundle(
      JSON.stringify({
        workflow: { id: 'wf-1', name: 'W', nodes: [], edges: [] },
        sources: [{ id: 's1', name: 'S', type: 'postgres', config: {} }],
        sinks: [{ id: 'k1', name: 'K', type: 'postgres', config: {} }],
      }),
    )
    expect('bundle' in r).toBe(true)
    if (!('bundle' in r)) return
    expect(r.bundle.workflow.id).toBe('wf-1')
    expect(r.bundle.sources).toHaveLength(1)
    expect(r.bundle.sinks).toHaveLength(1)
  })

  it('reads a bare workflow, the shape older exports used', () => {
    const r = parseImportBundle(JSON.stringify({ id: 'wf-1', name: 'W', nodes: [], edges: [] }))
    expect('bundle' in r).toBe(true)
    if (!('bundle' in r)) return
    expect(r.bundle.workflow.name).toBe('W')
    expect(r.bundle.sources).toEqual([])
  })

  it('keeps the missing references the export reported', () => {
    const r = parseImportBundle(
      JSON.stringify({
        workflow: { id: 'wf-1', name: 'W', nodes: [], edges: [] },
        missing_refs: [{ kind: 'source', id: 'gone', node_id: 'n1' }],
      }),
    )
    if (!('bundle' in r)) throw new Error('expected a bundle')
    expect(r.bundle.missing_refs).toHaveLength(1)
  })
})

describe('nodeTransType', () => {
  it('reads every shape a stored node writes its subtype in', () => {
    // The editor serialises a node's whole `data` object into `config`, and
    // three readers in the codebase disagree about the key: validation uses
    // transType, internal/ai uses type, and plain nodes carry it on `type`.
    expect(nodeTransType({ id: 'n', type: 'transformation', config: { transType: 'db_lookup' } })).toBe('db_lookup')
    expect(nodeTransType({ id: 'n', type: 'transformation', config: { type: 'db_lookup' } })).toBe('db_lookup')
    expect(nodeTransType({ id: 'n', type: 'db_lookup' })).toBe('db_lookup')
  })
})

describe('collectReviewNodes', () => {
  const wf = {
    id: 'wf',
    name: 'W',
    nodes: [
      { id: 'n1', type: 'source', ref_id: 'src-1' },
      { id: 'n2', type: 'transformation', config: { transType: 'db_lookup', sourceId: 'src-2' } },
      { id: 'n3', type: 'transformation', config: { transType: 'execute_sql', sourceID: 'src-3' } },
      { id: 'n4', type: 'transformation', config: { transType: 'api_lookup', url: 'https://api.example.com' } },
      { id: 'n5', type: 'transformation', config: { transType: 'encrypt', key: 'hunter2', fields: ['a'] } },
      { id: 'n6', type: 'transformation', config: { transType: 'decrypt', key: 'hunter2' } },
      { id: 'n7', type: 'transformation', config: { transType: 'mapping', mappings: [] } },
      { id: 'n8', type: 'sink', ref_id: 'snk-1' },
    ],
    edges: [],
  }

  it('flags every node holding something environment-specific', () => {
    const ids = collectReviewNodes(wf).map((r) => r.node.id)
    expect(ids).toEqual(['n2', 'n3', 'n4', 'n5', 'n6'])
  })

  it('leaves plain data-shaping nodes alone', () => {
    expect(collectReviewNodes(wf).map((r) => r.node.id)).not.toContain('n7')
  })

  it('says why each node was flagged', () => {
    const byId = Object.fromEntries(collectReviewNodes(wf).map((r) => [r.node.id, r.reasons]))
    expect(byId['n2']).toContain('source-reference')
    expect(byId['n4']).toContain('endpoint')
    expect(byId['n5']).toContain('credential')
  })

  it('flags a node whose config holds a credential even if its type is unremarkable', () => {
    // The rule is by shape, the way configsecrets decides on the server: a
    // connector added next year with an api_token is covered on the day it is
    // written, without anyone remembering to add it to a list.
    const found = collectReviewNodes({
      nodes: [{ id: 'x', type: 'transformation', config: { transType: 'custom_thing', api_token: 'abc' } }],
    })
    expect(found.map((r) => r.node.id)).toEqual(['x'])
    expect(found[0].reasons).toContain('credential')
  })

  it('does not flag an empty credential field — there is nothing to review', () => {
    const found = collectReviewNodes({
      nodes: [{ id: 'x', type: 'transformation', config: { transType: 'custom_thing', api_token: '' } }],
    })
    expect(found).toEqual([])
  })
})

describe('detectConflicts', () => {
  const bundle: ImportBundle = {
    workflow: { id: 'wf-1', name: 'Orders', nodes: [], edges: [] },
    sources: [{ id: 'src-1', name: 'Main', type: 'postgres', config: {} }],
    sinks: [{ id: 'snk-1', name: 'Out', type: 'postgres', config: {} }],
  }

  it('finds nothing on a clean instance', () => {
    expect(detectConflicts(bundle, { workflows: [], sources: [], sinks: [] })).toEqual([])
  })

  it('reports each colliding ID with the name it would replace', () => {
    const conflicts = detectConflicts(bundle, {
      workflows: [{ id: 'wf-1', name: 'Something else' }],
      sources: [{ id: 'src-1', name: 'Production DB' }],
      sinks: [],
    })
    expect(conflicts).toHaveLength(2)
    const src = conflicts.find((c) => c.kind === 'source')
    expect(src?.existingName).toBe('Production DB')
    expect(src?.incomingName).toBe('Main')
  })

  it('warns when a name collides under a different ID, which a copy would duplicate', () => {
    const conflicts = detectConflicts(bundle, {
      workflows: [],
      sources: [{ id: 'other-id', name: 'Main' }],
      sinks: [],
    })
    expect(conflicts).toEqual([])
  })
})

describe('applyConflictDecisions', () => {
  // The bundle that exercises all four places a workflow names a resource.
  const bundle = (): ImportBundle => ({
    workflow: {
      id: 'wf-1',
      name: 'Orders',
      dead_letter_sink_id: 'snk-dlq',
      nodes: [
        { id: 'n1', type: 'source', ref_id: 'src-main' },
        { id: 'n2', type: 'transformation', config: { transType: 'db_lookup', sourceId: 'src-lookup' } },
        { id: 'n3', type: 'transformation', config: { transType: 'execute_sql', sourceID: 'src-sql' } },
        { id: 'n4', type: 'sink', ref_id: 'snk-out' },
      ],
      edges: [{ id: 'e1', source_id: 'n1', target_id: 'n2' }],
    },
    sources: [
      { id: 'src-main', name: 'Main', type: 'postgres', config: {} },
      { id: 'src-lookup', name: 'Lookup', type: 'mysql', config: {} },
      { id: 'src-sql', name: 'SQL', type: 'mysql', config: {} },
      { id: 'src-batch', name: 'Batch', type: 'batch_sql', config: { source_id: 'src-main' } },
    ],
    sinks: [
      { id: 'snk-out', name: 'Out', type: 'postgres', config: {} },
      { id: 'snk-dlq', name: 'DLQ', type: 'postgres', config: {} },
    ],
  })

  it('changes nothing when every decision is overwrite', () => {
    const b = bundle()
    const out = applyConflictDecisions(b, { 'src-main': 'overwrite', 'wf-1': 'overwrite' }, seq())
    expect(out).toEqual(b)
  })

  it('rewrites a copied source ID at every place that names it', () => {
    const out = applyConflictDecisions(bundle(), { 'src-main': 'copy' }, seq())

    const copied = out.sources.find((s) => s.name === 'Main')!
    expect(copied.id).toBe('new-1')
    expect(copied.id).not.toBe('src-main')

    // 1. a source node's ref_id
    expect(out.workflow.nodes[0].ref_id).toBe('new-1')
    // 2. the batch_sql source that delegates to it
    expect(out.sources.find((s) => s.name === 'Batch')!.config.source_id).toBe('new-1')
    // Everything else is untouched.
    expect(out.workflow.nodes[1].config!.sourceId).toBe('src-lookup')
  })

  it('rewrites a copied source named only by a transformation config', () => {
    const out = applyConflictDecisions(bundle(), { 'src-lookup': 'copy', 'src-sql': 'copy' }, seq())
    const lookup = out.sources.find((s) => s.name === 'Lookup')!
    const sql = out.sources.find((s) => s.name === 'SQL')!

    expect(out.workflow.nodes[1].config!.sourceId).toBe(lookup.id)
    expect(out.workflow.nodes[2].config!.sourceID).toBe(sql.id)
    expect(lookup.id).not.toBe(sql.id)
  })

  it('rewrites a copied sink at its node and at the dead-letter reference', () => {
    const out = applyConflictDecisions(bundle(), { 'snk-out': 'copy', 'snk-dlq': 'copy' }, seq())
    const out1 = out.sinks.find((s) => s.name === 'Out')!
    const dlq = out.sinks.find((s) => s.name === 'DLQ')!

    expect(out.workflow.nodes[3].ref_id).toBe(out1.id)
    expect(out.workflow.dead_letter_sink_id).toBe(dlq.id)
  })

  it('gives the workflow a new ID without disturbing its node IDs', () => {
    const out = applyConflictDecisions(bundle(), { 'wf-1': 'copy' }, seq())
    expect(out.workflow.id).toBe('new-1')
    expect(out.workflow.nodes.map((n) => n.id)).toEqual(['n1', 'n2', 'n3', 'n4'])
    expect(out.workflow.edges[0].source_id).toBe('n1')
  })

  it('does not mutate the bundle it was given', () => {
    const b = bundle()
    const before = JSON.stringify(b)
    applyConflictDecisions(b, { 'src-main': 'copy', 'wf-1': 'copy' }, seq())
    expect(JSON.stringify(b)).toBe(before)
  })

  it('keeps a copied resource pointing at a non-copied one unchanged', () => {
    const out = applyConflictDecisions(bundle(), { 'src-batch': 'copy' }, seq())
    // src-batch got a new ID, but the source it delegates to did not.
    expect(out.sources.find((s) => s.name === 'Batch')!.config.source_id).toBe('src-main')
  })
})

describe('name collisions', () => {
  // sources.name and sinks.name are `NOT NULL UNIQUE` in the schema
  // (internal/storage/sql/queries.go). A bundle that brings a name already used
  // by a *different* record fails the insert with a raw constraint error from
  // the driver, whichever way the id conflict was resolved — so this has to be
  // caught before the request, not after.
  const bundle: ImportBundle = {
    workflow: { id: 'wf-1', name: 'Orders', nodes: [], edges: [] },
    sources: [{ id: 'src-1', name: 'Main', type: 'postgres', config: {} }],
    sinks: [{ id: 'snk-1', name: 'Out', type: 'postgres', config: {} }],
  }

  it('is silent when no name is taken', () => {
    expect(nameCollisions(bundle, { workflows: [], sources: [], sinks: [] })).toEqual([])
  })

  it('is silent when the same id already holds that name — that is an update', () => {
    const found = nameCollisions(bundle, {
      workflows: [], sinks: [],
      sources: [{ id: 'src-1', name: 'Main' }],
    })
    expect(found).toEqual([])
  })

  it('reports a name held by a different id', () => {
    const found = nameCollisions(bundle, {
      workflows: [], sinks: [],
      sources: [{ id: 'someone-else', name: 'Main' }],
    })
    expect(found).toEqual([{ kind: 'source', id: 'src-1', name: 'Main' }])
  })

  it('compares names the way the database does — case and padding included', () => {
    const found = nameCollisions(bundle, {
      workflows: [], sinks: [],
      sources: [{ id: 'someone-else', name: '  Main  ' }],
    })
    // Trimmed, but not case-folded: SQLite's UNIQUE is case-sensitive by
    // default, so claiming "main" collides would block a name that is legal.
    expect(found).toEqual([{ kind: 'source', id: 'src-1', name: 'Main' }])
  })

  it('does not confuse a sink name with a source name', () => {
    const found = nameCollisions(bundle, {
      workflows: [], sinks: [{ id: 'other', name: 'Main' }], sources: [],
    })
    expect(found).toEqual([])
  })
})

describe('suggestFreeName', () => {
  it('returns the name when it is free', () => {
    expect(suggestFreeName('Main', new Set())).toBe('Main')
  })

  it('appends a copy marker when taken', () => {
    expect(suggestFreeName('Main', new Set(['Main']))).toBe('Main (copy)')
  })

  it('counts up rather than giving up', () => {
    expect(suggestFreeName('Main', new Set(['Main', 'Main (copy)']))).toBe('Main (copy 2)')
    expect(suggestFreeName('Main', new Set(['Main', 'Main (copy)', 'Main (copy 2)']))).toBe('Main (copy 3)')
  })

  it('does not stack markers when asked again about a name that is already a copy', () => {
    expect(suggestFreeName('Main (copy)', new Set(['Main (copy)']))).toBe('Main (copy 2)')
  })
})
