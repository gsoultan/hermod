package s3parquet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gsoultan/Hermod"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/writer"
)

type S3ParquetSink struct {
	region       string
	bucket       string
	keyPrefix    string
	accessKey    string
	secretKey    string
	endpoint     string
	schema       string
	parallelizer int64
}

func NewS3ParquetSink(ctx context.Context, region, bucket, keyPrefix, accessKey, secretKey, endpoint, schema string, parallelizer int64) (*S3ParquetSink, error) {
	if parallelizer <= 0 {
		parallelizer = 4
	}
	return &S3ParquetSink{
		region:       region,
		bucket:       bucket,
		keyPrefix:    keyPrefix,
		accessKey:    accessKey,
		secretKey:    secretKey,
		endpoint:     endpoint,
		schema:       schema,
		parallelizer: parallelizer,
	}, nil
}

func (s *S3ParquetSink) getS3Client(ctx context.Context) (*s3.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(s.region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.accessKey, s.secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if s.endpoint != "" {
			o.BaseEndpoint = aws.String(s.endpoint)
			o.UsePathStyle = true
		}
	})
	return client, nil
}

func (s *S3ParquetSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *S3ParquetSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	key := fmt.Sprintf("%s%d_%d.parquet", s.keyPrefix, time.Now().Unix(), time.Now().UnixNano())

	tmpFile, err := os.CreateTemp("", "hermod-*.parquet")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFileName := tmpFile.Name()
	defer os.Remove(tmpFileName)
	tmpFile.Close() // Close it so parquet writer can open it

	fw, err := local.NewLocalFileWriter(tmpFileName)
	if err != nil {
		return fmt.Errorf("failed to create local file writer: %w", err)
	}

	pw, err := writer.NewJSONWriter(s.schema, fw, s.parallelizer)
	if err != nil {
		fw.Close()
		return fmt.Errorf("failed to create parquet writer: %w", err)
	}

	for _, msg := range filtered {
		// Refuse a record with nothing to write, naming it.
		//
		// This used to fall back to unmarshalling the payload when Data() was
		// nil and `continue` past a record it could not decode — silently, with
		// the batch still reporting success. That fallback was also dead:
		// Data() unmarshals the payload itself, so it returns an empty map
		// rather than nil and the skip never ran.
		//
		// What actually happened was worse than the silent drop it looked like.
		// An empty map marshals to "{}", the parquet writer accepts a row with
		// none of its required fields, and WriteStop then fails the whole batch
		// with "interface conversion: interface {} is nil, not string" — a
		// message naming neither the record nor the reason. Every good record in
		// that batch is blocked behind it, and because the engine retries a
		// failed batch it fails the same way forever.
		//
		// Failing here instead gives the engine something it can act on: the
		// error names the record, and a batch that keeps failing goes to the
		// dead-letter sink rather than wedging the pipeline.
		data := msg.Data()
		if len(data) == 0 {
			return fmt.Errorf("message %s carries no data the parquet schema can be "+
				"built from: Data() is empty and the payload is not JSON (%.60q)",
				msg.ID(), string(msg.Payload()))
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			pw.WriteStop()
			fw.Close()
			return fmt.Errorf("failed to marshal message data to json: %w", err)
		}

		if err := pw.Write(string(jsonData)); err != nil {
			pw.WriteStop()
			fw.Close()
			return fmt.Errorf("failed to write message to parquet: %w", err)
		}
	}

	if err := pw.WriteStop(); err != nil {
		fw.Close()
		return fmt.Errorf("failed to stop parquet writer: %w", err)
	}
	fw.Close()

	// Upload to S3
	client, err := s.getS3Client(ctx)
	if err != nil {
		return err
	}

	file, err := os.Open(tmpFileName)
	if err != nil {
		return fmt.Errorf("failed to open temp file for upload: %w", err)
	}
	defer file.Close()

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   file,
	})

	if err != nil {
		return fmt.Errorf("failed to put object to s3: %w", err)
	}

	return nil
}

func (s *S3ParquetSink) Ping(ctx context.Context) error {
	client, err := s.getS3Client(ctx)
	if err != nil {
		return err
	}
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("failed to ping s3 bucket: %w", err)
	}
	return nil
}

func (s *S3ParquetSink) Close() error {
	return nil
}
