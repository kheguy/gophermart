package service

import (
	"context"
	"testing"

	models "github.com/kheguy/gophermart/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type storageMock struct {
	mock.Mock
}

func (s *storageMock) CreateUser(ctx context.Context, login, hash string) (models.User, error) {
	args := s.Called(ctx, login, hash)
	return args.Get(0).(models.User), args.Error(1)
}

func (s *storageMock) GetUser(ctx context.Context, login string) (models.User, error) {
	args := s.Called(ctx, login)
	return args.Get(0).(models.User), args.Error(1)
}

func (s *storageMock) CreateOrder(ctx context.Context, userID int64, number string) error {
	return s.Called(ctx, userID, number).Error(0)
}

func (s *storageMock) GetOrders(ctx context.Context, userID int64) ([]models.Order, error) {
	args := s.Called(ctx, userID)
	return args.Get(0).([]models.Order), args.Error(1)
}

func (s *storageMock) GetBalance(ctx context.Context, userID int64) (models.Balance, error) {
	args := s.Called(ctx, userID)
	return args.Get(0).(models.Balance), args.Error(1)
}

func (s *storageMock) Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error {
	return s.Called(ctx, userID, withdrawal).Error(0)
}

func (s *storageMock) GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	args := s.Called(ctx, userID)
	return args.Get(0).([]models.Withdrawal), args.Error(1)
}

func TestRegister(t *testing.T) {
	ctx := context.Background()
	storage := &storageMock{}
	user := models.User{ID: 1, Login: "gopher"}
	storage.On("CreateUser", ctx, "gopher", mock.MatchedBy(func(hash string) bool {
		return hash != "secret" && checkPassword("secret", hash)
	})).Return(user, nil).Once()
	svc := MakeNewLoyaltyService(storage)

	result, err := svc.Register(ctx, "gopher", "secret")

	require.NoError(t, err)
	assert.Equal(t, user, result)
	storage.AssertExpectations(t)
}

func TestRegisterStorageError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"login taken", models.ErrLoginTaken},
		{"database error", assert.AnError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			storage := &storageMock{}
			storage.On("CreateUser", ctx, "gopher", mock.Anything).Return(models.User{}, tt.err).Once()
			svc := MakeNewLoyaltyService(storage)

			result, err := svc.Register(ctx, "gopher", "secret")

			assert.ErrorIs(t, err, tt.err)
			assert.Empty(t, result)
			storage.AssertExpectations(t)
		})
	}
}

func TestRegisterInvalidCredentials(t *testing.T) {
	for _, credentials := range []models.Credentials{{}, {Login: " ", Password: "secret"}, {Login: "gopher"}} {
		storage := &storageMock{}
		_, err := MakeNewLoyaltyService(storage).Register(context.Background(), credentials.Login, credentials.Password)
		assert.ErrorIs(t, err, models.ErrInvalidCredentials)
		storage.AssertNotCalled(t, "CreateUser")
	}
}

func TestLogin(t *testing.T) {
	hash, err := hashPassword("secret")
	require.NoError(t, err)
	user := models.User{ID: 1, Login: "gopher", PasswordHash: hash}
	ctx := context.Background()
	tests := []struct {
		name       string
		password   string
		storageErr error
		wantErr    error
	}{
		{"valid", "secret", nil, nil},
		{"wrong password", "wrong", nil, models.ErrInvalidCredentials},
		{"unknown login", "secret", models.ErrUserNotFound, models.ErrInvalidCredentials},
		{"database error", "secret", assert.AnError, assert.AnError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := &storageMock{}
			storage.On("GetUser", ctx, "gopher").Return(user, tt.storageErr).Once()
			result, err := MakeNewLoyaltyService(storage).Login(ctx, "gopher", tt.password)
			assert.ErrorIs(t, err, tt.wantErr)
			if err == nil {
				assert.Equal(t, user, result)
			}
			storage.AssertExpectations(t)
		})
	}
	for _, credentials := range []models.Credentials{{}, {Login: "gopher"}, {Login: " ", Password: "secret"}} {
		_, err := MakeNewLoyaltyService(&storageMock{}).Login(ctx, credentials.Login, credentials.Password)
		assert.ErrorIs(t, err, models.ErrInvalidCredentials)
	}
}

