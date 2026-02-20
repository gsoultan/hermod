package file

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type SourceType string

const (
	SourceTypeLocal  SourceType = "local"
	SourceTypeHTTP   SourceType = "http"
	SourceTypeS3     SourceType = "s3"
	SourceTypeCustom SourceType = "custom"
)

// CSVSource implements the hermod.Source interface for CSV files.
type CSVSource struct {
	sourceType SourceType
	filePath   string
	url        string
	headersMap map[string]string

	// S3 config
	s3Region    string
	s3Bucket    string
	s3Key       string
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string

	delimiter rune
	hasHeader bool
	closer    io.Closer
	reader    *csv.Reader
	headers   []string
	finished  bool

	// custom reader (for in-memory/remote streams like SFTP)
	rc io.ReadCloser
}

// NewCSVSource creates a new CSVSource.
func NewCSVSource(filePath string, delimiter rune, hasHeader bool) *CSVSource {
	if delimiter == 0 {
		delimiter = ','
	}
	return &CSVSource{
		sourceType: SourceTypeLocal,
		filePath:   filePath,
		delimiter:  delimiter,
		hasHeader:  hasHeader,
	}
}

// NewHTTPCSVSource creates a new CSVSource for HTTP.
func NewHTTPCSVSource(url string, delimiter rune, hasHeader bool, headers map[string]string) *CSVSource {
	if delimiter == 0 {
		delimiter = ','
	}
	return &CSVSource{
		sourceType: SourceTypeHTTP,
		url:        url,
		delimiter:  delimiter,
		hasHeader:  hasHeader,
		headersMap: headers,
	}
}

// NewS3CSVSource creates a new CSVSource for S3.
func NewS3CSVSource(region, bucket, key, endpoint, accessKey, secretKey string, delimiter rune, hasHeader bool) *CSVSource {
	if delimiter == 0 {
		delimiter = ','
	}
	return &CSVSource{
		sourceType:  SourceTypeS3,
		s3Region:    region,
		s3Bucket:    bucket,
		s3Key:       key,
		s3Endpoint:  endpoint,
		s3AccessKey: accessKey,
		s3SecretKey: secretKey,
		delimiter:   delimiter,
		hasHeader:   hasHeader,
	}
}

// NewCSVSourceFromReadCloser creates a CSVSource backed by a provided reader (e.g., SFTP file stream).
func NewCSVSourceFromReadCloser(rc io.ReadCloser, delimiter rune, hasHeader bool) *CSVSource {
	if delimiter == 0 {
		delimiter = ','
	}
	return &CSVSource{
		sourceType: SourceTypeCustom,
		delimiter:  delimiter,
		hasHeader:  hasHeader,
		rc:         rc,
	}
}

func (s *CSVSource) init(ctx context.Context) error {
	var rc io.ReadCloser

	switch s.sourceType {
	case SourceTypeLocal:
		file, err := os.Open(s.filePath)
		if err != nil {
			return fmt.Errorf("failed to open csv file: %w", err)
		}
		rc = file
	case SourceTypeHTTP:
		req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
		if err != nil {
			return fmt.Errorf("failed to create http request: %w", err)
		}
		for k, v := range s.headersMap {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to fetch csv from http: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("failed to fetch csv from http: status %d", resp.StatusCode)
		}
		rc = resp.Body
	case SourceTypeS3:
		cfg, err := config.LoadDefaultConfig(ctx,
			config.WithRegion(s.s3Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.s3AccessKey, s.s3SecretKey, "")),
		)
		if err != nil {
			return fmt.Errorf("failed to load aws config: %w", err)
		}

		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			if s.s3Endpoint != "" {
				o.BaseEndpoint = aws.String(s.s3Endpoint)
				o.UsePathStyle = true
			}
		})

		resp, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.s3Key),
		})
		if err != nil {
			return fmt.Errorf("failed to get object from s3: %w", err)
		}
		rc = resp.Body
	case SourceTypeCustom:
		if s.rc == nil {
			return fmt.Errorf("custom CSV source: reader is nil")
		}
		rc = s.rc
	default:
		return fmt.Errorf("unsupported source type: %s", s.sourceType)
	}

	s.closer = rc
	s.reader = csv.NewReader(rc)
	s.reader.Comma = s.delimiter

	if s.hasHeader {
		headers, err := s.reader.Read()
		if err != nil {
			if err == io.EOF {
				s.finished = true
				return nil
			}
			return fmt.Errorf("failed to read csv header: %w", err)
		}
		s.headers = headers
	}

	return nil
}

