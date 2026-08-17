/**
 * Parse a pasted database connection URL into Hermod's config fields.
 *
 * The fastest way to configure a database connector is to paste the URL the
 * user already has — from their provider's dashboard, a teammate, an env file —
 * and let the form fill itself. Every field stays editable afterwards; the
 * paste is a shortcut, not a lock-in.
 *
 * Returns only the keys the URL actually carried, so merging over an existing
 * config never blanks a field the URL said nothing about.
 */

export interface ParsedConnection {
  host?: string;
  port?: string;
  user?: string;
  password?: string;
  database?: string;
  sslmode?: string;
  /** MongoDB keeps the whole URI too — its config has a dedicated `uri` key. */
  uri?: string;
}

/** Schemes we recognise, mapped to the connector types they belong to. */
const KNOWN_SCHEMES = new Set([
  'postgres',
  'postgresql',
  'mysql',
  'mariadb',
  'mongodb',
  'mongodb+srv',
  'sqlserver',
  'mssql',
  'oracle',
  'clickhouse',
  'yugabyte',
]);

/**
 * Whether a string looks like a connection URL at all — used to decide when a
 * pasted value should be treated as one.
 */
export function looksLikeConnectionUrl(value: string): boolean {
  const m = /^([a-z][a-z0-9+]*):\/\//i.exec(value.trim());
  return m !== null && KNOWN_SCHEMES.has(m[1].toLowerCase());
}

/**
 * Parse the URL. Returns null when the value is not a recognisable connection
 * URL, so callers can fall back to treating the input as a plain host.
 */
export function parseConnectionUrl(value: string): ParsedConnection | null {
  const trimmed = value.trim();
  if (!looksLikeConnectionUrl(trimmed)) return null;

  let url: URL;
  try {
    // URL rejects some legal-in-driver strings (multi-host mongodb lists);
    // normalise the scheme to a parseable one and keep the original for uri.
    url = new URL(trimmed.replace(/^[a-z][a-z0-9+]*:\/\//i, 'http://'));
  } catch {
    return null;
  }

  const scheme = /^([a-z][a-z0-9+]*):\/\//i.exec(trimmed)![1].toLowerCase();
  const out: ParsedConnection = {};

  if (url.hostname) out.host = url.hostname;
  if (url.port) out.port = url.port;
  if (url.username) out.user = decodeURIComponent(url.username);
  if (url.password) out.password = decodeURIComponent(url.password);

  const path = url.pathname.replace(/^\//, '');
  if (path) out.database = decodeURIComponent(path);

  // sslmode is the one query parameter common enough to lift into a field.
  const sslmode = url.searchParams.get('sslmode');
  if (sslmode) out.sslmode = sslmode;

  // SQL Server style: ?database=name rather than a path.
  const qsDatabase = url.searchParams.get('database');
  if (!out.database && qsDatabase) out.database = qsDatabase;

  if (scheme.startsWith('mongodb')) out.uri = trimmed;

  return out;
}
