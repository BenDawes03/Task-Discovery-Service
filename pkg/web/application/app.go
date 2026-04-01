package application

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"tds/pkg/store"
	pgstore "tds/pkg/store/postgres"
)

type App struct {
	router http.Handler
	Store  store.Store
}

// New initialises the web application and a Postgres-backed store using the
// DATABASE_URL environment variable (falls back to a sensible default).
func New() (*App, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://tds:password@localhost:5432/tds?sslmode=disable"
	}
	fmt.Println(dbURL)
	ps, err := pgstore.NewPostgresStore(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize postgres store: %w", err)
	}

	// Run migrations to ensure required tables exist.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ps.Migrate(ctx); err != nil {
		_ = ps.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	app := &App{
		router: loadRoutes(),
		Store:  ps,
	}

	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	server := &http.Server{
		Addr:    ":8080",
		Handler: a.router,
	}

	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("failed to listen to server: %w", err)
	}

	return nil
}

// Close releases resources held by the app (e.g., database connections).
func (a *App) Close() error {
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}
