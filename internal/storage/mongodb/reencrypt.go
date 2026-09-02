package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/gsoultan/Hermod/internal/storage/configsecrets"
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

	for _, u := range updates {
		if _, err := s.db.Collection(u.collection).UpdateOne(ctx,
			bson.M{"_id": u.id},
			bson.M{"$set": bson.M{"config": u.config}}); err != nil {
			return fmt.Errorf("rewriting %s %s: %w", u.collection, u.id, err)
		}
	}
	return nil
}
