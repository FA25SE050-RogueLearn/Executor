package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	QueryTimeOutSecond   = 30 * time.Second
	CodeRunTimeOutSecond = 30 * time.Second
)

// InternalTestCase represents a test case for internal use
type InternalTestCase struct {
	ID             int32
	Input          string
	ExpectedOutput string
}

type Job struct {
	Language  Language
	Code      string
	TestCases []InternalTestCase // we will run all test cases in a job
	Result    chan Result
}

type CodeErr error

var CompileError CodeErr = errors.New("COMPILE_ERROR")
var RunTimeError CodeErr = errors.New("RUNTIME_ERROR")
var FailTestCase CodeErr = errors.New("WRONG_ANSWER")

type Result struct {
	Output        string
	Success       bool
	Error         CodeErr
	ExecutionTime string
}

type ExecuteCommandResult struct {
	Stdout   string
	Stderr   string
	Err      error
	Duration time.Duration
}

type WorkerPool struct {
	cm           *DockerContainerManager
	logger       *slog.Logger
	jobs         chan Job
	wg           sync.WaitGroup
	shutdownChan chan any
}

type WorkerPoolOptions struct {
	MaxWorkers    int
	MemoryLimitMB int64
	MaxJobCount   int
	CpuNanoLimit  int64
}

func NewWorkerPool(logger *slog.Logger, opts *WorkerPoolOptions) (*WorkerPool, error) {
	cm, err := NewDockerContainerManager(opts.MaxWorkers, opts.MemoryLimitMB, opts.CpuNanoLimit)
	if err != nil {
		return nil, err
	}

	err = cm.InitializePool()
	if err != nil {
		return nil, err
	}

	w := &WorkerPool{
		cm:           cm,
		logger:       logger,
		jobs:         make(chan Job, opts.MaxJobCount),
		shutdownChan: make(chan any),
	}

	for i := range opts.MaxWorkers {
		w.wg.Add(1)
		go w.worker(i + 1)
	}

	w.logger.Info("Initialized worker pool with max workers",
		"max_worker", w.cm.maxWorkers)

	return w, err
}

func (w *WorkerPool) worker(id int) {
	defer w.wg.Done()
	w.logger.Info("Worker started", "id", id)

	for {
		select {
		case j, ok := <-w.jobs:
			if !ok {
				w.logger.Info("Worker shutting down due to channel closed",
					"worker_id", id)
				return
			}
			w.executeJob(id, j)

		case <-w.shutdownChan:
			w.logger.Info("Worker received shutdown signal", "worker_id", id)
			return
		}
	}
}

// ExecuteJob submits the job for execution
// input as a pointer so we could either set it or make it null
func (w *WorkerPool) ExecuteJob(lang Language, code string, tcs []InternalTestCase) Result {
	w.logger.Info("Submitting job...",
		"language", lang.Name)

	result := make(chan Result, 1)
	select {
	case w.jobs <- Job{Language: lang, Code: code, TestCases: tcs, Result: result}:
		return <-result
	default:
		w.logger.Warn("Job queue is full, rejecting job...",
			"language", lang.Name,
			"maxJobCount", w.cm.maxWorkers)
		return Result{Success: false, Error: errors.New("job queue is full")}
	}
}

// executeInContainer is a generic function to run a specific shell command in a container using Docker SDK
func (w *WorkerPool) executeInContainer(ctx context.Context, containerID, command string, stdin io.Reader) ExecuteCommandResult {
	start := time.Now()

	// Create exec configuration
	execConfig := container.ExecOptions{
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"sh", "-c", command},
	}

	// Create the exec instance
	execIDResp, err := w.cm.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return ExecuteCommandResult{
			Stdout:   "",
			Stderr:   err.Error(),
			Err:      err,
			Duration: time.Since(start),
		}
	}

	// Attach to the exec instance
	attachResp, err := w.cm.cli.ContainerExecAttach(ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return ExecuteCommandResult{
			Stdout:   "",
			Stderr:   err.Error(),
			Err:      err,
			Duration: time.Since(start),
		}
	}
	defer attachResp.Close()

	// Handle stdin if provided
	if stdin != nil {
		go func() {
			defer attachResp.CloseWrite()
			io.Copy(attachResp.Conn, stdin)
		}()
	}

	// Read stdout and stderr
	var stdout, stderr bytes.Buffer
	outputDone := make(chan error, 1)
	go func() {
		// Docker multiplexes stdout and stderr in the response
		// Use stdcopy to properly demultiplex the streams
		_, err := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
		outputDone <- err
	}()

	// Wait for output to complete or context to cancel
	select {
	case <-ctx.Done():
		return ExecuteCommandResult{
			Stdout:   stdout.String(),
			Stderr:   "Execution timeout",
			Err:      ctx.Err(),
			Duration: time.Since(start),
		}
	case <-outputDone:
		// Continue to check exit code
	}

	// Check the exit code
	inspectResp, err := w.cm.cli.ContainerExecInspect(ctx, execIDResp.ID)
	if err != nil {
		return ExecuteCommandResult{
			Stdout:   stdout.String(),
			Stderr:   err.Error(),
			Err:      err,
			Duration: time.Since(start),
		}
	}

	duration := time.Since(start)

	// If exit code is non-zero, return an error
	var execErr error
	if inspectResp.ExitCode != 0 {
		execErr = fmt.Errorf("command exited with code %d", inspectResp.ExitCode)
	}

	return ExecuteCommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      execErr,
		Duration: duration,
	}
}

