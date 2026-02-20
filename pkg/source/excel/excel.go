package excel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/google/uuid"
	xlsx "github.com/tealeg/xlsx"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// Source implements reading rows from .xlsx files located on a local path using a glob pattern.
// It is intentionally simple and production-focused: explicit errors, guard rails, and stateful resume.
// .xls (BIFF) is not supported in v1; users should convert to .xlsx upstream.
type Source struct {
	// Configuration
	BasePath  string // e.g., ./uploads or absolute path
	Pattern   string // e.g., **/*.xlsx (glob relative to BasePath) or a single file name
	Sheet     string // sheet name; if empty, uses first sheet
	HeaderRow int    // 0 for none, otherwise 1-based row index for headers
	StartRow  int    // 1-based data start row (if HeaderRow>0 and StartRow==0, StartRow=HeaderRow+1)
	BatchSize int    // not used for streaming; reserved for future buffering

	// Backend selector: local (default) | http | s3
	SourceType string
	// HTTP
	URL     string
	Headers map[string]string
	// S3
	S3Region    string
	S3Bucket    string
	S3KeyPrefix string // or single key
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string

	// Runtime
	files   []string // local file paths, http(s) URLs, or s3://bucket/key
	fIdx    int
	rowIdx  int // 1-based row index within current file
	headers []string

	// Logging (optional)
	logger hermod.Logger
}

func New(basePath, pattern, sheet string, headerRow, startRow, batchSize int) *Source {
	return &Source{
		BasePath:  basePath,
		Pattern:   pattern,
		Sheet:     sheet,
		HeaderRow: headerRow,
		StartRow:  startRow,
		BatchSize: batchSize,
	}
}

// SetLogger enables structured logging from engine when available.
func (s *Source) SetLogger(l hermod.Logger) { s.logger = l }

func (s *Source) log(level, msg string, kv ...any) {
	if s.logger == nil {
		return
	}
	switch level {
	case "DEBUG":
		s.logger.Debug(msg, kv...)
	case "INFO":
		s.logger.Info(msg, kv...)
	case "WARN":
		s.logger.Warn(msg, kv...)
	case "ERROR":
		s.logger.Error(msg, kv...)
	}
}

func (s *Source) initFiles() error {
	// Determine backend and enumerate files/objects
	switch strings.ToLower(s.SourceType) {
	case "http":
		if s.URL == "" {
			return errors.New("excel source (http): url is required")
		}
		s.files = []string{s.URL}
	case "s3":
		if s.S3Bucket == "" {
			return errors.New("excel source (s3): bucket is required")
		}
		// List objects under prefix; allow direct key as well
		refs, err := s.listS3()
		if err != nil {
			return err
		}
		s.files = refs
	default:
		if s.BasePath == "" {
			s.BasePath = "."
		}
		patt := s.Pattern
		if patt == "" {
			patt = "*.xlsx"
		}
		// Consider pattern relative to base
		matches, err := filepath.Glob(filepath.Join(s.BasePath, patt))
		if err != nil {
			return fmt.Errorf("excel source: glob failed: %w", err)
		}
		s.files = matches
	}
	s.fIdx = 0
	s.rowIdx = 0
	s.headers = nil
	if len(s.files) == 0 {
		s.log("INFO", "excel: no files matched", "base", s.BasePath, "pattern", s.Pattern, "source_type", s.SourceType)
	}
	return nil
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	if len(s.files) == 0 {
		if err := s.initFiles(); err != nil {
			return nil, err
		}
		if len(s.files) == 0 {
			// Polling style: sleep a bit to avoid tight loop
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
				return nil, nil
			}
		}
	}

	// If current file exhausted, advance
	for s.fIdx < len(s.files) {
		filePath := s.files[s.fIdx]
		msg, done, err := s.readNextRowFromFile(ctx, filePath)
		if err != nil {
			// Log and skip file to avoid stalling
			s.log("ERROR", "excel: read error, skipping file", "file", filePath, "error", err.Error())
			s.fIdx++
			s.rowIdx = 0
			s.headers = nil
			continue
		}
		if done {
			s.fIdx++
			s.rowIdx = 0
			s.headers = nil
			continue
		}
		if msg != nil {
			return msg, nil
		}
		// If no message and not done, wait
		break
	}

	// End of available files; small pause
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
		return nil, nil
	}
}

