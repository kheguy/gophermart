package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareConfig(t *testing.T, args ...string) {
	t.Helper()
	previousArgs := os.Args
	os.Args = append([]string{"gophermart"}, args...)
	t.Cleanup(func() { os.Args = previousArgs })
	for _, name := range []string{"RUN_ADDRESS", "DATABASE_URI", "ACCRUAL_SYSTEM_ADDRESS"} {
		previous, exists := os.LookupEnv(name)
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, previous)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestLoadServerDefaults(t *testing.T) {
	prepareConfig(t, "-d=postgres://localhost/gophermart")
	cfg, err := LoadServer()
	require.NoError(t, err)
	assert.Equal(t, "localhost:8080", cfg.Address)
	assert.Equal(t, "postgres://localhost/gophermart", cfg.DBAddress)
	assert.Equal(t, "http://localhost:8081", cfg.AccrualAddress)
}

func TestLoadServerFlags(t *testing.T) {
	prepareConfig(t, "-a=:9000", "-d=postgres://db/loyalty", "-r=http://accrual:8081")
	cfg, err := LoadServer()
	require.NoError(t, err)
	assert.Equal(t, ServerConfig{Address: ":9000", DBAddress: "postgres://db/loyalty", AccrualAddress: "http://accrual:8081"}, cfg)
}

func TestLoadServerEnvironmentPriority(t *testing.T) {
	prepareConfig(t, "-a=:9000", "-d=postgres://db/loyalty", "-r=http://accrual:8081")
	t.Setenv("RUN_ADDRESS", "localhost:9001")
	t.Setenv("DATABASE_URI", "postgres://env/loyalty")
	t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "https://env-accrual")
	cfg, err := LoadServer()
	require.NoError(t, err)
	assert.Equal(t, ServerConfig{Address: "localhost:9001", DBAddress: "postgres://env/loyalty", AccrualAddress: "https://env-accrual"}, cfg)
}

func TestLoadServerInvalidConfig(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-unknown"}, {"-d=db", "-a=localhost"}, {"-d=db", "-r=accrual"}, {"-d=db", "-r=ftp://accrual"}, {"-d=db", "-r=http://accrual?query=1"}, {"-d=db", "-r=http://user:secret@accrual"}, {"-d=db", "-r=://"},
	} {
		t.Run("invalid", func(t *testing.T) {
			prepareConfig(t, args...)
			_, err := LoadServer()
			require.Error(t, err)
		})
	}
}
