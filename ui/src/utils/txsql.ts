// Transactional SQL sink editor logic.
//
// The sink stores its statements as JSON text (SQL mode) or its steps as JSON
// text (Guided mode). The checks here mirror the ones the server makes when the
// sink is saved and when its workflow starts (pkg/comm/sink/txsql), so a
// problem shows beside the statement that has it instead of as a refusal on
// save. The server stays the authority: it compiles Guided steps and refuses
// anything these checks miss.

export type TxStatement = {
  name: string;
  sql: string;
  forEach?: string;
  as?: string;
  expect?: '' | 'one' | 'affected' | 'any';
};

export type TxColumnValue = { column: string; value: string };

export type TxAction = 'insert' | 'upsert' | 'update' | 'delete';

export type TxStep = {
  name: string;
  table: string;
  action: TxAction;
  match: string[];
  columns: TxColumnValue[];
  forEach?: string;
  as?: string;
  returning: string[];
};

export const DRIVERS = [
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'yugabyte', label: 'YugabyteDB' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'mariadb', label: 'MariaDB' },
  { value: 'mssql', label: 'SQL Server' },
  { value: 'sqlite', label: 'SQLite' },
  { value: 'oracle', label: 'Oracle' },
];

/** The isolation levels a driver has; Oracle has two. */
export function isolationLevels(driver: string | undefined): { value: string; label: string }[] {
  const all = [
    { value: '', label: "The database's default" },
    { value: 'read_committed', label: 'Read committed' },
    { value: 'repeatable_read', label: 'Repeatable read' },
    { value: 'serializable', label: 'Serializable' },
  ];
  return driver === 'oracle' ? all.filter((l) => l.value !== 'repeatable_read') : all;
}

const NAME_RE = /^[a-z_][a-z0-9_]*$/;
const DDL = new Set(['CREATE', 'ALTER', 'DROP', 'TRUNCATE', 'RENAME', 'COMMENT', 'GRANT', 'REVOKE']);
const TOKEN_RE = /\{\{\s*([^{}]+?)\s*\}\}/g;
const ONE_TOKEN_RE = /^\{\{\s*[^{}]+?\s*\}\}$/;

function parseList<T>(raw: unknown): T[] {
  if (typeof raw !== 'string' || raw.trim() === '') return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? (v as T[]) : [];
  } catch {
    return [];
  }
}

export function parseStatements(raw: unknown): TxStatement[] {
  return parseList<TxStatement>(raw).map((s) => ({
    name: s.name ?? '',
    sql: s.sql ?? '',
    forEach: s.forEach ?? '',
    as: s.as ?? '',
    expect: (s.expect ?? '') as TxStatement['expect'],
  }));
}

/** The stored form: empty optional keys are left out, as the server writes them. */
export function formatStatements(list: TxStatement[]): string {
  return JSON.stringify(
    list.map((s) => {
      const out: Record<string, string> = { name: s.name.trim(), sql: s.sql };
      if (s.forEach?.trim()) out.forEach = s.forEach.trim();
      if (s.as?.trim()) out.as = s.as.trim();
      if (s.expect) out.expect = s.expect;
      return out;
    }),
  );
}

export function parseSteps(raw: unknown): TxStep[] {
  return parseList<TxStep>(raw).map((s) => ({
    name: s.name ?? '',
    table: s.table ?? '',
    action: (s.action ?? 'upsert') as TxAction,
    match: s.match ?? [],
    columns: (s.columns ?? []).map((c) => ({ column: c.column ?? '', value: c.value ?? '' })),
    forEach: s.forEach ?? '',
    as: s.as ?? '',
    returning: s.returning ?? [],
  }));
}

export function formatSteps(steps: TxStep[]): string {
  return JSON.stringify(
    steps.map((s) => {
      const out: Record<string, unknown> = {
        name: s.name.trim(),
        table: s.table.trim(),
        action: s.action,
        match: s.match,
        columns: s.columns,
        returning: s.returning,
      };
      if (s.forEach?.trim()) out.forEach = s.forEach.trim();
      if (s.as?.trim()) out.as = s.as.trim();
      return out;
    }),
  );
}

export const newStatement = (i: number): TxStatement => ({ name: `step${i + 1}`, sql: '', forEach: '', as: '', expect: '' });

export const newStep = (i: number): TxStep => ({
  name: `step${i + 1}`,
  table: '',
  action: 'upsert',
  match: [],
  columns: [],
  forEach: '',
  as: '',
  returning: [],
});

