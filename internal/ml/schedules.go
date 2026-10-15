// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// Schedules: a dataset refilled from a database query on a cron schedule,
// and a recommendation model's answers built for every salesman, customer or
// product and written to a database table. Every API replica runs a
// Scheduler; each run is claimed in storage, so it happens once across them.

const (
	// ScheduleInterval is how often a Scheduler looks for due schedules.
	ScheduleInterval = time.Minute
	// scheduleClaim bounds one run: a claim outlives the run it covers.
	scheduleClaim = 30 * time.Minute
	// MaxBuildRows bounds one build's rows: subjects x k (x plan days).
	MaxBuildRows = 1_000_000
)

// The subjects a build answers for.
const (
	BuildAllSalesmen  = "all_salesmen"
	BuildAllCustomers = "all_customers"
	BuildAllProducts  = "all_products"
)

// The ways a build writes its target table.
const (
	BuildReplace = "replace"
	BuildAppend  = "append"
)

// ErrBadSchedule marks a schedule that is wrong in itself.
var ErrBadSchedule = errors.New("invalid schedule")

func badSchedule(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadSchedule}, args...)...)
}

// DatasetRefresh is a dataset's refresh: run Query on the database source
// SourceID on Schedule and replace the dataset's rows with what it returns,
// at most MaxRows (the worker's default when 0).
type DatasetRefresh struct {
	Schedule string `json:"schedule"`
	SourceID string `json:"source_id"`
	Query    string `json:"query"`
	MaxRows  int    `json:"max_rows,omitempty"`
}

// ScheduleStatus is what GET shows of a schedule: its cron expression, when
// it last ran and what that did, and when it runs next.
type ScheduleStatus struct {
	Schedule  string     `json:"schedule"`
	LastRun   *time.Time `json:"last_run"`
	LastRows  int        `json:"last_rows"`
	LastError string     `json:"last_error"`
	NextRun   *time.Time `json:"next_run"`
}

// BuildTarget is the table a build writes: Table of the database source
// SourceID, whose rows of the model are replaced or appended to (Mode,
// replace when empty). The table must exist with the columns BuildColumns.
type BuildTarget struct {
	SourceID string `json:"source_id"`
	Table    string `json:"table"`
	Mode     string `json:"mode,omitempty"`
}

// ModelBuild is a recommendation model's scheduled build. WithinDays, for a
// reorder model, builds only the customers due to order within that many
// days of the build, overdue ones included.
type ModelBuild struct {
	Schedule   string      `json:"schedule"`
	For        string      `json:"for"`
	PeriodDays int         `json:"period_days,omitempty"`
	WithinDays int         `json:"within_days,omitempty"`
	K          int         `json:"k,omitempty"`
	Target     BuildTarget `json:"target"`
}

// BuildColumns are the columns a build writes, in order. A target table
// must have them all; BuildDueDateColumn, BuildSegmentColumn,
// BuildSizeColumn, BuildBrandColumn, BuildSubcategoryColumn,
// BuildSuggestedQtyColumn, BuildPriceTierColumn, BuildChannelColumn and
// BuildRegionColumn it may have, after them.
var BuildColumns = []string{"model", "version", "recommendation_id", "for_id", "item_id", "rank", "score", "plan_date",
	"reasons_text", "built_at"}

// BuildDueDateColumn is the optional column a build writes a reorder item's
// due date to, when the target table has it: a table made before it existed
// keeps working.
const BuildDueDateColumn = "due_date"

// BuildSegmentColumn and BuildSizeColumn are the optional columns a build
// writes a row's customer category and size to, when the target table has
// them: the listed customer's, or for a product list the customer it is for.
const (
	BuildSegmentColumn = "segment"
	BuildSizeColumn    = "size"
)

// BuildBrandColumn, BuildSubcategoryColumn and BuildSuggestedQtyColumn are
// the optional columns a build writes a product row's brand, subcategory
// and suggested quantity to, when the target table has them.
const (
	BuildBrandColumn        = "brand"
	BuildSubcategoryColumn  = "subcategory"
	BuildSuggestedQtyColumn = "suggested_qty"
)

// BuildPriceTierColumn, BuildChannelColumn and BuildRegionColumn are the
// optional columns a build writes a row's customer price tier, channel and
// region to, when the target table has them: as for BuildSegmentColumn.
const (
	BuildPriceTierColumn = "price_tier"
	BuildChannelColumn   = "channel"
	BuildRegionColumn    = "region"
)

