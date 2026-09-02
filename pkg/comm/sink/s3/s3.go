package s3

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gsoultan/hermod"
)

type S3Sink struct {
	client      *s3.Client
	bucket      string
	keyPrefix   string
	region      string
	suffix      string
	contentType string
	formatter   hermod.Formatter
	// idempotentKey drops the timestamp from the object key so a redelivered
	// message overwrites itself. See Write.
	idempotentKey bool
}

// NewS3Sink builds the sink. idempotentKey selects the object key layout: see
// the note in Write for why the default keeps a timestamp and when to turn it
// off.
func NewS3Sink(ctx context.Context, region, bucket, keyPrefix, accessKey, secretKey, endpoint string, formatter hermod.Formatter, suffix string, contentType string, idempotentKey bool) (*S3Sink, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})

	return &S3Sink{
		client:        client,
		bucket:        bucket,
		keyPrefix:     keyPrefix,
		region:        region,
		suffix:        suffix,
		contentType:   contentType,
		idempotentKey: idempotentKey,
		formatter:     formatter,
	}, nil
}

func (s *S3Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	// choose suffix; default to .json for backwards compatibility
	ext := s.suffix
	if ext == "" {
		ext = ".json"
	}
	if ext[0] != '.' {
		ext = "." + ext
	}
	// Where a redelivery lands.
	//
	// Delivery is at-least-once, so the same message arrives more than once
	// whenever a write is retried or a worker restarts with it unacknowledged.
	// What normally makes that harmless is the sink writing the record to the
	// same place twice; every SQL sink does it with an upsert keyed on the
	// message id.
	//
	// The default here does the opposite: the key carries a timestamp, so every
	// delivery invents a new location and a redelivery cannot overwrite the one
	// before it. That is deliberate rather than accidental — it is what an
	// archive wants, because successive CDC updates to one row share an id and
	// keying on the id alone would keep only the newest. But it does mean
	// retries leave duplicates at rest, so it is opt-out rather than silent:
	// idempotent_key keys on the message id and lets a redelivery overwrite
	// itself.
	key := fmt.Sprintf("%s%s_%d%s", s.keyPrefix, msg.ID(), time.Now().UnixNano(), ext)
	if s.idempotentKey {
		key = fmt.Sprintf("%s%s%s", s.keyPrefix, msg.ID(), ext)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if ct := strings.TrimSpace(s.contentType); ct != "" {
		input.ContentType = aws.String(ct)
	}

	_, err = s.client.PutObject(ctx, input)

	if err != nil {
		return fmt.Errorf("failed to put object to s3: %w", err)
	}

	return nil
}

func (s *S3Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if err := s.Write(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3Sink) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("failed to ping s3 bucket: %w", err)
	}
	return nil
}

func (s *S3Sink) Close() error {
	return nil
}
