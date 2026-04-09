package application

import (
	"net/http"

	"tds/pkg/store"
	"tds/pkg/web/handler"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// loadRoutes builds the HTTP router and wires handlers to the provided store.
func loadRoutes(s store.Store) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Logger)

	// Landing page
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "pkg/web/static/landing.html")
	})

	// Serve static dashboard assets
	fileServer := http.FileServer(http.Dir("pkg/web/static"))
	router.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	router.Get("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "pkg/web/static/index.html")
	})

	// API routes
	svcHandler := &handler.Service{Store: s}
	router.Route("/api/services", func(r chi.Router) {
		r.Post("/", svcHandler.Create)
		r.Get("/", svcHandler.List)
		r.Get("/{task}", svcHandler.GetById)
		r.Get("/{task}/query", svcHandler.Query)
	})
	router.Get("/api/metrics", svcHandler.Metrics)

	return router
}
