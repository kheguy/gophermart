// данные пользователей и заказов
package models

import (
	"errors"
	"time"
)

const (
	// только загрузили
	New = "NEW"
	// ещё считают
	Processing = "PROCESSING"
	// заказ не приняли
	Invalid = "INVALID"
	// расчёт окончен
	Processed = "PROCESSED"
	// заказ знают но пока не считают
	Registered = "REGISTERED"
)

var (
	// логин занят
	ErrLoginTaken = errors.New("login already taken")
	// такого пользователя нет
	ErrUserNotFound = errors.New("user not found")
	// не подошли данные входа
	ErrInvalidCredentials = errors.New("invalid credentials")
	// Загаз уже загружался
	ErrOrderExists = errors.New("order already uploaded")
	// заказ чужой
	ErrOrderConflict = errors.New("order uploaded by another user")
	// неверный номер или статус
	ErrInvalidOrder = errors.New("invalid order number")
	// сумма не подходит для операции
	ErrInvalidAmount = errors.New("invalid withdrawal amount")
	// не хватает баллов
	ErrInsufficientFunds = errors.New("insufficient funds")
)

// одна пара для регистрации и входа
type Credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// хеш и ID наружу не отдаём
type User struct {
	ID           int64  `json:"-"`
	Login        string `json:"login"`
	PasswordHash string `json:"-"`
}

// нулевое начисление в ответ не идёт
type Order struct {
	Number     string    `json:"number"`
	UserID     int64     `json:"-"`
	Status     string    `json:"status"`
	Accrual    Money     `json:"accrual,omitzero"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// сколько есть и сколько потрачено всего
type Balance struct {
	Current   Money `json:"current"`
	Withdrawn Money `json:"withdrawn"`
}

// оплата заказа накопленными баллами
type Withdrawal struct {
	Order       string    `json:"order"`
	Sum         Money     `json:"sum"`
	ProcessedAt time.Time `json:"processed_at,omitempty"`
}
