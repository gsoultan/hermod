package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

const (
	mlJobsCollection       = "ml_jobs"
	mlJobResultsCollection = "ml_job_results"
)

// mlJobDoc is one job. _id is the job's id. request and result are the JSON
// text, as the SQL store keeps them, so a request's numbers come back exactly
// as they were sent.
type mlJobDoc struct {
	ID                string     `bson:"_id"`
	VHost             string     `bson:"vhost"`
	Model             string     `bson:"model"`
	Kind              string     `bson:"kind"`
	Status            string     `bson:"status"`
	Progress          float64    `bson:"progress"`
	ProgressMessage   string     `bson:"progress_message,omitempty"`
	Request           string     `bson:"request,omitempty"`
	Result            string     `bson:"result,omitempty"`
	ResultRef         string     `bson:"result_ref,omitempty"`
	Error             string     `bson:"error,omitempty"`
	CallbackURL       string     `bson:"callback_url,omitempty"`
	CallbackStatus    string     `bson:"callback_status,omitempty"`
	CallbackAttempts  int        `bson:"callback_attempts"`
	CallbackNextAt    *time.Time `bson:"callback_next_at,omitempty"`
	CallbackLastError string     `bson:"callback_error,omitempty"`
	CallerKind        string     `bson:"caller_kind,omitempty"`
	CreatedBy         string     `bson:"created_by,omitempty"`
	CancelRequested   bool       `bson:"cancel_requested"`
	Attempts          int        `bson:"attempts"`
	ClaimedBy         string     `bson:"claimed_by,omitempty"`
	ClaimUntil        *time.Time `bson:"claim_until,omitempty"`
	CreatedAt         time.Time  `bson:"created_at"`
	StartedAt         *time.Time `bson:"started_at,omitempty"`
	FinishedAt        *time.Time `bson:"finished_at,omitempty"`
	ExpiresAt         time.Time  `bson:"expires_at"`
}

