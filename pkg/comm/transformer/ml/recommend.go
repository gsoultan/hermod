package ml

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// The node's modes.
const (
	modePredict   = "predict"
	modeRecommend = "recommend"
)

// defaultRecommendField is where the items go when the node names nothing.
const defaultRecommendField = "recommendations"

// recommender is the registry's call to a recommendation model. req holds
// "user" or "item", and "k" and "exclude_seen" when the node sets them; the
// answer is the items, best first: {"item", "score", "reason"?}.
type recommender interface {
	MLRecommend(ctx context.Context, vhost, model string, req map[string]any) ([]map[string]any, error)
}

// recommendInto is the node's Recommend mode: it reads a person's id (or an
// item's) from the message and writes the model's items onto it.
//
// Config, besides model and mode:
//   - recommendFor: "user" (the default: that person's top items) or "item"
//     (the items most like it).
//   - idField: the field path holding the id. Required.
//   - k: how many items; the model's default (10) when empty, at most 100.
//   - excludeSeen: for a user, whether to leave out items they already had;
//     true when unset.
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
	if _, ok := req["user"]; ok {
		req["user"] = id
	} else {
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
	switch to := core.GetConfigString(config, "recommendFor"); to {
	case "", "user":
		req["user"] = nil
	case "item":
		req["item"] = nil
		forUser = false
	default:
		return nil, "", fmt.Errorf(`ml_predict: recommendFor %q is not "user" or "item"`, to)
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
		if !forUser {
			return nil, "", errors.New("ml_predict: excludeSeen applies to a user's items, not to similar items")
		}
		req["exclude_seen"] = exclude
	}
	return req, path, nil
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
