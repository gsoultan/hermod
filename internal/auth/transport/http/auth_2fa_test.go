package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// twoFAMockStorage is a minimal in-memory storage mock for 2FA tests.
type twoFAMockStorage struct {
	storage.Storage
	usersByID       map[string]storage.User
	usersByUsername map[string]storage.User
}

func newTwoFAMockStorage() *twoFAMockStorage {
	return &twoFAMockStorage{
		usersByID:       map[string]storage.User{},
		usersByUsername: map[string]storage.User{},
	}
}

func (m *twoFAMockStorage) upsert(u storage.User) {
	m.usersByID[u.ID] = u
	m.usersByUsername[u.Username] = u
}

func (m *twoFAMockStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	if u, ok := m.usersByUsername[username]; ok {
		return u, nil
	}
	return storage.User{}, storage.ErrNotFound
}

func (m *twoFAMockStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	if u, ok := m.usersByID[id]; ok {
		return u, nil
	}
	return storage.User{}, storage.ErrNotFound
}

func (m *twoFAMockStorage) UpdateUser(ctx context.Context, user storage.User) error {
	m.upsert(user)
	return nil
}

func (m *twoFAMockStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	return nil
}
func (m *twoFAMockStorage) CreateLog(ctx context.Context, log storage.Log) error { return nil }

func TestLogin2FAFlow_SuccessAndFailure(t *testing.T) {
	t.Setenv("HERMOD_JWT_SECRET", "testsecret")

	// Prepare user with 2FA enabled
	secret, err := totp.Generate(totp.GenerateOpts{Issuer: "Hermod", AccountName: "alice@example.com"})
	if err != nil {
		t.Fatalf("failed to generate secret: %v", err)
	}

	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.DefaultCost)
	user := storage.User{
		ID:               "u1",
		Username:         "alice",
		Email:            "alice@example.com",
		Password:         string(pwdHash),
		Role:             storage.RoleEditor,
		VHosts:           []string{"*"},
		TwoFactorEnabled: true,
		TwoFactorSecret:  secret.Secret(),
	}

	ms := newTwoFAMockStorage()
	ms.upsert(user)
	h := &AuthHandler{Handler: &handlers.Handler{Storage: ms, LogStorage: ms}}

	// Step 1: password stage returns pending token
	body1 := bytes.NewBufferString(`{"username":"alice","password":"secret123"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/login", body1)
	w1 := httptest.NewRecorder()
	h.Login(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 from password stage, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 struct {
		TwoFactorRequired bool   `json:"two_factor_required"`
		UserID            string `json:"user_id"`
		PendingToken      string `json:"pending_token"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp1.TwoFactorRequired || resp1.PendingToken == "" || resp1.UserID != user.ID {
		t.Fatalf("unexpected response: %+v", resp1)
	}

	// Step 2a: wrong code should fail
	bodyWrong := bytes.NewBufferString(`{"user_id":"` + user.ID + `","pending_token":"` + resp1.PendingToken + `","code":"000000"}`)
	reqWrong := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/login", bodyWrong)
	wWrong := httptest.NewRecorder()
	h.Login2FA(wWrong, reqWrong)
	if wWrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong OTP, got %d", wWrong.Code)
	}

	// Step 2b: correct code should succeed
	code, err := totp.GenerateCode(user.TwoFactorSecret, time.Now())
	if err != nil {
		t.Fatalf("failed to generate code: %v", err)
	}
	body2 := bytes.NewBufferString(`{"user_id":"` + user.ID + `","pending_token":"` + resp1.PendingToken + `","code":"` + code + `"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/login", body2)
	w2 := httptest.NewRecorder()
	h.Login2FA(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct OTP, got %d: %s", w2.Code, w2.Body.String())
	}
	// Should set session cookie
	if c := w2.Result().Cookies(); len(c) == 0 {
		t.Fatalf("expected session cookie to be set")
	}
}

func TestSetupVerifyDisable2FA(t *testing.T) {
	t.Setenv("HERMOD_JWT_SECRET", "testsecret")
	// user without 2FA enabled yet
	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.DefaultCost)
	user := storage.User{ID: "u2", Username: "bob", Email: "bob@example.com", Password: string(pwdHash)}
	ms := newTwoFAMockStorage()
	ms.upsert(user)
	h := &AuthHandler{Handler: &handlers.Handler{Storage: ms, LogStorage: ms}}

	// Auth context
	ctx := context.WithValue(context.Background(), handlers.UserContextKey, &user)

	// Setup 2FA: should return secret and URL
	reqSetup := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil).WithContext(ctx)
	wSetup := httptest.NewRecorder()
	h.Setup2FA(wSetup, reqSetup)
	if wSetup.Code != http.StatusOK {
		t.Fatalf("setup expected 200, got %d: %s", wSetup.Code, wSetup.Body.String())
	}
	var setupResp struct {
		Secret string `json:"secret"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(wSetup.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("failed to parse setup resp: %v", err)
	}
	if setupResp.Secret == "" || setupResp.URL == "" {
		t.Fatalf("expected non-empty secret and url")
	}

	// Verify 2FA: enable with a valid code
	code, err := totp.GenerateCode(setupResp.Secret, time.Now())
	if err != nil {
		t.Fatalf("failed to generate code: %v", err)
	}
	verifyPayload, _ := json.Marshal(map[string]string{"secret": setupResp.Secret, "code": code})
	reqVerify := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/verify", bytes.NewReader(verifyPayload)).WithContext(ctx)
	reqVerify.Header.Set("Content-Type", "application/json")
	wVerify := httptest.NewRecorder()
	h.Verify2FA(wVerify, reqVerify)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("verify expected 200, got %d: %s", wVerify.Code, wVerify.Body.String())
	}
	// Ensure persisted
	saved, err := ms.GetUser(context.Background(), user.ID)
	if err != nil || !saved.TwoFactorEnabled || saved.TwoFactorSecret == "" {
		t.Fatalf("expected user 2FA enabled and secret saved, got: %+v, err=%v", saved, err)
	}

	// Disable 2FA
	reqDisable := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/disable", nil).WithContext(ctx)
	wDisable := httptest.NewRecorder()
	h.Disable2FA(wDisable, reqDisable)
	if wDisable.Code != http.StatusOK {
		t.Fatalf("disable expected 200, got %d: %s", wDisable.Code, wDisable.Body.String())
	}
	saved2, _ := ms.GetUser(context.Background(), user.ID)
	if saved2.TwoFactorEnabled || saved2.TwoFactorSecret != "" {
		t.Fatalf("expected 2FA disabled and secret cleared, got: %+v", saved2)
	}

	// Cleanup env for other tests
	_ = os.Unsetenv("HERMOD_JWT_SECRET")
}

