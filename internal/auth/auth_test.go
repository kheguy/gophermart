package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager([]byte(strings.Repeat("a", 32)))
	require.NoError(t, err)
	return manager
}

func TestNewManagerRejectsInvalidKey(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		manager, err := NewManager(make([]byte, size))
		require.Error(t, err)
		assert.Nil(t, manager)
	}
}

func TestManagersShareKey(t *testing.T) {
	key := []byte(strings.Repeat("a", 32))
	manager, err := NewManager(key)
	require.NoError(t, err)
	other, err := NewManager(key)
	require.NoError(t, err)

	token, err := manager.CreateToken(42)
	require.NoError(t, err)
	key[0] = 'b'

	id, err := other.ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
	_, err = manager.ParseToken(token)
	require.NoError(t, err)
}

func TestToken(t *testing.T) {
	manager := newTestManager(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	token, err := manager.CreateToken(42)
	require.NoError(t, err)
	id, err := manager.ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
	manager.now = func() time.Time { return now.Add(24 * time.Hour) }
	_, err = manager.ParseToken(token)
	require.Error(t, err)
}

func TestTokenRejectsInvalidUser(t *testing.T) {
	manager := newTestManager(t)
	for _, id := range []int64{0, -1} {
		_, err := manager.CreateToken(id)
		require.Error(t, err)
	}
}

func TestTokenRejectsInvalidSignature(t *testing.T) {
	manager := newTestManager(t)
	token, err := manager.CreateToken(1)
	require.NoError(t, err)
	other, err := NewManager([]byte(strings.Repeat("b", 32)))
	require.NoError(t, err)
	_, err = other.ParseToken(token)
	require.Error(t, err)
	_, err = manager.ParseToken(token[:len(token)-1] + "!")
	require.Error(t, err)
	for _, token := range []string{"", "abc", "a.b.c", "a.%%%", "a.b", strings.Repeat("a", 257)} {
		_, err := manager.ParseToken(token)
		require.Error(t, err)
	}
}

func TestTokenRejectsInvalidPayload(t *testing.T) {
	manager := newTestManager(t)
	for _, payload := range []string{"42", "0:99999999999", "x:99999999999", "1:no", "1:0", "1:99999999999999999999999999999999", "1:2:3"} {
		t.Run(payload, func(t *testing.T) {
			encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
			token := encoded + "." + base64.RawURLEncoding.EncodeToString(manager.sign(encoded))
			_, err := manager.ParseToken(token)
			require.Error(t, err)
		})
	}
	encoded := "%%%"
	_, err := manager.ParseToken(encoded + "." + base64.RawURLEncoding.EncodeToString(manager.sign(encoded)))
	require.Error(t, err)
}
