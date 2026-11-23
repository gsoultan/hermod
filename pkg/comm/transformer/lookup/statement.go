// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"errors"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// Statement is the SQL a DB Lookup node sends, with its placeholders.
type Statement struct {
	SQL string
	// Args holds one value per placeholder. A value that comes from a message
	// is nil: a plan check has no message, and a generic plan does not depend
	// on it. A literal written in the node's where clause keeps its value.
	Args []any
	// Params is len(Args).
	Params int
}

// LookupStatement builds the statement a db_lookup node with this config sends
// to a database of the given driver, through the same builders the node runs
// with, so a query-plan check plans what actually runs.
//
// Batching is ignored: a batched lookup sends the same statement with an IN
// list, and its plan reads the same indexes.
func LookupStatement(config map[string]any, driver string) (Statement, error) {
	mode := core.GetConfigString(config, "mode")
	queryTemplate := core.GetConfigString(config, "queryTemplate")
	useTemplate := mode == "query" || (mode != "table" && queryTemplate != "")

	if useTemplate {
		if strings.TrimSpace(queryTemplate) == "" {
			return Statement{}, errors.New("db_lookup: the node has no query")
		}
		b := sqlutil.ParameterizeTemplateWith(driver, queryTemplate, func(string) any { return nil })
		if b.Err != nil {
			return Statement{}, b.Err
		}
		return Statement{SQL: b.SQL, Args: b.Args, Params: len(b.Args)}, nil
	}

	table := core.GetConfigString(config, "table")
	if table == "" {
		return Statement{}, errors.New("db_lookup: the node has no table")
	}
	query, args, _, err := tableLookupQuery(driver, table,
		core.GetConfigString(config, "keyColumn"), nil,
		core.GetConfigString(config, "whereClause"),
		core.GetConfigString(config, "valueColumn"), mapClause(nil))
	if err != nil {
		return Statement{}, err
	}
	return Statement{SQL: query, Args: args, Params: len(args)}, nil
}

// LookupTarget resolves the source a db_lookup node with this config queries,
// and the dialect its SQL is written in, refusing what the node refuses: a
// node with no source, and a source that streams changes (CDC).
func LookupTarget(ctx context.Context, registry interface {
	GetSourceConfig(ctx context.Context, id string) (storage.Source, error)
}, config map[string]any) (storage.Source, string, error) {
	id := core.GetConfigString(config, "sourceId")
	if id == "" {
		return storage.Source{}, "", errors.New("db_lookup: the node has no source")
	}
	src, err := registry.GetSourceConfig(ctx, id)
	if err != nil {
		return storage.Source{}, "", err
	}
	if err := requireNonCDCSource(src); err != nil {
		return storage.Source{}, "", err
	}
	return src, lookupDialect(ctx, registry, src), nil
}
