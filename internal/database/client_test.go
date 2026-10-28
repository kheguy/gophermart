package database

import (
	"context"
	"testing"

	"github.com/kheguy/gophermart/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type poolStub struct {
	repository.Pool
	pingErr error
	closed  bool
}

func (p *poolStub) Ping(context.Context) error {
	return p.pingErr
}

func (p *poolStub) Close() {
	p.closed = true
}

func TestNewClient(t *testing.T) {
	for _, tt := range []struct {
		name         string
		migrationErr error
		poolErr      error
		pingErr      error
		message      string
	}{
		{name: "success"},
		{name: "migration error", migrationErr: assert.AnError, message: "apply database migrations error"},
		{name: "pool error", poolErr: assert.AnError, message: "create database pool error"},
		{name: "ping error", pingErr: assert.AnError, message: "ping database error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dsn := "postgres://localhost/gophermart"
			dbPool := &poolStub{pingErr: tt.pingErr}
			factoryCalled := false
			client, err := newClient(ctx, dsn,
				func(actualCtx context.Context, actualDSN string) error {
					assert.Equal(t, ctx, actualCtx)
					assert.Equal(t, dsn, actualDSN)
					return tt.migrationErr
				},
				func(actualCtx context.Context, actualDSN string) (pool, error) {
					factoryCalled = true
					assert.Equal(t, ctx, actualCtx)
					assert.Equal(t, dsn, actualDSN)
					return dbPool, tt.poolErr
				},
			)
			if tt.message != "" {
				assert.Nil(t, client)
				assert.ErrorIs(t, err, assert.AnError)
				assert.ErrorContains(t, err, tt.message)
				assert.Equal(t, tt.pingErr != nil, dbPool.closed)
				assert.Equal(t, tt.migrationErr == nil, factoryCalled)
				return
			}
			require.NoError(t, err)
			assert.Same(t, dbPool, client.pool)
			assert.NotNil(t, client.Storage())
			require.NoError(t, client.Ping(ctx))
			dbPool.pingErr = assert.AnError
			assert.ErrorIs(t, client.Ping(ctx), assert.AnError)
			client.Close()
			assert.True(t, dbPool.closed)
		})
	}
}
