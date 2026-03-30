package application

import (
	"net/http"

	"tds/pkg/web/handler"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func loadRoutes() *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.Route("/services", loadServiceRoutes)
	return router

}

func loadServiceRoutes(router chi.Router) {
	serviceHandler := &handler.Service{}

	router.Post("/", serviceHandler.Create)
	router.Get("/", serviceHandler.List)
	router.Get("/{id}", serviceHandler.GetById)
	router.Put("/{id}", serviceHandler.UpdateById)
	router.Delete("/{id}", serviceHandler.DeleteById)

}
