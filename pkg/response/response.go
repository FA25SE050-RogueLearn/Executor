package response

import (
	"encoding/json"
	"net/http"
)

type JsonResponse struct {
	Success      bool   `json:"success"`
	Data         any    `json:"data"`
	Message      string `json:"message"`
	ErrorMessage string `json:"error_message"`
}

type JSONResponseParameters struct {
	Status  int
	Data    any
	Success bool
	Msg     string
	ErrMsg  string
}

func JSON(w http.ResponseWriter, params JSONResponseParameters) error {
	return JSONWithHeaders(w, params, nil)
}

func JSONWithHeaders(w http.ResponseWriter, params JSONResponseParameters, headers http.Header) error {
	for key, value := range headers {
		w.Header()[key] = value
	}

	response := &JsonResponse{
		Success:      params.Success,
		Message:      params.Msg,
		ErrorMessage: params.ErrMsg,
		Data:         params.Data,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(params.Status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return err
	}

	return nil
}
