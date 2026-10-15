package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// ModelSource is the part of the model registry the model tools use. Predict
// is the registry's one inference path, so a model called over MCP is held to
// the same quotas and metrics as every other caller's.
type ModelSource interface {
	ListModels(ctx context.Context, vhost string) ([]storage.MLModel, error)
	Predict(ctx context.Context, vhost, name string, rows []map[string]any) ([]map[string]any, error)
	// Recommend calls a recommendation model, on the same path as REST, and
	// returns the answer's recommendation_id with it.
	Recommend(ctx context.Context, vhost, name string, req RecommendArgs) ([]recommend.Item, string, error)
	// RecommendScenario is a scenario call ("for", "period", "rules"), on
	// the path of POST .../recommend.
	RecommendScenario(ctx context.Context, vhost, name string, req RecommendArgs) (ScenarioAnswer, error)
}

// RecommendArgs are a recommend tool's arguments: "user" for a person's
// items or "item" for items like it, "k" and, for a user, "exclude_seen"; or
// a scenario call's "for", "territory", "period", "within_days", "k",
// "rules", "exclude_seen" and "stock", as POST .../recommend takes them.
type RecommendArgs struct {
	User        any                `json:"user,omitempty"`
	Item        any                `json:"item,omitempty"`
	For         *ScenarioFor       `json:"for,omitempty"`
	Territory   any                `json:"territory,omitempty"`
	Period      *ScenarioPeriod    `json:"period,omitempty"`
	WithinDays  int                `json:"within_days,omitempty"`
	K           int                `json:"k,omitempty"`
	Rules       []recommend.Rule   `json:"rules,omitempty"`
	ExcludeSeen *bool              `json:"exclude_seen,omitempty"`
	Stock       map[string]float64 `json:"stock,omitempty"`
}

// ScenarioFor is who or what a scenario call is for: one of customer,
// customers, product, salesman or all_customers, the territory a rule binds
// to, the customer category (segment), size, price tier, channel and region
// a list of customers keeps, and the brand and subcategory a list of
// products keeps.
type ScenarioFor struct {
	Customer     any   `json:"customer,omitempty"`
	Customers    []any `json:"customers,omitempty"`
	Product      any   `json:"product,omitempty"`
	Salesman     any   `json:"salesman,omitempty"`
	Territory    any   `json:"territory,omitempty"`
	AllCustomers bool  `json:"all_customers,omitempty"`
	Segment      any   `json:"segment,omitempty"`
	Size         any   `json:"size,omitempty"`
	PriceTier    any   `json:"price_tier,omitempty"`
	Channel      any   `json:"channel,omitempty"`
	Region       any   `json:"region,omitempty"`
	Brand        any   `json:"brand,omitempty"`
	Subcategory  any   `json:"subcategory,omitempty"`
}

// ScenarioPeriod is a visit plan's days, YYYY-MM-DD, both included.
type ScenarioPeriod struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// ScenarioAnswer is a scenario call's answer.
type ScenarioAnswer struct {
	RecommendationID string
	Results          []recommend.Result
}

// ErrModelNotFound is returned for a model tool the caller may not use.
var ErrModelNotFound = errors.New("no MCP-exposed model by that name")

// modelToolPrefix starts every model tool's name; recommendToolPrefix a
// recommendation model's.
const (
	modelToolPrefix     = "predict_"
	recommendToolPrefix = "recommend_"
)

// maxToolName is the longest tool name MCP allows.
const maxToolName = 128

// ModelTool is one model a vhost exposed, as an MCP tool. It is built per
// request for the caller, like the server it is added to.
type ModelTool struct {
	// Name is predict_<model>, or predict_<model>__<vhost> when the caller
	// can reach two vhosts exposing a model of that name.
	Name        string
	VHost       string
	Model       string
	Description string
	// InputSchema has one property per feature, typed where the type is
	// known, all required; a model that declares no features takes any
	// object as its one row.
	InputSchema map[string]any
	// Recommend marks a recommendation model's tool, recommend_<model>,
	// called with RecommendTool rather than PredictTool.
	Recommend bool

	features []string
	types    map[string]string
}

// PredictResult is what a model tool answers.
type PredictResult struct {
	Model      string         `json:"model"`
	VHost      string         `json:"vhost"`
	Prediction map[string]any `json:"prediction"`
}

