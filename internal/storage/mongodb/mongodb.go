package mongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/crypto"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var sensitiveKeys = map[string]bool{
	"password":          true,
	"connection_string": true,
	"uri":               true,
	"token":             true,
	"secret":            true,
	"key":               true,
	"access_key":        true,
	"secret_key":        true,
}

func encryptConfig(config map[string]string) map[string]string {
	encrypted := make(map[string]string)
	for k, v := range config {
		if sensitiveKeys[strings.ToLower(k)] && v != "" {
			enc, err := crypto.Encrypt(v)
			if err == nil {
				encrypted[k] = "enc:" + enc
				continue
			}
		}
		encrypted[k] = v
	}
	return encrypted
}

func decryptConfig(config map[string]string) map[string]string {
	decrypted := make(map[string]string)
	for k, v := range config {
		if strings.HasPrefix(v, "enc:") {
			dec, err := crypto.Decrypt(v[4:])
			if err == nil {
				decrypted[k] = dec
				continue
			}
		}
		decrypted[k] = v
	}
	return decrypted
}

type mongoStorage struct {
	client *mongo.Client
	db     *mongo.Database
}

func NewMongoStorage(client *mongo.Client, dbName string) storage.Storage {
	return &mongoStorage{
		client: client,
		db:     client.Database(dbName),
	}
}

func (s *mongoStorage) ListApprovals(ctx context.Context, filter storage.ApprovalFilter) ([]storage.Approval, int, error) {
	coll := s.db.Collection("approvals")
	q := bson.M{}
	if filter.WorkflowID != "" {
		q["workflow_id"] = filter.WorkflowID
	}
	if filter.Status != "" {
		q["status"] = filter.Status
	}
	total64, err := coll.CountDocuments(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit)).SetSkip(int64(filter.Page * filter.Limit))
	}
	cur, err := coll.Find(ctx, q, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	var apps []storage.Approval
	for cur.Next(ctx) {
		var a storage.Approval
		if err := cur.Decode(&a); err != nil {
			return nil, 0, err
		}
		apps = append(apps, a)
	}
	return apps, int(total64), nil
}

func (s *mongoStorage) CreateApproval(ctx context.Context, app storage.Approval) error {
	if app.ID == "" {
		app.ID = uuid.New().String()
	}
	_, err := s.db.Collection("approvals").InsertOne(ctx, app)
	return err
}

func (s *mongoStorage) GetApproval(ctx context.Context, id string) (storage.Approval, error) {
	var a storage.Approval
	err := s.db.Collection("approvals").FindOne(ctx, bson.M{"id": id}).Decode(&a)
	if err == mongo.ErrNoDocuments {
		return a, storage.ErrNotFound
	}
	return a, err
}

func (s *mongoStorage) UpdateApprovalStatus(ctx context.Context, id string, status string, processedBy string, notes string) error {
	upd := bson.M{
		"$set": bson.M{
			"status":       status,
			"processed_at": time.Now(),
			"processed_by": processedBy,
			"notes":        notes,
		},
	}
	res, err := s.db.Collection("approvals").UpdateOne(ctx, bson.M{"id": id}, upd)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) DeleteApproval(ctx context.Context, id string) error {
	res, err := s.db.Collection("approvals").DeleteOne(ctx, bson.M{"id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *mongoStorage) Init(ctx context.Context) error {
	// Create indexes
	collections := []string{"sources", "sinks", "users", "vhosts", "workflows", "workers", "logs", "settings", "audit_logs", "webhook_requests", "schemas", "message_traces", "workflow_versions"}

	for _, collName := range collections {
		coll := s.db.Collection(collName)

		// All collections have an ID index (default _id), but we might want to ensure other indexes
		var indexModels []mongo.IndexModel

		switch collName {
		case "message_traces":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys: bson.D{{Key: "workflow_id", Value: 1}, {Key: "message_id", Value: 1}},
			})
			indexModels = append(indexModels, mongo.IndexModel{
				Keys: bson.D{{Key: "created_at", Value: -1}},
			})
		case "workflow_versions":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys:    bson.D{{Key: "workflow_id", Value: 1}, {Key: "version", Value: -1}},
				Options: options.Index().SetUnique(true),
			})
		case "schemas":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys:    bson.D{{Key: "name", Value: 1}, {Key: "version", Value: 1}},
				Options: options.Index().SetUnique(true),
			})
		case "sources", "sinks":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys:    bson.D{{Key: "name", Value: 1}},
				Options: options.Index().SetUnique(true),
			})
		case "users":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys:    bson.D{{Key: "username", Value: 1}},
				Options: options.Index().SetUnique(true),
			})
		case "vhosts":
			indexModels = append(indexModels, mongo.IndexModel{
				Keys:    bson.D{{Key: "name", Value: 1}},
				Options: options.Index().SetUnique(true),
			})
		case "logs":
			indexModels = append(indexModels,
				mongo.IndexModel{Keys: bson.D{{Key: "timestamp", Value: -1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "source_id", Value: 1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "sink_id", Value: 1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "workflow_id", Value: 1}}},
			)
		case "workflows":
			indexModels = append(indexModels,
				mongo.IndexModel{Keys: bson.D{{Key: "owner_id", Value: 1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "lease_until", Value: 1}}},
			)
		case "audit_logs":
			indexModels = append(indexModels,
				mongo.IndexModel{Keys: bson.D{{Key: "timestamp", Value: -1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "user_id", Value: 1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "entity_type", Value: 1}, {Key: "entity_id", Value: 1}}},
			)
		case "webhook_requests":
			indexModels = append(indexModels,
				mongo.IndexModel{Keys: bson.D{{Key: "timestamp", Value: -1}}},
				mongo.IndexModel{Keys: bson.D{{Key: "path", Value: 1}}},
			)
		}

		if len(indexModels) > 0 {
			_, err := coll.Indexes().CreateMany(ctx, indexModels)
			if err != nil {
				return fmt.Errorf("failed to create indexes for %s: %w", collName, err)
			}
		}
	}

	return nil
}

