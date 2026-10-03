package message

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

// A message carries the vhost of the workflow it is running in, so that
// secret("NAME") is answered from that vhost's own secrets. The mark is the
// engine's, not the payload's: it is not data and not metadata, so nothing a
// source delivers can set it, and nothing a sink writes contains it.
func TestMessageCarriesItsVHostOutsideItsContent(t *testing.T) {
	msg := AcquireMessage()
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("vhost", "claimed-by-the-row")
	msg.SetMetadata("vhost", "claimed-by-metadata")
	msg.SetVHost("tenant-a")

	if got := msg.VHost(); got != "tenant-a" {
		t.Fatalf("VHost() = %q, want tenant-a: content must not set the mark", got)
	}

	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	asMap, err := json.Marshal(msg.ToMap())
	if err != nil {
		t.Fatalf("marshal ToMap: %v", err)
	}
	for name, text := range map[string]string{"JSON": string(body), "ToMap": string(asMap)} {
		if strings.Contains(text, "tenant-a") {
			t.Errorf("the vhost mark leaked into the message's %s: %s", name, text)
		}
	}
}

// A fan-out clones the message once per branch, and each branch still runs in
// the same workflow.
func TestCloneKeepsTheVHost(t *testing.T) {
	msg := AcquireMessage()
	msg.SetVHost("tenant-a")

	clone := msg.Clone()
	scoped, ok := clone.(hermod.VHostScoped)
	if !ok {
		t.Fatal("a cloned message is not VHostScoped")
	}
	if got := scoped.VHost(); got != "tenant-a" {
		t.Errorf("clone VHost() = %q, want tenant-a", got)
	}
}

// Messages are pooled. One that ran in tenant-a must not come back out of the
// pool still marked tenant-a, or the next workflow to get it reads tenant-a's
// secrets.
func TestAPooledMessageForgetsItsVHost(t *testing.T) {
	msg := AcquireMessage()
	msg.SetVHost("tenant-a")
	msg.Reset()

	if got := msg.VHost(); got != "" {
		t.Errorf("after Reset VHost() = %q, want empty", got)
	}
}