func TestPasswordHash(t *testing.T) {
	first, err := hashPassword("secret")
	require.NoError(t, err)
	second, err := hashPassword("secret")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.True(t, checkPassword("secret", first))
	assert.False(t, checkPassword("wrong", first))
	for _, stored := range []string{"", "secret", "a$b$c$d", "pbkdf2-sha256$1$a$b", "pbkdf2-sha256$120000$xx$00", "pbkdf2-sha256$120000$00000000000000000000000000000000$xx", "pbkdf2-sha256$120000$00000000000000000000000000000000$00"} {
		assert.False(t, checkPassword("secret", stored))
	}
}

func TestUploadOrder(t *testing.T) {
	ctx := context.Background()
	for _, storageErr := range []error{nil, models.ErrOrderExists, models.ErrOrderConflict, assert.AnError} {
		storage := &storageMock{}
		storage.On("CreateOrder", ctx, int64(1), "12345678903").Return(storageErr).Once()
		err := MakeNewLoyaltyService(storage).UploadOrder(ctx, 1, "12345678903")
		assert.ErrorIs(t, err, storageErr)
		storage.AssertExpectations(t)
	}
	err := MakeNewLoyaltyService(&storageMock{}).UploadOrder(ctx, 1, "123")
	assert.ErrorIs(t, err, models.ErrInvalidOrder)
}

func TestWithdraw(t *testing.T) {
	ctx := context.Background()
	withdrawal := models.Withdrawal{Order: "2377225624", Sum: "123.45"}
	for _, storageErr := range []error{nil, models.ErrInsufficientFunds, assert.AnError} {
		storage := &storageMock{}
		storage.On("Withdraw", ctx, int64(1), withdrawal).Return(storageErr).Once()
		err := MakeNewLoyaltyService(storage).Withdraw(ctx, 1, withdrawal)
		assert.ErrorIs(t, err, storageErr)
		storage.AssertExpectations(t)
	}
	for _, withdrawal := range []models.Withdrawal{{Order: "123", Sum: "1.00"}, {Order: "2377225624"}, {Order: "2377225624", Sum: "-0.01"}} {
		err := MakeNewLoyaltyService(&storageMock{}).Withdraw(ctx, 1, withdrawal)
		require.Error(t, err)
	}
}

func TestGetUserData(t *testing.T) {
	ctx := context.Background()
	orders := []models.Order{{Number: "12345678903", Status: models.New}}
	balance := models.Balance{Current: "1.00", Withdrawn: "0.20"}
	withdrawals := []models.Withdrawal{{Order: "2377225624", Sum: "0.20"}}
	for _, storageErr := range []error{nil, assert.AnError} {
		storage := &storageMock{}
		storage.On("GetOrders", ctx, int64(1)).Return(orders, storageErr).Once()
		storage.On("GetBalance", ctx, int64(1)).Return(balance, storageErr).Once()
		storage.On("GetWithdrawals", ctx, int64(1)).Return(withdrawals, storageErr).Once()
		svc := MakeNewLoyaltyService(storage)
		gotOrders, err := svc.GetOrders(ctx, 1)
		assert.Equal(t, orders, gotOrders)
		assert.ErrorIs(t, err, storageErr)
		gotBalance, err := svc.GetBalance(ctx, 1)
		assert.Equal(t, balance, gotBalance)
		assert.ErrorIs(t, err, storageErr)
		gotWithdrawals, err := svc.GetWithdrawals(ctx, 1)
		assert.Equal(t, withdrawals, gotWithdrawals)
		assert.ErrorIs(t, err, storageErr)
		storage.AssertExpectations(t)
	}
}
