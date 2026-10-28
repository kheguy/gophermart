// данные системы лояльности в PostgreSQL
package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	models "github.com/kheguy/gophermart/internal/model"
)

// нужные хранилищу операции PostgreSQL
type Pool interface {
	// запрос без чтения результата
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	// строки нужно закрыть после чтения
	Query(context.Context, string, ...any) (pgx.Rows, error)
	// ошибка запроса приходит при чтении
	QueryRow(context.Context, string, ...any) pgx.Row
	// транзакция с настройками базы
	Begin(context.Context) (pgx.Tx, error)
}

// заказы и счета пользователей в PostgreSQL
type DBStorage struct {
	pool Pool
}

// пул остаётся у вызывающего кода
func NewDBStorage(p Pool) *DBStorage {
	return &DBStorage{
		pool: p,
	}
}

// общий ключ сессий переживает перезапуск
func (s *DBStorage) GetAuthKey(ctx context.Context) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	_, err := s.pool.Exec(ctx,
		"INSERT INTO auth_keys (singleton, secret) VALUES (TRUE, $1) ON CONFLICT (singleton) DO NOTHING",
		key,
	)
	if err != nil {
		return nil, err
	}

	err = s.pool.QueryRow(ctx, "SELECT secret FROM auth_keys WHERE singleton = TRUE").Scan(&key)
	return key, err
}

// занятый логин возвращает отдельную ошибку
func (s *DBStorage) CreateUser(ctx context.Context, login, passwordHash string) (models.User, error) {
	var user models.User
	err := s.pool.QueryRow(
		ctx,
		`INSERT INTO users (login, password_hash) VALUES ($1, $2)
		 RETURNING id, login, password_hash`,
		login,
		passwordHash,
	).Scan(&user.ID, &user.Login, &user.PasswordHash)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return models.User{}, models.ErrLoginTaken
	}

	return user, err
}

// пользователь с хешем для проверки пароля
func (s *DBStorage) GetUser(ctx context.Context, login string) (models.User, error) {
	var user models.User
	err := s.pool.QueryRow(
		ctx,
		"SELECT id, login, password_hash FROM users WHERE login = $1",
		login,
	).Scan(&user.ID, &user.Login, &user.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, models.ErrUserNotFound
	}

	return user, err
}

// повтор отличаем от конфликта владельца
func (s *DBStorage) CreateOrder(ctx context.Context, userID int64, number string) error {
	hash := sha256.Sum256([]byte(number))
	result, err := s.pool.Exec(
		ctx,
		`INSERT INTO orders (number_hash, number, user_id, status) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (number_hash) DO NOTHING`,
		hash[:],
		number,
		userID,
		models.New,
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() > 0 {
		return nil
	}

	var ownerID int64
	var storedNumber string
	if err := s.pool.QueryRow(ctx, "SELECT number, user_id FROM orders WHERE number_hash = $1", hash[:]).Scan(&storedNumber, &ownerID); err != nil {
		return err
	}
	if storedNumber != number {
		return errors.New("order number hash collision")
	}

	if ownerID == userID {
		return models.ErrOrderExists
	}

	return models.ErrOrderConflict
}

// новые заказы идут первыми
func (s *DBStorage) GetOrders(ctx context.Context, userID int64) ([]models.Order, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT number, user_id, status, accrual::text, uploaded_at FROM orders
		 WHERE user_id = $1 ORDER BY uploaded_at DESC, number DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanOrders(rows)
}

// остаток и сумма всех списаний
func (s *DBStorage) GetBalance(ctx context.Context, userID int64) (models.Balance, error) {
	var balance models.Balance
	err := s.pool.QueryRow(
		ctx,
		"SELECT balance::text, withdrawn::text FROM users WHERE id = $1",
		userID,
	).Scan(&balance.Current, &balance.Withdrawn)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Balance{}, models.ErrUserNotFound
	}

	return balance, err
}

// баланс и списание меняются одной транзакцией
func (s *DBStorage) Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error {
	if !withdrawal.Sum.IsPositive() {
		return models.ErrInvalidAmount
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	result, err := tx.Exec(
		ctx,
		`UPDATE users SET balance = balance - $2, withdrawn = withdrawn + $2
		 WHERE id = $1 AND balance >= $2`,
		userID,
		withdrawal.Sum.String(),
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return models.ErrInsufficientFunds
	}

	_, err = tx.Exec(
		ctx,
		"INSERT INTO withdrawals (user_id, order_number, amount) VALUES ($1, $2, $3)",
		userID,
		withdrawal.Order,
		withdrawal.Sum.String(),
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// последние списания идут первыми
func (s *DBStorage) GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT order_number, amount::text, processed_at FROM withdrawals
		 WHERE user_id = $1 ORDER BY processed_at DESC, id DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var withdrawals []models.Withdrawal
	for rows.Next() {
		var withdrawal models.Withdrawal

		if err := rows.Scan(&withdrawal.Order, &withdrawal.Sum, &withdrawal.ProcessedAt); err != nil {
			return nil, err
		}
		withdrawals = append(withdrawals, withdrawal)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return withdrawals, nil
}

// заказы ещё без окончательного расчёта
func (s *DBStorage) GetPendingOrders(ctx context.Context) ([]models.Order, error) {
	rows, err := s.pool.Query(
		ctx,
		`SELECT number, user_id, status, accrual::text, uploaded_at FROM orders
		 WHERE status IN ($1, $2) ORDER BY uploaded_at, number`,
		models.New,
		models.Processing,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanOrders(rows)
}

// блокировка заказа защищает от двойного начисления
func (s *DBStorage) UpdateOrder(ctx context.Context, number, status string, accrual models.Money) error {
	switch status {
	case models.New, models.Processing, models.Invalid, models.Processed:
	default:
		return models.ErrInvalidOrder
	}

	if accrual.IsNegative() {
		return models.ErrInvalidAmount
	}

	if status != models.Processed {
		accrual = "0"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var userID int64
	var currentStatus string
	hash := sha256.Sum256([]byte(number))

	if err := tx.QueryRow(
		ctx,
		"SELECT user_id, status FROM orders WHERE number_hash = $1 AND number = $2 FOR UPDATE",
		hash[:],
		number,
	).Scan(&userID, &currentStatus); err != nil {
		return err
	}

	if currentStatus == models.Processed || currentStatus == models.Invalid {
		return nil
	}

	if _, err := tx.Exec(
		ctx,
		"UPDATE orders SET status = $3, accrual = $4 WHERE number_hash = $1 AND number = $2",
		hash[:],
		number,
		status,
		accrual.String(),
	); err != nil {
		return err
	}

	if status == models.Processed && accrual.IsPositive() {
		if _, err := tx.Exec(
			ctx,
			"UPDATE users SET balance = balance + $2 WHERE id = $1",
			userID,
			accrual.String(),
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func scanOrders(rows pgx.Rows) ([]models.Order, error) {
	var orders []models.Order
	for rows.Next() {
		var order models.Order

		if err := rows.Scan(&order.Number, &order.UserID, &order.Status, &order.Accrual, &order.UploadedAt); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return orders, nil
}
