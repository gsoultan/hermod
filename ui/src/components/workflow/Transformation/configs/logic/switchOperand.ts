// A Switch operand is a field or a fixed value, optionally wrapped in one
// function. It is saved in the expression forms the engine already evaluates,
// so a saved workflow keeps its shape and the backend needs no new keys:
//
//   switch field        status              lower(source.status)
//   case value, fixed   active              {{upper("active")}}
//   case value, field   {{limit}}           {{lower(source.region)}}
//
// A case value is resolved by EvaluateConditions (pkg/infra/evaluator), which
// expands {{ }} tokens; inside a call an argument is a field only when written
// source.<path>, and a quoted argument is text.

export type OperandSource = 'value' | 'field';

export interface Operand {
  fn: string;
  source: OperandSource;
  text: string;
}

/** One-argument functions offered in the picker; all run in both the engine and the preview. */
export const OPERAND_FUNCTIONS = [
  { value: 'lower', label: 'lower', group: 'Text' },
  { value: 'upper', label: 'upper', group: 'Text' },
  { value: 'trim', label: 'trim', group: 'Text' },
  { value: 'normalize_space', label: 'normalize_space', group: 'Text' },
  { value: 'len', label: 'len', group: 'Text' },
  { value: 'abs', label: 'abs', group: 'Number' },
  { value: 'round', label: 'round', group: 'Number' },
  { value: 'floor', label: 'floor', group: 'Number' },
  { value: 'ceil', label: 'ceil', group: 'Number' },
] as const;

const FUNCTION_NAMES = new Set<string>(OPERAND_FUNCTIONS.map((f) => f.value));

const TOKEN = /^\{\{\s*(.*?)\s*\}\}$/s;
const CALL = /^([A-Za-z_][A-Za-z0-9_]*)\(\s*(.*?)\s*\)$/s;
const PATH = /^\.?[^\s{}()"',]+$/;

const quote = (text: string) => (text.includes('"') ? `'${text}'` : `"${text}"`);

/** The function call `fn(arg)` read into an operand, or null when it is anything else. */
function parseCall(expr: string): Operand | null {
  const call = CALL.exec(expr);
  if (!call || !FUNCTION_NAMES.has(call[1])) return null;
  const [, fn, arg] = call;
  if (arg.startsWith('source.') && PATH.test(arg)) return { fn, source: 'field', text: arg.slice(7) };
  const quoted = /^"([^"]*)"$|^'([^']*)'$/.exec(arg);
  if (quoted) return { fn, source: 'value', text: quoted[1] ?? quoted[2] };
  if (arg !== '' && !Number.isNaN(Number(arg))) return { fn, source: 'value', text: arg };
  return null;
}

/**
 * A saved case value read as an operand. Anything the picker cannot show —
 * a nested call, several tokens, a function not in the list — reads as a
 * fixed value holding the text as saved, which is what it always was.
 */
export function parseCaseValue(value: unknown): Operand {
  if (value === undefined || value === null) return { fn: '', source: 'value', text: '' };
  const raw = String(value);
  const token = TOKEN.exec(raw);
  if (token && !token[1].includes('{{')) {
    const inner = token[1];
    if (PATH.test(inner)) return { fn: '', source: 'field', text: inner.replace(/^\./, '') };
    const call = parseCall(inner);
    if (call) return call;
  }
  return { fn: '', source: 'value', text: raw };
}

/** An operand written back as a case value; an operand with no field picked yet saves as empty. */
export function formatCaseValue(op: Operand): string {
  if (op.source === 'field') {
    if (!op.text) return '';
    return op.fn ? `{{${op.fn}(source.${op.text})}}` : `{{${op.text}}}`;
  }
  return op.fn ? `{{${op.fn}(${quote(op.text)})}}` : op.text;
}

/**
 * The switch field read as a function and a field. A saved expression the
 * picker cannot show comes back with no function and the text as saved.
 */
export function parseSwitchField(value: unknown): Operand {
  const raw = typeof value === 'string' ? value : '';
  const call = parseCall(raw.trim());
  if (call && call.source === 'field') return call;
  return { fn: '', source: 'field', text: raw };
}

export function formatSwitchField(op: Operand): string {
  if (!op.fn || !op.text) return op.text;
  return `${op.fn}(source.${op.text.replace(/^source\./, '')})`;
}

/** Whether a function can be applied to this text; a hand-written expression cannot take one. */
export const acceptsFunction = (text: string) => !/[(){}]/.test(text);