func (s *Source) readNextRowFromFile(ctx context.Context, path string) (hermod.Message, bool, error) {
	// Open workbook based on backend; we keep it simple-per-read for reliability
	var wb *xlsx.File
	var err error
	backend := strings.ToLower(s.SourceType)
	if backend == "http" || strings.HasPrefix(strings.ToLower(path), "http") {
		data, err := s.fetchHTTP(ctx, path)
		if err != nil {
			return nil, false, err
		}
		wb, err = xlsx.OpenBinary(data)
		if err != nil {
			return nil, false, fmt.Errorf("open xlsx (http): %w", err)
		}
	} else if backend == "s3" || strings.HasPrefix(strings.ToLower(path), "s3://") {
		data, err := s.fetchS3(ctx, path)
		if err != nil {
			return nil, false, err
		}
		wb, err = xlsx.OpenBinary(data)
		if err != nil {
			return nil, false, fmt.Errorf("open xlsx (s3): %w", err)
		}
	} else {
		// Local filesystem
		wb, err = xlsx.OpenFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("open xlsx: %w", err)
		}
	}

	// Resolve sheet
	var sh *xlsx.Sheet
	if s.Sheet != "" {
		sh, ok := wb.Sheet[s.Sheet]
		if !ok {
			return nil, true, fmt.Errorf("sheet not found: %s", s.Sheet)
		}
		_ = sh
	}
	if sh == nil {
		if len(wb.Sheets) == 0 {
			return nil, true, errors.New("xlsx has no sheets")
		}
		sh = wb.Sheets[0]
	}

	// Iterate rows
	current := 0
	for _, row := range sh.Rows {
		current++
		if s.rowIdx == 0 {
			if s.HeaderRow > 0 && s.StartRow == 0 {
				s.StartRow = s.HeaderRow + 1
			}
			s.rowIdx = 1
		}
		// Extract cells to strings
		cols := make([]string, len(row.Cells))
		for i, c := range row.Cells {
			v := c.String()
			cols[i] = v
		}

		// Header processing
		if s.HeaderRow > 0 && current == s.HeaderRow {
			s.headers = normalizeHeaders(cols)
			continue
		}
		if s.StartRow > 0 && current < s.StartRow {
			continue
		}

		// Build message
		m := message.AcquireMessage()
		m.SetID(uuid.NewString())
		m.SetOperation(hermod.Operation("insert"))
		// table name: basename of file or URL path
		base := path
		if strings.HasPrefix(strings.ToLower(path), "http") {
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				base = path[idx+1:]
			}
		} else if strings.HasPrefix(strings.ToLower(path), "s3://") {
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				base = path[idx+1:]
			}
		} else {
			base = filepath.Base(path)
		}
		m.SetTable(base)
		if len(s.headers) > 0 {
			for i, v := range cols {
				key := s.headers[i]
				if key == "" {
					key = fmt.Sprintf("column_%d", i+1)
				}
				m.SetData(key, v)
			}
		} else {
			for i, v := range cols {
				m.SetData(fmt.Sprintf("column_%d", i+1), v)
			}
		}
		m.SetMetadata("source", "excel")
		m.SetMetadata("file_path", path)
		if s.Sheet != "" {
			m.SetMetadata("sheet", s.Sheet)
		} else {
			m.SetMetadata("sheet", sh.Name)
		}
		m.SetMetadata("row_index", fmt.Sprintf("%d", current))

		s.rowIdx = current + 1
		return m, false, nil
	}

	return nil, true, nil
}

// fetchHTTP downloads the file contents.
func (s *Source) fetchHTTP(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// listS3 returns a list of s3://bucket/key refs
func (s *Source) listS3() ([]string, error) {
	awsCfg := &aws.Config{Region: aws.String(s.S3Region)}
	if s.S3Endpoint != "" {
		awsCfg.Endpoint = aws.String(s.S3Endpoint)
		awsCfg.S3ForcePathStyle = aws.Bool(true)
	}
	if s.S3AccessKey != "" && s.S3SecretKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentials(s.S3AccessKey, s.S3SecretKey, "")
	}
	sess, err := session.NewSession(awsCfg)
	if err != nil {
		return nil, err
	}
	svc := awss3.New(sess)
	// If S3KeyPrefix appears to be a single file (ends with .xlsx), still list but will yield that one
	var refs []string
	prefix := strings.TrimPrefix(s.S3KeyPrefix, "/")
	var token *string
	for {
		out, err := svc.ListObjectsV2(&awss3.ListObjectsV2Input{Bucket: aws.String(s.S3Bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			key := *obj.Key
			if s.Pattern != "" {
				if ok, _ := filepath.Match(s.Pattern, filepath.Base(key)); !ok {
					continue
				}
			}
			refs = append(refs, "s3://"+s.S3Bucket+"/"+key)
		}
		if out.IsTruncated != nil && *out.IsTruncated {
			token = out.NextContinuationToken
		} else {
			break
		}
	}
	// If no objects found but S3KeyPrefix looks like a direct key, add it explicitly
	if len(refs) == 0 && prefix != "" && strings.HasSuffix(strings.ToLower(prefix), ".xlsx") {
		refs = append(refs, "s3://"+s.S3Bucket+"/"+prefix)
	}
	return refs, nil
}

// fetchS3 downloads an S3 object given ref s3://bucket/key using configured creds/endpoint.
func (s *Source) fetchS3(ctx context.Context, ref string) ([]byte, error) {
	bucket := s.S3Bucket
	key := ""
	if strings.HasPrefix(strings.ToLower(ref), "s3://") {
		rem := strings.TrimPrefix(ref, "s3://")
		// rem = bucket/key
		if i := strings.Index(rem, "/"); i > 0 {
			bucket = rem[:i]
			key = strings.TrimPrefix(rem[i+1:], "/")
		}
	}
	if bucket == "" || key == "" {
		// Fallback to config
		bucket = s.S3Bucket
		key = strings.TrimPrefix(s.S3KeyPrefix, "/")
	}
	awsCfg := &aws.Config{Region: aws.String(s.S3Region)}
	if s.S3Endpoint != "" {
		awsCfg.Endpoint = aws.String(s.S3Endpoint)
		awsCfg.S3ForcePathStyle = aws.Bool(true)
	}
	if s.S3AccessKey != "" && s.S3SecretKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentials(s.S3AccessKey, s.S3SecretKey, "")
	}
	sess, err := session.NewSession(awsCfg)
	if err != nil {
		return nil, err
	}
	svc := awss3.New(sess)
	out, err := svc.GetObjectWithContext(ctx, &awss3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, out.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeHeaders(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			c = fmt.Sprintf("column_%d", i+1)
		}
		// Replace spaces and punctuation with underscores, lower-case
		c = strings.ToLower(c)
		replacer := strings.NewReplacer(" ", "_", ".", "_", ",", "_", "/", "_", "\\", "_", "-", "_")
		out[i] = replacer.Replace(c)
	}
	return out
}

func (s *Source) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *Source) Ping(ctx context.Context) error                    { return nil }
func (s *Source) Close() error                                      { return nil }
