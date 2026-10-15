// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package lineage derives a data-lineage graph from what is stored: the
// workflows of a vhost, the sources and sinks their nodes name, and the ML
// models the vhost registers. Nothing is inferred from names or guessed: an
// asset is identified only by the configuration keys the engine itself reads
// to reach it (internal/factory, the transformers in pkg/comm/transformer, the
// node executors in internal/engine/registry/nodes).
//
// Nodes are data assets (a table, a topic, an index, a file, an endpoint, an
// ML model or dataset) and workflows. Edges point the way data flows: an asset
// a workflow reads points at the workflow, and the workflow points at every
// asset it writes. Two workflows are therefore connected when one writes an
// asset the other reads, even when each names its own source or sink record.
//
// The package is the domain model and its two use cases, Derive and
// Downstream. It does no I/O; internal/lineage/repository loads its Inputs
// and internal/lineage/service scopes them to a caller.
package lineage

import "github.com/gsoultan/hermod/internal/storage"

// Node kinds. KindConnector is an asset whose physical identity cannot be
// derived from its configuration (a cron trigger, an SMTP sink, a type this
// package does not know): it is identified by its source or sink record
// instead, and Node.Resolved is false.
const (
	KindWorkflow   = "workflow"
	KindTable      = "table"
	KindDatabase   = "database"
	KindCollection = "collection"
	KindTopic      = "topic"
	KindSubject    = "subject"
	KindStream     = "stream"
	KindQueue      = "queue"
	KindIndex      = "index"
	KindObject     = "object"
	KindFile       = "file"
	KindEndpoint   = "endpoint"
	KindSheet      = "sheet"
	KindEntity     = "entity"
	KindProcess    = "process"
	KindChannel    = "channel"
	KindDataset    = "dataset"
	KindModel      = "model"
	KindConnector  = "connector"
)

// Edge relations. Every edge points the way data flows.
const (
	RelReads     = "reads"     // asset -> workflow
	RelLooksUp   = "looks_up"  // asset -> workflow, per message enrichment
	RelRetrieves = "retrieves" // vector index -> workflow
	RelPredicts  = "predicts"  // model -> workflow
	RelWrites    = "writes"    // workflow -> asset
	RelTrains    = "trains"    // workflow -> model, or dataset -> model
)

// Ref is a stored record that points at an asset: a source or sink.
type Ref struct {
	Kind string `json:"kind"` // "source" or "sink"
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Node is a data asset or a workflow.
type Node struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// System is the engine family the asset lives in (postgres, kafka, s3...).
	System string `json:"system,omitempty"`
	// Locator is where the system is reached, without credentials: host, port
	// and database; brokers; a bucket. Empty for an inbound endpoint.
	Locator string `json:"locator,omitempty"`
	// Resolved is false for a KindConnector asset, which is known only by the
	// record that configures it and so cannot be matched across records.
	Resolved bool   `json:"resolved"`
	VHost    string `json:"vhost,omitempty"`
	// Active is set for workflows only.
	Active *bool `json:"active,omitempty"`
	Refs   []Ref `json:"refs,omitempty"`
}

// Edge is one read or write.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
	// WorkflowID and NodeID are the workflow, and the node of it, that the
	// edge comes from. NodeID is empty for a workflow-level setting (its
	// dead-letter sink) and for a model's retraining policy.
	WorkflowID string `json:"workflow_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
}

// Graph is the lineage of one scope.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node returns the node with the id.
func (g Graph) Node(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Inputs is what a graph is derived from: the stored records of one scope.
// A reference to a record that is not in Inputs (another vhost's, a deleted
// one) contributes nothing.
type Inputs struct {
	Workflows []storage.Workflow
	Sources   []storage.Source
	Sinks     []storage.Sink
	Models    []storage.MLModel
}

// WorkflowNodeID is the graph id of a workflow.
func WorkflowNodeID(workflowID string) string { return KindWorkflow + ":" + workflowID }
