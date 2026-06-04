package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/filestorage"
)

type WorkerUpdater interface {
	SetStorage(s storage.Storage)
}

type Handler struct {
	Storage     storage.Storage
	LogStorage  storage.Storage
	Registry    *registry.Registry
	Worker      WorkerUpdater
	AI          *ai.SelfHealingService
	Config      *config.Config
	FileStorage filestorage.Storage

	// StoreMu guards concurrent reads/writes to storage during hot-swap.
	StoreMu sync.RWMutex

	// readiness debounce state
	ReadyMu            sync.Mutex
	LastReadyStatus    bool
	LastReadyStatusSet bool
	LastReadyStatusAt  time.Time

	// Common state for middleware
	FormRateLimit sync.Map
	RateLimitOnce sync.Once
	RateLimitQuit chan struct{}
}

type contextKey string

const (
	UserContextKey contextKey = "user"
)

func SameSiteFromEnv() http.SameSite {
	s := os.Getenv("HERMOD_COOKIE_SAMESITE")
	switch strings.ToLower(s) {
	case "lax":
		return http.SameSiteLaxMode
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteStrictMode
	}
}

func (h *Handler) JsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) ParseCommonFilter(r *http.Request) storage.CommonFilter {
	f := storage.CommonFilter{
		Page:   1,
		Limit:  100,
		Search: r.URL.Query().Get("search"),
		VHost:  r.URL.Query().Get("vhost"),
	}

	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		f.Page = p
	}
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		f.Limit = l
	}

	return f
}

func (h *Handler) GetRoleAndVHosts(r *http.Request) (storage.Role, []string) {
	if u, ok := r.Context().Value(UserContextKey).(*storage.User); ok {
		return u.Role, u.VHosts
	}
	return "", nil
}

func (h *Handler) HasVHostAccess(vhost string, allowedVHosts []string) bool {
	if vhost == "" || vhost == "all" || vhost == "default" {
		return true
	}
	for _, av := range allowedVHosts {
		if av == vhost || av == "*" {
			return true
		}
	}
	return false
}

