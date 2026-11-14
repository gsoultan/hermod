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
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

// vhostNameCollections hold the name of the vhost a document belongs to in a
// vhost field and nowhere else, so a rename is one UpdateMany each.
var vhostNameCollections = []string{
	"workflows", "sources", "sinks", deadLettersCollection, "dashboard_history",
	mlJobsCollection, mlJobResultsCollection, mlFeedbackCollection, mlPredictionLogsCollection,
	"audit_logs",
}

// vhostPrefixedCollections have an _id that starts with the vhost's name and
// a slash -- vhost_secrets, ml_models, ml_scripts, ml_blobs, ml_schedules --
// so a document moves to the new name with the rest of its _id unchanged.
var vhostPrefixedCollections = []string{
	vhostSecretsCollection, mlModelsCollection, mlScriptsCollection, mlBlobsCollection, mlSchedulesCollection,
}

// vhostCollections have the vhost's name as their whole _id: one document
// per vhost.
var vhostCollections = []string{mlQuotasCollection, aiBudgetsCollection}

// UpdateVHost saves the vhost's name and description, and carries a new name
// to everything kept under the old one: workflows, sources, sinks, users'
// access, secrets, ML data, the AI budget and usage, dead letters and
// dashboard history.
//
// MongoDB has no multi-document transaction outside a replica set, so the
// vhost document is renamed last and every step can run again: a rename cut
// short leaves the vhost under its old name, and saving it again finishes
// what is left.
func (s *mongoStorage) UpdateVHost(ctx context.Context, vhost storage.VHost) error {
	current, err := s.GetVHost(ctx, vhost.ID)
	if err != nil {
		return err
	}
	vhosts := s.db.Collection("vhosts")
	if vhost.Name != current.Name {
		taken, err := vhosts.CountDocuments(ctx, bson.M{"name": vhost.Name, "_id": bson.M{"$ne": vhost.ID}})
		if err != nil {
			return fmt.Errorf("renaming vhost %q: %w", current.Name, err)
		}
		if taken > 0 {
			return fmt.Errorf("renaming vhost %q to %q: %w", current.Name, vhost.Name, storage.ErrVHostNameTaken)
		}
		if current.Name != "" {
			if err := s.renameVHostContents(ctx, current.Name, vhost.Name); err != nil {
				return fmt.Errorf("renaming vhost %q to %q: %w", current.Name, vhost.Name, err)
			}
		}
	}
	_, err = vhosts.UpdateOne(ctx, bson.M{"_id": vhost.ID}, bson.M{
		"$set": bson.M{"name": vhost.Name, "description": vhost.Description},
	})
	return err
}

func (s *mongoStorage) renameVHostContents(ctx context.Context, from, to string) error {
	for _, name := range vhostNameCollections {
		if _, err := s.db.Collection(name).UpdateMany(ctx, bson.M{"vhost": from}, bson.M{"$set": bson.M{"vhost": to}}); err != nil {
			return fmt.Errorf("moving %s: %w", name, err)
		}
	}
	for _, name := range vhostPrefixedCollections {
		err := s.moveVHostDocs(ctx, name, bson.M{"vhost": from}, func(doc bson.M) (string, error) {
			id, _ := doc["_id"].(string)
			if !strings.HasPrefix(id, from+"/") {
				return "", fmt.Errorf("%s %q is not keyed by vhost %q", name, id, from)
			}
			doc["vhost"] = to
			return to + strings.TrimPrefix(id, from), nil
		})
		if err != nil {
			return err
		}
	}
	err := s.moveVHostDocs(ctx, aiUsageCollection, bson.M{"vhost": from}, func(doc bson.M) (string, error) {
		period, _ := doc["period"].(string)
		workflowID, _ := doc["workflow_id"].(string)
		doc["vhost"] = to
		return storage.AIUsageID(to, period, workflowID), nil
	})
	if err != nil {
		return err
	}
	for _, name := range vhostCollections {
		if err := s.moveVHostDocs(ctx, name, bson.M{"_id": from}, func(bson.M) (string, error) { return to, nil }); err != nil {
			return err
		}
	}
	// The vhost's retention is a settings document keyed by its name.
	if err := s.moveVHostDocs(ctx, "settings", bson.M{"_id": storage.VHostRetentionSettingPrefix + from},
		func(bson.M) (string, error) { return storage.VHostRetentionSettingPrefix + to, nil }); err != nil {
		return err
	}
	return s.renameVHostInUsers(ctx, from, to)
}

// moveVHostDocs gives every document of coll matching filter the _id newID
// returns for it. An _id cannot be changed in place, so each is written under
// the new one and then removed under the old; the write replaces, so a move
// cut short between the two is finished by running it again.
func (s *mongoStorage) moveVHostDocs(ctx context.Context, coll string, filter bson.M, newID func(bson.M) (string, error)) error {
	c := s.db.Collection(coll)
	cur, err := c.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("reading %s: %w", coll, err)
	}
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("reading %s: %w", coll, err)
	}
	for _, doc := range docs {
		oldID := doc["_id"]
		id, err := newID(doc)
		if err != nil {
			return err
		}
		doc["_id"] = id
		if _, err := c.ReplaceOne(ctx, bson.M{"_id": id}, doc, options.Replace().SetUpsert(true)); err != nil {
			return fmt.Errorf("moving %s %v: %w", coll, oldID, err)
		}
		if oldID != id {
			if _, err := c.DeleteOne(ctx, bson.M{"_id": oldID}); err != nil {
				return fmt.Errorf("moving %s %v: %w", coll, oldID, err)
			}
		}
	}
	return nil
}

func (s *mongoStorage) renameVHostInUsers(ctx context.Context, from, to string) error {
	users := s.db.Collection("users")
	cur, err := users.Find(ctx, bson.M{"vhosts": from}, options.Find().SetProjection(bson.M{"vhosts": 1}))
	if err != nil {
		return fmt.Errorf("reading users: %w", err)
	}
	var docs []struct {
		ID     string   `bson:"_id"`
		VHosts []string `bson:"vhosts"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("reading users: %w", err)
	}
	var errs []error
	for _, d := range docs {
		renamed, ok := storage.RenameInVHostList(d.VHosts, from, to)
		if !ok {
			continue
		}
		if _, err := users.UpdateOne(ctx, bson.M{"_id": d.ID}, bson.M{"$set": bson.M{"vhosts": renamed}}); err != nil {
			errs = append(errs, fmt.Errorf("updating the vhosts of user %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}
