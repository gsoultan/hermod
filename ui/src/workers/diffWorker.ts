/// <reference lib="webworker" />
// A tiny web worker to compute deep-ish diffs between two JSON-like objects.
// Keep implementation aligned with PreviewPanel's previous getDiff behavior.

export type DiffRequest = {
  id: number;
  original: unknown;
  transformed: unknown;
};

export type DiffResponse = {
  id: number;
  result: unknown;
};

// eslint-disable-next-line no-restricted-globals
self.onmessage = (e: MessageEvent<DiffRequest>) => {
  const { id, original, transformed } = e.data;

  try {
    const result = getDiff(original as any, transformed as any);
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, result } as DiffResponse);
  } catch (err) {
    // Fallback: just return the transformed value on any error
    // eslint-disable-next-line no-restricted-globals
    (self as unknown as Worker).postMessage({ id, result: transformed } as DiffResponse);
  }
};

function getDiff(orig: any, trans: any): any {
  if (orig === trans) return {};
  if (!orig || typeof orig !== 'object' || !trans || typeof trans !== 'object') return trans;

  const diff: any = {};
  const transKeys = Object.keys(trans);

  for (const key of transKeys) {
    const a = orig[key];
    const b = trans[key];
    if (JSON.stringify(a) !== JSON.stringify(b)) {
      if (
        b && typeof b === 'object' && !Array.isArray(b) &&
        a && typeof a === 'object' && !Array.isArray(a)
      ) {
        const nested = getDiff(a, b);
        if (Object.keys(nested).length > 0) diff[key] = nested;
        else if (JSON.stringify(a) !== JSON.stringify(b)) diff[key] = b;
      } else {
        diff[key] = b;
      }
    }
  }
  return diff;
}
