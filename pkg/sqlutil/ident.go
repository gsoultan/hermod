package sqlutil

import (
	"fmt"
	"regexp"
	"strings"
)

var identRe = regexp.MustCompile(`^[A-Za-z0-9_\.]+$`)

// QuoteIdent validates and quotes an SQL identifier (optionally schema-qualified)
// according to the target driver. It supports dot-separated identifiers like schema.table.
// Drivers: pgx/postgres -> "name", mysql/mariadb/sqlite -> `name`, mssql -> [name].
func QuoteIdent(driver, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty identifier")
	}
	if !identRe.MatchString(name) {
		return "", fmt.Errorf("invalid identifier: %s", name)
	}
	parts := strings.Split(name, ".")

	quote := func(s string) string {
		switch driver {
		case "pgx", "postgres":
			return "\"" + s + "\""
		case "mysql", "mariadb", "sqlite":
			return "`" + s + "`"
		case "mssql", "sqlserver":
			return "[" + s + "]"
		default:
			// Safe fallback
			return "\"" + s + "\""
		}
	}

	for i, p := range parts {
		parts[i] = quote(p)
	}
	return strings.Join(parts, "."), nil
}

// Placeholder returns a placeholder suitable for the driver and 1-based index.
func Placeholder(driver string, index int) string {
	switch driver {
	case "pgx", "postgres":
		return fmt.Sprintf("$%d", index)
	default: // mysql, mariadb, sqlite, mssql use '?'
		return "?"
	}
}
