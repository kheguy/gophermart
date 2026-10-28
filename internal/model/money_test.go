package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoneyJSON(t *testing.T) {
	for _, input := range []string{
		"0", "500.5", "42", "0.01", "-0.01", "-42.51", "1.2300",
		"0.001", "123.00000000000000000001", "15e-2", "1E+2",
		"9223372036854775808000000.001",
	} {
		t.Run(input, func(t *testing.T) {
			var amount Money
			require.NoError(t, json.Unmarshal([]byte(input), &amount))
			assert.Equal(t, input, amount.String())
			data, err := json.Marshal(amount)
			require.NoError(t, err)
			assert.Equal(t, input, string(data))
		})
	}

	data, err := json.Marshal(Money(""))
	require.NoError(t, err)
	assert.Equal(t, "0", string(data))
}

func TestMoneySign(t *testing.T) {
	for _, amount := range []Money{"", "0", "-0.000", "0e10", "-0e-9"} {
		assert.True(t, amount.IsZero(), amount)
		assert.False(t, amount.IsNegative(), amount)
		assert.False(t, amount.IsPositive(), amount)
	}
	for _, amount := range []Money{"0.001", "123", "1e-12"} {
		assert.False(t, amount.IsZero(), amount)
		assert.False(t, amount.IsNegative(), amount)
		assert.True(t, amount.IsPositive(), amount)
	}
	for _, amount := range []Money{"-0.001", "-123", "-1e-12"} {
		assert.False(t, amount.IsZero(), amount)
		assert.True(t, amount.IsNegative(), amount)
		assert.False(t, amount.IsPositive(), amount)
	}
}

func TestMoneyRejectsInvalidAmount(t *testing.T) {
	for _, input := range []string{"null", `"12"`, "true", "{}", "[]", "NaN", "1 2", "", "+1", "01", "1.", "1e"} {
		t.Run(input, func(t *testing.T) {
			amount := Money("99")
			require.Error(t, amount.UnmarshalJSON([]byte(input)))
			assert.Equal(t, Money("99"), amount)
		})
	}
}

func TestValidOrder(t *testing.T) {
	for _, number := range []string{"12345678903", "9278923470", "2377225624", "00000000000000000000000000000000000000000000000000000000000000000000"} {
		assert.True(t, ValidOrder(number), number)
	}
	for _, number := range []string{"", "12345678904", "123a", " 12345678903", "12345678903\n", "１２３", "-1"} {
		assert.False(t, ValidOrder(number), number)
	}
}
