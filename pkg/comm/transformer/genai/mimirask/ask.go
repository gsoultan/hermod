// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package mimirask is the mimir_ask transformer: it asks a mimir-server's
// document collection a question about the message and writes the answer,
// with the passages it cites, into the message. It sits beside ai_retrieve
// in the AI group: where ai_retrieve searches a vector store Hermod filled,
// mimir_ask uses the files a team already keeps on its mimir server.
package mimirask

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	mimirsdk "github.com/gsoultan/mimir-server-sdk"
)

func init() {
	transformer.Register("mimir_ask", &Transformer{})
}

const (
	// ModeAsk answers the question from the collection, citing passages;
	// ModeSearch only finds the passages, for a following AI Prompt to use.
	ModeAsk    = "ask"
	ModeSearch = "search"

	// DefaultTargetField is where the answer goes by default.
	DefaultTargetField = "answer"
	defaultLimit       = 5
	maxLimit           = 50
	defaultSources     = 3
	defaultTimeout     = 60 * time.Second
)

// Transformer is mimir_ask.
//
// Config:
//   - url: the mimir server's address; apiKey: its API key, as a vhost
//     secret reference ({{secret.NAME}}). Both resolve for the message's
//     vhost only, so row data cannot steer a credential.
//   - collection: the collection's id on the server. Required.
//   - mode: "ask" (default) for an answer with the passages it cites, or
//     "search" for the best passages alone.
//   - question: a template over the message, or questionField: a field read
//     verbatim. One is required.
//   - limit: passages a search returns (default 5, at most 50); sources:
//     passages an answer may cite (default 3); maxTokens: the longest answer.
//   - targetField: where the result goes, default "answer": {"text",
//     "finished", "sources": [{"document", "document_id", "passage_id",
//     "number", "score", "text"}]} for an answer, {"sources": [...]} for a
//     search.
//   - timeout: how long one call may take, default 60s.
type Transformer struct{}

// settings is one call's configuration, read and checked.
type settings struct {
	url, key, collection, question, mode, target string
}

// read checks the config against the message and says what is missing.
func read(config map[string]any, msg hermod.Message) (settings, error) {
	var st settings
	st.url = strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, "url"), msg))
	if st.url == "" {
		return st, errors.New("mimir_ask: enter the mimir server's address")
	}
	st.key = strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, "apiKey"), msg))
	if st.key == "" {
		return st, errors.New("mimir_ask: the mimir server needs an API key: name the secret that holds it")
	}
	st.collection = strings.TrimSpace(core.GetConfigString(config, "collection"))
	if st.collection == "" {
		return st, errors.New("mimir_ask: choose a collection")
	}
	st.question = questionText(config, msg)
	if st.question == "" {
		return st, errors.New("mimir_ask: nothing to ask (set question or questionField)")
	}
	st.mode = strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "mode")))
	if st.mode == "" {
		st.mode = ModeAsk
	}
	if st.mode != ModeAsk && st.mode != ModeSearch {
		return st, fmt.Errorf("mimir_ask: mode is %q or %q, not %q", ModeAsk, ModeSearch, st.mode)
	}
	st.target = core.GetConfigString(config, "targetField")
	if st.target == "" {
		st.target = DefaultTargetField
	}
	return st, nil
}

// Transform implements transformer.Transformer.
func (t *Transformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	st, err := read(config, msg)
	if err != nil {
		return nil, err
	}
	client, err := mimirsdk.New(st.url,
		mimirsdk.WithAPIKey(st.key),
		mimirsdk.WithHTTPClient(httpclient.DataClient),
		mimirsdk.WithTimeout(0),
		mimirsdk.WithUserAgent("hermod"),
	)
	if err != nil {
		return nil, fmt.Errorf("mimir_ask: the mimir server address: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(config))
	defer cancel()

	var result map[string]any
	if st.mode == ModeSearch {
		result, err = search(ctx, client, st, config)
	} else {
		result, err = ask(ctx, client, st, config)
	}
	if err != nil {
		return nil, err
	}
	msg.SetData(st.target, result)
	return msg, nil
}

// search is the best passages alone.
func search(ctx context.Context, client *mimirsdk.Client, st settings, config map[string]any) (map[string]any, error) {
	passages, err := client.Documents.Search(ctx, st.collection, st.question, intOption(config, "limit", defaultLimit, maxLimit))
	if err != nil {
		return nil, fmt.Errorf("mimir_ask: the mimir server did not search: %w", err)
	}
	return map[string]any{"sources": sources(passages)}, nil
}

// ask is the answer with the passages it cites.
func ask(ctx context.Context, client *mimirsdk.Client, st settings, config map[string]any) (map[string]any, error) {
	req := &mimirsdk.AskRequest{
		CollectionId: st.collection,
		Messages:     []*mimirsdk.ChatMessage{mimirsdk.UserMessage(st.question)},
		Sources:      int32(intOption(config, "sources", defaultSources, maxLimit)),
		MaxTokens:    int32(intOption(config, "maxTokens", 0, 1<<20)),
	}
	answer, err := client.Documents.AskText(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mimir_ask: the mimir server did not answer: %w", err)
	}
	return map[string]any{"text": answer.Text, "finished": answer.Finished, "sources": sources(answer.Sources)}, nil
}

// questionText is the question: the field named by questionField, else the
// question template rendered over the message.
func questionText(config map[string]any, msg hermod.Message) string {
	if field := strings.TrimSpace(core.GetConfigString(config, "questionField")); field != "" {
		switch v := evaluator.GetMsgValByPath(msg, field).(type) {
		case nil:
			return ""
		case string:
			return strings.TrimSpace(v)
		default:
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return strings.TrimSpace(evaluator.ResolveTemplate(core.GetConfigString(config, "question"), msg.Data()))
}

// sources is the passages as message data, best first as the server lists them.
func sources(passages []*mimirsdk.Passage) []any {
	out := make([]any, 0, len(passages))
	for _, p := range passages {
		out = append(out, map[string]any{
			"document": p.GetDocumentName(), "document_id": p.GetDocumentId(), "passage_id": p.GetId(),
			"number": int(p.GetNumber()), "score": p.GetScore(), "text": p.GetText(),
		})
	}
	return out
}

// intOption reads a whole number from the config, def when absent or
// unreadable, never above limit.
func intOption(config map[string]any, key string, def, limit int) int {
	raw := strings.TrimSpace(core.GetConfigString(config, key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return min(n, limit)
}

func timeout(config map[string]any) time.Duration {
	if d, err := time.ParseDuration(core.GetConfigString(config, "timeout")); err == nil && d > 0 {
		return d
	}
	return defaultTimeout
}
