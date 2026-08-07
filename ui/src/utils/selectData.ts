export interface FlatSelectItem {
  value: string
  label: string
  group?: string
}

export interface GroupedSelectItem {
  group: string
  items: { value: string; label: string }[]
}

/**
 * Converts a flat option list into Mantine's grouped Select shape.
 *
 * Mantine expects `[{ group, items: [...] }]`, not a `group` key on each flat
 * option — passing the latter makes its combobox parser throw
 * "Cannot read properties of undefined (reading 'map')". Keeping the source
 * lists flat (and deriving the grouping here) means the rest of the code can
 * still treat them as a simple array.
 *
 * Group order follows first appearance, so the caller controls presentation by
 * ordering the source list. Ungrouped options are returned first, before any
 * group headers, matching how Mantine renders them.
 */
export function toGroupedSelectData(
  items: FlatSelectItem[] | undefined | null,
): (GroupedSelectItem | { value: string; label: string })[] {
  if (!Array.isArray(items) || items.length === 0) return []

  const ungrouped: { value: string; label: string }[] = []
  const order: string[] = []
  const byGroup = new Map<string, { value: string; label: string }[]>()

  for (const item of items) {
    if (!item || typeof item.value !== 'string') continue
    const entry = { value: item.value, label: item.label ?? item.value }
    const group = item.group?.trim()
    if (!group) {
      ungrouped.push(entry)
      continue
    }
    if (!byGroup.has(group)) {
      byGroup.set(group, [])
      order.push(group)
    }
    byGroup.get(group)!.push(entry)
  }

  return [
    ...ungrouped,
    ...order.map((group) => ({ group, items: byGroup.get(group)! })),
  ]
}
