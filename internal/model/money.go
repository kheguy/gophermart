package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// десятичные баллы без округления
type Money string

// пустая сумма уходит в базу нулём
func (m Money) String() string {
	if m == "" {
		return "0"
	}
	return string(m)
}

// степень не влияет на проверку нуля
func (m Money) IsZero() bool {
	s := m.String()
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	for _, digit := range s {
		if digit >= '1' && digit <= '9' {
			return false
		}
	}
	return true
}

// отрицательный ноль не считаем минусом
func (m Money) IsNegative() bool {
	return strings.HasPrefix(string(m), "-") && !m.IsZero()
}

// строго больше нуля
func (m Money) IsPositive() bool {
	return !m.IsZero() && !m.IsNegative()
}

// число без кавычек и округления
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(m.String()), nil
}

// принимаем только JSON число
func (m *Money) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || (data[0] != '-' && (data[0] < '0' || data[0] > '9')) || !json.Valid(data) {
		return errors.New("amount must be a number")
	}
	*m = Money(data)
	return nil
}
