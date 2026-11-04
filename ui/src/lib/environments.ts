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
  /** The other instance lets administrators here approve, reject and undo what this link sends. */
  decide?: boolean
  /** Added to a connection and not allowed there yet: it cannot promote until it is. */
  pending?: boolean
  /** The link whose token asked for it. */
  via?: string
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
  /** Its administrators may approve, reject and undo what it sends here, from there. */
  decide?: boolean
  /** Every vhost of one connection shares it: the first peer's id. */
  group?: string
  /** The connection may add vhosts here without an administrator allowing each. */
  add_vhosts?: boolean
}

/** A vhost a connected instance asked to add, waiting for an administrator here. */
export interface VHostRequest {
  id: string
  group: string
  peer_name: string
  vhost: string
  from_vhost?: string
  requested_at: string
}

/** A vhost here and the vhost there its workflows land in. */
export interface VHostRoute {
  vhost: string
  target_vhost: string
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
  /** Connections it uses with no match there yet, without that side's candidates (those come from matching). */
  unmatched?: MatchRow[] | null
  /** Server variables it reads that are not set there. */
  missing_envs?: string[] | null
  /** Secrets the workflow uses that the vhost there lacks; their values are typed for it, never copied from here. */
  missing_secrets?: string[] | null
  /** For a missing secret a source or sink there reads, those connections ("sink warehouse"). */
  secret_uses?: Record<string, string[]> | null
  /** Each source and sink it uses, and what happens to it there. */
  connections?: ConnectionRow[] | null
}

/** One setting of a connection created there; a credential's value shows only when it is a reference. */
export interface ConnectionSetting {
  key: string
  value: string
  secret?: boolean
}

/**
 * A source or sink a workflow uses: one there replaces it (uses), it is created
 * there on approval (create), a credential typed in plain text keeps it here
 * (blocked), or a person picks its match (unmatched).
 */
export interface ConnectionRow {
  kind: 'source' | 'sink'
  id: string
  name: string
  type: string
  status: 'uses' | 'create' | 'blocked' | 'unmatched'
  target_id?: string
  target_name?: string
  settings?: ConnectionSetting[] | null
  plain_keys?: string[] | null
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
  /** Secrets its vhost lacked when it arrived; the approver fills in what the sender left empty. Names only. */
  secrets?: string[] | null
  /** The ones of secrets the sender typed a value for. */
  secrets_filled?: string[] | null
  /** After the deploy: created from typed values, already there by then, and still without a value. */
  secrets_copied?: string[] | null
  secrets_kept?: string[] | null
  secrets_missing?: string[] | null
  /** Deployed stopped, though sent to start, because a secret is still without a value. */
  kept_stopped?: boolean
  /** Sources and sinks it brings, created here when it is approved. */
  connections?: { kind: 'source' | 'sink'; id: string; name: string; type: string }[] | null
}

export interface PromoteOptions {
  version?: number
  note?: string
  start?: boolean
  auto_rollback?: boolean
  /** Values typed for the secrets the preview lists as missing there, by name. Blank ones are left out. */
  secrets?: Record<string, string>
  /** Settings changed for the connections created there, by connection ID and setting. */
  settings?: Record<string, Record<string, string>>
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
  vhostRequests: ['environments', 'vhost-requests'] as const,
  matching: (linkId: string, vhost: string) => ['environments', 'matching', linkId, vhost] as const,
}

export const getInstance = (signal?: AbortSignal) => requestJson<Instance>(`${api}/self`, { signal })
export const putInstance = (in_: Instance) => requestJson<Instance>(`${api}/self`, { method: 'PUT', ...json(in_) })
export const getRules = (signal?: AbortSignal) => requestJson<Rules>(`${api}/rules`, { signal })
export const putRules = (r: Rules) => requestJson<Rules>(`${api}/rules`, { method: 'PUT', ...json(r) })

export interface PairingOptions {
  /** Let the connection add more vhosts later without a new code. */
  addVHosts?: boolean
  /** Let the linked instance's administrators decide what it sends, from there. */
  decide?: boolean
}
export const newPairingCode = (label: string, vhost: string, opts: PairingOptions = {}) =>
  requestJson<{ code: string; expires_at: string; vhost: string }>(`${api}/pairing-codes`, {
    method: 'POST',
    ...json({ label, vhost, ...(opts.addVHosts ? { add_vhosts: true } : {}), ...(opts.decide ? { decide: true } : {}) }),
  })
export async function setPeerDecide(peerId: string, decide: boolean): Promise<void> {
  // 204: no body to parse.
  await apiFetch(`${api}/peers/${id(peerId)}`, { method: 'PATCH', ...json({ decide }), silent: true })
}
export const listPeers = async (signal?: AbortSignal) => (await requestJson<Peer[] | null>(`${api}/peers`, { signal })) ?? []
export async function revokePeer(peerId: string): Promise<void> {
  // 204: no body to parse.
  await apiFetch(`${api}/peers/${id(peerId)}`, { method: 'DELETE', silent: true })
}

