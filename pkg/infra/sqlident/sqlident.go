// Package sqlident validates and quotes SQL identifiers.
//
// Values in Hermod's generated SQL are parameterized, but *identifiers* —
// table and schema names — cannot be: no driver supports a placeholder in
// `INSERT INTO ?`. They are interpolated into the statement, so they have to be
// proven safe before they get there.
//
// That mattered more than it looks. A SQL sink without an explicit table name
// falls back to the table and schema carried on the message itself, and a
// message's table can come from the wire: the WebSocket source sets it straight
// from the inbound envelope (`m.SetTable(env.Table)`). A peer could therefore
// choose the identifier that gets pasted into a MERGE or INSERT against the
// destination database — arbitrary SQL execution on a system that is usually
// far more sensitive than Hermod itself. The same held, with an authenticated
// Editor rather than a remote peer, for identifiers taken from sink config.
//
// Validation is deliberately strict rather than clever. It allows what a table
// name legitimately is and rejects everything else, instead of trying to escape
// hostile input — an allowlist cannot be outwitted by a quoting trick.
package sqlident

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MaxLength bounds an identifier. The shortest limit among the databases
// Hermod targets is 63 bytes (PostgreSQL); 128 covers the rest with room to
// spare, and any name longer than that is not a real table.
const MaxLength = 128

// Dialect selects the quoting style.
type Dialect string

const (
	Postgres   Dialect = "postgres"
	MySQL      Dialect = "mysql"
	MSSQL      Dialect = "mssql"
	SQLite     Dialect = "sqlite"
	ClickHouse Dialect = "clickhouse"
)

// Validate reports whether name is a safe SQL identifier, optionally
// schema-qualified as `schema.table`.
//
// Accepted: an optional schema part and a name part, each starting with a
// letter or underscore and continuing with letters, digits or underscores.
// Everything else — quotes, semicolons, whitespace, comment markers, unicode
// look-alikes, empty parts — is rejected.
func Validate(name string) error {
	if name == "" {
		return errors.New("sql identifier is empty")
	}
	if len(name) > MaxLength {
		return fmt.Errorf("sql identifier is %d bytes, limit is %d", len(name), MaxLength)
	}

	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return fmt.Errorf("sql identifier %q has %d dot-separated parts, expected at most schema.table", name, len(parts))
	}
	for _, part := range parts {
		if err := validatePart(part); err != nil {
			return fmt.Errorf("sql identifier %q: %w", name, err)
		}
	}
	return nil
}

func validatePart(part string) error {
	if part == "" {
		return errors.New("empty part")
	}
	for i, r := range part {
		// ASCII only. Unicode identifiers are legal in some engines, but
		// allowing them here would mean reasoning about homoglyphs and
		// normalisation for no practical gain.
		if r > unicode.MaxASCII {
			return fmt.Errorf("non-ASCII character %q", r)
		}
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		switch {
		case isLetter || r == '_':
			// always fine
		case isDigit:
			if i == 0 {
				return errors.New("starts with a digit")
			}
		default:
			return fmt.Errorf("illegal character %q", r)
		}
	}
	return nil
}

// Quote validates name and returns it quoted for the dialect.
//
// Quoting alone is not the defence — validation is. Quoting on top means a
// legitimate name that collides with a reserved word still works.
func Quote(d Dialect, name string) (string, error) {
	if err := Validate(name); err != nil {
		return "", err
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = quotePart(d, p)
	}
	return strings.Join(parts, "."), nil
}

func quotePart(d Dialect, part string) string {
	switch d {
	case MySQL, ClickHouse:
		return "`" + part + "`"
	case MSSQL:
		return "[" + part + "]"
	default: // Postgres, SQLite and the ANSI default
		return `"` + part + `"`
	}
}

// MustBeSafe returns name unchanged when it validates, and an error otherwise.
// It is the minimal call site for code that already builds its own SQL and just
// needs the identifier checked before interpolation.
func MustBeSafe(name string) (string, error) {
	if err := Validate(name); err != nil {
		return "", err
	}
	return name, nil
}