func TestPendingEnrollmentFlow_DuringLogin(t *testing.T) {
	t.Setenv("HERMOD_JWT_SECRET", "testsecret")

	// Admin toggled 2FA enabled but user has no registered secret yet
	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("pw123"), bcrypt.DefaultCost)
	user := storage.User{
		ID:               "u3",
		Username:         "charlie",
		Email:            "charlie@example.com",
		Password:         string(pwdHash),
		TwoFactorEnabled: true, // enabled without secret
		TwoFactorSecret:  "",
	}
	ms := newTwoFAMockStorage()
	ms.upsert(user)
	h := &AuthHandler{Handler: &handlers.Handler{Storage: ms, LogStorage: ms}}

	// Password step should ask for enrollment (not direct 2FA code)
	req1 := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"charlie","password":"pw123"}`))
	w1 := httptest.NewRecorder()
	h.Login(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 from password stage, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 struct {
		EnrollRequired bool   `json:"two_factor_enroll_required"`
		UserID         string `json:"user_id"`
		PendingToken   string `json:"pending_token"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp1.EnrollRequired || resp1.PendingToken == "" || resp1.UserID != user.ID {
		t.Fatalf("unexpected response: %+v", resp1)
	}

	// Request setup using the pending token
	setupBody, _ := json.Marshal(map[string]string{
		"user_id":       user.ID,
		"pending_token": resp1.PendingToken,
	})
	reqSetup := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup/pending", bytes.NewReader(setupBody))
	reqSetup.Header.Set("Content-Type", "application/json")
	wSetup := httptest.NewRecorder()
	h.Setup2FAPending(wSetup, reqSetup)
	if wSetup.Code != http.StatusOK {
		t.Fatalf("setup/pending expected 200, got %d: %s", wSetup.Code, wSetup.Body.String())
	}
	var setupResp struct {
		Secret string `json:"secret"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(wSetup.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("failed to parse setup pending resp: %v", err)
	}
	if setupResp.Secret == "" || setupResp.URL == "" {
		t.Fatalf("expected non-empty secret and url")
	}

	// Verify using the provided secret and complete login
	code, err := totp.GenerateCode(setupResp.Secret, time.Now())
	if err != nil {
		t.Fatalf("failed to generate code: %v", err)
	}
	verifyBody, _ := json.Marshal(map[string]string{
		"user_id":       user.ID,
		"pending_token": resp1.PendingToken,
		"secret":        setupResp.Secret,
		"code":          code,
	})
	reqVerify := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/verify/pending", bytes.NewReader(verifyBody))
	reqVerify.Header.Set("Content-Type", "application/json")
	wVerify := httptest.NewRecorder()
	h.Verify2FAPending(wVerify, reqVerify)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("verify/pending expected 200, got %d: %s", wVerify.Code, wVerify.Body.String())
	}
	if len(wVerify.Result().Cookies()) == 0 {
		t.Fatalf("expected session cookie to be set after verify/pending")
	}
	saved, _ := ms.GetUser(context.Background(), user.ID)
	if saved.TwoFactorSecret == "" || !saved.TwoFactorEnabled {
		t.Fatalf("expected user to have 2FA secret persisted and enabled, got: %+v", saved)
	}
}

// TestAuthMiddleware_AllowsPreAuth2FAEndpoints ensures the pre-auth 2FA
// endpoints are reachable without a session cookie/bearer. Without this, the
// middleware returns 401 and the UI bounces the user back to /login, breaking
// first-time 2FA enrollment after a correct username/password.
func TestAuthMiddleware_AllowsPreAuth2FAEndpoints(t *testing.T) {
	t.Setenv("HERMOD_JWT_SECRET", "testsecret")

	ms := newTwoFAMockStorage()
	h := &AuthHandler{Handler: &handlers.Handler{Storage: ms, LogStorage: ms}}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := h.AuthMiddleware(next)

	preAuthPaths := []string{
		"/api/auth/2fa/login",
		"/api/auth/2fa/setup/pending",
		"/api/auth/2fa/verify/pending",
	}
	for _, p := range preAuthPaths {
		reached = false
		req := httptest.NewRequest(http.MethodPost, p, nil) // no Authorization header / cookie
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized {
			t.Fatalf("path %s should be public (pending-token authenticated), got 401", p)
		}
		if !reached {
			t.Fatalf("path %s did not reach the wrapped handler", p)
		}
	}
}
