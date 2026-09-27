package factory

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// reachabilitySecret stands in for a token: distinctive, so a match can only
// mean the credential itself reached the error text.
const reachabilitySecret = "S3CR3Tsentinel"

// failingTransport fails every request the way a DNS failure or a network
// outage does. net/http wraps what it returns in a *url.Error quoting the
// request URL — the same error a real outage produces.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network is unreachable")
}

// TestSocialSinksBuiltFromStoredConfigKeepCredentialsOutOfErrors builds each
// URL-authenticated sink the way a workflow does — stored keys, the factory,
// the tracing and retry decorators — and fails its requests.
//
// A failed Ping is the dangerous one. Pre-flight turns it into the workflow's
// status, "Error: ...", and the status goes out in the "Workflow Error" alert
// to every notification channel. With the token in the URL, one network blip
// published a Telegram bot token, a Page access token or a whole webhook URL
// to all of them.
func TestSocialSinksBuiltFromStoredConfigKeepCredentialsOutOfErrors(t *testing.T) {
	prev := httpclient.DataClient.Transport
	httpclient.DataClient.Transport = failingTransport{}
	t.Cleanup(func() { httpclient.DataClient.Transport = prev })

	cases := []struct {
		typ  string
		cfg  hermod.StringMap
		host string
	}{
		{"telegram", hermod.StringMap{"bot_token": "123456:" + reachabilitySecret, "chat_id": "42"}, "api.telegram.org"},
		{"facebook", hermod.StringMap{"access_token": reachabilitySecret, "page_id": "1"}, "graph.facebook.com"},
		{"instagram", hermod.StringMap{"access_token": reachabilitySecret, "ig_user_id": "1"}, "graph.facebook.com"},
		{"slack", hermod.StringMap{"webhook_url": "https://hooks.slack.com/services/T000/B000/" + reachabilitySecret}, "hooks.slack.com"},
		{"discord", hermod.StringMap{"webhook_url": "https://discord.com/api/webhooks/1/" + reachabilitySecret}, "discord.com"},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			snk, err := CreateSink(SinkConfig{ID: "sink-1", Type: tc.typ, Config: tc.cfg})
			if err != nil {
				t.Fatalf("CreateSink: %v", err)
			}
			defer snk.Close()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			msg := message.AcquireMessage()
			msg.SetPayload([]byte("hello"))
			msg.SetData("image_url", "https://example.test/a.jpg")

			for op, err := range map[string]error{"Ping": snk.Ping(ctx), "Write": snk.Write(ctx, msg)} {
				if err == nil {
					t.Errorf("%s succeeded through a transport that fails every request", op)
					continue
				}
				if strings.Contains(err.Error(), reachabilitySecret) {
					t.Errorf("%s error carries the credential: %v", op, err)
				}
				// An operator still has to be able to tell which service failed.
				if !strings.Contains(err.Error(), tc.host) {
					t.Errorf("%s error no longer names the host %q: %v", op, tc.host, err)
				}
			}
		})
	}
}

// TestSocialSourcesBuiltFromStoredConfigKeepCredentialsOutOfErrors does the
// same for the sources that put their token in the URL. Their clients are
// their own, so the failure used here is the one an operator causes: a token
// pasted with its trailing newline. The URL then fails to parse, and the parse
// error quoted it — token included — into the log table on every poll.
func TestSocialSourcesBuiltFromStoredConfigKeepCredentialsOutOfErrors(t *testing.T) {
	pasted := reachabilitySecret + "\n"
	cases := []struct {
		typ string
		cfg hermod.StringMap
	}{
		{"facebook", hermod.StringMap{"access_token": pasted, "page_id": "1"}},
		{"instagram", hermod.StringMap{"access_token": pasted, "ig_user_id": "1"}},
		{"tiktok", hermod.StringMap{"access_token": pasted}},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			src, err := CreateSource(SourceConfig{ID: "source-1", Type: tc.typ, Config: tc.cfg})
			if err != nil {
				t.Fatalf("CreateSource: %v", err)
			}
			defer src.Close()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			_, readErr := src.Read(ctx)
			for op, err := range map[string]error{"Ping": src.Ping(ctx), "Read": readErr} {
				if err == nil {
					t.Errorf("%s succeeded with a token that cannot be sent", op)
					continue
				}
				if strings.Contains(err.Error(), reachabilitySecret) {
					t.Errorf("%s error carries the credential: %v", op, err)
				}
			}
		})
	}
}
