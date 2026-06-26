package sqlutil

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// identSegmentRe matches a single, unquoted identifier segment. Each dot-separated
// part of a qualified identifier (e.g. schema.table) must match this on its own,
// which rejects empty segments produced by leading/trailing/consecutive dots
// (e.g. ".", "..", "users.", ".users", "a..b").
var identSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// maxIdentLen is the smallest common identifier length limit across the supported
// engines (PostgreSQL = 63 bytes, NAMEDATALEN-1). Enforcing it early lets the
// builder fail closed with a clear error instead of deferring to an opaque server
// error or a silent truncation.
const maxIdentLen = 63

// QuoteIdent validates and quotes an SQL identifier (optionally schema-qualified)
// according to the target driver. It supports dot-separated identifiers like schema.table.
// Drivers: pgx/postgres -> "name", mysql/mariadb/sqlite -> `name`, mssql -> [name].
//
// It returns an error for any malformed or unsafe identifier. Callers MUST check the
// error and never splice the result on failure, otherwise an empty identifier would
// be interpolated straight into the SQL (e.g. "WHERE  = $1").
//
// NOTE: quoting makes identifiers case-sensitive. On engines that fold unquoted
// identifiers (e.g. PostgreSQL lower-cases them) a column physically named "id"
// must be referenced as "id", not "ID".
func QuoteIdent(driver, name string) (string, error) {
	if err := ValidateIdent(name); err != nil {
		return "", err
	}

	parts := strings.Split(name, ".")
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = quoteSegment(driver, p)
	}
	return strings.Join(quoted, "."), nil
}

// quoteSegment wraps a single, already-validated segment in the driver-specific
// quote characters. As defense in depth it also escapes the closing quote by
// doubling it, so the function stays injection-safe even if ValidateIdent is ever
// loosened (e.g. to support Unicode identifiers).
func quoteSegment(driver, s string) string {
	switch driver {
	case "mysql", "mariadb", "sqlite":
		return "`" + strings.ReplaceAll(s, "`", "``") + "`"
	case "mssql", "sqlserver":
		return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
	default:
		// pgx/postgres/snowflake/oracle/clickhouse/cassandra/yugabyte + safe fallback.
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
}

// ValidateIdent verifies that an identifier (optionally schema/keyspace-qualified)
// contains only safe characters, has no empty segments, and respects the common
// length limit, without altering its quoting. Use it for engines (e.g. CQL) where
// identifiers are interpolated as-is, to prevent SQL/CQL injection.
func ValidateIdent(name string) error {
	if name == "" {
		return errors.New("empty identifier")
	}
	if len(name) > maxIdentLen {
		return fmt.Errorf("identifier too long (%d > %d): %q", len(name), maxIdentLen, name)
	}
	for _, seg := range strings.Split(name, ".") {
		if !identSegmentRe.MatchString(seg) {
			return fmt.Errorf("invalid identifier segment in %q", name)
		}
	}
	return nil
}

// CanonicalDriver maps a user-facing source/sink type (e.g. "mssql", "postgres")
// to the actual database/sql driver name that must be passed to sql.Open. It is the
// single source of truth shared by connection opening and placeholder generation so
// the two can never drift apart. The second return value reports whether the type is
// backed by a generic SQL driver.
//
// IMPORTANT: this mapping MUST stay in sync with how connections are opened
// (see registry.getOrOpenDB). For example "mssql" is opened with the
// microsoft/go-mssqldb "sqlserver" driver, which only accepts @pN placeholders.
func CanonicalDriver(sourceType string) (string, bool) {
	switch sourceType {
	case "postgres", "yugabyte", "pgx":
		return "pgx", true
	case "mysql", "mariadb":
		return "mysql", true
	case "sqlite":
		return "sqlite", true
	case "mssql", "sqlserver":
		return "sqlserver", true
	case "oracle":
		return "oracle", true
	case "clickhouse":
		return "clickhouse", true
	case "snowflake":
		return "snowflake", true
	default:
		return "", false
	}
}

// Placeholder returns a bound-parameter placeholder suitable for the driver and
// 1-based index. The driver argument may be either a user-facing type label
// (e.g. "mssql") or an actual driver name (e.g. "sqlserver"); both are normalized
// through CanonicalDriver so the placeholder style always matches the driver that
// will ultimately execute the query.
func Placeholder(driver string, index int) string {
	canonical, ok := CanonicalDriver(driver)
	if !ok {
		// Unknown driver: fall back to the most widely supported positional style.
		canonical = driver
	}
	switch canonical {
	case "pgx":
		return fmt.Sprintf("$%d", index)
	case "oracle":
		return fmt.Sprintf(":%d", index)
	case "sqlserver":
		// microsoft/go-mssqldb rejects '?' and requires ordinal @p1..@pN
		// ("Incorrect syntax near '?'."). Args are bound positionally.
		return fmt.Sprintf("@p%d", index)
	default: // mysql, mariadb, sqlite, snowflake, clickhouse, cassandra use '?'
		return "?"
	}
}
