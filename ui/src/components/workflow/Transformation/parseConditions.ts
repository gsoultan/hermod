import type { Condition } from './FilterEditor';

/**
 * Reads a node's `conditions` into the array FilterEditor renders.
 *
 * The editor saves an array, but a workflow created through the API may carry
 * the list as a JSON string, and the engine accepts both
 * (evaluator.ParseObjectList). Anything that is not a list, including JSON
 * that does not parse, reads as no conditions rather than reaching `.map`.
 */
export function parseConditions(raw: unknown): Condition[] {
  let value = raw;
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value || '[]');
    } catch {
      return [];
    }
  }
  return Array.isArray(value) ? value : [];
}
