// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mongodb

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/nodeclean"
)

// nodesDoc is the part of a workflow or version document this file reads.
type nodesDoc struct {
	ID         any                    `bson:"_id"`
	WorkflowID string                 `bson:"workflow_id"`
	VHost      string                 `bson:"vhost"`
	Nodes      []storage.WorkflowNode `bson:"nodes"`
}

// CleanStoredWorkflows is the MongoDB twin of the SQL backend's: it rewrites
// every workflow and version whose nodes hold what nodeclean removes, versions
// first, and returns how many documents it rewrote.
func (s *mongoStorage) CleanStoredWorkflows(ctx context.Context) (int, error) {
	workflows, err := s.readNodeDocs(ctx, "workflows")
	if err != nil {
		return 0, err
	}
	vhostOf := map[string]string{}
	for i := range workflows {
		id, _ := workflows[i].ID.(string)
		workflows[i].WorkflowID = id
		vhostOf[id] = workflows[i].VHost
	}
	versions, err := s.readNodeDocs(ctx, "workflow_versions")
	if err != nil {
		return 0, err
	}
	rewritten := 0
	for _, batch := range []struct {
		coll string
		docs []nodesDoc
	}{{"workflow_versions", versions}, {"workflows", workflows}} {
		for _, d := range batch.docs {
			before, _ := json.Marshal(d.Nodes)
			cleaned, err := nodeclean.ForWrite(ctx, s, vhostOf[d.WorkflowID], d.WorkflowID, d.Nodes)
			if err != nil {
				return rewritten, fmt.Errorf("cleaning nodes of %v: %w", d.ID, err)
			}
			after, _ := json.Marshal(cleaned)
			if string(before) == string(after) {
				continue
			}
			if _, err := s.db.Collection(batch.coll).UpdateOne(ctx, bson.M{"_id": d.ID},
				bson.M{"$set": bson.M{"nodes": cleaned}}); err != nil {
				return rewritten, fmt.Errorf("rewriting nodes of %v: %w", d.ID, err)
			}
			rewritten++
		}
	}
	return rewritten, nil
}

func (s *mongoStorage) readNodeDocs(ctx context.Context, coll string) ([]nodesDoc, error) {
	cursor, err := s.db.Collection(coll).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(ctx) }()
	var out []nodesDoc
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
