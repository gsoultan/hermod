// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/advisor/queryplan"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgconn"
)

// speedCheckMaxBody caps a speed check request: a connector's config and one
// statement.
const speedCheckMaxBody int64 = 1 << 20

// errNotPostgres is the answer for a database the planner cannot read yet.
var errNotPostgres = errors.New("only PostgreSQL query plans can be read so far; this database is not PostgreSQL")

// SpeedCheckHandler serves the SQL builder's speed check: the statement being
// written, planned on its database and read against the tables' indexes.
type SpeedCheckHandler struct {
	*handlers.Handler

	// plan is set by tests; nil plans on the connector's database.
	plan func(ctx context.Context, src storage.Source, sql string) (queryplan.Check, error)
}

// NewSpeedCheckHandler wraps the shared handler.
func NewSpeedCheckHandler(h *handlers.Handler) *SpeedCheckHandler {
	return &SpeedCheckHandler{Handler: h}
}

// RegisterSpeedCheckRoutes registers the endpoint. It connects to the
// connector's database, like Run Query, so it takes an editor.
func (h *SpeedCheckHandler) RegisterSpeedCheckRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/sources/query/speed-check", h.EditorOnly(h.Check))
}

// speedCheckView is the answer: the statement as the engine sends it, and
// what its plan says.
type speedCheckView struct {
	// SQL is the template with each {{.path}} as a bound parameter.
	SQL string `json:"sql"`
	queryplan.Check
	// Rewritten is the whole query as written, templates included, with each
	// condition a finding offers a rewrite for replaced by it.
	Rewritten string `json:"rewritten,omitempty"`
	// RewrittenCost is the planner's cost for Rewritten, against Cost.
	RewrittenCost float64 `json:"rewritten_cost,omitempty"`
}

// rewritten is query with the conditions the plan faulted rewritten, or ""
// when the plan faults none or the text could not be rewritten.
func rewritten(query string, c queryplan.Check) string {
	if !slices.ContainsFunc(c.Findings, func(f queryplan.Finding) bool { return f.Rewrite != "" }) {
		return ""
	}
	typeOf := func(col string) string {
		for _, t := range c.Tables {
			if ty := t.ColumnType(col); ty != "" {
				return ty
			}
		}
		return ""
	}
	if out, ok := queryplan.RewriteTruncEquality(query, typeOf); ok {
		return out
	}
	return ""
}

// Check answers POST /api/sources/query/speed-check. The statement is only
// planned, never run, in a read-only transaction that is rolled back (see
// queryplan.Run), so a write statement is safe to check as well. The plan is
// the generic one, made without the message's values, which is the plan the
// engine's prepared statement gets once it is reused.
func (h *SpeedCheckHandler) Check(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config factory.SourceConfig `json:"config"`
		Query  string               `json:"query"`
	}
	if !h.DecodeJSONBody(w, r, speedCheckMaxBody, &req, "") {
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		h.JsonError(w, "Write a statement to check", http.StatusBadRequest)
		return
	}
	if !h.AllowConnectorVHost(w, r, req.Config.VHost) {
		return
	}
	src := storage.Source{ID: req.Config.ID, Type: req.Config.Type, VHost: req.Config.VHost, Config: req.Config.Config}
	driver, err := h.driver(r.Context(), src)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	stmt := sqlutil.ParameterizeTemplateWith(driver, req.Query, func(string) any { return nil })
	ctx, cancel := context.WithTimeout(r.Context(), queryplan.NodeTimeout)
	defer cancel()
	check, err := h.planOn(ctx, src, stmt.SQL)
	if err != nil {
		h.JsonError(w, err.Error(), planStatus(err))
		return
	}
	if check.Findings == nil {
		check.Findings = []queryplan.Finding{}
	}
	view := speedCheckView{SQL: stmt.SQL, Check: check}
	if better := rewritten(req.Query, check); better != "" {
		// Offered only once the database has planned it: a rewrite it
		// refuses, for a type the range does not fit, is no help.
		bound := sqlutil.ParameterizeTemplateWith(driver, better, func(string) any { return nil })
		if c, err := h.planOn(ctx, src, bound.SQL); err == nil {
			view.Rewritten, view.RewrittenCost = better, c.Cost
		}
	}
	h.RecordAuditLog(r, "INFO", "Checked a query's speed in the SQL builder", "SQL_SPEED_CHECK", "", src.ID, "", map[string]any{
		"findings": len(check.Findings),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

// driver resolves the connector to its database/sql driver, through a batch
// SQL source to the source it reads, and refuses anything but PostgreSQL.
func (h *SpeedCheckHandler) driver(ctx context.Context, src storage.Source) (string, error) {
	if h.Registry == nil {
		if d, _ := sqlutil.CanonicalDriver(src.Type); d == "pgx" {
			return d, nil
		}
		return "", errNotPostgres
	}
	_, driver, err := h.Registry.SourceConnString(ctx, src)
	if err != nil || driver != "pgx" {
		return "", errNotPostgres
	}
	return driver, nil
}

func (h *SpeedCheckHandler) planOn(ctx context.Context, src storage.Source, sql string) (queryplan.Check, error) {
	if h.plan != nil {
		return h.plan(ctx, src, sql)
	}
	db, err := h.Registry.GetDB(ctx, src.Type, src.Config)
	if err != nil {
		return queryplan.Check{}, err
	}
	return queryplan.Run(ctx, db, sql)
}

// planStatus tells a statement the server refused, which is the writer's to
// fix, from a database that could not be reached.
func planStatus(err error) int {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) || errors.Is(err, queryplan.ErrUnsupported) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadGateway
}
