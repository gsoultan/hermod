import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { requestJson } from '@/lib/executions'

/**
 * Environments: internal/environment and its transport/http handler.
 *
 * Each install says what it is (development, staging or production). A
 * sending install connects each of its vhosts to the one it promotes to with
 * a one-time pairing code; the receiving install holds a token for it, scoped
 * to the vhost the workflows land in, and creates that vhost when it is
 * missing. Workflows go as releases: everything sent together is approved,
 * undone and rolled back together.
 */

export type EnvironmentRole = 'development' | 'staging' | 'production'

export interface Instance {
  name: string
  role: EnvironmentRole | ''
}

export type ApprovalRule = 'admin_except_sender' | 'any_admin' | 'none'

export interface VHostRules {
  approval: ApprovalRule
  min_soak_minutes: number
}

export interface Rules {
  approval: ApprovalRule
  auto_rollback: boolean
  min_soak_minutes: number
  /** What a vhost here requires instead of the rules above. */
  by_vhost?: Record<string, VHostRules>
}

export interface Link {
  id: string
  name: string
  url: string
  role: EnvironmentRole | ''
  version?: string
  created_by: string
  created_at: string
  checked_at?: string
  reachable: boolean
  check_note?: string
  mapping?: Record<string, string>
  /** The vhost here whose workflows go through this link; empty on an older link that takes every vhost. */
  vhost: string
  /** The vhost there they land in, and whether linking created it. */
  target_vhost?: string
  vhost_created?: boolean
}

export interface Peer {
  id: string
  name: string
  url: string
  created_by: string
  created_at: string
  last_used_at?: string
  /** The one vhost here it sends into; empty on an older peer. */
  vhost?: string
  from_vhost?: string
}

export interface Connection {
  id: string
  name: string
  type: string
  vhost: string
}

export type MatchStatus = 'matched' | 'suggested' | 'missing'

export interface MatchRow {
  kind: 'source' | 'sink'
  id: string
  name: string
  type: string
  vhost: string
  target_id?: string
  target_name?: string
  status: MatchStatus
  manual: boolean
  candidates: Connection[] | null
}

export interface Check {
  name: string
  ok: boolean
  detail?: string
  blocking: boolean
}

export interface Change {
  kind: 'added' | 'removed' | 'changed'
  node_id?: string
  what: string
}

export interface Preview {
  link_id: string
  link_name: string
  workflow_id: string
  workflow_name: string
  vhost: string
  from_version: number
  from_version_at?: string
  current_version: number
  checks: Check[]
  changes: Change[] | null
  rules: Rules
  ready: boolean
}

export type PromotionStatus = 'pending' | 'deploying' | 'rejected' | 'deployed' | 'failed' | 'rolled_back'

export interface Promotion {
  id: string
  peer_id: string
  /** On the receiving side the sender's name; in the sent log, where it went. */
  from_env: string
  workflow_id: string
  workflow_name: string
  vhost: string
  from_version: number
  sender: string
  note?: string
  start: boolean
  auto_rollback: boolean
  changes?: Change[] | null
  status: PromotionStatus
  created_at: string
  decided_by?: string
  decided_at?: string
  previous_version?: number
  applied_version?: number
  deployed_at?: string
  reason?: string
  /** Shared by every workflow sent together; they are decided together. */
  release_id?: string
}

export interface PromoteOptions {
  version?: number
  note?: string
  start?: boolean
  auto_rollback?: boolean
}

export interface ReleaseItem {
  workflow_id: string
  /** 0 is the newest version. */
  version: number
}

export interface ReleaseGroup {
  link_id: string
  link_name: string
  vhost: string
  target_vhost?: string
  previews: Preview[]
  ready: boolean
}

export interface ReleasePreview {
  groups: ReleaseGroup[] | null
  /** Workflows in a vhost no environment is connected for. */
  unlinked: { workflow_id: string; workflow_name: string; vhost: string }[] | null
  ready: boolean
}

