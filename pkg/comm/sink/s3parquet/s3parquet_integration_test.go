//go:build integration

package s3parquet

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// The S3 Parquet sink, against a real object store.
//
// Two things are worth a server. That a batch actually becomes an object — this
// sink had never been shown to write one — and what happens to a message the
// sink cannot decode, because the answer was to drop it and say nothing.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 S3_ENDPOINT=http://127.0.0.1:9000 \
//	S3_ACCESS_KEY=minioadmin S3_SECRET_KEY=minioadmin \
//	go test -tags=integration ./pkg/comm/sink/s3parquet/

const testSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]}`

func requireS3P(t *testing.T) (endpoint, ak, sk, bucket string, client *awss3.Client) {
	t.Helper()
	endpoint = os.Getenv("S3_ENDPOINT")
	ak = os.Getenv("S3_ACCESS_KEY")
	sk = os.Getenv("S3_SECRET_KEY")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || endpoint == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q S3_ENDPOINT=%q in CI", os.Getenv("HERMOD_INTEGRATION"), endpoint)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and S3_ENDPOINT to run")
	}

	cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client = awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	bucket = "hermod-pq-" + strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	_, _ = client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	t.Cleanup(func() {
		ctx := context.Background()
		out, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err == nil {
			for _, o := range out.Contents {
				_, _ = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
					Bucket: aws.String(bucket), Key: o.Key,
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	return endpoint, ak, sk, bucket, client
}

func objectCount(t *testing.T, client *awss3.Client, bucket string) int {
	t.Helper()
	out, err := client.ListObjectsV2(context.Background(), &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return 0
	}
	return len(out.Contents)
}

func pqMsg(t *testing.T, id, name string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetData("id", id)
	m.SetData("name", name)
	return m
}

// A batch becomes an object.
func TestABatchBecomesAParquetObject(t *testing.T) {
	endpoint, ak, sk, bucket, client := requireS3P(t)

	sink, err := NewS3ParquetSink(t.Context(), "us-east-1", bucket, "pq/", ak, sk, endpoint, testSchema, 1)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.WriteBatch(t.Context(), []hermod.Message{
		pqMsg(t, "a", "ada"),
		pqMsg(t, "b", "grace"),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := objectCount(t, client, bucket); n != 1 {
		t.Errorf("a batch produced %d objects, want 1", n)
	}
}

// A message the sink cannot decode must fail in a way somebody can act on.
//
// The write loop looked like it dropped such a record silently — it fell back to
// unmarshalling the payload when Data() was nil and ran `continue` on failure.
// That branch was dead: Data() unmarshals the payload itself, so it returns an
// empty map rather than nil.
//
// What happened instead was worse. The empty map marshals to "{}", the parquet
// writer accepts a row with none of its required fields, and WriteStop fails the
// entire batch with
//
//	interface conversion: interface {} is nil, not string
//
// which names neither the record nor the reason. Every good record in the batch
// is blocked behind it, and since the engine retries a failed batch, it fails
// identically forever — a single undecodable message wedges the pipeline.
//
// So the assertion is not merely that the batch fails. It is that the failure
// names the record, which is what lets the engine dead-letter it and lets whoever
// is paged find it.
func TestAnUndecodableMessageIsNotSilentlyDropped(t *testing.T) {
	endpoint, ak, sk, bucket, _ := requireS3P(t)

	sink, err := NewS3ParquetSink(t.Context(), "us-east-1", bucket, "pq/", ak, sk, endpoint, testSchema, 1)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// No Data(), and a payload that is not JSON — which is what a message from a
	// binary or plain-text source looks like when it reaches a schema'd sink.
	bad := message.AcquireMessage()
	t.Cleanup(bad.Release)
	bad.SetID("bad")
	bad.SetOperation(hermod.OpCreate)
	bad.SetPayload([]byte("this is not json"))

	// Paired with a good record, deliberately. A batch containing only the bad
	// one fails for an unrelated reason — the parquet writer is handed zero rows
	// and panics inside WriteStop — which would let this test pass while proving
	// nothing about the drop. With a good record alongside, the file is valid,
	// the batch succeeds, and the bad record is simply gone.
	err = sink.WriteBatch(t.Context(), []hermod.Message{pqMsg(t, "good", "ada"), bad})
	if err == nil {
		t.Fatal("a message the sink could not decode was reported as written")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("the batch failed without naming the record responsible: %v\n"+
			"a parquet-writer panic surfacing as \"interface conversion\" tells whoever is "+
			"paged nothing, and the batch will fail the same way on every retry", err)
	}
}
