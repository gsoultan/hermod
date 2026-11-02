// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestBranchingWorkflowUnderConcurrentLoad drives one workflow with two real
// sources, three enrichment steps and every routing shape the editor draws, at
// 300 concurrent producers, and then accounts for every record:
//
//	pg_src (CDC) ─┐
//	mq_src (AMQP)─┴─> lk_customer (db_lookup) ─┬─> api_score (api_lookup, mock HTTP) ─┐
//	                                           └─> lk_region (db_lookup) ──────────────┴─> lk_discount
//	                                                              (db_lookup on the API's answer)
//	              ├─> audit (postgres sink)                       every record
//	              └─> sw (switch on api.risk)
//	                    high    ─> mark_review ─> review (rabbitmq sink)
//	                    medium  ─> mark_medium ─┐
//	                    default ────────────────┴─> big (condition amount >= 500)
//	                                                   true  ─> mark_big ─┐
//	                                                   false ─────────────┴─> enriched (postgres sink)
//
// lk_customer splits into two parallel enrichments that merge again in
// lk_discount, which needs both halves. The fan-out after lk_discount is a
// split, the switch is a branch, and both
// the switch's medium/default edges and the condition's true/false edges merge
// again into one node. The join barrier must prune a node only when nothing
// reached it (see branch_rejoin_join_barrier memory).
//
// A record passes when it is in audit exactly once, in exactly one of review
// or enriched, never both, written once, and every enriched value equals what
// the lookups and the API should have produced for it.
//
// Env: HERMOD_INTEGRATION=1, POSTGRES_DSN (or FLOW_SOURCE_DSN/FLOW_SINK_DSN),
// RABBITMQ_URL. LOAD_USERS (default 300) and LOAD_PER_USER (default 20) size it;
// LOAD_PG_CONNS (default 40) bounds the Postgres producers' pool;
// LOAD_RESTART_AT=n stops and restarts the workflow once n records are in the
// audit table, which must lose nothing and log no error.
func TestBranchingWorkflowUnderConcurrentLoad(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	// A load test, like the others: the -short pass, which CI runs under
	// -race against every integration test at once, is not the place for it.
	if testing.Short() {
		t.Skip("load test: run without -short")
	}
	amqpURL := os.Getenv("RABBITMQ_URL")
	if amqpURL == "" {
		t.Fatal("integration: HERMOD_INTEGRATION=1 is set but RABBITMQ_URL is not")
	}
	users := envInt(t, "LOAD_USERS", 300)
	perUser := envInt(t, "LOAD_PER_USER", 20)
	pgUsers := users / 2
	mqUsers := users - pgUsers
	total := users * perUser

	srcDSN, sinkDSN := flowDSNs(t)
	srcDB := openFlowDB(t, srcDSN)
	sinkDB := openFlowDB(t, sinkDSN)
	// The producers share a bounded pool rather than holding a connection
	// each: 150 of them exceed a stock server's max_connections (100), and the
	// workflow under test needs connections of its own.
	pgConns := envInt(t, "LOAD_PG_CONNS", 40)
	srcDB.SetMaxOpenConns(pgConns)
	srcDB.SetMaxIdleConns(pgConns)

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	slot := "lt_slot_" + runID
	inQueue := "lt_orders_in_" + runID
	outQueue := "lt_review_out_" + runID
	provisionBranchingLoadFixtures(t, srcDB, sinkDB)
	t.Cleanup(func() {
		// A slot left behind pins WAL on the shared server forever.
		_, _ = srcDB.ExecContext(context.Background(),
			`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
			 WHERE slot_name = $1 AND NOT active`, slot)
	})

	mqConn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatalf("the configured RabbitMQ is not reachable: %v", err)
	}
	t.Cleanup(func() { _ = mqConn.Close() })
	adminCh, err := mqConn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	for _, q := range []string{inQueue, outQueue} {
		if _, err := adminCh.QueueDeclare(q, true, false, false, false, nil); err != nil {
			t.Fatalf("declare %s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		_, _ = adminCh.QueueDelete(inQueue, false, false, false)
		_, _ = adminCh.QueueDelete(outQueue, false, false, false)
		_ = adminCh.Close()
	})

	api := newScoringAPI()
	t.Cleanup(api.srv.Close)

	ms := &mockE2EStorage{
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
		workflows: make(map[string]storage.Workflow),
	}
	store := &logCapturingStorage{mockE2EStorage: ms}

	ms.sources["pg-src"] = storage.Source{
		ID: "pg-src", Name: "orders cdc", Type: "postgres",
		Config: map[string]string{
			"connection_string": srcDSN,
			"tables":            "lt_orders",
			"use_cdc":           "true",
			"slot_name":         slot,
			"publication_name":  "lt_pub",
		},
	}
	ms.sources["mq-src"] = storage.Source{
		ID: "mq-src", Name: "orders queue", Type: "rabbitmq_queue",
		Config: map[string]string{"connection_string": amqpURL, "queue_name": inQueue},
	}
	ms.sources["lookup-src"] = storage.Source{
		ID: "lookup-src", Name: "reference data", Type: "postgres",
		Config: map[string]string{"connection_string": srcDSN, "use_cdc": "false"},
	}

	common := []map[string]any{
		{"source_field": "id", "target_column": "order_id", "is_primary_key": true},
		{"source_field": "origin", "target_column": "origin"},
		{"source_field": "user_id", "target_column": "user_id"},
		{"source_field": "customer_id", "target_column": "customer_id"},
		{"source_field": "customer_name", "target_column": "customer_name"},
		{"source_field": "region", "target_column": "region"},
		{"source_field": "amount", "target_column": "amount"},
		{"source_field": "api.score", "target_column": "score"},
		{"source_field": "api.risk", "target_column": "risk"},
		{"source_field": "discount_pct", "target_column": "discount_pct"},
	}
	auditMap, _ := json.Marshal(common)
	enrichedMap, _ := json.Marshal(append(append([]map[string]any{}, common...),
		map[string]any{"source_field": "lane", "target_column": "lane"},
		map[string]any{"source_field": "size", "target_column": "size"},
	))
	ms.sinks["audit-sink"] = storage.Sink{
		ID: "audit-sink", Name: "audit", Type: "postgres",
		Config: map[string]string{
			"connection_string": sinkDSN, "table": "lt_audit",
			"use_existing_table": "true", "column_mappings": string(auditMap),
		},
	}
	ms.sinks["enriched-sink"] = storage.Sink{
		ID: "enriched-sink", Name: "enriched", Type: "postgres",
		Config: map[string]string{
			"connection_string": sinkDSN, "table": "lt_enriched",
			"use_existing_table": "true", "column_mappings": string(enrichedMap),
		},
	}
	ms.sinks["review-sink"] = storage.Sink{
		ID: "review-sink", Name: "review queue", Type: "rabbitmq_queue",
		Config: map[string]string{"connection_string": amqpURL, "queue_name": outQueue},
	}

	cases, _ := json.Marshal([]map[string]any{
		{"label": "high", "operator": "=", "value": "high"},
		{"label": "medium", "operator": "=", "value": "medium"},
	})
	wf := storage.Workflow{
		ID: "wf-branching-load", Name: "branching load", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "pg_src", Type: "source", RefID: "pg-src"},
			{ID: "mq_src", Type: "source", RefID: "mq-src"},
			{ID: "lk_customer", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup", "sourceId": "lookup-src", "table": "lt_customers",
				"keyColumn": "code", "keyField": "customer_id",
				"valueColumn": "name", "targetField": "customer_name", "onMiss": "fail",
			}},
			{ID: "api_score", Type: "transformation", Config: map[string]any{
				"transType": "api_lookup", "method": "POST", "url": api.srv.URL + "/score",
				"body":        `{"order":"{{.id}}","customer":"{{.customer_id}}","amount":"{{.amount}}"}`,
				"targetField": "api", "maxRetries": float64(3), "retryDelay": "20ms",
				"timeout": "5s", "onMiss": "fail",
			}},
			{ID: "lk_region", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup", "sourceId": "lookup-src", "table": "lt_customers",
				"keyColumn": "code", "keyField": "customer_id",
				"valueColumn": "region", "targetField": "region", "onMiss": "fail",
			}},
			{ID: "lk_discount", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup", "sourceId": "lookup-src", "table": "lt_risk_rules",
				"keyColumn": "risk", "keyField": "api.risk",
				"valueColumn": "discount_pct", "targetField": "discount_pct", "onMiss": "fail",
			}},
			{ID: "audit", Type: "sink", RefID: "audit-sink"},
			{ID: "sw", Type: "switch", Config: map[string]any{"field": "api.risk", "cases": string(cases)}},
			{ID: "mark_review", Type: "transformation", Config: map[string]any{
				"transType": "set", "column.review_reason": "'manual'"}},
			{ID: "review", Type: "sink", RefID: "review-sink"},
			{ID: "mark_medium", Type: "transformation", Config: map[string]any{
				"transType": "set", "column.lane": "'medium'"}},
			{ID: "big", Type: "condition", Config: map[string]any{
				"conditions": []any{map[string]any{"field": "amount", "operator": ">=", "value": "500"}},
			}},
			{ID: "mark_big", Type: "transformation", Config: map[string]any{
				"transType": "set", "column.size": "'big'"}},
			{ID: "enriched", Type: "sink", RefID: "enriched-sink"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "pg_src", TargetID: "lk_customer"},
			{ID: "e2", SourceID: "mq_src", TargetID: "lk_customer"},
			{ID: "e3", SourceID: "lk_customer", TargetID: "api_score"},
			{ID: "e4", SourceID: "api_score", TargetID: "lk_discount"},
			{ID: "e4b", SourceID: "lk_customer", TargetID: "lk_region"},
			{ID: "e4c", SourceID: "lk_region", TargetID: "lk_discount"},
			{ID: "e5", SourceID: "lk_discount", TargetID: "audit"},
			{ID: "e6", SourceID: "lk_discount", TargetID: "sw"},
			{ID: "e7", SourceID: "sw", TargetID: "mark_review", SourceHandle: "high"},
			{ID: "e8", SourceID: "mark_review", TargetID: "review"},
			// Default drawn before medium, so the merge sees both arrival orders.
			{ID: "e9", SourceID: "sw", TargetID: "big", SourceHandle: "default"},
			{ID: "e10", SourceID: "sw", TargetID: "mark_medium", SourceHandle: "medium"},
			{ID: "e11", SourceID: "mark_medium", TargetID: "big"},
			{ID: "e12", SourceID: "big", TargetID: "enriched", SourceHandle: "false"},
			{ID: "e13", SourceID: "big", TargetID: "mark_big", SourceHandle: "true"},
			{ID: "e14", SourceID: "mark_big", TargetID: "enriched"},
		},
		MaxRetries:    5,
		RetryInterval: "200ms",
	}
	ms.workflows[wf.ID] = wf

	reg := NewRegistry(store)
	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	// The CDC source creates its slot on start; a row inserted before that is
	// not captured and would read as loss.
	waitUntil(t, 60*time.Second, "the replication slot to be active", func() bool {
		var active bool
		err := srcDB.QueryRowContext(t.Context(),
			`SELECT active FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&active)
		return err == nil && active
	})

	// Drain the review queue as it fills, the way a real consumer would.
	reviewed := newReviewCollector()
	consumeCh, err := mqConn.Channel()
	if err != nil {
		t.Fatalf("consumer channel: %v", err)
	}
	deliveries, err := consumeCh.Consume(outQueue, "lt-verify", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume %s: %v", outQueue, err)
	}
	go reviewed.drain(deliveries)

	expect := make(map[string]loadOrder, total)
	var expectMu sync.Mutex
	var produceErrs atomic.Int64
	var firstProduceErr atomic.Value

	start := time.Now()
	var wg sync.WaitGroup
	for u := range users {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(u), 42))
			var ch *amqp.Channel
			if u >= pgUsers {
				c, err := mqConn.Channel()
				if err != nil {
					produceErrs.Add(1)
					firstProduceErr.CompareAndSwap(nil, err.Error())
					return
				}
				defer c.Close()
				ch = c
			}
			for i := range perUser {
				o := loadOrder{
					ID:         fmt.Sprintf("U%03d-%03d", u, i),
					UserID:     u,
					CustomerID: fmt.Sprintf("C-%02d", rng.IntN(50)),
					// Cents, so the comparison and the round trip are exact.
					AmountCents: 1000 + rng.Int64N(99000),
				}
				var err error
				if ch == nil {
					o.Origin = "pg"
					_, err = srcDB.ExecContext(t.Context(),
						`INSERT INTO lt_orders (id, origin, user_id, customer_id, amount) VALUES ($1,'pg',$2,$3,$4)`,
						o.ID, o.UserID, o.CustomerID, o.amountText())
				} else {
					o.Origin = "mq"
					body := fmt.Sprintf(`{"id":%q,"origin":"mq","user_id":%d,"customer_id":%q,"amount":%s}`,
						o.ID, o.UserID, o.CustomerID, o.amountText())
					err = ch.PublishWithContext(t.Context(), "", inQueue, false, false, amqp.Publishing{
						ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: []byte(body),
					})
				}
				if err != nil {
					produceErrs.Add(1)
					firstProduceErr.CompareAndSwap(nil, err.Error())
					continue
				}
				expectMu.Lock()
				expect[o.ID] = o
				expectMu.Unlock()
			}
		})
	}
	wg.Wait()
	produced := time.Since(start)
	if n := produceErrs.Load(); n != 0 {
		t.Fatalf("PRODUCER: %d of %d records could not be produced; first: %v", n, total, firstProduceErr.Load())
	}
	t.Logf("produced %d records from %d concurrent producers (%d postgres, %d rabbitmq) in %s",
		total, users, pgUsers, mqUsers, produced.Round(time.Millisecond))

	wantReview := 0
	for _, o := range expect {
		if scoreFor(o.ID).risk == "high" {
			wantReview++
		}
	}
	wantEnriched := total - wantReview

	counts := func() (audit, enriched int) {
		_ = sinkDB.QueryRowContext(t.Context(), `SELECT count(*) FROM lt_audit`).Scan(&audit)
		_ = sinkDB.QueryRowContext(t.Context(), `SELECT count(*) FROM lt_enriched`).Scan(&enriched)
		return audit, enriched
	}
	deadline := time.Now().Add(envDuration(t, "LOAD_DRAIN_TIMEOUT", 5*time.Minute))
	lastLog := time.Now()
	restartAt := envInt(t, "LOAD_RESTART_AT", total+1)
	restarted := false
	for time.Now().Before(deadline) {
		a, e := counts()
		if !restarted && a >= restartAt {
			restarted = true
			t.Logf("restarting the workflow with audit at %d/%d", a, total)
			if err := reg.StopEngine(t.Context(), wf.ID); err != nil {
				t.Fatalf("stop: %v", err)
			}
			if err := reg.StartWorkflow(wf.ID, wf); err != nil {
				t.Fatalf("restart: %v", err)
			}
		}
		if a >= total && e >= wantEnriched && reviewed.distinct() >= wantReview {
			break
		}
		if time.Since(lastLog) > 10*time.Second {
			t.Logf("draining: audit %d/%d, enriched %d/%d, review %d/%d",
				a, total, e, wantEnriched, reviewed.distinct(), wantReview)
			lastLog = time.Now()
		}
		time.Sleep(250 * time.Millisecond)
	}
	drained := time.Since(start)
	// Long enough for a late duplicate or a redelivery to show up.
	time.Sleep(3 * time.Second)

	audit := readLoadRows(t, sinkDB, "lt_audit")
	enriched := readLoadRows(t, sinkDB, "lt_enriched")
	writes := readWriteCounts(t, sinkDB)
	review := reviewed.snapshot()

	t.Logf("end to end %s (%.0f records/s); audit %d/%d, enriched %d/%d, review %d/%d (%d deliveries)",
		drained.Round(time.Millisecond), float64(total)/drained.Seconds(),
		len(audit), total, len(enriched), wantEnriched, len(review), wantReview, reviewed.deliveries())
	t.Logf("scoring API: %d requests, %d refused on purpose, %d malformed",
		api.requests.Load(), api.refused.Load(), api.malformed.Load())

	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	// Delivery is at-least-once. Without a restart nothing should be delivered
	// twice, so a repeat is a defect. Across a restart, a change acknowledged
	// out of order behind one that was still in flight is replayed, so repeats
	// are expected there and counted rather than failed. Loss and wrong values
	// fail either way.
	var repeats int
	repeat := func(format string, args ...any) {
		repeats++
		if !restarted {
			report(format, args...)
		}
	}

	ids := make([]string, 0, len(expect))
	for id := range expect {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := expect[id]
		sc := scoreFor(id)
		want := loadRow{
			Origin: o.Origin, UserID: o.UserID, CustomerID: o.CustomerID,
			CustomerName: customerName(o.CustomerID), Region: regionFor(o.CustomerID), AmountCents: o.AmountCents,
			Score: sc.score, Risk: sc.risk, Discount: discountFor(sc.risk),
		}

		a, inAudit := audit[id]
		if !inAudit {
			report("%s (%s): missing from audit", id, o.Origin)
		} else if d := a.diff(want); d != "" {
			report("%s (%s): audit row is wrong: %s", id, o.Origin, d)
		}

		e, inEnriched := enriched[id]
		r, inReview := review[id]
		switch sc.risk {
		case "high":
			if inEnriched {
				report("%s (%s): high risk but written to enriched", id, o.Origin)
			}
			if !inReview {
				report("%s (%s): high risk but never reached the review queue", id, o.Origin)
			} else {
				if r.n != 1 {
					repeat("%s (%s): reached the review queue %d times", id, o.Origin, r.n)
				}
				if d := r.row.diff(want); d != "" {
					report("%s (%s): review message is wrong: %s", id, o.Origin, d)
				}
				if r.reason != "manual" {
					report("%s (%s): review message has review_reason %q", id, o.Origin, r.reason)
				}
			}
		default:
			if inReview {
				report("%s (%s): %s risk but sent to the review queue", id, o.Origin, sc.risk)
			}
			if !inEnriched {
				report("%s (%s): %s risk, never written to enriched", id, o.Origin, sc.risk)
				break
			}
			if d := e.diff(want); d != "" {
				report("%s (%s): enriched row is wrong: %s", id, o.Origin, d)
			}
			wantLane := ""
			if sc.risk == "medium" {
				wantLane = "medium"
			}
			wantSize := ""
			if o.AmountCents >= 50000 {
				wantSize = "big"
			}
			if e.Lane != wantLane {
				report("%s (%s): lane %q, want %q", id, o.Origin, e.Lane, wantLane)
			}
			if e.Size != wantSize {
				report("%s (%s): size %q, want %q (amount %s)", id, o.Origin, e.Size, wantSize, o.amountText())
			}
		}
		for _, tbl := range []string{"lt_audit", "lt_enriched"} {
			if n := writes[tbl+"/"+id]; n > 1 {
				repeat("%s (%s): written to %s %d times", id, o.Origin, tbl, n)
			}
		}
	}
	for id := range audit {
		if _, ok := expect[id]; !ok {
			report("%s: in audit but never produced", id)
		}
	}
	for id := range review {
		if _, ok := expect[id]; !ok {
			report("%s: in the review queue but never produced", id)
		}
	}

	if restarted {
		t.Logf("across the restart, %d record deliveries were repeated (at-least-once)", repeats)
	}

	logs := store.problems()
	for _, l := range logs {
		report("hermod logged %s: %s %s", l.Level, l.Message, l.Data)
	}

	if len(problems) > 0 {
		const show = 40
		for i, p := range problems {
			if i == show {
				t.Errorf("... and %d more", len(problems)-show)
				break
			}
			t.Error(p)
		}
		t.Fatalf("%d problem(s) across %d records", len(problems), total)
	}
}

// loadOrder is one produced record and everything needed to predict its fate.
type loadOrder struct {
	ID          string
	Origin      string
	UserID      int
	CustomerID  string
	AmountCents int64
}

func (o loadOrder) amountText() string {
	return fmt.Sprintf("%d.%02d", o.AmountCents/100, o.AmountCents%100)
}

type apiScore struct {
	score int
	risk  string
}

// scoreFor is the mock API's answer, deterministic per order id so the test can
// predict it without asking the server.
func scoreFor(id string) apiScore {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	s := int(h.Sum32() % 100)
	switch {
	case s < 40:
		return apiScore{s, "low"}
	case s < 80:
		return apiScore{s, "medium"}
	default:
		return apiScore{s, "high"}
	}
}

func customerName(code string) string { return "Customer " + strings.TrimPrefix(code, "C-") }

func regionFor(code string) string {
	n, _ := strconv.Atoi(strings.TrimPrefix(code, "C-"))
	return [...]string{"north", "south", "east", "west"}[n%4]
}

func discountFor(risk string) string {
	switch risk {
	case "low":
		return "2.50"
	case "medium":
		return "5.00"
	}
	return "15.00"
}

type scoringAPI struct {
	srv       *httptest.Server
	requests  atomic.Int64
	refused   atomic.Int64
	malformed atomic.Int64
	mu        sync.Mutex
	refusedID map[string]bool
}

// newScoringAPI answers POST /score with {"score":n,"risk":r}. It refuses the
// first attempt for one order in fifty with a 503, so the api_lookup retry path
// carries real traffic, and it checks the request is the one the template
// should have produced.
func newScoringAPI() *scoringAPI {
	a := &scoringAPI{refusedID: map[string]bool{}}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Order, Customer, Amount string
		}
		if r.Method != http.MethodPost || r.URL.Path != "/score" ||
			r.Header.Get("Content-Type") != "application/json" ||
			json.Unmarshal(raw, &req) != nil || req.Order == "" ||
			!strings.HasPrefix(req.Customer, "C-") || req.Amount == "" {
			a.malformed.Add(1)
			http.Error(w, "malformed: "+string(raw), http.StatusBadRequest)
			return
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(req.Order))
		if h.Sum32()%50 == 7 {
			a.mu.Lock()
			first := !a.refusedID[req.Order]
			a.refusedID[req.Order] = true
			a.mu.Unlock()
			if first {
				a.refused.Add(1)
				http.Error(w, "try again", http.StatusServiceUnavailable)
				return
			}
		}
		// A little latency, so concurrent requests overlap the way they would.
		time.Sleep(time.Duration(1+h.Sum32()%4) * time.Millisecond)
		sc := scoreFor(req.Order)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"score": sc.score, "risk": sc.risk})
	}))
	return a
}

// loadRow is what a sink holds for one record, normalised for comparison.
type loadRow struct {
	Origin       string
	UserID       int
	CustomerID   string
	CustomerName string
	Region       string
	AmountCents  int64
	Score        int
	Risk         string
	Discount     string
	Lane, Size   string
}

func (r loadRow) diff(w loadRow) string {
	var d []string
	add := func(name string, got, want any) {
		if got != want {
			d = append(d, fmt.Sprintf("%s=%v want %v", name, got, want))
		}
	}
	add("origin", r.Origin, w.Origin)
	add("user_id", r.UserID, w.UserID)
	add("customer_id", r.CustomerID, w.CustomerID)
	add("customer_name", r.CustomerName, w.CustomerName)
	add("region", r.Region, w.Region)
	add("amount_cents", r.AmountCents, w.AmountCents)
	add("score", r.Score, w.Score)
	add("risk", r.Risk, w.Risk)
	add("discount_pct", r.Discount, w.Discount)
	return strings.Join(d, ", ")
}

func readLoadRows(t *testing.T, db *sql.DB, table string) map[string]loadRow {
	t.Helper()
	extra := "'' , ''"
	if table == "lt_enriched" {
		extra = "coalesce(lane,''), coalesce(size,'')"
	}
	rows, err := db.QueryContext(t.Context(), fmt.Sprintf(`SELECT order_id, coalesce(origin,''),
		coalesce(user_id,-1), coalesce(customer_id,''), coalesce(customer_name,''), coalesce(region,''),
		coalesce((amount*100)::bigint,-1), coalesce(score,-1), coalesce(risk,''),
		coalesce(to_char(discount_pct,'FM990.00'),''), %s FROM %s`, extra, table))
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()
	out := map[string]loadRow{}
	for rows.Next() {
		var id string
		var r loadRow
		if err := rows.Scan(&id, &r.Origin, &r.UserID, &r.CustomerID, &r.CustomerName, &r.Region,
			&r.AmountCents, &r.Score, &r.Risk, &r.Discount, &r.Lane, &r.Size); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	return out
}

// readWriteCounts reads how many times the sinks wrote each row. The tables
// have primary keys, so an upsert hides a duplicate delivery from a plain
// count; the trigger behind lt_writes does not.
func readWriteCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT tbl, order_id, count(*) FROM lt_writes GROUP BY tbl, order_id`)
	if err != nil {
		t.Fatalf("read lt_writes: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var tbl, id string
		var n int
		if err := rows.Scan(&tbl, &id, &n); err != nil {
			t.Fatalf("scan lt_writes: %v", err)
		}
		out[tbl+"/"+id] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read lt_writes: %v", err)
	}
	return out
}

type reviewEntry struct {
	row    loadRow
	reason string
	n      int
}

type reviewCollector struct {
	mu    sync.Mutex
	byID  map[string]*reviewEntry
	total int
	bad   []string
}

func newReviewCollector() *reviewCollector { return &reviewCollector{byID: map[string]*reviewEntry{}} }

func (c *reviewCollector) drain(ds <-chan amqp.Delivery) {
	for d := range ds {
		var m map[string]any
		c.mu.Lock()
		c.total++
		if err := json.Unmarshal(d.Body, &m); err != nil {
			c.bad = append(c.bad, string(d.Body))
			c.mu.Unlock()
			continue
		}
		id := fmt.Sprint(m["id"])
		e := c.byID[id]
		if e == nil {
			e = &reviewEntry{row: reviewRow(m), reason: fmt.Sprint(m["review_reason"])}
			c.byID[id] = e
		}
		e.n++
		c.mu.Unlock()
	}
}

// reviewRow reads a review message into the row shape. Values arrive as JSON,
// so numbers may be numbers or numeric text depending on the source.
func reviewRow(m map[string]any) loadRow {
	api, _ := m["api"].(map[string]any)
	num := func(v any) float64 {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			f, _ := strconv.ParseFloat(x, 64)
			return f
		}
		return -1
	}
	disc := num(m["discount_pct"])
	return loadRow{
		Origin:       fmt.Sprint(m["origin"]),
		UserID:       int(num(m["user_id"])),
		CustomerID:   fmt.Sprint(m["customer_id"]),
		CustomerName: fmt.Sprint(m["customer_name"]),
		Region:       fmt.Sprint(m["region"]),
		AmountCents:  int64(num(m["amount"])*100 + 0.5),
		Score:        int(num(api["score"])),
		Risk:         fmt.Sprint(api["risk"]),
		Discount:     strconv.FormatFloat(disc, 'f', 2, 64),
	}
}

func (c *reviewCollector) distinct() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byID)
}

