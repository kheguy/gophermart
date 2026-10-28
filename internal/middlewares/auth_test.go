package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type tokenParserFunc func(string) (int64, error)

func (f tokenParserFunc) ParseToken(token string) (int64, error) {
	return f(token)
}

func TestWithAuth(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		cookie  string
		status  int
	}{
		{name: "bearer", headers: []string{"Bearer valid"}, status: http.StatusOK},
		{name: "case insensitive", headers: []string{"bearer valid"}, status: http.StatusOK},
		{name: "cookie", cookie: "valid", status: http.StatusOK},
		{name: "header priority", headers: []string{"Bearer valid"}, cookie: "invalid", status: http.StatusOK},
		{name: "missing", status: http.StatusUnauthorized},
		{name: "invalid cookie", cookie: "invalid", status: http.StatusUnauthorized},
		{name: "invalid bearer", headers: []string{"Bearer invalid"}, status: http.StatusUnauthorized},
		{name: "zero user", headers: []string{"Bearer zero"}, status: http.StatusUnauthorized},
		{name: "negative user", headers: []string{"Bearer negative"}, status: http.StatusUnauthorized},
		{name: "wrong auth scheme", headers: []string{"Basic valid"}, cookie: "valid", status: http.StatusUnauthorized},
		{name: "invalid header no fallback", headers: []string{"Bearer invalid"}, cookie: "valid", status: http.StatusUnauthorized},
		{name: "empty header no fallback", headers: []string{""}, cookie: "valid", status: http.StatusUnauthorized},
		{name: "missing bearer value", headers: []string{"Bearer"}, cookie: "valid", status: http.StatusUnauthorized},
		{name: "extra field", headers: []string{"Bearer valid extra"}, cookie: "valid", status: http.StatusUnauthorized},
		{name: "multiple headers", headers: []string{"Bearer valid", "Bearer valid"}, status: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := tokenParserFunc(func(token string) (int64, error) {
				switch token {
				case "valid":
					return 42, nil
				case "zero":
					return 0, nil
				case "negative":
					return -1, nil
				default:
					return 0, assert.AnError
				}
			})
			called := false
			handler := WithAuth(parser)(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
				called = true
				assert.EqualValues(t, 42, UserID(req.Context()))
				res.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
			for _, header := range tt.headers {
				req.Header.Add("Authorization", header)
			}
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "session", Value: tt.cookie})
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			assert.Equal(t, tt.status, res.Code)
			assert.Equal(t, tt.status == http.StatusOK, called)
		})
	}
}

func TestUserIDWithoutAuthentication(t *testing.T) {
	assert.Zero(t, UserID(context.Background()))
}
