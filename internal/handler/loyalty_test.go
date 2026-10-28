package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	models "github.com/kheguy/gophermart/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type serviceStub struct {
	register       func(context.Context, string, string) (models.User, error)
	login          func(context.Context, string, string) (models.User, error)
	uploadOrder    func(context.Context, int64, string) error
	getOrders      func(context.Context, int64) ([]models.Order, error)
	getBalance     func(context.Context, int64) (models.Balance, error)
	withdraw       func(context.Context, int64, models.Withdrawal) error
	getWithdrawals func(context.Context, int64) ([]models.Withdrawal, error)
}

func (s serviceStub) Register(ctx context.Context, login, password string) (models.User, error) {
	return s.register(ctx, login, password)
}

func (s serviceStub) Login(ctx context.Context, login, password string) (models.User, error) {
	return s.login(ctx, login, password)
}

func (s serviceStub) UploadOrder(ctx context.Context, userID int64, number string) error {
	return s.uploadOrder(ctx, userID, number)
}

func (s serviceStub) GetOrders(ctx context.Context, userID int64) ([]models.Order, error) {
	return s.getOrders(ctx, userID)
}

func (s serviceStub) GetBalance(ctx context.Context, userID int64) (models.Balance, error) {
	return s.getBalance(ctx, userID)
}

func (s serviceStub) Withdraw(ctx context.Context, userID int64, withdrawal models.Withdrawal) error {
	return s.withdraw(ctx, userID, withdrawal)
}

func (s serviceStub) GetWithdrawals(ctx context.Context, userID int64) ([]models.Withdrawal, error) {
	return s.getWithdrawals(ctx, userID)
}

type tokenStub struct {
	createError error
}

func (s tokenStub) CreateToken(userID int64) (string, error) {
	return fmt.Sprintf("token-%d", userID), s.createError
}

func (s tokenStub) ParseToken(token string) (int64, error) {
	if token != "token-42" {
		return 0, assert.AnError
	}
	return 42, nil
}

func serveRequest(router http.Handler, method, path, body, contentType string, authorized bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authorized {
		req.Header.Set("Authorization", "Bearer token-42")
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func TestAuthenticationHandlers(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		serviceErr error
		tokenErr   error
		status     int
	}{
		{name: "register", path: "/api/user/register", status: http.StatusOK},
		{name: "register login taken", path: "/api/user/register", serviceErr: fmt.Errorf("register: %w", models.ErrLoginTaken), status: http.StatusConflict},
		{name: "register credentials", path: "/api/user/register", serviceErr: models.ErrInvalidCredentials, status: http.StatusBadRequest},
		{name: "register error", path: "/api/user/register", serviceErr: assert.AnError, status: http.StatusInternalServerError},
		{name: "register token error", path: "/api/user/register", tokenErr: assert.AnError, status: http.StatusInternalServerError},
		{name: "login", path: "/api/user/login", status: http.StatusOK},
		{name: "login credentials", path: "/api/user/login", serviceErr: models.ErrInvalidCredentials, status: http.StatusUnauthorized},
		{name: "login missing user", path: "/api/user/login", serviceErr: models.ErrUserNotFound, status: http.StatusUnauthorized},
		{name: "login error", path: "/api/user/login", serviceErr: assert.AnError, status: http.StatusInternalServerError},
		{name: "login token error", path: "/api/user/login", tokenErr: assert.AnError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			authenticate := func(_ context.Context, login, password string) (models.User, error) {
				called = true
				assert.Equal(t, "alice", login)
				assert.Equal(t, "secret", password)
				return models.User{ID: 42}, tt.serviceErr
			}
			s := serviceStub{register: authenticate, login: authenticate}
			res := serveRequest(NewRouter(s, tokenStub{createError: tt.tokenErr}), http.MethodPost, tt.path,
				`{"login":"alice","password":"secret"}`, "application/json; charset=utf-8", false)
			assert.True(t, called)
			assert.Equal(t, tt.status, res.Code)
			if tt.status != http.StatusOK {
				assert.Empty(t, res.Header().Get("Authorization"))
				assert.Empty(t, res.Result().Cookies())
				assert.NotContains(t, res.Body.String(), assert.AnError.Error())
				return
			}
			assert.Equal(t, "Bearer token-42", res.Header().Get("Authorization"))
			cookies := res.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "session", cookies[0].Name)
			assert.Equal(t, "token-42", cookies[0].Value)
			assert.Equal(t, "/", cookies[0].Path)
			assert.Equal(t, 24*60*60, cookies[0].MaxAge)
			assert.True(t, cookies[0].HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
		})
	}
}

func TestAuthenticationHandlersRejectInvalidBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
	}{
		{name: "empty", body: "", contentType: "application/json"},
		{name: "malformed", body: "{", contentType: "application/json"},
		{name: "null", body: "null", contentType: "application/json"},
		{name: "array", body: "[]", contentType: "application/json"},
		{name: "missing password", body: `{"login":"alice"}`, contentType: "application/json"},
		{name: "blank login", body: `{"login":"  ","password":"secret"}`, contentType: "application/json"},
		{name: "empty password", body: `{"login":"alice","password":""}`, contentType: "application/json"},
		{name: "unknown field", body: `{"login":"alice","password":"secret","role":"admin"}`, contentType: "application/json"},
		{name: "two documents", body: `{"login":"alice","password":"secret"}{}`, contentType: "application/json"},
		{name: "trailing garbage", body: `{"login":"alice","password":"secret"} x`, contentType: "application/json"},
		{name: "wrong type", body: `{"login":42,"password":"secret"}`, contentType: "application/json"},
		{name: "missing content type", body: `{"login":"alice","password":"secret"}`},
		{name: "wrong content type", body: `{"login":"alice","password":"secret"}`, contentType: "text/plain"},
		{name: "oversized", body: `{"login":"alice","password":"` + strings.Repeat("x", maxRequestSize) + `"}`, contentType: "application/json"},
	}
	for _, path := range []string{"/api/user/register", "/api/user/login"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				res := serveRequest(NewRouter(serviceStub{}, tokenStub{}), http.MethodPost, path, tt.body, tt.contentType, false)
				assert.Equal(t, http.StatusBadRequest, res.Code)
			})
		}
	}
}

func TestUploadOrderHandler(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		status     int
	}{
		{name: "accepted", status: http.StatusAccepted},
		{name: "own duplicate", serviceErr: models.ErrOrderExists, status: http.StatusOK},
		{name: "other user", serviceErr: models.ErrOrderConflict, status: http.StatusConflict},
		{name: "invalid number", serviceErr: models.ErrInvalidOrder, status: http.StatusUnprocessableEntity},
		{name: "failure", serviceErr: assert.AnError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			s := serviceStub{uploadOrder: func(_ context.Context, userID int64, number string) error {
				called = true
				assert.EqualValues(t, 42, userID)
				assert.Equal(t, "12345678903", number)
				return tt.serviceErr
			}}
			res := serveRequest(NewRouter(s, tokenStub{}), http.MethodPost, "/api/user/orders", "12345678903\n", "text/plain", true)
			assert.True(t, called)
			assert.Equal(t, tt.status, res.Code)
		})
	}
}

func TestUploadOrderHandlerRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		body        string
		contentType string
	}{
		{body: "", contentType: "text/plain"},
		{body: " \n", contentType: "text/plain"},
		{body: "12345678903", contentType: "application/json"},
		{body: "12345678903"},
		{body: strings.Repeat("0", maxRequestSize+1), contentType: "text/plain"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			res := serveRequest(NewRouter(serviceStub{}, tokenStub{}), http.MethodPost, "/api/user/orders", tt.body, tt.contentType, true)
			assert.Equal(t, http.StatusBadRequest, res.Code)
		})
	}
}

