// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package recommend

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// The kinds of Rule.
const (
	RuleFilter    = "filter"
	RuleExclude   = "exclude"
	RuleBoost     = "boost"
	RuleDiversity = "diversity"
	RuleCapacity  = "capacity"
	RulePin       = "pin"
	RuleBlock     = "block"
)

// The comparisons a filter or boost makes.
const (
	OpEq  = "eq"
	OpIn  = "in"
	OpNeq = "neq"
	OpNin = "nin"
	OpGt  = "gt"
	OpLt  = "lt"
)

// The request fields a rule's value may be bound to.
const (
	FromSalesman  = "salesman"
	FromTerritory = "territory"
)

// Bounds of a rule.
const (
	MaxRules       = 50
	MaxRuleIDs     = 1000
	MaxRuleValues  = 1000
	MaxExcludeDays = 3650
	MaxPerValue    = 100
)

var (
	ruleKinds = []string{RuleFilter, RuleExclude, RuleBoost, RuleDiversity, RuleCapacity, RulePin, RuleBlock}
	ruleOps   = []string{OpEq, OpIn, OpNeq, OpNin, OpGt, OpLt}
	weekdays  = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	// fieldName is an attribute or feature a rule names after its entity.
	fieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ErrBadRule marks a rule that is wrong in itself, or for its scenario.
var ErrBadRule = errors.New("invalid rule")

// ErrLoosens marks a request rule that would loosen a rule the model was
// trained with. A request may only tighten them.
var ErrLoosens = errors.New("a request rule may only tighten the model's rules")

// Rule is one business rule, applied when serving: on the model as a
// default for every call, or on one request, where it is added to the
// defaults and may only tighten them.
//
//   - filter keeps only what matches Field Op Value.
//   - exclude drops what was interacted with (bought; visited, for a visit
//     plan) in the last Days days, or ever when Days is 0.
//   - boost adds Weight x the normalised Field to the score, or a flat
//     Weight to what matches Field Op Value or is listed in IDs.
//   - diversity keeps at most MaxPer results per value of Field.
//   - capacity (visits only) plans at most PerDay visits a day, on the
//     weekdays Value lists ("mon", ...) when it lists any.
//   - pin always includes IDs; block never does.
//
// Field is "customers.<name>" or "products.<name>": an attribute the
// training mapped (salesman, territory, segment, blocked, name, category,
// active) or a feature (monetary, price, margin, ...), or id. FromRequest
// binds Value to the request's salesman or territory instead.
type Rule struct {
	Kind        string   `json:"kind"`
	Field       string   `json:"field,omitempty"`
	Op          string   `json:"op,omitempty"`
	Value       any      `json:"value,omitempty"`
	FromRequest string   `json:"from_request,omitempty"`
	Days        int      `json:"days,omitempty"`
	Weight      float64  `json:"weight,omitempty"`
	MaxPer      int      `json:"max_per,omitempty"`
	PerDay      int      `json:"per_day,omitempty"`
	IDs         []string `json:"ids,omitempty"`
}

// The entities a field names.
const (
	entityCustomers = "customers"
	entityProducts  = "products"
)

// itemEntity is what a scenario's results are: customers or products.
func itemEntity(scenario string) string {
	switch scenario {
	case ScenarioVisits, ScenarioReorder, ScenarioAudience:
		return entityCustomers
	}
	return entityProducts
}

// subjectEntity is what a scenario's request names, when that is a customer
// or a product: "" for a visit plan, which is for a salesman.
func subjectEntity(scenario string) string {
	switch scenario {
	case ScenarioProducts, ScenarioReorder:
		return entityCustomers
	case ScenarioAudience, ScenarioSimilar:
		return entityProducts
	}
	return ""
}

// splitField is a field's entity and name.
func splitField(field string) (entity, name string, err error) {
	entity, name, ok := strings.Cut(field, ".")
	if !ok || (entity != entityCustomers && entity != entityProducts) || !fieldName.MatchString(name) {
		return "", "", fmt.Errorf(`field %q is not "customers.<name>" or "products.<name>"`, field)
	}
	return entity, name, nil
}

// ValidateRules reports the first rule that is wrong in itself or for the
// scenario, and refuses more than MaxRules.
func ValidateRules(scenario string, rules []Rule) error {
	if len(rules) > MaxRules {
		return fmt.Errorf("%w: at most %d rules, not %d", ErrBadRule, MaxRules, len(rules))
	}
	for i, r := range rules {
		if err := r.validate(scenario); err != nil {
			return fmt.Errorf("%w: rule %d (%s): %w", ErrBadRule, i+1, r.Kind, err)
		}
	}
	return nil
}

func (r Rule) validate(scenario string) error {
	entity, err := r.validateFields()
	if err != nil {
		return err
	}
	switch r.Kind {
	case RuleFilter:
		return r.validateFilter(scenario, entity)
	case RuleExclude:
		if r.Field != "" || r.Op != "" || r.Value != nil || len(r.IDs) > 0 {
			return errors.New("an exclude takes only days")
		}
	case RuleBoost:
		return r.validateBoost(scenario, entity)
	case RuleDiversity:
		return r.validateDiversity(scenario, entity)
	case RuleCapacity:
		return r.validateCapacity(scenario)
	case RulePin, RuleBlock:
		if len(r.IDs) == 0 || r.Field != "" || r.Op != "" {
			return fmt.Errorf("a %s lists ids and nothing else", r.Kind)
		}
	}
	return nil
}

func (r Rule) validateDiversity(scenario, entity string) error {
	if items := itemEntity(scenario); r.Field == "" || entity != items {
		return fmt.Errorf("a diversity names a field of the %s scenario %q lists", items, scenario)
	}
	if r.MaxPer < 1 || r.MaxPer > MaxPerValue {
		return fmt.Errorf("max_per must be between 1 and %d", MaxPerValue)
	}
	return nil
}

// validateFields checks what every kind of rule shares, and returns the
// entity its field names ("" for none).
func (r Rule) validateFields() (string, error) {
	if !slices.Contains(ruleKinds, r.Kind) {
		return "", fmt.Errorf("kind %q is not one of %s", r.Kind, strings.Join(ruleKinds, ", "))
	}
	var entity string
	if r.Field != "" {
		var err error
		if entity, _, err = splitField(r.Field); err != nil {
			return "", err
		}
	}
	switch {
	case r.FromRequest != "" && r.FromRequest != FromSalesman && r.FromRequest != FromTerritory:
		return "", fmt.Errorf(`from_request is "salesman", "territory" or null, not %q`, r.FromRequest)
	case r.Days < 0 || r.Days > MaxExcludeDays:
		return "", fmt.Errorf("days must be between 0 and %d", MaxExcludeDays)
	case math.IsNaN(r.Weight) || r.Weight < -1 || r.Weight > 1:
		return "", errors.New("weight must be between -1 and 1")
	case len(r.IDs) > MaxRuleIDs || slices.Contains(r.IDs, ""):
		return "", fmt.Errorf("ids must be at most %d non-empty ids", MaxRuleIDs)
	}
	return entity, nil
}

func (r Rule) validateFilter(scenario, entity string) error {
	if r.Field == "" {
		return errors.New("a filter names a field")
	}
	if items := itemEntity(scenario); entity != items && entity != subjectEntity(scenario) {
		return fmt.Errorf("scenario %q lists %s; a filter on %s does not apply", scenario, items, r.Field)
	}
	return r.validatePredicate()
}

func (r Rule) validateBoost(scenario, entity string) error {
	if r.Weight == 0 {
		return errors.New("a boost needs a weight other than 0")
	}
	if (r.Field == "") == (len(r.IDs) == 0) {
		return errors.New("a boost names a field or lists ids, not both")
	}
	if items := itemEntity(scenario); r.Field != "" && entity != items {
		return fmt.Errorf("scenario %q lists %s; a boost on %s does not apply", scenario, items, r.Field)
	}
	if r.Op != "" || r.FromRequest != "" {
		return r.validatePredicate()
	}
	if r.Value != nil {
		return errors.New("a boost on a field's size takes no value; give an op to boost what matches it")
	}
	return nil
}

func (r Rule) validateCapacity(scenario string) error {
	if scenario != ScenarioVisits {
		return errors.New("capacity applies to a visit plan only")
	}
	if r.PerDay < 0 || r.PerDay > MaxPlan {
		return fmt.Errorf("per_day must be between 1 and %d", MaxPlan)
	}
	days, err := ruleWeekdays(r.Value)
	if err != nil {
		return err
	}
	if r.PerDay == 0 && len(days) == 0 {
		return errors.New("a capacity sets per_day, working days in value, or both")
	}
	return nil
}

// validatePredicate checks Op and Value (or FromRequest) of a filter or a
// boost on what matches.
func (r Rule) validatePredicate() error {
	if !slices.Contains(ruleOps, r.Op) {
		return fmt.Errorf("op %q is not one of %s", r.Op, strings.Join(ruleOps, ", "))
	}
	if r.FromRequest != "" {
		if r.Value != nil {
			return errors.New("give a value or from_request, not both")
		}
		if r.Op != OpEq && r.Op != OpNeq {
			return errors.New("from_request compares with eq or neq")
		}
		return nil
	}
	switch r.Op {
	case OpIn, OpNin:
		list, ok := r.Value.([]any)
		if !ok || len(list) == 0 || len(list) > MaxRuleValues {
			return fmt.Errorf("%s takes a list of 1 to %d values", r.Op, MaxRuleValues)
		}
		for _, v := range list {
			if _, ok := ID(v); !ok {
				return fmt.Errorf("%v is not a value to compare with", v)
			}
		}
	case OpGt, OpLt:
		if _, ok := r.Value.(float64); !ok {
			return fmt.Errorf("%s compares with a number", r.Op)
		}
	default:
		if _, ok := ID(r.Value); !ok {
			return fmt.Errorf("%s compares with a text, number or true/false value", r.Op)
		}
	}
	return nil
}

// ruleWeekdays reads a capacity's working days: nil, or a list of "mon" ...
// "sun".
func ruleWeekdays(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New(`a capacity's value is its working days, such as ["mon", "tue"]`)
	}
	out := make([]string, 0, len(list))
	for _, d := range list {
		s, _ := d.(string)
		s = strings.ToLower(s)
		if !slices.Contains(weekdays, s) {
			return nil, fmt.Errorf("%v is not a weekday (mon ... sun)", d)
		}
		out = append(out, s)
	}
	return out, nil
}

