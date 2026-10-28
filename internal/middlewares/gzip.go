package middlewares

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter  *gzip.Writer
	wroteHeader bool
}

// сжимаем JSON и HTML если есть тело
func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	contentType := w.Header().Get("Content-Type")
	compressible := strings.HasPrefix(contentType, "application/json") || strings.HasPrefix(contentType, "text/html")

	if statusCode >= 200 && statusCode != http.StatusNoContent && statusCode != http.StatusNotModified &&
		w.Header().Get("Content-Encoding") == "" && compressible {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.gzipWriter = gzip.NewWriter(w.ResponseWriter)
	}

	w.ResponseWriter.WriteHeader(statusCode)
}

// первый вызов сам выставляет статус
func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if w.gzipWriter != nil {
		return w.gzipWriter.Write(data)
	}

	return w.ResponseWriter.Write(data)
}

// поток сжатия надо дописать до конца
func (w *gzipResponseWriter) Close() error {
	if w.gzipWriter == nil {
		return nil
	}

	return w.gzipWriter.Close()
}

// сжатие ответа только с согласия клиента
func WithGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		addVary(res.Header(), "Accept-Encoding")

		if encoding := strings.TrimSpace(req.Header.Get("Content-Encoding")); encoding != "" {
			if !strings.EqualFold(encoding, "gzip") {
				http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}

			reader, err := gzip.NewReader(req.Body)

			if err != nil {
				http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			defer reader.Close()
			req.Body = reader
		}

		if req.Method == http.MethodHead || !acceptsGzip(req.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(res, req)
			return
		}

		writer := &gzipResponseWriter{ResponseWriter: res}
		defer writer.Close()
		next.ServeHTTP(writer, req)
	})
}

func acceptsGzip(header string) bool {
	wildcard := false
	for _, encoding := range strings.Split(header, ",") {
		name, params, err := mime.ParseMediaType(strings.TrimSpace(encoding))
		if err != nil {
			continue
		}

		accepted := true
		if value, exists := params["q"]; exists {
			quality, err := strconv.ParseFloat(value, 64)
			accepted = err == nil && quality > 0 && quality <= 1
		}
		if name == "gzip" {
			return accepted
		}
		if name == "*" {
			wildcard = accepted
		}
	}

	return wildcard
}

func addVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for _, existing := range strings.Split(line, ",") {
			existing = strings.TrimSpace(existing)
			if strings.EqualFold(existing, value) || existing == "*" {
				return
			}
		}
	}
	header.Add("Vary", value)
}