// --- Lease APIs (MongoDB) ---
// Implement atomic lease operations using findOneAndUpdate with conditional filters.
func (s *mongoStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	coll := s.db.Collection("workflows")
	now := time.Now().UTC()
	until := now.Add(time.Duration(ttlSeconds) * time.Second)

	filter := bson.M{
		"id": workflowID,
		"$or": []bson.M{
			{"owner_id": bson.M{"$exists": false}},
			{"owner_id": nil},
			{"lease_until": bson.M{"$exists": false}},
			{"lease_until": nil},
			{"lease_until": bson.M{"$lt": now}},
			{"owner_id": ownerID},
		},
	}
	update := bson.M{"$set": bson.M{"owner_id": ownerID, "lease_until": until}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	res := coll.FindOneAndUpdate(ctx, filter, update, opts)
	if res.Err() == mongo.ErrNoDocuments {
		return false, nil
	}
	if err := res.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *mongoStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	coll := s.db.Collection("workflows")
	now := time.Now().UTC()
	until := now.Add(time.Duration(ttlSeconds) * time.Second)

	filter := bson.M{"id": workflowID, "owner_id": ownerID, "lease_until": bson.M{"$gte": now}}
	update := bson.M{"$set": bson.M{"lease_until": until}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	res := coll.FindOneAndUpdate(ctx, filter, update, opts)
	if res.Err() == mongo.ErrNoDocuments {
		return false, nil
	}
	if err := res.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *mongoStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	coll := s.db.Collection("workflows")
	filter := bson.M{"id": workflowID, "owner_id": ownerID}
	update := bson.M{"$unset": bson.M{"owner_id": "", "lease_until": ""}}
	_, err := coll.UpdateOne(ctx, filter, update)
	return err
}

func (s *mongoStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	coll := s.db.Collection("sources")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"type": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"vhost": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	if filter.VHost != "" {
		query["vhost"] = filter.VHost
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	sources := []storage.Source{}
	for cursor.Next(ctx) {
		var src struct {
			storage.Source `bson:",inline"`
			ID             string `bson:"_id"`
		}
		if err := cursor.Decode(&src); err != nil {
			return nil, 0, err
		}
		src.Source.ID = src.ID
		src.Source.Config = decryptConfig(src.Source.Config)
		sources = append(sources, src.Source)
	}

	return sources, int(total), nil
}

func (s *mongoStorage) CreateSource(ctx context.Context, src storage.Source) error {
	if src.ID == "" {
		src.ID = uuid.New().String()
	}
	src.Config = encryptConfig(src.Config)

	coll := s.db.Collection("sources")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":       src.ID,
		"name":      src.Name,
		"type":      src.Type,
		"vhost":     src.VHost,
		"active":    src.Active,
		"status":    src.Status,
		"worker_id": src.WorkerID,
		"config":    src.Config,
		"sample":    src.Sample,
		"state":     src.State,
	})
	return err
}

func (s *mongoStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	src.Config = encryptConfig(src.Config)
	coll := s.db.Collection("sources")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": src.ID}, bson.M{"$set": bson.M{
		"name":      src.Name,
		"type":      src.Type,
		"vhost":     src.VHost,
		"active":    src.Active,
		"status":    src.Status,
		"worker_id": src.WorkerID,
		"config":    src.Config,
		"sample":    src.Sample,
		"state":     src.State,
	}})
	return err
}

func (s *mongoStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	coll := s.db.Collection("sources")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"state": state}})
	return err
}

func (s *mongoStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	coll := s.db.Collection("sources")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}

