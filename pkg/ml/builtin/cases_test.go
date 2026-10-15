// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package builtin

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The worked cases of docs/ml-cases.md, trained on the built-in engine the
// way the guide says: the case's CSV file uploaded as a dataset, the
// pre-flight check, then a training with the guide's columns and algorithm,
// which must beat its baseline by a margin.
//
// The data is each case's committed sample (ml/examples/cases/samples). With
// HERMOD_ML_CASES_DIR set, it is read from <dir>/<case>.csv instead: the
// full-size files the generators write, which is how the guide's numbers were
// measured (see docs/ml-cases.md, "Reproducing the numbers").

// casesEnv names a directory of full-size case files.
const casesEnv = "HERMOD_ML_CASES_DIR"

// caseCSV is a case's data as an uploaded CSV file.
func caseCSV(t *testing.T, name string) []byte {
	t.Helper()
	if dir := os.Getenv(casesEnv); dir != "" {
		raw, err := os.ReadFile(filepath.Join(dir, name+".csv"))
		if err != nil {
			t.Fatalf("%s: %v", casesEnv, err)
		}
		return raw
	}
	f, err := os.Open(filepath.Join("..", "..", "..", "ml", "examples", "cases", "samples", name+".csv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fullSize reports whether the cases read full-size files, which the slower
// trainings wait for unless the tests are short.
func fullSize() bool { return os.Getenv(casesEnv) != "" }

// uploadCase uploads a case's file as the dataset of the same name.
func uploadCase(t *testing.T, c *worker.Client, name string) {
	t.Helper()
	if _, err := c.UploadFile(context.Background(), "v", name, "csv", bytes.NewReader(caseCSV(t, name))); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
}

// checkRoles runs the pre-flight check over every column and asserts the
// roles the guide says Hermod gives them.
func checkRoles(t *testing.T, c *worker.Client, dataset, target string, want map[string]string) {
	t.Helper()
	check, err := c.TrainCheck(context.Background(), "v", worker.TrainSpec{Dataset: dataset, Target: target})
	if err != nil {
		t.Fatalf("TrainCheck: %v", err)
	}
	roles := map[string]string{}
	for _, col := range check.Columns {
		roles[col.Name] = col.Role
	}
	for col, role := range want {
		if roles[col] != role {
			t.Errorf("the check gave %s the role %q, want %q", col, roles[col], role)
		}
	}
	t.Logf("check: task %s, %d rows, roles %v", check.Task, check.Rows, roles)
}

// trainCase trains a case's model and logs what it scored, for the guide.
func trainCase(t *testing.T, c *worker.Client, model string, spec worker.TrainSpec) worker.Version {
	t.Helper()
	v, err := c.Train(context.Background(), "v", model, spec)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	t.Logf("%s: %s %s on %d+%d rows\n metrics  %v\n baseline %v\n importance %v\n leaderboard %+v\n warnings %q",
		model, v.Task, v.Algorithm, v.Rows.Train, v.Rows.Test, v.Metrics, v.Baseline, v.Importance, v.Leaderboard, v.Warnings)
	return v
}

func atLeast(t *testing.T, what string, got, want float64) {
	t.Helper()
	if !(got >= want) {
		t.Errorf("%s = %.4f, want at least %.4f", what, got, want)
	}
}

func TestCaseLatePaymentBeatsItsBaseline(t *testing.T) {
	e, c := newEngine(t)
	uploadCase(t, c, "late_payment")
	checkRoles(t, c, "late_payment", "paid_late", map[string]string{
		"invoice_id": "id_like", "reminders_sent": "leak_suspect", "prior_late_share": "use", "paid_late": "target",
	})
	v := trainCase(t, c, "late_payment", worker.TrainSpec{Dataset: "late_payment", Target: "paid_late",
		Features: []string{"issue_month", "amount", "currency", "payment_terms_days", "segment", "industry", "region",
			"customer_tenure_days", "prior_invoices", "prior_late_share", "avg_days_late_prior", "credit_utilisation", "has_dispute"},
		Algorithm: "gradient_boosting"})
	if !slices.Equal(v.Labels, []any{"no", "yes"}) {
		t.Errorf("labels = %v", v.Labels)
	}
	// Always "no" is right on most invoices and finds no late one: its
	// balanced accuracy is 0.5 and its ROC AUC 0.5.
	atLeast(t, "roc_auc", v.Metrics["roc_auc"], 0.68)
	atLeast(t, "balanced_accuracy over the baseline's", v.Metrics["balanced_accuracy"]-v.Baseline["balanced_accuracy"], 0.1)
	// The customer's record decides; on the full-size file prior_late_share
	// leads, on the small sample any of the three may.
	if len(v.Importance) == 0 || !slices.Contains([]string{"prior_late_share", "avg_days_late_prior", "credit_utilisation"},
		v.Importance[0].Feature) {
		t.Errorf("importance = %v, want the customer's payment record or credit first", v.Importance)
	}
	out, err := e.Predict(context.Background(), "v", "late_payment", v.Version, []map[string]any{
		{"issue_month": 12, "amount": 18400.0, "currency": "EUR", "payment_terms_days": 60, "segment": "public_sector",
			"industry": "construction", "region": "north", "customer_tenure_days": 410, "prior_invoices": 19,
			"prior_late_share": 0.42, "avg_days_late_prior": 16.5, "credit_utilisation": 0.97, "has_dispute": true},
		{"issue_month": 4, "amount": 950.0, "currency": "EUR", "payment_terms_days": 30, "segment": "enterprise",
			"industry": "healthcare", "region": "south", "customer_tenure_days": 2900, "prior_invoices": 140,
			"prior_late_share": 0.01, "avg_days_late_prior": 2.0, "credit_utilisation": 0.31, "has_dispute": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("predictions %v", out)
	if out[0]["label"] != "yes" || out[1]["label"] != "no" {
		t.Errorf("predictions = %v, want the stretched public-sector invoice late and the reliable one not", out)
	}
}

func TestCaseTicketRoutingBeatsItsBaseline(t *testing.T) {
	e, c := newEngine(t)
	uploadCase(t, c, "ticket_routing")
	checkRoles(t, c, "ticket_routing", "team", map[string]string{
		"ticket_id": "id_like", "resolution_code": "leak_suspect", "handled_by": "leak_suspect",
		"customer_satisfaction": "mostly_empty", "team": "target",
	})
	v := trainCase(t, c, "ticket_routing", worker.TrainSpec{Dataset: "ticket_routing", Target: "team",
		Features: []string{"hour", "weekend", "channel", "customer_tier", "product_line", "has_order_ref", "order_value",
			"days_since_order", "attachments", "previous_tickets_90d", "kw_invoice", "kw_refund", "kw_tracking", "kw_broken",
			"kw_password", "kw_price", "kw_return", "kw_charged_twice"},
		Algorithm: "linear"})
	if len(v.Labels) != 6 {
		t.Errorf("labels = %v, want the six teams", v.Labels)
	}
	// The baseline sends every ticket to billing: right on about 29% of
	// them, and a balanced accuracy of one in six.
	atLeast(t, "accuracy over the baseline's", v.Metrics["accuracy"]-v.Baseline["accuracy"], 0.25)
	atLeast(t, "balanced_accuracy", v.Metrics["balanced_accuracy"], 0.55)
	out, err := e.Predict(context.Background(), "v", "ticket_routing", v.Version, []map[string]any{{
		"hour": 21, "weekend": false, "channel": "chat", "customer_tier": "standard", "product_line": "furniture",
		"has_order_ref": true, "order_value": 389.0, "days_since_order": 3, "attachments": 0, "previous_tickets_90d": 1,
		"kw_invoice": false, "kw_refund": false, "kw_tracking": true, "kw_broken": false, "kw_password": false,
		"kw_price": false, "kw_return": false, "kw_charged_twice": false,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("predictions %v", out)
	if out[0]["label"] != "shipping" {
		t.Errorf("predictions = %v, want shipping for a tracking question three days after the order", out)
	}
}

func TestCaseDeliveryTimeBeatsItsBaseline(t *testing.T) {
	e, c := newEngine(t)
	uploadCase(t, c, "delivery_time")
	checkRoles(t, c, "delivery_time", "delivery_hours", map[string]string{
		"shipment_id": "id_like", "delivery_days": "leak_suspect", "scan_events": "use", "distance_km": "use",
		"delivery_hours": "target",
	})
	v := trainCase(t, c, "delivery_time", worker.TrainSpec{Dataset: "delivery_time", Target: "delivery_hours",
		Features: []string{"placed_hour", "placed_weekday", "peak_season", "warehouse", "carrier", "service_level",
			"destination_region", "remote_area", "distance_km", "weight_kg", "parcels", "cash_on_delivery", "cross_border",
			"lane_avg_hours_30d"},
		Algorithm: "gradient_boosting"})
	if v.Task != "regression" {
		t.Fatalf("task = %s", v.Task)
	}
	// The baseline answers the mean for every parcel: an R² of about 0.
	// Parcels gone astray cap the R² any model reaches (about 0.46 on the
	// full file, less on the sample); its MAE should still drop by almost a
	// third.
	atLeast(t, "r2", v.Metrics["r2"], 0.15)
	atLeast(t, "mae saved over the baseline's", 1-v.Metrics["mae"]/v.Baseline["mae"], 0.3)
	out, err := e.Predict(context.Background(), "v", "delivery_time", v.Version, []map[string]any{
		{"placed_hour": 10, "placed_weekday": 1, "peak_season": false, "warehouse": "WH-JKT", "carrier": "kilat",
			"service_level": "express", "destination_region": "region_02", "remote_area": false, "distance_km": 140.0,
			"weight_kg": 1.2, "parcels": 1, "cash_on_delivery": false, "cross_border": false, "lane_avg_hours_30d": 12.5},
		{"placed_hour": 18, "placed_weekday": 5, "peak_season": true, "warehouse": "WH-MKS", "carrier": "cargo_one",
			"service_level": "economy", "destination_region": "region_17", "remote_area": true, "distance_km": 2300.0,
			"weight_kg": 42.0, "parcels": 3, "cash_on_delivery": true, "cross_border": false, "lane_avg_hours_30d": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("predictions %v", out)
	if fast, slow := out[0]["value"].(float64), out[1]["value"].(float64); !(fast < 48 && slow > 3*fast) {
		t.Errorf("predictions = %v, want a nearby express parcel well under two days and a remote economy one far longer", out)
	}
}

// demandRows are the weekly demand case's rows as typed values, split at
// the last holdout weeks: the rows to train on, and the later ones a
// forecast is judged on.
func demandRows(t *testing.T, holdout int) (train, later []map[string]any) {
	t.Helper()
	tb, err := table.ParseCSV(bytes.NewReader(caseCSV(t, "weekly_demand")))
	if err != nil {
		t.Fatal(err)
	}
	week := tb.Column("week_start")
	weeks := slices.Clone(week.Str)
	slices.Sort(weeks)
	weeks = slices.Compact(weeks)
	cut := weeks[len(weeks)-holdout]
	for i := range tb.Rows {
		row := map[string]any{}
		for _, col := range tb.Columns {
			row[col.Name] = col.Value(i)
		}
		if week.Str[i] < cut {
			train = append(train, row)
		} else {
			later = append(later, row)
		}
	}
	return train, later
}

func TestCaseWeeklyDemandForecastsBetterThanLastWeek(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	train, later := demandRows(t, 8)
	appendRows(t, c, "weekly_demand", train)
	checkRoles(t, c, "weekly_demand", "units_next_week", map[string]string{
		"series_id": "use", "revenue_next_week": "use", "same_week_last_year": "use", "units_next_week": "target",
	})
	features := []string{"warehouse", "category", "week_of_year_next", "holiday_next_week", "promo_next_week",
		"discount_next_week", "price_next_week", "units_last_week", "units_2_weeks_ago", "avg_units_last_4_weeks",
		"avg_units_last_13_weeks", "same_week_last_year", "stockout_last_week"}
	v := trainCase(t, c, "weekly_demand", worker.TrainSpec{Dataset: "weekly_demand", Target: "units_next_week",
		Features: features, Algorithm: "gradient_boosting"})
	atLeast(t, "r2", v.Metrics["r2"], 0.6)

	// The real test of a forecast: the weeks after the training rows, against
	// the forecasts a planner makes without a model.
	var model, lastWeek, fourWeeks float64
	for start := 0; start < len(later); start += 1000 {
		batch := later[start:min(start+1000, len(later))]
		in := make([]map[string]any, len(batch))
		for i, r := range batch {
			in[i] = map[string]any{}
			for _, f := range features {
				in[i][f] = r[f]
			}
		}
		out, err := e.Predict(ctx, "v", "weekly_demand", v.Version, in)
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range batch {
			y := r["units_next_week"].(float64)
			model += math.Abs(out[i]["value"].(float64) - y)
			lastWeek += math.Abs(r["units_last_week"].(float64) - y)
			fourWeeks += math.Abs(r["avg_units_last_4_weeks"].(float64) - y)
		}
	}
	n := float64(len(later))
	t.Logf("the last 8 weeks, %d rows: MAE model %.2f, same as last week %.2f, mean of the last 4 weeks %.2f",
		len(later), model/n, lastWeek/n, fourWeeks/n)
	atLeast(t, "MAE saved over the mean of the last 4 weeks", 1-model/fourWeeks, 0.15)
	atLeast(t, "MAE saved over last week's units", 1-model/lastWeek, 0.25)
}

func TestCaseAccountSpendNeuralNetworkBeatsItsBaseline(t *testing.T) {
	_, c := newEngine(t)
	uploadCase(t, c, "account_spend")
	checkRoles(t, c, "account_spend", "spend_next_quarter", map[string]string{
		"account_id": "id_like", "invoiced_next_quarter": "leak_suspect", "nps": "mostly_empty",
		"spend_next_quarter": "target",
	})
	features := []string{"plan", "region", "industry", "months_as_customer", "seats", "price_per_seat", "discount",
		"spend_this_quarter", "spend_growth_qoq", "active_user_share", "logins_per_user_30d", "api_calls_30d",
		"support_tickets_90d"}
	v := trainCase(t, c, "account_spend", worker.TrainSpec{Dataset: "account_spend", Target: "spend_next_quarter",
		Features: features, Algorithm: "mlp",
		Params: &worker.TrainParams{HiddenLayers: []int{128, 64, 32}, Epochs: 300, BatchSize: 256, LearningRate: 0.001, Patience: 20}})
	if v.Algorithm != "mlp" {
		t.Fatalf("algorithm = %s", v.Algorithm)
	}
	atLeast(t, "r2", v.Metrics["r2"], 0.8)
	atLeast(t, "mae saved over the baseline's", 1-v.Metrics["mae"]/v.Baseline["mae"], 0.6)
}

func TestCaseCardFraudBeatsItsBaseline(t *testing.T) {
	if !fullSize() {
		t.Skip("card fraud keeps no sample (a sample holds a handful of frauds); set " + casesEnv)
	}
	_, c := newEngine(t)
	uploadCase(t, c, "card_fraud")
	checkRoles(t, c, "card_fraud", "is_fraud", map[string]string{
		"tx_id": "id_like", "chargeback_filed": "leak_suspect", "new_device": "mostly_empty", "is_fraud": "target",
	})
	v := trainCase(t, c, "card_fraud", worker.TrainSpec{Dataset: "card_fraud", Target: "is_fraud",
		Features: []string{"hour", "amount", "merchant_group", "channel", "country", "card_country", "is_foreign",
			"card_age_days", "tx_count_last_1h", "amount_anomaly_score", "distance_from_home_km", "new_device", "cvv_failed"},
		Algorithm: "gradient_boosting"})
	// Always "no" is 99% accurate and catches no fraud.
	atLeast(t, "roc_auc", v.Metrics["roc_auc"], 0.9)
	atLeast(t, "balanced_accuracy over the baseline's", v.Metrics["balanced_accuracy"]-v.Baseline["balanced_accuracy"], 0.3)
}

// The guide's leak columns are what they say: kept in, a model looks far
// better than it can be on new rows.
func TestCaseLeakColumnsFlatterTheScore(t *testing.T) {
	_, c := newEngine(t)
	uploadCase(t, c, "late_payment")
	honest := trainCase(t, c, "honest", worker.TrainSpec{Dataset: "late_payment", Target: "paid_late",
		Features:  []string{"prior_late_share", "credit_utilisation", "has_dispute", "segment", "payment_terms_days"},
		Algorithm: "gradient_boosting"})
	leaky := trainCase(t, c, "leaky", worker.TrainSpec{Dataset: "late_payment", Target: "paid_late",
		Features: []string{"prior_late_share", "credit_utilisation", "has_dispute", "segment", "payment_terms_days",
			"days_to_pay"},
		Algorithm: "gradient_boosting"})
	if leaky.Metrics["balanced_accuracy"] < 0.98 || honest.Metrics["balanced_accuracy"] > 0.9 {
		t.Errorf("balanced accuracy with days_to_pay %.3f, without %.3f: want a near-perfect score only with the leak",
			leaky.Metrics["balanced_accuracy"], honest.Metrics["balanced_accuracy"])
	}
}
