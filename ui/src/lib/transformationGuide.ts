/**
 * One plain sentence per transformation type: what the node does, and what to
 * do first.
 *
 * The form's header used to show only the raw type key in a badge —
 * FILTER_DATA, SCD, CHAR_MAP — and the explanation lived behind a help icon.
 * A user who does not already know the vocabulary had to open a modal to
 * learn what the node they just added even does. This map puts the answer
 * where the eyes already are, in words that assume nothing.
 */

export interface TransformationGuide {
  /** Human name for the badge, instead of the raw key. */
  title: string;
  /** What the node does — one sentence, no jargon. */
  what: string;
  /** The first thing to do — imperative, one step. */
  firstStep: string;
}

const GUIDES: Record<string, TransformationGuide> = {
  mapping: {
    title: 'Rename field',
    what: 'Renames one field on every record.',
    firstStep: 'Click a field on the left to pick it, then type its new name.',
  },
  set: {
    title: 'Set fields',
    what: 'Adds fields to every record, or overwrites existing ones.',
    firstStep: 'Click a field on the left to copy it in, or add a field and type a value.',
  },
  advanced: {
    title: 'Formulas',
    what: 'Builds new fields with formulas — combine, reformat, or compute values.',
    firstStep: 'Add a field, then pick a function from the library on the left.',
  },
  filter: {
    title: 'Filter',
    what: 'Keeps only the records that match your conditions; the rest are dropped.',
    firstStep: 'Add a condition — records must match it to continue.',
  },
  filter_data: {
    title: 'Filter',
    what: 'Keeps only the records that match your conditions; the rest are dropped.',
    firstStep: 'Add a condition — records must match it to continue.',
  },
  condition: {
    title: 'Branch',
    what: 'Sends each record down the true or the false path, based on your conditions.',
    firstStep: 'Add a condition; connect both outputs in the editor.',
  },
  switch: {
    title: 'Switch',
    what: 'Routes each record to one of several paths, based on a field value.',
    firstStep: 'Pick the field to switch on, then add one case per path.',
  },
  router: {
    title: 'Router',
    what: 'Routes each record to whichever paths match its rules.',
    firstStep: 'Add a route and its condition.',
  },
  mask: {
    title: 'Mask data',
    what: 'Hides sensitive values — emails, names, card numbers — before they travel on.',
    firstStep: 'Pick the field to mask and how to mask it.',
  },
  pii_masking: {
    title: 'Mask PII',
    what: 'Hides personal data in every field it recognises.',
    firstStep: 'Nothing required — it works as added. Preview to see the effect.',
  },
  mask_emails: {
    title: 'Mask emails',
    what: 'Hides email addresses.',
    firstStep: 'Nothing required — it works as added. Preview to see the effect.',
  },
  validator: {
    title: 'Validate',
    what: 'Checks each record against rules; failures can be dropped or routed.',
    firstStep: 'Add a rule for a field.',
  },
  validate: {
    title: 'Validate',
    what: 'Checks each record against rules; failures can be dropped or routed.',
    firstStep: 'Add a rule for a field.',
  },
  aggregate: {
    title: 'Aggregate',
    what: 'Counts, sums or averages records over a time window.',
    firstStep: 'Pick the operation and the field to aggregate.',
  },
  stateful: {
    title: 'Aggregate',
    what: 'Counts, sums or averages records over a time window.',
    firstStep: 'Pick the operation and the field to aggregate.',
  },
  // Both keys below are the *transformation*, which keeps one record. The
  // node that actually emits one record per item is in NODE_TYPE_GUIDES.
  // `fanout` is a live alias -- the Go transformer registers it, TRANSFORM_CONFIGS
  // keys on it, and an imported bundle can carry it -- and it used to claim a
  // fan-out this transformer does not do.
  foreach: {
    title: 'Expand list',
    what: 'Expands a list onto the same record, which stays a single record; downstream nodes still run once.',
    firstStep: 'Point Array Path at the list field; the expanded list lands under Result Field.',
  },
  fanout: {
    title: 'Expand list',
    what: 'Expands a list onto the same record, which stays a single record; downstream nodes still run once.',
    firstStep: 'Point Array Path at the list field; the expanded list lands under Result Field.',
  },
  lua: {
    title: 'Lua script',
    what: 'Runs your Lua code on every record.',
    firstStep: 'Edit the transform(msg) function; the example shows the shape.',
  },
  wasm: {
    title: 'WebAssembly',
    what: 'Runs a compiled WebAssembly module on every record.',
    firstStep: 'Provide the module and the function name to call.',
  },
  db_lookup: {
    title: 'Database lookup',
    what: 'Enriches each record with data looked up from a database.',
    firstStep: 'Pick the source to look up in, then map the key field.',
  },
  lookup: {
    title: 'Lookup',
    what: 'Enriches each record with a value looked up from reference data.',
    firstStep: 'Pick where to look and which field is the key.',
  },
  api_lookup: {
    title: 'API lookup',
    what: 'Enriches each record with data fetched from an HTTP API.',
    firstStep: 'Enter the URL; use {field} to insert record values.',
  },
  execute_sql: {
    title: 'Run SQL',
    what: 'Runs a SQL statement for each record.',
    firstStep: 'Pick the database and write the statement.',
  },
  char_map: {
    title: 'Character cleanup',
    what: 'Replaces or strips characters in a text field.',
    firstStep: 'Pick the field and the replacement rules.',
  },
  data_conversion: {
    title: 'Convert types',
    what: 'Changes field types — text to number, number to date, text to a list and back, an object to JSON for a jsonb column.',
    firstStep: 'Add a row per field, each with the type it should become.',
  },
  sampling: {
    title: 'Sample',
    what: 'Lets only a percentage of records through.',
    firstStep: 'Set the percentage to keep.',
  },
  pivot: {
    title: 'Pivot',
    what: 'Turns rows into columns.',
    firstStep: 'Pick the key field and the value field.',
  },
  unpivot: {
    title: 'Unpivot',
    what: 'Turns columns into rows.',
    firstStep: 'Pick the columns to unpivot.',
  },
  scd: {
    title: 'History tracking (SCD)',
    what: 'Tracks how records change over time, keeping old versions.',
    firstStep: 'Pick the key field that identifies a record.',
  },
  join: {
    title: 'Join fields',
    what: 'Combines several fields into one.',
    firstStep: 'Pick the fields and the separator.',
  },
  collect: {
    title: 'Collect',
    what: 'Gathers records into batches before passing them on.',
    firstStep: 'Set the batch size or time window.',
  },
  wait: {
    title: 'Wait',
    what: 'Holds each record for a fixed time before passing it on.',
    firstStep: 'Set how long to wait.',
  },
  circuit_breaker: {
    title: 'Circuit breaker',
    what: 'Stops sending records downstream after repeated failures, then retries later.',
    firstStep: 'Set the failure threshold.',
  },
  approval: {
    title: 'Approval',
    what: 'Pauses records until a person approves them.',
    firstStep: 'Choose who can approve.',
  },
  log: {
    title: 'Log',
    what: 'Writes each record to the workflow log, unchanged.',
    firstStep: 'Nothing required — add it where you want visibility.',
  },
  multicast: {
    title: 'Multicast',
    what: 'Sends a copy of each record down every connected path.',
    firstStep: 'Connect the outputs in the editor.',
  },
  fuzzy_lookup: {
    title: 'Fuzzy lookup',
    what: 'Enriches records by matching text approximately, not exactly.',
    firstStep: 'Pick the field to match and the reference data.',
  },
  term_extraction: {
    title: 'Extract terms',
    what: 'Pulls key words and phrases out of a text field.',
    firstStep: 'Pick the text field to analyse.',
  },
  ai_enrichment: {
    title: 'AI enrich',
    what: 'Asks an AI model to add information to each record.',
    firstStep: 'Describe what to add, in plain words.',
  },
  ai_mapper: {
    title: 'AI mapper',
    what: 'Asks an AI model to map your fields onto the target schema.',
    firstStep: 'Review the suggested mapping before saving.',
  },
  pipeline: {
    title: 'Pipeline',
    what: 'Runs several transformations in order, as one node.',
    firstStep: 'Add the first step.',
  },
};

