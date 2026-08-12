package smtp

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/gsmail"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// A send that failed must be retryable.
//
// The sink claims an idempotency key, sends, then marks the key sent. The claim
// is what stops a replay being delivered twice — but it was never given up when
// the send failed, so the key stayed claimed for good.
//
// The retry then found the key already claimed, treated it as a duplicate, and
// reported success. Any transient SMTP failure — a timeout, a refused
// connection, a rate limit — permanently dropped the message while the pipeline
// recorded a delivery. The guarantee is at-least-once; this made it at-most-once
// for every key that ever hit a temporary error.
// ---------------------------------------------------------------------------

// countingStore is an in-memory idempotency store that records what it was asked.
type countingStore struct {
	mu       sync.Mutex
	claimed  map[string]bool
	sent     map[string]bool
	releases int
}

func newCountingStore() *countingStore {
	return &countingStore{claimed: map[string]bool{}, sent: map[string]bool{}}
}

func (c *countingStore) Claim(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimed[key] {
		return false, nil
	}
	c.claimed[key] = true
	return true, nil
}

func (c *countingStore) MarkSent(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent[key] = true
	return nil
}

func (c *countingStore) Release(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sent[key] {
		return nil // a completed key stays claimed
	}
	delete(c.claimed, key)
	c.releases++
	return nil
}

// fakeSender stands in for the SMTP provider.
type fakeSender struct {
	mu        sync.Mutex
	failures  int
	attempts  int
	delivered int
}

func (f *fakeSender) Send(_ context.Context, _ gsmail.Email) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.failures > 0 {
		f.failures--
		return errors.New("smtp: connection refused")
	}
	f.delivered++
	return nil
}

func (f *fakeSender) Validate(context.Context, string) error { return nil }
func (f *fakeSender) Ping(context.Context) error             { return nil }
func (f *fakeSender) SetRetryConfig(gsmail.RetryConfig)      {}

func (f *fakeSender) counts() (attempts, delivered int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts, f.delivered
}

// sinkUnderTest wires a sink to a fake sender and an in-memory store.
func sinkUnderTest(t *testing.T, sender *fakeSender, store IdempotencyStore) *SmtpSink {
	t.Helper()
	s := &SmtpSink{
		sender:  sender,
		from:    "from@example.com",
		to:      []string{"to@example.com"},
		subject: "subject",
	}
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)
	return s
}

func testMessage(t *testing.T) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID("m-1")
	m.SetData("body", "hello")
	t.Cleanup(func() { message.ReleaseMessage(m) })
	return m
}

// TestARetryAfterAFailedSendIsDelivered is the whole bug in one test.
//
// The first attempt claims the key and the send fails. The retry must get
// through — before the claim was released, it was refused as a duplicate and the
// message was silently dropped.
func TestARetryAfterAFailedSendIsDelivered(t *testing.T) {
	sender := &fakeSender{failures: 1}
	store := newCountingStore()
	sink := sinkUnderTest(t, sender, store)

	if err := sink.Write(t.Context(), testMessage(t)); err == nil {
		t.Fatal("the first send failed but the sink reported success")
	}

	// The engine retries the same message.
	if err := sink.Write(t.Context(), testMessage(t)); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}

	attempts, delivered := sender.counts()
	if delivered != 1 {
		t.Errorf("the message was delivered %d time(s) across a failure and a retry, want 1; "+
			"a transient SMTP error drops it permanently and the pipeline records a delivery",
			delivered)
	}
	if attempts != 2 {
		t.Errorf("the sender saw %d attempt(s), want 2; the retry never reached it", attempts)
	}
}

// TestAGenuineDuplicateIsStillSuppressed. Releasing on failure must not weaken
// the suppression that makes at-least-once safe.
func TestAGenuineDuplicateIsStillSuppressed(t *testing.T) {
	sender := &fakeSender{}
	store := newCountingStore()
	sink := sinkUnderTest(t, sender, store)

	for range 3 {
		if err := sink.Write(t.Context(), testMessage(t)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	_, delivered := sender.counts()
	if delivered != 1 {
		t.Errorf("the same message was delivered %d times; duplicate suppression is the "+
			"reason the pipeline can be at-least-once", delivered)
	}
}

// TestRepeatedFailuresKeepRetrying: the claim must be released every time, not
// only the first.
func TestRepeatedFailuresKeepRetrying(t *testing.T) {
	sender := &fakeSender{failures: 3}
	store := newCountingStore()
	sink := sinkUnderTest(t, sender, store)

	for i := range 3 {
		if err := sink.Write(t.Context(), testMessage(t)); err == nil {
			t.Fatalf("attempt %d reported success while the sender was failing", i+1)
		}
	}
	if err := sink.Write(t.Context(), testMessage(t)); err != nil {
		t.Fatalf("the fourth attempt failed: %v", err)
	}

	_, delivered := sender.counts()
	if delivered != 1 {
		t.Errorf("delivered %d time(s) after three failures and a success, want 1", delivered)
	}
}