export interface SentRelease {
  link_id: string
  link_name: string
  release_id: string
  promotions: Promotion[]
}

export interface ReadyWorkflow {
  id: string
  name: string
  vhost: string
  version: number
  version_at?: string
  /** 0 when it never went through this link. */
  last_sent_version: number
  active: boolean
}

export interface ReadyLane {
  link_id: string
  link_name: string
  vhost: string
  target_vhost?: string
  workflows: ReadyWorkflow[] | null
}

export type ReleaseOptions = Omit<PromoteOptions, 'version'>

const api = '/api/environments'
const json = (body: unknown): RequestInit => ({ body: JSON.stringify(body) })
const id = (s: string) => encodeURIComponent(s)

export const environmentKeys = {
  self: ['environments', 'self'] as const,
  rules: ['environments', 'rules'] as const,
  links: ['environments', 'links'] as const,
  peers: ['environments', 'peers'] as const,
  promotions: ['environments', 'promotions'] as const,
  sent: ['environments', 'sent'] as const,
  ready: ['environments', 'ready'] as const,
  matching: (linkId: string, vhost: string) => ['environments', 'matching', linkId, vhost] as const,
}

export const getInstance = (signal?: AbortSignal) => requestJson<Instance>(`${api}/self`, { signal })
export const putInstance = (in_: Instance) => requestJson<Instance>(`${api}/self`, { method: 'PUT', ...json(in_) })
export const getRules = (signal?: AbortSignal) => requestJson<Rules>(`${api}/rules`, { signal })
export const putRules = (r: Rules) => requestJson<Rules>(`${api}/rules`, { method: 'PUT', ...json(r) })

export const newPairingCode = (label: string, vhost: string) =>
  requestJson<{ code: string; expires_at: string; vhost: string }>(`${api}/pairing-codes`, { method: 'POST', ...json({ label, vhost }) })
export const listPeers = async (signal?: AbortSignal) => (await requestJson<Peer[] | null>(`${api}/peers`, { signal })) ?? []
export async function revokePeer(peerId: string): Promise<void> {
  // 204: no body to parse.
  await apiFetch(`${api}/peers/${id(peerId)}`, { method: 'DELETE', silent: true })
}

export const listLinks = async (signal?: AbortSignal) => (await requestJson<Link[] | null>(`${api}/links`, { signal })) ?? []
export const createLink = (url: string, code: string, vhost: string) =>
  requestJson<Link>(`${api}/links`, { method: 'POST', ...json({ url, code, self_url: window.location.origin, vhost }) })
export const deleteLink = (linkId: string) => requestJson<{ note?: string }>(`${api}/links/${id(linkId)}`, { method: 'DELETE' })
export const testLink = (linkId: string) =>
  requestJson<{ link: Link; info?: { instance: Instance; rules: Rules; version: string }; error?: string }>(
    `${api}/links/${id(linkId)}/test`,
    { method: 'POST' },
  )
export const getMatching = async (linkId: string, vhost: string, signal?: AbortSignal) => {
  const q = vhost && vhost !== 'all' ? `?vhost=${encodeURIComponent(vhost)}` : ''
  return (await requestJson<MatchRow[] | null>(`${api}/links/${id(linkId)}/matching${q}`, { signal })) ?? []
}
export const putMapping = (linkId: string, mapping: Record<string, string>) =>
  requestJson<Link>(`${api}/links/${id(linkId)}/mapping`, { method: 'PUT', ...json({ mapping }) })

export const previewPromotion = (linkId: string, workflowId: string, version = 0) =>
  requestJson<Preview>(`${api}/links/${id(linkId)}/preview`, { method: 'POST', ...json({ workflow_id: workflowId, version }) })
export const promote = (linkId: string, workflowId: string, opts: PromoteOptions) =>
  requestJson<Promotion>(`${api}/links/${id(linkId)}/promotions`, { method: 'POST', ...json({ workflow_id: workflowId, ...opts }) })