// mlJobResultDoc is a result too large for its job. _id is the job's id.
type mlJobResultDoc struct {
	ID        string    `bson:"_id"`
	VHost     string    `bson:"vhost"`
	Result    string    `bson:"result"`
	ExpiresAt time.Time `bson:"expires_at"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func mlJobDocOf(j storage.MLJob) mlJobDoc {
	return mlJobDoc{
		ID: j.ID, VHost: j.VHost, Model: j.Model, Kind: j.Kind, Status: j.Status, Progress: j.Progress,
		ProgressMessage: j.ProgressMessage, Request: string(j.Request), Result: string(j.Result), ResultRef: j.ResultRef,
		Error: j.Error, CallbackURL: j.CallbackURL, CallbackStatus: j.CallbackStatus, CallbackAttempts: j.CallbackAttempts,
		CallbackNextAt: timePtr(j.CallbackNextAt), CallbackLastError: j.CallbackLastError, CallerKind: j.CallerKind,
		CreatedBy: j.CreatedBy, CancelRequested: j.CancelRequested, Attempts: j.Attempts, ClaimedBy: j.ClaimedBy,
		ClaimUntil: timePtr(j.ClaimUntil), CreatedAt: j.CreatedAt.UTC(), StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		ExpiresAt: j.ExpiresAt.UTC(),
	}
}

func (d mlJobDoc) job() storage.MLJob {
	j := storage.MLJob{
		ID: d.ID, VHost: d.VHost, Model: d.Model, Kind: d.Kind, Status: d.Status, Progress: d.Progress,
		ProgressMessage: d.ProgressMessage, ResultRef: d.ResultRef, Error: d.Error, CallbackURL: d.CallbackURL,
		CallbackStatus: d.CallbackStatus, CallbackAttempts: d.CallbackAttempts, CallbackNextAt: timeOf(d.CallbackNextAt),
		CallbackLastError: d.CallbackLastError, CallerKind: d.CallerKind, CreatedBy: d.CreatedBy,
		CancelRequested: d.CancelRequested, Attempts: d.Attempts, ClaimedBy: d.ClaimedBy, ClaimUntil: timeOf(d.ClaimUntil),
		CreatedAt: d.CreatedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt, ExpiresAt: d.ExpiresAt,
	}
	if d.Request != "" {
		j.Request = json.RawMessage(d.Request)
	}
	if d.Result != "" {
		j.Result = json.RawMessage(d.Result)
	}
	return j
}

func (s *mongoStorage) CreateMLJob(ctx context.Context, j storage.MLJob) error {
	if _, err := s.db.Collection(mlJobsCollection).InsertOne(ctx, mlJobDocOf(j)); err != nil {
		return fmt.Errorf("saving ML job %q of vhost %q: %w", j.ID, j.VHost, err)
	}
	return nil
}

func (s *mongoStorage) GetMLJob(ctx context.Context, vhost, id string) (storage.MLJob, error) {
	var d mlJobDoc
	err := s.db.Collection(mlJobsCollection).FindOne(ctx, bson.M{"_id": id, "vhost": vhost}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLJob{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLJob{}, fmt.Errorf("reading ML job %q of vhost %q: %w", id, vhost, err)
	}
	return d.job(), nil
}

func (s *mongoStorage) ListMLJobs(ctx context.Context, f storage.MLJobFilter) ([]storage.MLJob, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > storage.MaxMLJobPage {
		limit = storage.MaxMLJobPage
	}
	filter := bson.M{"vhost": f.VHost}
	if f.Model != "" {
		filter["model"] = f.Model
	}
	if f.Status != "" {
		filter["status"] = f.Status
	}
	coll := s.db.Collection(mlJobsCollection)
	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("counting the ML jobs of vhost %q: %w", f.VHost, err)
	}
	cur, err := coll.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64(max(f.Offset, 0))).SetLimit(int64(limit)).
		SetProjection(bson.M{"request": 0, "result": 0}))
	if err != nil {
		return nil, 0, fmt.Errorf("listing the ML jobs of vhost %q: %w", f.VHost, err)
	}
	var docs []mlJobDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, 0, fmt.Errorf("listing the ML jobs of vhost %q: %w", f.VHost, err)
	}
	out := make([]storage.MLJob, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.job())
	}
	return out, int(total), nil
}

// ClaimMLJob takes the oldest claimable job in one findOneAndUpdate, so two
// Hermods cannot both take it. The update is a pipeline so a takeover keeps
// the started_at of the first run.
func (s *mongoStorage) ClaimMLJob(ctx context.Context, owner string, ttl time.Duration, now time.Time) (storage.MLJob, bool, error) {
	now = now.UTC()
	filter := bson.M{"$or": []bson.M{
		{"status": storage.MLJobQueued},
		{"status": storage.MLJobRunning, "$or": []bson.M{
			{"claim_until": bson.M{"$exists": false}}, {"claim_until": nil}, {"claim_until": bson.M{"$lt": now}},
		}},
	}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.M{
		"status": storage.MLJobRunning, "claimed_by": owner, "claim_until": now.Add(ttl),
		"started_at": bson.M{"$ifNull": bson.A{"$started_at", now}},
		"attempts":   bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$attempts", 0}}, 1}},
	}}}}
	var d mlJobDoc
	err := s.db.Collection(mlJobsCollection).FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).
			SetReturnDocument(options.After)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLJob{}, false, nil
	}
	if err != nil {
		return storage.MLJob{}, false, fmt.Errorf("claiming an ML job to run: %w", err)
	}
	return d.job(), true, nil
}

func (s *mongoStorage) UpdateMLJobProgress(ctx context.Context, id, owner string, progress float64, message string, until time.Time) (bool, error) {
	var d mlJobDoc
	err := s.db.Collection(mlJobsCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": id, "claimed_by": owner, "status": storage.MLJobRunning},
		bson.M{"$set": bson.M{"progress": progress, "progress_message": message, "claim_until": until.UTC()}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, storage.ErrMLJobClaimLost
	}
	if err != nil {
		return false, fmt.Errorf("recording the progress of ML job %q: %w", id, err)
	}
	return d.CancelRequested, nil
}

func (s *mongoStorage) FinishMLJob(ctx context.Context, id, owner string, f storage.MLJobFinish) error {
	coll := s.db.Collection(mlJobsCollection)
	var d mlJobDoc
	if err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return storage.ErrMLJobClaimLost
		}
		return fmt.Errorf("finishing ML job %q: %w", id, err)
	}
	set := bson.M{"status": f.Status, "progress": 1.0, "finished_at": f.At.UTC()}
	unset := bson.M{"claimed_by": "", "claim_until": ""}
	for field, v := range map[string]string{"result": string(f.Result), "result_ref": f.ResultRef, "error": f.Error} {
		if v == "" {
			unset[field] = ""
		} else {
			set[field] = v
		}
	}
	if d.CallbackURL != "" {
		set["callback_status"], set["callback_next_at"] = storage.MLCallbackPending, f.At.UTC()
	}
	res, err := coll.UpdateOne(ctx, bson.M{"_id": id, "claimed_by": owner, "status": storage.MLJobRunning},
		bson.M{"$set": set, "$unset": unset})
	if err != nil {
		return fmt.Errorf("finishing ML job %q: %w", id, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrMLJobClaimLost
	}
	return nil
}

func (s *mongoStorage) CancelMLJob(ctx context.Context, vhost, id string, now time.Time) (storage.MLJob, error) {
	coll := s.db.Collection(mlJobsCollection)
	j, err := s.GetMLJob(ctx, vhost, id)
	if err != nil {
		return storage.MLJob{}, err
	}
	now = now.UTC()
	set := bson.M{"status": storage.MLJobCancelled, "progress": 1.0, "finished_at": now}
	if j.CallbackURL != "" {
		set["callback_status"], set["callback_next_at"] = storage.MLCallbackPending, now
	}
	if _, err := coll.UpdateOne(ctx, bson.M{"_id": id, "vhost": vhost, "status": storage.MLJobQueued}, bson.M{"$set": set}); err != nil {
		return storage.MLJob{}, fmt.Errorf("cancelling ML job %q: %w", id, err)
	}
	if _, err := coll.UpdateOne(ctx, bson.M{"_id": id, "vhost": vhost, "status": storage.MLJobRunning},
		bson.M{"$set": bson.M{"cancel_requested": true}}); err != nil {
		return storage.MLJob{}, fmt.Errorf("cancelling ML job %q: %w", id, err)
	}
	return s.GetMLJob(ctx, vhost, id)
}

func (s *mongoStorage) ClaimMLJobCallback(ctx context.Context, owner string, ttl time.Duration, now time.Time) (storage.MLJob, bool, error) {
	now = now.UTC()
	filter := bson.M{
		"callback_status":  storage.MLCallbackPending,
		"callback_next_at": bson.M{"$lte": now},
		"$or": []bson.M{
			{"claimed_by": bson.M{"$exists": false}}, {"claimed_by": nil},
			{"claim_until": bson.M{"$exists": false}}, {"claim_until": nil}, {"claim_until": bson.M{"$lt": now}},
		},
	}
	var d mlJobDoc
	err := s.db.Collection(mlJobsCollection).FindOneAndUpdate(ctx, filter,
		bson.M{"$set": bson.M{"claimed_by": owner, "claim_until": now.Add(ttl)}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "callback_next_at", Value: 1}, {Key: "_id", Value: 1}}).
			SetReturnDocument(options.After)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.MLJob{}, false, nil
	}
	if err != nil {
		return storage.MLJob{}, false, fmt.Errorf("claiming an ML job callback to send: %w", err)
	}
	return d.job(), true, nil
}

func (s *mongoStorage) RecordMLJobCallback(ctx context.Context, id, owner, status string, attempts int, next time.Time, lastError string) error {
	set := bson.M{"callback_status": status, "callback_attempts": attempts}
	unset := bson.M{"claimed_by": "", "claim_until": ""}
	if next.IsZero() {
		unset["callback_next_at"] = ""
	} else {
		set["callback_next_at"] = next.UTC()
	}
	if lastError == "" {
		unset["callback_error"] = ""
	} else {
		set["callback_error"] = lastError
	}
	res, err := s.db.Collection(mlJobsCollection).UpdateOne(ctx, bson.M{"_id": id, "claimed_by": owner},
		bson.M{"$set": set, "$unset": unset})
	if err != nil {
		return fmt.Errorf("recording the callback of ML job %q: %w", id, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrMLJobClaimLost
	}
	return nil
}

func (s *mongoStorage) PutMLJobResult(ctx context.Context, vhost, id string, result json.RawMessage) error {
	j, err := s.GetMLJob(ctx, vhost, id)
	if err != nil {
		return err
	}
	_, err = s.db.Collection(mlJobResultsCollection).ReplaceOne(ctx, bson.M{"_id": id},
		mlJobResultDoc{ID: id, VHost: vhost, Result: string(result), ExpiresAt: j.ExpiresAt},
		options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving the result of ML job %q: %w", id, err)
	}
	return nil
}

func (s *mongoStorage) GetMLJobResult(ctx context.Context, vhost, id string) (json.RawMessage, error) {
	var d mlJobResultDoc
	err := s.db.Collection(mlJobResultsCollection).FindOne(ctx, bson.M{"_id": id, "vhost": vhost}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading the result of ML job %q: %w", id, err)
	}
	return json.RawMessage(d.Result), nil
}

func (s *mongoStorage) DeleteExpiredMLJobs(ctx context.Context, now time.Time) (int, error) {
	expired := bson.M{"expires_at": bson.M{"$lt": now.UTC()}}
	if _, err := s.db.Collection(mlJobResultsCollection).DeleteMany(ctx, expired); err != nil {
		return 0, fmt.Errorf("removing expired ML job results: %w", err)
	}
	res, err := s.db.Collection(mlJobsCollection).DeleteMany(ctx, expired)
	if err != nil {
		return 0, fmt.Errorf("removing expired ML jobs: %w", err)
	}
	return int(res.DeletedCount), nil
}

func (s *mongoStorage) DeleteMLJobs(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(mlJobResultsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the ML job results of vhost %q: %w", vhost, err)
	}
	if _, err := s.db.Collection(mlJobsCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the ML jobs of vhost %q: %w", vhost, err)
	}
	return nil
}