func TestOrdersHandler(t *testing.T) {
	date := time.Date(2020, 12, 10, 15, 15, 45, 0, time.FixedZone("MSK", 3*60*60))
	tests := []struct {
		name   string
		orders []models.Order
		err    error
		status int
	}{
		{name: "success", orders: []models.Order{{Number: "12345678903", UserID: 42, Status: models.Processed, Accrual: "500.50", UploadedAt: date}}, status: http.StatusOK},
		{name: "empty", status: http.StatusNoContent},
		{name: "failure", err: assert.AnError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			s := serviceStub{getOrders: func(_ context.Context, userID int64) ([]models.Order, error) {
				called = true
				assert.EqualValues(t, 42, userID)
				return tt.orders, tt.err
			}}
			res := serveRequest(NewRouter(s, tokenStub{}), http.MethodGet, "/api/user/orders", "", "", true)
			assert.True(t, called)
			require.Equal(t, tt.status, res.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, "application/json", res.Header().Get("Content-Type"))
				assert.JSONEq(t, `[{"number":"12345678903","status":"PROCESSED","accrual":500.5,"uploaded_at":"2020-12-10T15:15:45+03:00"}]`, res.Body.String())
			} else if tt.status == http.StatusNoContent {
				assert.Empty(t, res.Body.String())
			} else {
				assert.NotContains(t, res.Body.String(), assert.AnError.Error())
			}
		})
	}
}

func TestBalanceHandler(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "success", status: http.StatusOK},
		{name: "failure", err: assert.AnError, status: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			s := serviceStub{getBalance: func(_ context.Context, userID int64) (models.Balance, error) {
				called = true
				assert.EqualValues(t, 42, userID)
				return models.Balance{Current: "500.50", Withdrawn: "42.00"}, tt.err
			}}
			res := serveRequest(NewRouter(s, tokenStub{}), http.MethodGet, "/api/user/balance", "", "", true)
			assert.True(t, called)
			require.Equal(t, tt.status, res.Code)
			if tt.err != nil {
				assert.NotContains(t, res.Body.String(), assert.AnError.Error())
				return
			}
			assert.Equal(t, "application/json", res.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"current":500.5,"withdrawn":42}`, res.Body.String())
		})
	}
}

func TestWithdrawalsHandler(t *testing.T) {
	date := time.Date(2020, 12, 10, 15, 15, 45, 0, time.FixedZone("MSK", 3*60*60))
	tests := []struct {
		name        string
		withdrawals []models.Withdrawal
		err         error
		status      int
	}{
		{name: "success", withdrawals: []models.Withdrawal{{Order: "2377225624", Sum: "500.50", ProcessedAt: date}}, status: http.StatusOK},
		{name: "empty", status: http.StatusNoContent},
		{name: "failure", err: assert.AnError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			s := serviceStub{getWithdrawals: func(_ context.Context, userID int64) ([]models.Withdrawal, error) {
				called = true
				assert.EqualValues(t, 42, userID)
				return tt.withdrawals, tt.err
			}}
			res := serveRequest(NewRouter(s, tokenStub{}), http.MethodGet, "/api/user/withdrawals", "", "", true)
			assert.True(t, called)
			require.Equal(t, tt.status, res.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, "application/json", res.Header().Get("Content-Type"))
				assert.JSONEq(t, `[{"order":"2377225624","sum":500.5,"processed_at":"2020-12-10T15:15:45+03:00"}]`, res.Body.String())
			} else if tt.status == http.StatusNoContent {
				assert.Empty(t, res.Body.String())
			} else {
				assert.NotContains(t, res.Body.String(), assert.AnError.Error())
			}
		})
	}
}

func TestWithdrawHandler(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		status     int
	}{
		{name: "success", status: http.StatusOK},
		{name: "insufficient funds", serviceErr: models.ErrInsufficientFunds, status: http.StatusPaymentRequired},
		{name: "invalid order", serviceErr: models.ErrInvalidOrder, status: http.StatusUnprocessableEntity},
		{name: "invalid amount", serviceErr: models.ErrInvalidAmount, status: http.StatusBadRequest},
		{name: "failure", serviceErr: assert.AnError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			s := serviceStub{withdraw: func(_ context.Context, userID int64, withdrawal models.Withdrawal) error {
				called = true
				assert.EqualValues(t, 42, userID)
				assert.Equal(t, models.Withdrawal{Order: "2377225624", Sum: "751.25"}, withdrawal)
				return tt.serviceErr
			}}
			res := serveRequest(NewRouter(s, tokenStub{}), http.MethodPost, "/api/user/balance/withdraw", `{"order":"2377225624","sum":751.25}`, "application/json", true)
			assert.True(t, called)
			assert.Equal(t, tt.status, res.Code)
		})
	}
}

func TestWithdrawHandlerRejectsInvalidBody(t *testing.T) {
	for _, body := range []string{
		`{`, `null`, `{}`, `{"order":"2377225624"}`,
		`{"order":"2377225624","sum":0}`, `{"order":"2377225624","sum":-1}`,
		`{"order":"2377225624","sum":"751"}`,
		`{"order":"2377225624","sum":null}`, `{"order":"2377225624","sum":1}{}`,
		`{"order":"2377225624","sum":1,"processed_at":"2020-12-10T15:15:45Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			res := serveRequest(NewRouter(serviceStub{}, tokenStub{}), http.MethodPost, "/api/user/balance/withdraw", body, "application/json", true)
			assert.Equal(t, http.StatusBadRequest, res.Code)
		})
	}
}

