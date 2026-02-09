package file

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	goftp "github.com/jlaffaye/ftp"
	sftp "github.com/pkg/sftp"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"golang.org/x/crypto/ssh"
)

// Backend enumerates supported storage backends for files.
type Backend string

const (
	BackendLocal Backend = "local"
	BackendHTTP  Backend = "http"
	BackendS3    Backend = "s3" // handled by CSV-specific path or raw via presigned URL/HTTP
	BackendFTP   Backend = "ftp"
	BackendSFTP  Backend = "sftp"
)

// Format enumerates parsing/ingestion modes.
type Format string

const (
	FormatRaw Format = "raw" // emit one message per file, payload contains file bytes
	FormatCSV Format = "csv" // emit one message per CSV record (like legacy CSVSource)
)

// GenericConfig contains configuration for the generic file source.
type GenericConfig struct {
	Backend Backend
	// Common
	Pattern      string // glob like *.csv (applies to local/ftp; s3 handled via prefix + filter)
	Recursive    bool
	PollInterval time.Duration // how often to rescan when queue is empty (0 = one-shot)
	Format       Format        // raw or csv

	// Local
	LocalPath string // directory or file path

	// HTTP (single file)
	URL     string
	Headers map[string]string

	// FTP
	FTPAddr    string // host:port
	FTPUser    string
	FTPPass    string
	FTPRootDir string // list from here
	FTPTLS     bool   // reserved; not used for now (plain FTP)

	// S3
	S3Region    string
	S3Bucket    string
	S3Prefix    string
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
}

// fileRef represents a single file to process.
type fileRef struct {
	Name     string
	FullPath string
	Size     int64
	ModTime  time.Time
	Backend  Backend
	Extra    map[string]string
}

// GenericFileSource implements a polling file reader producing messages per file or per row (CSV).
type GenericFileSource struct {
	cfg    GenericConfig
	logger hermod.Logger

	mu          sync.Mutex
	queue       []fileRef
	initScanned bool
	lastMTime   time.Time // watermark by modification time

	// Active reader for CSV per-file iteration
	activeFile *fileRef
	csvReader  *CSVSource // reuse existing csv reader for per-row mode
}

func NewGenericFileSource(cfg GenericConfig) *GenericFileSource {
	return &GenericFileSource{cfg: cfg}
}

func (s *GenericFileSource) SetLogger(l hermod.Logger) { s.logger = l }

// GetState/SetState implement hermod.Stateful to persist watermark.
func (s *GenericFileSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := map[string]string{}
	if !s.lastMTime.IsZero() {
		st["last_mtime_unix"] = fmt.Sprintf("%d", s.lastMTime.Unix())
	}
	return st
}

func (s *GenericFileSource) SetState(state map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts, ok := state["last_mtime_unix"]; ok && ts != "" {
		var sec int64
		_, _ = fmt.Sscanf(ts, "%d", &sec)
		if sec > 0 {
			s.lastMTime = time.Unix(sec, 0)
		}
	}
}

func (s *GenericFileSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	backend := s.cfg.Backend
	s.mu.Unlock()
	switch backend {
	case BackendLocal:
		if s.cfg.LocalPath == "" {
			return errors.New("local_path is required")
		}
		_, err := os.Stat(s.cfg.LocalPath)
		return err
	case BackendHTTP:
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.cfg.URL, nil)
		if err != nil {
			return err
		}
		for k, v := range s.cfg.Headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("http status %d", resp.StatusCode)
		}
		return nil
	case BackendFTP:
		c, err := goftp.Dial(s.cfg.FTPAddr, goftp.DialWithTimeout(5*time.Second))
		if err != nil {
			return err
		}
		defer c.Quit()
		if s.cfg.FTPUser != "" || s.cfg.FTPPass != "" {
			if err := c.Login(s.cfg.FTPUser, s.cfg.FTPPass); err != nil {
				return err
			}
		}
		_, err = c.List(s.cfg.FTPRootDir)
		return err
	case BackendSFTP:
		addr := s.cfg.FTPAddr
		if addr == "" {
			// build from host/port in config if provided
			host := os.Getenv("SFTP_HOST")
			port := os.Getenv("SFTP_PORT")
			if host != "" && port != "" {
				addr = fmt.Sprintf("%s:%s", host, port)
			} else {
				return errors.New("sftp address is required (ftp_host:ftp_port or ftp_addr)")
			}
		}
		auths := []ssh.AuthMethod{}
		if s.cfg.FTPPass != "" {
			auths = append(auths, ssh.Password(s.cfg.FTPPass))
		}
		if s.cfg.FTPUser == "" {
			return errors.New("sftp username is required")
		}
		cfg := &ssh.ClientConfig{
			User:            s.cfg.FTPUser,
			Auth:            auths,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}
		conn, err := ssh.Dial("tcp", addr, cfg)
		if err != nil {
			return err
		}
		defer conn.Close()
		cli, err := sftp.NewClient(conn)
		if err != nil {
			return err
		}
		defer cli.Close()
		root := s.cfg.FTPRootDir
		if root == "" {
			root = "/"
		}
		_, err = cli.ReadDir(root)
		return err
	default:
		return nil
	}
}

