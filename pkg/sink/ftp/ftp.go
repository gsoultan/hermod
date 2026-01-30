package ftp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	goftp "github.com/jlaffaye/ftp"
	"github.com/user/hermod"
)

// FTPSink implements the hermod.Sink interface for uploading files to an FTP/FTPS server.
type FTPSink struct {
	addr             string
	username         string
	password         string
	useTLS           bool
	timeout          time.Duration
	rootDir          string
	pathTemplate     string
	filenameTemplate string
	writeMode        string // overwrite|append
	mkdirs           bool
	formatter        hermod.Formatter

	// client is created lazily; we keep a single connection and recreate on errors.
	client *goftp.ServerConn
}

// NewFTPSink constructs a new FTPSink.
func NewFTPSink(host string, port int, username, password string, useTLS bool, timeout time.Duration, rootDir, pathTemplate, filenameTemplate, writeMode string, mkdirs bool, formatter hermod.Formatter) (*FTPSink, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	if writeMode == "" {
		writeMode = "overwrite"
	}
	return &FTPSink{
		addr:             addr,
		username:         username,
		password:         password,
		useTLS:           useTLS,
		timeout:          timeout,
		rootDir:          strings.TrimSuffix(rootDir, "/"),
		pathTemplate:     pathTemplate,
		filenameTemplate: filenameTemplate,
		writeMode:        strings.ToLower(writeMode),
		mkdirs:           mkdirs,
		formatter:        formatter,
	}, nil
}

func (s *FTPSink) ensureClient(ctx context.Context) error {
	if s.client != nil {
		return nil
	}
	dialOpts := []goftp.DialOption{goftp.DialWithTimeout(s.timeout)}
	if s.useTLS {
		dialOpts = append(dialOpts, goftp.DialWithTLS(&tls.Config{InsecureSkipVerify: true}))
	}
	c, err := goftp.Dial(s.addr, dialOpts...)
	if err != nil {
		return fmt.Errorf("ftp dial: %w", err)
	}
	err = c.Login(s.username, s.password)
	if err != nil {
		_ = c.Quit()
		return fmt.Errorf("ftp login: %w", err)
	}
	s.client = c
	return nil
}

// render applies text/template on a string with data.
func render(tmpl string, data map[string]interface{}) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New("ftp").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *FTPSink) buildTemplateData(msg hermod.Message) map[string]interface{} {
	data := map[string]interface{}{}
	for k, v := range msg.Data() {
		data[k] = v
	}
	if _, ok := data["id"]; !ok {
		data["id"] = msg.ID()
	}
	if _, ok := data["operation"]; !ok {
		data["operation"] = msg.Operation()
	}
	if _, ok := data["table"]; !ok {
		data["table"] = msg.Table()
	}
	if _, ok := data["schema"]; !ok {
		data["schema"] = msg.Schema()
	}
	if _, ok := data["metadata"]; !ok {
		data["metadata"] = msg.Metadata()
	}
	return data
}

// Write uploads the formatted message to the FTP server.
func (s *FTPSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureClient(ctx); err != nil {
		return err
	}

	// Content
	var content []byte
	var err error
	if s.formatter != nil {
		content, err = s.formatter.Format(msg)
		if err != nil {
			return fmt.Errorf("format message: %w", err)
		}
	} else {
		content = msg.Payload()
	}

	// Paths
	tdata := s.buildTemplateData(msg)
	relPath, err := render(s.pathTemplate, tdata)
	if err != nil {
		return fmt.Errorf("render path template: %w", err)
	}
	filename, err := render(s.filenameTemplate, tdata)
	if err != nil {
		return fmt.Errorf("render filename template: %w", err)
	}
	// normalize and join
	relPath = filepath.ToSlash(strings.Trim(relPath, "/"))
	fullDir := path.Clean(path.Join("/", s.rootDir, relPath))
	if s.mkdirs && fullDir != "/" {
		// create directories recursively
		// Split and MakeDir each step, ignoring already exists errors
		parts := strings.Split(strings.TrimPrefix(fullDir, "/"), "/")
		cur := "/"
		for _, p := range parts {
			if p == "" {
				continue
			}
			cur = path.Join(cur, p)
			// Try to change to the dir; if fail, attempt to create
			if err := s.client.ChangeDir(cur); err != nil {
				// attempt create
				_ = s.client.MakeDir(cur)
			}
		}
	}
	if fullDir != "/" {
		// ensure we are back to root before uploading using path
		_ = s.client.ChangeDir("/")
	}
	fullPath := path.Clean(path.Join(fullDir, filename))

	// Upload
	rdr := bytes.NewReader(content)
	switch s.writeMode {
	case "append":
		if err := s.client.Append(fullPath, rdr); err != nil {
			// Attempt to create if not exists by doing STOR
			if err := s.client.Stor(fullPath, bytes.NewReader(content)); err != nil {
				return fmt.Errorf("ftp append/store %s: %w", fullPath, err)
			}
		}
	default: // overwrite
		if err := s.client.Stor(fullPath, rdr); err != nil {
			return fmt.Errorf("ftp store %s: %w", fullPath, err)
		}
	}
	return nil
}

// Ping verifies the connection by issuing NOOP. Reconnects if needed.
func (s *FTPSink) Ping(ctx context.Context) error {
	if s.client == nil {
		return s.ensureClient(ctx)
	}
	if err := s.client.NoOp(); err != nil {
		_ = s.client.Quit()
		s.client = nil
		return s.ensureClient(ctx)
	}
	return nil
}

// Close closes the FTP connection.
func (s *FTPSink) Close() error {
	if s.client != nil {
		err := s.client.Quit()
		s.client = nil
		return err
	}
	return nil
}
