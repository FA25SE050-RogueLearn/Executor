package service

import (
	"context"
	"log/slog"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/executor"
	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/protos"
)

type ExecutorServer struct {
	protos.UnimplementedExecutorServiceServer
	logger *slog.Logger
	engine *executor.Executor
}

func NewExecutorServer(logger *slog.Logger, engine *executor.Executor) *ExecutorServer {
	return &ExecutorServer{
		logger: logger,
		engine: engine,
	}
}

func (s *ExecutorServer) ExecuteAndValidate(ctx context.Context, req *protos.ExecuteRequest) (*protos.ExecuteResponse, error) {
	s.logger.Info("received execution request", "language", req.Language)

	tcs := make([]executor.TestCase, len(req.TestCases))
	for i, tc := range req.TestCases {
		tcs[i] = executor.TestCase{
			Input:         tc.Input,
			ExpectedOuput: tc.ExpectedOutput,
		}
	}

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

	result := s.engine.Execute(ctx, job)

	return &protos.ExecuteResponse{
		Success:         result.Success,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		Message:         result.Message,
		ErrorType:       string(result.Error),
		ExecutionTimeMs: result.ExecutionTime,
	}, nil
}
