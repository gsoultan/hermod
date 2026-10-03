import { fieldToken, jsonObjectFromRows, rowsFromJsonObject } from '../Transformation/configs/enrichment/apiLookup/jsonText';

/**
 * Keys that describe a captured change event rather than being one of its
 * columns. The editor's sample of a CDC row holds the row twice -- nested under
 * `after` and hoisted beside it -- and a chip for `after` would send the whole
 * image as one JSON string, which is the mistake the chips exist to avoid.
 */
const ENVELOPE_KEYS = new Set(['after', 'before', 'metadata', 'source', 'op', 'ts_ms', 'transaction']);

/** More chips than this is a wall; the per-row field picker reaches the rest. */
const MAX_CHIPS = 30;

/**
 * The incoming row's own columns, as the chips that add one to the data in a
 * click: top-level scalars only. A nested object is still reachable through the
 * field picker on a row, under the name the operator chooses for it.
 */
export function rowColumns(availableFields: Array<{ path: string; type?: string }>): string[] {
  const out: string[] = [];
  for (const field of availableFields) {
    if (field.path.includes('.') || ENVELOPE_KEYS.has(field.path)) continue;
    if (field.type === 'object' || field.type === 'array') continue;
    if (!out.includes(field.path)) out.push(field.path);
    if (out.length === MAX_CHIPS) break;
  }
  return out;
}

/** Whether data_json sends `column` under its own name, as the chip would add it. */
export function sendsColumn(dataJson: string, column: string): boolean {
  const rows = rowsFromJsonObject(dataJson);
  return rows !== null && rows.some((row) => row.key === column && row.value === fieldToken(column));
}

/**
 * data_json with `column` added under its own name, or taken out. Null when the
 * stored text is JSON the rows cannot represent; the chips are then disabled
 * rather than rewriting something the operator typed by hand.
 */
export function toggleColumn(dataJson: string, column: string, on: boolean): string | null {
  const rows = rowsFromJsonObject(dataJson);
  if (rows === null) return null;
  const rest = rows.filter((row) => row.key !== column);
  return jsonObjectFromRows(on ? [...rest, { key: column, value: fieldToken(column) }] : rest);
}

const FIELD_REF = /\{\{\s*\.(?:(?:after|before)\.)?([A-Za-z0-9_]+)|\{\{\s*index\s+\.\s+"([^"]+)"/g;

/**
 * The columns the destination templates read, spelled the ways the sink's own
 * destinationFields reads them: `{{.col}}`, `{{.after.col}}` and
 * `{{index . "col"}}`. The backend withholds these from the data it builds
 * itself; the chips leave them out for the same reason.
 */
export function destinationColumns(templates: string[]): string[] {
  const out: string[] = [];
  for (const template of templates) {
    for (const match of template.matchAll(FIELD_REF)) {
      const column = match[1] ?? match[2];
      if (column && !out.includes(column)) out.push(column);
    }
  }
  return out;
}
