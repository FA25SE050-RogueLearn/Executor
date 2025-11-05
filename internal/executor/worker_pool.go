package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
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

var CompileError CodeErr = errors.New("Failed to compile code")
var RunTimeError CodeErr = errors.New("Failed to run code")
var FailTestCase CodeErr = errors.New("Test case failed")

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
	MaxWorkers       int
	MemoryLimitBytes int64
	MaxJobCount      int
	CpuNanoLimit     int64
}

func NewWorkerPool(logger *slog.Logger, opts *WorkerPoolOptions) (*WorkerPool, error) {
	cm, err := NewDockerContainerManager(opts.MaxWorkers, opts.MemoryLimitBytes, opts.CpuNanoLimit)
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

// executeInContainer is a generic function to run a specific shell command in a container
func (w *WorkerPool) executeInContainer(ctx context.Context, containerID, command string, stdin io.Reader) ExecuteCommandResult {
	dockerArgs := []string{"exec", "-i", containerID, "sh", "-c", command}
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	return ExecuteCommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      err,
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

	// Step 2: Run the code
	// If compiled lang -> compiled first.
	if job.Language.CompileCmd != "" { // case Compiled Lang
		// The compile and run commands should already have the correct paths
		compileCmd := strings.ReplaceAll(job.Language.CompileCmd, tempFileDirHolder, job.Language.TempFileDir.String)
		w.logger.Info("Compiling code...", "container_id", containerID, "command", compileCmd)

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
	}

	// Step 4: Run all test cases
	finalRunCmd := strings.ReplaceAll(job.Language.RunCmd, tempFileDirHolder, job.Language.TempFileDir.String)
	finalRunCmd = strings.ReplaceAll(finalRunCmd, tempFileNameHolder, job.Language.TempFileName.String)

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
