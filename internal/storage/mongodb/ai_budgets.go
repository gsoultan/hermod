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

const (
	aiBudgetsCollection = "ai_budgets"
	aiUsageCollection   = "ai_usage"
)

// aiBudgetDoc is the stored budget; _id is the vhost.
type aiBudgetDoc struct {
	VHost         string                  `bson:"_id"`
	Disabled      bool                    `bson:"disabled"`
	MonthlyTokens int64                   `bson:"monthly_tokens"`
	MonthlyCost   float64                 `bson:"monthly_cost"`
	Currency      string                  `bson:"currency,omitempty"`
	Prices        []storage.AIModelPrice  `bson:"prices,omitempty"`
	Workflows     []storage.AIWorkflowCap `bson:"workflows,omitempty"`
	UpdatedBy     string                  `bson:"updated_by"`
	UpdatedAt     time.Time               `bson:"updated_at"`
}

// aiUsageDoc is one scope's month; _id is storage.AIUsageID.
type aiUsageDoc struct {
	ID             string     `bson:"_id"`
	VHost          string     `bson:"vhost"`
	Period         string     `bson:"period"`
	WorkflowID     string     `bson:"workflow_id"`
	Calls          int64      `bson:"calls"`
	InputTokens    int64      `bson:"input_tokens"`
	OutputTokens   int64      `bson:"output_tokens"`
	CostMicros     int64      `bson:"cost_micros"`
	TokensWarnedAt *time.Time `bson:"tokens_warned_at,omitempty"`
	CostWarnedAt   *time.Time `bson:"cost_warned_at,omitempty"`
	UpdatedAt      time.Time  `bson:"updated_at"`
}

func (d aiUsageDoc) usage() storage.AIUsage {
	return storage.AIUsage{
		VHost: d.VHost, Period: d.Period, WorkflowID: d.WorkflowID,
		Calls: d.Calls, InputTokens: d.InputTokens, OutputTokens: d.OutputTokens, CostMicros: d.CostMicros,
		TokensWarned: d.TokensWarnedAt != nil, CostWarned: d.CostWarnedAt != nil, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStorage) GetAIBudget(ctx context.Context, vhost string) (storage.AIBudget, error) {
	var d aiBudgetDoc
	err := s.db.Collection(aiBudgetsCollection).FindOne(ctx, bson.M{"_id": vhost}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.AIBudget{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AIBudget{}, fmt.Errorf("reading the AI budget of vhost %q: %w", vhost, err)
	}
	return storage.AIBudget{
		VHost: d.VHost, Disabled: d.Disabled, MonthlyTokens: d.MonthlyTokens, MonthlyCost: d.MonthlyCost,
		Currency: d.Currency, Prices: d.Prices, Workflows: d.Workflows, UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt,
	}, nil
}

func (s *mongoStorage) PutAIBudget(ctx context.Context, b storage.AIBudget) error {
	if err := storage.ValidateAIBudget(b); err != nil {
		return err
	}
	d := aiBudgetDoc{
		VHost: b.VHost, Disabled: b.Disabled, MonthlyTokens: b.MonthlyTokens, MonthlyCost: b.MonthlyCost,
		Currency: b.Currency, Prices: b.Prices, Workflows: b.Workflows, UpdatedBy: b.UpdatedBy, UpdatedAt: time.Now().UTC(),
	}
	_, err := s.db.Collection(aiBudgetsCollection).ReplaceOne(ctx, bson.M{"_id": b.VHost}, d, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("saving the AI budget of vhost %q: %w", b.VHost, err)
	}
	return nil
}

// AddAIUsage is one upserting $inc, which MongoDB applies atomically to the
// document, so concurrent callers on any replica never lose each other's calls.
func (s *mongoStorage) AddAIUsage(ctx context.Context, vhost, period, workflowID string, d storage.AIUsageDelta) (storage.AIUsage, error) {
	var doc aiUsageDoc
	err := s.db.Collection(aiUsageCollection).FindOneAndUpdate(ctx,
		bson.M{"_id": storage.AIUsageID(vhost, period, workflowID)},
		bson.M{
			"$inc":         bson.M{"calls": 1, "input_tokens": d.InputTokens, "output_tokens": d.OutputTokens, "cost_micros": d.CostMicros},
			"$set":         bson.M{"updated_at": time.Now().UTC()},
			"$setOnInsert": bson.M{"vhost": vhost, "period": period, "workflow_id": workflowID},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&doc)
	if err != nil {
		return storage.AIUsage{}, fmt.Errorf("adding to the AI usage of vhost %q: %w", vhost, err)
	}
	return doc.usage(), nil
}

func (s *mongoStorage) GetAIUsage(ctx context.Context, vhost, period, workflowID string) (storage.AIUsage, error) {
	var doc aiUsageDoc
	err := s.db.Collection(aiUsageCollection).FindOne(ctx, bson.M{"_id": storage.AIUsageID(vhost, period, workflowID)}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return storage.AIUsage{VHost: vhost, Period: period, WorkflowID: workflowID}, nil
	}
	if err != nil {
		return storage.AIUsage{}, fmt.Errorf("reading the AI usage of vhost %q: %w", vhost, err)
	}
	return doc.usage(), nil
}

func (s *mongoStorage) ListAIUsage(ctx context.Context, vhost, period string) ([]storage.AIUsage, error) {
	cur, err := s.db.Collection(aiUsageCollection).Find(ctx, bson.M{"vhost": vhost, "period": period},
		options.Find().SetSort(bson.D{{Key: "workflow_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("listing the AI usage of vhost %q: %w", vhost, err)
	}
	var docs []aiUsageDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("listing the AI usage of vhost %q: %w", vhost, err)
	}
	out := make([]storage.AIUsage, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.usage())
	}
	return out, nil
}

// MarkAIUsageWarned sets the alert's field only where it is still missing, so
// of any number of callers exactly one modifies the document.
func (s *mongoStorage) MarkAIUsageWarned(ctx context.Context, vhost, period, workflowID, kind string) (bool, error) {
	var field string
	switch kind {
	case storage.AIWarnTokens:
		field = "tokens_warned_at"
	case storage.AIWarnCost:
		field = "cost_warned_at"
	default:
		return false, fmt.Errorf("unknown AI budget alert %q", kind)
	}
	res, err := s.db.Collection(aiUsageCollection).UpdateOne(ctx,
		bson.M{"_id": storage.AIUsageID(vhost, period, workflowID), field: bson.M{"$exists": false}},
		bson.M{"$set": bson.M{field: time.Now().UTC()}})
	if err != nil {
		return false, fmt.Errorf("recording the AI budget alert of vhost %q: %w", vhost, err)
	}
	return res.ModifiedCount > 0, nil
}

func (s *mongoStorage) DeleteAIBudgets(ctx context.Context, vhost string) error {
	if _, err := s.db.Collection(aiBudgetsCollection).DeleteOne(ctx, bson.M{"_id": vhost}); err != nil {
		return fmt.Errorf("deleting the AI budget of vhost %q: %w", vhost, err)
	}
	if _, err := s.db.Collection(aiUsageCollection).DeleteMany(ctx, bson.M{"vhost": vhost}); err != nil {
		return fmt.Errorf("deleting the AI usage of vhost %q: %w", vhost, err)
	}
	return nil
}
