/** The vhost every flow, source and sink lives in until others are created. */
export const DEFAULT_VHOST = 'default'

/**
 * The virtual hosts the Add source / Add sink forms offer.
 *
 * An administrator may use any vhost: `default` -- which needs no record and
 * is where a new flow is saved -- and every one created since. Anyone else may
 * use only the vhosts they were given.
 *
 * Listing only created vhosts left the picker empty on a fresh install, so no
 * source or sink could be added through the UI at all.
 */
export function connectionVHostChoices(
  role: string | null | undefined,
  storedVHosts: ReadonlyArray<{ name: string } | string>,
  assigned: readonly string[],
): string[] {
  if (role !== 'Administrator') return [...assigned]
  const names = storedVHosts.map((v) => (typeof v === 'string' ? v : v.name)).filter(Boolean)
  return Array.from(new Set([DEFAULT_VHOST, ...names]))
}
