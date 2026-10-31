// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mimirask

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	mimirv1 "github.com/gsoultan/mimir-server/gen/mimir/v1"
	"github.com/gsoultan/mimir-server/gen/mimir/v1/mimirv1connect"
)

// fakeDocuments is a mimir server with one collection that answers every
// question the same way and records what it was asked.
type fakeDocuments struct {
	mimirv1connect.UnimplementedDocumentServiceHandler
	mu         sync.Mutex
	key        string
	auth       []string
	searched   []*mimirv1.SearchRequest
	asked      []*mimirv1.AskRequest
	unfinished bool
}

func (f *fakeDocuments) refuse(h http.Header) error {
	if h.Get("Authorization") != "Bearer "+f.key {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("the API key is wrong or revoked"))
	}
	return nil
}

var passages = []*mimirv1.Passage{
	{Id: "p-1", DocumentId: "d-1", DocumentName: "returns.pdf", Number: 3, Score: 0.91, Text: "Used items may be returned within 14 days."},
	{Id: "p-2", DocumentId: "d-2", DocumentName: "faq.md", Number: 1, Score: 0.64, Text: "Refunds take five days."},
}

func (f *fakeDocuments) Search(_ context.Context, req *connect.Request[mimirv1.SearchRequest]) (*connect.Response[mimirv1.SearchResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, req.Header().Get("Authorization"))
	if err := f.refuse(req.Header()); err != nil {
		return nil, err
	}
	f.searched = append(f.searched, req.Msg)
	return connect.NewResponse(&mimirv1.SearchResponse{Passages: passages[:min(int(req.Msg.GetLimit()), len(passages))]}), nil
}

func (f *fakeDocuments) Ask(_ context.Context, req *connect.Request[mimirv1.AskRequest], stream *connect.ServerStream[mimirv1.AskResponse]) error {
	f.mu.Lock()
	f.auth = append(f.auth, req.Header().Get("Authorization"))
	if err := f.refuse(req.Header()); err != nil {
		f.mu.Unlock()
		return err
	}
	f.asked = append(f.asked, req.Msg)
	unfinished := f.unfinished
	f.mu.Unlock()
	for _, piece := range []*mimirv1.AskResponse{
		{Sources: passages[:1]},
		{Text: "Yes, within "},
		{Text: "14 days [1]."},
		{Done: true, Finished: !unfinished, PromptTokens: 120, GeneratedTokens: 9},
	} {
		if err := stream.Send(piece); err != nil {
			return err
		}
	}
	return nil
}

func serve(t *testing.T, f *fakeDocuments) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewDocumentServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func run(t *testing.T, cfg map[string]any, data map[string]any) (map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetVHost("tenant-a")
	for k, v := range data {
		msg.SetData(k, v)
	}
	out, err := (&Transformer{}).Transform(t.Context(), msg, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("mimir_ask"); !ok {
		t.Fatal("mimir_ask is not registered")
	}
}

func TestAskAnswersTheQuestionWithItsSources(t *testing.T) {
	f := &fakeDocuments{key: "mimir_k"}
	url := serve(t, f)
	data, err := run(t,
		map[string]any{"url": url, "apiKey": "mimir_k", "collection": "col-1", "question": "Can {{customer}} return a used {{item}}?", "sources": "2"},
		map[string]any{"customer": "Ada", "item": "kettle"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	answer, _ := data["answer"].(map[string]any)
	if answer["text"] != "Yes, within 14 days [1]." || answer["finished"] != true {
		t.Errorf("answer = %v", answer)
	}
	srcs, _ := answer["sources"].([]any)
	if len(srcs) != 1 {
		t.Fatalf("sources = %v", answer["sources"])
	}
	src, _ := srcs[0].(map[string]any)
	if src["document"] != "returns.pdf" || src["document_id"] != "d-1" || src["passage_id"] != "p-1" || src["number"] != 3 || src["score"] != 0.91 ||
		src["text"] != passages[0].Text {
		t.Errorf("source = %v", src)
	}
	if len(f.asked) != 1 || f.asked[0].GetCollectionId() != "col-1" || f.asked[0].GetSources() != 2 ||
		f.asked[0].GetMessages()[0].GetContent() != "Can Ada return a used kettle?" {
		t.Errorf("the server was asked %+v", f.asked)
	}
	if f.auth[0] != "Bearer mimir_k" {
		t.Errorf("auth = %q", f.auth[0])
	}
}

func TestSearchReturnsThePassagesAloneWhereTold(t *testing.T) {
	f := &fakeDocuments{key: "mimir_k"}
	url := serve(t, f)
	data, err := run(t,
		map[string]any{"url": url, "apiKey": "mimir_k", "collection": "col-1", "mode": "search", "questionField": "ticket.body", "limit": "1", "targetField": "context"},
		map[string]any{"ticket": map[string]any{"body": "refund timing"}})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	result, _ := data["context"].(map[string]any)
	if _, hasText := result["text"]; hasText {
		t.Errorf("a search wrote an answer text: %v", result)
	}
	srcs, _ := result["sources"].([]any)
	if len(srcs) != 1 {
		t.Fatalf("sources = %v", result["sources"])
	}
	if len(f.searched) != 1 || f.searched[0].GetQuery() != "refund timing" || f.searched[0].GetLimit() != 1 || len(f.asked) != 0 {
		t.Errorf("the server was searched %+v and asked %+v", f.searched, f.asked)
	}
}

func TestAnUnfinishedAnswerSaysSo(t *testing.T) {
	f := &fakeDocuments{key: "mimir_k", unfinished: true}
	url := serve(t, f)
	data, err := run(t, map[string]any{"url": url, "apiKey": "mimir_k", "collection": "c", "question": "why?", "maxTokens": "5"}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if answer, _ := data["answer"].(map[string]any); answer["finished"] != false {
		t.Errorf("finished = %v, want false", answer["finished"])
	}
	if f.asked[0].GetMaxTokens() != 5 {
		t.Errorf("max tokens = %d", f.asked[0].GetMaxTokens())
	}
}

func TestRefusesWhatItCannotDo(t *testing.T) {
	url := serve(t, &fakeDocuments{key: "mimir_k"})
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no url", map[string]any{"apiKey": "k", "collection": "c", "question": "q"}, "address"},
		{"no key", map[string]any{"url": url, "collection": "c", "question": "q"}, "API key"},
		{"no collection", map[string]any{"url": url, "apiKey": "k", "question": "q"}, "collection"},
		{"no question", map[string]any{"url": url, "apiKey": "k", "collection": "c"}, "nothing to ask"},
		{"empty question field", map[string]any{"url": url, "apiKey": "k", "collection": "c", "questionField": "missing"}, "nothing to ask"},
		{"bad mode", map[string]any{"url": url, "apiKey": "k", "collection": "c", "question": "q", "mode": "chat"}, "mode is"},
		{"wrong key", map[string]any{"url": url, "apiKey": "nope", "collection": "c", "question": "q"}, "wrong or revoked"},
		{"wrong key on search", map[string]any{"url": url, "apiKey": "nope", "collection": "c", "question": "q", "mode": "search"}, "wrong or revoked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, tc.cfg, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
