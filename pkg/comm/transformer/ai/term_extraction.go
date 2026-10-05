package ai

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("term_extraction", &TermExtractionTransformer{})
}

type TermExtractionTransformer struct{}

// wordBreak is everything that is not part of a word. Compiled once: it used to
// be compiled for every message.
var wordBreak = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// preparedStopWords is the set Prepare built, with the text it was built from.
// Transform uses the set only while the config still holds that text.
type preparedStopWords struct {
	text  string
	words map[string]bool
}

const ignoredWordsCacheKey = "_parsed_stop_words"

// Prepare builds the node's own stop words once, rather than on every message.
func (t *TermExtractionTransformer) Prepare(config map[string]any) (map[string]any, error) {
	if text, ok := config["stopWords"].(string); ok && text != "" {
		config[ignoredWordsCacheKey] = preparedStopWords{text: text, words: stopWordSet(strings.Split(text, ","))}
	}
	return config, nil
}

// extraStopWords are the words the node's own config adds to the built-in
// ones. The editor's "Stopwords" stores them as comma-separated text; a list
// is read too. The key was never read at all, so the control did nothing.
func extraStopWords(config map[string]any) map[string]bool {
	switch v := config["stopWords"].(type) {
	case string:
		if v == "" {
			return nil
		}
		if p, ok := config[ignoredWordsCacheKey].(preparedStopWords); ok && p.text == v {
			return p.words
		}
		return stopWordSet(strings.Split(v, ","))
	case []string:
		return stopWordSet(v)
	case []any:
		words := make([]string, 0, len(v))
		for _, w := range v {
			words = append(words, fmt.Sprintf("%v", w))
		}
		return stopWordSet(words)
	}
	return nil
}

// stopWordSet is words as the extraction compares them: trimmed, lowercased.
func stopWordSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			set[w] = true
		}
	}
	return set
}

// minWordLength is the shortest word kept: minLength, which the editor's "Min
// Word Length" writes, or minLen, the only key the node used to read -- so the
// editor's control did nothing. Unset, or not a positive number, is 3.
func minWordLength(config map[string]any) int {
	for _, key := range []string{"minLength", "minLen"} {
		if n, ok := evaluator.ToInt64(config[key]); ok && n > 0 {
			return int(n)
		}
	}
	return 3
}

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

	// Settled before anything is read: a call with no target field is a fault
	// in the node, whatever this message holds.
	configuredTarget, _ := config["targetField"].(string)
	targetField, err := evaluator.OutputField(field, configuredTarget, "_terms")
	if err != nil {
		return msg, err
	}

	valRaw := evaluator.EvaluateField(msg, field)
	if valRaw == nil {
		return msg, nil
	}
	text := fmt.Sprintf("%v", valRaw)

	// Simple extraction: split by non-alphanumeric, filter short words and stop words
	words := wordBreak.Split(text, -1)

	terms := make([]string, 0)
	seen := make(map[string]bool)

	minLen := minWordLength(config)
	ignored := extraStopWords(config)

	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) < minLen {
			continue
		}
		if stopWords[word] || ignored[word] {
			continue
		}
		if !seen[word] {
			terms = append(terms, word)
			seen[word] = true
		}
	}

	msg.SetData(targetField, terms)
	return msg, nil
}
