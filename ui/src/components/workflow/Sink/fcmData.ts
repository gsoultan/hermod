import { jsonObjectFromRows, rowsFromJsonObject } from '../Transformation/configs/enrichment/apiLookup/jsonText';

/**
 * Keys that describe a captured change event rather than being one of its
 * columns. The editor's sample of a CDC row holds the row twice -- nested under
 * `after` and hoisted beside it -- and offering `after` would send the whole
 * image as one JSON string, which is the mistake the list exists to avoid.
 *
 * `after`, `before` and `metadata` are never columns. The rest are only
 * envelope keys on a change event: a plain table may well have a `source`.
 */
const ALWAYS_ENVELOPE = new Set(['after', 'before', 'metadata']);
const CDC_ENVELOPE = new Set(['source', 'op', 'ts_ms', 'transaction']);
const IMAGE_PREFIX = /^(?:after|before)\./;

/** A column of the incoming row, as the app data list offers it. */
export interface RowField {
  name: string;
  type: string;
  /** An object or a list: sent as JSON text, since FCM data values are text. */
  composite: boolean;
}

const isComposite = (type?: string) => type === 'object' || type === 'array';

/**
 * Every column of the incoming row, in the order the editor lists them.
 *
 * The list used to stop at thirty, skip object and list columns, and skip a
 * column the editor only knew as `after.<column>` -- one a step upstream of a
 * CDC source adds. All three looked like the form losing fields. Nested paths
 * (`address.city`) are not columns; the object they sit in is.
 */
export function rowFields(availableFields: Array<{ path: string; type?: string }>): RowField[] {
  const cdc = availableFields.some((field) => field.path === 'after' || field.path === 'before');
  const out: RowField[] = [];
  const seen = new Set<string>();
  for (const field of availableFields) {
    const name = field.path.replace(IMAGE_PREFIX, '');
    if (name.includes('.') || ALWAYS_ENVELOPE.has(name) || (cdc && CDC_ENVELOPE.has(name))) continue;
    if (seen.has(name)) continue;
    seen.add(name);
    out.push({ name, type: field.type || 'any', composite: isComposite(field.type) });
  }
  return out;
}

const IDENTIFIER = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * The template that sends one column under its own name, written so the sink
 * can parse it: `{{.first name}}` is not a template, and an object rendered
 * plainly arrives as Go's `map[k:v]`.
 */
export function columnToken(field: Pick<RowField, 'name' | 'composite'>): string {
  const read = IDENTIFIER.test(field.name) ? `.${field.name}` : `index . ${JSON.stringify(field.name)}`;
  if (!field.composite) return `{{${read}}}`;
  return IDENTIFIER.test(field.name) ? `{{json ${read}}}` : `{{json (${read})}}`;
}

const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Whether a stored value reads exactly `column`, however it is spaced or spelled. */
function readsColumn(value: string, column: string): boolean {
  const name = escapeRegExp(column);
  const quoted = escapeRegExp(JSON.stringify(column));
  const read = `(?:\\.(?:(?:after|before)\\.)?${name}|index\\s+\\.\\s+${quoted})`;
  const pattern = new RegExp(`^\\{\\{\\s*(?:${read}|json\\s+${read}|json\\s+\\(\\s*${read}\\s*\\))\\s*\\}\\}$`);
  return pattern.test(value.trim());
}

/** Whether data_json sends `field` under its own name. */
export function sendsColumn(dataJson: string, field: Pick<RowField, 'name'>): boolean {
  const rows = rowsFromJsonObject(dataJson);
  return rows !== null && rows.some((row) => row.key === field.name && readsColumn(row.value, field.name));
}

/**
 * data_json with each of `fields` added under its own name, or taken out. Null
 * when the stored text is JSON the rows cannot represent; the list is then
 * disabled rather than rewriting something the operator typed by hand.
 */
export function setColumns(dataJson: string, fields: Array<Pick<RowField, 'name' | 'composite'>>, on: boolean): string | null {
  const rows = rowsFromJsonObject(dataJson);
  if (rows === null) return null;
  const names = new Set(fields.map((field) => field.name));
  if (!on) {
    // Only a key that reads its column is the list's to take out.
    return jsonObjectFromRows(rows.filter((row) => !(names.has(row.key) && readsColumn(row.value, row.key))));
  }
  const kept = rows.filter((row) => !names.has(row.key) || readsColumn(row.value, row.key));
  const present = new Set(kept.map((row) => row.key));
  const added = fields.filter((field) => !present.has(field.name)).map((field) => ({ key: field.name, value: columnToken(field) }));
  return jsonObjectFromRows([...kept, ...added]);
}

export function toggleColumn(dataJson: string, field: Pick<RowField, 'name' | 'composite'>, on: boolean): string | null {
  return setColumns(dataJson, [field], on);
}

const FIELD_REF = /\{\{\s*\.(?:(?:after|before)\.)?([A-Za-z0-9_]+)|\{\{\s*index\s+\.\s+"([^"]+)"/g;

/**
 * The columns the destination templates read, spelled the ways the sink's own
 * destinationFields reads them: `{{.col}}`, `{{.after.col}}` and
 * `{{index . "col"}}`. The backend withholds these from the data it builds
 * itself; the list shows them switched off, and says why.
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