func (s *mongoStorage) DeleteSource(ctx context.Context, id string) error {
	coll := s.db.Collection("sources")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	coll := s.db.Collection("sources")
	var src struct {
		storage.Source `bson:",inline"`
		ID             string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&src)
	if err == mongo.ErrNoDocuments {
		return storage.Source{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Source{}, err
	}
	src.Source.ID = src.ID
	src.Source.Config = decryptConfig(src.Source.Config)
	return src.Source, nil
}

func (s *mongoStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	coll := s.db.Collection("sinks")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"type": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"vhost": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	if filter.VHost != "" {
		query["vhost"] = filter.VHost
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	sinks := []storage.Sink{}
	for cursor.Next(ctx) {
		var snk struct {
			storage.Sink `bson:",inline"`
			ID           string `bson:"_id"`
		}
		if err := cursor.Decode(&snk); err != nil {
			return nil, 0, err
		}
		snk.Sink.ID = snk.ID
		snk.Sink.Config = decryptConfig(snk.Sink.Config)
		sinks = append(sinks, snk.Sink)
	}

	return sinks, int(total), nil
}

func (s *mongoStorage) CreateSink(ctx context.Context, snk storage.Sink) error {
	if snk.ID == "" {
		snk.ID = uuid.New().String()
	}
	snk.Config = encryptConfig(snk.Config)

	coll := s.db.Collection("sinks")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":       snk.ID,
		"name":      snk.Name,
		"type":      snk.Type,
		"vhost":     snk.VHost,
		"active":    snk.Active,
		"status":    snk.Status,
		"worker_id": snk.WorkerID,
		"config":    snk.Config,
	})
	return err
}

func (s *mongoStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	snk.Config = encryptConfig(snk.Config)
	coll := s.db.Collection("sinks")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": snk.ID}, bson.M{"$set": bson.M{
		"name":      snk.Name,
		"type":      snk.Type,
		"vhost":     snk.VHost,
		"active":    snk.Active,
		"status":    snk.Status,
		"worker_id": snk.WorkerID,
		"config":    snk.Config,
	}})
	return err
}

func (s *mongoStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	coll := s.db.Collection("sinks")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}

func (s *mongoStorage) DeleteSink(ctx context.Context, id string) error {
	coll := s.db.Collection("sinks")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	coll := s.db.Collection("sinks")
	var snk struct {
		storage.Sink `bson:",inline"`
		ID           string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&snk)
	if err == mongo.ErrNoDocuments {
		return storage.Sink{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Sink{}, err
	}
	snk.Sink.ID = snk.ID
	snk.Sink.Config = decryptConfig(snk.Sink.Config)
	return snk.Sink, nil
}

func (s *mongoStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	coll := s.db.Collection("users")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"username": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"full_name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"email": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	users := []storage.User{}
	for cursor.Next(ctx) {
		var u struct {
			storage.User `bson:",inline"`
			ID           string `bson:"_id"`
		}
		if err := cursor.Decode(&u); err != nil {
			return nil, 0, err
		}
		u.User.ID = u.ID
		u.User.Password = "" // Don't return password
		users = append(users, u.User)
	}

	return users, int(total), nil
}

func (s *mongoStorage) CreateUser(ctx context.Context, user storage.User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	coll := s.db.Collection("users")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":                user.ID,
		"username":           user.Username,
		"password":           user.Password,
		"full_name":          user.FullName,
		"email":              user.Email,
		"role":               user.Role,
		"vhosts":             user.VHosts,
		"two_factor_enabled": user.TwoFactorEnabled,
		"two_factor_secret":  user.TwoFactorSecret,
	})
	return err
}

func (s *mongoStorage) UpdateUser(ctx context.Context, user storage.User) error {
	coll := s.db.Collection("users")
	update := bson.M{
		"username":           user.Username,
		"full_name":          user.FullName,
		"email":              user.Email,
		"role":               user.Role,
		"vhosts":             user.VHosts,
		"two_factor_enabled": user.TwoFactorEnabled,
		"two_factor_secret":  user.TwoFactorSecret,
	}
	if user.Password != "" {
		update["password"] = user.Password
	}
	_, err := coll.UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{"$set": update})
	return err
}

func (s *mongoStorage) DeleteUser(ctx context.Context, id string) error {
	coll := s.db.Collection("users")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	coll := s.db.Collection("users")
	var u struct {
		storage.User `bson:",inline"`
		ID           string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	u.User.ID = u.ID
	return u.User, nil
}

func (s *mongoStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	coll := s.db.Collection("users")
	var u struct {
		storage.User `bson:",inline"`
		ID           string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"username": username}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	u.User.ID = u.ID
	return u.User, nil
}

func (s *mongoStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	coll := s.db.Collection("users")
	var u struct {
		storage.User `bson:",inline"`
		ID           string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"email": email}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	u.User.ID = u.ID
	return u.User, nil
}

func (s *mongoStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	coll := s.db.Collection("vhosts")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"description": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	vhosts := []storage.VHost{}
	for cursor.Next(ctx) {
		var v struct {
			storage.VHost `bson:",inline"`
			ID            string `bson:"_id"`
		}
		if err := cursor.Decode(&v); err != nil {
			return nil, 0, err
		}
		v.VHost.ID = v.ID
		vhosts = append(vhosts, v.VHost)
	}

	return vhosts, int(total), nil
}

func (s *mongoStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error {
	if vhost.ID == "" {
		vhost.ID = uuid.New().String()
	}
	coll := s.db.Collection("vhosts")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":         vhost.ID,
		"name":        vhost.Name,
		"description": vhost.Description,
	})
	return err
}

