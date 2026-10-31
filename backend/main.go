// Command gradly is the Gradly server: a Gin JSON API with GORM/SQLite storage that also serves the Svelte app.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/config"
	"github.com/younesidk/gradely/internal/mail"
	"github.com/younesidk/gradely/internal/server"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("cannot create the data directory", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}
	db, err := server.OpenDB(cfg.DatabasePath())
	if err != nil {
		slog.Error("cannot open the database", "error", err)
		os.Exit(1)
	}

	app := server.New(cfg, db, mail.NewResend(cfg.ResendAPIKey, cfg.ResendFromEmail))
	if err := app.BootstrapAdmin(); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("Gradly is listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
