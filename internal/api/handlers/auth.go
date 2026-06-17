package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/smtp"
	"github.com/pquerna/otp/totp"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

func (h *Handler) RegisterAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", h.Login)
	mux.HandleFunc("POST /api/auth/2fa/login", h.Login2FA)
	mux.HandleFunc("POST /api/auth/2fa/setup", h.Setup2FA)
	mux.HandleFunc("POST /api/auth/2fa/verify", h.Verify2FA)
	// Pre-auth (pending-token) enrollment endpoints to allow 2FA registration during login
	mux.HandleFunc("POST /api/auth/2fa/setup/pending", h.Setup2FAPending)
	mux.HandleFunc("POST /api/auth/2fa/verify/pending", h.Verify2FAPending)
	mux.HandleFunc("POST /api/auth/2fa/disable", h.Disable2FA)
	mux.HandleFunc("POST /api/auth/generate-password", h.GeneratePasswordHandler)
	mux.HandleFunc("GET /api/auth/oidc", h.OidcLogin)
	mux.HandleFunc("GET /api/auth/callback", h.OidcCallback)
	mux.HandleFunc("POST /api/forgot-password", h.ForgotPassword)
	mux.HandleFunc("GET /api/me", h.Me)
	mux.HandleFunc("PUT /api/me", h.UpdateMe)
	mux.Handle("GET /api/users", h.AdminOnly(h.ListUsers))
	mux.Handle("GET /api/users/{id}", h.AdminOnly(h.GetUser))
	mux.Handle("POST /api/users", h.AdminOnly(h.CreateUser))
	mux.Handle("PUT /api/users/{id}", h.AdminOnly(h.UpdateUser))
	mux.HandleFunc("PUT /api/users/{id}/password", h.ChangeUserPassword)
	mux.Handle("DELETE /api/users/{id}", h.AdminOnly(h.DeleteUser))
	mux.HandleFunc("GET /api/vhosts", h.ListVHosts)
	mux.HandleFunc("GET /api/vhosts/", h.ListVHosts)
	mux.HandleFunc("GET /api/vhosts/{id}", h.GetVHost)
	mux.Handle("POST /api/vhosts", h.AdminOnly(h.CreateVHost))
	mux.Handle("POST /api/vhosts/", h.AdminOnly(h.CreateVHost))
	mux.Handle("PUT /api/vhosts/{id}", h.AdminOnly(h.UpdateVHost))
	mux.Handle("DELETE /api/vhosts/{id}", h.AdminOnly(h.DeleteVHost))
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Enforce account lockout after too many failed attempts.
	attemptKey := loginAttemptKey(creds.Username, r)
	if locked, retryAfter := h.CheckLoginLockout(attemptKey); locked {
		h.RecordAuditLog(r, "WARN", "Login blocked for "+creds.Username+" (too many failed attempts)", "login", "", "user", "", nil)
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		h.JsonError(w, "too many failed login attempts, please try again later", http.StatusTooManyRequests)
		return
	}

	user, err := h.Storage.GetUserByUsername(r.Context(), creds.Username)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.RegisterFailedLogin(attemptKey)
			h.JsonError(w, "invalid username or password", http.StatusUnauthorized)
		} else {
			// Never expose raw database errors (which can leak host/IP and port).
			h.JsonError(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(creds.Password)); err != nil {
		h.RegisterFailedLogin(attemptKey)
		h.JsonError(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	// Successful credential check: clear any failed-attempt state.
	h.ResetLoginAttempts(attemptKey)

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		h.JsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(dbCfg.JWTSecret) == "" {
		// Refuse to sign tokens with an empty secret (would make them trivially forgeable).
		h.JsonError(w, "server is misconfigured: missing JWT secret", http.StatusInternalServerError)
		return
	}

	if user.TwoFactorEnabled {
		// If 2FA is marked enabled but no secret is stored, the user must enroll now.
		// This can happen if an admin toggled the flag without completing verification.
		if strings.TrimSpace(user.TwoFactorSecret) == "" {
			pendingToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"id":       user.ID,
				"username": user.Username,
				"pending":  true,
				"exp":      time.Now().Add(time.Minute * 5).Unix(),
			})
			pendingTokenString, err := pendingToken.SignedString([]byte(dbCfg.JWTSecret))
			if err != nil {
				h.JsonError(w, "failed to generate pending token", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"two_factor_enroll_required": true,
				"user_id":                    user.ID,
				"pending_token":              pendingTokenString,
			})
			return
		}
		pendingToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":       user.ID,
			"username": user.Username,
			"pending":  true,
			"exp":      time.Now().Add(time.Minute * 5).Unix(),
		})
		pendingTokenString, err := pendingToken.SignedString([]byte(dbCfg.JWTSecret))
		if err != nil {
			h.JsonError(w, "failed to generate pending token", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"two_factor_required": true,
			"user_id":             user.ID,
			"pending_token":       pendingTokenString,
		})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":       user.ID,
		"username": user.Username,
		"role":     string(user.Role),
		"vhosts":   user.VHosts,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	tokenString, err := token.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		h.JsonError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := SameSiteFromEnv()
	cookie := &http.Cookie{
		Name:     "hermod_session",
		Value:    tokenString,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: ss,
		MaxAge:   24 * 60 * 60,
	}
	if ss == http.SameSiteNoneMode {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
	h.RecordAuditLog(r, "INFO", "User "+user.Username+" logged in", "login", user.ID, "user", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}

var (
	oidcProviderCache   = map[string]*oidc.Provider{}
	oidcProviderCacheMu sync.RWMutex
)

// oidcProvider returns a cached OIDC provider for the configured issuer,
// avoiding a discovery round-trip on every login/callback request.
func oidcProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	oidcProviderCacheMu.RLock()
	p, ok := oidcProviderCache[issuer]
	oidcProviderCacheMu.RUnlock()
	if ok {
		return p, nil
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	oidcProviderCacheMu.Lock()
	oidcProviderCache[issuer] = p
	oidcProviderCacheMu.Unlock()
	return p, nil
}

// requestIsHTTPS reports whether the request was served over TLS, honoring a
// trusted X-Forwarded-Proto header set by a reverse proxy.
func requestIsHTTPS(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return r.TLS != nil
}

func (h *Handler) OidcLogin(w http.ResponseWriter, r *http.Request) {
	if h.Config == nil || !h.Config.Auth.OIDC.Enabled {
		h.JsonError(w, "OIDC is not enabled", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	provider, err := oidcProvider(ctx, h.Config.Auth.OIDC.IssuerURL)
	if err != nil {
		h.JsonError(w, "Failed to get provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	scopes := h.Config.Auth.OIDC.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	oauth2Config := oauth2.Config{
		ClientID:     h.Config.Auth.OIDC.ClientID,
		ClientSecret: h.Config.Auth.OIDC.ClientSecret,
		RedirectURL:  h.Config.Auth.OIDC.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	state, _ := h.GenerateRandomPassword(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})

	http.Redirect(w, r, oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func (h *Handler) OidcCallback(w http.ResponseWriter, r *http.Request) {
	if h.Config == nil || !h.Config.Auth.OIDC.Enabled {
		h.JsonError(w, "OIDC is not enabled", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || r.URL.Query().Get("state") != stateCookie.Value {
		h.JsonError(w, "Invalid state", http.StatusBadRequest)
		return
	}

	provider, err := oidcProvider(ctx, h.Config.Auth.OIDC.IssuerURL)
	if err != nil {
		h.JsonError(w, "Failed to get provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	oauth2Config := oauth2.Config{
		ClientID:     h.Config.Auth.OIDC.ClientID,
		ClientSecret: h.Config.Auth.OIDC.ClientSecret,
		RedirectURL:  h.Config.Auth.OIDC.RedirectURL,
		Endpoint:     provider.Endpoint(),
	}

	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		h.JsonError(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		h.JsonError(w, "No id_token", http.StatusInternalServerError)
		return
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: h.Config.Auth.OIDC.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		h.JsonError(w, "Failed to verify ID token", http.StatusInternalServerError)
		return
	}

	var claims struct {
		Email    string `json:"email"`
		Username string `json:"preferred_username"`
		Name     string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		h.JsonError(w, "Failed to parse claims", http.StatusInternalServerError)
		return
	}

	// Find or create user
	var user storage.User
	var uErr error
	user, uErr = h.Storage.GetUserByEmail(ctx, claims.Email)
	if uErr != nil {
		// Auto-provision user
		user = storage.User{
			ID:       uuid.New().String(),
			Username: claims.Username,
			FullName: claims.Name,
			Email:    claims.Email,
			Role:     storage.RoleViewer, // Default role
		}
		if user.Username == "" {
			user.Username = claims.Email
		}
		_ = h.Storage.CreateUser(ctx, user)
	}

	// Generate Hermod JWT and set cookie
	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		h.JsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	claimsMap := jwt.MapClaims{
		"id":       user.ID,
		"username": user.Username,
		"role":     string(user.Role),
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	}
	if len(user.VHosts) > 0 {
		claimsMap["vhosts"] = user.VHosts
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsMap)
	tokenString, err := token.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		h.JsonError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "User "+user.Username+" logged in (OIDC)", "login", user.ID, "user", "", nil)

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := SameSiteFromEnv()
	cookie := &http.Cookie{
		Name:     "hermod_session",
		Value:    tokenString,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: ss,
		MaxAge:   24 * 60 * 60,
	}
	if ss == http.SameSiteNoneMode {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)

	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := h.Storage.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "If the email exists, a new password has been sent."})
			return
		}
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newPass, _ := h.GenerateRandomPassword(12)
	hashedPass, _ := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	user.Password = string(hashedPass)
	_ = h.Storage.UpdateUser(r.Context(), user)

	h.RecordAuditLog(r, "INFO", "Password reset for "+user.Username, "update", user.ID, "user", "", nil)

	val, err := h.Storage.GetSetting(r.Context(), "notification_settings")
	if err != nil || val == "" {
		h.JsonError(w, "SMTP is not configured", http.StatusInternalServerError)
		return
	}

	var ns notification.NotificationSettings
	_ = json.Unmarshal([]byte(val), &ns)

	sender := smtp.NewSender(ns.SMTPHost, ns.SMTPPort, ns.SMTPUser, ns.SMTPPassword, ns.SMTPSSL)
	emailBody := fmt.Sprintf("Your new password is: %s", newPass)
	email := gsmail.Email{
		From:    ns.SMTPFrom,
		To:      []string{user.Email},
		Subject: "Hermod Password Reset",
		Body:    []byte(emailBody),
	}
	_ = sender.Send(r.Context(), email)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "A new password has been sent to your email."})
}

func (h *Handler) GenerateRandomPassword(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, total, err := h.Storage.ListUsers(r.Context(), h.ParseCommonFilter(r))
	if err != nil {
		h.JsonError(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	for i := range users {
		h.SanitizeUser(&users[i])
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  users,
		"total": total,
	})
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	user, err := h.Storage.GetUser(r.Context(), id)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}
	h.SanitizeUser(&user)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	// Allow creating the very first user during initial setup without authentication.
	// If users already exist, only administrators can create new users.
	initialSetup := false
	if h.Storage != nil {
		if _, total, err := h.Storage.ListUsers(r.Context(), storage.CommonFilter{Limit: 1}); err == nil && total == 0 {
			initialSetup = true
		}
	}

	if !initialSetup {
		role, _ := h.GetRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	var user storage.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if user.Password != "" {
		hashed, _ := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		user.Password = string(hashed)
	}

	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	// Ensure the first user is an administrator if not specified
	if initialSetup && user.Role == "" {
		user.Role = storage.RoleAdministrator
	}

	if err := h.Storage.CreateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.SanitizeUser(&user)
	h.RecordAuditLog(r, "INFO", "Created user "+user.Username, "create", user.ID, "user", "", user)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(user)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	var req storage.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), id)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}

	// Merge changes
	if req.Username != "" {
		user.Username = req.Username
	}
	if req.FullName != "" {
		user.FullName = req.FullName
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Role != "" {
		user.Role = req.Role
	}
	if req.VHosts != nil {
		user.VHosts = req.VHosts
	}
	user.TwoFactorEnabled = req.TwoFactorEnabled
	if req.TwoFactorSecret != "" {
		user.TwoFactorSecret = req.TwoFactorSecret
	}

	if req.Password != "" {
		hashed, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		user.Password = string(hashed)
	}

	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.SanitizeUser(&user)
	h.RecordAuditLog(r, "INFO", "Updated user "+user.Username, "update", user.ID, "user", "", user)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *Handler) ChangeUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// RBAC: only admins or the user themselves can change password
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if ok && userCtx.Role != storage.RoleAdministrator && userCtx.ID != id {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	hashed, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	user, err := h.Storage.GetUser(r.Context(), id)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}
	user.Password = string(hashed)

	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update password: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "password updated"})
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	if err := h.Storage.DeleteUser(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete user", http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Deleted user "+id, "delete", id, "user", "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Setup2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Hermod",
		AccountName: user.Email,
	})
	if err != nil {
		h.JsonError(w, "Failed to generate 2FA key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
}

func (h *Handler) Verify2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	valid := totp.Validate(req.Code, req.Secret)
	if !valid {
		h.JsonError(w, "Invalid verification code", http.StatusBadRequest)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.TwoFactorEnabled = true
	user.TwoFactorSecret = req.Secret
	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update user", http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Enabled 2FA for "+user.Username, "update", user.ID, "user", "", nil)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "2FA enabled"})
}

