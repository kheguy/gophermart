//go:build integration

package repository

import (
	"context"
	"crypto/rand"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	models "github.com/kheguy/gophermart/internal/model"
	"github.com/kheguy/gophermart/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestDBStorageIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("gophermart"),
		postgres.WithUsername("gophermart"),
		postgres.WithPassword("password"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, migrations.Up(ctx, dsn))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	storage := NewDBStorage(pool)

	reset := func(t *testing.T) {
		t.Helper()
		_, err := pool.Exec(ctx, "TRUNCATE withdrawals, orders, users RESTART IDENTITY CASCADE")
		require.NoError(t, err)
	}
	createUser := func(t *testing.T, login string) models.User {
		t.Helper()
		user, err := storage.CreateUser(ctx, login, "password hash")
		require.NoError(t, err)
		return user
	}
	readOrders := func(t *testing.T, orders iter.Seq2[models.Order, error]) []models.Order {
		t.Helper()
		var result []models.Order
		for order, err := range orders {
			require.NoError(t, err)
			result = append(result, order)
		}
		return result
	}

	t.Run("users orders and sorting", func(t *testing.T) {
		reset(t)
		alice := createUser(t, "alice")
		bob := createUser(t, "bob")
		user, err := storage.GetUser(ctx, "alice")
		require.NoError(t, err)
		assert.Equal(t, alice, user)
		_, err = storage.CreateUser(ctx, "alice", "other hash")
		assert.ErrorIs(t, err, models.ErrLoginTaken)
		_, err = storage.GetUser(ctx, "missing")
		assert.ErrorIs(t, err, models.ErrUserNotFound)
		_, err = storage.GetBalance(ctx, -1)
		assert.ErrorIs(t, err, models.ErrUserNotFound)

		digits := make([]byte, 16000)
		_, err = rand.Read(digits)
		require.NoError(t, err)
		for i := range digits {
			digits[i] = '0' + digits[i]%10
		}
		longNumber := string(digits)
		require.NoError(t, storage.CreateOrder(ctx, alice.ID, longNumber))
		assert.ErrorIs(t, storage.CreateOrder(ctx, alice.ID, longNumber), models.ErrOrderExists)
		assert.ErrorIs(t, storage.CreateOrder(ctx, bob.ID, longNumber), models.ErrOrderConflict)
		require.NoError(t, storage.CreateOrder(ctx, alice.ID, "111"))
		require.NoError(t, storage.CreateOrder(ctx, alice.ID, "222"))
		_, err = pool.Exec(ctx, `UPDATE orders SET uploaded_at = CASE number
			WHEN '111' THEN '2026-01-02T00:00:00Z'::timestamptz
			WHEN '222' THEN '2026-01-03T00:00:00Z'::timestamptz
			ELSE '2026-01-01T00:00:00Z'::timestamptz END`)
		require.NoError(t, err)
		orders := readOrders(t, storage.GetOrders(ctx, alice.ID))
		require.Len(t, orders, 3)
		assert.Equal(t, "222", orders[0].Number)
		assert.Equal(t, "111", orders[1].Number)
		assert.Equal(t, longNumber, orders[2].Number)
		assert.Empty(t, readOrders(t, storage.GetOrders(ctx, bob.ID)))
		require.NoError(t, storage.UpdateOrder(ctx, longNumber, models.Processed, "15.125"))
		balance, err := storage.GetBalance(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, "15.125", balance.Current.String())

		require.NoError(t, storage.Withdraw(ctx, alice.ID, models.Withdrawal{Order: "333", Sum: "1.125"}))
		require.NoError(t, storage.Withdraw(ctx, alice.ID, models.Withdrawal{Order: "444", Sum: "2"}))
		_, err = pool.Exec(ctx, `UPDATE withdrawals SET processed_at = CASE order_number
			WHEN '333' THEN '2026-01-01T00:00:00Z'::timestamptz
			ELSE '2026-01-02T00:00:00Z'::timestamptz END`)
		require.NoError(t, err)
		withdrawals, err := storage.GetWithdrawals(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, withdrawals, 2)
		assert.Equal(t, "444", withdrawals[0].Order)
		assert.Equal(t, "333", withdrawals[1].Order)
		withdrawals, err = storage.GetWithdrawals(ctx, bob.ID)
		require.NoError(t, err)
		assert.Empty(t, withdrawals)
	})

	t.Run("parallel withdrawals", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		require.NoError(t, storage.UpdateOrder(ctx, "111", models.Processed, "10.05"))

		const workers = 32
		start := make(chan struct{})
		results := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Go(func() {
				<-start
				results <- storage.Withdraw(ctx, user.ID, models.Withdrawal{Order: "222", Sum: "0.70"})
			})
		}
		close(start)
		group.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else {
				assert.ErrorIs(t, err, models.ErrInsufficientFunds)
			}
		}
		assert.Equal(t, 14, successes)
		balance, err := storage.GetBalance(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "0.25", balance.Current.String())
		assert.Equal(t, "9.80", balance.Withdrawn.String())
		withdrawals, err := storage.GetWithdrawals(ctx, user.ID)
		require.NoError(t, err)
		assert.Len(t, withdrawals, successes)
		for _, withdrawal := range withdrawals {
			assert.Equal(t, "222", withdrawal.Order)
			assert.Equal(t, "0.70", withdrawal.Sum.String())
		}
	})

	t.Run("parallel accrual", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		const workers = 16
		start := make(chan struct{})
		results := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Go(func() {
				<-start
				results <- storage.UpdateOrder(ctx, "111", models.Processed, "12.34567890123456789")
			})
		}
		close(start)
		group.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		require.NoError(t, storage.UpdateOrder(ctx, "111", models.Invalid, "0"))
		balance, err := storage.GetBalance(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "12.34567890123456789", balance.Current.String())
		orders := readOrders(t, storage.GetOrders(ctx, user.ID))
		require.Len(t, orders, 1)
		assert.Equal(t, models.Processed, orders[0].Status)
		assert.Equal(t, "12.34567890123456789", orders[0].Accrual.String())
	})

	t.Run("withdrawal insert rollback", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		require.NoError(t, storage.UpdateOrder(ctx, "111", models.Processed, "10.25"))
		_, err := pool.Exec(ctx, `CREATE FUNCTION reject_withdrawal() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'withdrawal rejected' USING ERRCODE = '23514'; END $$;
			CREATE TRIGGER reject_withdrawal BEFORE INSERT ON withdrawals
			FOR EACH ROW EXECUTE FUNCTION reject_withdrawal()`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_withdrawal ON withdrawals; DROP FUNCTION IF EXISTS reject_withdrawal()")
			assert.NoError(t, err)
		})
		err = storage.Withdraw(ctx, user.ID, models.Withdrawal{Order: "222", Sum: "1.5"})
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, "23514", pgErr.Code)
		balance, err := storage.GetBalance(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "10.25", balance.Current.String())
		assert.True(t, balance.Withdrawn.IsZero())
		withdrawals, err := storage.GetWithdrawals(ctx, user.ID)
		require.NoError(t, err)
		assert.Empty(t, withdrawals)
	})

	t.Run("credit update rollback", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		require.NoError(t, storage.UpdateOrder(ctx, "111", models.Processing, "0"))
		_, err := pool.Exec(ctx, `CREATE FUNCTION reject_credit() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF NEW.balance > OLD.balance THEN
			RAISE EXCEPTION 'credit rejected' USING ERRCODE = '23514'; END IF; RETURN NEW; END $$;
			CREATE TRIGGER reject_credit BEFORE UPDATE ON users
			FOR EACH ROW EXECUTE FUNCTION reject_credit()`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_credit ON users; DROP FUNCTION IF EXISTS reject_credit()")
			assert.NoError(t, err)
		})
		err = storage.UpdateOrder(ctx, "111", models.Processed, "5.25")
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, "23514", pgErr.Code)
		balance, err := storage.GetBalance(ctx, user.ID)
		require.NoError(t, err)
		assert.True(t, balance.Current.IsZero())
		orders := readOrders(t, storage.GetOrders(ctx, user.ID))
		require.Len(t, orders, 1)
		assert.Equal(t, models.Processing, orders[0].Status)
		assert.True(t, orders[0].Accrual.IsZero())
	})

	t.Run("order lock cancellation", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		locker, err := pool.Begin(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = locker.Rollback(context.Background()) })
		var number string
		require.NoError(t, locker.QueryRow(ctx, "SELECT number FROM orders WHERE number = $1 FOR UPDATE", "111").Scan(&number))

		config, err := pgxpool.ParseConfig(dsn)
		require.NoError(t, err)
		config.MaxConns = 1
		config.ConnConfig.RuntimeParams["application_name"] = "gophermart-lock-test"
		waiterPool, err := pgxpool.NewWithConfig(ctx, config)
		require.NoError(t, err)
		t.Cleanup(waiterPool.Close)
		waiter := NewDBStorage(waiterPool)
		waitCtx, stop := context.WithCancel(ctx)
		defer stop()
		finished := make(chan error, 1)
		go func() {
			finished <- waiter.UpdateOrder(waitCtx, "111", models.Processed, "5.25")
		}()
		require.Eventually(t, func() bool {
			var waiting bool
			err := pool.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity WHERE application_name = 'gophermart-lock-test'
				AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%')`).Scan(&waiting)
			return err == nil && waiting
		}, 5*time.Second, 10*time.Millisecond)
		stop()
		select {
		case err := <-finished:
			assert.ErrorIs(t, err, context.Canceled)
		case <-ctx.Done():
			t.Fatal("blocked order update did not stop")
		}
		require.NoError(t, locker.Rollback(ctx))
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		require.NoError(t, waiterPool.Ping(pingCtx))
		orders := readOrders(t, storage.GetOrders(ctx, user.ID))
		require.Len(t, orders, 1)
		assert.Equal(t, models.New, orders[0].Status)
		require.NoError(t, waiter.UpdateOrder(ctx, "111", models.Processed, "5.25"))
		balance, err := storage.GetBalance(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "5.25", balance.Current.String())
	})

	t.Run("pending batches", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		_, err := pool.Exec(ctx, `INSERT INTO orders (number_hash, number, user_id, status, uploaded_at)
			SELECT sha256(convert_to(number, 'UTF8')), number, $1, status, uploaded_at
			FROM (VALUES
			('10', 'NEW', '2026-01-01T00:00:00Z'::timestamptz),
			('20', 'PROCESSING', '2026-01-01T00:00:00Z'::timestamptz),
			('30', 'NEW', '2026-01-01T00:00:00Z'::timestamptz),
			('40', 'INVALID', '2026-01-02T00:00:00Z'::timestamptz),
			('50', 'PROCESSED', '2026-01-02T00:00:00Z'::timestamptz),
			('60', 'NEW', '2026-01-02T00:00:00Z'::timestamptz)
			) AS pending(number, status, uploaded_at)`, user.ID)
		require.NoError(t, err)
		first := readOrders(t, storage.GetPendingOrders(ctx, models.Order{}, 2))
		require.Len(t, first, 2)
		assert.Equal(t, "10", first[0].Number)
		assert.Equal(t, "20", first[1].Number)
		second := readOrders(t, storage.GetPendingOrders(ctx, first[1], 2))
		require.Len(t, second, 2)
		assert.Equal(t, "30", second[0].Number)
		assert.Equal(t, "60", second[1].Number)
		assert.Empty(t, readOrders(t, storage.GetPendingOrders(ctx, second[1], 2)))
		start := models.Order{UploadedAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}
		assert.Equal(t, "10", readOrders(t, storage.GetPendingOrders(ctx, start, 1))[0].Number)

		_, err = pool.Exec(ctx, `INSERT INTO orders (number_hash, number, user_id, status)
			SELECT sha256(convert_to(n::text, 'UTF8')), n::text, $1, 'NEW'
			FROM generate_series(100, 205) AS n`, user.ID)
		require.NoError(t, err)
		for _, limit := range []int{0, -1, 101} {
			assert.Len(t, readOrders(t, storage.GetPendingOrders(ctx, models.Order{}, limit)), 100)
		}
	})

	t.Run("iterator break releases rows", func(t *testing.T) {
		reset(t)
		user := createUser(t, "alice")
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "111"))
		require.NoError(t, storage.CreateOrder(ctx, user.ID, "222"))
		config, err := pgxpool.ParseConfig(dsn)
		require.NoError(t, err)
		config.MaxConns = 1
		oneConnection, err := pgxpool.NewWithConfig(ctx, config)
		require.NoError(t, err)
		t.Cleanup(oneConnection.Close)
		reader := NewDBStorage(oneConnection)
		for _, err := range reader.GetOrders(ctx, user.ID) {
			require.NoError(t, err)
			break
		}
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		require.NoError(t, oneConnection.Ping(pingCtx))
		for _, err := range reader.GetPendingOrders(ctx, models.Order{}, 2) {
			require.NoError(t, err)
			break
		}
		require.NoError(t, oneConnection.Ping(pingCtx))
		assert.Zero(t, oneConnection.Stat().AcquiredConns())
	})
}
