package executor

import (
	"context"
	"database/sql"
	"log/slog"
)

// TestCase represents a single test case with input and expected output
type TestCase struct {
	ID            int32
	Input         string
	ExpectedOuput string // keeping the typo for compatibility with handlers
}

// ExecutionJob represents a code execution request
type ExecutionJob struct {
	Language     string
	Code         string
	DriverCode   string
	CompileCmd   string
	RunCmd       string
	TempFileDir  string
	TempFileName string
	TestCases    []TestCase
}

// ExecutionResult represents the result of code execution
type ExecutionResult struct {
	Success       bool    `json:"success"`
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	Message       string  `json:"message"`
	Error         CodeErr `json:"error"`
	ExecutionTime string  `json:"execution_time"`
}

// Language represents a programming language configuration
type Language struct {
	Name         string
	CompileCmd   string
	RunCmd       string
	TempFileDir  sql.NullString
	TempFileName sql.NullString
}

// Executor manages code execution using a worker pool
type Executor struct {
	logger      *slog.Logger
	workerPool  *WorkerPool
	codeBuilder CodeBuilder
}

// NewExecutor creates a new Executor instance
func NewExecutor(logger *slog.Logger, workerPool *WorkerPool) *Executor {
	// Can add more in the future
	pkgAnalyzers := []PackageAnalyzer{
		NewGoPackageAnalyzer(),
	}

	return &Executor{
		logger:      logger,
		workerPool:  workerPool,
		codeBuilder: NewCodeBuilder(pkgAnalyzers, logger),
	}
}

// Execute runs the provided code execution job and returns the result
func (e *Executor) Execute(ctx context.Context, job ExecutionJob) ExecutionResult {
	e.logger.Info("executing job",
		"language", job.Language,
		"test_cases", len(job.TestCases))

	// Convert ExecutionJob to internal Job format
	internalTestCases := make([]InternalTestCase, len(job.TestCases))
	for i, tc := range job.TestCases {
		internalTestCases[i] = InternalTestCase{
			ID:             tc.ID,
			Input:          tc.Input,
			ExpectedOutput: tc.ExpectedOuput,
		}
	}

	internalLang := Language{
		Name:       job.Language,
		CompileCmd: job.CompileCmd,
		RunCmd:     job.RunCmd,
		TempFileDir: sql.NullString{
			String: job.TempFileDir,
			Valid:  job.TempFileDir != "",
		},
		TempFileName: sql.NullString{
			String: job.TempFileName,
			Valid:  job.TempFileName != "",
		},
	}
	finalCode, err := e.codeBuilder.Build(job.Language, job.DriverCode, job.Code)
	if err == ErrParsed {
		e.logger.Warn("Wrong syntax")
		return ExecutionResult{
			Success: false,
			Message: "Failed to analyze code",
			Error:   CompileError,
		}
	}
	// Execute the job using the worker pool
	result := e.workerPool.ExecuteJob(internalLang, finalCode, internalTestCases)

	// Convert internal Result to ExecutionResult
	executionResult := ExecutionResult{
		Success:       result.Success,
		Stdout:        result.Output,
		Stderr:        "",
		Message:       result.Output,
		ExecutionTime: result.ExecutionTime,
	}

	if result.Error != nil {
		executionResult.Error = result.Error
		executionResult.Stderr = result.Output
	}

	e.logger.Info("execution completed",
		"success", executionResult.Success,
		"execution_time", executionResult.ExecutionTime)

	return executionResult
}
