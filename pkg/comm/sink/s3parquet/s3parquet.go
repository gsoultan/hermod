package s3parquet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gsoultan/hermod"
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
	schemaFields map[string]struct{}
	opField      string
	parallelizer int64
}

// defaultOperationField is the column the CDC operation is written to when the
// schema declares it and the connector was not told to use another name.
const defaultOperationField = "operation"

func NewS3ParquetSink(ctx context.Context, region, bucket, keyPrefix, accessKey, secretKey, endpoint, schema, operationField string, parallelizer int64) (*S3ParquetSink, error) {
	if parallelizer <= 0 {
		parallelizer = 4
	}
	schemaFields := schemaFieldNames(schema)
	opField, err := resolveOperationField(operationField, schemaFields)
	if err != nil {
		return nil, err
	}
	return &S3ParquetSink{
		region:       region,
		bucket:       bucket,
		keyPrefix:    keyPrefix,
		accessKey:    accessKey,
		secretKey:    secretKey,
		endpoint:     endpoint,
		schema:       schema,
		schemaFields: schemaFields,
		opField:      opField,
		parallelizer: parallelizer,
	}, nil
}

// resolveOperationField decides which column carries msg.Operation(), returning
// "" when nothing should be written for it.
//
// A column named explicitly but absent from the schema is a configuration
// error, not something to shrug off. The connector was told to record the CDC
// operation; writing files where a delete is indistinguishable from an insert
// instead — with nothing in the log to say so — is the failure mode this whole
// change exists to remove. Refuse at construction, while the operator is still
// looking at the form.
//
// Left empty, the default column is used only when the schema declares it, so
// schemas written before this existed keep producing exactly the columns they
// always have.
func resolveOperationField(configured string, schemaFields map[string]struct{}) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		if _, ok := schemaFields[defaultOperationField]; ok {
			return defaultOperationField, nil
		}
		return "", nil
	}
	// A nil schemaFields means the schema could not be parsed, which callers
	// treat as "no opinion" rather than "no columns" — see schemaFieldNames.
	if len(schemaFields) > 0 {
		if _, ok := schemaFields[configured]; !ok {
			known := make([]string, 0, len(schemaFields))
			for name := range schemaFields {
				known = append(known, name)
			}
			sort.Strings(known)
			return "", fmt.Errorf("operation_field %q is not a column in the parquet schema; "+
				"add it to the schema or pick one of: %s",
				configured, strings.Join(known, ", "))
		}
	}
	return configured, nil
}

// withOperation materialises the CDC operation into the row about to be
// written. A value already in the record was computed by the pipeline and
// outranks the envelope's; an operation never set at all is an insert, because
// writing "" into the column would be a value no reader can interpret.
func (s *S3ParquetSink) withOperation(data map[string]any, op hermod.Operation) map[string]any {
	if s.opField == "" {
		return data
	}
	if data == nil {
		data = make(map[string]any, 1)
	}
	if _, ok := data[s.opField]; ok {
		return data
	}
	if op == "" {
		op = hermod.OpCreate
	}
	data[s.opField] = string(op)
	return data
}

// schemaFieldNames pulls the top-level column names out of a parquet-go JSON
// schema, whose fields carry them inside a comma-separated Tag: "name=id,
// type=BYTE_ARRAY, ...".
//
// It returns nil when the schema cannot be parsed, which callers treat as "no
// opinion" rather than "no columns" — refusing every record because the schema
// string is in a shape not recognised here would be worse than the write error
// the parquet writer would raise anyway.
func schemaFieldNames(schema string) map[string]struct{} {
	var parsed struct {
		Fields []struct {
			Tag string `json:"Tag"`
		} `json:"Fields"`
	}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		return nil
	}
	names := make(map[string]struct{}, len(parsed.Fields))
	for _, f := range parsed.Fields {
		for part := range strings.SplitSeq(f.Tag, ",") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(part), "name="); ok {
				if name := strings.TrimSpace(rest); name != "" {
					names[name] = struct{}{}
				}
				break
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

// writableFieldCount reports how many of the schema's columns this record can
// actually fill. Zero means the parquet writer would be handed a row with none
// of its required fields.
func writableFieldCount(data map[string]any, schemaFields map[string]struct{}) int {
	if len(schemaFields) == 0 {
		// Schema shape unknown; fall back to "does it carry anything at all".
		return len(data)
	}
	n := 0
	for k := range data {
		if _, ok := schemaFields[k]; ok {
			n++
		}
	}
	return n
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
		// Data() unmarshals the payload itself, so the skip never ran.
		//
		// The check is against the schema's columns rather than against Data()
		// being empty, because a payload that is not a JSON object now decodes
		// to a single synthetic "payload" field. That is a real field, so an
		// emptiness test passes it straight through to the writer — which is
		// the very failure described below.
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
		if writableFieldCount(data, s.schemaFields) == 0 {
			return fmt.Errorf("message %s carries no field the parquet schema can be "+
				"built from: none of its keys match a schema column and the payload "+
				"is not a JSON object (%.60q)",
				msg.ID(), string(msg.Payload()))
		}
		data = s.withOperation(data, msg.Operation())

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
