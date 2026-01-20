package mongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/crypto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

func (s *mongoStorage) Init(ctx context.Context) error {
	// Create indexes
	collections := []string{"sources", "sinks", "users", "vhosts", "workflows", "workers", "logs", "settings"}

	for _, collName := range collections {
		coll := s.db.Collection(collName)

		// All collections have an ID index (default _id), but we might want to ensure other indexes
		var indexModels []mongo.IndexModel

		switch collName {
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

	var sources []storage.Source
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

	var sinks []storage.Sink
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

	var users []storage.User
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
		"_id":       user.ID,
		"username":  user.Username,
		"password":  user.Password,
		"full_name": user.FullName,
		"email":     user.Email,
		"role":      user.Role,
		"vhosts":    user.VHosts,
	})
	return err
}

func (s *mongoStorage) UpdateUser(ctx context.Context, user storage.User) error {
	coll := s.db.Collection("users")
	update := bson.M{
		"username":  user.Username,
		"full_name": user.FullName,
		"email":     user.Email,
		"role":      user.Role,
		"vhosts":    user.VHosts,
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

	var vhosts []storage.VHost
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

	var wfs []storage.Workflow
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

func (s *mongoStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	if wf.ID == "" {
		wf.ID = uuid.New().String()
	}
	coll := s.db.Collection("workflows")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":       wf.ID,
		"name":      wf.Name,
		"vhost":     wf.VHost,
		"active":    wf.Active,
		"status":    wf.Status,
		"worker_id": wf.WorkerID,
		"nodes":     wf.Nodes,
		"edges":     wf.Edges,
	})
	return err
}

func (s *mongoStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	coll := s.db.Collection("workflows")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": wf.ID}, bson.M{"$set": bson.M{
		"name":      wf.Name,
		"vhost":     wf.VHost,
		"active":    wf.Active,
		"status":    wf.Status,
		"worker_id": wf.WorkerID,
		"nodes":     wf.Nodes,
		"edges":     wf.Edges,
	}})
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

	var workers []storage.Worker
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
	coll := s.db.Collection("workers")
	_, err := coll.InsertOne(ctx, bson.M{
		"_id":         worker.ID,
		"name":        worker.Name,
		"host":        worker.Host,
		"port":        worker.Port,
		"description": worker.Description,
		"token":       worker.Token,
		"last_seen":   worker.LastSeen,
	})
	return err
}

func (s *mongoStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	coll := s.db.Collection("workers")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": worker.ID}, bson.M{"$set": bson.M{
		"name":        worker.Name,
		"host":        worker.Host,
		"port":        worker.Port,
		"description": worker.Description,
		"token":       worker.Token,
		"last_seen":   worker.LastSeen,
	}})
	return err
}

func (s *mongoStorage) UpdateWorkerHeartbeat(ctx context.Context, id string) error {
	coll := s.db.Collection("workers")
	_, err := coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"last_seen": time.Now()}})
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

	var logs []storage.Log
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
	opts := options.Update().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, bson.M{"_id": key}, bson.M{"$set": bson.M{"value": value}}, opts)
	return err
}
