// настройки для запуска без перекомпиляции
package config

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/caarlos0/env/v11"
)

// три адреса для запуска сервиса
type ServerConfig struct {
	// адрес с портом для входящих запросов
	Address string `env:"RUN_ADDRESS"`
	// без БД сервис не запустится
	DBAddress string `env:"DATABASE_URI"`
	// только HTTP или HTTPS
	AccrualAddress string `env:"ACCRUAL_SYSTEM_ADDRESS"`
}

// переменные окружения перекрывают флаги
func LoadServer() (ServerConfig, error) {
	cfg := ServerConfig{
		Address:        "localhost:8080",
		AccrualAddress: "http://localhost:8081",
	}

	flags := flag.NewFlagSet("gophermart", flag.ContinueOnError)
	flags.StringVar(&cfg.Address, "a", cfg.Address, "Address of the server")
	flags.StringVar(&cfg.DBAddress, "d", cfg.DBAddress, "Database address")
	flags.StringVar(&cfg.AccrualAddress, "r", cfg.AccrualAddress, "Accrual system address")

	if err := flags.Parse(os.Args[1:]); err != nil {
		return ServerConfig{}, err
	}
	if err := env.Parse(&cfg); err != nil {
		return ServerConfig{}, err
	}

	if cfg.DBAddress == "" {
		return ServerConfig{}, fmt.Errorf("database address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return ServerConfig{}, fmt.Errorf("invalid server address: %w", err)
	}

	u, err := url.Parse(cfg.AccrualAddress)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("invalid accrual system address: %w", err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ServerConfig{}, fmt.Errorf("invalid accrual system address")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ServerConfig{}, fmt.Errorf("invalid accrual system address")
	}

	return cfg, nil
}
