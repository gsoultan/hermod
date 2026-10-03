import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'

/** One secret as the API lists it: a name and who changed it when. Never a value. */
export interface VHostSecretEntry {
  name: string
  updated_by: string
  updated_at: string
  created_at?: string
}

/** What a secret may be named: the same rule storage.ValidVHostSecretName applies. */
export const SECRET_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/

export const secretNameRule =
  'Letters, digits and underscores, starting with a letter or underscore.'

/** A vhost that secrets can belong to: a real one, not the "every vhost" filter. */
export function isSecretVHost(vhost: string | undefined | null): vhost is string {
  return !!vhost && vhost !== 'all'
}

export const vhostSecretsKey = (vhost: string) => ['vhost-secrets', vhost] as const

const secretsUrl = (vhost: string) => `/api/vhosts/${encodeURIComponent(vhost)}/secrets`

export async function listVHostSecrets(vhost: string, signal?: AbortSignal): Promise<VHostSecretEntry[]> {
  // silent: the caller shows a refusal where it happened.
  const res = await apiFetch(secretsUrl(vhost), { signal, silent: true })
  const body = await res.json()
  return (body?.data ?? []) as VHostSecretEntry[]
}

export async function saveVHostSecret(vhost: string, name: string, value: string): Promise<void> {
  await apiFetch(`${secretsUrl(vhost)}/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
    silent: true,
  })
}

export async function deleteVHostSecret(vhost: string, name: string): Promise<void> {
  await apiFetch(`${secretsUrl(vhost)}/${encodeURIComponent(name)}`, { method: 'DELETE', silent: true })
}

/**
 * The names of a vhost's secrets, for the editor's picker.
 *
 * It answers an empty list rather than an error: a Viewer, a vhost nobody has
 * saved a secret in, and a storage backend without vhost secrets all look the
 * same to someone writing an expression -- there is nothing to pick.
 */
export function useVHostSecretNames(vhost: string | undefined, enabled = true): string[] {
  const { data } = useQuery({
    queryKey: vhostSecretsKey(vhost ?? ''),
    queryFn: ({ signal }) => listVHostSecrets(vhost as string, signal),
    enabled: enabled && isSecretVHost(vhost),
    retry: false,
    staleTime: 30_000,
  })
  return (data ?? []).map((s) => s.name)
}