func (c *reviewCollector) deliveries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func (c *reviewCollector) snapshot() map[string]reviewEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]reviewEntry, len(c.byID))
	for k, v := range c.byID {
		out[k] = *v
	}
	if len(c.bad) > 0 {
		out["<unparseable>"] = reviewEntry{n: len(c.bad), reason: c.bad[0]}
	}
	return out
}

// logCapturingStorage keeps every WARN and ERROR line Hermod writes, so the
// test fails on an error it logged and carried on from.
type logCapturingStorage struct {
	*mockE2EStorage
	mu   sync.Mutex
	logs []storage.Log
}

func (s *logCapturingStorage) CreateLog(_ context.Context, l storage.Log) error {
	s.keep(l)
	return nil
}

func (s *logCapturingStorage) CreateLogs(_ context.Context, ls []storage.Log) error {
	for _, l := range ls {
		s.keep(l)
	}
	return nil
}

func (s *logCapturingStorage) keep(l storage.Log) {
	switch strings.ToUpper(l.Level) {
	case "WARN", "WARNING", "ERROR", "FATAL":
		s.mu.Lock()
		s.logs = append(s.logs, l)
		s.mu.Unlock()
	}
}

func (s *logCapturingStorage) problems() []storage.Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.Log(nil), s.logs...)
}