// Setup2FAPending starts 2FA enrollment using a pending login token (no session required).
// Request body: { "user_id": "...", "pending_token": "..." }
// Response: { "secret": "...", "url": "otpauth://..." }
func (h *Handler) Setup2FAPending(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string `json:"user_id"`
		PendingToken string `json:"pending_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		h.JsonError(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Verify pending token
	token, err := jwt.Parse(req.PendingToken, func(token *jwt.Token) (any, error) {
		return []byte(dbCfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		h.JsonError(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["id"] != req.UserID || claims["pending"] != true {
		h.JsonError(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), req.UserID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusUnauthorized)
		return
	}

	// Only meaningful if 2FA is required but not yet registered.
	if !user.TwoFactorEnabled || strings.TrimSpace(user.TwoFactorSecret) != "" {
		h.JsonError(w, "2FA enrollment not required", http.StatusBadRequest)
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Hermod", AccountName: user.Email})
	if err != nil {
		h.JsonError(w, "Failed to generate 2FA key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
}

// Verify2FAPending finalizes 2FA enrollment using a pending token and immediately completes login.
// Request body: { "user_id": "...", "pending_token": "...", "secret": "...", "code": "123456" }
// Response: { "token": "..." } (also sets session cookie)
func (h *Handler) Verify2FAPending(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string `json:"user_id"`
		PendingToken string `json:"pending_token"`
		Secret       string `json:"secret"`
		Code         string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Secret) == "" || strings.TrimSpace(req.Code) == "" {
		h.JsonError(w, "secret and code are required", http.StatusBadRequest)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		h.JsonError(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Verify pending token
	token, err := jwt.Parse(req.PendingToken, func(token *jwt.Token) (any, error) {
		return []byte(dbCfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		h.JsonError(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["id"] != req.UserID || claims["pending"] != true {
		h.JsonError(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), req.UserID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusUnauthorized)
		return
	}

	// Must be in enrollment-required state
	if !user.TwoFactorEnabled || strings.TrimSpace(user.TwoFactorSecret) != "" {
		h.JsonError(w, "2FA enrollment not required", http.StatusBadRequest)
		return
	}

	// Validate code against provided secret
	if !totp.Validate(req.Code, req.Secret) {
		h.JsonError(w, "Invalid verification code", http.StatusUnauthorized)
		return
	}

	// Persist secret and enable 2FA
	user.TwoFactorSecret = req.Secret
	// TwoFactorEnabled is already true; keep as-is.
	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update user", http.StatusInternalServerError)
		return
	}

	// Issue final JWT and set cookie (completes login)
	finalToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":       user.ID,
		"username": user.Username,
		"role":     string(user.Role),
		"vhosts":   user.VHosts,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := finalToken.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		h.JsonError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}
	ss := SameSiteFromEnv()
	cookie := &http.Cookie{
		Name:     "hermod_session",
		Value:    tokenString,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: ss,
		MaxAge:   24 * 60 * 60,
	}
	if ss == http.SameSiteNoneMode {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
	h.RecordAuditLog(r, "INFO", "Enabled 2FA (during login) for "+user.Username, "update", user.ID, "user", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}

func (h *Handler) Disable2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.TwoFactorEnabled = false
	user.TwoFactorSecret = ""
	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update user", http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Disabled 2FA for "+user.Username, "update", user.ID, "user", "", nil)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "2FA disabled"})
}

func (h *Handler) Login2FA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string `json:"user_id"`
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		h.JsonError(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Verify pending token
	token, err := jwt.Parse(req.PendingToken, func(token *jwt.Token) (any, error) {
		return []byte(dbCfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		h.JsonError(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["id"] != req.UserID || claims["pending"] != true {
		h.JsonError(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), req.UserID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusUnauthorized)
		return
	}

	if !user.TwoFactorEnabled {
		h.JsonError(w, "2FA is not enabled for this user", http.StatusBadRequest)
		return
	}

	valid := totp.Validate(req.Code, user.TwoFactorSecret)
	if !valid {
		h.JsonError(w, "Invalid 2FA code", http.StatusUnauthorized)
		return
	}

	// Issue final JWT
	finalToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":       user.ID,
		"username": user.Username,
		"role":     string(user.Role),
		"vhosts":   user.VHosts,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	tokenString, err := finalToken.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		h.JsonError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := SameSiteFromEnv()
	cookie := &http.Cookie{
		Name:     "hermod_session",
		Value:    tokenString,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: ss,
		MaxAge:   24 * 60 * 60,
	}
	if ss == http.SameSiteNoneMode {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
	h.RecordAuditLog(r, "INFO", "User "+user.Username+" logged in (2FA)", "login", user.ID, "user", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}

func (h *Handler) GeneratePasswordHandler(w http.ResponseWriter, r *http.Request) {
	// Requires authentication OR initial setup
	if !h.IsFirstRun(r.Context()) {
		_, ok := r.Context().Value(UserContextKey).(*storage.User)
		if !ok {
			h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	password, err := h.GenerateRandomPassword(16)
	if err != nil {
		h.JsonError(w, "Failed to generate password", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"password": password})
}

func (h *Handler) SanitizeUser(u *storage.User) {
	if u == nil {
		return
	}
	u.Password = ""
	u.TwoFactorSecret = ""
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}
	h.SanitizeUser(&user)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(UserContextKey).(*storage.User)
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := h.Storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		h.JsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.FullName = req.FullName
	user.Email = req.Email

	if err := h.Storage.UpdateUser(r.Context(), user); err != nil {
		h.JsonError(w, "Failed to update profile: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.SanitizeUser(&user)
	h.RecordAuditLog(r, "INFO", "Updated profile for "+user.Username, "update", user.ID, "user", "", user)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (h *Handler) ListVHosts(w http.ResponseWriter, r *http.Request) {
	role, vhosts := h.GetRoleAndVHosts(r)

	allVHosts, total, err := h.Storage.ListVHosts(r.Context(), storage.CommonFilter{})
	if err != nil {
		h.JsonError(w, "Failed to list vhosts", http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.VHost{}
		for _, vh := range allVHosts {
			if h.HasVHostAccess(vh.Name, vhosts) {
				filtered = append(filtered, vh)
			}
		}
		allVHosts = filtered
		total = len(allVHosts)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  allVHosts,
		"total": total,
	})
}

func (h *Handler) GetVHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vh, err := h.Storage.GetVHost(r.Context(), id)
	if err != nil {
		h.JsonError(w, "VHost not found", http.StatusNotFound)
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(vh.Name, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(vh)
}

func (h *Handler) CreateVHost(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var vh storage.VHost
	if err := json.NewDecoder(r.Body).Decode(&vh); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if vh.ID == "" {
		vh.ID = uuid.New().String()
	}

	if err := h.Storage.CreateVHost(r.Context(), vh); err != nil {
		h.JsonError(w, "Failed to create vhost: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Created vhost "+vh.Name, "create", vh.ID, "vhost", "", vh)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(vh)
}

func (h *Handler) UpdateVHost(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	var vh storage.VHost
	if err := json.NewDecoder(r.Body).Decode(&vh); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	vh.ID = id

	if err := h.Storage.UpdateVHost(r.Context(), vh); err != nil {
		h.JsonError(w, "Failed to update vhost: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Updated vhost "+vh.Name, "update", vh.ID, "vhost", "", vh)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(vh)
}

func (h *Handler) DeleteVHost(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	if err := h.Storage.DeleteVHost(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete vhost", http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Deleted vhost "+id, "delete", id, "vhost", "", nil)
	w.WriteHeader(http.StatusNoContent)
}