export const listReady = async (signal?: AbortSignal) => (await requestJson<ReadyLane[] | null>(`${api}/ready`, { signal })) ?? []
export const previewRelease = (items: ReleaseItem[]) =>
  requestJson<ReleasePreview>(`${api}/releases/preview`, { method: 'POST', ...json({ items }) })
export const sendRelease = (items: ReleaseItem[], opts: ReleaseOptions) =>
  requestJson<SentRelease[]>(`${api}/releases`, { method: 'POST', ...json({ items, ...opts }) })

/** The vhost names here, for the pickers; empty when the caller may not list them. */
export async function listVHostNames(signal?: AbortSignal): Promise<string[]> {
  const body = await requestJson<{ data?: { name: string }[] } | null>('/api/vhosts', { signal })
  return (body?.data ?? []).map((v) => String(v.name)).sort()
}

export const listPromotions = async (signal?: AbortSignal) =>
  (await requestJson<Promotion[] | null>(`${api}/promotions`, { signal })) ?? []
export const listSent = async (signal?: AbortSignal) => (await requestJson<Promotion[] | null>(`${api}/sent`, { signal })) ?? []
export const approvePromotion = (pid: string) => requestJson<Promotion>(`${api}/promotions/${id(pid)}/approve`, { method: 'POST' })
export const rejectPromotion = (pid: string, reason: string) =>
  requestJson<Promotion>(`${api}/promotions/${id(pid)}/reject`, { method: 'POST', ...json({ reason }) })
export const rollbackPromotion = (pid: string) => requestJson<Promotion>(`${api}/promotions/${id(pid)}/rollback`, { method: 'POST' })

export function useInstance() {
  return useQuery({ queryKey: environmentKeys.self, queryFn: ({ signal }) => getInstance(signal), staleTime: 60_000 })
}

export function useRules(enabled = true) {
  return useQuery({ queryKey: environmentKeys.rules, queryFn: ({ signal }) => getRules(signal), enabled })
}

export function useLinks(enabled = true) {
  return useQuery({ queryKey: environmentKeys.links, queryFn: ({ signal }) => listLinks(signal), enabled })
}

export function usePeers(enabled = true) {
  return useQuery({ queryKey: environmentKeys.peers, queryFn: ({ signal }) => listPeers(signal), enabled })
}

export function useReady(enabled = true) {
  return useQuery({ queryKey: environmentKeys.ready, queryFn: ({ signal }) => listReady(signal), enabled })
}

/** Keyed under ['vhosts'] so creating or deleting a vhost refreshes it. */
export function useVHostNames(enabled = true) {
  return useQuery({ queryKey: ['vhosts', 'names'], queryFn: ({ signal }) => listVHostNames(signal), enabled, staleTime: 60_000 })
}

export function usePromotions(enabled = true) {
  return useQuery({ queryKey: environmentKeys.promotions, queryFn: ({ signal }) => listPromotions(signal), enabled })
}

export function useSent(enabled = true) {
  return useQuery({ queryKey: environmentKeys.sent, queryFn: ({ signal }) => listSent(signal), enabled })
}

export const roleLabel: Record<EnvironmentRole, string> = {
  development: 'Development',
  staging: 'Staging',
  production: 'Production',
}

export const roleColor: Record<EnvironmentRole, string> = {
  development: 'gray',
  staging: 'yellow',
  production: 'red',
}

export const approvalLabel: Record<ApprovalRule, string> = {
  admin_except_sender: 'An administrator other than the sender approves',
  any_admin: 'Any administrator approves, the sender included',
  none: 'No approval: deploy as soon as it arrives',
}

export const statusLabel: Record<PromotionStatus, string> = {
  pending: 'Waiting for approval',
  deploying: 'Deploying',
  rejected: 'Rejected',
  deployed: 'Live',
  failed: 'Failed',
  rolled_back: 'Undone',
}

export const statusColor: Record<PromotionStatus, string> = {
  pending: 'yellow',
  deploying: 'blue',
  rejected: 'gray',
  deployed: 'green',
  failed: 'red',
  rolled_back: 'orange',
}