func TestRouterProtectsAccountEndpoints(t *testing.T) {
	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/user/orders"},
		{http.MethodGet, "/api/user/orders"},
		{http.MethodGet, "/api/user/balance"},
		{http.MethodPost, "/api/user/balance/withdraw"},
		{http.MethodGet, "/api/user/withdrawals"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			res := serveRequest(NewRouter(serviceStub{}, tokenStub{}), endpoint.method, endpoint.path, "", "", false)
			assert.Equal(t, http.StatusUnauthorized, res.Code)
		})
	}
}

func TestWithdrawHandlerFractionalPoints(t *testing.T) {
	s := serviceStub{withdraw: func(_ context.Context, userID int64, withdrawal models.Withdrawal) error {
		assert.Equal(t, int64(42), userID)
		assert.Equal(t, models.Money("0.001"), withdrawal.Sum)
		return nil
	}}
	res := serveRequest(NewRouter(s, tokenStub{}), http.MethodPost, "/api/user/balance/withdraw",
		`{"order":"2377225624","sum":0.001}`, "application/json", true)
	assert.Equal(t, http.StatusOK, res.Code)
}

func TestOrdersHandlerOmitsZeroAccrual(t *testing.T) {
	uploadedAt := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	orders := []models.Order{
		{Number: "1", Status: models.New, Accrual: "0", UploadedAt: uploadedAt},
		{Number: "2", Status: models.Processing, Accrual: "0.000", UploadedAt: uploadedAt},
		{Number: "3", Status: models.Invalid, Accrual: "-0.0", UploadedAt: uploadedAt},
		{Number: "4", Status: models.Processed, Accrual: "0e10", UploadedAt: uploadedAt},
		{Number: "5", Status: models.Processed, Accrual: "0.001", UploadedAt: uploadedAt},
	}
	s := serviceStub{getOrders: func(_ context.Context, userID int64) ([]models.Order, error) {
		assert.Equal(t, int64(42), userID)
		return orders, nil
	}}
	res := serveRequest(NewRouter(s, tokenStub{}), http.MethodGet, "/api/user/orders", "", "", true)

	require.Equal(t, http.StatusOK, res.Code)
	assert.JSONEq(t, `[
		{"number":"1","status":"NEW","uploaded_at":"2026-10-27T12:00:00Z"},
		{"number":"2","status":"PROCESSING","uploaded_at":"2026-10-27T12:00:00Z"},
		{"number":"3","status":"INVALID","uploaded_at":"2026-10-27T12:00:00Z"},
		{"number":"4","status":"PROCESSED","uploaded_at":"2026-10-27T12:00:00Z"},
		{"number":"5","status":"PROCESSED","accrual":0.001,"uploaded_at":"2026-10-27T12:00:00Z"}
	]`, res.Body.String())
}
