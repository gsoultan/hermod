import { describe, expect, it } from 'vitest';
import { guideFor } from '../lib/transformationGuide';

// The registry's keys, as of the last audit. If a new transformation type is
// registered without a guide, the fallback keeps the form honest (raw name,
// no invented text) — but this list is the reminder to write one.
const REGISTRY_KEYS = [
  'mapping', 'filter', 'filter_data', 'set', 'aggregate', 'mask', 'pii_masking',
  'mask_emails', 'advanced', 'pipeline', 'validator', 'validate', 'char_map',
  'data_conversion', 'sampling', 'unpivot', 'pivot', 'scd', 'lookup',
  'db_lookup', 'execute_sql', 'fuzzy_lookup', 'term_extraction', 'api_lookup',
  'ai_enrichment', 'ai_mapper', 'condition', 'switch', 'router', 'wait',
  'join', 'foreach', 'fanout', 'collect', 'circuit_breaker', 'approval',
  'stateful', 'log', 'multicast',
];

describe('transformation guide', () => {
  it('has a plain-words entry for every registered type', () => {
    const missing = REGISTRY_KEYS.filter((k) => guideFor(k).what === '');
    expect(missing).toEqual([]);
  });

  it('speaks in sentences, not fragments', () => {
    for (const k of REGISTRY_KEYS) {
      const g = guideFor(k);
      expect(g.what.endsWith('.'), `${k}: "${g.what}"`).toBe(true);
      expect(g.firstStep.endsWith('.'), `${k}: "${g.firstStep}"`).toBe(true);
    }
  });

  it('never shows the raw key as the title for a known type', () => {
    expect(guideFor('filter_data').title).not.toContain('_');
    expect(guideFor('scd').title).not.toBe('scd');
  });

  it('stays honest for unknown types: readable name, no invented description', () => {
    const g = guideFor('future_thing');
    expect(g.title).toBe('future thing');
    expect(g.what).toBe('');
  });
});
