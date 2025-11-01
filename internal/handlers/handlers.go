package handlers

import (
	"log/slog"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/executor"
)

type Handler struct {
	logger   *slog.Logger
	executor *executor.Executor
}

func NewHandler(logger *slog.Logger, executor *executor.Executor) *Handler {
	return &Handler{
		logger:   logger,
		executor: executor,
	}
}
