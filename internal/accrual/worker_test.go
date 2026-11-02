package accrual

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"sync"
	"sync/atomic"
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

type pendingRead struct {
	after models.Order
	limit int
}

type testStorage struct {
	mu           sync.Mutex
	orders       []models.Order
	updates      []orderUpdate
	reads        []pendingRead
	readErr      error
	readErrAfter int
	updateErr    error
	ignoreLimit  bool
	iteratorOpen atomic.Bool
	yielded      int
}

func (s *testStorage) GetPendingOrders(ctx context.Context, after models.Order, limit int) iter.Seq2[models.Order, error] {
	return func(yield func(models.Order, error) bool) {
		s.reads = append(s.reads, pendingRead{after: after, limit: limit})
		s.iteratorOpen.Store(true)
		defer s.iteratorOpen.Store(false)

		count := 0
		for _, order := range s.orders {
			if err := ctx.Err(); err != nil {
				yield(models.Order{}, err)
				return
			}
			if after.Number != "" && (order.UploadedAt.Before(after.UploadedAt) ||
				(order.UploadedAt.Equal(after.UploadedAt) && order.Number <= after.Number)) {
				continue
			}
			if s.readErr != nil && count == s.readErrAfter {
				yield(models.Order{}, s.readErr)
				return
			}
			if !s.ignoreLimit && count == limit {
				return
			}
			count++
			s.yielded++
			if !yield(order, nil) {
				return
			}
		}
		if s.readErr != nil {
			yield(models.Order{}, s.readErr)
		}
	}
}

func (s *testStorage) UpdateOrder(ctx context.Context, number, status string, amount models.Money) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, orderUpdate{number: number, status: status, amount: amount})
	return s.updateErr
}

type testClient func(context.Context, string) (Result, error)

func (c testClient) GetOrder(ctx context.Context, number string) (Result, error) {
	return c(ctx, number)
}

func testOrders(count int) []models.Order {
	orders := make([]models.Order, count)
	date := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	for index := range orders {
		orders[index] = models.Order{
			Number: fmt.Sprintf("%04d", index+1), Status: models.New,
			UploadedAt: date.Add(time.Duration(index) * time.Second),
		}
	}
	return orders
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish")
	}
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
			worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
				assert.Equal(t, "123", number)
				return tt.result, tt.err
			}))
			assert.Equal(t, time.Second, worker.poll(context.Background()))
			if tt.update == nil {
				assert.Empty(t, storage.updates)
			} else {
				assert.Equal(t, []orderUpdate{*tt.update}, storage.updates)
			}
		})
	}
}

func TestWorker_BatchLimitAndClosedRows(t *testing.T) {
	storage := &testStorage{orders: testOrders(150), ignoreLimit: true}
	var calls atomic.Int64
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		assert.False(t, storage.iteratorOpen.Load())
		calls.Add(1)
		return Result{Status: models.Processed, Accrual: "1.00"}, nil
	}))
	worker.poll(context.Background())
	assert.EqualValues(t, 100, calls.Load())
	assert.Equal(t, storage.orders[99], worker.after)
	assert.Equal(t, 100, storage.yielded)
	assert.False(t, storage.iteratorOpen.Load())
	assert.Equal(t, []pendingRead{{limit: 100}}, storage.reads)
}

func TestWorker_PaginationReachesTail(t *testing.T) {
	storage := &testStorage{orders: testOrders(205)}
	var mu sync.Mutex
	calls := make(map[string]int)
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		mu.Lock()
		calls[number]++
		mu.Unlock()
		return Result{}, ErrNotRegistered
	}))
	for range 3 {
		worker.poll(context.Background())
	}
	require.Len(t, calls, 205)
	for _, count := range calls {
		assert.Equal(t, 1, count)
	}
	assert.Empty(t, worker.after.Number)
	assert.Equal(t, []pendingRead{
		{limit: 100}, {after: storage.orders[99], limit: 100}, {after: storage.orders[199], limit: 100},
	}, storage.reads)
	worker.poll(context.Background())
	assert.Equal(t, 2, calls["0001"])
	assert.Equal(t, 1, calls["0205"])
}