// RecommendResult is what a recommend tool answers: items best first for a
// user or an item, or a scenario call's results; and the recommendation_id
// feedback on it is sent with.
type RecommendResult struct {
	Model            string             `json:"model"`
	VHost            string             `json:"vhost"`
	RecommendationID string             `json:"recommendation_id"`
	Items            []recommend.Item   `json:"items,omitempty"`
	Results          []recommend.Result `json:"results,omitempty"`
}

// ModelTools returns a tool for every model exposed to MCP in the vhosts
// given that the caller may use. A vhost the caller may not use is skipped
// whatever the list says.
func (s *Service) ModelTools(ctx context.Context, c Caller, vhosts []string) ([]ModelTool, error) {
	if s.Models == nil {
		return nil, nil
	}
	vhosts = slices.Clone(vhosts)
	slices.Sort(vhosts)
	vhosts = slices.Compact(vhosts)

	var tools []ModelTool
	count := map[string]int{}
	for _, vhost := range vhosts {
		if vhost == "" || c.MayAccess == nil || !c.MayAccess(vhost) {
			continue
		}
		models, err := s.Models.ListModels(ctx, vhost)
		if err != nil {
			return nil, fmt.Errorf("listing the models of vhost %q: %w", vhost, err)
		}
		for _, m := range models {
			if !m.MCPExposed {
				continue
			}
			tools = append(tools, modelTool(m))
			count[m.Name]++
		}
	}

	// A name two vhosts share is told apart by the vhost; anything that
	// still collides is left out rather than shadowing another tool.
	seen := map[string]bool{}
	out := tools[:0]
	for _, t := range tools {
		if count[t.Model] > 1 {
			t.Name = clipName(t.Name + "__" + toolSafe(t.VHost))
		}
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, t)
	}
	return out, nil
}

func modelTool(m storage.MLModel) ModelTool {
	if m.IsRecommender() {
		return recommendTool(m)
	}
	desc := fmt.Sprintf("Runs Hermod model %q of vhost %q on one record and returns its prediction.", m.Name, m.VHost)
	if d := strings.TrimSpace(m.Description); d != "" {
		desc = d + " " + desc
	}
	schema := map[string]any{"type": "object"}
	if len(m.Features) > 0 {
		props := make(map[string]any, len(m.Features))
		required := make([]any, 0, len(m.Features))
		for _, f := range m.Features {
			prop := map[string]any{}
			if t := jsonType(m.FeatureTypes[f]); t != "" {
				prop["type"] = t
			}
			props[f] = prop
			required = append(required, f)
		}
		schema["properties"] = props
		schema["required"] = required
		schema["additionalProperties"] = false
	}
	return ModelTool{
		Name: clipName(modelToolPrefix + m.Name), VHost: m.VHost, Model: m.Name, Description: desc,
		InputSchema: schema, features: m.Features, types: m.FeatureTypes,
	}
}

