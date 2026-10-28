// подключение после применения миграций
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kheguy/gophermart/internal/repository"
	"github.com/kheguy/gophermart/migrations"
)

// пул и хранилище с общим временем жизни
type Client struct {
	pool    pool
	storage *repository.DBStorage
}

// ошибка запуска не оставляет открытый пул
func NewClient(ctx context.Context, dsn string) (*Client, error) {
	return newClient(
		ctx,
		dsn,
		migrations.Up,
		func(ctx context.Context, dsn string) (pool, error) {
			return pgxpool.New(ctx, dsn)
		},
	)
}

type pool interface {
	repository.Pool
	// проверка базы с отменой через контекст
	Ping(context.Context) error
	// полное завершение работы пула
	Close()
}

type migrateFunc func(context.Context, string) error

type poolFactory func(context.Context, string) (pool, error)

func newClient(ctx context.Context, dsn string, migrate migrateFunc, newPool poolFactory) (*Client, error) {
	if err := migrate(ctx, dsn); err != nil {
		return nil, fmt.Errorf("apply database migrations error: %w", err)
	}

	pool, err := newPool(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create database pool error: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database error: %w", err)
	}

	return &Client{
		pool:    pool,
		storage: repository.NewDBStorage(pool),
	}, nil
}

// хранилище использует тот же пул
func (c *Client) Storage() *repository.DBStorage {
	return c.storage
}

// контекст ограничивает ожидание ответа
func (c *Client) Ping(ctx context.Context) error {
	return c.pool.Ping(ctx)
}

// ждём возврата занятых соединений
func (c *Client) Close() {
	c.pool.Close()
}
