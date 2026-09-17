import { describe, it, expect } from 'vitest'
import type { Node, Edge } from '@xyflow/react'
import {
  findUpstreamSource,
  resolveSampleSource,
  sampleTableFor,
  shouldAutoSample,
} from '@/pages/workflows/WorkflowEditor/sampleCapture'

/**
 * Available Fields on a transformation is built from the upstream source's
 * stored sample. A batch_sql source created through the API — or through the
 * wizard without pressing Test Connection — has no stored sample, so every
 * downstream node opened with an empty list and nothing said why.
 *
 * The sample is captured by exactly two user actions (Test Connection, and the
 * refresh icon beside AVAILABLE FIELDS); neither is on the path an operator
 * takes to open a transformation. These helpers back the automatic capture, so
 * the list fills on open for the sources where sampling reads without
 * consuming.
 */

const src = (id: string, refId: string): Node =>
  ({ id, type: 'source', data: { ref_id: refId } }) as unknown as Node
const tr = (id: string): Node => ({ id, type: 'transformation', data: {} }) as unknown as Node
const sink = (id: string): Node => ({ id, type: 'sink', data: {} }) as unknown as Node
const edge = (source: string, target: string): Edge =>
  ({ id: `${source}-${target}`, source, target }) as Edge

describe('findUpstreamSource', () => {
  it('finds the source feeding a transformation', () => {
    const nodes = [src('n-src', 'batch-1'), tr('n-tr'), sink('n-snk')]
    const edges = [edge('n-src', 'n-tr'), edge('n-tr', 'n-snk')]
    const sources = [{ id: 'batch-1', type: 'batch_sql', config: {} }]

    expect(findUpstreamSource('n-tr', nodes, edges, sources)?.id).toBe('batch-1')
  })

  // handleRefreshFields took `nodes.find(n => n.type === 'source')` — the first
  // source anywhere in the workflow, in node order, regardless of which branch
  // the operator had open. On a two-source workflow that sampled the wrong
  // database and filled the panel with another branch's columns.
  it('picks the source on the selected node\'s own branch, not the first in the workflow', () => {
    const nodes = [src('n-a', 'kafka-1'), src('n-b', 'batch-1'), tr('n-tr'), sink('n-snk')]
    const edges = [edge('n-a', 'n-snk'), edge('n-b', 'n-tr'), edge('n-tr', 'n-snk')]
    const sources = [
      { id: 'kafka-1', type: 'kafka', config: {} },
      { id: 'batch-1', type: 'batch_sql', config: {} },
    ]

    expect(findUpstreamSource('n-tr', nodes, edges, sources)?.id).toBe('batch-1')
  })

  it('walks through a chain of transformations', () => {
    const nodes = [src('n-src', 'batch-1'), tr('n-t1'), tr('n-t2')]
    const edges = [edge('n-src', 'n-t1'), edge('n-t1', 'n-t2')]
    const sources = [{ id: 'batch-1', type: 'batch_sql', config: {} }]

    expect(findUpstreamSource('n-t2', nodes, edges, sources)?.id).toBe('batch-1')
  })

  it('returns the node itself when a source node is selected', () => {
    const nodes = [src('n-src', 'batch-1'), tr('n-tr')]
    const edges = [edge('n-src', 'n-tr')]
    const sources = [{ id: 'batch-1', type: 'batch_sql', config: {} }]

    expect(findUpstreamSource('n-src', nodes, edges, sources)?.id).toBe('batch-1')
  })

  // A cycle drawn in the editor must not hang the panel.
  it('terminates on a cycle', () => {
    const nodes = [tr('n-a'), tr('n-b')]
    const edges = [edge('n-a', 'n-b'), edge('n-b', 'n-a')]

    expect(findUpstreamSource('n-a', nodes, edges, [])).toBeNull()
  })

  it('returns null when the branch has no source', () => {
    const nodes = [tr('n-tr'), sink('n-snk')]
    const edges = [edge('n-tr', 'n-snk')]

    expect(findUpstreamSource('n-snk', nodes, edges, [])).toBeNull()
  })
})

