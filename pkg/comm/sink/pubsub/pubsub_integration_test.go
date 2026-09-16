//go:build integration
// +build integration

package pubsub

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestPubSubSinkPublishesAndPingsAgainstTheEmulator exercises the paths the v2
// migration rewrote, against a real Pub/Sub server.
//
// The migration replaced Topic with Publisher and lost Topic.Exists, so Ping had
// to be rebuilt on the admin API. Both changes compiled and neither was executed
// by anything: the package had no tests. Compiling is not evidence that a
// message arrives.
func TestPubSubSinkPublishesAndPingsAgainstTheEmulator(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	host := os.Getenv("PUBSUB_EMULATOR_HOST")
	if host == "" {
		t.Fatal("integration: HERMOD_INTEGRATION=1 is set but PUBSUB_EMULATOR_HOST is not")
	}

	const (
		project = "hermod-test"
		topicID = "flow-topic"
		subID   = "flow-sub"
	)
	ctx := t.Context()

	admin, err := pubsub.NewClient(ctx, project)
	if err != nil {
		t.Fatalf("the configured Pub/Sub emulator is not reachable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	topicName := fmt.Sprintf("projects/%s/topics/%s", project, topicID)
	subName := fmt.Sprintf("projects/%s/subscriptions/%s", project, subID)

	// Recreate both, so a rerun starts with no backlog from the last one.
	_ = admin.TopicAdminClient.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{Topic: topicName})
	_ = admin.SubscriptionAdminClient.DeleteSubscription(ctx,
		&pubsubpb.DeleteSubscriptionRequest{Subscription: subName})
	if _, err := admin.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName}); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if _, err := admin.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name: subName, Topic: topicName,
	}); err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	sink, err := NewPubSubSink(project, topicID, "", nil)
	if err != nil {
		t.Fatalf("NewPubSubSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// Ping must succeed for a topic that exists. Before the migration this was
	// Topic.Exists; it is now a GetTopic on the admin client, and getting that
	// wrong would have reported a healthy sink as broken.
	if err := sink.Ping(ctx); err != nil {
		t.Fatalf("Ping on an existing topic: %v", err)
	}

	msg := message.AcquireMessage()
	msg.SetID("P-1")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("orders")
	msg.SetSchema("public")
	msg.SetPayload([]byte(`{"id":"P-1","amount":"12.50"}`))

	if err := sink.Write(ctx, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back, which is the only thing that proves the publish path works.
	recvCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var once sync.Once
	var got *pubsub.Message
	err = admin.Subscriber(subID).Receive(recvCtx, func(_ context.Context, m *pubsub.Message) {
		once.Do(func() {
			got = m
			cancel()
		})
		m.Ack()
	})
	if err != nil && recvCtx.Err() == nil {
		t.Fatalf("Receive: %v", err)
	}
	if got == nil {
		t.Fatal("nothing arrived on the subscription: the published message did not reach Pub/Sub")
	}

	if string(got.Data) != `{"id":"P-1","amount":"12.50"}` {
		t.Errorf("payload = %s, want the message body", got.Data)
	}
	// The attributes the sink sets by hand, which the migration also had to carry.
	for k, want := range map[string]string{
		"id": "P-1", "operation": "create", "table": "orders", "schema": "public",
	} {
		if got.Attributes[k] != want {
			t.Errorf("attribute %q = %q, want %q", k, got.Attributes[k], want)
		}
	}
}

// TestPingReportsAMissingTopic covers the other half of the rebuilt Ping: the
// NotFound branch, which has to be told apart from a transport failure.
func TestPingReportsAMissingTopic(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" || os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and PUBSUB_EMULATOR_HOST")
	}
	sink, err := NewPubSubSink("hermod-test", "no-such-topic", "", nil)
	if err != nil {
		t.Fatalf("NewPubSubSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	err = sink.Ping(t.Context())
	if err == nil {
		t.Fatal("Ping succeeded for a topic that does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing topic was not reported as missing: %v", err)
	}
}
