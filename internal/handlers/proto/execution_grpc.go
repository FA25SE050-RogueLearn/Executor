package handlers

import (
	"context"
	"log/slog"

	"github.com/FA25SE050-RogueLearn/RogueLearn.Executor/internal/executor"
	pb "github.com/FA25SE050-RogueLearn/RogueLearn.Executor/protos"
)

type ExecutorServer struct {
	pb.UnimplementedExecutorServiceServer
	executor *executor.Executor
	logger   *slog.Logger
}

const PUBLIC = 1

func NewExecutorGRPCServer(executor *executor.Executor, logger *slog.Logger) *ExecutorServer {
	return &ExecutorServer{
		executor: executor,
		logger:   logger,
	}
}

func (s *ExecutorServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	job := executor.ExecutionJob{
		Language:     req.Language,
		Code:         req.Code,
		DriverCode:   req.DriverCode,
		CompileCmd:   req.CompileCmd,
		RunCmd:       req.RunCmd,
		TempFileDir:  req.TempFileDir,
		TempFileName: req.TempFileName,
		TestCases:    pbTestCaseToTestCase(req.TestCases),
	}

	s.logger.Info("execution job prepared", "job", job)

	result := s.executor.Execute(ctx, job)

	s.logger.Info("execution finished", "success", result.Success, "message", result.Message)

	// convert result to grpc response
	resp := &pb.ExecuteResponse{
		Success:         result.Success,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		Message:         result.Message,
		ErrorType:       string(result.Error),
		ExecutionTimeMs: result.ExecutionTime,
	}

	return resp, nil
}

func pbTestCaseToTestCase(tcs []*pb.TestCase) (result []executor.TestCase) {
	for _, tc := range tcs {
		result = append(result, executor.TestCase{
			Input:         tc.Input,
			ExpectedOuput: tc.ExpectedOutput,
		})
	}
	return nil
}
