package accrual

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ХЕх мда
func TestHTTPClient_GetOrder(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		body   string
		result Result
		err    bool
	}{
		{
			name: "processed", code: http.StatusOK,
			body:   `{"order":"123","status":"PROCESSED","accrual":500.25}`,
			result: Result{Order: "123", Status: models.Processed, Accrual: "500.25"},
		},
		{
			name: "registered", code: http.StatusOK,
			body:   `{"order":"123","status":"REGISTERED"}`,
			result: Result{Order: "123", Status: models.Registered},
		},
		{
			name: "processing", code: http.StatusOK,
			body:   `{"order":"123","status":"PROCESSING"}`,
			result: Result{Order: "123", Status: models.Processing},
		},
		{
			name: "invalid", code: http.StatusOK,
			body:   `{"order":"123","status":"INVALID"}`,
			result: Result{Order: "123", Status: models.Invalid},
		},
		{
			name: "fractional points", code: http.StatusOK,
			body:   `{"order":"123","status":"PROCESSED","accrual":0.001}`,
			result: Result{Order: "123", Status: models.Processed, Accrual: "0.001"},
		},
		{name: "oversized response", code: http.StatusOK, body: `{"order":"123","status":"PROCESSED"}` + strings.Repeat(" ", 1<<20) + "garbage", err: true},
		{name: "not registered", code: http.StatusNoContent, err: true},
		{name: "server error", code: http.StatusInternalServerError, err: true},
		{name: "invalid json", code: http.StatusOK, body: `{`, err: true},
		{name: "another order", code: http.StatusOK, body: `{"order":"321","status":"PROCESSED"}`, err: true},
		{name: "unknown status", code: http.StatusOK, body: `{"order":"123","status":"NEW"}`, err: true},
		{name: "negative accrual", code: http.StatusOK, body: `{"order":"123","status":"PROCESSED","accrual":-1}`, err: true},
		{name: "invalid accrual", code: http.StatusOK, body: `{"order":"123","status":"PROCESSED","accrual":"one"}`, err: true},
		{name: "extra json", code: http.StatusOK, body: `{"order":"123","status":"INVALID"} {}`, err: true},
		{name: "trailing data", code: http.StatusOK, body: `{"order":"123","status":"INVALID"} error`, err: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/orders/123", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Accept"))
				w.WriteHeader(tt.code)
				_, err := w.Write([]byte(tt.body))
				assert.NoError(t, err)
			}))
			defer server.Close()

			client := NewHTTPClient(server.URL + "/")
			result, err := client.GetOrder(context.Background(), "123")
			if tt.err {
				require.Error(t, err)
				if tt.code == http.StatusNoContent {
					assert.ErrorIs(t, err, ErrNotRegistered)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.result, result)
		})
	}
}

func TestHTTPClient_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := NewHTTPClient(server.URL).GetOrder(context.Background(), "123")
	var limit *RateLimitError
	require.ErrorAs(t, err, &limit)
	assert.Equal(t, time.Minute, limit.RetryAfter)
	assert.Contains(t, limit.Error(), "1m0s")
}

func TestHTTPClient_RequestError(t *testing.T) {
	t.Run("invalid address", func(t *testing.T) {
		_, err := NewHTTPClient("://").GetOrder(context.Background(), "123")
		require.Error(t, err)
	})
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewHTTPClient("http://localhost").GetOrder(ctx, "123")
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("network error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		server.Close()
		_, err := NewHTTPClient(server.URL).GetOrder(context.Background(), "123")
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrNotRegistered))
	})
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "seconds", value: "60", want: time.Minute},
		{name: "zero", value: "0", want: 0},
		{name: "whitespace", value: " 30 ", want: 30 * time.Second},
		{name: "http date", value: now.Add(time.Minute).Format(http.TimeFormat), want: time.Minute},
		{name: "past date", value: now.Add(-time.Minute).Format(http.TimeFormat), want: time.Second},
		{name: "missing", want: time.Second},
		{name: "invalid", value: "later", want: time.Second},
		{name: "negative", value: "-1", want: time.Second},
		{name: "overflow", value: "9223372036854775807", want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, retryAfter(tt.value, now))
		})
	}
}
