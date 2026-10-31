const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);

/**
 * The record a step's live sample carries. The engine samples whole messages:
 * the row under `after` (or `before`, for a delete), with the message's id,
 * operation, table and metadata around it. A sample that is not shaped like a
 * message is returned as it is; of several samples, the newest is used.
 */
export function latestRecord(sample: unknown): unknown {
  const latest = Array.isArray(sample) ? sample[sample.length - 1] : sample;
  if (!isObject(latest)) return latest;
  if (isObject(latest.after)) return latest.after;
  if (isObject(latest.before)) return latest.before;
  return latest;
}