// buildOptional are the optional columns, in the order a build writes them
// after BuildColumns, each with its value of a row (nil for NULL).
var buildOptional = []struct {
	name  string
	value func(BuildRow) any
}{
	{BuildDueDateColumn, func(r BuildRow) any { return nullIfEmpty(r.DueDate) }},
	{BuildSegmentColumn, func(r BuildRow) any { return nullIfEmpty(r.Segment) }},
	{BuildSizeColumn, func(r BuildRow) any { return nullIfEmpty(r.Size) }},
	{BuildBrandColumn, func(r BuildRow) any { return nullIfEmpty(r.Brand) }},
	{BuildSubcategoryColumn, func(r BuildRow) any { return nullIfEmpty(r.Subcategory) }},
	{BuildSuggestedQtyColumn, func(r BuildRow) any {
		if r.SuggestedQty <= 0 {
			return nil
		}
		return r.SuggestedQty
	}},
	{BuildPriceTierColumn, func(r BuildRow) any { return nullIfEmpty(r.PriceTier) }},
	{BuildChannelColumn, func(r BuildRow) any { return nullIfEmpty(r.Channel) }},
	{BuildRegionColumn, func(r BuildRow) any { return nullIfEmpty(r.Region) }},
}

// BuildRow is one written row: one item of one subject's answer. PlanDate
// is the day a visit plan puts it on, DueDate the day a reorder customer is
// due to order; each empty otherwise. Segment and Size are the customer's
// category and size, when the model knows them: the listed customer's, or
// for a product list the customer it is for; PriceTier, Channel and Region
// its price tier, channel and region likewise. Brand and Subcategory are a
// product row's, when the model knows them, and SuggestedQty what its
// answer suggests ordering (0 for none).
type BuildRow struct {
	Model            string    `json:"model"`
	Version          string    `json:"version"`
	RecommendationID string    `json:"recommendation_id"`
	ForID            string    `json:"for_id"`
	ItemID           string    `json:"item_id"`
	Rank             int       `json:"rank"`
	Score            float64   `json:"score"`
	PlanDate         string    `json:"plan_date,omitempty"`
	DueDate          string    `json:"due_date,omitempty"`
	Segment          string    `json:"segment,omitempty"`
	Size             string    `json:"size,omitempty"`
	Brand            string    `json:"brand,omitempty"`
	Subcategory      string    `json:"subcategory,omitempty"`
	SuggestedQty     int       `json:"suggested_qty,omitempty"`
	PriceTier        string    `json:"price_tier,omitempty"`
	Channel          string    `json:"channel,omitempty"`
	Region           string    `json:"region,omitempty"`
	ReasonsText      string    `json:"reasons_text"`
	BuiltAt          time.Time `json:"built_at"`
}

// ScheduleRunner is what a schedule's run needs of the engine: refilling a
// dataset from a database source, and opening a source of the vhost for a
// build's writes. The registry implements it.
type ScheduleRunner interface {
	MLDatasetFromQuery(ctx context.Context, vhost, dataset, sourceID, query string, maxRows int) (int, error)
	OpenMLTarget(ctx context.Context, vhost, sourceID string) (*sql.DB, string, error)
}

func (s *Service) scheduleStore() (storage.MLScheduleStore, error) {
	ss, ok := s.store().(storage.MLScheduleStore)
	if !ok {
		return nil, storage.ErrMLModelsUnsupported
	}
	return ss, nil
}

func checkCron(expr string) error {
	if expr == "" {
		return badSchedule("give a schedule: a five-field cron expression or a descriptor such as @daily")
	}
	if _, err := cronParser.Parse(expr); err != nil {
		return badSchedule("schedule %q is not a five-field cron expression or a descriptor such as @daily: %v", expr, err)
	}
	return nil
}

// Validate reports what is wrong with a refresh.
func (r DatasetRefresh) Validate() error {
	if err := checkCron(r.Schedule); err != nil {
		return err
	}
	switch {
	case r.SourceID == "" || strings.TrimSpace(r.Query) == "":
		return badSchedule(`a refresh names a database source and a query: {"schedule", "source_id", "query"}`)
	case r.MaxRows < 0:
		return badSchedule("max_rows cannot be negative")
	}
	return nil
}

// SetDatasetRefresh makes a dataset refresh itself on a schedule.
func (s *Service) SetDatasetRefresh(ctx context.Context, vhost, dataset string, r DatasetRefresh, by string) (ScheduleStatus, error) {
	if err := r.Validate(); err != nil {
		return ScheduleStatus{}, err
	}
	return s.putSchedule(ctx, vhost, storage.MLScheduleDatasetRefresh, dataset, r.Schedule, r, by)
}

