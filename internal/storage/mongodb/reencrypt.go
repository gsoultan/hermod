// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// ReEncryptSecrets rewrites every stored credential under newKey.
//
// See the SQL implementation for why this exists. The shape is the same: build
// all the new ciphertext first and fail without writing anything if a single
// value cannot be read, so a rotation is never half-applied. MongoDB gets no
// transaction here because the deployments Hermod targets are not guaranteed to
// be replica sets, and requiring one to rotate a key would be a worse trade
// than a rewrite that can stop partway — which the pre-flight decrypt check
// makes vanishingly unlikely, since by then every value is known to be
// readable.
func (s *mongoStorage) ReEncryptSecrets(ctx context.Context, newKey string) error {
	if newKey == "" {
		return errors.New("re-encrypt: empty key")
	}

	type update struct {
		collection string
		id         string
		config     map[string]string
	}

	var updates []update
	for _, name := range []string{"sources", "sinks"} {
		cur, err := s.db.Collection(name).Find(ctx, bson.M{})
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		var docs []struct {
			ID     string            `bson:"_id"`
			Config map[string]string `bson:"config"`
		}
		if err := cur.All(ctx, &docs); err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		for _, d := range docs {
			next, err := configsecrets.ReEncrypt(d.Config, newKey)
			if err != nil {
				return fmt.Errorf("%s %s: %w", name, d.ID, err)
			}
			updates = append(updates, update{collection: name, id: d.ID, config: next})
		}
	}

	// A vhost's secrets are encrypted under the same key. Left out, they would
	// become unreadable the moment the caller installs the new one. As above,
	// every value is re-encrypted before anything is written.
	secretValues, err := s.reEncryptVHostSecrets(ctx, newKey)
	if err != nil {
		return err
	}
	// So are the settings that hold credentials, such as the ML worker's
	// token.
	settingValues, err := s.reEncryptSettings(ctx, newKey)
	if err != nil {
		return err
	}

	for _, u := range updates {
		if _, err := s.db.Collection(u.collection).UpdateOne(ctx,
			bson.M{"_id": u.id},
			bson.M{"$set": bson.M{"config": u.config}}); err != nil {
			return fmt.Errorf("rewriting %s %s: %w", u.collection, u.id, err)
		}
	}
	for id, value := range secretValues {
		if _, err := s.db.Collection(vhostSecretsCollection).UpdateOne(ctx,
			bson.M{"_id": id},
			bson.M{"$set": bson.M{"value": value}}); err != nil {
			return fmt.Errorf("rewriting %s %s: %w", vhostSecretsCollection, id, err)
		}
	}
	for key, value := range settingValues {
		if err := s.SaveSetting(ctx, key, value); err != nil {
			return fmt.Errorf("rewriting setting %s: %w", key, err)
		}
	}
	return nil
}

// reEncryptSettings returns the new value of every setting in
// storage.EncryptedSettingKeys that is set, keyed by its key. One value that
// cannot be read under the current key fails the rotation before anything is
// written.
func (s *mongoStorage) reEncryptSettings(ctx context.Context, newKey string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range storage.EncryptedSettingKeys {
		raw, err := s.GetSetting(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("reading setting %s: %w", key, err)
		}
		if raw == "" {
			continue
		}
		cfg := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("setting %s is unreadable: %w", key, err)
		}
		next, err := configsecrets.ReEncrypt(cfg, newKey)
		if err != nil {
			return nil, fmt.Errorf("setting %s: %w", key, err)
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, fmt.Errorf("setting %s: %w", key, err)
		}
		out[key] = string(encoded)
	}
	return out, nil
}

// reEncryptVHostSecrets returns every vhost secret's value encrypted under
// newKey, keyed by document id. One value that cannot be read under the
// current key fails the rotation before anything is written.
func (s *mongoStorage) reEncryptVHostSecrets(ctx context.Context, newKey string) (map[string]string, error) {
	cur, err := s.db.Collection(vhostSecretsCollection).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", vhostSecretsCollection, err)
	}
	var docs []vhostSecretDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("reading %s: %w", vhostSecretsCollection, err)
	}
	out := make(map[string]string, len(docs))
	for _, d := range docs {
		plain, err := crypto.Decrypt(d.Value)
		if err != nil {
			return nil, fmt.Errorf("%s %s cannot be read under the current key: %w", vhostSecretsCollection, d.ID, err)
		}
		next, err := crypto.EncryptWith(newKey, plain)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", vhostSecretsCollection, d.ID, err)
		}
		out[d.ID] = next
	}
	return out, nil
}
