package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

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
	return nil
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
