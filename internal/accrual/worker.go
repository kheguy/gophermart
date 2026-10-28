package accrual

import (
	"context"
	"errors"
	"log"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
)

// очередь заказов и запись расчёта
type Storage interface {
	// только заказы с незавершённым расчётом
	GetPendingOrders(context.Context) ([]models.Order, error)
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
	storage  Storage
	client   Client
	interval time.Duration
	next     string
}

// между обходами ждём секунду
func NewWorker(storage Storage, client Client) *Worker {
	return &Worker{storage: storage, client: client, interval: time.Second}
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
	orders, err := w.storage.GetPendingOrders(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Pending orders read error: %v", err)
		}
		return w.interval
	}

	if len(orders) == 0 {
		return w.interval
	}

	start := 0
	for index, order := range orders {
		if order.Number == w.next {
			start = index
			break
		}
	}

	for offset := range orders {
		if ctx.Err() != nil {
			return w.interval
		}

		index := (start + offset) % len(orders)
		order := orders[index]
		w.next = orders[(index+1)%len(orders)].Number

		result, err := w.client.GetOrder(ctx, order.Number)
		if err != nil {
			var limit *RateLimitError
			if errors.As(err, &limit) {
				w.next = order.Number
				if limit.RetryAfter > 0 {
					return limit.RetryAfter
				}

				return w.interval
			}

			if !errors.Is(err, ErrNotRegistered) && ctx.Err() == nil {
				log.Printf("Order %s accrual request error: %v", order.Number, err)
			}
			continue
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
			log.Printf("Order %s has unknown accrual status %q", order.Number, status)
			continue
		}

		if status == order.Status {
			continue
		}

		err = w.storage.UpdateOrder(ctx, order.Number, status, amount)
		if err != nil && ctx.Err() == nil {
			log.Printf("Order %s update error: %v", order.Number, err)
		}
	}

	return w.interval
}