/** Revokes every vhost of the connection the peer belongs to. */
export async function revokePeerGroup(peerId: string): Promise<void> {
  await apiFetch(`${api}/peers/${id(peerId)}?all=true`, { method: 'DELETE', silent: true })
}
export const listVHostRequests = async (signal?: AbortSignal) =>
  (await requestJson<VHostRequest[] | null>(`${api}/vhost-requests`, { signal })) ?? []
export async function allowVHostRequest(requestId: string): Promise<void> {
  await apiFetch(`${api}/vhost-requests/${id(requestId)}/allow`, { method: 'POST', silent: true })
}
export async function declineVHostRequest(requestId: string): Promise<void> {
  await apiFetch(`${api}/vhost-requests/${id(requestId)}`, { method: 'DELETE', silent: true })
}

export const listLinks = async (signal?: AbortSignal) => (await requestJson<Link[] | null>(`${api}/links`, { signal })) ?? []
export const createLink = (url: string, code: string, vhost: string) =>
  requestJson<Link>(`${api}/links`, { method: 'POST', ...json({ url, code, self_url: window.location.origin, vhost }) })
export const deleteLink = (linkId: string) => requestJson<{ note?: string }>(`${api}/links/${id(linkId)}`, { method: 'DELETE' })
/** Adds vhosts to the connection linkId belongs to, without a new pairing code. */
export const addVHosts = (linkId: string, routes: VHostRoute[]) =>
  requestJson<Link[]>(`${api}/links/${id(linkId)}/vhosts`, { method: 'POST', ...json({ routes }) })
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

/** Moves a credential typed in plain text in a source or sink into a vhost secret named name. */
export async function moveToSecret(kind: string, connId: string, key: string, name: string): Promise<void> {
  await apiFetch(`${api}/connections/${id(kind)}/${id(connId)}/secret`, { method: 'POST', ...json({ key, name }), silent: true })
}

/** The vhost names here, for the pickers; empty when the caller may not list them. */
export async function listVHostNames(signal?: AbortSignal): Promise<string[]> {
  const body = await requestJson<{ data?: { name: string }[] } | null>('/api/vhosts', { signal })
  return (body?.data ?? []).map((v) => String(v.name)).sort()
}

export const listPromotions = async (signal?: AbortSignal) =>
  (await requestJson<Promotion[] | null>(`${api}/promotions`, { signal })) ?? []
export const listSent = async (signal?: AbortSignal) => (await requestJson<Promotion[] | null>(`${api}/sent`, { signal })) ?? []
/** Decide, from here, on the instance it went to, a promotion this instance sent. */
export const approveSent = (pid: string, startAnyway = false) =>
  requestJson<Promotion>(`${api}/sent/${id(pid)}/approve`, { method: 'POST', ...(startAnyway ? json({ start_anyway: true }) : {}) })
export const rejectSent = (pid: string, reason: string) =>
  requestJson<Promotion>(`${api}/sent/${id(pid)}/reject`, { method: 'POST', ...json({ reason }) })
export const rollbackSent = (pid: string) => requestJson<Promotion>(`${api}/sent/${id(pid)}/rollback`, { method: 'POST' })
/** startAnyway starts a flow whose secrets are not all set; it is deployed stopped otherwise. */
export function approvePromotion(pid: string, secrets: Record<string, string> = {}, startAnyway = false) {
  const body = { ...(Object.keys(secrets).length ? { secrets } : {}), ...(startAnyway ? { start_anyway: true } : {}) }
  return requestJson<Promotion>(`${api}/promotions/${id(pid)}/approve`, {
    method: 'POST',
    ...(Object.keys(body).length ? json(body) : {}),
  })
}
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

export function useVHostRequests(enabled = true) {
  return useQuery({ queryKey: environmentKeys.vhostRequests, queryFn: ({ signal }) => listVHostRequests(signal), enabled })
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

/** One server this instance promotes to, with every vhost connected to it. */
export interface Server {
  url: string
  name: string
  role: EnvironmentRole | ''
  links: Link[]
}

/** Links grouped by the server they go to, in the order each server was first linked. */
export function groupLinksByServer(links: Link[]): Server[] {
  const out: Server[] = []
  for (const l of links) {
    const url = normalizeUrl(l.url).toLowerCase()
    const s = out.find((x) => x.url === url)
    if (s) s.links.push(l)
    else out.push({ url, name: l.name, role: l.role, links: [l] })
  }
  for (const s of out) s.links.sort((a, b) => (a.vhost || '').localeCompare(b.vhost || ''))
  return out
}

/** The connections sending here: peers grouped by the connection they belong to. */
export function groupPeers(peers: Peer[]): { group: string; name: string; url: string; addVHosts: boolean; peers: Peer[] }[] {
  const out: { group: string; name: string; url: string; addVHosts: boolean; peers: Peer[] }[] = []
  for (const p of peers) {
    const group = p.group || p.id
    const g = out.find((x) => x.group === group)
    if (g) g.peers.push(p)
    else out.push({ group, name: p.name, url: p.url, addVHosts: !!p.add_vhosts, peers: [p] })
  }
  return out
}
