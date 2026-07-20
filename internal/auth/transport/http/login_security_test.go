package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
)

// lockoutMockStorage always reports the user as not found and accepts audit logs.
type lockoutMockStorage struct {
	storage.Storage
}

func (m *lockoutMockStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}

func (m *lockoutMockStorage) CreateLog(ctx context.Context, log storage.Log) error { return nil }

func (m *lockoutMockStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	return nil
}

func TestLoginLockoutHTTP(t *testing.T) {
	store := &lockoutMockStorage{}
	h := &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}

	doLogin := func() int {
		body := strings.NewReader(`{"username":"alice","password":"wrong"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/login", body)
		req.RemoteAddr = "203.0.113.77:5555"
		w := httptest.NewRecorder()
		h.Login(w, req)
		return w.Code
	}

	// First handlers.MaxLoginAttempts attempts should be rejected as unauthorized.
	for i := 1; i <= handlers.MaxLoginAttempts; i++ {
		if code := doLogin(); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected %d, got %d", i, http.StatusUnauthorized, code)
		}
	}

	// The next attempt must be locked out.
	if code := doLogin(); code != http.StatusTooManyRequests {
		t.Fatalf("expected lockout status %d, got %d", http.StatusTooManyRequests, code)
	}
}

func TestLoginLockoutAfterMaxAttempts(t *testing.T) {
	h := &AuthHandler{Handler: &handlers.Handler{}}
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	key := loginAttemptKey("alice", r)

	// Not locked initially.
	if locked, _ := h.CheckLoginLockout(key); locked {
		t.Fatalf("expected not locked before any failures")
	}

	// Register failures up to the limit; should not be locked until the last one.
	for i := 1; i <= handlers.MaxLoginAttempts; i++ {
		h.RegisterFailedLogin(key)
		locked, _ := h.CheckLoginLockout(key)
		if i < handlers.MaxLoginAttempts && locked {
			t.Fatalf("locked too early at attempt %d", i)
		}
		if i == handlers.MaxLoginAttempts && !locked {
			t.Fatalf("expected lockout after %d attempts", handlers.MaxLoginAttempts)
		}
	}

	// Reset clears the lockout.
	h.ResetLoginAttempts(key)
	if locked, _ := h.CheckLoginLockout(key); locked {
		t.Fatalf("expected not locked after reset")
	}
}

func TestLoginAttemptKeyScoping(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r1.RemoteAddr = "203.0.113.5:1111"
	r2 := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r2.RemoteAddr = "198.51.100.9:2222"

	if loginAttemptKey("bob", r1) == loginAttemptKey("bob", r2) {
		t.Fatalf("expected different keys for different client IPs")
	}
	if loginAttemptKey("BOB", r1) != loginAttemptKey("bob", r1) {
		t.Fatalf("expected username comparison to be case-insensitive")
	}
}

func TestSanitizeDBError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		mustNot   []string
		mustEmpty bool
	}{
		{
			name:    "ipv4 host and port",
			err:     errors.New("dial tcp 192.168.1.50:5432: connect: connection refused"),
			mustNot: []string{"192.168.1.50", "5432"},
		},
		{
			name:    "hostname and port",
			err:     errors.New("dial tcp db.internal.example.com:3306: i/o timeout"),
			mustNot: []string{"db.internal.example.com:3306", "3306"},
		},
		{
			name:    "ipv6 host and port",
			err:     errors.New("dial tcp [2001:db8::1]:5432: connection refused"),
			mustNot: []string{"2001:db8::1", "5432"},
		},
		{
			name:      "nil error",
			err:       nil,
			mustEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handlers.SanitizeDBError(tt.err)
			if tt.mustEmpty {
				if got != "" {
					t.Fatalf("expected empty string, got %q", got)
				}
				return
			}
			for _, s := range tt.mustNot {
				if strings.Contains(got, s) {
					t.Fatalf("sanitized error %q must not contain %q", got, s)
				}
			}
		})
	}
}
