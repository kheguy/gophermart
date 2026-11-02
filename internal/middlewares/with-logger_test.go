package middlewares

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingResponseWriter(t *testing.T) {
	res := httptest.NewRecorder()
	data := &responseData{}
	writer := &loggingResponseWriter{ResponseWriter: res, responseData: data}
	_, err := writer.Write([]byte("hello"))
	require.NoError(t, err)
	writer.WriteHeader(http.StatusInternalServerError)
	_, err = writer.Write([]byte(" world"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, data.status)
	assert.Equal(t, 11, data.size)
	assert.Equal(t, "hello world", res.Body.String())
}

func TestWithLogging(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			body := ""
			handler := WithLogging(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
				if status != http.StatusOK {
					res.WriteHeader(status)
					body = "accepted"
					_, _ = res.Write([]byte(body))
				}
			}))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/user/balance", nil))
			assert.Equal(t, status, res.Code)

			var entry map[string]interface{}
			require.NoError(t, json.Unmarshal(logs.Bytes(), &entry))
			assert.Equal(t, "INFO", entry["level"])
			assert.Equal(t, "request", entry["msg"])
			assert.Equal(t, http.MethodGet, entry["method"])
			assert.Equal(t, "/api/user/balance", entry["uri"])
			assert.Equal(t, float64(res.Code), entry["status"])
			assert.Equal(t, float64(len(body)), entry["size"])
			duration, ok := entry["duration"].(float64)
			require.True(t, ok)
			assert.GreaterOrEqual(t, duration, float64(0))
		})
	}
}
