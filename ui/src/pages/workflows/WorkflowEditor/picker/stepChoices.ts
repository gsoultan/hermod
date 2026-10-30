import type { ComponentType } from 'react';
import { IconPuzzle, IconTable } from '@tabler/icons-react';
import { NODE_CATEGORIES } from '../constants/nodeCategories';
import { matchesQuery } from '../utils/paletteSearch';

/**
 * The add-a-step picker asks "What should happen next?" and answers with
 * eight plain choices. Every node type in the palette lands in exactly one of
 * them, by what it does rather than by the heading the catalogue files it
 * under, so the picker and the palette are two doors to the same list.
 */

export interface StepItem {
  type: string;
  refId: string;
  label: string;
  subType: string;
  icon: ComponentType<{ size?: string | number; style?: React.CSSProperties }>;
  color: string;
  description?: string;
  /** A saved connection rather than a type to configure. */
  existing?: boolean;
  /** An installed plugin. */
  plugin?: boolean;
  pluginID?: string;
  defaults?: Record<string, unknown>;
}

export type ChoiceKey = 'read' | 'keep' | 'change' | 'lookup' | 'flow' | 'code' | 'ai' | 'send';

export interface StepChoice {
  key: ChoiceKey;
  title: string;
  hint: string;
  items: StepItem[];
}

export const STEP_CHOICES: Omit<StepChoice, 'items'>[] = [
  { key: 'read', title: 'Read from somewhere', hint: 'A database, a queue, a webhook, a file, a schedule.' },
  { key: 'keep', title: 'Only keep some records', hint: 'Filter, validate, score or throttle what comes through.' },
  { key: 'change', title: 'Change the data', hint: 'Map, set, rename, mask, parse, flatten, convert.' },
  { key: 'lookup', title: 'Look something up', hint: 'Enrich each record from a table, an API or a reference list.' },
  { key: 'flow', title: 'Branch, wait or approve', hint: 'If, route, switch, merge, wait, aggregate, human approval.' },
  { key: 'code', title: 'Run code or SQL', hint: 'Lua, WebAssembly, a SQL statement.' },
  { key: 'ai', title: 'Ask AI', hint: 'Prompt, extract, classify, summarize, translate, predict.' },
  { key: 'send', title: 'Send somewhere', hint: 'A database, a warehouse, a queue, an API, a chat, a file.' },
];

/** Where a transformation goes, by what it does; the catalogue heading is the fallback. */
function choiceFor(categoryTitle: string, item: { label: string; subType?: string; type?: string }): ChoiceKey {
  const label = item.label.toLowerCase();
  if (/lookup|enrich/.test(label)) return 'lookup';
  if (/lua|wasm|execute sql/.test(label)) return 'code';
  if (/^filter|validat|quality scorer|rate limit|circuit breaker/.test(label)) return 'keep';
  if (/condition|router|switch|merge|fan-out|wait|aggregate|^stateful|approval|pipeline|multicast|^note/.test(label)) return 'flow';
  switch (categoryTitle) {
    case 'AI':
    case 'Machine Learning':
    case 'Feature Engineering':
      return 'ai';
    case 'Logic & Flow':
    case 'Utilities':
      return 'flow';
    default:
      return 'change';
  }
}

interface Saved { id: string; name: string; type: string }
interface Plugin { id: string; name: string; type: string; description?: string }

export function stepChoices({ sources, sinks, plugins }: { sources: Saved[]; sinks: Saved[]; plugins: Plugin[] }): StepChoice[] {
  const buckets: Record<ChoiceKey, StepItem[]> = { read: [], keep: [], change: [], lookup: [], flow: [], code: [], ai: [], send: [] };
  // Saved connections first: the thing a person most often means by "read from the orders database".
  for (const s of Array.isArray(sources) ? sources : []) {
    buckets.read.push({ type: 'source', refId: s.id, label: s.name, subType: s.type, icon: IconTable, color: 'blue', description: s.type, existing: true });
  }
  for (const s of Array.isArray(sinks) ? sinks : []) {
    buckets.send.push({ type: 'sink', refId: s.id, label: s.name, subType: s.type, icon: IconTable, color: 'green', description: s.type, existing: true });
  }
  for (const cat of NODE_CATEGORIES) {
    for (const item of cat.items as any[]) {
      const key: ChoiceKey = cat.group === 'sources' ? 'read' : cat.group === 'sinks' ? 'send' : choiceFor(cat.title, item);
      buckets[key].push({ ...item });
    }
  }
  for (const pl of Array.isArray(plugins) ? plugins : []) {
    const kind = pl.type.toLowerCase();
    const isSink = kind === 'connector';
    buckets[isSink ? 'send' : 'change'].push({
      type: isSink ? 'sink' : 'transformation',
      refId: 'new',
      label: pl.name,
      subType: kind === 'wasm' || kind === 'transformer' ? 'wasm' : kind,
      icon: IconPuzzle,
      color: 'indigo',
      description: pl.description,
      plugin: true,
      pluginID: pl.id,
    });
  }
  return STEP_CHOICES.map((c) => ({ ...c, items: buckets[c.key] }));
}

export interface StepMatch { choice: StepChoice; item: StepItem }

/** Every item whose name, kind or description matches; an empty query matches nothing, since the choices are the answer then. */
export function searchSteps(choices: StepChoice[], query: string): StepMatch[] {
  if (!query.trim()) return [];
  const out: StepMatch[] = [];
  for (const choice of choices) {
    for (const item of choice.items) {
      if (matchesQuery({ label: item.label, subType: item.subType, type: item.type, description: item.description }, query)) out.push({ choice, item });
    }
  }
  return out;
}
