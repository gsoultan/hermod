package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/auth/2fa/login", s.login2FA)
	mux.HandleFunc("POST /api/auth/2fa/setup", s.setup2FA)
	mux.HandleFunc("POST /api/auth/2fa/verify", s.verify2FA)
	mux.HandleFunc("POST /api/auth/2fa/disable", s.disable2FA)
	mux.HandleFunc("POST /api/auth/generate-password", s.generatePasswordHandler)
	mux.HandleFunc("GET /api/auth/oidc", s.oidcLogin)
	mux.HandleFunc("GET /api/auth/callback", s.oidcCallback)
	mux.HandleFunc("POST /api/forgot-password", s.forgotPassword)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PUT /api/me", s.updateMe)
	mux.Handle("GET /api/users", s.adminOnly(s.listUsers))
	mux.Handle("GET /api/users/{id}", s.adminOnly(s.getUser))
	mux.Handle("POST /api/users", s.adminOnly(s.createUser))
	mux.Handle("PUT /api/users/{id}", s.adminOnly(s.updateUser))
	mux.HandleFunc("PUT /api/users/{id}/password", s.changeUserPassword)
	mux.Handle("DELETE /api/users/{id}", s.adminOnly(s.deleteUser))
	mux.HandleFunc("GET /api/vhosts", s.listVHosts)
	mux.HandleFunc("GET /api/vhosts/{id}", s.getVHost)
	mux.Handle("POST /api/vhosts", s.adminOnly(s.createVHost))
	mux.Handle("DELETE /api/vhosts/{id}", s.adminOnly(s.deleteVHost))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUserByUsername(r.Context(), creds.Username)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "invalid username or password", http.StatusUnauthorized)
		} else {
			s.jsonError(w, "database error: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(creds.Password)); err != nil {
		s.jsonError(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		s.jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	if user.TwoFactorEnabled {
		pendingToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":       user.ID,
			"username": user.Username,
			"pending":  true,
			"exp":      time.Now().Add(time.Minute * 5).Unix(),
		})
		pendingTokenString, err := pendingToken.SignedString([]byte(dbCfg.JWTSecret))
		if err != nil {
			s.jsonError(w, "failed to generate pending token", http.StatusInternalServerError)
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
		s.jsonError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := sameSiteFromEnv()
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
	s.recordAuditLog(r, "INFO", "User "+user.Username+" logged in", "login", user.ID, "user", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.config == nil || !s.config.Auth.OIDC.Enabled {
		s.jsonError(w, "OIDC is not enabled", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	provider, err := oidc.NewProvider(ctx, s.config.Auth.OIDC.IssuerURL)
	if err != nil {
		s.jsonError(w, "Failed to get provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	scopes := s.config.Auth.OIDC.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	oauth2Config := oauth2.Config{
		ClientID:     s.config.Auth.OIDC.ClientID,
		ClientSecret: s.config.Auth.OIDC.ClientSecret,
		RedirectURL:  s.config.Auth.OIDC.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	state, _ := s.generateRandomPassword(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   300,
	})

	http.Redirect(w, r, oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.config == nil || !s.config.Auth.OIDC.Enabled {
		s.jsonError(w, "OIDC is not enabled", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || r.URL.Query().Get("state") != stateCookie.Value {
		s.jsonError(w, "Invalid state", http.StatusBadRequest)
		return
	}

	provider, err := oidc.NewProvider(ctx, s.config.Auth.OIDC.IssuerURL)
	if err != nil {
		s.jsonError(w, "Failed to get provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	oauth2Config := oauth2.Config{
		ClientID:     s.config.Auth.OIDC.ClientID,
		ClientSecret: s.config.Auth.OIDC.ClientSecret,
		RedirectURL:  s.config.Auth.OIDC.RedirectURL,
		Endpoint:     provider.Endpoint(),
	}

	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		s.jsonError(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		s.jsonError(w, "No id_token", http.StatusInternalServerError)
		return
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: s.config.Auth.OIDC.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		s.jsonError(w, "Failed to verify ID token", http.StatusInternalServerError)
		return
	}

	var claims struct {
		Email    string `json:"email"`
		Username string `json:"preferred_username"`
		Name     string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		s.jsonError(w, "Failed to parse claims", http.StatusInternalServerError)
		return
	}

	// Find or create user
	var user storage.User
	var uErr error
	user, uErr = s.storage.GetUserByEmail(ctx, claims.Email)
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
		_ = s.storage.CreateUser(ctx, user)
	}

	// Generate Hermod JWT and set cookie
	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		s.jsonError(w, "failed to load config", http.StatusInternalServerError)
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
		s.jsonError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "User "+user.Username+" logged in (OIDC)", "login", user.ID, "user", "", nil)

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := sameSiteFromEnv()
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

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "If the email exists, a new password has been sent."})
			return
		}
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newPass, _ := s.generateRandomPassword(12)
	hashedPass, _ := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	user.Password = string(hashedPass)
	_ = s.storage.UpdateUser(r.Context(), user)

	s.recordAuditLog(r, "INFO", "Password reset for "+user.Username, "update", user.ID, "user", "", nil)

	val, err := s.storage.GetSetting(r.Context(), "notification_settings")
	if err != nil || val == "" {
		s.jsonError(w, "SMTP is not configured", http.StatusInternalServerError)
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

func (s *Server) generateRandomPassword(length int) (string, error) {
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

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, total, err := s.storage.ListUsers(r.Context(), s.parseCommonFilter(r))
	if err != nil {
		s.jsonError(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	for i := range users {
		s.sanitizeUser(&users[i])
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  users,
		"total": total,
	})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	user, err := s.storage.GetUser(r.Context(), id)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	s.sanitizeUser(&user)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	// Allow creating the very first user during initial setup without authentication.
	// If users already exist, only administrators can create new users.
	initialSetup := false
	if s.storage != nil {
		if _, total, err := s.storage.ListUsers(r.Context(), storage.CommonFilter{Limit: 1}); err == nil && total == 0 {
			initialSetup = true
		}
	}

	if !initialSetup {
		role, _ := s.getRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	var user storage.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
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

	if err := s.storage.CreateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.sanitizeUser(&user)
	s.recordAuditLog(r, "INFO", "Created user "+user.Username, "create", user.ID, "user", "", user)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	var req storage.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUser(r.Context(), id)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
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

	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to update user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.sanitizeUser(&user)
	s.recordAuditLog(r, "INFO", "Updated user "+user.Username, "update", user.ID, "user", "", user)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) changeUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// RBAC: only admins or the user themselves can change password
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if ok && userCtx.Role != storage.RoleAdministrator && userCtx.ID != id {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	hashed, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	user, err := s.storage.GetUser(r.Context(), id)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	user.Password = string(hashed)

	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to update password: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "password updated"})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	if err := s.storage.DeleteUser(r.Context(), id); err != nil {
		s.jsonError(w, "Failed to delete user", http.StatusInternalServerError)
		return
	}
	s.recordAuditLog(r, "INFO", "Deleted user "+id, "delete", id, "user", "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setup2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if !ok {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := s.storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Hermod",
		AccountName: user.Email,
	})
	if err != nil {
		s.jsonError(w, "Failed to generate 2FA key", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
}

func (s *Server) verify2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if !ok {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	valid := totp.Validate(req.Code, req.Secret)
	if !valid {
		s.jsonError(w, "Invalid verification code", http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.TwoFactorEnabled = true
	user.TwoFactorSecret = req.Secret
	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to update user", http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Enabled 2FA for "+user.Username, "update", user.ID, "user", "", nil)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "2FA enabled"})
}

func (s *Server) disable2FA(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if !ok {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := s.storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.TwoFactorEnabled = false
	user.TwoFactorSecret = ""
	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to update user", http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Disabled 2FA for "+user.Username, "update", user.ID, "user", "", nil)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "2FA disabled"})
}

func (s *Server) login2FA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID       string `json:"user_id"`
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		s.jsonError(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Verify pending token
	token, err := jwt.Parse(req.PendingToken, func(token *jwt.Token) (any, error) {
		return []byte(dbCfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		s.jsonError(w, "Invalid or expired session", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["id"] != req.UserID || claims["pending"] != true {
		s.jsonError(w, "Invalid session", http.StatusUnauthorized)
		return
	}

	user, err := s.storage.GetUser(r.Context(), req.UserID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusUnauthorized)
		return
	}

	if !user.TwoFactorEnabled {
		s.jsonError(w, "2FA is not enabled for this user", http.StatusBadRequest)
		return
	}

	valid := totp.Validate(req.Code, user.TwoFactorSecret)
	if !valid {
		s.jsonError(w, "Invalid 2FA code", http.StatusUnauthorized)
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
		s.jsonError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return r.TLS != nil
	}

	ss := sameSiteFromEnv()
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
	s.recordAuditLog(r, "INFO", "User "+user.Username+" logged in (2FA)", "login", user.ID, "user", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}

func (s *Server) generatePasswordHandler(w http.ResponseWriter, r *http.Request) {
	// Requires authentication OR initial setup
	if !s.isFirstRun(r.Context()) {
		_, ok := r.Context().Value(userContextKey).(*storage.User)
		if !ok {
			s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	password, err := s.generateRandomPassword(16)
	if err != nil {
		s.jsonError(w, "Failed to generate password", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"password": password})
}

func (s *Server) sanitizeUser(u *storage.User) {
	if u == nil {
		return
	}
	u.Password = ""
	u.TwoFactorSecret = ""
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if !ok {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := s.storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	s.sanitizeUser(&user)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := r.Context().Value(userContextKey).(*storage.User)
	if !ok {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUser(r.Context(), userCtx.ID)
	if err != nil {
		s.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	user.FullName = req.FullName
	user.Email = req.Email

	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		s.jsonError(w, "Failed to update profile: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.sanitizeUser(&user)
	s.recordAuditLog(r, "INFO", "Updated profile for "+user.Username, "update", user.ID, "user", "", user)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) listVHosts(w http.ResponseWriter, r *http.Request) {
	role, vhosts := s.getRoleAndVHosts(r)

	allVHosts, total, err := s.storage.ListVHosts(r.Context(), storage.CommonFilter{})
	if err != nil {
		s.jsonError(w, "Failed to list vhosts", http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.VHost{}
		for _, vh := range allVHosts {
			if s.hasVHostAccess(vh.Name, vhosts) {
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

func (s *Server) getVHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vh, err := s.storage.GetVHost(r.Context(), id)
	if err != nil {
		s.jsonError(w, "VHost not found", http.StatusNotFound)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(vh.Name, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(vh)
}

func (s *Server) createVHost(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var vh storage.VHost
	if err := json.NewDecoder(r.Body).Decode(&vh); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.storage.CreateVHost(r.Context(), vh); err != nil {
		s.jsonError(w, "Failed to create vhost: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Created vhost "+vh.Name, "create", vh.ID, "vhost", "", vh)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(vh)
}

func (s *Server) deleteVHost(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		s.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	if err := s.storage.DeleteVHost(r.Context(), id); err != nil {
		s.jsonError(w, "Failed to delete vhost", http.StatusInternalServerError)
		return
	}
	s.recordAuditLog(r, "INFO", "Deleted vhost "+id, "delete", id, "vhost", "", nil)
	w.WriteHeader(http.StatusNoContent)
}