// recommendTool is a recommendation model's tool: a user or an item in, the
// recommended items out.
func recommendTool(m storage.MLModel) ModelTool {
	desc := fmt.Sprintf("Asks Hermod recommendation model %q of vhost %q. A scenario model takes \"for\": "+
		"{\"salesman\"} for a visit plan (with \"period\"), {\"customer\"} or {\"customers\"} for products to offer "+
		"or reorders due, {\"product\"} for the customers to offer it to or similar products; each result item "+
		"carries its reasons. A reorder model (when will they buy again) also takes {\"product\"} or "+
		"{\"all_customers\": true}, and \"within_days\" (14 for two weeks, 30 for a month) to list only the customers "+
		"due to buy within that window, overdue ones marked \"overdue\", each with its due_date and the products it is "+
		"due for. Any list of customers (a visit plan, reorders, a product's audience) takes \"segment\" and \"size\" in "+
		"\"for\" to keep only one customer category and size, such as {\"segment\": \"Retail\", \"size\": \"large\"}; "+
		"each customer listed carries its segment and size in its fields; \"price_tier\", \"channel\" and \"region\" "+
		"keep them likewise, and are carried likewise. A list of products (a customer's products "+
		"or reorders, similar products) takes \"brand\" and \"subcategory\" in \"for\" to keep only those products, "+
		"and \"stock\" (product id to items on hand) for stock newer than training: one out of stock is not offered, "+
		"its substitute is. Products carry their brand, subcategory, unit and pack_size, and a suggested_qty and "+
		"suggested_text in whole packs when there is one. A customer's products may come with a "+
		"\"summary\" saying what nearby customers of its category buy. A model of a person's items takes \"user\" for their top items or \"item\" for the "+
		"items most like one item; an id it never saw gets the most popular items, marked reason \"popular\".",
		m.Name, m.VHost)
	if d := strings.TrimSpace(m.Description); d != "" {
		desc = d + " " + desc
	}
	id := map[string]any{"type": []any{"string", "number"}}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user": mergeDesc(id, "The person's id, as in the training data."),
			"item": mergeDesc(id, "An item's id: answers the items most like it."),
			"for": map[string]any{"type": "object", "additionalProperties": false,
				"description": "A scenario model's subject: give one of customer, customers, product, salesman or all_customers.",
				"properties": map[string]any{
					"customer":  mergeDesc(id, "A customer's id."),
					"customers": map[string]any{"type": "array", "items": id, "maxItems": recommend.MaxBatch, "description": "Several customers: one result each."},
					"product":   mergeDesc(id, "A product's id."),
					"salesman":  mergeDesc(id, "A salesman's id: their visit plan, or their customers' reorders."),
					"territory": mergeDesc(id, "The territory a rule's from_request \"territory\" binds to."),
					"all_customers": map[string]any{"type": "boolean",
						"description": "A reorder model: every customer, by when they will buy again."},
					"segment": mergeDesc(id, "A list of customers: only this customer category (the model's customers.segment), in any case."),
					"size": mergeDesc(id, "A list of customers: only this size, small, medium or large (or the model's own size labels), "+
						"or a number such as employees, bucketed as training cut the sizes."),
					"price_tier":  mergeDesc(id, "A list of customers: only this price tier (the model's customers.price_tier), in any case."),
					"channel":     mergeDesc(id, "A list of customers: only this channel (the model's customers.channel), in any case."),
					"region":      mergeDesc(id, "A list of customers: only this region (the model's customers.region), in any case."),
					"brand":       mergeDesc(id, "A list of products: only this brand (the model's products.brand), in any case."),
					"subcategory": mergeDesc(id, "A list of products: only this subcategory (the model's products.subcategory), in any case."),
				}},
			"territory": mergeDesc(id, "The territory a rule's from_request \"territory\" binds to."),
			"period": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"from"},
				"description": "A visit plan's days, both included; today and the model's period by default.",
				"properties": map[string]any{
					"from": map[string]any{"type": "string", "format": "date"},
					"to":   map[string]any{"type": "string", "format": "date"},
				}},
			"within_days": map[string]any{"type": "integer", "minimum": 1, "maximum": recommend.MaxWithinDays,
				"description": "A reorder model: only the customers due to buy within this many days from today (14 for two weeks, 30 for a month), overdue ones included and marked."},
			"rules": map[string]any{"type": "array", "items": map[string]any{"type": "object"},
				"description": "Rules for this call (filter, exclude, boost, diversity, pin, block, capacity); they may only tighten the model's own."},
			"k": map[string]any{"type": "integer", "minimum": 1, "maximum": recommend.MaxPlan,
				"description": "How many results per subject; the model's default when left out."},
			"exclude_seen": map[string]any{"type": "boolean",
				"description": "Leave out items the person or customer already had. True by default."},
			"stock": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "number"},
				"maxProperties": recommend.MaxStock,
				"description":   "Items on hand now, by product id, replacing the stock the model was trained with: one at 0 or less is not offered."},
		},
		"additionalProperties": false,
	}
	return ModelTool{Name: clipName(recommendToolPrefix + m.Name), VHost: m.VHost, Model: m.Name, Description: desc,
		InputSchema: schema, Recommend: true}
}

func mergeDesc(prop map[string]any, desc string) map[string]any {
	out := make(map[string]any, len(prop)+1)
	for k, v := range prop {
		out[k] = v
	}
	out["description"] = desc
	return out
}

