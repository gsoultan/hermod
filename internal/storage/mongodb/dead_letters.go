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

const deadLettersCollection = "dead_letters"

// deadLetterDoc is the stored dead letter; _id is its id.
type deadLetterDoc struct {
	ID          string                    `bson:"_id"`
	VHost       string                    `bson:"vhost"`
	WorkflowID  string                    `bson:"workflow_id"`
	MessageID   string                    `bson:"message_id"`
	NodeID      string                    `bson:"node_id"`
	SinkID      string                    `bson:"sink_id"`
	Error       string                    `bson:"error"`
	FailedAt    time.Time                 `bson:"failed_at"`
	Payload     []byte                    `bson:"payload,omitempty"`
	PayloadSize int                       `bson:"payload_size"`
	Truncated   bool                      `bson:"truncated"`
	Metadata    map[string]string         `bson:"metadata,omitempty"`
	Origin      storage.DeadLetterOrigin  `bson:"origin"`
	Status      string                    `bson:"status"`
	SettledAt   *time.Time                `bson:"settled_at,omitempty"`
	SettledBy   string                    `bson:"settled_by,omitempty"`
	History     []storage.DeadLetterEvent `bson:"history,omitempty"`
}

func (d deadLetterDoc) deadLetter() storage.DeadLetter {
	out := storage.DeadLetter{
		ID: d.ID, VHost: d.VHost, WorkflowID: d.WorkflowID, MessageID: d.MessageID, NodeID: d.NodeID, SinkID: d.SinkID,
		Error: d.Error, FailedAt: d.FailedAt.UTC(), Payload: d.Payload, PayloadSize: d.PayloadSize, Truncated: d.Truncated,
		Metadata: d.Metadata, Origin: d.Origin, Status: storage.DeadLetterStatus(d.Status), SettledBy: d.SettledBy, History: d.History,
	}
	if d.SettledAt != nil {
		t := d.SettledAt.UTC()
		out.SettledAt = &t
	}
	return out
}

func (s *mongoStorage) PutDeadLetter(ctx context.Context, d storage.DeadLetter) error {
	d = storage.CapDeadLetter(d)
	if err := storage.ValidateDeadLetter(d); err != nil {
		return err
	}
	doc := deadLetterDoc{
		ID: d.ID, VHost: d.VHost, WorkflowID: d.WorkflowID, MessageID: d.MessageID, NodeID: d.NodeID, SinkID: d.SinkID,
		Error: d.Error, FailedAt: d.FailedAt, Payload: d.Payload, PayloadSize: d.PayloadSize, Truncated: d.Truncated,
		Metadata: d.Metadata, Origin: d.Origin, Status: string(d.Status), SettledAt: d.SettledAt, SettledBy: d.SettledBy, History: d.History,
	}
	if _, err := s.db.Collection(deadLettersCollection).InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("keeping dead letter %s: %w", d.ID, err)
	}
	return nil
}

