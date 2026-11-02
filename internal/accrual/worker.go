package accrual

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
	"golang.org/x/sync/errgroup"
)

const batchSize = 100

// очередь заказов и запись расчёта
type Storage interface {
	// только заказы с незавершённым расчётом
	GetPendingOrders(context.Context, models.Order, int) iter.Seq2[models.Order, error]
	// окончательный расчёт начисляем один раз
	UpdateOrder(context.Context, string, string, models.Money) error
}

// внешний расчёт и его ошибки
type Client interface {
	// результат расчёта либо причина ожидания
	GetOrder(context.Context, string) (Result, error)
}

// дожидается расчёта по загруженным заказам
type Worker struct {
	storage     Storage
	client      Client
	interval    time.Duration
	after       models.Order
	parallelism int
}

// между обходами ждём секунду
func NewWorker(storage Storage, client Client) *Worker {
	return &Worker{storage: storage, client: client, interval: time.Second, parallelism: 4}
}

// крутим до отмены контекста
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		delay := w.poll(ctx)
		timer := time.NewTimer(delay)

		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (w *Worker) poll(ctx context.Context) time.Duration {
	orders := make([]models.Order, 0, batchSize)
	for order, err := range w.storage.GetPendingOrders(ctx, w.after, batchSize) {
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("Pending orders read error", "error", err)
			}
			return w.interval
		}
		orders = append(orders, order)
		if len(orders) == batchSize {
			break
		}
	}
	if ctx.Err() != nil {
		return w.interval
	}
	if len(orders) == 0 {
		w.after = models.Order{}
		return w.interval
	}

	// закрываем rows перед параллельной записью в БД
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(w.parallelism)
	results := make([]pollResult, len(orders))
	for index, order := range orders {
		if groupCtx.Err() != nil {
			break
		}
		group.Go(func() error {
			if groupCtx.Err() != nil {
				return nil
			}
			// лимит не отменяет уже начатые запросы
			err := w.pollOrder(ctx, order)
			var limit *RateLimitError
			if errors.As(err, &limit) {
				results[index].retryAfter = limit.RetryAfter
				results[index].limited = true
			} else {
				results[index].completed = err == nil
			}
			return err
		})
	}
	_ = group.Wait()
	if ctx.Err() != nil {
		return w.interval
	}

	delay := time.Duration(0)
	limited := false
	for _, result := range results {
		if result.limited {
			limited = true
			if result.retryAfter > delay {
				delay = result.retryAfter
			}
		}
	}
	if limited {
		w.parallelism = 1
	} else {
		w.parallelism = 4
	}
	for index, result := range results {
		if !result.completed {
			break
		}
		w.after = orders[index]
	}
	if !limited && len(orders) < batchSize {
		w.after = models.Order{}
	}
	if delay > 0 {
		return delay
	}
	return w.interval
}

type pollResult struct {
	completed  bool
	limited    bool
	retryAfter time.Duration
}

func (w *Worker) pollOrder(ctx context.Context, order models.Order) error {
	result, err := w.client.GetOrder(ctx, order.Number)
	if err != nil {
		var limit *RateLimitError
		if errors.As(err, &limit) {
			return err
		}
		if errors.Is(err, ErrNotRegistered) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Error("Accrual request error", "order", order.Number, "error", err)
		return nil
	}

	status := result.Status
	amount := models.Money("")
	switch status {
	case models.Registered:
		status = models.Processing
	case models.Processed:
		amount = result.Accrual
	case models.Processing, models.Invalid:
	default:
		slog.Error("Unknown accrual status", "order", order.Number, "status", status)
		return nil
	}
	if status == order.Status {
		return nil
	}

	err = w.storage.UpdateOrder(ctx, order.Number, status, amount)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Error("Order update error", "order", order.Number, "error", err)
	}
	return nil
}
