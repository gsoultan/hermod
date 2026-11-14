import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { getSessionRole, getSessionUser } from '@/auth/session'
import {
  environmentKeys, groupByRelease, listLinks, listPromotions, listSent, listVHostRequests, getRules, releaseStatus, sentByMeForAnother,
  type Link, type Promotion, type Rules,
} from '@/lib/environments'

/**
 * Everything in Hermod that waits for a person, counted in one place so the
 * sidebar badge, the Approvals tabs and Home all agree.
 *
 * There are two kinds, and they have nothing in common but the word:
 *
 *  - A release is a set of flow versions sent from another environment
 *    (internal/environment). It waits for an administrator of the instance it
 *    was sent to, or of the one that sent it when that instance lets it
 *    decide, and approving it makes those versions live.
 *  - A held message is one message an Approval step of a running flow paused
 *    (internal/approval), or a write an AI agent step wants to make. An editor
 *    approves it and that one message carries on.
 *
 * A connected environment asking to add a virtual host is counted with the
 * releases: it is the same administrator, on the same connection.
 */

export interface Waiting {
  releases: number
  held: number
  requests: number
  total: number
}

export const NOTHING_WAITING: Waiting = { releases: 0, held: 0, requests: 0, total: 0 }

export interface WaitingInput {
  role: string | null | undefined
  username: string | null | undefined
  /** Pending held messages, as /api/approvals counts them. */
  held: number
  /** Promotions sent here. */
  received: Promotion[]
  /** Promotions this instance sent elsewhere. */
  sent: Promotion[]
  links: Link[]
  rules?: Rules
  vhostRequests: number
}

const isAdmin = (role: WaitingInput['role']) => role === 'Administrator'
const canEdit = (role: WaitingInput['role']) => role === 'Administrator' || role === 'Editor'

/** Only what this person may decide: a count they cannot act on is noise. */
export function waitingApprovals(w: WaitingInput): Waiting {
  // An older server, or one without environments, can answer with something other than a list.
  const list = <T,>(v: T[] | unknown): T[] => (Array.isArray(v) ? v : [])
  const held = canEdit(w.role) ? w.held : 0
  let releases = 0
  let requests = 0
  if (isAdmin(w.role)) {
    const me = (w.username ?? '').toLowerCase()
    // The server compares the sender case-insensitively (environment approve).
    const mineToSkip = (p: Promotion) =>
      (w.rules?.by_vhost?.[p.vhost]?.approval ?? w.rules?.approval) === 'admin_except_sender' && p.sender.toLowerCase() === me
    releases += groupByRelease(list<Promotion>(w.received)).filter((ps) => releaseStatus(ps) === 'pending' && !mineToSkip(ps[0])).length
    const deciding = new Set(list<Link>(w.links).filter((l) => l.decide).map((l) => l.id))
    releases += groupByRelease(list<Promotion>(w.sent))
      .filter((ps) => releaseStatus(ps) === 'pending' && deciding.has(ps[0].peer_id) && !sentByMeForAnother(ps[0], w.username)).length
    requests = w.vhostRequests
  }
  return { releases, held, requests, total: releases + held + requests }
}

const POLL = 60_000

/**
 * The live counts for the signed-in person. Each query shares its key with
 * the page that lists the same things, so approving there updates the badge.
 */
export function useWaitingApprovals(): Waiting {
  const role = getSessionRole()
  const username = getSessionUser()?.username
  const admin = isAdmin(role)
  const editor = canEdit(role)
  const opts = { refetchInterval: POLL, retry: false } as const

  const held = useQuery({
    queryKey: ['approvals', 'pending-count'],
    queryFn: async () => {
      const res = await apiFetch('/api/approvals?status=pending&page=1&limit=1', { silent: true })
      return (await res.json()) as { total?: number }
    },
    enabled: editor,
    ...opts,
  })
  const received = useQuery({ queryKey: environmentKeys.promotions, queryFn: ({ signal }) => listPromotions(signal), enabled: admin, ...opts })
  const sent = useQuery({ queryKey: environmentKeys.sent, queryFn: ({ signal }) => listSent(signal), enabled: admin, ...opts })
  const links = useQuery({ queryKey: environmentKeys.links, queryFn: ({ signal }) => listLinks(signal), enabled: admin, ...opts })
  const rules = useQuery({ queryKey: environmentKeys.rules, queryFn: ({ signal }) => getRules(signal), enabled: admin, ...opts })
  const requests = useQuery({
    queryKey: environmentKeys.vhostRequests,
    queryFn: ({ signal }) => listVHostRequests(signal),
    enabled: admin,
    ...opts,
  })

  return waitingApprovals({
    role,
    username,
    held: held.data?.total ?? 0,
    received: received.data ?? [],
    sent: sent.data ?? [],
    links: links.data ?? [],
    rules: rules.data,
    vhostRequests: Array.isArray(requests.data) ? requests.data.length : 0,
  })
}
