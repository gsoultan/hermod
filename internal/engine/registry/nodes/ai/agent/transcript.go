package agent

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/llm"
)

// The transcript is written onto the message so the trace and the debugger
// show every step. It is bounded: each text is cut at maxEntryText bytes and
// only the newest maxEntries entries are kept.
const (
	maxEntries   = 64
	maxEntryText = 1024
)

// entry is one step of the transcript.
type entry struct {
	Step         int    `json:"step"`
	Kind         string `json:"kind"` // model, tool_call, tool_result, approval_requested, approval_decision
	Text         string `json:"text,omitempty"`
	Tool         string `json:"tool,omitempty"`
	CallID       string `json:"call_id,omitempty"`
	Input        string `json:"input,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	InputTokens  int64  `json:"input_tokens,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
}

type transcript struct {
	Entries   []entry `json:"entries"`
	Truncated bool    `json:"truncated,omitempty"`
}

func (t *transcript) add(e entry) {
	e.Text = clip(e.Text)
	e.Input = clip(e.Input)
	t.Entries = append(t.Entries, e)
	if len(t.Entries) > maxEntries {
		t.Entries = append(t.Entries[:0:0], t.Entries[len(t.Entries)-maxEntries:]...)
		t.Truncated = true
	}
}

func clip(s string) string { return clipTo(s, maxEntryText) }

// clipTo cuts s to at most n bytes on a rune boundary, marking the cut.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
}

// write puts the transcript on the message under field, with the run's
// status, step count, usage and error, as plain JSON values.
func (t *transcript) write(msg hermod.Message, field, status string, steps int, usage llm.Usage, runErr error) {
	view := map[string]any{
		"status":  status,
		"steps":   steps,
		"usage":   map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens},
		"entries": t.Entries,
	}
	if t.Truncated {
		view["truncated"] = true
	}
	if runErr != nil {
		view["error"] = runErr.Error()
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return
	}
	var plain map[string]any
	if json.Unmarshal(raw, &plain) == nil {
		msg.SetData(field, plain)
	}
}
