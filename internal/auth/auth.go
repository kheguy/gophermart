// сессии проверяются без запроса к БД
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// один ключ на все пользовательские токены
type Manager struct {
	key []byte
	now func() time.Time
}

// копируем ключ чтобы снаружи не поменяли
func NewManager(key []byte) (*Manager, error) {
	if len(key) != 32 {
		return nil, errors.New("auth key must contain 32 bytes")
	}

	return &Manager{
		key: append([]byte(nil), key...),
		now: time.Now,
	}, nil
}

// выдаём токен на сутки
func (m *Manager) CreateToken(userID int64) (string, error) {
	if userID <= 0 {
		return "", errors.New("invalid user ID")
	}

	expires := m.now().Add(24 * time.Hour).Unix()
	payload := strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(expires, 10)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(m.sign(encoded))

	return encoded + "." + signature, nil
}

// просроченный токен больше не принимаем
func (m *Manager) ParseToken(token string) (int64, error) {
	if len(token) > 256 {
		return 0, errors.New("invalid token")
	}

	parts := strings.Split(token, ".")

	if len(parts) != 2 {
		return 0, errors.New("invalid token")
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])

	if err != nil || !hmac.Equal(signature, m.sign(parts[0])) {
		return 0, errors.New("invalid token signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])

	if err != nil {
		return 0, errors.New("invalid token payload")
	}

	values := strings.Split(string(payload), ":")

	if len(values) != 2 {
		return 0, errors.New("invalid token payload")
	}

	userID, err := strconv.ParseInt(values[0], 10, 64)

	if err != nil || userID <= 0 {
		return 0, errors.New("invalid token user")
	}

	expires, err := strconv.ParseInt(values[1], 10, 64)

	if err != nil || m.now().Unix() >= expires {
		return 0, errors.New("expired token")
	}

	return userID, nil
}

func (m *Manager) sign(payload string) []byte {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(payload))

	return mac.Sum(nil)
}
