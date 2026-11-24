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
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

const agentsCollection = "agents"

// agentDoc is a stored agent; _id is the agent's id.
type agentDoc struct {
	ID        string                `bson:"_id"`
	VHost     string                `bson:"vhost"`
	Name      string                `bson:"name"`
	TokenHash string                `bson:"token_hash"`
	Devices   []storage.AgentDevice `bson:"devices"`
	Status    storage.AgentStatus   `bson:"status"`
	CreatedBy string                `bson:"created_by"`
	CreatedAt time.Time             `bson:"created_at"`
	UpdatedAt time.Time             `bson:"updated_at"`
}

func (d agentDoc) agent() storage.Agent {
	return storage.Agent{
		ID: d.ID, VHost: d.VHost, Name: d.Name, TokenHash: d.TokenHash, Devices: d.Devices, Status: d.Status,
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStorage) CreateAgent(ctx context.Context, a storage.Agent) error {
	if err := storage.ValidateAgent(a); err != nil {
		return err
	}
	now := time.Now().UTC()
	d := agentDoc{
		ID: a.ID, VHost: a.VHost, Name: a.Name, TokenHash: a.TokenHash, Devices: a.Devices, Status: a.Status,
		CreatedBy: a.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := s.db.Collection(agentsCollection).InsertOne(ctx, d); err != nil {
		return fmt.Errorf("saving agent %q: %w", a.Name, err)
	}
	return nil
}

func (s *mongoStorage) GetAgent(ctx context.Context, id string) (storage.Agent, error) {
	var d agentDoc
	err := s.db.Collection(agentsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.Agent{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Agent{}, fmt.Errorf("reading agent %q: %w", id, err)
	}
	return d.agent(), nil
}

func (s *mongoStorage) ListAgents(ctx context.Context, vhost string) ([]storage.Agent, error) {
	filter := bson.M{}
	if vhost != "" {
		filter["vhost"] = vhost
	}
	opts := options.Find().SetSort(bson.D{{Key: "vhost", Value: 1}, {Key: "name", Value: 1}, {Key: "_id", Value: 1}})
	cur, err := s.db.Collection(agentsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	var docs []agentDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	out := make([]storage.Agent, len(docs))
	for i, d := range docs {
		out[i] = d.agent()
	}
	return out, nil
}

func (s *mongoStorage) UpdateAgentDevices(ctx context.Context, id, name string, devices []storage.AgentDevice) error {
	current, err := s.GetAgent(ctx, id)
	if err != nil {
		return err
	}
	current.Name, current.Devices = name, devices
	if err := storage.ValidateAgent(current); err != nil {
		return err
	}
	return s.updateAgent(ctx, id, "devices", bson.M{"name": name, "devices": devices, "updated_at": time.Now().UTC()})
}

func (s *mongoStorage) UpdateAgentStatus(ctx context.Context, id string, status storage.AgentStatus) error {
	return s.updateAgent(ctx, id, "status", bson.M{"status": status})
}

func (s *mongoStorage) UpdateAgentToken(ctx context.Context, id, tokenHash string) error {
	return s.updateAgent(ctx, id, "token", bson.M{"token_hash": tokenHash, "updated_at": time.Now().UTC()})
}

// updateAgent sets fields on one agent and reports ErrNotFound when there is
// no such agent.
func (s *mongoStorage) updateAgent(ctx context.Context, id, what string, set bson.M) error {
	res, err := s.db.Collection(agentsCollection).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("saving the %s of agent %q: %w", what, id, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.Collection(agentsCollection).DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return fmt.Errorf("deleting agent %q: %w", id, err)
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteAgents(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(agentsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the agents of vhost %q: %w", vhost, err)
	}
	return nil
}
