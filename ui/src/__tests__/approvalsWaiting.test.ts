import { describe, expect, it } from 'vitest'
import { waitingApprovals } from '@/lib/approvals'
import type { Link, Promotion, Rules } from '@/lib/environments'

const promo = (over: Partial<Promotion>): Promotion => ({
  id: 'p', peer_id: 'peer', from_env: 'Staging', workflow_id: 'wf', workflow_name: 'orders', vhost: 'hr',
  from_version: 1, sender: 'ada', start: true, auto_rollback: false, status: 'pending', created_at: '2026-11-05T09:00:00Z',
  ...over,
})

const link = (over: Partial<Link>): Link => ({
  id: 'l-1', name: 'Production', url: 'https://prod', role: 'production', created_by: 'root', created_at: '',
  reachable: true, vhost: 'hr', ...over,
})

const anyAdmin: Rules = { approval: 'any_admin', auto_rollback: false, min_soak_minutes: 0 }
const notSender: Rules = { approval: 'admin_except_sender', auto_rollback: false, min_soak_minutes: 0 }

const none = { held: 0, received: [], sent: [], links: [], vhostRequests: 0 }

describe('what is waiting for you', () => {
  it('counts a release once, however many flows it carries', () => {
    const w = waitingApprovals({
      ...none,
      role: 'Administrator', username: 'root', rules: anyAdmin,
      received: [
        promo({ id: 'a', release_id: 'r1', workflow_id: 'w1' }),
        promo({ id: 'b', release_id: 'r1', workflow_id: 'w2' }),
        promo({ id: 'c', release_id: 'r2' }),
        promo({ id: 'd', status: 'deployed' }),
        promo({ id: 'e', status: 'deploying' }),
      ],
    })
    expect(w.releases).toBe(2)
    expect(w.total).toBe(2)
  })

  it('leaves out a release you sent when another administrator must approve it', () => {
    const w = waitingApprovals({
      ...none,
      role: 'Administrator', username: 'Ada', rules: notSender,
      received: [promo({ id: 'a', sender: 'ada' }), promo({ id: 'b', sender: 'grace' })],
    })
    expect(w.releases).toBe(1)
  })

  it('applies the rule of the release vhost when it has its own', () => {
    const w = waitingApprovals({
      ...none,
      role: 'Administrator', username: 'ada',
      rules: { ...notSender, by_vhost: { hr: { approval: 'any_admin', min_soak_minutes: 0 } } },
      received: [promo({ id: 'a', sender: 'ada', vhost: 'hr' })],
    })
    expect(w.releases).toBe(1)
  })

  it('counts a release sent from here only where production lets administrators here decide', () => {
    const sent = [
      promo({ id: 'a', peer_id: 'l-1', release_id: 'r1' }),
      promo({ id: 'b', peer_id: 'l-2', release_id: 'r2' }),
      promo({ id: 'c', peer_id: 'l-1', status: 'deployed' }),
    ]
    const links = [link({ id: 'l-1', decide: true }), link({ id: 'l-2', decide: false })]
    expect(waitingApprovals({ ...none, role: 'Administrator', username: 'root', sent, links }).releases).toBe(1)
  })

  it('leaves out a release you sent from here when production wants another administrator', () => {
    const sent = [
      promo({ id: 'a', peer_id: 'l-1', release_id: 'r1', sender: 'Root', approval: 'admin_except_sender' }),
      promo({ id: 'b', peer_id: 'l-1', release_id: 'r2', sender: 'root', approval: 'any_admin' }),
      promo({ id: 'c', peer_id: 'l-1', release_id: 'r3', sender: 'grace', approval: 'admin_except_sender' }),
    ]
    const links = [link({ id: 'l-1', decide: true })]
    expect(waitingApprovals({ ...none, role: 'Administrator', username: 'root', sent, links }).releases).toBe(2)
  })

  it('adds held messages and connection requests to the total', () => {
    const w = waitingApprovals({ ...none, role: 'Administrator', username: 'root', held: 3, vhostRequests: 1 })
    expect(w).toEqual({ releases: 0, held: 3, requests: 1, total: 4 })
  })

  it('gives an editor only the held messages, which is all an editor may decide', () => {
    const w = waitingApprovals({
      role: 'Editor', username: 'ed', held: 2, received: [promo({})], sent: [promo({})],
      links: [link({ id: 'peer', decide: true })], vhostRequests: 1,
    })
    expect(w).toEqual({ releases: 0, held: 2, requests: 0, total: 2 })
  })

  it('gives a viewer nothing to decide', () => {
    const w = waitingApprovals({ ...none, role: 'Viewer', username: 'v', held: 5, received: [promo({})] })
    expect(w.total).toBe(0)
  })
})