func provisionBranchingLoadFixtures(t *testing.T, srcDB, sinkDB *sql.DB) {
	t.Helper()
	var walLevel string
	if err := srcDB.QueryRowContext(t.Context(), "SHOW wal_level").Scan(&walLevel); err != nil {
		t.Fatalf("read wal_level: %v", err)
	}
	if walLevel != "logical" {
		t.Fatalf("integration: source PostgreSQL has wal_level=%q, CDC needs \"logical\"", walLevel)
	}

	mustExec(t, srcDB, `DROP TABLE IF EXISTS lt_orders`)
	mustExec(t, srcDB, `CREATE TABLE lt_orders (
		id TEXT PRIMARY KEY, origin TEXT NOT NULL, user_id INTEGER NOT NULL,
		customer_id TEXT NOT NULL, amount TEXT NOT NULL)`)
	var havePub bool
	if err := srcDB.QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'lt_pub')`).Scan(&havePub); err != nil {
		t.Fatalf("check publication: %v", err)
	}
	if !havePub {
		mustExec(t, srcDB, `CREATE PUBLICATION lt_pub FOR TABLE lt_orders`)
	}

	mustExec(t, srcDB, `DROP TABLE IF EXISTS lt_customers`)
	mustExec(t, srcDB, `CREATE TABLE lt_customers (code TEXT PRIMARY KEY, name TEXT NOT NULL, region TEXT NOT NULL)`)
	mustExec(t, srcDB, `INSERT INTO lt_customers (code, name, region)
		SELECT 'C-' || lpad(g::text, 2, '0'), 'Customer ' || lpad(g::text, 2, '0'),
		       (ARRAY['north','south','east','west'])[g % 4 + 1]
		FROM generate_series(0, 49) g`)
	mustExec(t, srcDB, `CREATE TABLE IF NOT EXISTS lt_risk_rules (risk TEXT PRIMARY KEY, discount_pct NUMERIC NOT NULL)`)
	mustExec(t, srcDB, `INSERT INTO lt_risk_rules VALUES ('low', 2.5), ('medium', 5), ('high', 15)
		ON CONFLICT (risk) DO UPDATE SET discount_pct = EXCLUDED.discount_pct`)

	cols := `order_id TEXT PRIMARY KEY, origin TEXT, user_id INTEGER, customer_id TEXT,
		customer_name TEXT, region TEXT, amount NUMERIC(12,2), score INTEGER, risk TEXT, discount_pct NUMERIC`
	mustExec(t, sinkDB, `DROP TABLE IF EXISTS lt_audit, lt_enriched, lt_writes`)
	mustExec(t, sinkDB, `CREATE TABLE lt_audit (`+cols+`)`)
	mustExec(t, sinkDB, `CREATE TABLE lt_enriched (`+cols+`, lane TEXT, size TEXT)`)
	mustExec(t, sinkDB, `CREATE TABLE lt_writes (tbl TEXT NOT NULL, order_id TEXT NOT NULL, at TIMESTAMPTZ DEFAULT now())`)
	mustExec(t, sinkDB, `CREATE OR REPLACE FUNCTION lt_count_write() RETURNS trigger AS $$
		BEGIN INSERT INTO lt_writes (tbl, order_id) VALUES (TG_TABLE_NAME, NEW.order_id); RETURN NEW; END
		$$ LANGUAGE plpgsql`)
	mustExec(t, sinkDB, `CREATE TRIGGER lt_audit_w AFTER INSERT OR UPDATE ON lt_audit FOR EACH ROW EXECUTE FUNCTION lt_count_write()`)
	mustExec(t, sinkDB, `CREATE TRIGGER lt_enriched_w AFTER INSERT OR UPDATE ON lt_enriched FOR EACH ROW EXECUTE FUNCTION lt_count_write()`)
}

func envInt(t *testing.T, key string, def int) int {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q is not a positive whole number", key, v)
	}
	return n
}

func envDuration(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s=%q is not a duration", key, v)
	}
	return d
}