// putSchedule saves a schedule with spec as its definition.
func (s *Service) putSchedule(ctx context.Context, vhost, kind, name, schedule string, spec any, by string) (ScheduleStatus, error) {
	ss, err := s.scheduleStore()
	if err != nil {
		return ScheduleStatus{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return ScheduleStatus{}, err
	}
	sc := storage.MLSchedule{VHost: vhost, Kind: kind, Name: name, Schedule: schedule, Spec: raw, UpdatedBy: by}
	if err := ss.PutMLSchedule(ctx, sc); err != nil {
		return ScheduleStatus{}, err
	}
	saved, err := ss.GetMLSchedule(ctx, vhost, kind, name)
	if err != nil {
		return ScheduleStatus{}, err
	}
	return statusOf(saved), nil
}

// ClearDatasetRefresh stops a dataset refreshing itself; ErrNotFound for
// none.
func (s *Service) ClearDatasetRefresh(ctx context.Context, vhost, dataset string) error {
	ss, err := s.scheduleStore()
	if err != nil {
		return err
	}
	return ss.DeleteMLSchedule(ctx, vhost, storage.MLScheduleDatasetRefresh, dataset)
}

// Schedule is a dataset's refresh or a model's build, and its status; nil
// for none, or for a store without schedules.
func (s *Service) Schedule(ctx context.Context, vhost, kind, name string) (*storage.MLSchedule, *ScheduleStatus, error) {
	ss, err := s.scheduleStore()
	if err != nil {
		return nil, nil, nil //nolint:nilerr // a store without schedules has none
	}
	sc, err := ss.GetMLSchedule(ctx, vhost, kind, name)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	st := statusOf(sc)
	return &sc, &st, nil
}

// statusOf is a stored schedule's status: next_run after the last run, or
// after it was set when it has not run.
func statusOf(sc storage.MLSchedule) ScheduleStatus {
	st := ScheduleStatus{Schedule: sc.Schedule, LastRows: sc.LastRows, LastError: sc.LastError}
	if !sc.LastRun.IsZero() {
		at := sc.LastRun
		st.LastRun = &at
	}
	if sched, err := cronParser.Parse(sc.Schedule); err == nil {
		next := sched.Next(scheduleBasis(sc))
		st.NextRun = &next
	}
	return st
}

// scheduleBasis is what a schedule's next run counts from: its last run, or
// when it was set. A run missed while no Hermod was up happens once.
func scheduleBasis(sc storage.MLSchedule) time.Time {
	if sc.LastRun.After(sc.UpdatedAt) {
		return sc.LastRun
	}
	return sc.UpdatedAt
}

// scheduleDue reports whether a schedule should run at now.
func scheduleDue(sc storage.MLSchedule, now time.Time) bool {
	sched, err := cronParser.Parse(sc.Schedule)
	return err == nil && !sched.Next(scheduleBasis(sc)).After(now)
}

// subjects lists what a build answers for, as queries of the model's
// scenario.
func buildQueries(rec *recommend.Model, b ModelBuild, today time.Time) ([]recommend.Query, error) {
	scenario := rec.Scenario()
	allowed := map[string][]string{
		BuildAllSalesmen:  {recommend.ScenarioVisits, recommend.ScenarioReorder},
		BuildAllCustomers: {recommend.ScenarioProducts, recommend.ScenarioReorder},
		BuildAllProducts:  {recommend.ScenarioAudience, recommend.ScenarioSimilar, recommend.ScenarioProducts},
	}
	if !slices.Contains(allowed[b.For], scenario) {
		return nil, badSchedule("a %s model is not built for %q", scenario, b.For)
	}
	if b.WithinDays > 0 && scenario != recommend.ScenarioReorder {
		return nil, badSchedule(`"within_days" applies to a reorder model, not %s`, scenario)
	}
	base := recommend.Query{Today: today, K: b.K, ExcludeSeen: true, WithinDays: b.WithinDays}
	if scenario == recommend.ScenarioVisits && b.PeriodDays > 0 {
		base.From, base.To = today, today.AddDate(0, 0, b.PeriodDays-1)
	}
	var out []recommend.Query
	switch b.For {
	case BuildAllSalesmen:
		for _, id := range rec.Salesmen() {
			q := base
			q.Salesman = id
			out = append(out, q)
		}
	case BuildAllCustomers:
		for _, id := range rec.CustomerIDs() {
			q := base
			q.Customer = id
			out = append(out, q)
		}
	case BuildAllProducts:
		for _, id := range rec.ProductIDs() {
			q := base
			q.Product = id
			out = append(out, q)
		}
	}
	return out, nil
}

// Validate reports what is wrong with a build, as far as the build alone can
// tell; the model's scenario is checked when it runs.
func (b ModelBuild) Validate(scheduled bool) error {
	if scheduled {
		if err := checkCron(b.Schedule); err != nil {
			return err
		}
	}
	if !slices.Contains([]string{BuildAllSalesmen, BuildAllCustomers, BuildAllProducts}, b.For) {
		return badSchedule(`"for" is all_salesmen, all_customers or all_products, not %q`, b.For)
	}
	if b.K < 0 || b.K > recommend.MaxPlan {
		return badSchedule("k is between 1 and %d", recommend.MaxPlan)
	}
	if b.PeriodDays < 0 || b.PeriodDays > 366 {
		return badSchedule("period_days is between 1 and 366")
	}
	if b.WithinDays < 0 || b.WithinDays > recommend.MaxWithinDays {
		return badSchedule("within_days is between 1 and %d", recommend.MaxWithinDays)
	}
	if scheduled || b.Target.SourceID != "" || b.Target.Table != "" {
		return b.Target.validate()
	}
	return nil
}

func (t BuildTarget) validate() error {
	if t.SourceID == "" || t.Table == "" {
		return badSchedule(`a build writes to "target": {"source_id", "table", "mode"}`)
	}
	if err := sqlutil.ValidateIdent(t.Table); err != nil {
		return badSchedule("target table %q: %v", t.Table, err)
	}
	if t.Mode != "" && t.Mode != BuildReplace && t.Mode != BuildAppend {
		return badSchedule(`target mode is "replace" or "append", not %q`, t.Mode)
	}
	return nil
}

// SetModelBuild makes a recommendation model build its answers on a schedule.
func (s *Service) SetModelBuild(ctx context.Context, vhost, name string, b ModelBuild, by string) (ScheduleStatus, error) {
	if err := b.Validate(true); err != nil {
		return ScheduleStatus{}, err
	}
	if _, err := s.recommenderModel(ctx, vhost, name); err != nil {
		return ScheduleStatus{}, err
	}
	return s.putSchedule(ctx, vhost, storage.MLScheduleModelBuild, name, b.Schedule, b, by)
}

// ClearModelBuild stops a model's scheduled build; ErrNotFound for none.
func (s *Service) ClearModelBuild(ctx context.Context, vhost, name string) error {
	ss, err := s.scheduleStore()
	if err != nil {
		return err
	}
	return ss.DeleteMLSchedule(ctx, vhost, storage.MLScheduleModelBuild, name)
}

// BuildRecommendations answers b for every salesman, customer or product of
// the model's live version, as rows. The answers share one
// recommendation_id. A build is not counted against the per-second quota:
// it is a batch the vhost scheduled, not traffic.
func (s *Service) BuildRecommendations(ctx context.Context, vhost, name string, b ModelBuild) ([]BuildRow, error) {
	if err := b.Validate(false); err != nil {
		return nil, err
	}
	m, err := s.recommenderModel(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	rec, err := s.recommender(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", name, err)
	}
	now := s.now().UTC()
	queries, err := buildQueries(rec, b, now)
	if err != nil {
		return nil, err
	}
	id := newRecommendationID()
	var rows []BuildRow
	for _, q := range queries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		results, err := rec.Answer(q)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadRecommendRequest, err)
		}
		for _, r := range results {
			rows = appendBuildRows(rows, BuildRow{Model: name, Version: m.RemoteVersion, RecommendationID: id, BuiltAt: now}, r, rec)
		}
		if len(rows) > MaxBuildRows {
			return nil, fmt.Errorf("%w: the build is over %d rows; lower k", ErrBadSchedule, MaxBuildRows)
		}
	}
	return rows, nil
}

