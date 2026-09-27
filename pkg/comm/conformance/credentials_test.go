package conformance_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"

	sinkdiscord "github.com/gsoultan/hermod/pkg/comm/sink/discord"
	sinkfacebook "github.com/gsoultan/hermod/pkg/comm/sink/facebook"
	sinkinstagram "github.com/gsoultan/hermod/pkg/comm/sink/instagram"
	sinklinkedin "github.com/gsoultan/hermod/pkg/comm/sink/linkedin"
	sinkslack "github.com/gsoultan/hermod/pkg/comm/sink/slack"
	sinktelegram "github.com/gsoultan/hermod/pkg/comm/sink/telegram"
	sinktiktok "github.com/gsoultan/hermod/pkg/comm/sink/tiktok"
	sinktwitter "github.com/gsoultan/hermod/pkg/comm/sink/twitter"

	srcdiscord "github.com/gsoultan/hermod/pkg/comm/source/discord"
	srcfacebook "github.com/gsoultan/hermod/pkg/comm/source/facebook"
	srcinstagram "github.com/gsoultan/hermod/pkg/comm/source/instagram"
	srclinkedin "github.com/gsoultan/hermod/pkg/comm/source/linkedin"
	srcslack "github.com/gsoultan/hermod/pkg/comm/source/slack"
	srctiktok "github.com/gsoultan/hermod/pkg/comm/source/tiktok"
	srctwitter "github.com/gsoultan/hermod/pkg/comm/source/twitter"
)

// credentialSentinel stands in for a token: distinctive, so a match can only
// mean the credential itself reached the error text.
const credentialSentinel = "S3CR3Tsentinel"

// TestConnectorErrorsDoNotCarryCredentials holds every social connector to one
// rule: whatever it fails with, the error does not contain its credential.
//
// An error does not stay with the connector that raised it. The engine writes
// it to the log table, sets it as the workflow's status, and puts that status
// in the "Workflow Error" alert, which goes to every notification channel. And
// net/http quotes the full request URL in a failed request's error, removing
// only a userinfo password — so the connectors that authenticate through their
// URL (Telegram's bot path, the Graph API's access_token, a webhook URL that is
// itself the credential) handed the secret to all of those on any network
// failure.
//
// The connectors that send their credential in a header pass already. They are
// in the table anyway, because it is also what stops one of them moving the
// credential into the URL later.
func TestConnectorErrorsDoNotCarryCredentials(t *testing.T) {
	// The same secret spelled two ways: as typed, and pasted with the newline a
	// copy from a web page brings along. The second fails URL parsing before
	// anything is sent, and the parse error quotes the URL as well.
	spellings := []struct{ name, secret string }{
		{"typed", credentialSentinel},
		{"pasted", credentialSentinel + "\n"},
	}
	for _, sp := range spellings {
		for _, tc := range credentialCases(sp.secret) {
			t.Run(sp.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()

				for op, err := range tc.run(ctx) {
					if err == nil {
						t.Errorf("%s succeeded against an address nothing listens on, so its failure was never examined", op)
						continue
					}
					if strings.Contains(err.Error(), credentialSentinel) {
						t.Errorf("%s error carries the credential: %v", op, err)
					}
				}
			})
		}
	}
}

type credentialCase struct {
	name string
	// run exercises every call that reaches the network and returns each
	// call's error by name.
	run func(ctx context.Context) map[string]error
}

func credentialCases(secret string) []credentialCase {
	const poll = 50 * time.Millisecond

	sink := func(s hermod.Sink) func(context.Context) map[string]error {
		return func(ctx context.Context) map[string]error {
			defer s.Close()
			return map[string]error{
				"Ping":  s.Ping(ctx),
				"Write": s.Write(ctx, credentialMessage()),
			}
		}
	}
	vendorSink := func(s hermod.Sink) func(context.Context) map[string]error {
		s.(interface{ SetBaseURL(string) }).SetBaseURL(deadURL)
		return sink(s)
	}
	vendorSource := func(s hermod.Source) func(context.Context) map[string]error {
		s.(interface{ SetBaseURL(string) }).SetBaseURL(deadURL)
		return func(ctx context.Context) map[string]error {
			defer s.Close()
			_, readErr := s.Read(ctx)
			return map[string]error{
				"Ping": s.Ping(ctx),
				"Read": readErr,
			}
		}
	}

	// A Slack or Discord webhook URL is the credential in its entirety.
	webhook := deadURL + "/services/T000/B000/" + secret

	return []credentialCase{
		{"sink/telegram", vendorSink(sinktelegram.NewTelegramSink("123456:"+secret, "42", nil))},
		{"sink/facebook", vendorSink(sinkfacebook.NewFacebookSink(secret, "1", nil))},
		{"sink/instagram", vendorSink(sinkinstagram.NewInstagramSink(secret, "1", nil))},
		{"sink/linkedin", vendorSink(sinklinkedin.NewLinkedInSink(secret, "urn:li:person:x", nil))},
		{"sink/tiktok", vendorSink(sinktiktok.NewTikTokSink(secret, nil))},
		{"sink/twitter", vendorSink(sinktwitter.NewTwitterSink(secret, nil))},
		{"sink/slack webhook", sink(sinkslack.NewSlackSink(webhook, "", "", nil))},
		{"sink/discord webhook", sink(sinkdiscord.NewDiscordSink(webhook, "", "", nil))},

		{"source/facebook feed", vendorSource(srcfacebook.NewFacebookSource(secret, "1", poll, "feed"))},
		{"source/facebook comments", vendorSource(srcfacebook.NewFacebookSource(secret, "1", poll, "comments"))},
		{"source/facebook insights", vendorSource(srcfacebook.NewFacebookSource(secret, "1", poll, "insights"))},
		{"source/instagram media", vendorSource(srcinstagram.NewInstagramSource(secret, "1", poll, "media"))},
		{"source/instagram comments", vendorSource(srcinstagram.NewInstagramSource(secret, "1", poll, "comments"))},
		{"source/instagram insights", vendorSource(srcinstagram.NewInstagramSource(secret, "1", poll, "insights"))},
		{"source/tiktok video", vendorSource(srctiktok.NewTikTokSource(secret, poll, "video"))},
		{"source/tiktok comments", vendorSource(srctiktok.NewTikTokSource(secret, poll, "comments"))},
		{"source/tiktok statistics", vendorSource(srctiktok.NewTikTokSource(secret, poll, "statistics"))},
		{"source/twitter search", vendorSource(srctwitter.NewTwitterSource(secret, "hermod", poll, "search"))},
		{"source/twitter mentions", vendorSource(srctwitter.NewTwitterSource(secret, "hermod", poll, "mentions"))},
		{"source/linkedin", vendorSource(srclinkedin.NewLinkedInSource(secret, "urn:li:person:x", poll))},
		{"source/slack", vendorSource(srcslack.NewSlackSource(secret, "C1", poll))},
		{"source/discord", vendorSource(srcdiscord.NewDiscordSource(secret, "1", poll))},
	}
}

// credentialMessage carries what every social sink needs before it will send:
// text, and for the media sinks a media URL.
func credentialMessage() hermod.Message {
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("hello"))
	msg.SetData("image_url", "https://example.test/a.jpg")
	msg.SetData("video_url", "https://example.test/a.mp4")
	return msg
}
