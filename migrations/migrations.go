// SQL файлы входят в бинарник
package migrations

import (
	"context"
	"embed"
	"errors"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var files embed.FS

// миграции с ограничением времени запросов
func Up(ctx context.Context, databaseDSN string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	config, err := pgx.ParseConfig(databaseDSN)
	if err != nil {
		return err
	}
	config.ConnectTimeout = 5 * time.Second
	config.RuntimeParams["statement_timeout"] = "5000"

	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{StatementTimeout: 5 * time.Second})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	source, err := iofs.New(files, ".")
	if err != nil {
		_ = driver.Close()
		return err
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return err
	}
	defer func() {
		_, _ = m.Close()
	}()
	stop := context.AfterFunc(ctx, func() { m.GracefulStop <- true })
	defer stop()

	err = m.Up()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}

	return err
}
