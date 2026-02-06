package transformer

import (
	"context"
	"fmt"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("fuzzy_lookup", &FuzzyLookupTransformer{})
}

type FuzzyLookupTransformer struct{}

func (t *FuzzyLookupTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	threshold, _ := evaluator.ToFloat64(config["threshold"]) // 0.0 to 1.0 (similarity)
	if threshold == 0 {
		threshold = 0.8
	}

	options, _ := config["options"].([]interface{})
	if len(options) == 0 {
		return msg, nil
	}

	valRaw := evaluator.GetMsgValByPath(msg, field)
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

	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = field + "_fuzzy"
	}

	scoreField, _ := config["scoreField"].(string)
	if scoreField == "" {
		scoreField = field + "_score"
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
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}

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
