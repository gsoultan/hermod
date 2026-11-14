import { describe, it, expect } from 'vitest'
import { parseImportBundle, forImport, importErrorMessage, WITHHELD } from '../utils/importBundle'

// An export leaves each connection credential out as WITHHELD. The wizard
// shows those fields empty so a person can type them, and an empty one goes
// back as WITHHELD so the server keeps this instance's own value.
const file = JSON.stringify({
  workflow: { id: 'wf-1', name: 'orders', nodes: [], edges: [] },
  sources: [{ id: 'src-1', name: 'pg', type: 'postgres', config: { host: 'db', password: WITHHELD, user: 'app' } }],
  sinks: [{ id: 'snk-1', name: 'hook', type: 'http', config: { url: WITHHELD } }],
  missing_refs: [],
})

describe('credentials an export left out', () => {
  it('are shown empty and listed on the resource', () => {
    const r = parseImportBundle(file)
    if (!('bundle' in r)) throw new Error(r.error)
    const src = r.bundle.sources[0]
    expect(src.config.password).toBe('')
    expect(src.config.host).toBe('db')
    expect(src.withheld).toEqual(['password'])
    expect(r.bundle.sinks[0].withheld).toEqual(['url'])
  })

  it('go back as left out when nothing was typed, and as typed otherwise', () => {
    const r = parseImportBundle(file)
    if (!('bundle' in r)) throw new Error(r.error)
    r.bundle.sinks[0].config.url = 'https://hooks.example.com'
    const payload = forImport(r.bundle)
    expect(payload.sources[0].config.password).toBe(WITHHELD)
    expect(payload.sinks[0].config.url).toBe('https://hooks.example.com')
    expect('withheld' in payload.sources[0]).toBe(false)
    expect('missing_refs' in payload).toBe(false)
  })
})

describe('importErrorMessage', () => {
  it('names each credential the server still needs', () => {
    expect(importErrorMessage('Enter them.', {
      missing_credentials: [{ kind: 'source', id: 's1', name: 'pg', field: 'password' }, { kind: 'sink', id: 'k1', field: 'api_key' }],
    })).toBe('Enter them. Missing: source pg: password; sink k1: api_key.')
  })
  it('is the message alone otherwise', () => {
    expect(importErrorMessage('Boom.', {})).toBe('Boom.')
    expect(importErrorMessage('Boom.')).toBe('Boom.')
  })
})