func (s *mongoStorage) DeleteVHost(ctx context.Context, id string) error {
	coll := s.db.Collection("vhosts")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	coll := s.db.Collection("vhosts")
	var v struct {
		storage.VHost `bson:",inline"`
		ID            string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&v)
	if err == mongo.ErrNoDocuments {
		return storage.VHost{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.VHost{}, err
	}
	v.VHost.ID = v.ID
	return v.VHost, nil
}

func (s *mongoStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	coll := s.db.Collection("workflows")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"vhost": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	if filter.VHost != "" {
		query["vhost"] = filter.VHost
	}

	if filter.WorkspaceID != "" {
		query["workspace_id"] = filter.WorkspaceID
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	wfs := []storage.Workflow{}
	for cursor.Next(ctx) {
		var wf struct {
			storage.Workflow `bson:",inline"`
			ID               string `bson:"_id"`
		}
		if err := cursor.Decode(&wf); err != nil {
			return nil, 0, err
		}
		wf.Workflow.ID = wf.ID
		wfs = append(wfs, wf.Workflow)
	}

	return wfs, int(total), nil
}

func (s *mongoStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	cursor, err := s.db.Collection("workspaces").Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"name": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	wss := []storage.Workspace{}
	if err := cursor.All(ctx, &wss); err != nil {
		return nil, err
	}
	return wss, nil
}

func (s *mongoStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	if ws.ID == "" {
		ws.ID = bson.NewObjectID().Hex()
	}
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = time.Now()
	}
	_, err := s.db.Collection("workspaces").InsertOne(ctx, ws)
	return err
}

func (s *mongoStorage) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := s.db.Collection("workspaces").DeleteOne(ctx, bson.M{"id": id})
	return err
}

func (s *mongoStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	var ws storage.Workspace
	err := s.db.Collection("workspaces").FindOne(ctx, bson.M{"id": id}).Decode(&ws)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return storage.Workspace{}, storage.ErrNotFound
		}
		return storage.Workspace{}, err
	}
	return ws, nil
}

func (s *mongoStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	if wf.ID == "" {
		wf.ID = uuid.New().String()
	}
	coll := s.db.Collection("workflows")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":                 wf.ID,
		"name":                wf.Name,
		"vhost":               wf.VHost,
		"active":              wf.Active,
		"status":              wf.Status,
		"worker_id":           wf.WorkerID,
		"nodes":               wf.Nodes,
		"edges":               wf.Edges,
		"dead_letter_sink_id": wf.DeadLetterSinkID,
		"prioritize_dlq":      wf.PrioritizeDLQ,
		"max_retries":         wf.MaxRetries,
		"retry_interval":      wf.RetryInterval,
		"reconnect_interval":  wf.ReconnectInterval,
		"dry_run":             wf.DryRun,
		"schema_type":         wf.SchemaType,
		"schema":              wf.Schema,
		"cron":                wf.Cron,
		"idle_timeout":        wf.IdleTimeout,
		"tier":                wf.Tier,
		"retention_days":      wf.RetentionDays,
		"dlq_threshold":       wf.DLQThreshold,
		"trace_sample_rate":   wf.TraceSampleRate,
		"tags":                wf.Tags,
		"workspace_id":        wf.WorkspaceID,
		"trace_retention":     wf.TraceRetention,
		"audit_retention":     wf.AuditRetention,
		"cpu_request":         wf.CPURequest,
		"memory_request":      wf.MemoryRequest,
		"throughput_request":  wf.ThroughputRequest,
	})
	return err
}

func (s *mongoStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	coll := s.db.Collection("workflows")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": wf.ID}, bson.M{"$set": bson.M{
		"name":                wf.Name,
		"vhost":               wf.VHost,
		"active":              wf.Active,
		"status":              wf.Status,
		"worker_id":           wf.WorkerID,
		"nodes":               wf.Nodes,
		"edges":               wf.Edges,
		"dead_letter_sink_id": wf.DeadLetterSinkID,
		"prioritize_dlq":      wf.PrioritizeDLQ,
		"max_retries":         wf.MaxRetries,
		"retry_interval":      wf.RetryInterval,
		"reconnect_interval":  wf.ReconnectInterval,
		"dry_run":             wf.DryRun,
		"schema_type":         wf.SchemaType,
		"schema":              wf.Schema,
		"cron":                wf.Cron,
		"idle_timeout":        wf.IdleTimeout,
		"tier":                wf.Tier,
		"retention_days":      wf.RetentionDays,
		"dlq_threshold":       wf.DLQThreshold,
		"trace_sample_rate":   wf.TraceSampleRate,
		"tags":                wf.Tags,
		"workspace_id":        wf.WorkspaceID,
		"trace_retention":     wf.TraceRetention,
		"audit_retention":     wf.AuditRetention,
		"cpu_request":         wf.CPURequest,
		"memory_request":      wf.MemoryRequest,
		"throughput_request":  wf.ThroughputRequest,
	}})
	return err
}

