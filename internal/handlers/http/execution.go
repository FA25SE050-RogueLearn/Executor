package handlers

import (
	"net/http"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/executor"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/pkg/request"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/pkg/response"
)

type jsonTestCase struct {
	Input          string `json:"input"`
	ExpectedOutput string `json:"expected_output"`
}

// ExecuteRequest defines the expected JSON body for the /execute endpoint
type ExecuteRequest struct {
	Language     string         `json:"language"`
	Code         string         `json:"code"`
	DriverCode   string         `json:"driver_code"`
	CompileCmd   string         `json:"compile_cmd"`
	RunCmd       string         `json:"run_cmd"`
	TempFileDir  string         `json:"temp_file_dir"`
	TempFileName string         `json:"temp_file_name"`
	TestCases    []jsonTestCase `json:"test_cases"`
}

// ExecuteHandler handles the incoming HTTP request for code execution
func (h *Handler) ExecuteHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Decode the incoming JSON request
	var req ExecuteRequest
	err := request.DecodeJSON(w, r, &req)
	if err != nil {
		h.badRequest(w, r, err)
	}

	// 2. Map the JSON test cases to the executor's TestCase struct
	tcs := make([]executor.TestCase, len(req.TestCases))
	for i, tc := range req.TestCases {
		tcs[i] = executor.TestCase{
			Input:         tc.Input,
			ExpectedOuput: tc.ExpectedOutput,
		}
	}

	// 3. Create the ExecutionJob for the executor
	job := executor.ExecutionJob{
		Language:     req.Language,
		Code:         req.Code,
		DriverCode:   req.DriverCode,
		CompileCmd:   req.CompileCmd,
		RunCmd:       req.RunCmd,
		TempFileDir:  req.TempFileDir,
		TempFileName: req.TempFileName,
		TestCases:    tcs,
	}

	h.logger.Info("received http execution request", "language", req.Language)

	// 4. Call the executor
	// We pass the request context, which will handle timeouts or cancellations
	result := h.executor.Execute(r.Context(), job)

	h.logger.Info("execution finished", "success", result.Success, "message", result.Message)

	// 5. Return the result as JSON
	// The executor.Execute function returns a result, not an error (it packages errors
	// inside the ExecutionResult). So, we almost always return 200 OK.
	err = response.JSON(w, response.JSONResponseParameters{
		Status:  http.StatusOK,
		Data:    result,
		Success: true,
		Msg:     "executed successfuly",
	})
	if err != nil {
		h.serverError(w, r, err)
	}
}
