package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

func (app *Application) routes() http.Handler {
	mux := chi.NewRouter()

	mux.Use(cors.AllowAll().Handler)

	mux.Route("/healthcheck", func(r chi.Router) {
		r.Get("/", app.Handlers.HealthCheckHandler)
	})

	mux.Route("/execution", func(r chi.Router) {
		r.Post("/", app.Handlers.ExecuteHandler)
	})
	return mux
}
