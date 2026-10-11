package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

const mlScriptsCollection = "ml_scripts"

// mlScriptDoc is one script version. _id is vhost + "/" + name + "/" +
// version, so two saves racing for the same next version cannot both land.
type mlScriptDoc struct {
	ID          string    `bson:"_id"`
	VHost       string    `bson:"vhost"`
	Name        string    `bson:"name"`
	Version     int       `bson:"version"`
	SHA256      string    `bson:"sha256"`
	Source      string    `bson:"source,omitempty"`
	Description string    `bson:"description,omitempty"`
	CreatedBy   string    `bson:"created_by"`
	CreatedAt   time.Time `bson:"created_at"`
}

func mlScriptID(vhost, name string, version int) string {
	return vhost + "/" + name + "/" + strconv.Itoa(version)
}

func (d mlScriptDoc) script() storage.MLScript {
	return storage.MLScript{
		VHost: d.VHost, Name: d.Name, Version: d.Version, SHA256: d.SHA256, Source: d.Source,
		Description: d.Description, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt,
	}
}

// withoutSource leaves the source out of a listing.
var withoutSource = bson.M{"source": 0}

func (s *mongoStorage) ListMLScripts(ctx context.Context, vhost string) ([]storage.MLScript, error) {
	cur, err := s.db.Collection(mlScriptsCollection).Find(ctx, bson.M{"vhost": vhost},
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "version", Value: -1}}).SetProjection(withoutSource))
	if err != nil {
		return nil, fmt.Errorf("listing scripts of vhost %q: %w", vhost, err)
	}
	var docs []mlScriptDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing scripts of vhost %q: %w", vhost, err)
	}
	var out []storage.MLScript
	for _, d := range docs {
		if len(out) > 0 && out[len(out)-1].Name == d.Name {
			continue
		}
		out = append(out, d.script())
	}
	return out, nil
}

func (s *mongoStorage) ListMLScriptVersions(ctx context.Context, vhost, name string) ([]storage.MLScript, error) {
	cur, err := s.db.Collection(mlScriptsCollection).Find(ctx, bson.M{"vhost": vhost, "name": name},
		options.Find().SetSort(bson.D{{Key: "version", Value: -1}}).SetProjection(withoutSource))
	if err != nil {
		return nil, fmt.Errorf("listing versions of script %q of vhost %q: %w", name, vhost, err)
	}
	var docs []mlScriptDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing versions of script %q of vhost %q: %w", name, vhost, err)
	}
	out := make([]storage.MLScript, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.script())
	}
	return out, nil
}

func (s *mongoStorage) GetMLScript(ctx context.Context, vhost, name string) (storage.MLScript, error) {
	var d mlScriptDoc
	err := s.db.Collection(mlScriptsCollection).FindOne(ctx, bson.M{"vhost": vhost, "name": name},
		options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLScript{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLScript{}, fmt.Errorf("reading script %q of vhost %q: %w", name, vhost, err)
	}
	return d.script(), nil
}

// PutMLScript inserts the next version; a duplicate _id means another save
// took that number first, so it reads the new latest and tries once more.
func (s *mongoStorage) PutMLScript(ctx context.Context, sc storage.MLScript) (storage.MLScript, error) {
	if err := storage.ValidateMLScript(sc); err != nil {
		return storage.MLScript{}, err
	}
	sc.SHA256 = storage.MLScriptSHA256(sc.Source)
	var lastErr error
	for range 2 {
		latest, err := s.GetMLScript(ctx, sc.VHost, sc.Name)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			sc.Version = 1
		case err != nil:
			return storage.MLScript{}, err
		case latest.SHA256 == sc.SHA256:
			return latest, nil
		default:
			sc.Version = latest.Version + 1
		}
		sc.CreatedAt = time.Now().UTC()
		_, lastErr = s.db.Collection(mlScriptsCollection).InsertOne(ctx, mlScriptDoc{
			ID: mlScriptID(sc.VHost, sc.Name, sc.Version), VHost: sc.VHost, Name: sc.Name, Version: sc.Version,
			SHA256: sc.SHA256, Source: sc.Source, Description: sc.Description, CreatedBy: sc.CreatedBy, CreatedAt: sc.CreatedAt,
		})
		if lastErr == nil {
			return sc, nil
		}
		if !mongo.IsDuplicateKeyError(lastErr) {
			break
		}
	}
	return storage.MLScript{}, fmt.Errorf("saving script %q of vhost %q: %w", sc.Name, sc.VHost, lastErr)
}

func (s *mongoStorage) DeleteMLScript(ctx context.Context, vhost, name string) error {
	res, err := s.db.Collection(mlScriptsCollection).DeleteMany(ctx, bson.M{"vhost": vhost, "name": name})
	if err != nil {
		return fmt.Errorf("deleting script %q of vhost %q: %w", name, vhost, err)
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteMLScripts(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(mlScriptsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the scripts of vhost %q: %w", vhost, err)
	}
	return nil
}
