/**
 * What the suite has to cover, read from the app's own sources.
 *
 * The lists are not typed out here. They are parsed from the files the UI
 * itself renders from, so a new source, sink, transformation, function or page
 * is picked up by the next run without anyone editing a spec. A hand-kept list
 * is how coverage quietly falls behind: the earlier one-off QA pass counted 171
 * palette items, and a list frozen at that number would say "all covered" the
 * day the 172nd shipped.
 *
 * Parsing TypeScript with regular expressions is deliberate. Importing these
 * modules would drag React, Mantine and the icon set into the Node process that
 * runs Playwright. Each parser asserts it found a plausible number of entries,
 * so a reformatted file fails loudly instead of yielding an empty list that
 * covers nothing and passes.
 */
import { readFileSync } from 'fs';
import { dirname, join } from 'path';
import { fileURLToPath } from 'url';

const UI_SRC = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'src');
const read = (rel: string) => readFileSync(join(UI_SRC, rel), 'utf8');

function atLeast<T>(what: string, items: T[], min: number): T[] {
  if (items.length < min) {
    throw new Error(
      `catalog: parsed only ${items.length} ${what} (expected at least ${min}). ` +
        'The file it is read from probably changed shape; update the parser in ui/__tests__/coverage/catalog.ts.'
    );
  }
  return items;
}

// ---------------------------------------------------------------------------
// Palette: every step the workflow editor offers.
// ---------------------------------------------------------------------------

export interface PaletteItem {
  /** sources | sinks | transformations */
  group: 'sources' | 'sinks' | 'transformations';
  /** The category card it sits under, e.g. "Databases". */
  category: string;
  /** The node type the canvas gets: source, sink, transformation, condition... */
  type: string;
  label: string;
  subType: string;
}

export const PALETTE: PaletteItem[] = (() => {
  const src = read('pages/workflows/WorkflowEditor/constants/nodeCategories.ts');
  const items: PaletteItem[] = [];
  // Each category is `{ title: '...', group: '...', items: [ ... ] }`.
  const catRe = /title:\s*'([^']+)',\s*group:\s*'(sources|sinks|transformations)',\s*items:\s*\[([\s\S]*?)\n\s*\]/g;
  for (const m of src.matchAll(catRe)) {
    const [, category, group, body] = m;
    const itemRe = /\{\s*type:\s*'([^']+)',[^}]*?label:\s*'((?:[^'\\]|\\.)+)',\s*subType:\s*'([^']+)'/g;
    for (const it of body.matchAll(itemRe)) {
      items.push({
        group: group as PaletteItem['group'],
        category,
        type: it[1],
        label: it[2].replace(/\\'/g, "'"),
        subType: it[3],
      });
    }
  }
  return atLeast('palette items', items, 150);
})();

// ---------------------------------------------------------------------------
// Source and sink types: what the Add source / Add sink forms offer.
// ---------------------------------------------------------------------------

export interface ConnectorType {
  value: string;
  label: string;
  group: string;
}

function parseTypes(rel: string, constName: string, min: number): ConnectorType[] {
  const src = read(rel);
  const start = src.indexOf(`export const ${constName} = [`);
  if (start < 0) throw new Error(`catalog: ${constName} not found in ${rel}`);
  const body = src.slice(start, src.indexOf('\n];', start));
  const out: ConnectorType[] = [];
  for (const m of body.matchAll(/\{\s*value:\s*'([^']+)',\s*label:\s*'((?:[^'\\]|\\.)+)'\s*,\s*group:\s*'([^']+)'/g)) {
    out.push({ value: m[1], label: m[2].replace(/\\'/g, "'"), group: m[3] });
  }
  return atLeast(constName, out, min);
}

export const SOURCE_TYPES = parseTypes('components/forms/SourceForm.tsx', 'SOURCE_TYPES', 35);
export const SINK_TYPES = parseTypes('components/forms/SinkForm.tsx', 'SINK_TYPES', 40);

// ---------------------------------------------------------------------------
// Expression functions, with the engine's own expected answers.
// ---------------------------------------------------------------------------

export interface ExpressionFunction {
  name: string;
  category: string;
  signature: string;
  example: string;
  result?: unknown;
  volatile?: boolean;
}

const fnCatalog = JSON.parse(read('lib/expressionFunctions.json')) as {
  source: Record<string, unknown>;
  functions: ExpressionFunction[];
};

/** The record every catalog example is evaluated against. */
export const FUNCTION_SAMPLE: Record<string, unknown> = fnCatalog.source;
export const FUNCTIONS: ExpressionFunction[] = atLeast('expression functions', fnCatalog.functions, 60);

// ---------------------------------------------------------------------------
// Routes: every page the router declares, as a full path.
// ---------------------------------------------------------------------------

/**
 * Every full route path in router.tsx, e.g. `/sources/$sourceId/edit`.
 *
 * Child routes declare a path relative to their parent (`getParentRoute`), so
 * the full path is assembled by walking up. Layout-only parents (a route with
 * a path but no page of its own, like `/sources`, whose `/` child is the page)
 * still appear; the pages spec decides what each one renders.
 */
export const ROUTES: string[] = (() => {
  const src = read('router.tsx');
  const parent = new Map<string, string>();
  const ownPath = new Map<string, string>();
  const re = /const\s+(\w+)\s*=\s*createRoute\(\{\s*getParentRoute:\s*\(\)\s*=>\s*(\w+),\s*path:\s*'([^']+)'/g;
  for (const m of src.matchAll(re)) {
    parent.set(m[1], m[2]);
    ownPath.set(m[1], m[3]);
  }
  const full = (name: string): string => {
    const own = ownPath.get(name);
    if (own === undefined) return ''; // the root route
    const up = full(parent.get(name)!);
    if (own.startsWith('/')) return up && own !== '/' ? up + own : up || own;
    return `${up}/${own}`;
  };
  const paths = [...ownPath.keys()].map(full).map((p) => (p.length > 1 ? p.replace(/\/$/, '') : p));
  return atLeast('routes', [...new Set(paths)], 40);
})();
