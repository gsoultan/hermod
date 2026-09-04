/**
 * Filtering for the workflow panel's node palette.
 *
 * The palette lists well over a hundred sources, sinks and transformations
 * across three tabs and a dozen collapsed categories, and the only way to find
 * one was to scroll and recognise it. That works if you already know the label
 * Hermod chose; it does not if you know the product by another name, or you are
 * looking for "the one that drops records" without knowing it is called Filter.
 *
 * Kept separate from the component so the matching rules can be tested for what
 * they find, rather than through a store-driven drawer.
 */

export interface PaletteItem {
  type?: string;
  label?: string;
  subType?: string;
  description?: string;
  [key: string]: unknown;
}

export interface PaletteCategory {
  title: string;
  group?: string;
  items: PaletteItem[];
  [key: string]: unknown;
}

export interface PaletteMatchCounts {
  sources: number;
  sinks: number;
  transformations: number;
}

/**
 * Spaces, underscores and hyphens are dropped on both sides of the comparison.
 *
 * Labels and sub-types disagree about them constantly -- the item labelled
 * "Filter" has the sub-type `filter_data`, and "Foreach / Fanout" is `foreach`.
 * Someone typing "filter data" means the same thing as "filter_data", and a
 * literal match finds neither from the other.
 */
const normalise = (value: string) => value.toLowerCase().replace(/[\s_/-]+/g, '');

/**
 * matchesQuery reports whether an item should survive the given query.
 *
 * Description is searched as well as label and sub-type, so "drop records"
 * finds Filter without knowing its name. An empty query matches everything,
 * which keeps the caller from having to special-case the unfiltered palette.
 */
export function matchesQuery(item: PaletteItem, query: string): boolean {
  const q = normalise(query.trim());
  if (!q) return true;

  return [item.label, item.subType, item.description, item.type].some(
    (value) => typeof value === 'string' && normalise(value).includes(q)
  );
}

/**
 * filterCategories narrows each category to its matching items and drops the
 * categories left empty, so the palette does not show a column of headings with
 * nothing under them.
 *
 * An empty query returns the original array untouched rather than a rebuilt
 * copy: the unfiltered palette is the common case and it should cost nothing.
 */
export function filterCategories<T extends PaletteCategory>(categories: T[], query: string): T[] {
  if (!query.trim()) return categories;

  return categories
    .map((cat) => ({ ...cat, items: cat.items.filter((item) => matchesQuery(item, query)) }))
    .filter((cat) => cat.items.length > 0);
}

/**
 * countMatches totals the matches in each tab.
 *
 * The palette is tabbed, so a search can easily match nothing where you are
 * looking and plenty one tab over -- searching "postgres" from Transformations
 * is the obvious case. Counting all three lets an empty tab say where the
 * results actually are instead of just showing nothing.
 */
export function countMatches(categories: PaletteCategory[], query: string): PaletteMatchCounts {
  const counts: PaletteMatchCounts = { sources: 0, sinks: 0, transformations: 0 };

  for (const cat of filterCategories(categories, query)) {
    const group = cat.group;
    if (group === 'sources' || group === 'sinks' || group === 'transformations') {
      counts[group] += cat.items.length;
    }
  }

  return counts;
}
