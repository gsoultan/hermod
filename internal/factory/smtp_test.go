package factory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// The SMTP form writes template_s3_* for the S3 template location
// (ui/src/components/workflow/Sink/SMTPSinkConfig.tsx). The factory read s3_*,
// so every field typed into that tab reached the sink as an empty S3Config:
// the tab was inert, and nothing said so.
func TestSmtpTemplateS3Config_ReadsTheKeysTheFormWrites(t *testing.T) {
	got := smtpTemplateS3Config(hermod.StringMap{
		"template_s3_region":     "ap-southeast-1",
		"template_s3_bucket":     "templates",
		"template_s3_key":        "welcome.html",
		"template_s3_endpoint":   "https://minio.example.com",
		"template_s3_access_key": "AKIAEXAMPLE",
		"template_s3_secret_key": "s3cret",
	})
	want := gsmail.S3Config{
		Region:    "ap-southeast-1",
		Bucket:    "templates",
		Key:       "welcome.html",
		Endpoint:  "https://minio.example.com",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "s3cret",
	}
	if got != want {
		t.Errorf("S3 config = %+v, want %+v", got, want)
	}
}

// A config written against the factory's own key names still works: they are
// what an imported bundle or an API caller may carry.
func TestSmtpTemplateS3Config_StillReadsTheBareKeys(t *testing.T) {
	got := smtpTemplateS3Config(hermod.StringMap{
		"s3_region": "us-east-1",
		"s3_bucket": "legacy",
		"s3_key":    "old.html",
	})
	want := gsmail.S3Config{Region: "us-east-1", Bucket: "legacy", Key: "old.html"}
	if got != want {
		t.Errorf("S3 config = %+v, want %+v", got, want)
	}
}

// When a config carries both, the form's key is the one the operator can see.
func TestSmtpTemplateS3Config_PrefersTheFormKey(t *testing.T) {
	got := smtpTemplateS3Config(hermod.StringMap{
		"s3_bucket":          "legacy",
		"template_s3_bucket": "current",
	})
	if got.Bucket != "current" {
		t.Errorf("bucket = %q, want %q", got.Bucket, "current")
	}
}

// The sink builds with an S3 template source, which is the wiring the three
// tests above only describe.
func TestCreateSink_SmtpBuildsWithAnS3Template(t *testing.T) {
	snk, err := createSinkBase(SinkConfig{
		ID:   "smtp-1",
		Type: "smtp",
		Config: hermod.StringMap{
			"host":               "smtp.example.com",
			"port":               "587",
			"from":               "ops@example.com",
			"to":                 "someone@example.com",
			"subject":            "Hello",
			"template_source":    "s3",
			"template_s3_region": "ap-southeast-1",
			"template_s3_bucket": "templates",
			"template_s3_key":    "welcome.html",
		},
	})
	if err != nil {
		t.Fatalf("building the sink failed: %v", err)
	}
	if snk == nil {
		t.Fatal("no sink was built")
	}
	_ = snk.Close()
}

// The whole chain for an S3 template, with a stand-in for S3: the form's keys,
// through the factory, into the sink, out as a fetched and rendered body. The
// three tests above describe the mapping; this one is the only thing that
// proves the mapping is the one the sink uses.
func TestCreateSink_SmtpFetchesTheTemplateTheFormPointsAt(t *testing.T) {
	var asked string
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		_, _ = w.Write([]byte(`<p>Hi {{ .name }}, on {{ .created_at.Format "02 Jan 2006" }}</p>`))
	}))
	defer s3.Close()

	snk, err := createSinkBase(SinkConfig{
		ID:   "smtp-s3",
		Type: "smtp",
		Config: hermod.StringMap{
			"from":                   "ops@example.com",
			"to":                     "someone@example.com",
			"subject":                "Hello",
			"template_source":        "s3",
			"template_s3_region":     "ap-southeast-1",
			"template_s3_bucket":     "templates",
			"template_s3_key":        "welcome.html",
			"template_s3_endpoint":   s3.URL,
			"template_s3_access_key": "AKIAEXAMPLE",
			"template_s3_secret_key": "s3cret",
		},
	})
	if err != nil {
		t.Fatalf("building the sink failed: %v", err)
	}
	defer func() { _ = snk.Close() }()

	renderer, ok := snk.(interface {
		BuildEmail(context.Context, hermod.Message) (gsmail.Email, error)
	})
	if !ok {
		t.Fatal("the smtp sink no longer renders an email without sending")
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("name", "Ana")
	msg.SetData("created_at", "2026-12-01T09:30:00Z")

	email, err := renderer.BuildEmail(t.Context(), msg)
	if err != nil {
		t.Fatalf("rendering the S3 template failed: %v", err)
	}
	if want := "/templates/welcome.html"; asked != want {
		t.Errorf("fetched %q, want %q — the bucket and key did not reach the sink", asked, want)
	}
	if want := "<p>Hi Ana, on 01 Dec 2026</p>"; string(email.Body) != want {
		t.Errorf("body = %q, want %q", string(email.Body), want)
	}
}
