package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// The node's modes.
const (
	modePredict   = "predict"
	modeRecommend = "recommend"
)

// defaultRecommendField is where the items go when the node names nothing.
const defaultRecommendField = "recommendations"

// recommender is the registry's call to a recommendation model. req holds
// "user" or "item" (#268), or "for" with a scenario subject ({"salesman"},
// {"customer"} or {"product"}) and "period_days", "within_days" and "rules"
// when the node sets them; and "k" and "exclude_seen" when set. The answer
// is the items, best first: {"item", "score", "reason"?} for "user" or
// "item", and {"id", "score", "rank", "reasons", "fields", "due_date"?,
// "overdue"?, "products"?, "plan_date"?} for "for".
type recommender interface {
	MLRecommend(ctx context.Context, vhost, model string, req map[string]any) ([]map[string]any, error)
}

// recommendInto is the node's Recommend mode: it reads a person's id (or an
// item's) from the message and writes the model's items onto it.
//
// Config, besides model and mode:
//   - recommendFor: for a scenario model, "salesman" (a visit plan or that
//     salesman's reorders), "customer" (products to offer, or when they
//     reorder) or "product" (its audience, or products like it); for a #268
//     model, "user" (the default: that person's top items) or "item" (the
//     items most like it). Scenario models answer user and item too, as
//     products.
//   - idField: the field path holding the id. Required.
//   - k: how many items; the model's default (10, or a visit plan's
//     capacity) when empty.
//   - excludeSeen: for a user or customer, whether to leave out what they
//     already had; true when unset.
//   - periodDays: a visit plan's days from today; the model's period when
//     empty.
//   - withinDays: for a reorder model, only the customers due to order
//     within that many days from today, overdue ones included, most likely
//     first, each with the products they are due for (1 to 366).
//   - rules: request rules (docs/ml.md, "Rules"), a list or its JSON text.
//   - outputField: where the items go, default "recommendations".
func recommendInto(ctx context.Context, msg hermod.Message, model string, config map[string]any) (hermod.Message, error) {
	req, path, err := recommendRequest(config)
	if err != nil {
		return msg, err
	}
	caller, ok := ctx.Value(hermod.RegistryKey).(recommender)
	if !ok {
		return msg, errors.New("ml_predict: this registry cannot call a recommendation model (recommend mode)")
	}
	id := evaluator.GetMsgValByPath(msg, path)
	if id == nil {
		return msg, fmt.Errorf("ml_predict: the record has no field %q to recommend for", path)
	}
	subject, scenario := req["for"].(map[string]any)
	switch {
	case scenario:
		for k := range subject {
			subject[k] = id
		}
	case hasKey(req, "user"):
		req["user"] = id
	default:
		req["item"] = id
	}

	vhost := defaultVHost
	if scoped, ok := msg.(hermod.VHostScoped); ok && scoped.VHost() != "" {
		vhost = scoped.VHost()
	}
	items, err := caller.MLRecommend(ctx, vhost, model, req)
	if err != nil {
		return msg, fmt.Errorf("ml_predict: %w", err)
	}
	field := core.GetConfigString(config, "outputField")
	if field == "" {
		field = defaultRecommendField
	}
	msg.SetData(field, items)
	return msg, nil
}

// recommendRequest reads the node's config into the request, with a
// placeholder under "user" or "item" for the id, and the id's field path.
func recommendRequest(config map[string]any) (map[string]any, string, error) {
	path := core.GetConfigString(config, "idField")
	if path == "" {
		return nil, "", errors.New("ml_predict: recommend mode needs idField, the field holding the user or item id")
	}
	req := map[string]any{}
	forUser := true
	scenario := false
	switch to := core.GetConfigString(config, "recommendFor"); to {
	case "", "user":
		req["user"] = nil
	case "item":
		req["item"] = nil
		forUser = false
	case "salesman", "customer", "product":
		req["for"] = map[string]any{to: nil}
		forUser, scenario = to == "customer", true
	default:
		return nil, "", fmt.Errorf(`ml_predict: recommendFor %q is not "salesman", "customer", "product", "user" or "item"`, to)
	}
	days, err := configInt(config["periodDays"])
	if err != nil {
		return nil, "", fmt.Errorf("ml_predict: periodDays: %w", err)
	}
	if days < 0 || days > 366 {
		return nil, "", errors.New("ml_predict: periodDays is between 1 and 366")
	}
	if days > 0 {
		if !scenario {
			return nil, "", errors.New(`ml_predict: periodDays applies to recommendFor "salesman" (a visit plan)`)
		}
		req["period_days"] = days
	}
	within, err := configInt(config["withinDays"])
	if err != nil {
		return nil, "", fmt.Errorf("ml_predict: withinDays: %w", err)
	}
	if within < 0 || within > recommend.MaxWithinDays {
		return nil, "", fmt.Errorf("ml_predict: withinDays is between 1 and %d", recommend.MaxWithinDays)
	}
	if within > 0 {
		if !scenario {
			return nil, "", errors.New(`ml_predict: withinDays applies to a reorder model's recommendFor "salesman", "customer" or "product"`)
		}
		req["within_days"] = within
	}
	rules, err := configRules(config["rules"])
	if err != nil {
		return nil, "", fmt.Errorf("ml_predict: rules: %w", err)
	}
	if rules != nil {
		if !scenario {
			return nil, "", errors.New(`ml_predict: rules apply to recommendFor "salesman", "customer" or "product"`)
		}
		req["rules"] = rules
	}
	k, err := configInt(config["k"])
	if err != nil {
		return nil, "", fmt.Errorf("ml_predict: k: %w", err)
	}
	if k > 0 {
		req["k"] = k
	}
	exclude, set, err := configBool(config["excludeSeen"])
	if err != nil {
		return nil, "", fmt.Errorf("ml_predict: excludeSeen: %w", err)
	}
	if set {
		if !forUser && !scenario {
			return nil, "", errors.New("ml_predict: excludeSeen applies to a user's items, not to similar items")
		}
		req["exclude_seen"] = exclude
	}
	return req, path, nil
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// configRules reads request rules: a list, or the editor's JSON text of
// one; nil when unset. The model checks each rule.
func configRules(v any) ([]any, error) {
	switch r := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return r, nil
	case string:
		if strings.TrimSpace(r) == "" {
			return nil, nil
		}
		var list []any
		if err := json.Unmarshal([]byte(r), &list); err != nil {
			return nil, errors.New("not a JSON list of rules")
		}
		return list, nil
	}
	return nil, fmt.Errorf("%T is not a list of rules", v)
}

// configInt reads a whole number that the editor may save as text.
func configInt(v any) (int, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return n, nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%v is not a whole number", n)
		}
		return int(n), nil
	case string:
		if strings.TrimSpace(n) == "" {
			return 0, nil
		}
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", n)
		}
		return i, nil
	}
	return 0, fmt.Errorf("%T is not a whole number", v)
}

// configBool reads a flag that the editor may save as text, and whether it
// was set at all.
func configBool(v any) (value, set bool, err error) {
	switch b := v.(type) {
	case nil:
		return false, false, nil
	case bool:
		return b, true, nil
	case string:
		if strings.TrimSpace(b) == "" {
			return false, false, nil
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(b))
		if err != nil {
			return false, false, fmt.Errorf("%q is not true or false", b)
		}
		return parsed, true, nil
	}
	return false, false, fmt.Errorf("%T is not true or false", v)
}
