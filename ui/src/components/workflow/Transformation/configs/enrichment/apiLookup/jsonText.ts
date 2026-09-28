/**
 * Text-level helpers for the api_lookup request editor.
 *
 * Everything here works on the JSON *text* the node stores, never on a parsed
 * copy written back: api_lookup sends number literals byte for byte
 * (evaluator.ResolveJSONTemplateMsg), so a round trip through JSON.parse would
 * change the request -- a 64-bit id above 2^53 comes back rounded, `1.50` comes
 * back `1.5`.
 */

/** Whether text parses as JSON. The template's own text, before any token is filled in. */
export function isJsonText(text: string): boolean {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

/**
 * Re-indents JSON text without re-encoding any value in it: strings, numbers and
 * literals are copied verbatim and only the whitespace between them changes.
 * Returns null when the text is not JSON.
 */
export function formatJsonText(text: string, indent = '  '): string | null {
  if (!isJsonText(text)) return null;
  let out = '';
  let depth = 0;
  const newline = () => '\n' + indent.repeat(depth);
  for (let i = 0; i < text.length; ) {
    const ch = text[i];
    if (ch === '"') {
      let j = i + 1;
      while (j < text.length && text[j] !== '"') j += text[j] === '\\' ? 2 : 1;
      out += text.slice(i, j + 1);
      i = j + 1;
    } else if (ch === '{' || ch === '[') {
      const close = ch === '{' ? '}' : ']';
      let k = i + 1;
      while (k < text.length && /\s/.test(text[k])) k++;
      if (text[k] === close) {
        out += ch + close;
        i = k + 1;
      } else {
        depth++;
        out += ch + newline();
        i++;
      }
    } else if (ch === '}' || ch === ']') {
      depth--;
      out += newline() + ch;
      i++;
    } else if (ch === ',') {
      out += ',' + newline();
      i++;
    } else if (ch === ':') {
      out += ': ';
      i++;
    } else if (/\s/.test(ch)) {
      i++;
    } else {
      // A number, true, false or null: copied as written.
      let j = i;
      while (j < text.length && !/[\s,:[\]{}"]/.test(text[j])) j++;
      out += text.slice(i, j);
      i = j;
    }
  }
  return out;
}

/** Whether position pos in text is inside a JSON string literal. */
function insideJsonString(text: string, pos: number): boolean {
  let inside = false;
  for (let i = 0; i < pos && i < text.length; i++) {
    if (inside && text[i] === '\\') {
      i++;
    } else if (text[i] === '"') {
      inside = !inside;
    }
  }
  return inside;
}

/** The {{ }} token for a field path, in the spelling every resolver reads. */
export function fieldToken(path: string): string {
  return `{{.${path}}}`;
}

/**
 * Inserts a field's token into a JSON body in place of [start, end), and says
 * where the caret goes. Inside a string the token goes in as it is; anywhere
 * else it is quoted, because an unquoted token is not JSON until it is filled
 * in, and the backend then has to resolve the whole body as plain text.
 */
export function insertBodyToken(
  text: string,
  start: number,
  end: number,
  path: string
): { text: string; caret: number } {
  const token = insideJsonString(text, start) ? fieldToken(path) : `"${fieldToken(path)}"`;
  return { text: text.slice(0, start) + token + text.slice(end), caret: start + token.length };
}

/** One name/value row of a headers or query-params object. */
export interface NameValue {
  key: string;
  value: string;
}

/**
 * The rows a stored headers or queryParams object holds, or null when it cannot
 * be edited as rows without changing what is sent: not a JSON object, or a value
 * that is an object, an array, null, or a number a double cannot hold exactly.
 * An empty string is an object with no rows.
 */
export function rowsFromJsonObject(text: string): NameValue[] | null {
  if (!text.trim()) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
  const rows: NameValue[] = [];
  for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (typeof value === 'string') {
      rows.push({ key, value });
    } else if (typeof value === 'boolean' || (typeof value === 'number' && Number.isSafeInteger(value))) {
      rows.push({ key, value: String(value) });
    } else {
      return null;
    }
  }
  return rows;
}

/**
 * The stored form of rows: a JSON object, one entry per named row -- a row with
 * no name yet is still being typed -- or '' when there is none, which the
 * backend reads as "no headers" or "no query params".
 */
export function jsonObjectFromRows(rows: NameValue[]): string {
  const object: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key) object[key] = row.value;
  }
  return Object.keys(object).length > 0 ? JSON.stringify(object, null, 2) : '';
}

/** How many entries a stored headers or queryParams object holds; 0 when it is not an object. */
export function countJsonObjectEntries(text: string): number {
  if (!text?.trim()) return 0;
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? Object.keys(parsed).length : 0;
  } catch {
    return 0;
  }
}

/**
 * Names given to more than one row. Only the last is sent: the rows become one
 * JSON object. Header names are compared the way HTTP compares them.
 */
export function repeatedNames(rows: NameValue[], caseInsensitive: boolean): string[] {
  const seen = new Map<string, string>();
  const repeated = new Set<string>();
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    const id = caseInsensitive ? key.toLowerCase() : key;
    if (seen.has(id)) repeated.add(seen.get(id)!);
    else seen.set(id, key);
  }
  return [...repeated];
}
