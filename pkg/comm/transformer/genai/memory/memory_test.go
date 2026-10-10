package memory

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

func TestParse(t *testing.T) {
	if _, on, err := Parse(nil); on || err != nil {
		t.Fatalf("absent: on=%v err=%v", on, err)
	}
	if _, on, err := Parse(map[string]any{"enabled": false, "maxTurns": 3}); on || err != nil {
		t.Fatalf("disabled: on=%v err=%v", on, err)
	}
	c, on, err := Parse(map[string]any{})
	if !on || err != nil || c.Field != DefaultField || c.MaxTurns != DefaultMaxTurns || c.TTL != DefaultTTL {
		t.Fatalf("defaults: %+v on=%v err=%v", c, on, err)
	}
	c, _, err = Parse(map[string]any{"conversationField": "chat_id", "maxTurns": float64(4), "ttl": "2h"})
	if err != nil || c.Field != "chat_id" || c.MaxTurns != 4 || c.TTL != 2*time.Hour {
		t.Fatalf("explicit: %+v err=%v", c, err)
	}
	for _, bad := range []any{
		"yes",
		map[string]any{"maxTurns": 0.0},
		map[string]any{"maxTurns": float64(MaxTurnsLimit + 1)},
		map[string]any{"maxTurns": 2.5},
		map[string]any{"ttl": "forever"},
		map[string]any{"ttl": "-1h"},
		map[string]any{"ttl": (MaxTTL + time.Hour).String()},
		map[string]any{"conversationField": 7},
	} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%v) accepted", bad)
		}
	}
}

func chatMsg(conv string) hermod.Message {
	m := message.AcquireMessage()
	m.SetMetadata("_hermod_workflow_id", "wf")
	if conv != "" {
		m.SetMetadata("conversation_id", conv)
	}
	return m
}

func ctxWith(store hermod.StateStore, node string) context.Context {
	ctx := context.WithValue(context.Background(), hermod.StateStoreKey, store)
	return context.WithValue(ctx, hermod.NodeIDKey, node)
}

func TestConversationRemembersTurnsWithinItsLimit(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := ctxWith(store, "chat")
	cfg := Config{Field: DefaultField, MaxTurns: 2, TTL: time.Hour}

	for _, q := range []string{"one", "two", "three"} {
		c, err := Open(ctx, chatMsg("c-1"), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(ctx, q, "answer "+q); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := Open(ctx, chatMsg("c-1"), cfg)
	turns, err := c.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{{RoleUser, "two"}, {RoleAssistant, "answer two"}, {RoleUser, "three"}, {RoleAssistant, "answer three"}}
	if len(turns) != len(want) {
		t.Fatalf("history = %+v", turns)
	}
	for i := range want {
		if turns[i] != want[i] {
			t.Fatalf("history = %+v, want %+v", turns, want)
		}
	}

	// Another conversation, and the same conversation at another node, start empty.
	other, _ := Open(ctx, chatMsg("c-2"), cfg)
	if h, _ := other.History(ctx); len(h) != 0 {
		t.Errorf("another conversation sees %+v", h)
	}
	elsewhere, _ := Open(ctxWith(store, "other-node"), chatMsg("c-1"), cfg)
	if h, _ := elsewhere.History(ctx); len(h) != 0 {
		t.Errorf("another node sees %+v", h)
	}
}

func TestConversationExpires(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := ctxWith(store, "chat")
	cfg := Config{Field: DefaultField, MaxTurns: 5, TTL: time.Minute}
	t0 := time.Now()
	restore := setNow(func() time.Time { return t0 })
	defer restore()

	c, _ := Open(ctx, chatMsg("c-1"), cfg)
	_ = c.Append(ctx, "hi", "hello")

	setNow(func() time.Time { return t0.Add(2 * time.Minute) })
	if h, _ := c.History(ctx); len(h) != 0 {
		t.Fatalf("an expired conversation was remembered: %+v", h)
	}
	_ = c.Append(ctx, "again", "back")
	if h, _ := c.History(ctx); len(h) != 2 || h[0].Text != "again" {
		t.Fatalf("after expiry: %+v", h)
	}
}

func TestTurnsAreBounded(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := ctxWith(store, "chat")
	cfg := Config{Field: DefaultField, MaxTurns: MaxTurnsLimit, TTL: time.Hour}
	c, _ := Open(ctx, chatMsg("c-1"), cfg)
	big := strings.Repeat("x", MaxTurnBytes*3)
	for range 40 {
		if err := c.Append(ctx, big, big); err != nil {
			t.Fatal(err)
		}
	}
	turns, _ := c.History(ctx)
	total := 0
	for _, tr := range turns {
		if len(tr.Text) > MaxTurnBytes {
			t.Fatalf("a turn of %d bytes was kept", len(tr.Text))
		}
		total += len(tr.Text)
	}
	if total > MaxTotalBytes {
		t.Fatalf("kept %d bytes", total)
	}
	if len(turns) == 0 || turns[0].Role != RoleUser {
		t.Fatalf("history must start with a user turn: %d turns", len(turns))
	}
	raw, _ := store.Get(ctx, c.key)
	if len(raw) > MaxTotalBytes+4096 {
		t.Fatalf("stored %d bytes", len(raw))
	}
}

// Concurrent messages of one conversation do not lose each other's turns.
func TestConcurrentAppendsAreAllKept(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := ctxWith(store, "chat")
	cfg := Config{Field: DefaultField, MaxTurns: MaxTurnsLimit, TTL: time.Hour}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			c, err := Open(ctx, chatMsg("c-1"), cfg)
			if err == nil {
				err = c.Append(ctx, "q", "a")
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	c, _ := Open(ctx, chatMsg("c-1"), cfg)
	if h, _ := c.History(ctx); len(h) != 40 {
		t.Fatalf("kept %d turns, want 40", len(h))
	}
}

func TestOpenNeedsAStoreAndAConversation(t *testing.T) {
	cfg := Config{Field: DefaultField, MaxTurns: 2, TTL: time.Hour}
	if _, err := Open(context.Background(), chatMsg("c-1"), cfg); err == nil {
		t.Error("opened with no state store")
	}
	if _, err := Open(ctxWith(state.NewMemoryStore(), "n"), chatMsg(""), cfg); err == nil {
		t.Error("opened with no conversation id")
	}
	if _, err := Open(ctxWith(state.NewMemoryStore(), "n"), chatMsg("bad id/x"), cfg); err == nil {
		t.Error("opened with an invalid conversation id")
	}
	// The id may come from the payload.
	m := message.AcquireMessage()
	m.SetData("chat_id", "from-data")
	if _, err := Open(ctxWith(state.NewMemoryStore(), "n"), m, Config{Field: "chat_id", MaxTurns: 2, TTL: time.Hour}); err != nil {
		t.Errorf("payload field: %v", err)
	}
}
