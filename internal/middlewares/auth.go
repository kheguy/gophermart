// авторизация сжатие и логи запросов
package middlewares

import (
	"context"
	"net/http"
	"strings"
)

type userIDKey struct{}

// проверка подписи и срока токена
type TokenParser interface {
	// возвращает ID после проверки токена
	ParseToken(token string) (int64, error)
}

// заголовок авторизации важнее cookie
func WithAuth(auth TokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			var token string
			headers := req.Header.Values("Authorization")

			if len(headers) > 0 {
				fields := strings.Fields(headers[0])

				if len(headers) != 1 || len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
					http.Error(res, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
					return
				}
				token = fields[1]
			} else if cookie, err := req.Cookie("session"); err == nil {
				token = cookie.Value
			}

			if token == "" {
				http.Error(res, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}

			userID, err := auth.ParseToken(token)

			if err != nil || userID <= 0 {
				http.Error(res, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(req.Context(), userIDKey{}, userID)
			next.ServeHTTP(res, req.WithContext(ctx))
		})
	}
}

// без авторизации получим ноль
func UserID(ctx context.Context) int64 {
	userID, _ := ctx.Value(userIDKey{}).(int64)

	return userID
}