describe('resolveSampleSource', () => {
  // A transformation dragged in but not yet wired has no branch to walk. The
  // old code reached for the first source in the workflow; when there is only
  // one that is the same answer, so the convenience is kept where it cannot be
  // wrong.
  it('falls back to the only source when the node is not wired up yet', () => {
    const nodes = [src('n-src', 'batch-1'), tr('n-loose')]
    const sources = [{ id: 'batch-1', type: 'batch_sql', config: {} }]

    expect(resolveSampleSource('n-loose', nodes, [], sources)?.id).toBe('batch-1')
  })

  it('refuses to guess when an unwired node could have come from either source', () => {
    const nodes = [src('n-a', 'batch-1'), src('n-b', 'batch-2'), tr('n-loose')]
    const sources = [
      { id: 'batch-1', type: 'batch_sql', config: {} },
      { id: 'batch-2', type: 'batch_sql', config: {} },
    ]

    expect(resolveSampleSource('n-loose', nodes, [], sources)).toBeNull()
  })

  it('prefers the wired branch over the fallback', () => {
    const nodes = [src('n-a', 'batch-1'), src('n-b', 'batch-2'), tr('n-tr')]
    const edges = [edge('n-b', 'n-tr')]
    const sources = [
      { id: 'batch-1', type: 'batch_sql', config: {} },
      { id: 'batch-2', type: 'batch_sql', config: {} },
    ]

    expect(resolveSampleSource('n-tr', nodes, edges, sources)?.id).toBe('batch-2')
  })
})

describe('sampleTableFor', () => {
  it('uses the configured table', () => {
    expect(sampleTableFor({ table: 'orders' })).toBe('orders')
  })

  it('uses a collection when that is what the source calls it', () => {
    expect(sampleTableFor({ collection: 'events' })).toBe('events')
  })

  it('takes the first of a comma separated list', () => {
    expect(sampleTableFor({ tables: ' orders , customers ' })).toBe('orders')
  })

  // batch_sql carries `queries`, never `table`/`tables`. The empty string is
  // what tells the backend to preview the configured query instead of building
  // "SELECT * FROM <table>".
  it('is empty for a batch_sql config, which names queries rather than tables', () => {
    expect(sampleTableFor({ source_id: 'pg-1', queries: '["SELECT 1"]' })).toBe('')
  })
})

describe('shouldAutoSample', () => {
  it('samples a batch_sql source that has never been sampled', () => {
    expect(shouldAutoSample({ id: 'batch-1', type: 'batch_sql', config: {} })).toBe(true)
  })

  it('leaves a source that already has a sample alone', () => {
    expect(
      shouldAutoSample({ id: 'batch-1', type: 'batch_sql', config: {}, sample: '{"after":{}}' })
    ).toBe(false)
  })

  // Sampling a queue consumes a message. Doing that because someone opened a
  // node would silently eat real data, so auto-capture is limited to the types
  // isNonDestructiveSample already certifies as read-only.
  it('never samples a queue on its own', () => {
    for (const type of ['kafka', 'rabbitmq', 'nats', 'mqtt']) {
      expect(shouldAutoSample({ id: 'q', type, config: {} }), type).toBe(false)
    }
  })

  // A source that has run is still sampled: captureSample omits `state` from
  // the write and the handler keeps the row's own cursor, so no copy of the
  // source held in the browser — however stale — can rewind a watermark.
  it('samples a source that has already advanced a cursor', () => {
    expect(
      shouldAutoSample({ id: 'batch-1', type: 'batch_sql', config: {}, state: { last_value: '42' } })
    ).toBe(true)
  })

  it('handles a missing source', () => {
    expect(shouldAutoSample(null)).toBe(false)
  })
})
