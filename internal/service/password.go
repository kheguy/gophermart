package service

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
)

const passwordIterations = 120000

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)

	if err != nil {
		return "", err
	}

	return "pbkdf2-sha256$" + strconv.Itoa(passwordIterations) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

func checkPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")

	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != strconv.Itoa(passwordIterations) {
		return false
	}

	salt, err := hex.DecodeString(parts[2])

	if err != nil || len(salt) != 16 {
		return false
	}

	storedHash, err := hex.DecodeString(parts[3])

	if err != nil || len(storedHash) != 32 {
		return false
	}

	hash, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)

	return err == nil && subtle.ConstantTimeCompare(hash, storedHash) == 1
}
