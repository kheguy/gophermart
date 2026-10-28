// логин заказы и списание баллов
package service

import (
	"context"
	"errors"
	"strings"

	models "github.com/kheguy/gophermart/internal/model"
)

// баланс и история меняются вместе
type Storage interface {
	// занятый логин даёт отдельную ошибку
	CreateUser(ctx context.Context, login, passwordHash string) (models.User, error)
	// в ответе нужен и хеш пароля
	GetUser(ctx context.Context, login string) (models.User, error)
	// повтор от владельца не новый заказ
	CreateOrder(ctx context.Context, userID int64, number string) error
	// сначала недавно загруженные заказы
	GetOrders(ctx context.Context, userID int64) ([]models.Order, error)
	// списанная сумма считается за всё время
	GetBalance(ctx context.Context, userID int64) (models.Balance, error)
	// баланса должно хватить на всю сумму
	Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error
	// последнее списание идёт первым
	GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error)
}

// номер и сумма проверяются до записи
type LoyaltyService struct {
	storage Storage
}

// хранилище передаётся уже готовым
func MakeNewLoyaltyService(s Storage) *LoyaltyService {
	return &LoyaltyService{
		storage: s,
	}
}

// пароль в хранилище попадает только хешем
func (s *LoyaltyService) Register(ctx context.Context, login, password string) (models.User, error) {
	if strings.TrimSpace(login) == "" || password == "" {
		return models.User{}, models.ErrInvalidCredentials
	}

	hash, err := hashPassword(password)

	if err != nil {
		return models.User{}, err
	}

	return s.storage.CreateUser(ctx, login, hash)
}

// неизвестный логин и неверный пароль не различаем
func (s *LoyaltyService) Login(ctx context.Context, login, password string) (models.User, error) {
	if strings.TrimSpace(login) == "" || password == "" {
		return models.User{}, models.ErrInvalidCredentials
	}

	user, err := s.storage.GetUser(ctx, login)

	if errors.Is(err, models.ErrUserNotFound) {
		return models.User{}, models.ErrInvalidCredentials
	}

	if err != nil {
		return models.User{}, err
	}

	if !checkPassword(password, user.PasswordHash) {
		return models.User{}, models.ErrInvalidCredentials
	}

	return user, nil
}

// неверный по Луну номер не сохраняем
func (s *LoyaltyService) UploadOrder(ctx context.Context, userID int64, number string) error {
	if !models.ValidOrder(number) {
		return models.ErrInvalidOrder
	}

	return s.storage.CreateOrder(ctx, userID, number)
}

// заказы без начисления тоже видны
func (s *LoyaltyService) GetOrders(ctx context.Context, userID int64) ([]models.Order, error) {
	return s.storage.GetOrders(ctx, userID)
}

// остаток не включает уже списанные баллы
func (s *LoyaltyService) GetBalance(ctx context.Context, userID int64) (models.Balance, error) {
	return s.storage.GetBalance(ctx, userID)
}

// списываем только положительную сумму
func (s *LoyaltyService) Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error {
	if !models.ValidOrder(withdrawal.Order) {
		return models.ErrInvalidOrder
	}

	if !withdrawal.Sum.IsPositive() {
		return models.ErrInvalidAmount
	}

	return s.storage.Withdraw(ctx, userID, withdrawal)
}

// здесь только списания этого пользователя
func (s *LoyaltyService) GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	return s.storage.GetWithdrawals(ctx, userID)
}