func (s *GenericFileSource) Close() error { return nil }

func (s *GenericFileSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

// Read implements hermod.Source. It returns a message when available.
func (s *GenericFileSource) Read(ctx context.Context) (hermod.Message, error) {
	// If we are in CSV per-row mode and have an active reader, drain it first
	if s.cfg.Format == FormatCSV && s.csvReader != nil {
		msg, err := s.csvReader.Read(ctx)
		if err != nil {
			return nil, err
		}
		if msg != nil {
			return msg, nil
		}
		// finished current file
		_ = s.csvReader.Close()
		s.csvReader = nil
		s.activeFile = nil
	}

	for {
		// Ensure queue is populated
		if err := s.ensureQueue(ctx); err != nil {
			return nil, err
		}
		ref := s.pop()
		if ref == nil {
			// no items
			if s.cfg.PollInterval <= 0 {
				// one-shot
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(1 * time.Second):
					return nil, nil
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(s.cfg.PollInterval):
				continue
			}
		}

		// Process item
		switch s.cfg.Format {
		case FormatCSV:
			// Build a CSVSource bound to this file
			csvSrc := s.csvReaderFor(ref)
			s.csvReader = csvSrc
			s.activeFile = ref
			return s.csvReader.Read(ctx)
		default: // raw
			b, meta, err := s.readFileBytes(ctx, ref)
			if err != nil {
				return nil, err
			}
			msg := message.AcquireMessage()
			msg.SetID(fmt.Sprintf("%s-%d", filepath.Base(ref.Name), time.Now().UnixNano()))
			msg.SetMetadata("source", "file")
			msg.SetMetadata("backend", string(ref.Backend))
			for k, v := range meta {
				msg.SetMetadata(k, v)
			}
			if b != nil { // payload
				// Write whole payload
				for k, v := range map[string]interface{}{"file_name": filepath.Base(ref.Name), "file_size": ref.Size, "mod_time": ref.ModTime.Unix()} {
					msg.SetData(k, v)
				}
				// Attach payload
				msg.SetMetadata("content_type", meta["content_type"])
				// Store raw bytes as payload
				msg.SetAfter(b)
			}
			return msg, nil
		}
	}
}

func (s *GenericFileSource) csvReaderFor(ref *fileRef) *CSVSource {
	// Determine how to open CSV depending on backend
	delim := ','
	hasHeader := true
	switch ref.Backend {
	case BackendLocal:
		return NewCSVSource(ref.FullPath, delim, hasHeader)
	case BackendHTTP:
		return NewHTTPCSVSource(ref.FullPath, delim, hasHeader, s.cfg.Headers)
	case BackendSFTP:
		if rc, err := s.fetchSFTPFileReader(ref); err == nil && rc != nil {
			return NewCSVSourceFromReadCloser(rc, delim, hasHeader)
		}
		return NewCSVSource(ref.FullPath, delim, hasHeader)
	default:
		return NewCSVSource(ref.FullPath, delim, hasHeader)
	}
}

// ensureQueue scans backend and fills s.queue with files newer than watermark.
func (s *GenericFileSource) ensureQueue(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initScanned && len(s.queue) > 0 {
		return nil
	}
	if !s.initScanned || len(s.queue) == 0 {
		files, err := s.listFiles(ctx)
		if err != nil {
			return err
		}
		// Filter by mtime strictly greater than watermark
		var items []fileRef
		for _, f := range files {
			if f.ModTime.After(s.lastMTime) {
				items = append(items, f)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ModTime.Before(items[j].ModTime) })
		s.queue = items
		s.initScanned = true
	}
	return nil
}

func (s *GenericFileSource) pop() *fileRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	ref := s.queue[0]
	s.queue = s.queue[1:]
	// advance watermark
	if ref.ModTime.After(s.lastMTime) {
		s.lastMTime = ref.ModTime
	}
	return &ref
}