func (s *mongoStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	coll := s.db.Collection("workflows")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}

func (s *mongoStorage) DeleteWorkflow(ctx context.Context, id string) error {
	coll := s.db.Collection("workflows")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	coll := s.db.Collection("workflows")
	var wf struct {
		storage.Workflow `bson:",inline"`
		ID               string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&wf)
	if err == mongo.ErrNoDocuments {
		return storage.Workflow{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Workflow{}, err
	}
	wf.Workflow.ID = wf.ID
	return wf.Workflow, nil
}

func (s *mongoStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	coll := s.db.Collection("workers")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"name": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"host": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find()
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	workers := []storage.Worker{}
	for cursor.Next(ctx) {
		var w struct {
			storage.Worker `bson:",inline"`
			ID             string `bson:"_id"`
		}
		if err := cursor.Decode(&w); err != nil {
			return nil, 0, err
		}
		w.Worker.ID = w.ID
		workers = append(workers, w.Worker)
	}

	return workers, int(total), nil
}

func (s *mongoStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	if worker.ID == "" {
		worker.ID = uuid.New().String()
	}
	if worker.Token == "" {
		worker.Token = uuid.New().String()
	}
	coll := s.db.Collection("workers")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":          worker.ID,
		"name":         worker.Name,
		"host":         worker.Host,
		"port":         worker.Port,
		"description":  worker.Description,
		"token":        worker.Token,
		"last_seen":    worker.LastSeen,
		"cpu_usage":    worker.CPUUsage,
		"memory_usage": worker.MemoryUsage,
	})
	return err
}

func (s *mongoStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	coll := s.db.Collection("workers")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": worker.ID}, bson.M{"$set": bson.M{
		"name":         worker.Name,
		"host":         worker.Host,
		"port":         worker.Port,
		"description":  worker.Description,
		"token":        worker.Token,
		"last_seen":    worker.LastSeen,
		"cpu_usage":    worker.CPUUsage,
		"memory_usage": worker.MemoryUsage,
	}})
	return err
}

func (s *mongoStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	coll := s.db.Collection("workers")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"last_seen":    time.Now(),
		"cpu_usage":    cpu,
		"memory_usage": mem,
	}})
	return err
}

func (s *mongoStorage) DeleteWorker(ctx context.Context, id string) error {
	coll := s.db.Collection("workers")
	_, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (s *mongoStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	coll := s.db.Collection("workers")
	var w struct {
		storage.Worker `bson:",inline"`
		ID             string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&w)
	if err == mongo.ErrNoDocuments {
		return storage.Worker{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Worker{}, err
	}
	w.Worker.ID = w.ID
	return w.Worker, nil
}

func (s *mongoStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	coll := s.db.Collection("logs")
	query := bson.M{}

	// Time bounds (if provided)
	if !filter.Since.IsZero() || !filter.Until.IsZero() {
		ts := bson.M{}
		if !filter.Since.IsZero() {
			ts["$gte"] = filter.Since
		}
		if !filter.Until.IsZero() {
			ts["$lt"] = filter.Until
		}
		query["timestamp"] = ts
	}

	if filter.SourceID != "" {
		query["source_id"] = filter.SourceID
	}
	if filter.SinkID != "" {
		query["sink_id"] = filter.SinkID
	}
	if filter.WorkflowID != "" {
		query["workflow_id"] = filter.WorkflowID
	}
	if filter.Level != "" {
		query["level"] = filter.Level
	}
	if filter.Action != "" {
		query["action"] = filter.Action
	}
	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"message": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"data": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"source_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"sink_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"workflow_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"user_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"username": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}})
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	} else {
		opts.SetLimit(100)
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	logs := []storage.Log{}
	for cursor.Next(ctx) {
		var l struct {
			storage.Log `bson:",inline"`
			ID          string `bson:"_id"`
		}
		if err := cursor.Decode(&l); err != nil {
			return nil, 0, err
		}
		l.Log.ID = l.ID
		logs = append(logs, l.Log)
	}

	return logs, int(total), nil
}

func (s *mongoStorage) CreateLog(ctx context.Context, l storage.Log) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = time.Now()
	}

	coll := s.db.Collection("logs")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":         l.ID,
		"timestamp":   l.Timestamp,
		"level":       l.Level,
		"message":     l.Message,
		"action":      l.Action,
		"source_id":   l.SourceID,
		"sink_id":     l.SinkID,
		"workflow_id": l.WorkflowID,
		"user_id":     l.UserID,
		"username":    l.Username,
		"data":        l.Data,
	})
	return err
}

func (s *mongoStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	coll := s.db.Collection("logs")
	query := bson.M{}

	if filter.SourceID != "" {
		query["source_id"] = filter.SourceID
	}
	if filter.SinkID != "" {
		query["sink_id"] = filter.SinkID
	}
	if filter.WorkflowID != "" {
		query["workflow_id"] = filter.WorkflowID
	}
	if filter.Level != "" {
		query["level"] = filter.Level
	}
	if filter.Action != "" {
		query["action"] = filter.Action
	}
	if !filter.Since.IsZero() || !filter.Until.IsZero() {
		ts := bson.M{}
		if !filter.Since.IsZero() {
			ts["$gte"] = filter.Since
		}
		if !filter.Until.IsZero() {
			ts["$lt"] = filter.Until
		}
		query["timestamp"] = ts
	}

	_, err := coll.DeleteMany(ctx, query)
	return err
}

