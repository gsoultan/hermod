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

  // Two different nodes answer to "foreach": the `foreach` node type splits the
  // message into one per item, while a `transformation` with transType
  // foreach/fanout keeps one message and materialises the expanded array on it.
  // `transType` cannot tell them apart -- TransformationForm computes it as
  // `data.transType || node.type`, so both come out "foreach" -- which is why
  // the badge over the editor described the same thing for both. The node's own
  // type is the discriminator, the same one ForeachConfig already takes.
  describe('the two foreaches', () => {
    it('does not describe them identically', () => {
      const node = guideFor('foreach', 'foreach');
      const transformation = guideFor('foreach', 'transformation');

      expect(node.title).not.toBe(transformation.title);
      expect(node.what).not.toBe(transformation.what);
    });

    it('says the fan-out node emits one record per item', () => {
      expect(guideFor('foreach', 'foreach').what).toMatch(/one record per item/i);
    });

    it('never claims the transformation emits one record per item', () => {
      // `fanout` is a live transType: the Go transformer registers it as an
      // alias, TRANSFORM_CONFIGS keys on it, and an imported bundle can carry
      // it. Its guide entry claimed a fan-out the transformer does not do.
      for (const key of ['foreach', 'fanout']) {
        const g = guideFor(key, 'transformation');
        expect(g.what, `${key}: "${g.what}"`).not.toMatch(/one record per item/i);
        expect(g.what, `${key}: "${g.what}"`).toMatch(/same record/i);
      }
    });

    it('falls back to the transformation reading when no node type is given', () => {
      // guideFor is called from surfaces that have no node in hand. The safe
      // default is the one that does not promise a fan-out.
      expect(guideFor('foreach').what).toBe(guideFor('foreach', 'transformation').what);
    });
  });
});