func (s *GenericFileSource) listFiles(ctx context.Context) ([]fileRef, error) {
	switch s.cfg.Backend {
	case BackendLocal:
		return s.listLocal()
	case BackendHTTP:
		// Treat as single file
		return []fileRef{{
			Name:     filepath.Base(s.cfg.URL),
			FullPath: s.cfg.URL,
			Size:     -1,
			ModTime:  time.Now(),
			Backend:  BackendHTTP,
			Extra:    map[string]string{"url": s.cfg.URL},
		}}, nil
	case BackendFTP:
		return s.listFTP(ctx)
	case BackendSFTP:
		return s.listSFTP(ctx)
	case BackendS3:
		return s.listS3(ctx)
	default:
		return nil, fmt.Errorf("unsupported backend: %s", s.cfg.Backend)
	}
}

func (s *GenericFileSource) listLocal() ([]fileRef, error) {
	path := s.cfg.LocalPath
	if path == "" {
		return nil, errors.New("local_path is required")
	}
	var refs []fileRef
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	matcher := func(name string) bool {
		if s.cfg.Pattern == "" {
			return true
		}
		ok, _ := filepath.Match(s.cfg.Pattern, filepath.Base(name))
		return ok
	}
	if !info.IsDir() {
		if matcher(path) {
			refs = append(refs, fileRef{Name: filepath.Base(path), FullPath: path, Size: info.Size(), ModTime: info.ModTime(), Backend: BackendLocal})
		}
		return refs, nil
	}
	if s.cfg.Recursive {
		filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !matcher(p) {
				return nil
			}
			fi, e := os.Stat(p)
			if e != nil {
				return nil
			}
			refs = append(refs, fileRef{Name: filepath.Base(p), FullPath: p, Size: fi.Size(), ModTime: fi.ModTime(), Backend: BackendLocal})
			return nil
		})
	} else {
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(path, e.Name())
			if !matcher(p) {
				continue
			}
			fi, _ := os.Stat(p)
			refs = append(refs, fileRef{Name: e.Name(), FullPath: p, Size: fi.Size(), ModTime: fi.ModTime(), Backend: BackendLocal})
		}
	}
	return refs, nil
}

func (s *GenericFileSource) listS3(ctx context.Context) ([]fileRef, error) {
	awsCfg := &aws.Config{Region: aws.String(s.cfg.S3Region)}
	if s.cfg.S3Endpoint != "" {
		awsCfg.Endpoint = aws.String(s.cfg.S3Endpoint)
		awsCfg.S3ForcePathStyle = aws.Bool(true)
	}
	if s.cfg.S3AccessKey != "" && s.cfg.S3SecretKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentials(s.cfg.S3AccessKey, s.cfg.S3SecretKey, "")
	}
	sess, err := session.NewSession(awsCfg)
	if err != nil {
		return nil, err
	}
	svc := awss3.New(sess)
	var refs []fileRef
	var token *string
	for {
		out, err := svc.ListObjectsV2WithContext(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String(s.cfg.S3Bucket), Prefix: aws.String(s.cfg.S3Prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			key := *obj.Key
			if s.cfg.Pattern != "" {
				ok, _ := filepath.Match(s.cfg.Pattern, filepath.Base(key))
				if !ok {
					continue
				}
			}
			mod := time.Now()
			if obj.LastModified != nil {
				mod = *obj.LastModified
			}
			size := int64(0)
			if obj.Size != nil {
				size = *obj.Size
			}
			refs = append(refs, fileRef{Name: key, FullPath: fmt.Sprintf("s3://%s/%s", s.cfg.S3Bucket, key), Size: size, ModTime: mod, Backend: BackendS3})
		}
		if out.IsTruncated != nil && *out.IsTruncated {
			token = out.NextContinuationToken
		} else {
			break
		}
	}
	return refs, nil
}

