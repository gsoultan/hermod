//go:build integration

package s3

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// The S3 sink, against a real object store.
//
// S3 was Beta: substantial and unit-tested, never shown to put an object. The
// property worth a server is not "does PutObject work" — it is what happens on
// a redelivery, because that is the case the platform's delivery guarantee
// turns on.
//
// Delivery is at-least-once. What makes it exactly-once as observed at the
// destination is the sink writing the same record to the same place, so a
// redelivered message overwrites itself instead of accumulating. Every SQL sink
// does that with an upsert keyed on the message id. A sink that instead invents
// a fresh location each time silently turns every retry into a duplicate — and
// retries are not exceptional, they are the mechanism.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 S3_ENDPOINT=http://127.0.0.1:9000 \
//	S3_ACCESS_KEY=minioadmin S3_SECRET_KEY=minioadmin \
//	go test -tags=integration ./pkg/comm/sink/s3/

func requireS3(t *testing.T) (endpoint, accessKey, secretKey, bucket string) {
	t.Helper()
	endpoint = os.Getenv("S3_ENDPOINT")
	accessKey = os.Getenv("S3_ACCESS_KEY")
	secretKey = os.Getenv("S3_SECRET_KEY")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || endpoint == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q S3_ENDPOINT=%q in CI, where an object store is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), endpoint)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and S3_ENDPOINT to run")
	}

	bucket = "hermod-it-" + strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	client := rawClient(t, endpoint, accessKey, secretKey)
	_, _ = client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	t.Cleanup(func() {
		ctx := context.Background()
		for _, k := range listKeys(t, client, bucket) {
			_, _ = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(k),
			})
		}
		_, _ = client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	return endpoint, accessKey, secretKey, bucket
}

func rawClient(t *testing.T, endpoint, accessKey, secretKey string) *awss3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

func listKeys(t *testing.T, client *awss3.Client, bucket string) []string {
	t.Helper()
	out, err := client.ListObjectsV2(context.Background(), &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(out.Contents))
	for _, o := range out.Contents {
		keys = append(keys, aws.ToString(o.Key))
	}
	return keys
}

func newS3Msg(t *testing.T, id, body string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetPayload([]byte(body))
	return m
}

// The ordinary path.
func TestAnObjectIsPut(t *testing.T) {
	endpoint, ak, sk, bucket := requireS3(t)
	client := rawClient(t, endpoint, ak, sk)

	sink, err := NewS3Sink(t.Context(), "us-east-1", bucket, "recs/", ak, sk, endpoint, nil, "", "", false)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.Write(t.Context(), newS3Msg(t, "a", `{"v":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if keys := listKeys(t, client, bucket); len(keys) != 1 {
		t.Fatalf("put %d objects, want 1: %v", len(keys), keys)
	}
}

// The default keeps every delivery, and that is on purpose — but it has to be
// on purpose rather than a surprise, which is what this pins.
//
// The key carries a timestamp, so a redelivery cannot overwrite the delivery
// before it. For an archive that is correct: successive CDC updates to one row
// share a message id, and keying on the id alone would keep only the newest.
// The cost is that at-least-once leaves duplicates at rest, and until this test
// existed nothing said so anywhere.
func TestTheDefaultKeyKeepsEveryDelivery(t *testing.T) {
	endpoint, ak, sk, bucket := requireS3(t)
	client := rawClient(t, endpoint, ak, sk)

	sink, err := NewS3Sink(t.Context(), "us-east-1", bucket, "recs/", ak, sk, endpoint, nil, "", "", false)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	for i := range 3 {
		if err := sink.Write(t.Context(), newS3Msg(t, "order-42", `{"v":1}`)); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if keys := listKeys(t, client, bucket); len(keys) != 3 {
		t.Errorf("three deliveries of one message left %d objects, want 3 — the default "+
			"key is meant to preserve every delivery", len(keys))
	}
}

// idempotent_key is the other bargain: a redelivered message overwrites itself,
// which is what the platform's delivery guarantee assumes of a sink.
func TestARedeliveredMessageDoesNotLeaveASecondObject(t *testing.T) {
	endpoint, ak, sk, bucket := requireS3(t)
	client := rawClient(t, endpoint, ak, sk)

	sink, err := NewS3Sink(t.Context(), "us-east-1", bucket, "recs/", ak, sk, endpoint, nil, "", "", true)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// The same message, delivered three times — which is exactly what
	// at-least-once means when a sink write is retried or a worker restarts
	// with the message unacknowledged.
	for i := range 3 {
		if err := sink.Write(t.Context(), newS3Msg(t, "order-42", `{"v":1}`)); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}

	keys := listKeys(t, client, bucket)
	if len(keys) != 1 {
		t.Errorf("one message delivered three times left %d objects, want 1:\n  %s\n"+
			"with idempotent_key set the key is the message id, so a redelivery must "+
			"overwrite itself — that is what makes at-least-once observable as "+
			"exactly-once at the destination",
			len(keys), strings.Join(keys, "\n  "))
	}
}

// Two different messages must not collide, which is the other half of keying on
// identity rather than on a clock.
func TestDistinctMessagesLandSeparately(t *testing.T) {
	endpoint, ak, sk, bucket := requireS3(t)
	client := rawClient(t, endpoint, ak, sk)

	sink, err := NewS3Sink(t.Context(), "us-east-1", bucket, "recs/", ak, sk, endpoint, nil, "", "", false)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	for i := range 3 {
		if err := sink.Write(t.Context(), newS3Msg(t, fmt.Sprintf("order-%d", i), `{"v":1}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if keys := listKeys(t, client, bucket); len(keys) != 3 {
		t.Errorf("three distinct messages left %d objects, want 3: %v", len(keys), keys)
	}
}
