package middlewares

import (
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
	for _, status := range []int{0, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := WithLogging(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
				if status != 0 {
					res.WriteHeader(status)
				}
			}))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/user/balance", nil))
			if status == 0 {
				assert.Equal(t, http.StatusOK, res.Code)
			} else {
				assert.Equal(t, status, res.Code)
			}
		})
	}
}