func (s *mongoStorage) GetDeadLetter(ctx context.Context, id string) (storage.DeadLetter, error) {
	var d deadLetterDoc
	err := s.db.Collection(deadLettersCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.DeadLetter{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.DeadLetter{}, fmt.Errorf("reading dead letter %s: %w", id, err)
	}
	return d.deadLetter(), nil
}

// deadLetterQuery is the filter as a query; the stats share it.
func deadLetterQuery(f storage.DeadLetterFilter) bson.M {
	q := bson.M{}
	var and []bson.M
	if vhosts := storage.WithSharedVHosts(f.VHosts); vhosts != nil {
		q["vhost"] = bson.M{"$in": vhosts}
	}
	if f.WorkflowID != "" {
		q["workflow_id"] = f.WorkflowID
	}
	if f.Where != "" {
		and = append(and, bson.M{"$or": []bson.M{{"node_id": f.Where}, {"sink_id": f.Where}}})
	}
	if f.Status != "" {
		q["status"] = string(f.Status)
	}
	if f.Search != "" {
		and = append(and, bson.M{"$or": searchAcross(f.Search, "error", "message_id", "node_id", "sink_id")})
	}
	failedAt := bson.M{}
	if !f.Since.IsZero() {
		failedAt["$gte"] = f.Since.UTC()
	}
	if !f.Until.IsZero() {
		failedAt["$lte"] = f.Until.UTC()
	}
	if len(failedAt) > 0 {
		q["failed_at"] = failedAt
	}
	if len(and) > 0 {
		q["$and"] = and
	}
	return q
}

func (s *mongoStorage) ListDeadLetters(ctx context.Context, f storage.DeadLetterFilter) ([]storage.DeadLetter, int, error) {
	coll := s.db.Collection(deadLettersCollection)
	q := deadLetterQuery(f)
	total, err := coll.CountDocuments(ctx, q)
	if err != nil {
		return nil, 0, fmt.Errorf("counting dead letters: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "failed_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64(max(f.Offset, 0))).SetLimit(int64(limit)).
		SetProjection(bson.M{"payload": 0, "origin": 0, "metadata": 0, "history": 0})
	cur, err := coll.Find(ctx, q, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("listing dead letters: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	out := []storage.DeadLetter{}
	for cur.Next(ctx) {
		var d deadLetterDoc
		if err := cur.Decode(&d); err != nil {
			return nil, 0, fmt.Errorf("listing dead letters: %w", err)
		}
		out = append(out, d.deadLetter())
	}
	if err := cur.Err(); err != nil {
		return nil, 0, fmt.Errorf("listing dead letters: %w", err)
	}
	return out, int(total), nil
}

// SettleDeadLetter updates only a queued document, in one statement, so of
// two concurrent settles exactly one matches.
func (s *mongoStorage) SettleDeadLetter(ctx context.Context, id string, status storage.DeadLetterStatus, ev storage.DeadLetterEvent) error {
	if !storage.ValidSettledStatus(status) {
		return fmt.Errorf("a dead letter cannot be settled as %q", status)
	}
	coll := s.db.Collection(deadLettersCollection)
	res, err := coll.UpdateOne(ctx,
		bson.M{"_id": id, "status": string(storage.DeadLetterQueued)},
		bson.M{
			"$set":  bson.M{"status": string(status), "settled_at": ev.At.UTC(), "settled_by": ev.By},
			"$push": bson.M{"history": bson.M{"$each": []storage.DeadLetterEvent{ev}, "$slice": -100}},
		})
	if err != nil {
		return fmt.Errorf("settling dead letter %s: %w", id, err)
	}
	if res.MatchedCount == 1 {
		return nil
	}
	n, err := coll.CountDocuments(ctx, bson.M{"_id": id})
	if err != nil {
		return fmt.Errorf("settling dead letter %s: %w", id, err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return storage.ErrDeadLetterSettled
}

func (s *mongoStorage) AddDeadLetterEvent(ctx context.Context, id string, ev storage.DeadLetterEvent) error {
	res, err := s.db.Collection(deadLettersCollection).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$push": bson.M{"history": bson.M{"$each": []storage.DeadLetterEvent{ev}, "$slice": -100}}})
	if err != nil {
		return fmt.Errorf("noting an event on dead letter %s: %w", id, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeadLetterStats(ctx context.Context, vhosts []string, now time.Time) (storage.DeadLetterStats, error) {
	coll := s.db.Collection(deadLettersCollection)
	var st storage.DeadLetterStats
	count := func(q bson.M) (int, error) {
		n, err := coll.CountDocuments(ctx, q)
		return int(n), err
	}
	queued := deadLetterQuery(storage.DeadLetterFilter{VHosts: vhosts, Status: storage.DeadLetterQueued})
	var err error
	if st.Queued, err = count(queued); err != nil {
		return st, fmt.Errorf("counting queued dead letters: %w", err)
	}
	if st.Last24h, err = count(deadLetterQuery(storage.DeadLetterFilter{VHosts: vhosts, Since: now.Add(-24 * time.Hour)})); err != nil {
		return st, fmt.Errorf("counting recent dead letters: %w", err)
	}
	replayed := deadLetterQuery(storage.DeadLetterFilter{VHosts: vhosts, Status: storage.DeadLetterReplayed})
	replayed["settled_at"] = bson.M{"$gte": now.Add(-7 * 24 * time.Hour).UTC()}
	if st.ReplayedThisWeek, err = count(replayed); err != nil {
		return st, fmt.Errorf("counting replayed dead letters: %w", err)
	}
	cur, err := coll.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: queued}},
		{{Key: "$group", Value: bson.M{"_id": "$error", "n": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.D{{Key: "n", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: 1}},
	})
	if err != nil {
		return st, fmt.Errorf("finding the most common dead letter error: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	if cur.Next(ctx) {
		var top struct {
			Error string `bson:"_id"`
			N     int    `bson:"n"`
		}
		if err := cur.Decode(&top); err != nil {
			return st, fmt.Errorf("finding the most common dead letter error: %w", err)
		}
		st.TopError, st.TopErrorCount = top.Error, top.N
	}
	return st, cur.Err()
}

func (s *mongoStorage) PurgeDeadLetters(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.Collection(deadLettersCollection).DeleteMany(ctx, bson.M{
		"failed_at": bson.M{"$lt": cutoff.UTC()},
		"status":    bson.M{"$ne": string(storage.DeadLetterQueued)},
	})
	if err != nil {
		return 0, fmt.Errorf("purging dead letters: %w", err)
	}
	return res.DeletedCount, nil
}