func (s *GenericFileSource) listFTP(ctx context.Context) ([]fileRef, error) {
	addr := s.cfg.FTPAddr
	if addr == "" {
		return nil, errors.New("ftp_addr is required")
	}
	c, err := goftp.Dial(addr, goftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return nil, err
	}
	defer c.Quit()
	if s.cfg.FTPUser != "" || s.cfg.FTPPass != "" {
		if err := c.Login(s.cfg.FTPUser, s.cfg.FTPPass); err != nil {
			return nil, err
		}
	}
	root := s.cfg.FTPRootDir
	if root == "" {
		root = "/"
	}
	entries, err := c.List(root)
	if err != nil {
		return nil, err
	}
	var refs []fileRef
	for _, e := range entries {
		if e.Type == goftp.EntryTypeFolder {
			continue
		}
		name := e.Name
		if s.cfg.Pattern != "" {
			ok, _ := filepath.Match(s.cfg.Pattern, filepath.Base(name))
			if !ok {
				continue
			}
		}
		mod := e.Time
		refs = append(refs, fileRef{Name: name, FullPath: filepath.Join(root, name), Size: int64(e.Size), ModTime: mod, Backend: BackendFTP})
	}
	return refs, nil
}

func (s *GenericFileSource) readFileBytes(ctx context.Context, ref *fileRef) ([]byte, map[string]string, error) {
	meta := map[string]string{"file_name": ref.Name}
	var r io.ReadCloser
	var err error
	switch ref.Backend {
	case BackendLocal:
		r, err = os.Open(ref.FullPath)
	case BackendHTTP:
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, ref.FullPath, nil)
		if e != nil {
			return nil, nil, e
		}
		for k, v := range s.cfg.Headers {
			req.Header.Set(k, v)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			return nil, nil, e
		}
		if resp.StatusCode >= 400 {
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf("http status %d", resp.StatusCode)
		}
		r = resp.Body
	case BackendFTP:
		r, err = s.fetchFTPFileReader(ref)
	case BackendSFTP:
		r, err = s.fetchSFTPFileReader(ref)
	case BackendS3:
		awsCfg := &aws.Config{Region: aws.String(s.cfg.S3Region)}
		if s.cfg.S3Endpoint != "" {
			awsCfg.Endpoint = aws.String(s.cfg.S3Endpoint)
			awsCfg.S3ForcePathStyle = aws.Bool(true)
		}
		if s.cfg.S3AccessKey != "" && s.cfg.S3SecretKey != "" {
			awsCfg.Credentials = credentials.NewStaticCredentials(s.cfg.S3AccessKey, s.cfg.S3SecretKey, "")
		}
		sess2, err2 := session.NewSession(awsCfg)
		if err2 != nil {
			return nil, nil, err2
		}
		svc := awss3.New(sess2)
		key := strings.TrimPrefix(ref.Name, "/")
		out, err2 := svc.GetObjectWithContext(ctx, &awss3.GetObjectInput{Bucket: aws.String(s.cfg.S3Bucket), Key: aws.String(key)})
		if err2 != nil {
			return nil, nil, err2
		}
		r = out.Body
	default:
		return nil, nil, fmt.Errorf("unsupported backend: %s", ref.Backend)
	}
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	meta["content_type"] = detectContentType(ref.Name, b)
	return b, meta, nil
}

func (s *GenericFileSource) fetchFTPFileReader(ref *fileRef) (io.ReadCloser, error) {
	c, err := goftp.Dial(s.cfg.FTPAddr, goftp.DialWithTimeout(15*time.Second))
	if err != nil {
		return nil, err
	}
	if s.cfg.FTPUser != "" || s.cfg.FTPPass != "" {
		if err := c.Login(s.cfg.FTPUser, s.cfg.FTPPass); err != nil {
			c.Quit()
			return nil, err
		}
	}
	rc, err := c.Retr(ref.FullPath)
	if err != nil {
		c.Quit()
		return nil, err
	}
	return &ftpReadCloser{ReadCloser: rc, c: c}, nil
}

type ftpReadCloser struct {
	io.ReadCloser
	c *goftp.ServerConn
}

