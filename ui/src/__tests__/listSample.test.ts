import { describe, expect, it } from 'vitest'
import type { Node } from '@xyflow/react'
import { ownPayloadOf, simulationInputs } from '@/pages/workflows/WorkflowEditor/sampleCapture'

// A source's stored sample can be a list of rows (a pasted sample, or a source
// that previews several). The editor used it as the message itself: Live
// Preview sent {"0": {...row}} -- every field one level down, so
// array_len(source.users) > 0 was false -- and the Test simulation sent the
// array, which the backend refused outright. One record is a message; the
// newest row of a list is the one to run on, as the live sample view does.
const source = (id: string, ref: string) => ({ id, type: 'source', position: { x: 0, y: 0 }, data: { ref_id: ref } }) as unknown as Node

describe('a sample stored as a list of rows', () => {
  const rows = [{ id: 1, users: [] }, { id: 2, users: [{ id: 9 }] }]
  const sources = [{ id: 'src-1', sample: JSON.stringify(rows) }]

  it('gives the newest row as the payload', () => {
    expect(ownPayloadOf(source('n1', 'src-1'), sources, {})).toEqual(rows[1])
  })

  it('seeds the simulation with one record, not the list', () => {
    expect(simulationInputs([source('n1', 'src-1')], sources, {})).toEqual({ n1: rows[1] })
  })

  it('leaves a one-record sample as it is', () => {
    const one = [{ id: 'src-2', sample: JSON.stringify({ id: 3 }) }]
    expect(ownPayloadOf(source('n2', 'src-2'), one, {})).toEqual({ id: 3 })
  })

  it('gives nothing for an empty list', () => {
    expect(ownPayloadOf(source('n3', 'src-3'), [{ id: 'src-3', sample: '[]' }], {})).toBeNull()
  })
})
