// Statement templates and keyword lists for the SQL builder.
//
// A template is the statement for the table the operator has open, with a
// {{ }} token per column. It is a starting point to edit, not a query planner:
// what it must never be is a statement the chosen database cannot run.

/** What the statement is for. A lookup or a batch query reads; execute_sql writes. */
export type SqlIntent = 'read' | 'write';

/** A column as `POST /api/{sources,sinks}/discover/columns` describes it. */
export interface SqlColumn {
  name: string;
  type?: string;
  is_pk?: boolean;
  is_identity?: boolean;
  is_nullable?: boolean;
  default?: string;
}

export interface TemplateContext {
  engine?: string;
  table?: string;
  columns?: SqlColumn[];
  /** Paths the editor lists for the incoming message. */
  fields?: string[];
}

export interface SqlTemplate {
  id: string;
  label: string;
  description: string;
  sql: string;
}

const READ_KEYWORDS = [
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'JOIN', 'LEFT JOIN', 'INNER JOIN',
  'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET', 'DISTINCT',
];

const WRITE_KEYWORDS = [
  'INSERT INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE FROM', 'WHERE', 'AND', 'OR',
  'RETURNING', 'ON CONFLICT', 'NULL',
];

export function keywordsFor(intent: SqlIntent): string[] {
  return intent === 'write' ? WRITE_KEYWORDS : READ_KEYWORDS;
}

const PLACEHOLDER_TABLE = 'table_name';
const PLACEHOLDER_COLUMNS: SqlColumn[] = [{ name: 'column_a' }, { name: 'column_b' }];

type Returning = 'clause' | 'output' | 'none';
type Upsert = 'on-conflict' | 'on-duplicate-key' | 'none';

function dialect(engine?: string): { returning: Returning; upsert: Upsert } {
  switch ((engine || '').toLowerCase()) {
    case 'postgres':
    case 'pgvector':
    case 'yugabyte':
    case 'sqlite':
      return { returning: 'clause', upsert: 'on-conflict' };
    case 'mariadb':
      return { returning: 'clause', upsert: 'on-duplicate-key' };
    case 'mysql':
      return { returning: 'none', upsert: 'on-duplicate-key' };
    case 'mssql':
      return { returning: 'output', upsert: 'none' };
    default:
      return { returning: 'none', upsert: 'none' };
  }
}

// The token for a column: the message field of that name as the editor lists
// it. A top-level field wins over a nested one, and `after.code` is the only
// nesting followed -- a CDC row's columns -- so `after.note.amount` does not
// answer for `amount`.
function tokenFor(column: string, fields: string[]): string {
  const path = fields.includes(column)
    ? column
    : fields.includes(`after.${column}`)
      ? `after.${column}`
      : column;
  return `{{.${path}}}`;
}

export function buildTemplates(intent: SqlIntent, ctx: TemplateContext = {}): SqlTemplate[] {
  const table = ctx.table || PLACEHOLDER_TABLE;
  const columns = ctx.columns && ctx.columns.length > 0 ? ctx.columns : PLACEHOLDER_COLUMNS;
  const fields = ctx.fields ?? [];
  const { returning, upsert } = dialect(ctx.engine);

  const token = (c: SqlColumn) => tokenFor(c.name, fields);
  const names = (cols: SqlColumn[]) => cols.map((c) => c.name).join(', ');
  const tokens = (cols: SqlColumn[]) => cols.map(token).join(', ');

  // The key a WHERE or a conflict target names: the declared primary key, else
  // a column called id, else the first one.
  const declared = columns.filter((c) => c.is_pk);
  const keys = declared.length > 0
    ? declared
    : [columns.find((c) => c.name.toLowerCase() === 'id') ?? columns[0]];
  const isKey = (c: SqlColumn) => keys.includes(c);
  const where = keys.map((c) => `${c.name} = ${token(c)}`).join(' AND ');

  if (intent === 'read') {
    return [
      {
        id: 'select',
        label: 'Select by key',
        description: 'The row whose key matches the message.',
        sql: `SELECT ${names(columns)}\nFROM ${table}\nWHERE ${where}`,
      },
    ];
  }

  // A column with a default is left to it unless the message has a value: a
  // token with nothing behind it binds NULL, and an explicit NULL beats the
  // default.
  const hasValue = (c: SqlColumn) => fields.includes(c.name) || fields.includes(`after.${c.name}`);
  const written = (c: SqlColumn) => !c.default || hasValue(c);

  // The database fills an identity column; naming it in an INSERT is an error
  // on PostgreSQL (GENERATED ALWAYS) and SQL Server.
  const insertable = columns.filter((c) => !c.is_identity && written(c));
  const settable = columns.filter((c) => !isKey(c) && !c.is_identity);
  const insertHead = `INSERT INTO ${table} (${names(insertable)})`;
  const insertValues = `VALUES (${tokens(insertable)})`;

  const templates: SqlTemplate[] = [
    {
      id: 'insert',
      label: 'Insert a row',
      description: 'One row per message.',
      sql: `${insertHead}\n${insertValues}`,
    },
  ];

  if (returning === 'clause') {
    templates.push({
      id: 'insert-returning',
      label: 'Insert and return the row',
      description: 'Hands back what was written, generated key included. Name a Returned Rows Field to keep it.',
      sql: `${insertHead}\n${insertValues}\nRETURNING *`,
    });
  } else if (returning === 'output') {
    templates.push({
      id: 'insert-returning',
      label: 'Insert and return the row',
      description: 'Hands back what was written, generated key included. Name a Returned Rows Field to keep it.',
      sql: `${insertHead}\nOUTPUT inserted.*\n${insertValues}`,
    });
  }

  if (settable.length > 0) {
    templates.push({
      id: 'update',
      label: 'Update by key',
      description: 'Changes the row whose key matches the message.',
      sql: `UPDATE ${table}\nSET ${settable.map((c) => `${c.name} = ${token(c)}`).join(', ')}\nWHERE ${where}`,
    });
  }

  // An upsert names its key, so the key is written even when it is an identity
  // column. With nothing left to update on a conflict there is no upsert to
  // offer.
  const upserted = columns.filter((c) => isKey(c) || written(c));
  const updated = settable.filter(written);
  if (updated.length > 0 && upsert !== 'none') {
    const head = `INSERT INTO ${table} (${names(upserted)})\nVALUES (${tokens(upserted)})`;
    templates.push({
      id: 'upsert',
      label: 'Insert or update',
      description: 'Inserts the row, or updates it when the key already exists.',
      sql: upsert === 'on-conflict'
        ? `${head}\nON CONFLICT (${names(keys)}) DO UPDATE\n` +
          `SET ${updated.map((c) => `${c.name} = EXCLUDED.${c.name}`).join(', ')}`
        : `${head}\nON DUPLICATE KEY UPDATE ` +
          updated.map((c) => `${c.name} = VALUES(${c.name})`).join(', '),
    });
  }

  templates.push({
    id: 'delete',
    label: 'Delete by key',
    description: 'Removes the row whose key matches the message.',
    sql: `DELETE FROM ${table}\nWHERE ${where}`,
  });

  return templates;
}