func (f *ftpReadCloser) Close() error { _ = f.ReadCloser.Close(); return f.c.Quit() }

// SFTP listing and fetch helpers
func (s *GenericFileSource) listSFTP(ctx context.Context) ([]fileRef, error) {
	addr := s.cfg.FTPAddr
	if addr == "" {
		host := os.Getenv("SFTP_HOST")
		port := os.Getenv("SFTP_PORT")
		if host != "" && port != "" {
			addr = fmt.Sprintf("%s:%s", host, port)
		} else {
			return nil, errors.New("sftp address is required (ftp_host:ftp_port or ftp_addr)")
		}
	}
	auths := []ssh.AuthMethod{}
	if s.cfg.FTPPass != "" {
		auths = append(auths, ssh.Password(s.cfg.FTPPass))
	}
	if s.cfg.FTPUser == "" {
		return nil, errors.New("sftp username is required")
	}
	cfg := &ssh.ClientConfig{
		User:            s.cfg.FTPUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cli, err := sftp.NewClient(conn)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	root := s.cfg.FTPRootDir
	if root == "" {
		root = "/"
	}
	entries, err := cli.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var refs []fileRef
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if s.cfg.Pattern != "" {
			ok, _ := filepath.Match(s.cfg.Pattern, filepath.Base(name))
			if !ok {
				continue
			}
		}
		mod := e.ModTime()
		refs = append(refs, fileRef{Name: name, FullPath: filepath.Join(root, name), Size: e.Size(), ModTime: mod, Backend: BackendSFTP})
	}
	return refs, nil
}

func (s *GenericFileSource) fetchSFTPFileReader(ref *fileRef) (io.ReadCloser, error) {
	addr := s.cfg.FTPAddr
	if addr == "" {
		host := os.Getenv("SFTP_HOST")
		port := os.Getenv("SFTP_PORT")
		if host != "" && port != "" {
			addr = fmt.Sprintf("%s:%s", host, port)
		} else {
			return nil, errors.New("sftp address is required (ftp_host:ftp_port or ftp_addr)")
		}
	}
	auths := []ssh.AuthMethod{}
	if s.cfg.FTPPass != "" {
		auths = append(auths, ssh.Password(s.cfg.FTPPass))
	}
	if s.cfg.FTPUser == "" {
		return nil, errors.New("sftp username is required")
	}
	cfg := &ssh.ClientConfig{
		User:            s.cfg.FTPUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	cli, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	f, err := cli.Open(ref.FullPath)
	if err != nil {
		cli.Close()
		conn.Close()
		return nil, err
	}
	// wrap closer to ensure sftp and ssh are closed when reader closed
	return &sftpReadCloser{ReadCloser: f, cli: cli, conn: conn}, nil
}

type sftpReadCloser struct {
	io.ReadCloser
	cli  *sftp.Client
	conn *ssh.Client
}

func (s *sftpReadCloser) Close() error {
	_ = s.ReadCloser.Close()
	_ = s.cli.Close()
	return s.conn.Close()
}

func detectContentType(name string, b []byte) string {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); ct != "" {
		return ct
	}
	if len(b) > 512 {
		return http.DetectContentType(b[:512])
	}
	return http.DetectContentType(b)
}

// newCSVSourceFromReader creates a temporary CSVSource backed by an io.ReadCloser stream.
// We emulate the existing CSVSource by writing bytes into memory and serving via http-style path.
func newCSVSourceFromReader(name string, rc io.ReadCloser, delimiter rune, hasHeader bool) *CSVSource {
	// Read all into memory to reuse CSVSource which expects os.File/http/S3 body
	buf, _ := io.ReadAll(rc)
	_ = rc.Close()
	return &CSVSource{
		sourceType: SourceTypeHTTP,
		url:        name,
		delimiter:  delimiter,
		hasHeader:  hasHeader,
		reader:     csvReaderFromBytes(buf, delimiter),
	}
}

// csvReaderFromBytes returns a *csv.Reader over bytes.
func csvReaderFromBytes(b []byte, delimiter rune) *csv.Reader {
	r := bytes.NewReader(b)
	cr := csv.NewReader(r)
	if delimiter == 0 {
		delimiter = ','
	}
	cr.Comma = delimiter
	return cr
}
