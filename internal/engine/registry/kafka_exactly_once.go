// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// checkKafkaExactlyOnce refuses a workflow that reads a Kafka source, writes a
// Kafka sink with a transactional ID, and also writes some other sink.
//
// The transactional sink commits the source group's offsets inside its own
// transaction (see pkg/comm/sink/kafka), so once it commits, the input is
// consumed for the whole workflow. Any other sink that has not written yet,
// or fails, is skipped for good after a restart. Exactly-once is only true
// when the Kafka sink is the only one.
func (r *Registry) checkKafkaExactlyOnce(ctx context.Context, wf storage.Workflow) error {
	if r.store() == nil {
		return nil
	}
	sinks := reachableSinkNodes(wf)
	if len(sinks) < 2 {
		return nil
	}
	eos, txnID := -1, ""
	for i, node := range sinks {
		// A sink that cannot be read already fails checkNodeRefs.
		snk, err := r.GetSinkConfig(ctx, node.RefID)
		if err != nil || snk.Type != "kafka" {
			continue
		}
		// The factory trims the ID the same way before it turns transactions on.
		if id := strings.TrimSpace(snk.Config["transactional_id"]); id != "" {
			eos, txnID = i, id
			break
		}
	}
	if eos < 0 || !r.readsKafka(ctx, wf) {
		return nil
	}
	other := sinks[0]
	if eos == 0 {
		other = sinks[1]
	}
	return fmt.Errorf("sink node %s writes exactly-once under transactional ID %q, which commits the Kafka "+
		"source's offsets together with its own output, so sink node %s would not get those records again "+
		"if it failed or the workflow stopped before it wrote them; exactly-once needs Kafka to be the "+
		"workflow's only sink", r.getNodeName(sinks[eos]), txnID, r.getNodeName(other))
}

// readsKafka reports whether any source node of the workflow is a Kafka
// source. Without one the transaction carries no offsets, and a second sink
// loses nothing to it.
func (r *Registry) readsKafka(ctx context.Context, wf storage.Workflow) bool {
	for _, node := range wf.Nodes {
		if node.Type != "source" {
			continue
		}
		if src, err := r.GetSourceConfig(ctx, node.RefID); err == nil && src.Type == "kafka" {
			return true
		}
	}
	return false
}

// CheckKafkaExactlyOnce is checkKafkaExactlyOnce for the workflow API's
// validator, so the editor refuses on save what starting would refuse. A nil
// registry has no sinks to read and refuses nothing.
func (r *Registry) CheckKafkaExactlyOnce(ctx context.Context, wf storage.Workflow) error {
	if r == nil {
		return nil
	}
	return r.checkKafkaExactlyOnce(ctx, wf)
}