func TestWorker_RateLimitFairnessWithUnregisteredOrders(t *testing.T) {
	storage := &testStorage{orders: testOrders(107)}
	worker := NewWorker(storage, nil)
	var mu sync.Mutex
	accepted := make(map[string]int)
	for range 36 {
		start := 0
		if worker.after.Number != "" {
			var err error
			start, err = strconv.Atoi(worker.after.Number)
			require.NoError(t, err)
		}
		ready := make(chan struct{})
		var successful atomic.Int64
		worker.client = testClient(func(ctx context.Context, number string) (Result, error) {
			index, err := strconv.Atoi(number)
			assert.NoError(t, err)
			if index <= start+3 {
				mu.Lock()
				accepted[number]++
				mu.Unlock()
				if successful.Add(1) == 3 {
					close(ready)
				}
				return Result{}, ErrNotRegistered
			}
			select {
			case <-ready:
				return Result{}, &RateLimitError{RetryAfter: time.Minute}
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		})
		worker.poll(context.Background())
	}
	require.Len(t, accepted, 107)
	for _, count := range accepted {
		assert.Equal(t, 1, count)
	}
	assert.Empty(t, worker.after.Number)
}

func TestWorker_ConcurrencyLimit(t *testing.T) {
	storage := &testStorage{orders: testOrders(25)}
	started := make(chan struct{}, 25)
	release := make(chan struct{})
	var active, calls atomic.Int64
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		assert.LessOrEqual(t, current, int64(4))
		calls.Add(1)
		started <- struct{}{}
		<-release
		return Result{}, ErrNotRegistered
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.poll(context.Background())
	}()
	for range 4 {
		waitSignal(t, started)
	}
	assert.EqualValues(t, 4, active.Load())
	assert.Empty(t, started)
	close(release)
	waitSignal(t, done)
	assert.EqualValues(t, 25, calls.Load())
	assert.Zero(t, active.Load())
}

func TestWorker_RateLimitKeepsSlowAcceptedRequest(t *testing.T) {
	storage := &testStorage{orders: testOrders(4)}
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	accepted := make(map[string]int)
	var canceled atomic.Int64
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		started <- struct{}{}
		<-release
		if number != "0004" {
			return Result{}, &RateLimitError{RetryAfter: time.Minute}
		}
		select {
		case <-time.After(100 * time.Millisecond):
			mu.Lock()
			accepted[number]++
			mu.Unlock()
			return Result{}, ErrNotRegistered
		case <-ctx.Done():
			canceled.Add(1)
			return Result{}, ctx.Err()
		}
	}))
	var delay time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		delay = worker.poll(context.Background())
	}()
	for range 4 {
		waitSignal(t, started)
	}
	close(release)
	waitSignal(t, done)
	assert.Equal(t, time.Minute, delay)
	assert.Zero(t, canceled.Load())
	assert.Equal(t, map[string]int{"0004": 1}, accepted)
	assert.Empty(t, worker.after.Number)
	assert.Equal(t, 1, worker.parallelism)

	var active atomic.Int64
	for range 4 {
		var calls atomic.Int64
		worker.client = testClient(func(ctx context.Context, number string) (Result, error) {
			current := active.Add(1)
			defer active.Add(-1)
			assert.EqualValues(t, 1, current)
			if calls.Add(1) > 1 {
				return Result{}, &RateLimitError{RetryAfter: time.Minute}
			}
			select {
			case <-time.After(10 * time.Millisecond):
				mu.Lock()
				accepted[number]++
				mu.Unlock()
				return Result{}, ErrNotRegistered
			case <-ctx.Done():
				canceled.Add(1)
				return Result{}, ctx.Err()
			}
		})
		worker.poll(context.Background())
	}
	assert.Equal(t, map[string]int{"0001": 1, "0002": 1, "0003": 1, "0004": 2}, accepted)
	assert.Zero(t, canceled.Load())
	assert.Empty(t, worker.after.Number)
	assert.Equal(t, 4, worker.parallelism)
}

