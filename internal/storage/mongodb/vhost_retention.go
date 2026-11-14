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
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ofWorkflow matches a workflow's documents under either key they are stored
// with. Versions and approvals are inserted as structs with no bson tags, which
// the driver writes as "workflowid", while fields set later by name use
// "workflow_id".
func ofWorkflow(workflowID string) bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"workflow_id": workflowID},
		bson.M{"workflowid": workflowID},
	}}
}

// PurgeAuditLogsForVHost deletes one vhost's audit rows older than before.
func (s *mongoStorage) PurgeAuditLogsForVHost(ctx context.Context, vhost string, before time.Time) error {
	if vhost == "" {
		return nil
	}
	_, err := s.db.Collection("audit_logs").DeleteMany(ctx,
		bson.M{"vhost": vhost, "timestamp": bson.M{"$lt": before}})
	return err
}

// PurgeWorkflowVersions keeps a workflow's newest keep versions.
func (s *mongoStorage) PurgeWorkflowVersions(ctx context.Context, workflowID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	coll := s.db.Collection("workflow_versions")
	// The oldest version that stays: the keep-th from the top. When there are
	// no more than keep, nothing is found and nothing is deleted.
	var oldestKept struct {
		Version int `bson:"version"`
	}
	opts := options.FindOne().
		SetSort(bson.D{{Key: "version", Value: -1}}).
		SetSkip(int64(keep - 1)).
		SetProjection(bson.M{"version": 1})
	if err := coll.FindOne(ctx, ofWorkflow(workflowID), opts).Decode(&oldestKept); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		return err
	}
	filter := ofWorkflow(workflowID)
	filter["version"] = bson.M{"$lt": oldestKept.Version}
	_, err := coll.DeleteMany(ctx, filter)
	return err
}

// PurgeDecidedApprovals deletes a workflow's approvals decided before the time
// given. A pending approval has no processed_at, so it never matches.
func (s *mongoStorage) PurgeDecidedApprovals(ctx context.Context, workflowID string, before time.Time) error {
	filter := ofWorkflow(workflowID)
	filter["status"] = bson.M{"$nin": bson.A{"pending", "", nil}}
	filter["processed_at"] = bson.M{"$lt": before}
	_, err := s.db.Collection("approvals").DeleteMany(ctx, filter)
	return err
}