// checkWeekdays checks a salesman's working days.
func checkWeekdays(days []string) error {
	for _, d := range days {
		if !slices.Contains(weekdays, d) {
			return fmt.Errorf("%q is not a weekday", d)
		}
	}
	return nil
}

// CheckTightens refuses request rules that would loosen the defaults, as far
// as that can be told from the rules alone: a higher capacity or working days
// the defaults do not allow, a higher diversity cap on the same field, or a
// pin of what a default blocks. A pin of what a default filter or exclude
// drops is refused when the call is answered, where the items are known.
// Filters, excludes, blocks and boosts are added to the defaults, so they can
// only narrow or reorder what the defaults allow.
func CheckTightens(defaults, request []Rule) error {
	for i, r := range request {
		for _, d := range defaults {
			if err := loosens(d, r); err != nil {
				return fmt.Errorf("%w: rule %d (%s): %w", ErrLoosens, i+1, r.Kind, err)
			}
		}
	}
	return nil
}

func loosens(d, r Rule) error {
	switch {
	case r.Kind == RuleCapacity && d.Kind == RuleCapacity:
		return loosensCapacity(d, r)
	case r.Kind == RuleDiversity && d.Kind == RuleDiversity && r.Field == d.Field && r.MaxPer > d.MaxPer:
		return fmt.Errorf("max_per %d for %s is above the model's %d", r.MaxPer, r.Field, d.MaxPer)
	case r.Kind == RulePin && d.Kind == RuleBlock:
		for _, id := range r.IDs {
			if slices.Contains(d.IDs, id) {
				return fmt.Errorf("%q is blocked by the model's rules", id)
			}
		}
	}
	return nil
}

// loosensCapacity refuses a request capacity above the model's per_day or
// on a day the model's capacity does not work.
func loosensCapacity(d, r Rule) error {
	if d.PerDay > 0 && r.PerDay > d.PerDay {
		return fmt.Errorf("per_day %d is above the model's %d", r.PerDay, d.PerDay)
	}
	allowed, _ := ruleWeekdays(d.Value)
	if len(allowed) == 0 {
		return nil
	}
	asked, _ := ruleWeekdays(r.Value)
	for _, day := range asked {
		if !slices.Contains(allowed, day) {
			return fmt.Errorf("%s is not one of the model's working days", day)
		}
	}
	return nil
}
