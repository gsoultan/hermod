// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package builtin

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// maxLoaded bounds the decoded models kept in memory, the least recently
// used going first, as the worker's serving cache does.
const maxLoaded = 32

// loaded is a decoded model version, ready to score.
type loaded struct {
	meta  worker.Version
	model *learn.Model
}

func cacheKey(vhost, name, version string) string { return vhost + "\x00" + name + "\x00" + version }

// load returns a version's decoded model, from the cache when it is there.
func (e *Engine) load(ctx context.Context, vhost, name, version string) (*loaded, error) {
	key := cacheKey(vhost, name, version)
	e.cacheMu.Lock()
	if l, ok := e.cache[key]; ok {
		e.touch(key)
		e.cacheMu.Unlock()
		return l, nil
	}
	e.cacheMu.Unlock()

	v, raw, err := e.models.file(ctx, vhost, name, version, modelFile)
	if err != nil {
		if v.Task == worker.TaskRecommendation {
			return nil, badRequest("Model %q is a recommendation model; Hermod serves it, not this endpoint.", name)
		}
		return nil, err
	}
	m, err := learn.Decode(raw)
	if err != nil {
		return nil, err
	}
	if m.Task == learn.TaskClassification && len(v.Labels) != m.Classes {
		return nil, learn.ErrInvalidModel
	}
	l := &loaded{meta: v, model: m}

	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if _, ok := e.cache[key]; !ok {
		e.cache[key] = l
		e.order = append(e.order, key)
		for len(e.order) > maxLoaded {
			delete(e.cache, e.order[0])
			e.order = e.order[1:]
		}
	}
	e.touch(key)
	return e.cache[key], nil
}

// touch marks key the most recently used; the caller holds cacheMu.
func (e *Engine) touch(key string) {
	if i := slices.Index(e.order, key); i >= 0 {
		e.order = append(slices.Delete(e.order, i, i+1), key)
	}
}

// evict drops a model's versions from the cache.
func (e *Engine) evict(vhost, name string) {
	prefix := vhost + "\x00" + name + "\x00"
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	e.order = slices.DeleteFunc(e.order, func(k string) bool {
		if strings.HasPrefix(k, prefix) {
			delete(e.cache, k)
			return true
		}
		return false
	})
}

// Predict scores rows with a version of a model, one prediction per row:
// {"label", "probability"} for a classifier (a whole-number label as a
// number), {"value"} for a regression, as the worker's serving answers. A
// missing or null number takes its training median; a value that is not a
// number for a number feature is refused, as the worker refuses it.
func (e *Engine) Predict(ctx context.Context, vhost, name, version string, rows []map[string]any) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []map[string]any{}, nil
	}
	l, err := e.load(ctx, vhost, name, version)
	if err != nil {
		return nil, err
	}
	x, err := frameOf(l.model.Prep, rows)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(rows))
	proba := l.model.Proba(x)
	for i, pr := range proba {
		if l.model.Task == learn.TaskRegression {
			out[i] = map[string]any{"value": pr[0]}
			continue
		}
		best := 0
		for c := range pr {
			if pr[c] > pr[best] {
				best = c
			}
		}
		out[i] = map[string]any{"label": labelValue(l.meta.Labels[best]), "probability": pr[best]}
	}
	return out, nil
}

// frameOf reads rows into the features the model was trained on. A feature
// no row names is refused, as the worker refuses an input left out.
func frameOf(p *learn.Prep, rows []map[string]any) (*learn.Frame, error) {
	x := learn.NewFrame(p.Features, p.Kinds, len(rows))
	for j, f := range p.Features {
		present := false
		for i, r := range rows {
			v, has := r[f]
			present = present || has
			if p.Kinds[j] == learn.KindString {
				x.Str[j][i] = text(v)
				continue
			}
			n, ok := number(v)
			if !ok {
				return nil, badRequest("Input %q row %d: %s is not a valid number.", f, i, show(v))
			}
			x.Num[j][i] = n
		}
		if !present {
			return nil, badRequest("Input %q is missing; the model needs it as a feature.", f)
		}
	}
	return x, nil
}

// number reads a value for a number feature, as the worker's serving does:
// nothing is missing (NaN, which the model fills), a boolean is 1 or 0, a
// number is itself, and text is the number it spells. A number is rounded
// to float32, as the worker feeds its graphs.
func number(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case nil:
		return math.NaN(), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		f = x
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case json.Number:
		var err error
		if f, err = x.Float64(); err != nil {
			return 0, false
		}
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(x), 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return float64(float32(f)), true
}

// text reads a value for a text feature: nothing is "", text is itself, and
// anything else is rendered as the worker renders it.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return table.ToText(v)
}

func show(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "the value"
	}
	return string(raw)
}

// labelValue is a label as the worker's serving answers it: a whole-number
// label as a number, any other as text.
func labelValue(l any) any {
	switch x := l.(type) {
	case string:
		return x
	case float64:
		return x
	}
	return table.ToText(l)
}
