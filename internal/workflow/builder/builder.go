package builder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/llm"
)

// Ref is a source or sink the draft may connect to.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Request is what to draft.
type Request struct {
	Description string
	VHost       string
	Model       string
	// Sources and Sinks are the ones in the caller's vhost the draft may use.
	Sources []Ref
	Sinks   []Ref
}

// Issue is a problem the builder found in the model's answer. It has the
// same shape as the workflow validator's issues so the two lists merge.
type Issue struct {
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Recommendation string `json:"recommendation"`
	NodeID         string `json:"node_id,omitempty"`
}

// Draft is the proposed workflow.
type Draft struct {
	Workflow storage.Workflow
	Issues   []Issue
	Provider string
	Model    string
	Usage    llm.Usage
}

// ErrEmptyDescription is returned when there is nothing to draft from.
var ErrEmptyDescription = errors.New("describe the automation to draft")

// MaxDescriptionLength bounds the description sent to the model.
const MaxDescriptionLength = 4000

// maxAnswerTokens bounds the model's answer: enough for a few dozen nodes.
const maxAnswerTokens = 8192

// ErrDescriptionTooLong is returned for a description over
// MaxDescriptionLength.
var ErrDescriptionTooLong = errors.New("the description is too long")

// Build asks the model for a workflow matching req.Description and returns it
// as a draft. It never saves or starts anything.
func Build(ctx context.Context, p llm.Provider, req Request) (Draft, error) {
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		return Draft{}, ErrEmptyDescription
	}
	if len(desc) > MaxDescriptionLength {
		return Draft{}, ErrDescriptionTooLong
	}
	kinds := Catalogue()
	resp, err := p.Chat(ctx, llm.ChatRequest{
		Model:          req.Model,
		System:         systemPrompt(kinds, req),
		Messages:       []llm.Message{{Role: llm.RoleUser, Text: desc}},
		ResponseSchema: responseSchema(nodeTypes(kinds)),
		MaxTokens:      maxAnswerTokens,
	})
	if err != nil {
		return Draft{}, err
	}
	if resp.StopReason == llm.StopRefusal {
		return Draft{}, fmt.Errorf("%s declined to draft this workflow", resp.Provider)
	}
	a, err := parseAnswer(resp.Text)
	if err != nil {
		return Draft{}, err
	}
	wf, issues := draftOf(a, kinds, req)
	return Draft{Workflow: wf, Issues: issues, Provider: resp.Provider, Model: resp.Model, Usage: resp.Usage}, nil
}
