package handlers

import (
	"net/http"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/pkg/response"
)

func (h *Handler) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	err := response.JSON(w, response.JSONResponseParameters{
		Status:  http.StatusOK,
		Data:    "OK",
		Success: true,
		Msg:     "OK",
	})

	if err != nil {
		h.serverError(w, r, err)
	}
}
