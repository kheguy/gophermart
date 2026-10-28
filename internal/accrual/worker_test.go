package accrual

import (
	"context"
	"errors"
	"testing"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderUpdate struct {
	number string
	status string
	amount models.Money
}

type testStorage struct {
	orders    []models.Order
	updates   []orderUpdate
	readErr   error
	updateErr error
}

func (s *testStorage) GetPendingOrders(context.Context) ([]models.Order, error) {
	return s.orders, s.readErr
}

func (s *testStorage) UpdateOrder(_ context.Context, number, status string, amount models.Money) error {
	s.updates = append(s.updates, orderUpdate{number: number, status: status, amount: amount})
	return s.updateErr
}

type testClient func(context.Context, string) (Result, error)

func (c testClient) GetOrder(ctx context.Context, number string) (Result, error) {
	return c(ctx, number)
}

func TestWorker_Poll(t *testing.T) {
	tests := []struct {
		name   string
		status string
		result Result
		err    error
		update *orderUpdate
	}{
		{
			name: "registered", status: models.New,
			result: Result{Status: models.Registered},
			update: &orderUpdate{number: "123", status: models.Processing},
		},
		{
			name: "processing", status: models.New,
			result: Result{Status: models.Processing},
			update: &orderUpdate{number: "123", status: models.Processing},
		},
		{
			name: "processed", status: models.Processing,
			result: Result{Status: models.Processed, Accrual: "120.25"},
			update: &orderUpdate{number: "123", status: models.Processed, amount: "120.25"},
		},
		{
			name: "invalid", status: models.New,
			result: Result{Status: models.Invalid, Accrual: "1.00"},
			update: &orderUpdate{number: "123", status: models.Invalid},
		},
		{name: "unchanged", status: models.Processing, result: Result{Status: models.Processing}},
		{name: "not registered", status: models.New, err: ErrNotRegistered},
		{name: "server error", status: models.New, err: errors.New("server error")},
		{name: "unknown status", status: models.New, result: Result{Status: "UNKNOWN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := &testStorage{orders: []models.Order{{Number: "123", Status: tt.status}}}
			client := testClient(func(ctx context.Context, number string) (Result, error) {
				assert.Equal(t, "123", number)
				return tt.result, tt.err
			})
			worker := NewWorker(storage, client)
			assert.Equal(t, time.Second, worker.poll(context.Background()))
			if tt.update == nil {
				assert.Empty(t, storage.updates)
			} else {
				assert.Equal(t, []orderUpdate{*tt.update}, storage.updates)
			}
		})
	}
}

func TestWorker_RateLimitDoesNotSkipOrder(t *testing.T) {
	storage := &testStorage{orders: []models.Order{{Number: "1"}, {Number: "2"}, {Number: "3"}}}
	var calls []string
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		calls = append(calls, number)
		return Result{}, &RateLimitError{RetryAfter: time.Minute}
	}))

	for range 3 {
		assert.Equal(t, time.Minute, worker.poll(context.Background()))
	}
	assert.Equal(t, []string{"1", "1", "1"}, calls)
	assert.Empty(t, storage.updates)
}

func TestWorker_RateLimitFairness(t *testing.T) {
	storage := &testStorage{orders: []models.Order{
		{Number: "1", Status: models.Processing},
		{Number: "2", Status: models.Processing},
		{Number: "3", Status: models.Processing},
	}}
	var calls, accepted []string
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		calls = append(calls, number)
		if len(calls)%2 == 0 {
			return Result{}, &RateLimitError{RetryAfter: time.Minute}
		}
		accepted = append(accepted, number)
		return Result{Status: models.Processing}, nil
	}))

	for range 3 {
		assert.Equal(t, time.Minute, worker.poll(context.Background()))
	}
	assert.Equal(t, []string{"1", "2", "2", "3", "3", "1"}, calls)
	assert.Equal(t, []string{"1", "2", "3"}, accepted)
}

func TestWorker_RateLimitWithoutDelay(t *testing.T) {
	storage := &testStorage{orders: []models.Order{{Number: "1"}}}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		return Result{}, &RateLimitError{}
	}))
	assert.Equal(t, time.Second, worker.poll(context.Background()))
}

func TestWorker_UpdateError(t *testing.T) {
	storage := &testStorage{
		orders:    []models.Order{{Number: "123", Status: models.New}},
		updateErr: errors.New("database is unavailable"),
	}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		return Result{Status: models.Processed, Accrual: "1.00"}, nil
	}))
	worker.poll(context.Background())
	storage.updateErr = nil
	worker.poll(context.Background())
	assert.Equal(t, []orderUpdate{
		{number: "123", status: models.Processed, amount: "1.00"},
		{number: "123", status: models.Processed, amount: "1.00"},
	}, storage.updates)
}

func TestWorker_ContinueAfterRequestError(t *testing.T) {
	storage := &testStorage{orders: []models.Order{{Number: "1", Status: models.New}, {Number: "2", Status: models.New}}}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		if number == "1" {
			return Result{}, errors.New("request failed")
		}
		return Result{Status: models.Processed, Accrual: "2.00"}, nil
	}))
	worker.poll(context.Background())
	assert.Equal(t, []orderUpdate{{number: "2", status: models.Processed, amount: "2.00"}}, storage.updates)
}

func TestWorker_EmptyOrUnavailableStorage(t *testing.T) {
	for _, err := range []error{nil, errors.New("database is unavailable")} {
		storage := &testStorage{readErr: err}
		worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
			t.Error("client must not be called without pending orders")
			return Result{}, nil
		}))
		assert.Equal(t, time.Second, worker.poll(context.Background()))
	}
}

func TestWorker_Run(t *testing.T) {
	storage := &testStorage{orders: []models.Order{{Number: "123", Status: models.New}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requested := make(chan struct{})
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		close(requested)
		return Result{}, &RateLimitError{RetryAfter: time.Hour}
	}))
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("worker did not request pending order on startup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel rate limit wait")
	}
}

func TestWorker_RunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	storage := &testStorage{orders: []models.Order{{Number: "123"}}}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		t.Error("client must not be called after cancellation")
		return Result{}, nil
	}))
	worker.Run(ctx)
	require.Empty(t, storage.updates)
	assert.Equal(t, time.Second, worker.poll(ctx))
}

func TestWorker_CancelDuringRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storage := &testStorage{orders: []models.Order{{Number: "1"}, {Number: "2"}}}
	var calls []string
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		calls = append(calls, number)
		cancel()
		return Result{}, ctx.Err()
	}))
	worker.Run(ctx)
	assert.Equal(t, []string{"1"}, calls)
	assert.Empty(t, storage.updates)
}
