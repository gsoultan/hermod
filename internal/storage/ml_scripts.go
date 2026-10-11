package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MLScript is one version of a custom training script a vhost holds: Python
// that trains a model and exports it to ONNX, run by a sandboxed hermod-ml
// worker pool. A script is versioned by the SHA-256 of its source. Saving new
// source under a name adds a version; saving the source the latest version
// already has adds nothing. Versions are never rewritten, so a model version
// can always say exactly which code trained it.
type MLScript struct {
	VHost   string `json:"vhost"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	// Source is empty in listings; GetMLScript fills it.
	Source      string    `json:"source,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MaxMLScriptBytes bounds a script's source; the worker refuses larger ones.
const MaxMLScriptBytes = 256 << 10

// maxMLScriptDescription bounds a script's description.
const maxMLScriptDescription = 1024

// MLScriptSHA256 is the hex SHA-256 of a script's source, its version identity.
func MLScriptSHA256(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// ValidateMLScript reports what is wrong with a script before it is saved.
// Names follow the model name rule: they appear in URLs and in the algorithm
// a training names ("custom:<name>").
func ValidateMLScript(s MLScript) error {
	if s.VHost == "" {
		return errors.New("a script belongs to one vhost: name it")
	}
	if !ValidMLModelName(s.Name) {
		return fmt.Errorf("script name %q must start with a letter and hold only letters, digits, '_' or '-' (at most %d)", s.Name, MaxMLModelNameLen)
	}
	if strings.TrimSpace(s.Source) == "" {
		return errors.New("the script is empty")
	}
	if len(s.Source) > MaxMLScriptBytes {
		return fmt.Errorf("the script is larger than %d KB", MaxMLScriptBytes>>10)
	}
	if len(s.Description) > maxMLScriptDescription {
		return fmt.Errorf("the description is longer than %d bytes", maxMLScriptDescription)
	}
	return nil
}

// MLScriptStore is implemented by a storage backend that can hold custom
// training scripts per vhost. Like MLModelStore it is separate from Storage;
// callers find it with a type assertion.
type MLScriptStore interface {
	// ListMLScripts returns the latest version of each of the vhost's
	// scripts, ordered by name, without their source.
	ListMLScripts(ctx context.Context, vhost string) ([]MLScript, error)
	// ListMLScriptVersions returns every version of one script, newest
	// first, without their source.
	ListMLScriptVersions(ctx context.Context, vhost, name string) ([]MLScript, error)
	// GetMLScript returns the latest version of a script with its source, or
	// ErrNotFound.
	GetMLScript(ctx context.Context, vhost, name string) (MLScript, error)
	// PutMLScript saves s.Source as the script's next version, unless it is
	// the source of the latest version already, and returns the version that
	// holds it. Version, SHA256 and CreatedAt are set here.
	PutMLScript(ctx context.Context, s MLScript) (MLScript, error)
	// DeleteMLScript removes every version of a script, or returns ErrNotFound.
	DeleteMLScript(ctx context.Context, vhost, name string) error
	// DeleteMLScripts removes every script the vhost holds.
	DeleteMLScripts(ctx context.Context, vhost string) error
}
