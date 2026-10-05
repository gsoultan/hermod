// Text helpers for the SQL builder. Pure, so the editor and its panels can be
// tested without rendering either.

// Keywords that should start on a new line when formatting a query, making
// long statements far easier to read.
const NEWLINE_KEYWORDS = [
  'FROM', 'WHERE', 'AND', 'OR', 'LEFT JOIN', 'RIGHT JOIN', 'INNER JOIN',
  'OUTER JOIN', 'JOIN', 'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET',
  'UNION', 'VALUES', 'SET', 'RETURNING', 'ON CONFLICT', 'ON DUPLICATE KEY UPDATE',
];

// Upper-cased where they stand, without a line break.
const INLINE_KEYWORDS = ['DO UPDATE', 'DO NOTHING'];

// What a statement opens with. `FROM` in DELETE FROM is part of the opener, not
// a clause to break before.
const LEADING_KEYWORDS = ['SELECT', 'INSERT INTO', 'UPDATE', 'DELETE FROM', 'WITH'];

// Stands in for a lifted-out literal while the rest is formatted. A private-use
// character: nothing a statement contains, and not whitespace.
const MARK = '\uE000';

const keywordPattern = (kw: string) => kw.replace(/ /g, '\\s+');

// formatSQL applies a lightweight, dependency-free formatting pass: it
// upper-cases well known keywords and breaks long statements onto multiple
// lines so they are easier to scan.
//
// String literals and {{ }} tokens are lifted out first and put back untouched.
// Everything below rewrites whatever matches a keyword, and inside a literal
// that is the data: 'salt and pepper' used to be written to the database with a
// line break in it.
export function formatSQL(sql: string): string {
  if (!sql.trim()) return sql;

  const kept: string[] = [];
  const keep = (text: string) => `${MARK}${kept.push(text) - 1}${MARK}`;
  let result = sql.replace(/'(?:[^']|'')*'|\{\{[^}]*\}\}/g, keep);

  result = result.replace(/\s+/g, ' ').trim();

  for (const kw of LEADING_KEYWORDS) {
    const re = new RegExp(`^${keywordPattern(kw)}\\b`, 'i');
    if (re.test(result)) {
      // Kept out of reach of the clause pass, which would break DELETE FROM in
      // two.
      result = result.replace(re, keep(kw));
      break;
    }
  }
  // Break major clauses onto their own line (longest keywords first to avoid
  // partially matching shorter ones).
  for (const kw of [...NEWLINE_KEYWORDS].sort((a, b) => b.length - a.length)) {
    const re = new RegExp(`\\s+${keywordPattern(kw)}\\b`, 'gi');
    result = result.replace(re, `\n${keep(kw)}`);
  }
  for (const kw of INLINE_KEYWORDS) {
    result = result.replace(new RegExp(`\\b${keywordPattern(kw)}\\b`, 'gi'), kw);
  }

  return result.replace(new RegExp(`${MARK}(\\d+)${MARK}`, 'g'), (_, i) => kept[Number(i)]).trim();
}

// The {{ }} tokens a statement binds, without the leading dot, in the order
// they first appear.
export function extractVariables(sql: string): string[] {
  const vars = new Set<string>();
  for (const match of sql.matchAll(/\{\{\s*(\.?[\w.]+)\s*\}\}/g)) {
    vars.add(match[1].startsWith('.') ? match[1].slice(1) : match[1]);
  }
  return Array.from(vars);
}

/**
 * Maps JS types to standard SQL types based on the database engine.
 */
export function jsToSqlType(jsType: string, engine?: string): string {
  const e = (engine || '').toLowerCase();
  const isPostgres = e === 'postgres' || e === 'pgvector' || e === 'yugabyte';
  const isMySQL = e === 'mysql' || e === 'mariadb';
  const isSQLite = e === 'sqlite';
  const isOracle = e === 'oracle';
  const isMSSQL = e === 'mssql';

  switch (jsType) {
    case 'number':
      return 'DECIMAL';
    case 'boolean':
      return isOracle ? 'NUMBER(1)' : 'BOOLEAN';
    case 'object':
    case 'array':
      if (isPostgres) return 'JSONB';
      if (isMySQL) return 'JSON';
      return 'TEXT';
    case 'string':
    default:
      if (isPostgres || isSQLite) return 'TEXT';
      if (isMSSQL) return 'NVARCHAR(MAX)';
      return 'VARCHAR(255)';
  }
}