// RecommendTool runs one call of a recommend tool.
func (s *Service) RecommendTool(ctx context.Context, c Caller, t ModelTool, args json.RawMessage) (RecommendResult, error) {
	if s.Models == nil || c.MayAccess == nil || !c.MayAccess(t.VHost) {
		return RecommendResult{}, ErrModelNotFound
	}
	if !t.Recommend {
		return RecommendResult{}, fmt.Errorf("model %q predicts from rows: call %s", t.Model, t.Name)
	}
	var req RecommendArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return RecommendResult{}, fmt.Errorf(`the arguments must be a JSON object of "for", "user" or "item", "k" and "exclude_seen": %w`, err)
	}
	given := 0
	for _, set := range []bool{req.User != nil, req.Item != nil, req.For != nil} {
		if set {
			given++
		}
	}
	if given != 1 {
		return RecommendResult{}, errors.New(`give "for" for a scenario model, or "user" for a person's items, or "item" for items like it`)
	}
	if req.For == nil {
		if req.Territory != nil || req.Period != nil || req.WithinDays != 0 || len(req.Rules) > 0 || req.Stock != nil {
			return RecommendResult{}, errors.New(`"territory", "period", "within_days", "rules" and "stock" go with "for", a scenario call`)
		}
		items, id, err := s.Models.Recommend(ctx, t.VHost, t.Model, req)
		if err != nil {
			return RecommendResult{}, err
		}
		return RecommendResult{Model: t.Model, VHost: t.VHost, RecommendationID: id, Items: items}, nil
	}
	ans, err := s.Models.RecommendScenario(ctx, t.VHost, t.Model, req)
	if err != nil {
		return RecommendResult{}, err
	}
	return RecommendResult{Model: t.Model, VHost: t.VHost, RecommendationID: ans.RecommendationID, Results: ans.Results}, nil
}

// jsonType is the JSON Schema type of a dataset column type.
func jsonType(t string) string {
	switch t {
	case "number":
		return "number"
	case "string":
		return "string"
	case "bool":
		return "boolean"
	}
	return ""
}

// toolSafe replaces what a tool name may not hold.
func toolSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

func clipName(n string) string {
	if len(n) > maxToolName {
		return n[:maxToolName]
	}
	return n
}

// PredictTool runs one call of a model tool: the arguments, bound to the
// model's features, are its one row.
func (s *Service) PredictTool(ctx context.Context, c Caller, t ModelTool, args json.RawMessage) (PredictResult, error) {
	if s.Models == nil || c.MayAccess == nil || !c.MayAccess(t.VHost) {
		return PredictResult{}, ErrModelNotFound
	}
	if t.Recommend {
		return PredictResult{}, fmt.Errorf("model %q is a recommendation model: call %s with a user or an item", t.Model, t.Name)
	}
	row, err := t.bind(args)
	if err != nil {
		return PredictResult{}, err
	}
	preds, err := s.Models.Predict(ctx, t.VHost, t.Model, []map[string]any{row})
	if err != nil {
		return PredictResult{}, err
	}
	if len(preds) != 1 {
		return PredictResult{}, fmt.Errorf("model %q returned %d predictions for one record", t.Model, len(preds))
	}
	return PredictResult{Model: t.Model, VHost: t.VHost, Prediction: preds[0]}, nil
}

// bind checks the arguments against the model's features: every feature
// present, of its type where that is known, and nothing else.
func (t ModelTool) bind(args json.RawMessage) (map[string]any, error) {
	in := map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, errors.New("the arguments must be a JSON object of the model's features")
		}
	}
	if len(t.features) == 0 {
		return in, nil
	}
	for k := range in {
		if !slices.Contains(t.features, k) {
			return nil, fmt.Errorf("%q is not a feature of this model; its features are %s", k, strings.Join(t.features, ", "))
		}
	}
	for _, f := range t.features {
		v, ok := in[f]
		if !ok || v == nil {
			return nil, fmt.Errorf("feature %q is required", f)
		}
		if !ofType(v, t.types[f]) {
			return nil, fmt.Errorf("feature %q must be a %s", f, jsonType(t.types[f]))
		}
	}
	return in, nil
}

func ofType(v any, typ string) bool {
	switch typ {
	case "number":
		_, ok := v.(float64)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	}
	return true
}
