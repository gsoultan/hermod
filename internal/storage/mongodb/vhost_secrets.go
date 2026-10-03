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
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

const vhostSecretsCollection = "vhost_secrets"

// vhostSecretDoc is the stored form. _id is vhost + "/" + name: a secret name
// cannot hold a slash, so the last one always separates the two, and the key
// makes a vhost's name unique without a second index.
type vhostSecretDoc struct {
	ID        string    `bson:"_id"`
	VHost     string    `bson:"vhost"`
	Name      string    `bson:"name"`
	Value     string    `bson:"value"`
	UpdatedBy string    `bson:"updated_by"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

func vhostSecretID(vhost, name string) string {
	return vhost + "/" + name
}

func (s *mongoStorage) ListVHostSecrets(ctx context.Context, vhost string) ([]storage.VHostSecret, error) {
	// The value is left out of the projection, not just out of the result: a
	// list has no business reading it.
	cur, err := s.db.Collection(vhostSecretsCollection).Find(ctx,
		bson.M{"vhost": vhost},
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetProjection(bson.M{"value": 0}))
	if err != nil {
		return nil, fmt.Errorf("listing secrets of vhost %q: %w", vhost, err)
	}
	var docs []vhostSecretDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing secrets of vhost %q: %w", vhost, err)
	}
	out := make([]storage.VHostSecret, 0, len(docs))
	for _, d := range docs {
		out = append(out, storage.VHostSecret{
			VHost: d.VHost, Name: d.Name, UpdatedBy: d.UpdatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		})
	}
	return out, nil
}

// GetVHostSecret returns the secret with its value decrypted. A value that
// cannot be decrypted is an error, never the ciphertext.
func (s *mongoStorage) GetVHostSecret(ctx context.Context, vhost, name string) (storage.VHostSecret, error) {
	var d vhostSecretDoc
	err := s.db.Collection(vhostSecretsCollection).FindOne(ctx, bson.M{"_id": vhostSecretID(vhost, name)}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.VHostSecret{}, fmt.Errorf("reading secret %q of vhost %q: %w", name, vhost, err)
	}
	value, err := crypto.Decrypt(d.Value)
	if err != nil {
		return storage.VHostSecret{}, fmt.Errorf("secret %q of vhost %q cannot be decrypted with the current master key: %w", name, vhost, err)
	}
	return storage.VHostSecret{
		VHost: vhost, Name: name, Value: value, UpdatedBy: d.UpdatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}, nil
}

func (s *mongoStorage) PutVHostSecret(ctx context.Context, secret storage.VHostSecret) error {
	if err := storage.ValidateVHostSecret(secret); err != nil {
		return err
	}
	stored, err := crypto.Encrypt(secret.Value)
	if err != nil {
		return fmt.Errorf("encrypting secret %q of vhost %q: %w", secret.Name, secret.VHost, err)
	}
	now := time.Now().UTC()
	_, err = s.db.Collection(vhostSecretsCollection).UpdateOne(ctx,
		bson.M{"_id": vhostSecretID(secret.VHost, secret.Name)},
		bson.M{
			"$set":         bson.M{"value": stored, "updated_by": secret.UpdatedBy, "updated_at": now},
			"$setOnInsert": bson.M{"vhost": secret.VHost, "name": secret.Name, "created_at": now},
		},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving secret %q of vhost %q: %w", secret.Name, secret.VHost, err)
	}
	return nil
}

func (s *mongoStorage) DeleteVHostSecret(ctx context.Context, vhost, name string) error {
	res, err := s.db.Collection(vhostSecretsCollection).DeleteOne(ctx, bson.M{"_id": vhostSecretID(vhost, name)})
	if err != nil {
		return fmt.Errorf("deleting secret %q of vhost %q: %w", name, vhost, err)
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteVHostSecrets(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(vhostSecretsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the secrets of vhost %q: %w", vhost, err)
	}
	return nil
}