func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public paths
		if path == "/" || path == "/index.html" || path == "/setup" ||
			path == "/api/login" || path == "/api/forgot-password" ||
			path == "/api/config/status" || path == "/api/version" ||
			strings.HasPrefix(path, "/api/webhooks/") ||
			strings.HasPrefix(path, "/api/forms/") ||
			strings.HasPrefix(path, "/forms/") ||
			path == "/livez" || path == "/readyz" || path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow all non-API routes without authentication so the SPA and static assets can load.
		// API endpoints remain protected below.
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Initial setup: allow creating the very first user without authentication
		// Only when DB is configured and there are currently no users.
		if r.Method == http.MethodPost && path == "/api/users" {
			// Ensure DB configured first (handled in setup step 1)
			if config.IsDBConfigured() && h.Storage != nil {
				if _, total, err := h.Storage.ListUsers(r.Context(), storage.CommonFilter{Limit: 1}); err == nil && total == 0 {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		// Allow unauthenticated access to DB config and setup-related endpoints only during initial setup.
		if h.IsFirstRun(r.Context()) {
			if r.Method == http.MethodPost && (path == "/api/config/database" || path == "/api/config/database/test" || path == "/api/config/databases" || path == "/api/settings/test" || path == "/api/settings/test-config" || path == "/api/users") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Allow one-shot setup endpoint only during first run; otherwise return Unauthorized
		if r.Method == http.MethodPost && path == "/api/config/setup" {
			if h.IsFirstRun(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		tokenString, ok := extractBearerOrCookie(r)
		if !ok {
			// Fallback: allow worker token authentication for non-setup API calls
			// Workers authenticate using the X-Worker-Token header.
			if workerToken := r.Header.Get("X-Worker-Token"); workerToken != "" && h.Storage != nil {
				// Find a worker with the provided token. This is a simple linear scan; acceptable for typical small worker counts.
				if workers, _, err := h.Storage.ListWorkers(r.Context(), storage.CommonFilter{Limit: -1}); err == nil {
					for _, wkr := range workers {
						if wkr.Token == workerToken {
							// Build a minimal user context with Editor role and full vhost access.
							user := storage.User{
								ID:       "worker:" + wkr.ID,
								Username: "worker:" + wkr.Name,
								Role:     storage.RoleEditor,
								VHosts:   []string{"*"},
							}
							ctx := context.WithValue(r.Context(), UserContextKey, &user)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
				}
			}
			h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		dbCfg, err := config.LoadDBConfig()
		if err != nil {
			h.JsonError(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		claims, err := parseSessionClaims(tokenString, []byte(dbCfg.JWTSecret))
		if err != nil {
			h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Use claims to build user context (avoids DB hit and dependency in tests)
		user := storage.User{
			ID:       claims.UserID,
			Username: claims.Username,
			Role:     storage.Role(claims.Role),
			VHosts:   claims.VHosts,
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) RbacMiddleware(requiredRole storage.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bypass RBAC during initial setup
			if h.IsFirstRun(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}

			user, ok := r.Context().Value(UserContextKey).(*storage.User)
			if !ok {
				h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Admin can do anything
			if user.Role == storage.RoleAdministrator {
				next.ServeHTTP(w, r)
				return
			}

			if requiredRole == storage.RoleAdministrator && user.Role != storage.RoleAdministrator {
				h.JsonError(w, "Forbidden", http.StatusForbidden)
				return
			}

			if requiredRole == storage.RoleEditor && user.Role == storage.RoleViewer {
				h.JsonError(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (h *Handler) AdminOnly(next http.HandlerFunc) http.Handler {
	return h.RbacMiddleware(storage.RoleAdministrator)(next)
}

func (h *Handler) EditorOnly(next http.HandlerFunc) http.Handler {
	return h.RbacMiddleware(storage.RoleEditor)(next)
}

func (h *Handler) RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				h.JsonError(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) StoreGuardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Avoid holding a read lock for endpoints that may need to acquire a write lock
		// to replace the storage during initial setup. Holding the RLock for the entire
		// request would deadlock when those handlers attempt to Lock().
		// Safe to bypass here because these endpoints manage storage initialization atomically.
		path := r.URL.Path
		if r.Method == http.MethodPost && (path == "/api/config/setup" || path == "/api/config/database") {
			next.ServeHTTP(w, r)
			return
		}

		h.StoreMu.RLock()
		defer h.StoreMu.RUnlock()
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		csp := os.Getenv("HERMOD_CSP")
		if csp == "" {
			csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:;"
			if os.Getenv("HERMOD_ENV") == "production" {
				csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:;"
			}
		}
		w.Header().Set("Content-Security-Policy", csp)

		next.ServeHTTP(w, r)
	})
}

func (h *Handler) CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type SessionClaims struct {
	UserID   string   `json:"id"`
	Username string   `json:"username"`
	Role     string   `json:"role"`
	VHosts   []string `json:"vhosts"`
	jwt.RegisteredClaims
}

func extractBearerOrCookie(r *http.Request) (string, bool) {
	// Try Authorization header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return authHeader[7:], true
	}

	// Try query parameter (specifically for WebSockets)
	if token := r.URL.Query().Get("token"); token != "" {
		return token, true
	}

	// Try cookie
	cookie, err := r.Cookie("hermod_session")
	if err == nil {
		return cookie.Value, true
	}

	return "", false
}

func parseSessionClaims(tokenString string, secret []byte) (SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &SessionClaims{}, func(token *jwt.Token) (any, error) {
		return secret, nil
	})

	if err != nil {
		return SessionClaims{}, err
	}

	if claims, ok := token.Claims.(*SessionClaims); ok && token.Valid {
		return *claims, nil
	}

	return SessionClaims{}, fmt.Errorf("invalid token")
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// IPv4 private ranges
	if ip4 := ip.To4(); ip4 != nil {
		privateIPBlocks := []string{
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
		}
		for _, block := range privateIPBlocks {
			_, ipNet, _ := net.ParseCIDR(block)
			if ipNet.Contains(ip) {
				return true
			}
		}
	} else {
		// IPv6 unique local address (fc00::/7)
		if len(ip) == net.IPv6len {
			return ip[0]&0xfe == 0xfc
		}
	}

	return false
}

func (h *Handler) RecordAuditLog(r *http.Request, level, message, action string, workflowID, sourceID, sinkID string, data any) {
	ctx := r.Context()
	l := storage.Log{
		Timestamp:  time.Now(),
		Level:      level,
		Message:    message,
		Action:     action,
		WorkflowID: workflowID,
		SourceID:   sourceID,
		SinkID:     sinkID,
	}

	user, _ := ctx.Value(UserContextKey).(*storage.User)
	if user != nil {
		l.UserID = user.ID
		l.Username = user.Username
	}

	var payloadStr string
	if data != nil {
		if str, ok := data.(string); ok {
			l.Data = str
			payloadStr = str
		} else {
			if b, err := json.Marshal(data); err == nil {
				l.Data = string(b)
				payloadStr = string(b)
			}
		}
	}

	_ = h.LogStorage.CreateLog(ctx, l)

	// Also write to dedicated audit_logs table
	entityType := ""
	entityID := ""
	if sourceID == "user" || sourceID == "vhost" {
		entityType = sourceID
		entityID = workflowID
	} else if workflowID != "" {
		entityType = "workflow"
		entityID = workflowID
	} else if sourceID != "" {
		entityType = "source"
		entityID = sourceID
	} else if sinkID != "" {
		entityType = "sink"
		entityID = sinkID
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}

	audit := storage.AuditLog{
		Timestamp:  time.Now(),
		UserID:     l.UserID,
		Username:   l.Username,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Payload:    payloadStr,
		IP:         ip,
	}
	_ = h.LogStorage.CreateAuditLog(ctx, audit)
}

func (h *Handler) HtmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

func (h *Handler) WantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (h *Handler) IsFirstRun(ctx context.Context) bool {
	// If DB config file is missing, we are definitely in first-run state
	if !config.IsDBConfigured() {
		return true
	}
	// If storage is not initialized but config exists, it's a failed connection, not a first run.
	if h.Storage == nil {
		return false
	}
	// Check if any user exists
	_, total, err := h.Storage.ListUsers(ctx, storage.CommonFilter{Limit: 1})
	if err != nil {
		// If already configured, a DB error should NOT trigger first-run state.
		return false
	}
	return total == 0
}

func (h *Handler) IsOriginAllowed(origin, referer, allowed string) bool {
	if allowed == "" {
		return true
	}
	allowedList := strings.Split(allowed, ",")
	for _, a := range allowedList {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		// Basic check: origin or referer contains the allowed domain
		if (origin != "" && strings.Contains(origin, a)) || (referer != "" && strings.Contains(referer, a)) {
			return true
		}
	}
	return false
}

func (h *Handler) IsRateLimited(r *http.Request, sourceID string, limit int) bool {
	if limit <= 0 {
		return false
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	// Use X-Forwarded-For if behind a proxy
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip = strings.Split(xff, ",")[0]
	}

	key := fmt.Sprintf("%s:%s:%s", sourceID, ip, time.Now().Format("2006-01-02:15"))
	val, ok := h.FormRateLimit.Load(key)
	count := 0
	if ok {
		count = val.(int)
	}
	if count >= limit {
		return true
	}
	h.FormRateLimit.Store(key, count+1)

	// Lazy start cleanup
	h.StartRateLimitCleanup()

	return false
}

func (h *Handler) StartRateLimitCleanup() {
	h.RateLimitOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					h.FormRateLimit.Range(func(key, value any) bool {
						k := key.(string)
						parts := strings.Split(k, ":")
						if len(parts) >= 3 {
							// Format: sourceID:IP:YYYY-MM-DD:HH
							// The last part is the key suffix we care about
							datePart := parts[len(parts)-1]
							if t, err := time.Parse("2006-01-02:15", datePart); err == nil {
								if time.Since(t) > 2*time.Hour {
									h.FormRateLimit.Delete(key)
								}
							}
						}
						return true
					})
				case <-h.RateLimitQuit:
					return
				}
			}
		}()
	})
}

func (h *Handler) BotProtectionCheck(r *http.Request, payload map[string]any, enable bool, minMs int, srcCfg map[string]string) error {
	ct := r.Header.Get("Content-Type")
	if !enable || (!strings.Contains(ct, "application/x-www-form-urlencoded") && !strings.Contains(ct, "multipart/form-data") && !strings.Contains(ct, "application/json")) {
		return nil
	}

	// Turnstile check if configured
	if srcCfg != nil && srcCfg["turnstile_secret"] != "" {
		token := ""
		if t, ok := payload["cf-turnstile-response"].(string); ok {
			token = t
		}
		if token == "" {
			return fmt.Errorf("missing bot protection token")
		}

		// Verify Turnstile token
		resp, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
			"secret":   {srcCfg["turnstile_secret"]},
			"response": {token},
			"remoteip": {r.RemoteAddr},
		})
		if err != nil {
			return fmt.Errorf("failed to verify bot protection")
		}
		defer resp.Body.Close()
		var res struct {
			Success bool `json:"success"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || !res.Success {
			return fmt.Errorf("bot detected (turnstile)")
		}
	}

	// Honeypot field must be empty
	hp := ""
	if v, ok := payload["website"].(string); ok {
		hp = v
	}
	if strings.TrimSpace(hp) != "" {
		return fmt.Errorf("bot detected")
	}

	// Minimum submit time window (skip for JSON/API submissions)
	if !strings.Contains(ct, "application/json") {
		// Token check
		tokenCookie, _ := r.Cookie("hf_token")
		formToken := ""
		if t, ok := payload["hf_token"].(string); ok {
			formToken = t
		}
		if tokenCookie != nil && (formToken == "" || tokenCookie.Value != formToken) {
			return fmt.Errorf("invalid form token")
		}

		issuedCookie, _ := r.Cookie("hf_issued")
		if issuedCookie != nil && issuedCookie.Value != "" {
			if ms, convErr := strconv.ParseInt(issuedCookie.Value, 10, 64); convErr == nil && minMs > 0 {
				elapsed := time.Since(time.UnixMilli(ms)).Milliseconds()
				if elapsed < int64(minMs) {
					return fmt.Errorf("submitted too quickly")
				}
			}
		}
	}

	return nil
}