// appendBuildRows appends one row per item of r to rows, each base with the
// item's fields: its plan day when r is a visit plan, its due date for
// reorder, its customer's category, size, price tier, channel and region, a
// product's brand, subcategory
// and suggested quantity, its reasons' texts joined with "; ".
func appendBuildRows(rows []BuildRow, base BuildRow, r recommend.Result, rec *recommend.Model) []BuildRow {
	days := map[string]string{}
	for _, d := range r.Plan {
		for _, p := range d.Items {
			days[p.ID] = d.Date
		}
	}
	for _, p := range r.Items {
		texts := make([]string, len(p.Reasons))
		for i, reason := range p.Reasons {
			texts[i] = reason.Text
		}
		row := base
		row.ForID, row.ItemID, row.Rank, row.Score = r.For, p.ID, p.Rank, p.Score
		row.PlanDate, row.DueDate, row.ReasonsText = days[p.ID], p.DueDate, strings.Join(texts, "; ")
		customer := p.ID
		if !customerResults(rec.Scenario()) {
			customer = r.For
			row.Brand, row.Subcategory = rec.ProductAttrs(p.ID)
			row.SuggestedQty, _ = p.Fields["suggested_qty"].(int)
		}
		row.Segment, row.Size = rec.CustomerAttrs(customer)
		row.PriceTier, row.Channel, row.Region = rec.CustomerMarket(customer)
		rows = append(rows, row)
	}
	return rows
}

