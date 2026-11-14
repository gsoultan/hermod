import { savedNodeConfig } from '@/pages/workflows/WorkflowEditor/store/savedNode'
import { persistedSnapshot } from '@/pages/workflows/WorkflowEditor/store/persistedSnapshot'

// What the source drawer hands back is the whole source, password included.
// Spreading it over the node is how a copy of every connection used to be
// saved inside the workflow.
const fromDrawer = {
  id: 'src-1', name: 'pg-src', type: 'postgres', vhost: 'default', active: true, worker_id: '',
  config: { host: 'db.internal', user: 'svc_reader', password: 'S3cretPW-src' },
  state: { lsn: '0/1' }, sample: '{"email":"a@b.c"}', created_at: '0001-01-01T00:00:00Z',
}

describe('savedNodeConfig', () => {
  it('keeps only a connection node’s own settings', () => {
    const node = { id: 'n1', type: 'source', data: { ...fromDrawer, label: 'pg-src', ref_id: 'src-1', lastSample: { email: 'a@b.c' } } }
    expect(savedNodeConfig(node)).toEqual({ label: 'pg-src', type: 'postgres' })
    expect(JSON.stringify(savedNodeConfig(node))).not.toContain('S3cretPW')
  })

  it('keeps a sink node’s execution settings', () => {
    const node = { id: 'n3', type: 'sink', data: { label: 'wh', type: 'postgres', sequential: true, transaction: 'orders', config: { password: 'x' } } }
    expect(savedNodeConfig(node)).toEqual({ label: 'wh', type: 'postgres', sequential: true, transaction: 'orders' })
  })

  it('drops captured data from a step but keeps its settings and a subflow’s name', () => {
    expect(savedNodeConfig({ id: 't', type: 'transformation', data: { transType: 'set', testResult: { payload: 1 }, ref_id: undefined, 'column.x': '1' } }))
      .toEqual({ transType: 'set', 'column.x': '1' })
    expect(savedNodeConfig({ id: 's', type: 'subflow_call', data: { name: 'enrich', label: 'x' } }))
      .toEqual({ name: 'enrich', label: 'x' })
  })

  it('does not count what Save leaves out as an unsaved change', () => {
    const base = { name: 'w', nodes: [{ id: 'n1', type: 'source', position: { x: 0, y: 0 }, data: { label: 'pg-src', type: 'postgres', ref_id: 'src-1' } }], edges: [] }
    const withCopy = { ...base, nodes: [{ ...base.nodes[0], data: { ...base.nodes[0].data, ...fromDrawer, label: 'pg-src', ref_id: 'src-1' } }] }
    expect(persistedSnapshot(withCopy)).toBe(persistedSnapshot(base))
  })
})
