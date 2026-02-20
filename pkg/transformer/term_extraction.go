package transformer

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

func init() {
	Register("term_extraction", &TermExtractionTransformer{})
}

type TermExtractionTransformer struct{}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	"of": true, "by": true, "from": true, "as": true, "it": true, "this": true,
	"that": true, "which": true, "who": true, "whom": true,
}

func (t *TermExtractionTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	valRaw := evaluator.GetMsgValByPath(msg, field)
	if valRaw == nil {
		return msg, nil
	}
	text := fmt.Sprintf("%v", valRaw)

	// Simple extraction: split by non-alphanumeric, filter short words and stop words
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	words := re.Split(text, -1)

	terms := make([]string, 0)
	seen := make(map[string]bool)

	minLen, _ := evaluator.ToInt64(config["minLen"])
	if minLen == 0 {
		minLen = 3
	}

	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) < int(minLen) {
			continue
		}
		if stopWords[word] {
			continue
		}
		if !seen[word] {
			terms = append(terms, word)
			seen[word] = true
		}
	}

	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = field + "_terms"
	}

	msg.SetData(targetField, terms)
	return msg, nil
}