// customerResults reports whether a scenario's results are customers.
func customerResults(scenario string) bool {
	return scenario == recommend.ScenarioVisits || scenario == recommend.ScenarioReorder || scenario == recommend.ScenarioAudience
}

// WriteBuild writes rows to a build's target table in one transaction: with
// mode replace, the model's earlier rows are deleted first. driver is the
// source's type. The table's columns are BuildColumns, and each of
// buildOptional's the table has; plan_date is NULL outside a visit plan,
// due_date outside reorder, segment, size, price_tier, channel, region,
// brand and subcategory where the model does not know them, and suggested_qty where the answer suggests
// none.
func WriteBuild(ctx context.Context, db *sql.DB, driver string, t BuildTarget, model string, rows []BuildRow) error {
	canonical, ok := sqlutil.CanonicalDriver(driver)
	if !ok {
		return badSchedule("a build writes to a SQL database source, not %q", driver)
	}
	table, err := sqlutil.QuoteIdent(canonical, t.Table)
	if err != nil {
		return badSchedule("target table %q: %v", t.Table, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	have, err := columnsOf(ctx, tx, table)
	if err != nil {
		return fmt.Errorf("reading the columns of %s: %w", t.Table, err)
	}
	columns := slices.Clone(BuildColumns)
	var optional []func(BuildRow) any
	for _, o := range buildOptional {
		if slices.ContainsFunc(have, func(n string) bool { return strings.EqualFold(n, o.name) }) {
			columns = append(columns, o.name)
			optional = append(optional, o.value)
		}
	}
	cols := make([]string, len(columns))
	marks := make([]string, len(columns))
	for i, c := range columns {
		if cols[i], err = sqlutil.QuoteIdent(canonical, c); err != nil {
			return err
		}
		marks[i] = sqlutil.Placeholder(canonical, i+1)
	}
	if t.Mode == "" || t.Mode == BuildReplace {
		//nolint:gosec // G202: table and column are quoted by sqlutil.QuoteIdent; the model is a placeholder.
		del := "DELETE FROM " + table + " WHERE " + cols[0] + " = " + sqlutil.Placeholder(canonical, 1)
		if _, err := tx.ExecContext(ctx, del, model); err != nil {
			return fmt.Errorf("clearing the model's rows from %s: %w", t.Table, err)
		}
	}
	//nolint:gosec // G202: table and columns are quoted by sqlutil.QuoteIdent; values are placeholders.
	insert := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		return fmt.Errorf("writing to %s: %w", t.Table, err)
	}
	defer func() { _ = stmt.Close() }()
	for _, r := range rows {
		values := []any{r.Model, r.Version, r.RecommendationID, r.ForID, r.ItemID, r.Rank, r.Score,
			nullIfEmpty(r.PlanDate), r.ReasonsText, r.BuiltAt}
		for _, value := range optional {
			values = append(values, value(r))
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			return fmt.Errorf("writing to %s: %w", t.Table, err)
		}
	}
	return tx.Commit()
}

// nullIfEmpty is s, or NULL for "".
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// columnsOf is the quoted table's column names, from the columns a query of
// no rows names.
func columnsOf(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	//nolint:gosec // G202: table is quoted by sqlutil.QuoteIdent.
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1 = 0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	return names, rows.Err()
}

// RunBuild builds and writes b, through runner's connection to the target
// source, which must be one of the vhost's. It returns the rows written.
func (s *Service) RunBuild(ctx context.Context, runner ScheduleRunner, vhost, name string, b ModelBuild) (int, error) {
	rows, err := s.BuildRecommendations(ctx, vhost, name, b)
	if err != nil {
		return 0, err
	}
	db, driver, err := runner.OpenMLTarget(ctx, vhost, b.Target.SourceID)
	if err != nil {
		return 0, err
	}
	if err := WriteBuild(ctx, db, driver, b.Target, name, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// Scheduler runs the due dataset refreshes and model builds. Every Hermod
// serving the API runs one; a schedule's claim makes each run happen on one
// of them, once.
type Scheduler struct {
	svc    func() *Service
	runner ScheduleRunner
	logger hermod.Logger
	owner  string
	now    func() time.Time
}

// NewScheduler builds a Scheduler. svc is called on every round, as the
// Retrainer's is; runner reaches the engine's database sources. logger may
// be nil.
func NewScheduler(svc func() *Service, runner ScheduleRunner, logger hermod.Logger) *Scheduler {
	return &Scheduler{svc: svc, runner: runner, logger: logger, owner: newClaimOwner(), now: time.Now}
}

// Run looks for due schedules every ScheduleInterval until ctx ends.
func (sc *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(ScheduleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sc.Tick(ctx)
		}
	}
}

// Tick runs, one after another, every schedule that is due now.
func (sc *Scheduler) Tick(ctx context.Context) {
	svc := sc.svc()
	if svc == nil {
		return
	}
	ss, err := svc.scheduleStore()
	if err != nil {
		return
	}
	list, err := ss.ListMLSchedules(ctx)
	if err != nil {
		sc.warn("Listing the ML schedules failed", "error", err)
		return
	}
	for _, s := range list {
		if ctx.Err() != nil {
			return
		}
		if scheduleDue(s, sc.now()) {
			sc.run(ctx, svc, ss, s)
		}
	}
}

// run claims a due schedule, checks again that it is due (another replica may
// have run it meanwhile), runs it and records what it did.
func (sc *Scheduler) run(ctx context.Context, svc *Service, ss storage.MLScheduleStore, s storage.MLSchedule) {
	claimed, err := ss.ClaimMLSchedule(ctx, s.VHost, s.Kind, s.Name, sc.owner, scheduleClaim)
	if err != nil || !claimed {
		return
	}
	defer func() { _ = ss.ReleaseMLSchedule(context.WithoutCancel(ctx), s.VHost, s.Kind, s.Name, sc.owner) }()
	fresh, err := ss.GetMLSchedule(ctx, s.VHost, s.Kind, s.Name)
	if err != nil || !scheduleDue(fresh, sc.now()) {
		return
	}
	at := sc.now().UTC()
	rows, runErr := sc.runOnce(ctx, svc, fresh)
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
		sc.warn("A scheduled ML run failed", "vhost", s.VHost, "kind", s.Kind, "name", s.Name, "error", runErr)
	} else {
		sc.info("Ran an ML schedule", "vhost", s.VHost, "kind", s.Kind, "name", s.Name, "rows", rows)
	}
	if err := ss.SetMLScheduleRun(ctx, s.VHost, s.Kind, s.Name, at, rows, msg); err != nil {
		sc.warn("Recording an ML schedule's run failed", "vhost", s.VHost, "name", s.Name, "error", err)
	}
}

func (sc *Scheduler) runOnce(ctx context.Context, svc *Service, s storage.MLSchedule) (int, error) {
	if sc.runner == nil {
		return 0, errors.New("the engine is not running, so no database source can be reached")
	}
	switch s.Kind {
	case storage.MLScheduleDatasetRefresh:
		var r DatasetRefresh
		if err := json.Unmarshal(s.Spec, &r); err != nil {
			return 0, fmt.Errorf("the refresh is unreadable: %w", err)
		}
		return sc.runner.MLDatasetFromQuery(ctx, s.VHost, s.Name, r.SourceID, r.Query, r.MaxRows)
	case storage.MLScheduleModelBuild:
		var b ModelBuild
		if err := json.Unmarshal(s.Spec, &b); err != nil {
			return 0, fmt.Errorf("the build is unreadable: %w", err)
		}
		return svc.RunBuild(ctx, sc.runner, s.VHost, s.Name, b)
	}
	return 0, fmt.Errorf("unknown schedule kind %q", s.Kind)
}

func (sc *Scheduler) info(msg string, kv ...any) {
	if sc.logger != nil {
		sc.logger.Info(msg, kv...)
	}
}

func (sc *Scheduler) warn(msg string, kv ...any) {
	if sc.logger != nil {
		sc.logger.Warn(msg, kv...)
	}
}
