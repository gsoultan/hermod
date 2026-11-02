// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DeadLetterGroupKey is what makes failures one row on the Dead Letters page:
// the same record failing at the same node or sink of the same workflow.
type DeadLetterGroupKey struct {
	WorkflowID string
	NodeID     string
	SinkID     string
	RecordKey  string
}

// GroupKey is the group d belongs to.
func (d DeadLetter) GroupKey() DeadLetterGroupKey {
	return DeadLetterGroupKey{WorkflowID: d.WorkflowID, NodeID: d.NodeID, SinkID: d.SinkID, RecordKey: d.RecordKey}
}

// Attempts is how many failed attempts d stands for: at least one, since a
// row from before counting has no count.
func (d DeadLetter) Attempts() int { return max(d.FailureCount, 1) }

// DeadLetterRecordKey says which record a failed message carried, so that the
// same record failing again is counted rather than kept twice.
//
// With fields, it is the values at those dot paths in the payload, for
// payloads that also carry something that changes on every read, such as a
// timestamp or a signed link. Without them, or when none of them is in the
// payload, it is a fingerprint of the payload itself, written canonically so
// key order and whitespace do not matter, together with the operation, schema
// and table the message described.
func DeadLetterRecordKey(payload []byte, origin DeadLetterOrigin, fields []string) string {
	var doc any
	isJSON := json.Unmarshal(payload, &doc) == nil
	if isJSON && len(fields) > 0 {
		var b strings.Builder
		found := false
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			v, ok := valueAtPath(doc, f)
			if ok {
				found = true
			}
			raw, _ := json.Marshal(v)
			b.WriteString(f)
			b.WriteByte('=')
			b.Write(raw)
			b.WriteByte(0)
		}
		if found {
			return "fields:" + digest([]byte(b.String()))
		}
	}
	canonical := payload
	if isJSON {
		// Decoding into any and encoding again sorts every object's keys and
		// drops whitespace. Numbers go through float64, which can merge two
		// integers past 2^53 that differ only in the last digits; for a key
		// that only decides what counts as "the same record again", that is
		// an acceptable price for not needing a canonical JSON library.
		if raw, err := json.Marshal(doc); err == nil {
			canonical = raw
		}
	}
	var b bytes.Buffer
	for _, part := range []string{origin.Operation, origin.Schema, origin.Table} {
		b.WriteString(part)
		b.WriteByte(0)
	}
	b.Write(canonical)
	return "payload:" + digest(b.Bytes())
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// valueAtPath follows a dot path through decoded JSON objects.
func valueAtPath(doc any, path string) (any, bool) {
	cur := doc
	for part := range strings.SplitSeq(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// maxFailureDetail bounds the error a folded history line repeats.
const maxFailureDetail = 300

// foldFailure counts next into group: the newest attempt's message, error and
// time become the group's, and the failure joins the history, folded into the
// last line when it repeats it.
func foldFailure(group, next DeadLetter) DeadLetter {
	out := group
	out.FailureCount = group.Attempts() + next.Attempts()
	first := group.FirstFailedAt
	if first.IsZero() {
		first = group.FailedAt
	}
	nextFirst := next.FirstFailedAt
	if nextFirst.IsZero() {
		nextFirst = next.FailedAt
	}
	if nextFirst.Before(first) {
		first = nextFirst
	}
	out.FirstFailedAt = first
	if !next.FailedAt.Before(group.FailedAt) {
		out.MessageID, out.Error, out.FailedAt = next.MessageID, next.Error, next.FailedAt
		out.Payload, out.PayloadSize, out.Truncated = next.Payload, next.PayloadSize, next.Truncated
		out.Metadata, out.Origin = next.Metadata, next.Origin
	}
	detail := next.Error
	if len(detail) > maxFailureDetail {
		detail = detail[:maxFailureDetail]
	}
	out.History = foldFailureEvent(group.History, next.FailedAt.UTC(), next.Attempts(), detail)
	return out
}

// foldFailureEvent adds n failures at "at" to h, as one line with the last
// line when that line is the same failure.
func foldFailureEvent(h []DeadLetterEvent, at time.Time, n int, detail string) []DeadLetterEvent {
	if len(h) > 0 {
		last := h[len(h)-1]
		if last.Action == "failed_again" && last.Detail == detail {
			out := append([]DeadLetterEvent{}, h...)
			last.Count += n
			if last.Until == nil || at.After(*last.Until) {
				last.Until = &at
			}
			out[len(out)-1] = last
			return out
		}
	}
	return AppendDeadLetterEvent(h, DeadLetterEvent{At: at, Action: "failed_again", Detail: detail, Count: n, Until: &at})
}

// keepRetries bounds how often KeepDeadLetter re-reads a group another writer
// changed under it before it keeps the failure as a row of its own.
const keepRetries = 5

// KeepDeadLetter keeps d, counting it into the waiting group of its record
// when there is one, and returns the id of the row that holds it. The record
// key is worked out from fields when d has none.
//
// Two writers folding into one group at once are told apart by the count:
// the loser reads the group again. A group that keeps changing does not cost
// the failure; after a few tries it is kept as a row of its own, which is the
// behaviour before grouping and loses nothing.
func KeepDeadLetter(ctx context.Context, ds DeadLetterStore, d DeadLetter, fields []string) (string, error) {
	if err := ValidateDeadLetter(CapDeadLetter(d)); err != nil {
		return "", err
	}
	if d.RecordKey == "" {
		d.RecordKey = DeadLetterRecordKey(d.Payload, d.Origin, fields)
	}
	for range keepRetries {
		group, err := ds.FindDeadLetterGroup(ctx, d.GroupKey())
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("finding the group of dead letter %s: %w", d.ID, err)
		}
		err = ds.UpdateDeadLetter(ctx, foldFailure(group, d), group.Attempts())
		if err == nil {
			return group.ID, nil
		}
		if !errors.Is(err, ErrDeadLetterConflict) {
			return "", fmt.Errorf("counting dead letter %s into %s: %w", d.ID, group.ID, err)
		}
	}
	if err := ds.PutDeadLetter(ctx, d); err != nil {
		return "", err
	}
	return d.ID, nil
}

// groupingPage is how many ungrouped rows one pass reads at a time.
const groupingPage = 200

// GroupUngroupedDeadLetters gives every waiting row an earlier release kept
// its record key, and folds copies of one record into one group: the oldest
// row becomes the group, and each later copy is counted into it and marked
// merged. Merged rows are purged with the settled ones, so until retention
// runs the step can be looked into. It returns how many rows it merged, and
// does nothing once every waiting row has a key.
func GroupUngroupedDeadLetters(ctx context.Context, ds DeadLetterStore) (int, error) {
	merged := 0
	for {
		page, _, err := ds.ListDeadLetters(ctx, DeadLetterFilter{
			Status: DeadLetterQueued, Ungrouped: true, OldestFirst: true, Limit: groupingPage,
		})
		if err != nil {
			return merged, fmt.Errorf("listing ungrouped dead letters: %w", err)
		}
		if len(page) == 0 {
			return merged, nil
		}
		progressed := false
		for _, row := range page {
			done, wasMerged, err := groupOne(ctx, ds, row.ID)
			if err != nil {
				return merged, err
			}
			if done {
				progressed = true
			}
			if wasMerged {
				merged++
			}
		}
		if !progressed {
			// Every row on the page lost a race; the next pass sees them again.
			return merged, nil
		}
	}
}

// groupOne gives one ungrouped row its key, merging it into its group when the
// record already has one. done is false when the row changed under it.
//
// The write is guarded by the attempt count, not the history, so an event a
// replay notes on the same legacy row between the read and the write here is
// overwritten. That window is the first sweep after an upgrade, on rows that
// predate it, and costs a history line, never the dead letter.
func groupOne(ctx context.Context, ds DeadLetterStore, id string) (done, merged bool, err error) {
	row, err := ds.GetDeadLetter(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading dead letter %s: %w", id, err)
	}
	if row.Status != DeadLetterQueued || row.RecordKey != "" {
		return true, false, nil
	}
	row.RecordKey = DeadLetterRecordKey(row.Payload, row.Origin, nil)
	group, err := ds.FindDeadLetterGroup(ctx, row.GroupKey())
	switch {
	case errors.Is(err, ErrNotFound):
		err = ds.UpdateDeadLetter(ctx, row, row.Attempts())
		if errors.Is(err, ErrDeadLetterConflict) {
			return false, false, nil
		}
		return err == nil, false, err
	case err != nil:
		return false, false, fmt.Errorf("finding the group of dead letter %s: %w", id, err)
	}
	if err := ds.UpdateDeadLetter(ctx, foldFailure(group, row), group.Attempts()); err != nil {
		if errors.Is(err, ErrDeadLetterConflict) {
			return false, false, nil
		}
		return false, false, err
	}
	err = ds.SettleDeadLetter(ctx, row.ID, DeadLetterMerged, NewDeadLetterEvent("merged", "", group.ID))
	if err != nil && !errors.Is(err, ErrDeadLetterSettled) {
		return false, false, fmt.Errorf("marking dead letter %s merged: %w", id, err)
	}
	return true, true, nil
}