func (s *mongoStorage) GetSetting(ctx context.Context, key string) (string, error) {
	coll := s.db.Collection("settings")
	var res struct {
		Value string `bson:"value"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": key}).Decode(&res)
	if err == mongo.ErrNoDocuments {
		return "", nil
	}
	return res.Value, err
}

func (s *mongoStorage) SaveSetting(ctx context.Context, key string, value string) error {
	coll := s.db.Collection("settings")
	opts := options.UpdateOne().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, bson.M{"_id": key}, bson.M{"$set": bson.M{"value": value}}, opts)
	return err
}

func (s *mongoStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	if log.ID == "" {
		log.ID = uuid.NewString()
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now().UTC()
	}
	coll := s.db.Collection("audit_logs")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":         log.ID,
		"timestamp":   log.Timestamp,
		"user_id":     log.UserID,
		"username":    log.Username,
		"action":      log.Action,
		"entity_type": log.EntityType,
		"entity_id":   log.EntityID,
		"payload":     log.Payload,
		"ip":          log.IP,
	})
	return err
}

func (s *mongoStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error {
	coll := s.db.Collection("audit_logs")
	_, err := coll.DeleteMany(ctx, bson.M{"timestamp": bson.M{"$lt": before}})
	return err
}

func (s *mongoStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error {
	coll := s.db.Collection("trace_steps")
	_, err := coll.DeleteMany(ctx, bson.M{"timestamp": bson.M{"$lt": before}})
	return err
}

func (s *mongoStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}
	coll := s.db.Collection("webhook_requests")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":       req.ID,
		"timestamp": req.Timestamp,
		"path":      req.Path,
		"method":    req.Method,
		"headers":   req.Headers,
		"body":      req.Body,
	})
	if err != nil {
		return err
	}

	// Keep only last 50 requests per path
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetSkip(50).SetProjection(bson.M{"_id": 1})
	cursor, err := coll.Find(ctx, bson.M{"path": req.Path}, opts)
	if err == nil {
		defer cursor.Close(ctx)
		var toDelete []string
		for cursor.Next(ctx) {
			var d struct {
				ID string `bson:"_id"`
			}
			if err := cursor.Decode(&d); err == nil {
				toDelete = append(toDelete, d.ID)
			}
		}
		if len(toDelete) > 0 {
			_, _ = coll.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": toDelete}})
		}
	}

	return nil
}

func (s *mongoStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	coll := s.db.Collection("webhook_requests")
	query := bson.M{}
	if filter.Path != "" {
		query["path"] = filter.Path
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}})
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	} else {
		opts.SetLimit(100)
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	requests := []storage.WebhookRequest{}
	for cursor.Next(ctx) {
		var r struct {
			storage.WebhookRequest `bson:",inline"`
			ID                     string `bson:"_id"`
		}
		if err := cursor.Decode(&r); err != nil {
			return nil, 0, err
		}
		r.WebhookRequest.ID = r.ID
		requests = append(requests, r.WebhookRequest)
	}
	return requests, int(total), nil
}

func (s *mongoStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	coll := s.db.Collection("webhook_requests")
	var r struct {
		storage.WebhookRequest `bson:",inline"`
		ID                     string `bson:"_id"`
	}
	err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&r)
	if err == mongo.ErrNoDocuments {
		return storage.WebhookRequest{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.WebhookRequest{}, err
	}
	r.WebhookRequest.ID = r.ID
	return r.WebhookRequest, nil
}

func (s *mongoStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	coll := s.db.Collection("webhook_requests")
	query := bson.M{}
	if filter.Path != "" {
		query["path"] = filter.Path
	}
	_, err := coll.DeleteMany(ctx, query)
	return err
}

func (s *mongoStorage) CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error {
	coll := s.db.Collection("form_submissions")
	_, err := coll.InsertOne(ctx, sub)
	return err
}

func (s *mongoStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	coll := s.db.Collection("form_submissions")
	query := bson.M{}
	if filter.Path != "" {
		query["path"] = filter.Path
	}
	if filter.Status != "" {
		query["status"] = filter.Status
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var submissions []storage.FormSubmission
	if err := cursor.All(ctx, &submissions); err != nil {
		return nil, 0, err
	}
	return submissions, int(total), nil
}

func (s *mongoStorage) GetFormSubmission(ctx context.Context, id string) (storage.FormSubmission, error) {
	coll := s.db.Collection("form_submissions")
	var sub storage.FormSubmission
	err := coll.FindOne(ctx, bson.M{"id": id}).Decode(&sub)
	return sub, err
}

func (s *mongoStorage) UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error {
	coll := s.db.Collection("form_submissions")
	_, err := coll.UpdateOne(ctx, bson.M{"id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}

func (s *mongoStorage) DeleteFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) error {
	coll := s.db.Collection("form_submissions")
	query := bson.M{}
	if filter.Path != "" {
		query["path"] = filter.Path
	}
	if filter.Status != "" {
		query["status"] = filter.Status
	}
	_, err := coll.DeleteMany(ctx, query)
	return err
}

func (s *mongoStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	coll := s.db.Collection("audit_logs")
	query := bson.M{}

	if filter.Search != "" {
		query["$or"] = []bson.M{
			{"_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"username": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"action": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"entity_id": bson.M{"$regex": filter.Search, "$options": "i"}},
			{"payload": bson.M{"$regex": filter.Search, "$options": "i"}},
		}
	}
	if filter.UserID != "" {
		query["user_id"] = filter.UserID
	}
	if filter.EntityType != "" {
		query["entity_type"] = filter.EntityType
	}
	if filter.EntityID != "" {
		query["entity_id"] = filter.EntityID
	}
	if filter.Action != "" {
		query["action"] = filter.Action
	}
	if filter.From != nil || filter.To != nil {
		tsQuery := bson.M{}
		if filter.From != nil {
			tsQuery["$gte"] = *filter.From
		}
		if filter.To != nil {
			tsQuery["$lte"] = *filter.To
		}
		query["timestamp"] = tsQuery
	}

	total, err := coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}})
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
		if filter.Page > 0 {
			opts.SetSkip(int64((filter.Page - 1) * filter.Limit))
		}
	}

	cursor, err := coll.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	logs := []storage.AuditLog{}
	for cursor.Next(ctx) {
		var l struct {
			storage.AuditLog `bson:",inline"`
			ID               string `bson:"_id"`
		}
		if err := cursor.Decode(&l); err != nil {
			return nil, 0, err
		}
		l.AuditLog.ID = l.ID
		logs = append(logs, l.AuditLog)
	}

	return logs, int(total), nil
}

func (s *mongoStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	coll := s.db.Collection("node_states")
	opts := options.UpdateOne().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, bson.M{"workflow_id": workflowID, "node_id": nodeID}, bson.M{"$set": bson.M{"state": state, "updated_at": time.Now()}}, opts)
	return err
}

func (s *mongoStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	coll := s.db.Collection("node_states")
	cursor, err := coll.Find(ctx, bson.M{"workflow_id": workflowID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	states := make(map[string]any)
	for cursor.Next(ctx) {
		var res struct {
			NodeID string `bson:"node_id"`
			State  any    `bson:"state"`
		}
		if err := cursor.Decode(&res); err != nil {
			return nil, err
		}
		states[res.NodeID] = res.State
	}
	return states, nil
}

func (s *mongoStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	coll := s.db.Collection("schemas")
	filter := bson.M{"name": name}
	opts := options.Find().SetSort(bson.D{{Key: "version", Value: -1}})
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	schemas := []storage.Schema{}
	if err := cursor.All(ctx, &schemas); err != nil {
		return nil, err
	}
	return schemas, nil
}

func (s *mongoStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	coll := s.db.Collection("schemas")
	pipeline := mongo.Pipeline{
		{{Key: "$sort", Value: bson.D{{Key: "name", Value: 1}, {Key: "version", Value: -1}}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$name"},
			{Key: "latest", Value: bson.M{"$first": "$$ROOT"}},
		}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$latest"}}},
		{{Key: "$sort", Value: bson.D{{Key: "name", Value: 1}}}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	schemas := []storage.Schema{}
	if err := cursor.All(ctx, &schemas); err != nil {
		return nil, err
	}
	return schemas, nil
}

func (s *mongoStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	coll := s.db.Collection("schemas")
	filter := bson.M{"name": name, "version": version}
	var sc storage.Schema
	err := coll.FindOne(ctx, filter).Decode(&sc)
	if err == mongo.ErrNoDocuments {
		return storage.Schema{}, fmt.Errorf("schema %s version %d not found", name, version)
	}
	return sc, err
}

func (s *mongoStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	coll := s.db.Collection("schemas")
	filter := bson.M{"name": name}
	opts := options.FindOne().SetSort(bson.D{{Key: "version", Value: -1}})
	var sc storage.Schema
	err := coll.FindOne(ctx, filter, opts).Decode(&sc)
	if err == mongo.ErrNoDocuments {
		return storage.Schema{}, fmt.Errorf("schema %s not found", name)
	}
	return sc, err
}

func (s *mongoStorage) CreateSchema(ctx context.Context, sc storage.Schema) error {
	coll := s.db.Collection("schemas")
	if sc.ID == "" {
		sc.ID = uuid.New().String()
	}
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now()
	}
	_, err := coll.InsertOne(ctx, sc)
	return err
}

func (s *mongoStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error {
	coll := s.db.Collection("message_traces")
	filter := bson.M{"workflow_id": workflowID, "message_id": messageID}

	update := bson.M{
		"$push": bson.M{"steps": step},
		"$setOnInsert": bson.M{
			"id":          uuid.New().String(),
			"workflow_id": workflowID,
			"message_id":  messageID,
			"created_at":  time.Now(),
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, filter, update, opts)
	return err
}

func (s *mongoStorage) CreateMessageTrace(ctx context.Context, tr storage.MessageTrace) error {
	coll := s.db.Collection("message_traces")
	if tr.ID == "" {
		tr.ID = uuid.New().String()
	}
	if tr.CreatedAt.IsZero() {
		tr.CreatedAt = time.Now()
	}
	_, err := coll.InsertOne(ctx, tr)
	return err
}

func (s *mongoStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	coll := s.db.Collection("message_traces")
	filter := bson.M{"workflow_id": workflowID, "message_id": messageID}
	var tr storage.MessageTrace
	err := coll.FindOne(ctx, filter).Decode(&tr)
	if err == mongo.ErrNoDocuments {
		return storage.MessageTrace{}, storage.ErrNotFound
	}
	return tr, err
}

func (s *mongoStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	coll := s.db.Collection("message_traces")
	filter := bson.M{"workflow_id": workflowID}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit))
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	traces := []storage.MessageTrace{}
	if err := cursor.All(ctx, &traces); err != nil {
		return nil, err
	}
	return traces, nil
}

func (s *mongoStorage) CreateWorkflowVersion(ctx context.Context, v storage.WorkflowVersion) error {
	coll := s.db.Collection("workflow_versions")
	_, err := coll.InsertOne(ctx, v)
	return err
}

func (s *mongoStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	coll := s.db.Collection("workflow_versions")
	filter := bson.M{"workflow_id": workflowID}
	opts := options.Find().SetSort(bson.D{{Key: "version", Value: -1}})
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	versions := []storage.WorkflowVersion{}
	if err := cursor.All(ctx, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

func (s *mongoStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	coll := s.db.Collection("workflow_versions")
	filter := bson.M{"workflow_id": workflowID, "version": version}
	var v storage.WorkflowVersion
	err := coll.FindOne(ctx, filter).Decode(&v)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return v, storage.ErrNotFound
		}
		return v, err
	}
	return v, nil
}

func (s *mongoStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	coll := s.db.Collection("outbox")
	if item.ID == "" {
		item.ID = uuid.New().String()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if item.Status == "" {
		item.Status = "pending"
	}
	_, err := coll.InsertOne(ctx, item)
	return err
}

func (s *mongoStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	coll := s.db.Collection("outbox")
	filter := bson.M{"status": status}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit))
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	items := []storage.OutboxItem{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *mongoStorage) DeleteOutboxItem(ctx context.Context, id string) error {
	coll := s.db.Collection("outbox")
	_, err := coll.DeleteOne(ctx, bson.M{"id": id})
	return err
}

func (s *mongoStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	coll := s.db.Collection("outbox")
	filter := bson.M{"id": item.ID}
	update := bson.M{
		"$set": bson.M{
			"attempts":   item.Attempts,
			"last_error": item.LastError,
			"status":     item.Status,
		},
	}
	_, err := coll.UpdateOne(ctx, filter, update)
	return err
}

func (s *mongoStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	// For MongoDB, we'll use the same logic of fetching and mapping
	workflows, _, err := s.ListWorkflows(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}

	sources, _, err := s.ListSources(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	srcMap := make(map[string]storage.Source)
	for _, src := range sources {
		srcMap[src.ID] = src
	}

	sinks, _, err := s.ListSinks(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	snkMap := make(map[string]storage.Sink)
	for _, snk := range sinks {
		snkMap[snk.ID] = snk
	}

	lineage := []storage.LineageEdge{}
	for _, wf := range workflows {
		wfSources := []storage.Source{}
		wfSinks := []storage.Sink{}

		for _, node := range wf.Nodes {
			if node.Type == "source" {
				if src, ok := srcMap[node.RefID]; ok {
					wfSources = append(wfSources, src)
				}
			} else if node.Type == "sink" {
				if snk, ok := snkMap[node.RefID]; ok {
					wfSinks = append(wfSinks, snk)
				}
			}
		}

		for _, src := range wfSources {
			for _, snk := range wfSinks {
				lineage = append(lineage, storage.LineageEdge{
					SourceID:     src.ID,
					SourceName:   src.Name,
					SourceType:   src.Type,
					SinkID:       snk.ID,
					SinkName:     snk.Name,
					SinkType:     snk.Type,
					WorkflowID:   wf.ID,
					WorkflowName: wf.Name,
				})
			}
		}
	}

	return lineage, nil
}

func (s *mongoStorage) ListPlugins(ctx context.Context) ([]storage.Plugin, error) {
	return nil, nil
}

func (s *mongoStorage) GetPlugin(ctx context.Context, id string) (storage.Plugin, error) {
	return storage.Plugin{}, nil
}

func (s *mongoStorage) InstallPlugin(ctx context.Context, id string) error {
	return nil
}

func (s *mongoStorage) UninstallPlugin(ctx context.Context, id string) error {
	return nil
}
