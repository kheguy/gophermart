// ручки для гофермарта
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kheguy/gophermart/internal/middlewares"
	models "github.com/kheguy/gophermart/internal/model"
)

const maxRequestSize = 1 << 20

// действия с аккаунтом и заказами
type Service interface {
	// занятый логин возвращаем ошибкой
	Register(ctx context.Context, login, password string) (models.User, error)
	// пользователь только после проверки пароля
	Login(ctx context.Context, login, password string) (models.User, error)
	// повтор своего заказа отличается от чужого
	UploadOrder(ctx context.Context, userID int64, number string) error
	// сначала последние загруженные
	GetOrders(ctx context.Context, userID int64) ([]models.Order, error)
	// остаток и сколько уже потрачено
	GetBalance(ctx context.Context, userID int64) (models.Balance, error)
	// ошибка если остатка не хватает
	Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error
	// последние списания идут первыми
	GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error)
}

// токены для авторизации запросов
type TokenManager interface {
	// подписанный ID со сроком действия
	CreateToken(userID int64) (string, error)
	// ID после проверки подписи и срока
	ParseToken(token string) (int64, error)
}

// ручки аккаунта и накопительного счёта
type LoyaltyHandler struct {
	service Service
	auth    TokenManager
}

// сервис и токены для ручек
func MakeNewLoyaltyHandler(s Service, auth TokenManager) *LoyaltyHandler {
	return &LoyaltyHandler{
		service: s,
		auth:    auth,
	}
}

// вход открыт остальные маршруты под авторизацией
func NewRouter(s Service, auth TokenManager) http.Handler {
	h := MakeNewLoyaltyHandler(s, auth)

	r := chi.NewRouter()
	r.Use(middlewares.WithGzip)

	r.Post("/api/user/register", h.RegisterHandler)
	r.Post("/api/user/login", h.LoginHandler)

	r.Group(func(r chi.Router) {
		r.Use(middlewares.WithAuth(auth))
		r.Post("/api/user/orders", h.UploadOrderHandler)
		r.Get("/api/user/orders", h.OrdersHandler)
		r.Get("/api/user/balance", h.BalanceHandler)
		r.Post("/api/user/balance/withdraw", h.WithdrawHandler)
		r.Get("/api/user/withdrawals", h.WithdrawalsHandler)
	})

	return r
}

// после регистрации сразу выдаём сессию
func (h *LoyaltyHandler) RegisterHandler(res http.ResponseWriter, req *http.Request) {
	var credentials models.Credentials
	if !readJSON(res, req, &credentials) {
		return
	}

	if strings.TrimSpace(credentials.Login) == "" || credentials.Password == "" {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	user, err := h.service.Register(req.Context(), credentials.Login, credentials.Password)

	if err != nil {
		switch {
		case errors.Is(err, models.ErrLoginTaken):
			http.Error(res, http.StatusText(http.StatusConflict), http.StatusConflict)
		case errors.Is(err, models.ErrInvalidCredentials):
			http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		default:
			http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
	h.authenticate(res, user.ID)
}

// успешный вход обновляет сессию
func (h *LoyaltyHandler) LoginHandler(res http.ResponseWriter, req *http.Request) {
	var credentials models.Credentials
	if !readJSON(res, req, &credentials) {
		return
	}

	if strings.TrimSpace(credentials.Login) == "" || credentials.Password == "" {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	user, err := h.service.Login(req.Context(), credentials.Login, credentials.Password)

	if err != nil {
		if errors.Is(err, models.ErrInvalidCredentials) || errors.Is(err, models.ErrUserNotFound) {
			http.Error(res, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		} else {
			http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
	h.authenticate(res, user.ID)
}

func (h *LoyaltyHandler) authenticate(res http.ResponseWriter, userID int64) {
	token, err := h.auth.CreateToken(userID)

	if err != nil {
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.SetCookie(res, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   24 * 60 * 60,
	})

	res.Header().Set("Authorization", "Bearer "+token)
	res.WriteHeader(http.StatusOK)
}

// новый заказ получает 202 повтор 200
func (h *LoyaltyHandler) UploadOrderHandler(res http.ResponseWriter, req *http.Request) {
	if !hasContentType(req, "text/plain") {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(res, req.Body, maxRequestSize))

	if err != nil {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	number := strings.TrimSpace(string(body))
	if number == "" {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err = h.service.UploadOrder(req.Context(), middlewares.UserID(req.Context()), number)

	switch {
	case err == nil:
		res.WriteHeader(http.StatusAccepted)
	case errors.Is(err, models.ErrOrderExists):
		res.WriteHeader(http.StatusOK)
	case errors.Is(err, models.ErrOrderConflict):
		http.Error(res, http.StatusText(http.StatusConflict), http.StatusConflict)
	case errors.Is(err, models.ErrInvalidOrder):
		http.Error(res, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	default:
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// без заказов отвечаем 204
func (h *LoyaltyHandler) OrdersHandler(res http.ResponseWriter, req *http.Request) {
	orders, err := h.service.GetOrders(req.Context(), middlewares.UserID(req.Context()))

	if err != nil {
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if len(orders) == 0 {
		res.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(res, orders)
}

// доступный остаток и сумма всех списаний
func (h *LoyaltyHandler) BalanceHandler(res http.ResponseWriter, req *http.Request) {
	balance, err := h.service.GetBalance(req.Context(), middlewares.UserID(req.Context()))

	if err != nil {
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	writeJSON(res, balance)
}

// нехватка баллов даёт 402
func (h *LoyaltyHandler) WithdrawHandler(res http.ResponseWriter, req *http.Request) {
	var input struct {
		Order string       `json:"order"`
		Sum   models.Money `json:"sum"`
	}

	if !readJSON(res, req, &input) {
		return
	}

	if !input.Sum.IsPositive() {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	withdrawal := models.Withdrawal{
		Order: input.Order,
		Sum:   input.Sum,
	}
	err := h.service.Withdraw(req.Context(), middlewares.UserID(req.Context()), withdrawal)

	switch {
	case err == nil:
		res.WriteHeader(http.StatusOK)
	case errors.Is(err, models.ErrInsufficientFunds):
		http.Error(res, http.StatusText(http.StatusPaymentRequired), http.StatusPaymentRequired)
	case errors.Is(err, models.ErrInvalidOrder):
		http.Error(res, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case errors.Is(err, models.ErrInvalidAmount):
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	default:
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// без списаний отвечаем 204
func (h *LoyaltyHandler) WithdrawalsHandler(res http.ResponseWriter, req *http.Request) {
	withdrawals, err := h.service.GetWithdrawals(req.Context(), middlewares.UserID(req.Context()))

	if err != nil {
		http.Error(res, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if len(withdrawals) == 0 {
		res.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(res, withdrawals)
}

func readJSON(res http.ResponseWriter, req *http.Request, value interface{}) bool {
	if !hasContentType(req, "application/json") {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return false
	}

	decoder := json.NewDecoder(http.MaxBytesReader(res, req.Body, maxRequestSize))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		http.Error(res, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return false
	}

	return true
}

func hasContentType(req *http.Request, want string) bool {
	contentType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	return err == nil && contentType == want
}

func writeJSON(res http.ResponseWriter, value interface{}) {
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(res).Encode(value)
}
