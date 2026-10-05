package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("fuzzy_lookup", &FuzzyLookupTransformer{})
}

type FuzzyLookupTransformer struct{}

// preparedOptions is the list Prepare parsed, with the text it was parsed
// from. Transform uses the list only while the config still holds that text,
// so options changed on a config that was not prepared again are read, not
// the ones cached beside them.
type preparedOptions struct {
	text    string
	options []any
}

const preparedOptionsKey = "_parsed_options"

// Prepare parses options held as JSON text once, rather than on every message.
// A fault in them is not reported here: the editor's Test button runs
// Transform on a config that was never prepared, so Transform has to report it
// anyway, and does.
func (t *FuzzyLookupTransformer) Prepare(config map[string]any) (map[string]any, error) {
	if text, ok := config["options"].(string); ok {
		if options, err := parseOptionsText(text); err == nil {
			config[preparedOptionsKey] = preparedOptions{text: text, options: options}
		}
	}
	return config, nil
}

// fuzzyOptions reads the node's options in every shape they are stored in.
//
// The editor's Options box is a JSON text field, so a node built there holds
// the text `["Jakarta", "Bandung"]`, where an API client sends a list. Only
// the list used to be read: for text the type assertion failed, the node had
// no options, and every record went through unchanged with nothing reported.
//
// No options at all is a node nobody has filled in yet and leaves the record
// alone. Options that are there but are not a list are a fault in the node,
// and say what they should look like.
func fuzzyOptions(config map[string]any) ([]any, error) {
	switch v := config["options"].(type) {
	case nil:
		return nil, nil
	case []any:
		return v, nil
	case []string:
		options := make([]any, len(v))
		for i, s := range v {
			options[i] = s
		}
		return options, nil
	case string:
		if p, ok := config[preparedOptionsKey].(preparedOptions); ok && p.text == v {
			return p.options, nil
		}
		return parseOptionsText(v)
	default:
		return nil, fmt.Errorf(`options must be a list such as ["Jakarta", "Bandung"], not %T`, v)
	}
}

func parseOptionsText(text string) ([]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var options []any
	if err := json.Unmarshal([]byte(text), &options); err != nil {
		return nil, fmt.Errorf(`options must be a JSON list such as ["Jakarta", "Bandung"]: %w`, err)
	}
	return options, nil
}

func (t *FuzzyLookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	// Where the match goes, settled before anything is read: a call with no
	// target field is a fault in the node, whatever this message holds.
	configuredTarget, _ := config["targetField"].(string)
	targetField, err := evaluator.OutputField(field, configuredTarget, "_fuzzy")
	if err != nil {
		return msg, err
	}
	// The score is named after the field too. A call has no name to give it,
	// so there it follows the match: city_match and city_match_score.
	configuredScore, _ := config["scoreField"].(string)
	scoreField, err := evaluator.OutputField(field, configuredScore, "_score")
	if err != nil {
		scoreField = targetField + "_score"
	}

	threshold, _ := evaluator.ToFloat64(config["threshold"]) // 0.0 to 1.0 (similarity)
	if threshold == 0 {
		threshold = 0.8
	}

	options, err := fuzzyOptions(config)
	if err != nil {
		return msg, err
	}
	if len(options) == 0 {
		return msg, nil
	}

	valRaw := evaluator.EvaluateField(msg, field)
	if valRaw == nil {
		return msg, nil
	}
	val := strings.ToLower(fmt.Sprintf("%v", valRaw))

	bestMatch := ""
	bestScore := 0.0

	for _, optRaw := range options {
		opt := strings.ToLower(fmt.Sprintf("%v", optRaw))
		score := t.similarity(val, opt)
		if score > bestScore {
			bestScore = score
			bestMatch = fmt.Sprintf("%v", optRaw) // Keep original casing
		}
	}

	if bestScore >= threshold {
		msg.SetData(targetField, bestMatch)
		msg.SetData(scoreField, bestScore)
	} else {
		msg.SetData(targetField, nil)
		msg.SetData(scoreField, bestScore)
	}

	return msg, nil
}

func (t *FuzzyLookupTransformer) similarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	if len(s1) == 0 || len(s2) == 0 {
		return 0.0
	}

	dist := t.levenshtein(s1, s2)
	maxLen := max(len(s2), len(s1))

	return 1.0 - float64(dist)/float64(maxLen)
}

func (t *FuzzyLookupTransformer) levenshtein(s1, s2 string) int {
	d := make([][]int, len(s1)+1)
	for i := range d {
		d[i] = make([]int, len(s2)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}

	for i := 1; i <= len(s1); i++ {
		for j := 1; j <= len(s2); j++ {
			cost := 1
			if s1[i-1] == s2[j-1] {
				cost = 0
			}
			d[i][j] = t.min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
		}
	}

	return d[len(s1)][len(s2)]
}

func (t *FuzzyLookupTransformer) min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
