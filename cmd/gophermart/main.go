// запускаем сервер и обработку начислений
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kheguy/gophermart/internal/accrual"
	"github.com/kheguy/gophermart/internal/auth"
	"github.com/kheguy/gophermart/internal/config"
	"github.com/kheguy/gophermart/internal/database"
	"github.com/kheguy/gophermart/internal/handler"
	"github.com/kheguy/gophermart/internal/middlewares"
	"github.com/kheguy/gophermart/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.LoadServer()
	// TODO мб тексты вынести
	if err != nil {
		slog.Error("Config loading error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		slog.Error("Server error", "address", cfg.Address, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.ServerConfig) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()

	dbClient, err := database.NewClient(startupCtx, cfg.DBAddress)

	if err != nil {
		return fmt.Errorf("DB initialization error: %w", err)
	}
	defer dbClient.Close()

	storage := dbClient.Storage()
	key, err := storage.GetAuthKey(startupCtx)
	if err != nil {
		return fmt.Errorf("auth key loading error: %w", err)
	}
	tokens, err := auth.NewManager(key)

	if err != nil {
		return fmt.Errorf("auth initialization error: %w", err)
	}
	cancelStartup()

	svc := service.MakeNewLoyaltyService(storage)

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           middlewares.WithLogging(handler.NewRouter(svc, tokens)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	worker := accrual.NewWorker(storage, accrual.NewHTTPClient(cfg.AccrualAddress))

	go func() {
		defer close(workerDone)
		worker.Run(workerCtx)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("Server started", "address", cfg.Address)
		serverErrors <- server.ListenAndServe()
	}()

	var serverErr error
	select {
	case serverErr = <-serverErrors:
	case <-ctx.Done():
		slog.Info("Server shutting down", "address", cfg.Address)
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "address", cfg.Address, "error", err)
			_ = server.Close()
		}
		cancelShutdown()
		serverErr = <-serverErrors
	}

	cancelWorker()
	<-workerDone

	if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
		return serverErr
	}

	slog.Info("Server stopped", "address", cfg.Address)
	return nil
}
