package lookup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("reference_lookup", &ReferenceLookupTransformer{})
}

// ReferenceLookupTransformer enriches a record from a reference file -- a CSV,
// TSV or Excel workbook -- held in memory.
//
// The file is read once and kept, keyed by its key column, and read again
// when its modification time or size changes (checked at most once a second).
// It is read from the worker's file system, and only from inside the upload
// directory or a directory in HERMOD_REFERENCE_DIRS (see reference_roots.go).
// Object-store and HTTP locations are not read.
//
// Config:
//   - filePath: the file. Required.
//   - format: "csv", "tsv" or "xlsx"; default from the extension.
//   - sheet: the workbook sheet, default the first. delimiter: a CSV's
//     separator, default "," ("tab" for a tab).
//   - keyColumn: the file's column to match on (its first row names the
//     columns). Required.
//   - keyField: the record's field holding the key. Required.
//   - columns: the columns to copy (a list or comma-separated), default every
//     column except the key.
//   - targetField: put the columns under this field as one object; empty
//     writes each column onto the record.
//   - onMiss / defaultValue: as for the other lookups -- passthrough
//     (default), fail, or default (writes defaultValue to targetField).
//   - maxBytes (default 32 MiB, at most 256 MiB) and maxRows (default
//     200000, at most 2000000) bound the file.
//
// Values are copied as text, as they are in the file. A key that appears on
// several rows matches the first.
type ReferenceLookupTransformer struct{}

func (t *ReferenceLookupTransformer) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	spec, keyField, err := referenceConfig(config)
	if err != nil {
		return msg, fmt.Errorf("reference_lookup: %w", err)
	}
	table, err := loadReference(spec)
	if err != nil {
		return msg, fmt.Errorf("reference_lookup: %w", err)
	}

	target := strings.TrimSpace(core.GetConfigString(config, "targetField"))
	defaultValue := core.GetConfigString(config, "defaultValue")
	policy := resolveMissPolicy(config, defaultValue != "")

	keyVal := evaluator.GetMsgValByPath(msg, keyField)
	if keyVal == nil {
		cause := fmt.Errorf("reference_lookup: the record has no value at key path %q", keyField)
		return msg, applyMissPolicy(msg, policy, target, defaultValue, cause)
	}
	key := strings.TrimSpace(fmt.Sprint(keyVal))
	row, ok := table.rows[key]
	if !ok {
		cause := fmt.Errorf("reference_lookup: %s has no row with %s = %q", spec.path, spec.keyColumn, key)
		return msg, applyMissPolicy(msg, policy, target, defaultValue, cause)
	}

	cols := referenceColumns(config, table, spec.keyColumn)
	if target != "" {
		obj := make(map[string]any, len(cols))
		for _, c := range cols {
			obj[c] = row[c]
		}
		msg.SetData(target, obj)
		return msg, nil
	}
	for _, c := range cols {
		msg.SetData(c, row[c])
	}
	return msg, nil
}

// referenceConfig reads and checks the settings that decide which table is
// loaded.
func referenceConfig(config map[string]any) (referenceSpec, string, error) {
	path := strings.TrimSpace(core.GetConfigString(config, "filePath"))
	keyColumn := strings.TrimSpace(core.GetConfigString(config, "keyColumn"))
	keyField := strings.TrimSpace(core.GetConfigString(config, "keyField"))
	switch {
	case path == "":
		return referenceSpec{}, "", errors.New("set filePath to the reference file")
	case strings.Contains(path, "://"):
		return referenceSpec{}, "", fmt.Errorf("%s is not a local file; upload the file or give a path on the worker", path)
	case keyColumn == "":
		return referenceSpec{}, "", errors.New("set keyColumn to the file's key column")
	case keyField == "":
		return referenceSpec{}, "", errors.New("set keyField to the record's key field")
	}
	format, err := referenceFormat(path, strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "format"))))
	if err != nil {
		return referenceSpec{}, "", err
	}
	return referenceSpec{
		path:      path,
		format:    format,
		sheet:     strings.TrimSpace(core.GetConfigString(config, "sheet")),
		delimiter: core.GetConfigString(config, "delimiter"),
		keyColumn: keyColumn,
		maxBytes:  boundedInt(config["maxBytes"], defaultReferenceBytes, maxReferenceBytes),
		maxRows:   boundedInt(config["maxRows"], defaultReferenceRows, maxReferenceRows),
	}, keyField, nil
}

func referenceColumns(config map[string]any, table *referenceTable, keyColumn string) []string {
	cols := core.GetConfigStringSlice(config, "columns")
	if len(cols) == 0 {
		cols = core.SplitComma(core.GetConfigString(config, "columns"))
	}
	if len(cols) > 0 {
		return cols
	}
	out := make([]string, 0, len(table.columns))
	for _, c := range table.columns {
		if c != keyColumn && c != "" {
			out = append(out, c)
		}
	}
	return out
}

// boundedInt reads a positive whole number, defaulting when it is absent or
// not positive and capping it at most.
func boundedInt(v any, def, most int) int {
	n, ok := evaluator.ToInt64(v)
	if !ok || n <= 0 {
		return def
	}
	return int(min(n, int64(most)))
}
