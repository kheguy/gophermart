// идём за начислениями
package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
)

// заказа пока нет во внешнем сервисе
var ErrNotRegistered = errors.New("order is not registered in accrual system")

// ответ внешнего расчёта
type Result struct {
	Order   string       `json:"order"`
	Status  string       `json:"status"`
	Accrual models.Money `json:"accrual"`
}

// внешний сервис просит подождать
type RateLimitError struct {
	// пауза до следующей попытки
	RetryAfter time.Duration
}

// текст ошибки с длительностью паузы
func (e *RateLimitError) Error() string {
	return fmt.Sprintf("accrual request limit exceeded, retry after %s", e.RetryAfter)
}

// клиент расчёта с таймаутом
type HTTPClient struct {
	address string
	client  *http.Client
}

// на запрос даём пять секунд
func NewHTTPClient(address string) *HTTPClient {
	return &HTTPClient{
		address: strings.TrimRight(address, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// начисления с проверкой номера и статуса
func (c *HTTPClient) GetOrder(ctx context.Context, number string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.address+"/api/orders/"+url.PathEscape(number), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")

	res, err := c.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusNoContent:
		return Result{}, ErrNotRegistered
	case http.StatusTooManyRequests:
		return Result{}, &RateLimitError{RetryAfter: retryAfter(res.Header.Get("Retry-After"), time.Now())}
	case http.StatusOK:
	default:
		return Result{}, fmt.Errorf("accrual system returned status %s", res.Status)
	}

	const maxResponseSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseSize+1))
	if err != nil {
		return Result{}, err
	}
	if len(body) > maxResponseSize {
		return Result{}, errors.New("accrual response is too large")
	}
	var result Result
	if err := json.Unmarshal(body, &result); err != nil {
		return Result{}, fmt.Errorf("decode accrual response: %w", err)
	}

	if result.Order != number {
		return Result{}, errors.New("accrual response contains another order")
	}
	if result.Accrual.IsNegative() {
		return Result{}, errors.New("accrual cannot be negative")
	}

	switch result.Status {
	case models.Registered, models.Processing, models.Invalid, models.Processed:
		return result, nil
	default:
		return Result{}, fmt.Errorf("unknown accrual status %q", result.Status)
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}

	date, err := http.ParseTime(value)
	if err == nil {
		if delay := date.Sub(now); delay > 0 {
			return delay
		}
	}
	// скип
	return time.Second
}
