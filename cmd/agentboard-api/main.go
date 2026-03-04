package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/markx3/agentboard/internal/api"
	boardpkg "github.com/markx3/agentboard/internal/board"
	"github.com/markx3/agentboard/internal/config"
	"github.com/markx3/agentboard/internal/db"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("agentboard-api: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbPath, err := config.DatabasePath()
	if err != nil {
		return err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", dbPath, err)
	}
	defer database.Close()

	apiKey, err := config.APIKey()
	if err != nil {
		return fmt.Errorf("load API key: %w", err)
	}
	svc := boardpkg.NewLocalService(database)

	addr, err := resolveAddr()
	if err != nil {
		return err
	}

	server := &http.Server{ // rely on chi timeouts internally
		Addr:    addr,
		Handler: api.NewServer(svc, apiKey),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("agentboard API listening on %s", addr)
		log.Printf("requests must include X-API-Key header")
		if err := server.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			if !errors.Is(shutdownErr, context.Canceled) {
				return fmt.Errorf("shutdown server: %w", shutdownErr)
			}
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func resolveAddr() (string, error) {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("invalid PORT %q: %w", port, err)
	}
	return ":" + port, nil
}
