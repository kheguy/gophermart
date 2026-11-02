package middlewares

import (
	"log/slog"
	"net/http"
	"time"
)

type responseData struct {
	status int
	size   int
}

type loggingResponseWriter struct {
	http.ResponseWriter
	responseData *responseData
}

// в лог идёт фактически записанный размер
func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.responseData.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	size, err := w.ResponseWriter.Write(data)
	w.responseData.size += size
	return size, err
}

// повторный статус уже не учитываем
func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	if w.responseData.status != 0 {
		return
	}

	w.responseData.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// метод путь статус и время запроса
func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		start := time.Now()

		data := &responseData{}
		writer := &loggingResponseWriter{
			ResponseWriter: res,
			responseData:   data,
		}

		next.ServeHTTP(writer, req)

		if data.status == 0 {
			data.status = http.StatusOK
		}

		slog.Info("request",
			"method", req.Method,
			"uri", req.RequestURI,
			"status", data.status,
			"duration", time.Since(start),
			"size", data.size,
		)
	})
}
