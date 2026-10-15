// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lineage

import (
	"net/url"
	"slices"
	"strings"
)

// asset is one data asset as a source, sink or node configures it.
type asset struct {
	kind    string
	system  string
	locator string
	object  string
	// label overrides the default label (the object, or the locator).
	label string
	// resolved is false for a record-scoped KindConnector asset.
	resolved bool
}

// id is the asset's graph id: kind:system://locator/object. Two records that
// configure the same table, topic or file produce the same id, which is what
// joins workflows through it.
func (a asset) id() string {
	id := a.kind + ":" + a.system + "://" + a.locator
	if a.object != "" {
		id += "/" + strings.TrimPrefix(a.object, "/")
	}
	return id
}

func (a asset) displayLabel() string {
	switch {
	case a.label != "":
		return a.label
	case a.object != "":
		return a.object
	case a.locator != "":
		return a.locator
	}
	return a.system
}

// connector is the record-scoped asset of a source or sink whose physical
// identity this package cannot derive.
func connector(typ, recordKind, recordID, name string) asset {
	return asset{kind: KindConnector, system: typ, locator: recordKind + "/" + recordID, label: name}
}

// cfgMap is a flat source or sink config.
type cfgMap map[string]string

func (c cfgMap) get(key string) string { return strings.TrimSpace(c[key]) }

// first returns the first non-empty value of keys.
func (c cfgMap) first(keys ...string) string {
	for _, k := range keys {
		if v := c.get(k); v != "" {
			return v
		}
	}
	return ""
}

// list splits a comma-separated value, trimmed, without empties.
func list(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sortedList is list, sorted, joined back with commas: broker and host lists
// name the same cluster in any order.
func sortedList(raw string, clean func(string) string) string {
	items := list(raw)
	if clean != nil {
		for i, it := range items {
			items[i] = clean(it)
		}
	}
	slices.Sort(items)
	return strings.Join(slices.Compact(items), ",")
}

// hostPort joins a host and an optional port, host lower-cased.
func hostPort(host, port string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if port = strings.TrimSpace(port); port != "" {
		return host + ":" + port
	}
	return host
}

// joinPath joins non-empty segments with "/".
func joinPath(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// urlLocator reduces a URL to host and path. User info, query and fragment are
// dropped: they are where credentials live (user:password@, ?password=,
// ?api_key=), and they do not change which system is reached. A database
// named in the query (sqlserver://host?database=x) is kept as the path. It
// reports false for a value that is not a URL with a host.
func urlLocator(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "{{") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		path = u.Query().Get("database")
	}
	return joinPath(strings.ToLower(u.Host), path), true
}

// dsnLocator reduces a non-URL DSN to where it points: the MySQL driver's
// user:pass@tcp(host:port)/db and Snowflake's user:pass@account/db/schema.
// Everything before the last "@" (the credentials) and after "?" is dropped.
func dsnLocator(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "{{") || strings.Contains(raw, " ") {
		return "", false
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	if strings.Contains(raw, "=") {
		// A key=value DSN ("host=x password=y") is not parsed: refusing it is
		// safer than guessing which of its words are secret.
		return "", false
	}
	if strings.HasPrefix(raw, "tcp(") || strings.HasPrefix(raw, "unix(") {
		end := strings.Index(raw, ")")
		if end < 0 {
			return "", false
		}
		addr := raw[strings.Index(raw, "(")+1 : end]
		return joinPath(strings.ToLower(addr), raw[end+1:]), true
	}
	if raw == "" || strings.Contains(raw, ":") && !strings.Contains(raw, "/") {
		return "", false
	}
	return strings.Trim(raw, "/"), true
}

// connLocator is where a connection-string-or-keys config points, read the way
// factory.BuildConnectionString reads it: connection_string, then uri, then
// url, and otherwise host, port and dbname — the last only when keysOK, for the
// types BuildConnectionString builds from keys.
func connLocator(c cfgMap, keysOK bool) (string, bool) {
	for _, key := range []string{"connection_string", "uri", "url", "dsn"} {
		raw := c.get(key)
		if raw == "" {
			continue
		}
		if loc, ok := urlLocator(raw); ok {
			return loc, true
		}
		if loc, ok := dsnLocator(raw); ok {
			return loc, true
		}
		return "", false
	}
	if !keysOK {
		return "", false
	}
	hp := hostPort(c.get("host"), c.get("port"))
	if hp == "" {
		return "", false
	}
	return joinPath(hp, c.get("dbname")), true
}

// hostOf is a host given either bare or as a URL ("https://sap.example.com").
func hostOf(raw string) string {
	if loc, ok := urlLocator(raw); ok {
		return loc
	}
	if strings.Contains(raw, "://") || strings.Contains(raw, "@") || strings.Contains(raw, "{{") {
		return ""
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(raw), "/"))
}