func (s *CSVSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.finished {
		// To avoid busy loop, we wait until context is cancelled or just return nil
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			return nil, nil
		}
	}

	if s.reader == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		if s.finished {
			return nil, nil
		}
	}

	record, err := s.reader.Read()
	if err == io.EOF {
		s.finished = true
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read csv record: %w", err)
	}

	data := make(map[string]any)
	if len(s.headers) > 0 {
		for i, val := range record {
			if i < len(s.headers) {
				data[s.headers[i]] = val
			} else {
				data[fmt.Sprintf("column_%d", i)] = val
			}
		}
	} else {
		for i, val := range record {
			data[fmt.Sprintf("column_%d", i)] = val
		}
	}

	msg := message.AcquireMessage()
	idBase := s.filePath
	if s.sourceType == SourceTypeHTTP {
		idBase = s.url
	} else if s.sourceType == SourceTypeS3 {
		idBase = fmt.Sprintf("s3://%s/%s", s.s3Bucket, s.s3Key)
	}
	msg.SetID(fmt.Sprintf("%s-%d", filepath.Base(idBase), time.Now().UnixNano()))
	for k, v := range data {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "csv")
	msg.SetMetadata("source_type", string(s.sourceType))
	switch s.sourceType {
	case SourceTypeLocal:
		msg.SetMetadata("file_path", s.filePath)
	case SourceTypeHTTP:
		msg.SetMetadata("url", s.url)
	case SourceTypeS3:
		msg.SetMetadata("bucket", s.s3Bucket)
		msg.SetMetadata("key", s.s3Key)
	}

	return msg, nil
}

func (s *CSVSource) Ack(ctx context.Context, msg hermod.Message) error {
	// CSV source doesn't support acknowledgement in this simple implementation
	return nil
}

func (s *CSVSource) Ping(ctx context.Context) error {
	switch s.sourceType {
	case SourceTypeLocal:
		_, err := os.Stat(s.filePath)
		if err != nil {
			return fmt.Errorf("csv file not found or inaccessible: %w", err)
		}
	case SourceTypeHTTP:
		req, err := http.NewRequestWithContext(ctx, "HEAD", s.url, nil)
		if err != nil {
			return err
		}
		for k, v := range s.headersMap {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("http head request failed: status %d", resp.StatusCode)
		}
	case SourceTypeS3:
		cfg, err := config.LoadDefaultConfig(ctx,
			config.WithRegion(s.s3Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.s3AccessKey, s.s3SecretKey, "")),
		)
		if err != nil {
			return fmt.Errorf("failed to load aws config: %w", err)
		}

		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			if s.s3Endpoint != "" {
				o.BaseEndpoint = aws.String(s.s3Endpoint)
				o.UsePathStyle = true
			}
		})

		_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.s3Key),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *CSVSource) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

func (s *CSVSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"default"}, nil
}

func (s *CSVSource) DiscoverTables(ctx context.Context) ([]string, error) {
	name := "csv_data"
	switch s.sourceType {
	case SourceTypeLocal:
		name = filepath.Base(s.filePath)
	case SourceTypeHTTP:
		name = filepath.Base(s.url)
	case SourceTypeS3:
		name = filepath.Base(s.s3Key)
	}
	return []string{name}, nil
}

func (s *CSVSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	// For sample, we re-init and read one record
	// This is not very efficient for remote sources but works for a sample

	// Create a copy of the source to avoid messign with current state if already initialized
	var sampleSource *CSVSource
	switch s.sourceType {
	case SourceTypeLocal:
		sampleSource = NewCSVSource(s.filePath, s.delimiter, s.hasHeader)
	case SourceTypeHTTP:
		sampleSource = NewHTTPCSVSource(s.url, s.delimiter, s.hasHeader, s.headersMap)
	case SourceTypeS3:
		sampleSource = NewS3CSVSource(s.s3Region, s.s3Bucket, s.s3Key, s.s3Endpoint, s.s3AccessKey, s.s3SecretKey, s.delimiter, s.hasHeader)
	}

	if err := sampleSource.init(ctx); err != nil {
		return nil, err
	}
	defer sampleSource.Close()

	return sampleSource.Read(ctx)
}