/** The message a production accept page posts back to the page that opened it. */
export const PAIRING_MESSAGE = 'hermod:pairing-code'

export interface PairingMessage {
  type: typeof PAIRING_MESSAGE
  code: string
}

export function isPairingMessage(data: unknown): data is PairingMessage {
  return (
    typeof data === 'object' && data !== null &&
    (data as PairingMessage).type === PAIRING_MESSAGE &&
    typeof (data as PairingMessage).code === 'string'
  )
}

/**
 * The sending install's origin, in a form that survives as one path segment
 * through the sign-in redirect (which keeps the path and drops the query).
 */
export function encodeOrigin(origin: string): string {
  return btoa(origin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** The origin encodeOrigin wrote, or null when the segment is not one. */
export function decodeOrigin(segment: string): string | null {
  try {
    const padded = segment.replace(/-/g, '+').replace(/_/g, '/')
    const origin = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4))
    const u = new URL(origin)
    if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.origin !== origin) return null
    return origin
  } catch {
    return null
  }
}

/**
 * The address to open on the receiving install so an admin there can approve
 * the link, and the vhost there the workflows should land in.
 */
export function acceptUrl(targetUrl: string, selfOrigin: string, vhost: string): string {
  return `${normalizeUrl(targetUrl)}/environments/accept/${encodeOrigin(selfOrigin)}/${encodeURIComponent(vhost)}`
}

/** The vhost names the server takes for a link or a rule (environment.ValidateVHostName). */
export function vhostNameProblem(name: string): string | null {
  if (!name) return 'Name the vhost.'
  if (name === 'all') return '"all" is not a vhost.'
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(name)) {
    return 'Use letters, digits, dot, dash or underscore, starting with a letter or digit, up to 64 characters.'
  }
  return null
}

/** Promotions sent together, newest first; a promotion without a release is its own. */
export function groupByRelease(promotions: Promotion[]): Promotion[][] {
  const out: Promotion[][] = []
  const at = new Map<string, number>()
  for (const p of promotions) {
    const key = p.release_id ? `${p.peer_id}|${p.release_id}` : `one|${p.id}`
    const i = at.get(key)
    if (i === undefined) {
      at.set(key, out.length)
      out.push([p])
    } else {
      out[i].push(p)
    }
  }
  return out
}

/** "orders", "orders and leave", "orders, leave and 3 more". */
export function workflowNames(ps: { workflow_name: string }[]): string {
  const names = ps.map((p) => p.workflow_name)
  if (names.length <= 1) return names[0] ?? ''
  if (names.length === 2) return `${names[0]} and ${names[1]}`
  if (names.length === 3) return `${names[0]}, ${names[1]} and ${names[2]}`
  return `${names[0]}, ${names[1]} and ${names.length - 2} more`
}

/** What a whole release is at, from its members. */
export function releaseStatus(ps: Promotion[]): PromotionStatus {
  for (const s of ['deploying', 'pending', 'failed', 'rolled_back', 'rejected'] as PromotionStatus[]) {
    if (ps.some((p) => p.status === s)) return s
  }
  return 'deployed'
}

/** What the server will accept as an instance address, trimmed of trailing slashes. */
export function normalizeUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, '')
}

/** Null when the address is usable, else what is wrong with it. */
export function urlProblem(raw: string): string | null {
  const s = normalizeUrl(raw)
  if (!s) return 'Enter the address of the other Hermod.'
  let u: URL
  try {
    u = new URL(s)
  } catch {
    return 'This is not a web address. It should look like https://hermod.example.com'
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'The address must start with http:// or https://'
  if (u.username || u.password || u.search || u.hash) return 'The address must not hold a user, a query or a fragment.'
  return null
}

/** XXXX-XXXX, upper case, as the receiving install shows it. */
export function formatPairingCode(raw: string): string {
  const s = raw.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8)
  return s.length > 4 ? `${s.slice(0, 4)}-${s.slice(4)}` : s
}
