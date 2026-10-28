package main

import (
	"context"
	"testing"

	"github.com/kheguy/gophermart/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRunDatabaseError(t *testing.T) {
	err := run(context.Background(), config.ServerConfig{DBAddress: "postgres://localhost/%invalid"})
	require.ErrorContains(t, err, "DB initialization error")
}