/**
 * Guides keyed by the node's own `type`, checked before `transType`.
 *
 * Two different nodes answer to "foreach" and `transType` cannot tell them
 * apart: TransformationForm computes it as `data.transType || node.type`, so
 * the `foreach` node type and a `transformation` with transType foreach both
 * arrive here as "foreach". They are not the same thing -- one splits the
 * message, the other expands an array onto it -- and describing both with the
 * GUIDES.foreach entry put the wrong sentence over half of them.
 *
 * Node type wins over transType, matching `resolveConfigComponent`'s ordering
 * and the `nodeType` prop ForeachConfig already takes for the same reason.
 */
const NODE_TYPE_GUIDES: Record<string, TransformationGuide> = {
  foreach: {
    title: 'Fan out',
    what: 'Splits the record into one record per item in the list, and everything downstream runs again for each one.',
    firstStep: 'Point Array Path at the list field; each record gets `_item` and `_index`.',
  },
};

/**
 * The guide for a type, with a fallback that stays honest for types this map
 * does not know yet: the raw name, no invented description.
 *
 * `nodeType` is optional because several callers have only a transType in hand.
 * Omitting it reads a shared key as the transformation, which is the safe
 * default: it is the one that does not promise a fan-out.
 */
export function guideFor(transType: string, nodeType?: string): TransformationGuide {
  if (nodeType && NODE_TYPE_GUIDES[nodeType]) return NODE_TYPE_GUIDES[nodeType];
  return (
    GUIDES[transType] ?? {
      title: transType ? transType.replace(/_/g, ' ') : 'Transformation',
      what: '',
      firstStep: '',
    }
  );
}