/** Blank out string literals, quoted names and {{tokens}}, so keyword checks see only SQL. */
function code(sql: string): string {
  return sql
    .replace(/\{\{[^{}]*\}\}/g, ' ')
    .replace(/'(?:[^']|'')*'/g, "''")
    .replace(/"(?:[^"]|"")*"/g, '""')
    .replace(/`[^`]*`/g, '``')
    .replace(/\[[^\]]*\]/g, '[]')
    .replace(/--[^\n]*/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ');
}

function firstKeyword(sql: string): string {
  const m = code(sql).trim().match(/^[A-Za-z]+/);
  return m ? m[0].toUpperCase() : '';
}

function tokenHeads(sql: string): string[] {
  const heads: string[] = [];
  for (const m of sql.matchAll(TOKEN_RE)) {
    heads.push(m[1].trim().replace(/^\./, '').split('.')[0]);
  }
  return heads;
}

/** One message per statement that the server would refuse, or undefined. */
export function statementProblems(list: TxStatement[]): (string | undefined)[] {
  const index = new Map<string, number>();
  const names = list.map((s, i) => s.name.trim() || `step${i + 1}`);
  const problems: (string | undefined)[] = list.map((s, i) => {
    const name = names[i];
    if (!NAME_RE.test(name)) return `The name "${name}" may use only lower-case letters, digits and _, and cannot start with a digit.`;
    if (index.has(name)) return `The name "${name}" is used more than once.`;
    index.set(name, i);
    if (!s.sql.trim()) return 'This statement has no SQL.';
    const kw = firstKeyword(s.sql);
    if (DDL.has(kw)) return `${kw} is a schema change; schema changes do not belong in a per-message transaction.`;
    const c = code(s.sql);
    const semi = c.indexOf(';');
    if (semi >= 0 && c.slice(semi).replace(/;/g, '').trim() !== '') {
      return 'This holds more than one statement; give each its own entry.';
    }
    const alias = s.forEach?.trim() ? s.as?.trim() || 'item' : '';
    if (alias && !NAME_RE.test(alias)) return `The item name "${alias}" may use only lower-case letters, digits and _.`;
    return undefined;
  });
  list.forEach((s, i) => {
    if (problems[i]) return;
    const alias = s.forEach?.trim() ? s.as?.trim() || 'item' : '';
    if (alias && index.has(alias)) {
      problems[i] = `The item name "${alias}" is also a statement name; pick another.`;
      return;
    }
    for (const head of tokenHeads(s.sql)) {
      if (head === alias) continue;
      const j = index.get(head);
      if (j === undefined) continue;
      if (j === i) {
        problems[i] = `This uses {{${head}...}}, its own result; a statement can only use what ran before it.`;
      } else if (j > i) {
        problems[i] = `This uses {{${head}...}}, which runs later; move it below "${head}".`;
      } else if (list[j].forEach?.trim()) {
        problems[i] = `This uses {{${head}...}}, but "${head}" runs once per item, so it has no single result to use.`;
      }
      if (problems[i]) return;
    }
  });
  return problems;
}

/** Things that will run but probably not as meant. */
export function statementWarnings(s: TxStatement): string[] {
  const c = code(s.sql).toUpperCase().replace(/\s+/g, ' ');
  const warnings: string[] = [];
  if (/\bDO NOTHING\b/.test(c) && /\bRETURNING\b/.test(c)) {
    warnings.push(
      'ON CONFLICT DO NOTHING returns no row when the row already exists, so later statements would get no id. Use DO UPDATE instead.',
    );
  }
  if (firstKeyword(s.sql) === 'INSERT' && !/\bON CONFLICT\b|\bON DUPLICATE KEY\b/.test(c)) {
    warnings.push('A redelivered message will insert this row again. An upsert on a natural key makes it safe to repeat.');
  }
  return warnings;
}

/** The columns a statement's RETURNING or OUTPUT list names. */
export function returnedColumns(sql: string): string[] {
  const m = sql.match(/\bRETURNING\s+([\s\S]+?)(?:\bINTO\b|;|$)/i) || sql.match(/\bOUTPUT\s+([\s\S]+?)(?:\bVALUES\b|\bWHERE\b|\bINTO\b|\bSELECT\b|;|$)/i);
  if (!m) return [];
  return m[1]
    .split(',')
    .map((part) => {
      const p = part.trim().split(/\s+AS\s+/i).pop()!.trim();
      return p.replace(/^(inserted|deleted)\./i, '').replace(/^[`"[]|[`"\]]$/g, '');
    })
    .filter((p) => p && p !== '*');
}

/** The {{name.column}} references statement `at` can use. */
export function referenceHints(list: TxStatement[], at: number, driver: string): string[] {
  const hints: string[] = [];
  list.slice(0, at).forEach((s, i) => {
    if (s.forEach?.trim()) return;
    const name = s.name.trim() || `step${i + 1}`;
    let cols = returnedColumns(s.sql);
    if (cols.length === 0 && (driver === 'mysql' || driver === 'mariadb') && firstKeyword(s.sql) === 'INSERT') cols = ['id'];
    // Oracle names columns in capitals; the sink lower-cases them.
    if (driver === 'oracle') cols = cols.map((c) => c.toLowerCase());
    for (const c of cols) hints.push(`{{${name}.${c}}}`);
  });
  return hints;
}

/** One message per Guided step that cannot be compiled yet, or undefined. */
export function stepProblems(steps: TxStep[]): (string | undefined)[] {
  return steps.map((s) => {
    if (!s.table.trim()) return 'Pick a table.';
    if (s.action !== 'delete' && s.columns.filter((c) => c.column).length === 0) return 'Set at least one column.';
    const bad = s.columns.find((c) => c.column && !ONE_TOKEN_RE.test(c.value.trim()));
    if (bad) return `The value for ${bad.column} must be one field, such as {{customer}}.`;
    if (s.action !== 'insert' && s.match.length === 0) {
      return s.action === 'upsert'
        ? 'Choose the key to match on, so a repeated message updates the row instead of adding another.'
        : `Choose the key to match on, or this ${s.action} would touch every row.`;
    }
    const missing = s.match.find((m) => !s.columns.some((c) => c.column === m));
    if (missing) return `The match column ${missing} needs a value under Columns.`;
    return undefined;
  });
}

/** The database sink type that can list this driver's tables and columns. */
export function discoveryType(driver: string | undefined): string | undefined {
  if (!driver) return undefined;
  return driver === 'sqlserver' ? 'mssql' : driver;
}
