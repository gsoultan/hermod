package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/storage"
)

type userTestStorage struct {
	fakeRBACStorage
	updatedUser storage.User
}

func (s *userTestStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	if id == "user1" {
		return storage.User{ID: "user1", Username: "user1", Role: storage.RoleViewer}, nil
	}
	return storage.User{}, storage.ErrNotFound
}

func (s *userTestStorage) UpdateUser(ctx context.Context, user storage.User) error {
	s.updatedUser = user
	return nil
}

func (s *userTestStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	return nil
}

func (s *userTestStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}

func TestChangeUserPassword(t *testing.T) {
	s := &userTestStorage{}
	server := NewServer(nil, s, nil, nil)
	handler := server.Routes()

	// Mock JWT secret
	config.SaveDBConfig(&config.DBConfig{JWTSecret: "test-secret"})

	t.Run("Change own password", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":   "user1",
			"role": string(storage.RoleViewer),
			"exp":  time.Now().Add(time.Hour).Unix(),
		})
		tokenString, _ := token.SignedString([]byte("test-secret"))

		reqBody := map[string]string{"password": "new-password"}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/api/users/user1/password", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+tokenString)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		if s.updatedUser.ID != "user1" || s.updatedUser.Password == "" {
			t.Errorf("user password was not updated")
		}
	})

	t.Run("Change other user password (forbidden for non-admin)", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":   "user1",
			"role": string(storage.RoleViewer),
			"exp":  time.Now().Add(time.Hour).Unix(),
		})
		tokenString, _ := token.SignedString([]byte("test-secret"))

		reqBody := map[string]string{"password": "new-password"}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/api/users/user2/password", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+tokenString)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("Change other user password (allowed for admin)", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":   "admin1",
			"role": string(storage.RoleAdministrator),
			"exp":  time.Now().Add(time.Hour).Unix(),
		})
		tokenString, _ := token.SignedString([]byte("test-secret"))

		reqBody := map[string]string{"password": "admin-set-password"}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/api/users/user1/password", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+tokenString)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestMe(t *testing.T) {
	s := &userTestStorage{}
	server := NewServer(nil, s, nil, nil)
	handler := server.Routes()

	config.SaveDBConfig(&config.DBConfig{JWTSecret: "test-secret"})

	t.Run("Get me", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":       "user1",
			"username": "user1",
			"role":     string(storage.RoleViewer),
			"exp":      time.Now().Add(time.Hour).Unix(),
		})
		tokenString, _ := token.SignedString([]byte("test-secret"))

		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		var user storage.User
		json.NewDecoder(rr.Body).Decode(&user)
		if user.ID != "user1" {
			t.Errorf("expected user1, got %s", user.ID)
		}
	})

	t.Run("Update me", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"id":       "user1",
			"username": "user1",
			"role":     string(storage.RoleViewer),
			"exp":      time.Now().Add(time.Hour).Unix(),
		})
		tokenString, _ := token.SignedString([]byte("test-secret"))

		reqBody := map[string]string{
			"full_name": "New Name",
			"email":     "new@email.com",
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/api/me", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+tokenString)
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		if s.updatedUser.FullName != "New Name" || s.updatedUser.Email != "new@email.com" {
			t.Errorf("user was not updated correctly: %+v", s.updatedUser)
		}
	})
}
