import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { requestJson } from '@/lib/executions'

/**
 * Environments: internal/environment and its transport/http handler.
 *
 * Each install says what it is (development, staging or production). A
 * sending install links to the one it promotes to with a one-time pairing
 * code; the receiving install holds a token for it, and every workflow sent
 * waits there for an administrator unless its rules say otherwise.
 */

export type EnvironmentRole = 'development' | 'staging' | 'production'

export interface Instance {
  name: string
  role: EnvironmentRole | ''
}

export type ApprovalRule = 'admin_except_sender' | 'any_admin' | 'none'

export interface Rules {
  approval: ApprovalRule
  auto_rollback: boolean
  min_soak_minutes: number
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
}

export interface Peer {
  id: string
  name: string
  url: string
  created_by: string
  created_at: string
  last_used_at?: string
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
}

export interface PromoteOptions {
  version?: number
  note?: string
  start?: boolean
  auto_rollback?: boolean
}

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
  matching: (linkId: string, vhost: string) => ['environments', 'matching', linkId, vhost] as const,
}

export const getInstance = (signal?: AbortSignal) => requestJson<Instance>(`${api}/self`, { signal })
export const putInstance = (in_: Instance) => requestJson<Instance>(`${api}/self`, { method: 'PUT', ...json(in_) })
export const getRules = (signal?: AbortSignal) => requestJson<Rules>(`${api}/rules`, { signal })
export const putRules = (r: Rules) => requestJson<Rules>(`${api}/rules`, { method: 'PUT', ...json(r) })

export const newPairingCode = (label: string) =>
  requestJson<{ code: string; expires_at: string }>(`${api}/pairing-codes`, { method: 'POST', ...json({ label }) })
export const listPeers = async (signal?: AbortSignal) => (await requestJson<Peer[] | null>(`${api}/peers`, { signal })) ?? []
export async function revokePeer(peerId: string): Promise<void> {
  // 204: no body to parse.
  await apiFetch(`${api}/peers/${id(peerId)}`, { method: 'DELETE', silent: true })
}

export const listLinks = async (signal?: AbortSignal) => (await requestJson<Link[] | null>(`${api}/links`, { signal })) ?? []
export const createLink = (url: string, code: string) =>
  requestJson<Link>(`${api}/links`, { method: 'POST', ...json({ url, code, self_url: window.location.origin }) })
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
  deployed: 'Deployed',
  failed: 'Failed',
  rolled_back: 'Rolled back',
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

/** The address to open on the receiving install so an admin there can approve the link. */
export function acceptUrl(targetUrl: string, selfOrigin: string): string {
  return `${normalizeUrl(targetUrl)}/environments/accept/${encodeOrigin(selfOrigin)}`
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
