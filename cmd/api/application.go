package api

import (
	"log/slog"
	"sync"

	handlers "github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/handlers/http"
)

type Application struct {
	Wg       sync.WaitGroup
	Cfg      *Config
	Logger   *slog.Logger
	Handlers *handlers.Handler
}

func NewApplication(cfg *Config, logger *slog.Logger, handler *handlers.Handler) *Application {
	return &Application{
		Cfg:      cfg,
		Logger:   logger,
		Handlers: handler,
	}
}

type Config struct {
	HttpPort int
	GrpcPort int
}
