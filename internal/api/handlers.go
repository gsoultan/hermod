package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/storage"
)

type contextKey string

const (
	userContextKey contextKey = "user"
)

func sameSiteFromEnv() http.SameSite {
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

func (s *Server) jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) parseCommonFilter(r *http.Request) storage.CommonFilter {
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

func (s *Server) getRoleAndVHosts(r *http.Request) (storage.Role, []string) {
	if u, ok := r.Context().Value(userContextKey).(*storage.User); ok {
		return u.Role, u.VHosts
	}
	return "", nil
}

func (s *Server) hasVHostAccess(vhost string, allowedVHosts []string) bool {
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

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public paths
		if path == "/" || path == "/index.html" || path == "/setup" ||
			path == "/api/login" || path == "/api/forgot-password" ||
			path == "/api/config/status" ||
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
			if config.IsDBConfigured() && s.storage != nil {
				if _, total, err := s.storage.ListUsers(r.Context(), storage.CommonFilter{Limit: 1}); err == nil && total == 0 {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		// Allow unauthenticated access to DB config and setup-related endpoints only during initial setup.
		if s.isFirstRun(r.Context()) {
			if r.Method == http.MethodPost && (path == "/api/config/database" || path == "/api/config/database/test" || path == "/api/config/databases" || path == "/api/settings/test" || path == "/api/settings/test-config" || path == "/api/users") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Allow one-shot setup endpoint only during first run; otherwise return Unauthorized
		if r.Method == http.MethodPost && path == "/api/config/setup" {
			if s.isFirstRun(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		tokenString, ok := extractBearerOrCookie(r)
		if !ok {
			s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		dbCfg, err := config.LoadDBConfig()
		if err != nil {
			s.jsonError(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		claims, err := parseSessionClaims(tokenString, []byte(dbCfg.JWTSecret))
		if err != nil {
			s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Use claims to build user context (avoids DB hit and dependency in tests)
		user := storage.User{
			ID:       claims.UserID,
			Username: claims.Username,
			Role:     storage.Role(claims.Role),
			VHosts:   claims.VHosts,
		}

		ctx := context.WithValue(r.Context(), userContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) rbacMiddleware(requiredRole storage.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bypass RBAC during initial setup
			if s.isFirstRun(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}

			user, ok := r.Context().Value(userContextKey).(*storage.User)
			if !ok {
				s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Admin can do anything
			if user.Role == storage.RoleAdministrator {
				next.ServeHTTP(w, r)
				return
			}

			if requiredRole == storage.RoleAdministrator && user.Role != storage.RoleAdministrator {
				s.jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}

			if requiredRole == storage.RoleEditor && user.Role == storage.RoleViewer {
				s.jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) adminOnly(next http.HandlerFunc) http.Handler {
	return s.rbacMiddleware(storage.RoleAdministrator)(next)
}

func (s *Server) editorOnly(next http.HandlerFunc) http.Handler {
	return s.rbacMiddleware(storage.RoleEditor)(next)
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.jsonError(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) storeGuardMiddleware(next http.Handler) http.Handler {
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

		s.storeMu.RLock()
		defer s.storeMu.RUnlock()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
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

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
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

	// Try cookie
	cookie, err := r.Cookie("hermod_session")
	if err == nil {
		return cookie.Value, true
	}

	return "", false
}

func parseSessionClaims(tokenString string, secret []byte) (SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &SessionClaims{}, func(token *jwt.Token) (interface{}, error) {
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
