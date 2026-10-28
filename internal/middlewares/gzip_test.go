package middlewares

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithGzipDecompressesRequestAndCompressesJSONResponse(t *testing.T) {
	body := []byte(`{"current":500.5,"withdrawn":42}`)
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	_, err := zipper.Write(body)
	require.NoError(t, err)
	require.NoError(t, zipper.Close())
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		decoded, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.Equal(t, body, decoded)
		res.Header().Set("Content-Type", "application/json")
		res.Header().Set("Content-Length", "33")
		_, err = res.Write(decoded)
		require.NoError(t, err)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/user/balance", &compressed)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	assert.Equal(t, http.StatusOK, res.Code)
	assert.Equal(t, "gzip", res.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res.Header().Get("Vary"))
	assert.Empty(t, res.Header().Get("Content-Length"))
	assert.Equal(t, body, gunzipBytes(t, res.Body.Bytes()))
}

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header   string
		accepted bool
	}{
		{header: "gzip", accepted: true},
		{header: "deflate, gzip;q=0.5", accepted: true},
		{header: "*;q=0.5", accepted: true},
		{header: ""},
		{header: "deflate"},
		{header: "gzip;q=0"},
		{header: "*;q=1, gzip;q=0"},
		{header: "gzip;q=broken"},
		{header: "gzip;q=2"},
		{header: "gzip;q=-1"},
		{header: "gzip;q=NaN"},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			assert.Equal(t, tt.accepted, acceptsGzip(tt.header))
		})
	}
}

func TestWithGzipCompressesHTMLResponse(t *testing.T) {
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
		res.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := res.Write([]byte("<h1>Balance</h1>"))
		require.NoError(t, err)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	assert.Equal(t, "gzip", res.Header().Get("Content-Encoding"))
	assert.Equal(t, "<h1>Balance</h1>", string(gunzipBytes(t, res.Body.Bytes())))
}

func TestWithGzipDoesNotCompressPlainText(t *testing.T) {
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
		res.Header().Set("Content-Type", "text/plain")
		_, err := res.Write([]byte("OK"))
		require.NoError(t, err)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	assert.Empty(t, res.Header().Get("Content-Encoding"))
	assert.Equal(t, "OK", res.Body.String())
}

func TestWithGzipKeepsExistingEncoding(t *testing.T) {
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
		res.Header().Set("Content-Type", "application/json")
		res.Header().Set("Content-Encoding", "br")
		_, err := res.Write([]byte("encoded body"))
		require.NoError(t, err)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	assert.Equal(t, "br", res.Header().Get("Content-Encoding"))
	assert.Equal(t, "encoded body", res.Body.String())
}

func TestWithGzipDoesNotCompressBodylessStatus(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
				res.Header().Set("Content-Type", "application/json")
				res.WriteHeader(status)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			assert.Equal(t, status, res.Code)
			assert.Empty(t, res.Header().Get("Content-Encoding"))
			assert.Empty(t, res.Body.String())
		})
	}
}

func TestWithGzipDoesNotCompressHEAD(t *testing.T) {
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
		res.Header().Set("Content-Type", "application/json")
		res.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	assert.Equal(t, http.StatusOK, res.Code)
	assert.Empty(t, res.Header().Get("Content-Encoding"))
	assert.Empty(t, res.Body.String())
}

func TestWithGzipRejectsInvalidEncoding(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "gzip, br"} {
		t.Run(encoding, func(t *testing.T) {
			called := false
			handler := WithGzip(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("broken"))
			req.Header.Set("Content-Encoding", encoding)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			assert.Equal(t, http.StatusBadRequest, res.Code)
			assert.False(t, called)
		})
	}
}

func TestWithGzipPreservesVary(t *testing.T) {
	for _, vary := range []string{"Origin", "Accept-Encoding", "origin, accept-encoding", "*"} {
		t.Run(vary, func(t *testing.T) {
			handler := WithGzip(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			res := httptest.NewRecorder()
			res.Header().Set("Vary", vary)
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
			if vary == "Origin" {
				assert.Equal(t, []string{"Origin", "Accept-Encoding"}, res.Header().Values("Vary"))
			} else {
				assert.Equal(t, []string{vary}, res.Header().Values("Vary"))
			}
		})
	}
}

func TestWithGzipKeepsFirstStatus(t *testing.T) {
	res := httptest.NewRecorder()
	handler := WithGzip(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
		res.WriteHeader(http.StatusAccepted)
		res.WriteHeader(http.StatusInternalServerError)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(res, req)
	assert.Equal(t, http.StatusAccepted, res.Code)
}

func gunzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	return body
}