// executeJob handle the execution of a single job
func (w *WorkerPool) executeJob(workerID int, job Job) error {
	ctx, cancel := context.WithTimeout(context.Background(), CodeRunTimeOutSecond)
	defer cancel()

	w.logger.Info("Job has been picked",
		"worker_id", workerID,
		"job", job)

	containerID, err := w.cm.GetAvailableContainer()
	if err != nil {
		w.logger.Error("Failed to get available Container",
			"err", err)
		return err
	}

	err = w.cm.SetContainerState(containerID, StateBusy)
	if err != nil {
		return err
	}

	defer func() {
		err = w.cm.SetContainerState(containerID, StateIdle)
		if err != nil {
			w.logger.Error("failed to set container state to idle",
				"container_id", containerID,
				"err", err)
		}
	}()

	start := time.Now()

	// Step 1: Copy code to container filesystem
	err = w.cm.copyCodeToContainer(ctx, containerID, job.Language.TempFileDir.String, job.Language.TempFileName.String, []byte(job.Code))
	if err != nil {
		w.logger.Error("Failed to copy code to container", "err", err)
		job.Result <- Result{Error: err, Success: false, Output: "Failed to set up execution environment."}
		return err
	}

	w.logger.Info("Code copied to container",
		"container_id", containerID,
		"code", job.Code)

	// Step 2: Compile once (if needed)
	var finalRunCmd string
	var compiledBinaryPath string

	if job.Language.CompileCmd != "" { // case Compiled Lang
		// Generate unique binary path to avoid conflicts between concurrent jobs
		compiledBinaryPath = fmt.Sprintf("/tmp/prog_%s_%d", containerID[:12], time.Now().UnixNano())

		// Replace placeholders in compile command and add output path
		compileCmd := strings.ReplaceAll(job.Language.CompileCmd, tempFileDirHolder, job.Language.TempFileDir.String)
		compileCmd = strings.ReplaceAll(compileCmd, tempFileNameHolder, job.Language.TempFileName.String)

		// If the compile command has an output flag (-o), replace it with our unique path
		// Otherwise append it
		if strings.Contains(compileCmd, " -o ") {
			// Replace existing output path
			parts := strings.Split(compileCmd, " -o ")
			if len(parts) >= 2 {
				// Find where the old output path ends (next space or end of string)
				oldPathParts := strings.Fields(parts[1])
				if len(oldPathParts) > 0 {
					// Replace the old path with new one, keep rest of command
					restOfCmd := strings.Join(oldPathParts[1:], " ")
					if restOfCmd != "" {
						compileCmd = parts[0] + " -o " + compiledBinaryPath + " " + restOfCmd
					} else {
						compileCmd = parts[0] + " -o " + compiledBinaryPath
					}
				}
			}
		} else {
			// No -o flag exists, append it
			compileCmd = compileCmd + " -o " + compiledBinaryPath
		}

		w.logger.Info("Compiling code...", "container_id", containerID, "command", compileCmd, "binary_path", compiledBinaryPath)

		compileResult := w.executeInContainer(ctx, containerID, compileCmd, nil)
		if compileResult.Err != nil {
			w.logger.Warn("Compilation failed",
				"err", compileResult.Err,
				"stderr", compileResult.Stderr,
				"stdout", compileResult.Stdout)

			errorMsg := fmt.Sprintf("Compilation Failed\n\nCommand:\n%s\n\nError Output:\n%s",
				compileCmd,
				compileResult.Stderr)

			job.Result <- Result{Error: CompileError, Success: false, Output: errorMsg}
			return err
		}
		w.logger.Info("Compilation successful", "duration", compileResult.Duration.Milliseconds())

		// Use the compiled binary for all test cases
		finalRunCmd = compiledBinaryPath
	} else {
		// Interpreted language - use run command as-is
		finalRunCmd = strings.ReplaceAll(job.Language.RunCmd, tempFileDirHolder, job.Language.TempFileDir.String)
		finalRunCmd = strings.ReplaceAll(finalRunCmd, tempFileNameHolder, job.Language.TempFileName.String)
	}

	// Cleanup compiled binary after all test cases
	if compiledBinaryPath != "" {
		defer func() {
			cleanupCmd := fmt.Sprintf("rm -f %s", compiledBinaryPath)
			w.executeInContainer(context.Background(), containerID, cleanupCmd, nil)
			w.logger.Info("Cleaned up compiled binary", "binary_path", compiledBinaryPath)
		}()
	}

	// Step 3: Run all test cases using the same compiled binary
	w.logger.Info("Preparing to run test cases", "command", finalRunCmd, "count", len(job.TestCases))

	totalExecutionTime := int64(0)
	for _, tc := range job.TestCases {
		runCtx, runCancel := context.WithTimeout(ctx, CodeRunTimeOutSecond)

		runResult := w.executeInContainer(runCtx, containerID, finalRunCmd, strings.NewReader(tc.Input))
		totalExecutionTime += runResult.Duration.Milliseconds()

		if runResult.Err != nil {
			w.logger.Warn("Runtime error", "test_case_id", tc.ID, "err", runResult.Err, "stderr", runResult.Stderr)

			errorMsg := fmt.Sprintf("Runtime Error on Test Case #%d\n\nInput:\n%s\n\nError Output:\n%s",
				tc.ID,
				tc.Input,
				runResult.Stderr)

			job.Result <- Result{Error: RunTimeError, Success: false, Output: errorMsg}
			runCancel()
			return runResult.Err
		}

		actualOutput := strings.TrimSpace(runResult.Stdout)
		w.logger.Info("Test case executed", "actual_output", actualOutput)
		expectedOutput := strings.TrimSpace(tc.ExpectedOutput)
		w.logger.Info("Test case executed", "expected_output", expectedOutput)

		if actualOutput != expectedOutput {
			w.logger.Warn("Wrong answer",
				"test_case_id", tc.ID,
				"actual_output", actualOutput,
				"expected_output", expectedOutput,
			)

			errorMsg := fmt.Sprintf("Wrong Answer on Test Case #%d\n\nInput:\n%s\n\nExpected Output:\n%s\n\nYour Output:\n%s",
				tc.ID,
				tc.Input,
				expectedOutput,
				actualOutput)

			job.Result <- Result{Success: false, Output: errorMsg, Error: FailTestCase}
			runCancel()
			return FailTestCase
		}

		runCancel()
	}

	duration := time.Since(start)
	w.logger.Info("full process done", "took", duration)

	// Step 4: Send Result
	w.logger.Info("All test cases passed!", "worker_id", workerID)
	job.Result <- Result{
		Success:       true,
		Output:        "All test cases passed!",
		ExecutionTime: fmt.Sprintf("%dms", totalExecutionTime),
	}

	if err != nil {
		w.logger.Error("Worker job failed",
			"worker_id", workerID,
			"container_id", containerID,
			"duration", duration.Milliseconds(),
			"lang", job.Language,
			"err", err)
	} else {
		w.logger.Info("Worker job completed",
			"worker_id", workerID,
			"container_id", containerID,
			"duration", duration.Milliseconds(),
			"lang", job.Language)
	}

	return nil
}

// SetContainerState set the status of a specific Container
func (d *DockerContainerManager) SetContainerState(containerID, state container.ContainerState) error {
	c, exists := d.containers[containerID]
	if !exists {
		d.logger.Error("Failed to find Container",
			"container_id", containerID)
		return ErrContainerNotFound
	}

	c.State = state
	d.logger.Info("Container state is set",
		"container_id", containerID,
		"state", state)

	return nil
}

// Shutdown gracefully shuts down the worker pool
func (w *WorkerPool) Shutdown() {
	w.logger.Info("Shutting down worker pool...")

	// Close the job channel
	close(w.jobs)

	// Signal all workers to shut down
	close(w.shutdownChan)

	// Wait for all workers to finish
	w.wg.Wait()

	// Shutdown container manager
	w.cm.ShutDown()

	w.logger.Info("Worker pool shutdown complete")
}