func TestWorker_RateLimitUsesLongestDelay(t *testing.T) {
	storage := &testStorage{orders: testOrders(4)}
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		started <- struct{}{}
		<-release
		index, err := strconv.Atoi(number)
		assert.NoError(t, err)
		return Result{}, &RateLimitError{RetryAfter: time.Duration(index) * time.Minute}
	}))
	var delay time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		delay = worker.poll(context.Background())
	}()
	for range 4 {
		waitSignal(t, started)
	}
	close(release)
	waitSignal(t, done)
	assert.Equal(t, 4*time.Minute, delay)
}

func TestWorker_RateLimitWithoutDelay(t *testing.T) {
	storage := &testStorage{orders: testOrders(1)}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		return Result{}, &RateLimitError{}
	}))
	assert.Equal(t, time.Second, worker.poll(context.Background()))
}

func TestWorker_IndividualErrors(t *testing.T) {
	storage := &testStorage{orders: testOrders(3), updateErr: errors.New("database is unavailable")}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		if number == "0001" {
			return Result{}, errors.New("request failed")
		}
		return Result{Status: models.Processed, Accrual: "2.00"}, nil
	}))
	worker.poll(context.Background())
	assert.ElementsMatch(t, []orderUpdate{
		{number: "0002", status: models.Processed, amount: "2.00"},
		{number: "0003", status: models.Processed, amount: "2.00"},
	}, storage.updates)
	storage.updateErr = nil
	worker.poll(context.Background())
	assert.Len(t, storage.updates, 4)
}

func TestWorker_IteratorErrorDoesNotAdvanceCursor(t *testing.T) {
	storage := &testStorage{
		orders: testOrders(10), readErr: errors.New("connection lost"), readErrAfter: 3,
	}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		t.Error("client must not be called after an incomplete read")
		return Result{}, nil
	}))
	worker.after = storage.orders[1]
	assert.Equal(t, time.Second, worker.poll(context.Background()))
	assert.Equal(t, storage.orders[1], worker.after)
	assert.False(t, storage.iteratorOpen.Load())
}

func TestWorker_EmptyPageResetsCursor(t *testing.T) {
	storage := &testStorage{}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		t.Error("client must not be called without pending orders")
		return Result{}, nil
	}))
	worker.after = testOrders(1)[0]
	assert.Equal(t, time.Second, worker.poll(context.Background()))
	assert.Empty(t, worker.after.Number)
}

func TestWorker_RunCancelsRequests(t *testing.T) {
	storage := &testStorage{orders: testOrders(20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 20)
	var calls, stopped atomic.Int64
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		stopped.Add(1)
		return Result{}, ctx.Err()
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	for range 4 {
		waitSignal(t, started)
	}
	cancel()
	waitSignal(t, done)
	assert.EqualValues(t, 4, calls.Load())
	assert.EqualValues(t, 4, stopped.Load())
	assert.Empty(t, worker.after.Number)
}

func TestWorker_RunCancelsRateLimitWait(t *testing.T) {
	storage := &testStorage{orders: testOrders(1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requested := make(chan struct{})
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		close(requested)
		return Result{}, &RateLimitError{RetryAfter: time.Hour}
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	waitSignal(t, requested)
	cancel()
	waitSignal(t, done)
}

func TestWorker_RunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	storage := &testStorage{orders: testOrders(1)}
	worker := NewWorker(storage, testClient(func(ctx context.Context, number string) (Result, error) {
		t.Error("client must not be called after cancellation")
		return Result{}, nil
	}))
	worker.Run(ctx)
	assert.Empty(t, storage.reads)
	assert.Equal(t, time.Second, worker.poll(ctx))
	assert.Empty(t, storage.updates)
}
